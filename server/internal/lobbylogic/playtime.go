package lobbylogic

import (
	"encoding/binary"
	"fmt"
	"strconv"
	"sync"
	"time"
)

// Play-time tracker (ClientInLobbyPlayer 20804 = {bool enabled, u64 remaining, u64 played}).
// See docs/LobbyServer.md "Play-time warnings".
//
// The handler (inline in FUN_7ff650c497f0) writes the profile (FUN_7ff650c24f80):
//
//	+0x18 bool enabled   +0x20 u64 remaining ms   +0x28 u64 played ms
//
// and re-arms each warning record whose threshold is above the new play time. The
// records ({bool fired, u64 threshold ms, string}) sit at +0x30 and +0x48; the client
// fills them itself (measured: 3 h = 10800000 "3 Hour CAIS warning goes here."
// 0x0DE..16BF, 5 h = 18000000 "5 Hour CAIS warning goes here." 0x0DE..16C0).
// While enabled, FUN_7ff650c5cf70 adds the frame time (s * 1000) to played, subtracts
// it from remaining (floored at 0), and pops up (UI 0x0AC..5D5) each record's string
// once played passes its threshold.
//
// The main menu's BooleanSwitch 0x4abdb7da (graph 0134 #15, FUN_7ff650d78140) reads
// +0x18 and shows the CAIS "Healthy Gaming" screen 0x09A..4B9 (#14; it has no button)
// for as long as it is true.
type playTime struct {
	enabled   bool
	remaining time.Duration
	played    time.Duration
}

func (p playTime) encode() []byte {
	b := appendBool(nil, p.enabled)
	b = binary.LittleEndian.AppendUint64(b, uint64(p.remaining.Milliseconds()))
	return binary.LittleEndian.AppendUint64(b, uint64(p.played.Milliseconds()))
}

var (
	playTimeMu sync.Mutex
	// playTimeConfig is what sendPlayTime sends; off by default.
	playTimeConfig playTime
)

// sendPlayTime sends the configured tracker (for the connect sequence; nothing when off).
func sendPlayTime(c *Ctx) error {
	playTimeMu.Lock()
	p := playTimeConfig
	playTimeMu.Unlock()
	if !p.enabled {
		return nil
	}
	return c.Send("ClientInLobbyPlayer", 20804, p.encode())
}

func parseHours(s string) (time.Duration, error) {
	h, err := strconv.ParseFloat(s, 64)
	if err != nil || h < 0 {
		return 0, fmt.Errorf("hours: %q", s)
	}
	return time.Duration(h * float64(time.Hour)), nil
}

func init() {
	commands["playtime"] = command{"!playtime on [played h] [remaining h] | off | warn [seconds] - play-time tracker (20804): on/off configure it (sent now and on connect), warn shows the CAIS warning once (on for N s, default 10, then off)", func(c *Ctx, a []string) error {
		if len(a) < 1 {
			return fmt.Errorf("usage: !playtime on [played h] [remaining h] | off | warn [seconds]")
		}
		switch a[0] {
		case "on":
			p := playTime{enabled: true, remaining: 24 * time.Hour}
			var err error
			if len(a) > 1 {
				if p.played, err = parseHours(a[1]); err != nil {
					return err
				}
			}
			if len(a) > 2 {
				if p.remaining, err = parseHours(a[2]); err != nil {
					return err
				}
			}
			playTimeMu.Lock()
			playTimeConfig = p
			playTimeMu.Unlock()
			if err := c.Send("ClientInLobbyPlayer", 20804, p.encode()); err != nil {
				return err
			}
			return c.SystemChat(fmt.Sprintf("play-time tracker on: played %v, remaining %v", p.played, p.remaining))
		case "off":
			playTimeMu.Lock()
			playTimeConfig = playTime{}
			playTimeMu.Unlock()
			if err := c.Send("ClientInLobbyPlayer", 20804, playTime{}.encode()); err != nil {
				return err
			}
			return c.SystemChat("play-time tracker off")
		case "warn":
			secs := 10
			if len(a) > 1 {
				n, err := strconv.Atoi(a[1])
				if err != nil || n <= 0 {
					return fmt.Errorf("seconds: %q", a[1])
				}
				secs = n
			}
			// Played stays below the 3 h threshold: only the main-menu screen shows.
			if err := c.Send("ClientInLobbyPlayer", 20804, playTime{enabled: true, remaining: time.Hour}.encode()); err != nil {
				return err
			}
			time.AfterFunc(time.Duration(secs)*time.Second, func() {
				if err := c.Send("ClientInLobbyPlayer", 20804, playTime{}.encode()); err != nil {
					c.Log.Warn("play-time warning: turning it off failed", "err", err)
				}
			})
			return c.SystemChat(fmt.Sprintf("CAIS warning on for %d s (shows while the main menu is up)", secs))
		default:
			return fmt.Errorf("usage: !playtime on [played h] [remaining h] | off | warn [seconds]")
		}
	}}
}
