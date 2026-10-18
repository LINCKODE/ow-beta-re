package lobbylogic

import (
	"encoding/binary"
)

// Store (ClientInLobbyStore 26400-26409; dispatch FUN_7ff650c4a390). See docs/LobbyScripts.md.
//
// 26401 = {[]u64 products, []{u16 currency, f32 amount}}. Its handler (FUN_7ff650c774b0)
// replaces the profile's owned-product set (+0x90, insert FUN_7ff650cca6d0) and the
// currency balances (+0xd8), then posts a game message.
//
// The lobby hero page (graph 0x0580000000000144, "My Heroes" -> a hero) branches on
// ConfigVar 0xd1211491 = FUN_7ff650cc7fe0(profile, hero GUID): "is the hero in the
// owned-product set". Owned -> hero details (graph 0x0141: details, change mods ->
// hero customize 0x015f); not owned -> hero sheet with an Unlock button (-> the
// purchase flow 0x017d). We send every hero as owned.

func ownedProducts(products []uint64) []byte {
	b := binary.LittleEndian.AppendUint32(nil, uint32(len(products)))
	for _, p := range products {
		b = binary.LittleEndian.AppendUint64(b, p)
	}
	return binary.LittleEndian.AppendUint32(b, 0) // no currency balances
}

func sendOwnedProducts(c *Ctx, products []uint64) error {
	return c.Send("ClientInLobbyStore", 26401, ownedProducts(products))
}

func init() {
	commands["products"] = command{"!products [none] - send the owned-product set (all heroes, or empty)", func(c *Ctx, a []string) error {
		if len(a) > 0 && a[0] == "none" {
			return sendOwnedProducts(c, nil)
		}
		return sendOwnedProducts(c, heroes)
	}}
}
