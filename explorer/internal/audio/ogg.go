package audio

import (
	"bytes"
	"encoding/binary"
)

var oggCRC [256]uint32

func init() {
	for i := range oggCRC {
		r := uint32(i) << 24
		for range 8 {
			if r&0x80000000 != 0 {
				r = r<<1 ^ 0x04c11db7
			} else {
				r <<= 1
			}
		}
		oggCRC[i] = r
	}
}

// Granules returns the granule position (samples per channel decoded so far) after each audio packet: the first
// packet only primes the overlap, each later one adds a quarter of the previous plus a quarter of its block size.
func (v *Vorbis) Granules() []int64 {
	g := make([]int64, len(v.Packets))
	var pos int64
	for i := range v.Packets {
		if i > 0 {
			pos += int64(v.blocksize(v.Long[i-1])/4 + v.blocksize(v.Long[i])/4)
		}
		g[i] = pos
	}
	return g
}

func (v *Vorbis) blocksize(long bool) int {
	if long {
		return v.Header.Blocksize1
	}
	return v.Header.Blocksize0
}

// Ogg writes the stream as an Ogg Vorbis file. The last page's granule is the WEM's sample count when that is
// shorter, which tells decoders to trim the end (Vorbis I §A.2).
func (v *Vorbis) Ogg() []byte {
	var out bytes.Buffer
	o := oggWriter{out: &out, serial: v.Header.UID}
	o.packet(v.ID, 0)
	o.flush(true, false)
	o.packet(v.Comment, 0)
	o.packet(v.Setup, 0)
	o.flush(false, false)
	g := v.Granules()
	for i, p := range v.Packets {
		gr := g[i]
		last := i == len(v.Packets)-1
		if last && int64(v.Header.Samples) < gr {
			gr = int64(v.Header.Samples)
		}
		o.packet(p, gr)
		if last {
			o.flush(false, true)
		} else if i == 0 || len(o.data) >= 4096 {
			// The first audio packet gets a page of its own (granule 0): with the whole stream on one page, a
			// decoder can't tell end trimming (a last granule below the decoded length) from start trimming, and
			// ffmpeg cuts the wrong end [D: 186 of 745 short WEMs before this].
			o.flush(false, false)
		}
	}
	if len(v.Packets) == 0 {
		o.flush(false, true)
	}
	return out.Bytes()
}

type oggWriter struct {
	out     *bytes.Buffer
	serial  uint32
	seq     uint32
	segs    []byte
	data    []byte
	granule int64
	cont    bool // the first packet on the next page continues from the previous page
}

// packet appends a packet; pages are cut at 255 segments.
func (o *oggWriter) packet(p []byte, granule int64) {
	for {
		n := min(len(p), 255)
		o.segs = append(o.segs, byte(n))
		o.data = append(o.data, p[:n]...)
		p = p[n:]
		if n < 255 {
			break
		}
		if len(o.segs) == 255 {
			o.page(false, false, -1)
			o.cont = true
		}
	}
	o.granule = granule
	if len(o.segs) == 255 {
		o.flush(false, false)
	}
}

func (o *oggWriter) flush(bos, eos bool) {
	if len(o.segs) == 0 && !eos {
		return
	}
	o.page(bos, eos, o.granule)
	o.cont = false
}

func (o *oggWriter) page(bos, eos bool, granule int64) {
	h := make([]byte, 27, 27+len(o.segs))
	copy(h, "OggS")
	var flags byte
	if o.cont {
		flags |= 1
	}
	if bos {
		flags |= 2
	}
	if eos {
		flags |= 4
	}
	h[5] = flags
	binary.LittleEndian.PutUint64(h[6:], uint64(granule))
	binary.LittleEndian.PutUint32(h[14:], o.serial)
	binary.LittleEndian.PutUint32(h[18:], o.seq)
	o.seq++
	h[26] = byte(len(o.segs))
	h = append(h, o.segs...)
	var crc uint32
	for _, b := range h {
		crc = crc<<8 ^ oggCRC[byte(crc>>24)^b]
	}
	for _, b := range o.data {
		crc = crc<<8 ^ oggCRC[byte(crc>>24)^b]
	}
	binary.LittleEndian.PutUint32(h[22:], crc)
	o.out.Write(h)
	o.out.Write(o.data)
	o.segs, o.data = o.segs[:0], o.data[:0]
}
