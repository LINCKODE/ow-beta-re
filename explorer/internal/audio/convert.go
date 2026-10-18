package audio

import (
	"encoding/binary"
	"fmt"
)

// Vorbis is a WEM rebuilt into standard Vorbis I packets (docs/AssetFormats.md §5.3).
type Vorbis struct {
	Header  *WEM
	ID      []byte   // identification header
	Comment []byte   // comment header
	Setup   []byte   // setup header
	Packets [][]byte // audio packets
	Long    []bool   // per audio packet: long block
	modes   []bool   // block flag per mode
}

// Vendor is written into the comment header.
const Vendor = "owexplorer (Wwise Vorbis rebuilt)"

// Convert rebuilds a WEM's packets. The WEM's packets are {u16 size, bytes}; the setup packet is Vorbis' setup with
// the codebooks replaced by library ids and every type / reserved field that has one legal value dropped; with
// ModPackets the audio packets lack the packet type bit and the long window's previous / next flags.
func Convert(b []byte, lib *Codebooks) (*Vorbis, error) {
	h, err := ParseWEM(b)
	if err != nil {
		return nil, err
	}
	data := b[h.DataOffset : h.DataOffset+h.DataSize]
	v := &Vorbis{Header: h}
	v.ID = idHeader(h)
	v.Comment = commentHeader()
	setup, _, err := packetAt(data, h.SetupOffset)
	if err != nil {
		return nil, fmt.Errorf("audio: setup packet: %w", err)
	}
	if v.Setup, v.modes, err = rebuildSetup(setup, h.Channels, lib); err != nil {
		return nil, err
	}
	modeBits := ilog(uint32(len(v.modes) - 1))
	prevLong := false
	for off := h.AudioOffset; off < len(data); {
		p, next, err := packetAt(data, off)
		if err != nil {
			return nil, fmt.Errorf("audio: packet at 0x%x: %w", off, err)
		}
		off = next
		if len(p) == 0 {
			continue
		}
		if !h.ModPackets {
			// Not seen in 0.8 (every WEM has ModPackets); kept for completeness.
			m := 0
			if len(p) > 0 {
				m = int((&bitReader{b: p[:1]}).read(1+modeBits) >> 1)
			}
			v.Packets = append(v.Packets, p)
			v.Long = append(v.Long, m < len(v.modes) && v.modes[m])
			continue
		}
		r := &bitReader{b: p}
		w := &bitWriter{}
		w.write(0, 1) // audio packet
		mode := int(r.read(modeBits))
		if mode >= len(v.modes) {
			return nil, fmt.Errorf("audio: packet at 0x%x: mode %d of %d", off, mode, len(v.modes))
		}
		w.write(uint32(mode), modeBits)
		long := v.modes[mode]
		if long {
			nextLong := false
			if q, _, err := packetAt(data, next); err == nil && len(q) > 0 {
				if nm := int((&bitReader{b: q}).read(modeBits)); nm < len(v.modes) {
					nextLong = v.modes[nm]
				}
			}
			w.flag(prevLong)
			w.flag(nextLong)
		}
		prevLong = long
		for r.left() > 0 {
			n := min(r.left(), 32)
			w.write(r.read(n), n)
		}
		v.Packets = append(v.Packets, w.b)
		v.Long = append(v.Long, long)
	}
	return v, nil
}

// packetAt reads a {u16 size, bytes} packet; next is the offset after it.
func packetAt(data []byte, off int) (p []byte, next int, err error) {
	if off+2 > len(data) {
		return nil, 0, errBits
	}
	n := int(binary.LittleEndian.Uint16(data[off:]))
	if off+2+n > len(data) {
		return nil, 0, fmt.Errorf("packet of %d bytes past the end", n)
	}
	return data[off+2 : off+2+n], off + 2 + n, nil
}

func vorbisHeader(t byte) *bitWriter {
	w := &bitWriter{}
	w.write(uint32(t), 8)
	for _, c := range []byte("vorbis") {
		w.write(uint32(c), 8)
	}
	return w
}

func idHeader(h *WEM) []byte {
	w := vorbisHeader(1)
	w.write(0, 32)
	w.write(uint32(h.Channels), 8)
	w.write(uint32(h.SampleRate), 32)
	w.write(0, 32)
	w.write(uint32(h.AvgBytes*8), 32)
	w.write(0, 32)
	w.write(uint32(ilog(uint32(h.Blocksize0))-1), 4)
	w.write(uint32(ilog(uint32(h.Blocksize1))-1), 4)
	w.write(1, 1)
	return w.b
}

func commentHeader() []byte {
	w := vorbisHeader(3)
	w.write(uint32(len(Vendor)), 32)
	for _, c := range []byte(Vendor) {
		w.write(uint32(c), 8)
	}
	w.write(0, 32) // no user comments
	w.write(1, 1)
	return w.b
}

// rebuildSetup expands the stripped setup packet (Vorbis I §4.2.4) and returns the block flag of every mode.
func rebuildSetup(p []byte, channels int, lib *Codebooks) ([]byte, []bool, error) {
	if lib == nil {
		return nil, nil, fmt.Errorf("audio: no codebook library")
	}
	r := &bitReader{b: p}
	w := vorbisHeader(5)
	copyBits := func(n int) uint32 {
		x := r.read(n)
		w.write(x, n)
		return x
	}
	books := int(copyBits(8)) + 1
	for range books {
		id := int(r.read(10))
		if err := lib.rebuild(id, w); err != nil {
			return nil, nil, err
		}
	}
	w.write(0, 6)  // time domain transforms: 1
	w.write(0, 16) // its type: 0
	floors := int(copyBits(6)) + 1
	for range floors {
		w.write(1, 16) // floor type 1
		parts := int(copyBits(5))
		classes := make([]int, parts)
		maxClass := -1
		for i := range classes {
			classes[i] = int(copyBits(4))
			maxClass = max(maxClass, classes[i])
		}
		dims := make([]int, maxClass+1)
		for c := range dims {
			dims[c] = int(copyBits(3)) + 1
			sub := copyBits(2)
			if sub != 0 {
				copyBits(8)
			}
			for range 1 << sub {
				copyBits(8)
			}
		}
		copyBits(2)
		rangeBits := int(copyBits(4))
		for _, c := range classes {
			for range dims[c] {
				copyBits(rangeBits)
			}
		}
	}
	residues := int(copyBits(6)) + 1
	for range residues {
		t := r.read(2)
		if t > 2 {
			return nil, nil, fmt.Errorf("audio: residue type %d", t)
		}
		w.write(t, 16)
		copyBits(24)
		copyBits(24)
		copyBits(24)
		classifications := int(copyBits(6)) + 1
		copyBits(8)
		cascade := make([]uint32, classifications)
		for i := range cascade {
			low := copyBits(3)
			high := uint32(0)
			if copyBits(1) != 0 {
				high = copyBits(5)
			}
			cascade[i] = high<<3 | low
		}
		for _, c := range cascade {
			for k := range 8 {
				if c&(1<<k) != 0 {
					copyBits(8)
				}
			}
		}
	}
	mappings := int(copyBits(6)) + 1
	chBits := ilog(uint32(channels - 1))
	for range mappings {
		w.write(0, 16) // mapping type 0
		submaps := 1
		if copyBits(1) != 0 {
			submaps = int(copyBits(4)) + 1
		}
		if copyBits(1) != 0 {
			steps := int(copyBits(8)) + 1
			for range steps {
				copyBits(chBits)
				copyBits(chBits)
			}
		}
		if copyBits(2) != 0 {
			return nil, nil, fmt.Errorf("audio: mapping reserved bits set")
		}
		if submaps > 1 {
			for range channels {
				copyBits(4)
			}
		}
		for range submaps {
			copyBits(8)
			copyBits(8)
			copyBits(8)
		}
	}
	modeCount := int(copyBits(6)) + 1
	modes := make([]bool, modeCount)
	for i := range modes {
		modes[i] = copyBits(1) != 0
		w.write(0, 16)
		w.write(0, 16)
		copyBits(8)
	}
	w.write(1, 1)
	if r.err != nil {
		return nil, nil, fmt.Errorf("audio: setup packet: %w", r.err)
	}
	if (r.pos+7)/8 != len(p) {
		return nil, nil, fmt.Errorf("audio: setup packet: %d bits used of %d bytes", r.pos, len(p))
	}
	return w.b, modes, nil
}
