package lobbylogic

import (
	"encoding/binary"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"slices"
	"strconv"
	"strings"
	"sync"

	"owserver/inst/jam"
)

// Player identity: the local player's BattleTag and player icon.
//
// Where the client shows them:
//   - Title screen party panel (UXPartyScreen, FUN_7ff650d85700): for every 20700 party
//     member, the name before "#" (-> UI var 0xfe11d138) and the icon member +0x20 looked
//     up with FUN_7ff650d52060 (-> 0x11db7719). An unknown icon makes the panel rebuild
//     every frame. [G]
//   - ClientInLobbyPlayerProgression 24300 A.u64 -> profile +0x150 (equipped icon) and
//     A.list -> +0x158 (owned account unlocks; all assets loaded into +0x188). [G]
//   - ClientInLobbyConnect 20500's string -> profile +0x70: the social panel header and
//     the name on the player's own chat lines (both without "#..."). Re-sending 20500
//     mid-session updates them. [G][H]
//
// The identity is one per lobbylogic process (one user) and is kept in a JSON file
// (LOGIC_IDENTITY, default lobby-identity.json in the working directory) so that
// restarts on .go edits keep it. !name and !icon change it live.

// Identity is the local player's name and icon.
type Identity struct {
	Name string // BattleTag, "Name#1234"
	Icon uint64 // player icon unlock (STUUnlock_AvatarPortrait, 0x025...)
}

// identityJSON is the file form; the icon is a GUID in hex or an icon name ("Tracer").
type identityJSON struct {
	Name string `json:"name"`
	Icon string `json:"icon"`
}

// The 23 player icons 0.8 ships (content reader: STUUnlock_AvatarPortrait m_name).
var iconNames = map[uint64]string{
	0x02500000000002F7: "Overwatch Logo", 0x02500000000002F9: "Logo 2",
	0x0250000000000304: "McCree", 0x0250000000000305: "Mercy", 0x0250000000000306: "Tracer",
	0x0250000000000307: "Reaper", 0x0250000000000308: "Widowmaker", 0x0250000000000309: "Pharah",
	0x025000000000030A: "Reinhardt", 0x025000000000030B: "Hanzo", 0x025000000000030C: "Torbjörn",
	0x025000000000030D: "Winston", 0x025000000000030E: "Zenyatta", 0x025000000000030F: "Symmetra",
	0x0250000000000310: "Bastion", 0x0250000000000311: "Zarya", 0x0250000000000312: "Soldier: 76",
	0x0250000000000313: "Lúcio", 0x0250000000000314: "Roadhog", 0x0250000000000315: "Junkrat",
	0x0250000000000316: "Genji", 0x0250000000000317: "D.Va", 0x0250000000000318: "Mei",
}

var defaultIdentity = Identity{Name: "Player#1234", Icon: 0x0250000000000306} // Tracer

var (
	identityMu     sync.Mutex
	identity       Identity
	identityLoaded bool
	// startup overrides (--name / --icon, LOGIC_NAME / LOGIC_ICON): applied over the
	// file on first use, not written to it; !name / !icon still persist.
	startupName string
	startupIcon uint64
)

// ConfigureIdentity sets the process-wide startup overrides for the player's name
// (BattleTag; "#1234" is added when missing) and icon (GUID in hex or an icon name,
// e.g. "Mei"). Empty values keep the file's / default values. Call before any
// message is sent.
func ConfigureIdentity(name, icon string) error {
	identityMu.Lock()
	defer identityMu.Unlock()
	if name != "" {
		if err := validName(name); err != nil {
			return fmt.Errorf("--name: %w", err)
		}
		startupName = withTag(name)
	}
	if icon != "" {
		id, err := findIcon(icon)
		if err != nil {
			return fmt.Errorf("--icon: %w", err)
		}
		startupIcon = id
	}
	identityLoaded = false // re-apply on next use
	return nil
}

func identityFile() string {
	if p := os.Getenv("LOGIC_IDENTITY"); p != "" {
		return p
	}
	return "lobby-identity.json"
}

// currentIdentity returns the identity, loading the file on first use.
func currentIdentity() Identity {
	identityMu.Lock()
	defer identityMu.Unlock()
	if !identityLoaded {
		identityLoaded = true
		identity = defaultIdentity
		if b, err := os.ReadFile(identityFile()); err == nil {
			var f identityJSON
			if json.Unmarshal(b, &f) == nil && validName(f.Name) == nil {
				if icon, err := findIcon(f.Icon); err == nil {
					identity = Identity{Name: f.Name, Icon: icon}
				}
			}
		}
		if startupName != "" {
			identity.Name = startupName
		}
		if startupIcon != 0 {
			identity.Icon = startupIcon
		}
	}
	return identity
}

func setIdentity(id Identity) error {
	currentIdentity()
	identityMu.Lock()
	defer identityMu.Unlock()
	identity = id
	b, _ := json.MarshalIndent(identityJSON{id.Name, fmt.Sprintf("%#016x", id.Icon)}, "", "  ")
	return os.WriteFile(identityFile(), append(b, '\n'), 0o644)
}

// validName checks a BattleTag. The client strips "#..." for display and keeps at
// most 63 bytes (FUN_7ff650d85700 copies into a 64-byte buffer).
func validName(name string) error {
	short, _, _ := strings.Cut(name, "#")
	switch {
	case short == "":
		return errors.New("empty name")
	case len(name) > 63:
		return errors.New("name longer than 63 bytes")
	case strings.ContainsAny(name, "\x00"):
		return errors.New("name contains NUL")
	}
	return nil
}

func validIcon(icon uint64) error {
	if _, ok := iconNames[icon]; !ok {
		return fmt.Errorf("%#016x is not one of the 23 player icons (!icon list)", icon)
	}
	return nil
}

// withTag adds "#1234" to names without a tag.
func withTag(name string) string {
	if strings.Contains(name, "#") {
		return name
	}
	return name + "#1234"
}

// playerName is the local player's BattleTag.
func playerName() string { return currentIdentity().Name }

// playerIcon is the local player's equipped player icon.
func playerIcon() uint64 { return currentIdentity().Icon }

// sortedIcons lists the icon GUIDs in order.
func sortedIcons() []uint64 {
	ids := make([]uint64, 0, len(iconNames))
	for id := range iconNames {
		ids = append(ids, id)
	}
	slices.Sort(ids)
	return ids
}

// findIcon resolves an icon by GUID (full or short hex) or by name (case-insensitive).
func findIcon(s string) (uint64, error) {
	for id, name := range iconNames {
		if strings.EqualFold(name, s) {
			return id, nil
		}
	}
	v, err := strconv.ParseUint(strings.TrimPrefix(strings.ToLower(s), "0x"), 16, 64)
	if err != nil {
		return 0, fmt.Errorf("unknown icon %q (!icon list)", s)
	}
	if v>>48 == 0 {
		v |= 0x0250000000000000
	}
	return v, validIcon(v)
}

// playerProgression encodes ClientInLobbyPlayerProgression 24300:
// A {bool tutorialDone, u64 equippedIcon, []unlock owned}, B {[]unlock}, with
// unlock = {bool, i32 level, u64 asset, u64}. The owned list is every player icon.
func playerProgression(tutorialDone bool, icon uint64, owned []uint64) []byte {
	b := []byte{0}
	if tutorialDone {
		b[0] = 1
	}
	b = binary.LittleEndian.AppendUint64(b, icon)
	b = binary.LittleEndian.AppendUint32(b, uint32(len(owned)))
	for _, u := range owned {
		b = append(b, 0)
		b = binary.LittleEndian.AppendUint32(b, 0)
		b = binary.LittleEndian.AppendUint64(b, u)
		b = binary.LittleEndian.AppendUint64(b, 0)
	}
	return binary.LittleEndian.AppendUint32(b, 0) // B: empty
}

func sendPlayerProgression(c *Ctx) error {
	return c.Send("ClientInLobbyPlayerProgression", 24300, playerProgression(true, playerIcon(), sortedIcons()))
}

// connectAccepted is ClientInLobbyConnect 20500 {ID self, cstring}.
func connectAccepted(self []byte, name string) *jam.ConnectAccepted {
	return &jam.ConnectAccepted{Player: jam.JamIDFromBytes(self), BattleTag: name}
}

func init() {
	commands["name"] = command{"!name <BattleTag> - set your name (party panel via 20700, social panel + chat via 20500)", func(c *Ctx, a []string) error {
		if len(a) < 1 {
			return c.SystemChat("name: " + playerName())
		}
		id := currentIdentity()
		id.Name = withTag(a[0])
		if err := validName(id.Name); err != nil {
			return err
		}
		if err := setIdentity(id); err != nil {
			return err
		}
		// 20500 is accepted again mid-session (confirmed): it updates the social panel
		// header and the chat name.
		if err := c.SendMsg(connectAccepted(c.ClientJamID(), id.Name)); err != nil {
			return err
		}
		return c.sendParty()
	}}
	commands["icon"] = command{"!icon <name|guid|list> - set your player icon", func(c *Ctx, a []string) error {
		if len(a) < 1 {
			return c.SystemChat(fmt.Sprintf("icon: %s (%#016x)", iconNames[playerIcon()], playerIcon()))
		}
		if strings.EqualFold(a[0], "list") {
			var names []string
			for _, id := range sortedIcons() {
				names = append(names, fmt.Sprintf("%x %s", id&0xFFFF, iconNames[id]))
			}
			return c.SystemChat("icons: " + strings.Join(names, ", "))
		}
		icon, err := findIcon(strings.Join(a, " "))
		if err != nil {
			return err
		}
		id := currentIdentity()
		id.Icon = icon
		if err := setIdentity(id); err != nil {
			return err
		}
		if err := sendPlayerProgression(c); err != nil {
			return err
		}
		return c.sendParty()
	}}
}
