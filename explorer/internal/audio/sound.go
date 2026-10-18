package audio

import (
	"encoding/binary"
	"fmt"

	"owexplorer/internal/names"
	"owstu/stu"
)

// Reader reads a resource by GUID.
type Reader func(guid uint64) ([]byte, error)

// SoundInfo is an STUSound (0x02C): the Wwise events it posts, its bank and the WEMs the bank's sounds play
// (docs/AssetFormats.md §5.1).
type SoundInfo struct {
	EventPlay uint32    `json:"eventPlay"` // Wwise event id (HIRC Event in the bank)
	EventStop uint32    `json:"eventStop"`
	Bank      uint64    `json:"bank,string"`
	BankID    uint32    `json:"bankId"` // STUSoundBankDataVersion field 0x40ac0372 = the bank's BKHD id [D]
	Files     []WEMFile `json:"files"`
}

// WEMFile is one WEM of a sound: its resource and its Wwise source id.
type WEMFile struct {
	GUID   uint64 `json:"guid,string"`
	ID     uint32 `json:"id"`
	Stream bool   `json:"stream"` // listed under m_soundWEMStreams (0x0B2)
}

var (
	hVersioned  = names.Hash("m_versionedBankData")
	hBank       = names.Hash("m_soundBank")
	hFiles      = names.Hash("m_soundWEMFiles")
	hStreams    = names.Hash("m_soundWEMStreams")
	hFileIDs    = names.Hash("m_wwiseWEMFileIDs")
	hStreamIDs  = names.Hash("m_wwiseWEMStreamIDs")
	hEventPlay  = names.Hash("m_wwiseEventIDPlay")
	hEventStop  = names.Hash("m_wwiseEventIDStop")
	hBankIDData = uint32(0x40ac0372)
)

func u32(v stu.Value) uint32 {
	b, err := v.Raw()
	if err != nil || len(b) < 4 {
		return 0
	}
	return binary.LittleEndian.Uint32(b)
}

// ReadSound decodes an STUSound document.
func ReadSound(d *stu.Document) (*SoundInfo, error) {
	root := d.Decode(d.Root())
	s := &SoundInfo{EventPlay: u32(root.Field(hEventPlay)), EventStop: u32(root.Field(hEventStop))}
	bd := root.Field(hVersioned).Deref()
	if !bd.Valid() {
		return s, nil // one empty sound in 0.8: 0x0D40000000000F49 (no bank, no events) [D]
	}
	s.Bank, _, _ = bd.Field(hBank).Ref()
	s.BankID = u32(bd.Field(hBankIDData))
	add := func(files, ids uint32, stream bool) error {
		fs, is := bd.Field(files).Items(), bd.Field(ids).Items()
		if len(fs) != len(is) {
			return fmt.Errorf("audio: %d WEM files but %d ids", len(fs), len(is))
		}
		for i, f := range fs {
			g, _, _ := f.Ref()
			s.Files = append(s.Files, WEMFile{GUID: g, ID: u32(is[i]), Stream: stream})
		}
		return nil
	}
	if err := add(hFiles, hFileIDs, false); err != nil {
		return s, err
	}
	return s, add(hStreams, hStreamIDs, true)
}

// OpenSound reads and decodes an STUSound resource.
func OpenSound(read Reader, guid uint64) (*SoundInfo, error) {
	b, err := read(guid)
	if err != nil {
		return nil, err
	}
	d, err := stu.Open(b, stu.File, 0)
	if err != nil {
		return nil, err
	}
	defer d.Close()
	return ReadSound(d)
}

// Named is a switch group, switch, parameter or state group (0x02D–0x030): a name (empty when stripped) and its Wwise
// id (FNV-1 of the name, WwiseID).
type Named struct {
	Name  string `json:"name"`
	ID    uint32 `json:"id"`
	Group uint64 `json:"group,string,omitempty"` // a switch's group (0x02D)
}

// ReadNamed decodes a 0x02D–0x030 document: its string field, its u32 id and a switch's group reference.
func ReadNamed(d *stu.Document) Named {
	var n Named
	for _, f := range d.Decode(d.Root()).Items() {
		switch f.Type() {
		case stu.String:
			n.Name, _ = f.Str()
		case stu.Primitive:
			if b, err := f.Raw(); err == nil && len(b) == 4 {
				n.ID = binary.LittleEndian.Uint32(b)
			}
		}
		if g, _, err := f.Ref(); err == nil && g != 0 {
			n.Group = g
		}
	}
	return n
}
