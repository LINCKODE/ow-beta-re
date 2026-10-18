package texture

import (
	"errors"
	"fmt"
)

// Reader reads a resource by GUID. It returns ErrMissing (or an error wrapping it) for GUIDs the install lacks.
type Reader func(guid uint64) ([]byte, error)

// ErrMissing: a resource the texture refers to is not in the install.
var ErrMissing = errors.New("not in the install")

// Mip is one mip level: its size and where its bytes are (nil Data = not available).
type Mip struct {
	Width, Height int
	Source        uint64   // the payload GUID, 0 = inline
	Data          [][]byte // one slice per surface
}

// Payload is one of a texture's payloads.
type Payload struct {
	GUID    uint64
	Present bool
	Header  PayloadHeader
	First   int // first mip (0 = full size)
	Err     string
}

// Texture is a texture header with its mips resolved to bytes.
type Texture struct {
	GUID     uint64
	Header   Header
	Format   *Format // nil: no decoder for the format
	Mips     []Mip
	Payloads []Payload
	Inline   bool
}

// Load reads texture guid and its payloads. It fails only when the header is unusable; missing payloads leave their
// mips without data (Texture.Available) and are listed in Payloads.
func Load(read Reader, guid uint64) (*Texture, error) { return load(read, guid, MaxPayloads) }

// LoadSmall reads only payload 0 (the small mips: everything below the largest one or two), for thumbnails.
func LoadSmall(read Reader, guid uint64) (*Texture, error) { return load(read, guid, 1) }

func load(read Reader, guid uint64, payloads int) (*Texture, error) {
	b, err := read(guid)
	if err != nil {
		return nil, err
	}
	h, err := ParseHeader(b)
	if err != nil {
		return nil, err
	}
	t := &Texture{GUID: guid, Header: h, Format: LookupFormat(h.Format), Inline: h.Payloads == 0}
	mips := h.MipCount()
	t.Mips = make([]Mip, mips)
	for m := range t.Mips {
		t.Mips[m].Width, t.Mips[m].Height = h.MipDim(m)
	}
	if t.Format == nil {
		return t, nil
	}
	if t.Inline {
		if len(b) < InlineOffset+int(h.DataSize) {
			return t, fmt.Errorf("texture: inline data %d bytes, have %d", h.DataSize, len(b)-InlineOffset)
		}
		return t, t.place(b[InlineOffset:InlineOffset+int(h.DataSize)], 0, mips, 0)
	}
	for i := range min(int(h.Payloads), payloads) {
		t.Payloads = append(t.Payloads, t.payload(read, i))
	}
	return t, nil
}

// payload reads payload i and places its mips; a delta texture's payload patches the base texture's payload i
// (docs/AssetFormats.md §1.5).
func (t *Texture) payload(read Reader, i int) Payload {
	g := PayloadGUID(t.GUID, i)
	p := Payload{GUID: g}
	b, err := read(g)
	if err != nil {
		if !errors.Is(err, ErrMissing) {
			p.Err = err.Error()
		}
		return p
	}
	p.Present = true
	if p.Header, err = ParsePayloadHeader(b); err != nil {
		p.Err = err.Error()
		return p
	}
	p.First = p.Header.FirstMip(len(t.Mips))
	if p.First < 0 || p.First+int(p.Header.Count) > len(t.Mips) {
		p.Err = fmt.Sprintf("payload mips %d+%d outside the texture's %d", p.First, p.Header.Count, len(t.Mips))
		return p
	}
	data := b[PayloadHdr:]
	switch {
	case t.Header.Flags&FlagDelta != 0:
		data, err = t.applyDelta(read, i, p, data)
	case t.Header.Flags&FlagSplit != 0:
		data = Unsplit(t.Header.Format, data)
	}
	if err == nil {
		err = t.place(data, p.First, int(p.Header.Count), g)
	}
	if err != nil {
		p.Err = err.Error()
	}
	return p
}

// place splits data holding mips [first, first+count) of every surface: surface-major, then mip, as the client
// sets up the subresources [G: 0x961e60] (docs/AssetFormats.md §1.4).
func (t *Texture) place(data []byte, first, count int, src uint64) error {
	ns := t.Header.SurfaceCount()
	for m := first; m < first+count; m++ {
		t.Mips[m].Data = make([][]byte, ns)
		t.Mips[m].Source = src
	}
	o := 0
	for s := range ns {
		for m := first; m < first+count; m++ {
			mp := &t.Mips[m]
			n := t.Format.MipBytes(mp.Width, mp.Height)
			if o+n > len(data) {
				for m := first; m < first+count; m++ {
					t.Mips[m].Data = nil
				}
				return fmt.Errorf("data ends in surface %d mip %d (%d bytes, need %d)", s, m, len(data), o+n)
			}
			mp.Data[s] = data[o : o+n]
			o += n
		}
	}
	if o != len(data) {
		return fmt.Errorf("%d bytes after the last mip", len(data)-o)
	}
	return nil
}

// Available reports whether mip m has data.
func (t *Texture) Available(m int) bool { return m >= 0 && m < len(t.Mips) && t.Mips[m].Data != nil }

// Best is the largest available mip, or -1.
func (t *Texture) Best() int {
	for m := range t.Mips {
		if t.Available(m) {
			return m
		}
	}
	return -1
}

// Decode decodes one surface of mip m to RGBA float32.
func (t *Texture) Decode(m, surface int) ([]float32, error) {
	if t.Format == nil {
		return nil, fmt.Errorf("texture: no decoder for %s", FormatName(t.Header.Format))
	}
	if !t.Available(m) {
		return nil, fmt.Errorf("texture: mip %d not available", m)
	}
	if surface < 0 || surface >= len(t.Mips[m].Data) {
		return nil, fmt.Errorf("texture: surface %d of %d", surface, len(t.Mips[m].Data))
	}
	mp := t.Mips[m]
	return t.Format.Decode(mp.Data[surface], mp.Width, mp.Height)
}
