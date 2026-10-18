package texture

import (
	"owcontent/content"
	"owexplorer/internal/refindex"
)

// Source adds texture references to the reference index (docs/Explorer.md "Plugging in a format"):
// texture → its payloads, delta texture → its base texture and the base's payloads.
type Source struct{}

const (
	kindPayload = iota
	kindBase
)

func (Source) ID() string   { return "texture" }
func (Source) Version() int { return 1 }
func (Source) Kinds() []refindex.KindInfo {
	return []refindex.KindInfo{{Name: "texture.payload", Label: "texture payload"}, {Name: "texture.base", Label: "delta base"}}
}
func (Source) Workers() int { return 8 }

// Select: textures with payloads are header-only (32 bytes); inline textures reference nothing.
func (Source) Select(_ *refindex.Ctx, r content.Resource) bool {
	return r.Type == TypeTexture && r.Size < InlineOffset
}

func (Source) Scan(c *refindex.Ctx, r content.Resource, e *refindex.Emitter) error {
	b, err := c.Store.Read(r.GUID)
	if err != nil {
		return err
	}
	h, err := ParseHeader(b)
	if err != nil {
		return err
	}
	for i := range int(h.Payloads) {
		e.Ref(r.GUID, PayloadGUID(r.GUID, i), kindPayload, uint32(i))
		if h.Flags&FlagDelta != 0 {
			e.Ref(r.GUID, PayloadGUID(h.Base, i), kindBase, uint32(i))
		}
	}
	if h.Flags&FlagDelta != 0 {
		e.Ref(r.GUID, h.Base, kindBase, 0)
	}
	return nil
}
