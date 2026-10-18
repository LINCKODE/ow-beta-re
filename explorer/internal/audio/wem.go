package audio

import (
	"encoding/binary"
	"errors"
	"fmt"
)

// Resource types (docs/AssetFormats.md §5).
const (
	TypeSound       = 0x02C // STUSound: event ids, bank, WEM files
	TypeSwitchGroup = 0x02D // STUSoundSwitchGroup
	TypeSwitch      = 0x02E // STUSoundSwitch
	TypeParameter   = 0x02F // STUSoundParameter (RTPC)
	TypeStateGroup  = 0x030 // STUSoundStateGroup
	TypeWEM         = 0x03F // WEM file (RIFF, Wwise Vorbis)
	TypeBank        = 0x043 // sound bank (BKHD + HIRC)
	TypeWEMStream   = 0x0B2 // streamed WEM file (same format)
)

// FormatVorbis is the WAVE format tag of Wwise Vorbis (0xFFFF).
const FormatVorbis = 0xFFFF

// Chunk is one RIFF chunk of a WEM.
type Chunk struct {
	ID     string `json:"id"`
	Offset int    `json:"offset"` // of the chunk data
	Size   int    `json:"size"`
}

// WEM is a parsed WEM header.
type WEM struct {
	Format      uint16  `json:"format"`
	Channels    int     `json:"channels"`
	SampleRate  int     `json:"sampleRate"`
	AvgBytes    int     `json:"avgBytesPerSecond"`
	ChannelMask uint32  `json:"channelMask"`
	Chunks      []Chunk `json:"chunks"`
	Samples     int     `json:"samples"`     // per channel (vorb +0x00)
	ModSignal   uint32  `json:"modSignal"`   // vorb +0x04
	SetupOffset int     `json:"setupOffset"` // relative to the data chunk
	AudioOffset int     `json:"audioOffset"`
	UID         uint32  `json:"uid"`
	Blocksize0  int     `json:"blocksize0"` // short window, samples
	Blocksize1  int     `json:"blocksize1"`
	ModPackets  bool    `json:"modPackets"` // audio packets lack the packet type and window flags
	DataOffset  int     `json:"dataOffset"`
	DataSize    int     `json:"dataSize"`
	LoopStart   int     `json:"loopStart,omitempty"` // smpl chunk, samples
	LoopEnd     int     `json:"loopEnd,omitempty"`
	HasLoop     bool    `json:"hasLoop,omitempty"`
}

// Seconds is the duration.
func (w *WEM) Seconds() float64 {
	if w.SampleRate == 0 {
		return 0
	}
	return float64(w.Samples) / float64(w.SampleRate)
}

// ParseWEM reads a WEM's RIFF header. Only the layout 0.8 uses is accepted: a 0x42-byte fmt chunk with the vorb
// fields inside it at +0x18 (docs/AssetFormats.md §5.2).
func ParseWEM(b []byte) (*WEM, error) {
	if len(b) < 12 || string(b[0:4]) != "RIFF" || string(b[8:12]) != "WAVE" {
		return nil, errors.New("audio: not a RIFF WAVE file")
	}
	le := binary.LittleEndian
	w := &WEM{}
	fmtOff, fmtSize := -1, 0
	end := min(len(b), int(le.Uint32(b[4:]))+8)
	for off := 12; off+8 <= end; {
		id, size := string(b[off:off+4]), int(le.Uint32(b[off+4:]))
		data := off + 8
		if size < 0 || data+size > len(b) {
			return nil, fmt.Errorf("audio: chunk %q at 0x%x runs past the end", id, off)
		}
		w.Chunks = append(w.Chunks, Chunk{id, data, size})
		switch id {
		case "fmt ":
			fmtOff, fmtSize = data, size
		case "data":
			w.DataOffset, w.DataSize = data, size
		case "smpl":
			if size >= 0x24+0x18 && le.Uint32(b[data+0x1C:]) > 0 {
				w.HasLoop = true
				w.LoopStart = int(le.Uint32(b[data+0x24+0x08:]))
				w.LoopEnd = int(le.Uint32(b[data+0x24+0x0C:]))
			}
		}
		off = data + size + size&1
	}
	if fmtOff < 0 || w.DataSize == 0 {
		return nil, errors.New("audio: fmt or data chunk missing")
	}
	if fmtSize < 0x10 {
		return nil, fmt.Errorf("audio: fmt chunk of %d bytes", fmtSize)
	}
	f := b[fmtOff:]
	w.Format = le.Uint16(f)
	w.Channels = int(le.Uint16(f[2:]))
	w.SampleRate = int(le.Uint32(f[4:]))
	w.AvgBytes = int(le.Uint32(f[8:]))
	if w.Format != FormatVorbis {
		return w, fmt.Errorf("audio: format 0x%04x (only Wwise Vorbis 0xFFFF is known)", w.Format)
	}
	if fmtSize != 0x42 {
		return w, fmt.Errorf("audio: Wwise Vorbis fmt chunk of 0x%x bytes (only 0x42 is known)", fmtSize)
	}
	w.ChannelMask = le.Uint32(f[0x14:])
	v := f[0x18:]
	w.Samples = int(le.Uint32(v))
	w.ModSignal = le.Uint32(v[4:])
	// ww2ogg's observation, confirmed on every 0.8 WEM (0xD9 / 0xCB …): these signal values mean the plain layout.
	switch w.ModSignal {
	case 0x4A, 0x4B, 0x69, 0x70:
	default:
		w.ModPackets = true
	}
	w.SetupOffset = int(le.Uint32(v[0x10:]))
	w.AudioOffset = int(le.Uint32(v[0x14:]))
	w.UID = le.Uint32(v[0x24:])
	w.Blocksize0 = 1 << v[0x28]
	w.Blocksize1 = 1 << v[0x29]
	if w.Channels < 1 || w.Channels > 8 || w.SampleRate == 0 {
		return w, fmt.Errorf("audio: %d channels at %d Hz", w.Channels, w.SampleRate)
	}
	if v[0x28] < 6 || v[0x28] > 13 || v[0x29] < v[0x28] || v[0x29] > 13 {
		return w, fmt.Errorf("audio: block sizes 2^%d / 2^%d", v[0x28], v[0x29])
	}
	if w.SetupOffset >= w.DataSize || w.AudioOffset > w.DataSize {
		return w, errors.New("audio: setup / audio offsets past the data")
	}
	return w, nil
}

func errorf(format string, a ...any) error { return fmt.Errorf("audio: "+format, a...) }
