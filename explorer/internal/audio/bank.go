package audio

import (
	"encoding/binary"
	"errors"
	"fmt"
)

// Bank is a Wwise sound bank (docs/AssetFormats.md §5.4): BKHD header and the HIRC object hierarchy. 0.8's banks
// hold no media (no DIDX / DATA): the sounds are separate WEM resources.
type Bank struct {
	Version  uint32       `json:"version"`
	ID       uint32       `json:"id"`
	Language uint32       `json:"language"`
	Chunks   []Chunk      `json:"chunks"`
	Objects  []HircObject `json:"objects"`
}

// HircObject is one hierarchy object: its type, id and body.
type HircObject struct {
	Type   uint8  `json:"type"`
	ID     uint32 `json:"id"`
	Offset int    `json:"offset"` // of the body (after the id)
	Size   int    `json:"size"`   // of the body
	body   []byte
}

// HIRC object types (Wwise).
var HircTypeNames = map[uint8]string{
	1: "State", 2: "Sound", 3: "Action", 4: "Event", 5: "Random/Sequence container", 6: "Switch container",
	7: "Actor-mixer", 8: "Audio bus", 9: "Blend container", 10: "Music segment", 11: "Music track",
	12: "Music switch container", 13: "Music playlist container", 14: "Attenuation", 15: "Dialogue event",
	16: "Motion bus", 17: "Motion FX", 18: "Effect", 19: "Effect (custom)", 20: "Auxiliary bus", 21: "LFO modulator",
	22: "Envelope modulator", 23: "Audio device",
}

// ParseBank reads the chunks, the header and the HIRC object table.
func ParseBank(b []byte) (*Bank, error) {
	le := binary.LittleEndian
	bk := &Bank{}
	for off := 0; off+8 <= len(b); {
		id, size := string(b[off:off+4]), int(le.Uint32(b[off+4:]))
		data := off + 8
		if size < 0 || data+size > len(b) {
			return bk, fmt.Errorf("audio: bank chunk %q at 0x%x runs past the end", id, off)
		}
		bk.Chunks = append(bk.Chunks, Chunk{id, data, size})
		c := b[data : data+size]
		switch id {
		case "BKHD":
			if size < 12 {
				return bk, errors.New("audio: short BKHD")
			}
			bk.Version, bk.ID, bk.Language = le.Uint32(c), le.Uint32(c[4:]), le.Uint32(c[8:])
		case "HIRC":
			if size < 4 {
				return bk, errors.New("audio: short HIRC")
			}
			n := int(le.Uint32(c))
			p := 4
			for range n {
				if p+9 > len(c) {
					return bk, errors.New("audio: HIRC object table runs past the chunk")
				}
				t, sz := c[p], int(le.Uint32(c[p+1:]))
				if sz < 4 || p+5+sz > len(c) {
					return bk, fmt.Errorf("audio: HIRC object at 0x%x: size %d", data+p, sz)
				}
				bk.Objects = append(bk.Objects, HircObject{Type: t, ID: le.Uint32(c[p+5:]), Offset: data + p + 9,
					Size: sz - 4, body: c[p+9 : p+5+sz]})
				p += 5 + sz
			}
			if p != len(c) {
				return bk, fmt.Errorf("audio: HIRC has %d bytes after its %d objects", len(c)-p, n)
			}
		}
		off = data + size
	}
	if len(bk.Chunks) == 0 || bk.Chunks[0].ID != "BKHD" {
		return bk, errors.New("audio: not a sound bank (no BKHD)")
	}
	return bk, nil
}

// WwiseID is Wwise's id of a name: FNV-1 (32-bit) of the lower-cased name [D: every named switch / state group, switch,
// state and parameter of 0.8 matches its id].
func WwiseID(name string) uint32 {
	h := uint32(0x811c9dc5)
	for _, c := range []byte(name) {
		if c >= 'A' && c <= 'Z' {
			c += 'a' - 'A'
		}
		h *= 0x01000193
		h ^= uint32(c)
	}
	return h
}

// Sound is a Sound object's source (HIRC type 2, AkBankSourceData, bank version 112): plugin id u32, stream type u8,
// source id u32, file id u32 (0; for streamed sources the source id again), file offset u32 (only when not streamed),
// in-memory size u32, source bits u8 [D: the layout that makes every parent resolve; field meanings from Wwise, ?];
// a source plugin (plugin type 2: silence 0x0065, 0x0094) follows with u32 size + parameters.
type Sound struct {
	Plugin     uint32 `json:"plugin"` // 0x00040001 = Vorbis
	StreamType uint8  `json:"streamType"`
	SourceID   uint32 `json:"sourceId"` // the WEM's id (STUSoundBankDataVersion m_wwiseWEMFileIDs / m_wwiseWEMStreamIDs)
	MediaSize  uint32 `json:"mediaSize"`
	size       int    // bytes of the source data
}

// Sound decodes a Sound object's source.
func (o *HircObject) Sound() (Sound, bool) {
	if o.Type != 2 || len(o.body) < 22 {
		return Sound{}, false
	}
	le := binary.LittleEndian
	s := Sound{Plugin: le.Uint32(o.body), StreamType: o.body[4], SourceID: le.Uint32(o.body[5:]), size: 22}
	if s.StreamType == 2 {
		s.size = 18
	}
	s.MediaSize = le.Uint32(o.body[s.size-5:])
	if s.Plugin&0x0F == 2 {
		if len(o.body) < s.size+4 {
			return s, false
		}
		s.size += 4 + int(le.Uint32(o.body[s.size:]))
	}
	return s, s.size <= len(o.body)
}

// Parent is the DirectParentID of the audio nodes (Sound, containers, actor-mixers). NodeBaseParams (version 112)
// begins with the effects (override u8, count u8, [bypass bits u8, count × 7 bytes]), a u8 (0 in every 0.8 bank [D]),
// the output bus id u32, then the parent [D: every parent resolves, see docs/AssetFormats.md §5.4].
func (o *HircObject) Parent() (uint32, bool) {
	var p int
	switch o.Type {
	case 2:
		s, ok := o.Sound()
		if !ok {
			return 0, false
		}
		p = s.size
	case 5, 6, 7, 9:
		p = 0
	default:
		return 0, false
	}
	b := o.body
	if p+2 > len(b) {
		return 0, false
	}
	nfx := int(b[p+1])
	p += 2
	if nfx > 0 {
		p += 1 + 7*nfx
	}
	p++
	if p+8 > len(b) {
		return 0, false
	}
	return binary.LittleEndian.Uint32(b[p+4:]), true
}

// Actions is an Event's action list (HIRC type 4: u32 count, ids).
func (o *HircObject) Actions() ([]uint32, bool) {
	if o.Type != 4 || len(o.body) < 4 {
		return nil, false
	}
	n := int(binary.LittleEndian.Uint32(o.body))
	if len(o.body) != 4+4*n {
		return nil, false
	}
	ids := make([]uint32, n)
	for i := range ids {
		ids[i] = binary.LittleEndian.Uint32(o.body[4+4*i:])
	}
	return ids, true
}

// Action is an Action's type and target (HIRC type 3).
type Action struct {
	Type   uint16 `json:"type"`
	Target uint32 `json:"target"`
	IsBus  bool   `json:"isBus"`
}

func (o *HircObject) Action() (Action, bool) {
	if o.Type != 3 || len(o.body) < 7 {
		return Action{}, false
	}
	le := binary.LittleEndian
	return Action{le.Uint16(o.body), le.Uint32(o.body[2:]), o.body[6] != 0}, true
}

// Body is the object's bytes after its id.
func (o *HircObject) Body() []byte { return o.body }
