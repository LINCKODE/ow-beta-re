package audio

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"os"
)

// The Wwise Vorbis encoder strips the codebooks out of a WEM's setup packet and stores 10-bit ids into a codebook
// library the runtime links. The 0.8 client's library is 598 packed codebooks, 71,991 bytes, in GameClientApp.exe's
// .rdata at RVA 0x142b7b0 (pointer table at RVA 0x1717870) [G]; it is byte-identical to the "aoTuV 603" library
// other Wwise tools ship [D]. docs/AssetFormats.md §5.3.
const (
	libraryCount = 598
	librarySize  = 71991
	librarySHA   = "de31ec8db2278fad2ff395514a826f1c050703cb2a4f136bd36cc1bb2914b038"
)

// librarySig is the start of the library (codebook 0), used to find it in the executable.
var librarySig = []byte{0x91, 0x00, 0x58, 0x53, 0x55, 0x75, 0x75, 0x00, 0x91, 0x01, 0x58, 0x00, 0x75, 0x75, 0x95, 0xb5}

// Codebooks is the codebook library: codebook i's packed bytes.
type Codebooks struct {
	books [][]byte
}

// LoadCodebooks finds the library in the client executable (the install's GameClientApp.exe).
func LoadCodebooks(exe string) (*Codebooks, error) {
	b, err := os.ReadFile(exe)
	if err != nil {
		return nil, err
	}
	i := bytes.Index(b, librarySig)
	if i < 0 || i+librarySize > len(b) {
		return nil, fmt.Errorf("audio: no Wwise codebook library in %s", exe)
	}
	return ParseCodebooks(b[i : i+librarySize])
}

// ParseCodebooks splits the library into its codebooks (each starts on a byte; its length is floor(bits / 8) + 1)
// and checks the whole against the known hash.
func ParseCodebooks(lib []byte) (*Codebooks, error) {
	if len(lib) < librarySize {
		return nil, errors.New("audio: codebook library too short")
	}
	lib = lib[:librarySize]
	sum := sha256.Sum256(lib)
	if hex.EncodeToString(sum[:]) != librarySHA {
		return nil, errors.New("audio: codebook library hash mismatch")
	}
	c := &Codebooks{}
	off := 0
	for len(c.books) < libraryCount {
		r := &bitReader{b: lib[off:]}
		if _, err := rebuildCodebook(r, nil); err != nil {
			return nil, fmt.Errorf("audio: library codebook %d: %w", len(c.books), err)
		}
		n := r.pos/8 + 1 // the packer always ends a codebook with at least one pad bit [D: all 598]
		c.books = append(c.books, lib[off:off+n])
		off += n
	}
	if off != librarySize {
		return nil, fmt.Errorf("audio: codebook library parsed to %d of %d bytes", off, librarySize)
	}
	return c, nil
}

// rebuild writes library codebook id in the standard Vorbis setup form.
func (c *Codebooks) rebuild(id int, w *bitWriter) error {
	if id < 0 || id >= len(c.books) {
		return fmt.Errorf("audio: codebook id %d out of range", id)
	}
	_, err := rebuildCodebook(&bitReader{b: c.books[id]}, w)
	return err
}

// rebuildCodebook reads a packed codebook and writes it (w may be nil) as a Vorbis I codebook (§3.2.1). The packed
// form drops the sync pattern, shrinks the dimension / entry fields to 4 / 14 bits and codeword lengths to
// codeword_length_length bits, and the lookup type to 1 bit.
func rebuildCodebook(r *bitReader, w *bitWriter) (int, error) {
	if w == nil {
		w = &bitWriter{}
	}
	dims := r.read(4)
	entries := r.read(14)
	w.write(0x564342, 24)
	w.write(dims, 16)
	w.write(entries, 24)
	ordered := r.bit()
	w.flag(ordered)
	if ordered {
		w.write(r.read(5), 5)
		for cur := uint32(0); cur < entries; {
			bits := ilog(entries - cur)
			n := r.read(bits)
			w.write(n, bits)
			cur += n
			if r.err != nil {
				return 0, r.err
			}
		}
	} else {
		cwll := int(r.read(3))
		sparse := r.bit()
		if cwll == 0 || cwll > 5 {
			return 0, fmt.Errorf("bad codeword length length %d", cwll)
		}
		w.flag(sparse)
		for range entries {
			present := true
			if sparse {
				present = r.bit()
				w.flag(present)
			}
			if present {
				w.write(r.read(cwll), 5)
			}
		}
	}
	lookup := r.read(1)
	w.write(lookup, 4)
	if lookup == 1 {
		w.write(r.read(32), 32)
		w.write(r.read(32), 32)
		vl := r.read(4)
		w.write(vl, 4)
		w.write(r.read(1), 1)
		q := quantvals(entries, dims)
		for range q {
			w.write(r.read(int(vl)+1), int(vl)+1)
		}
	}
	if r.err != nil {
		return 0, r.err
	}
	return int(entries), nil
}

// quantvals is lookup1_values (§9.2.3): the largest q with q^dims ≤ entries.
func quantvals(entries, dims uint32) uint32 {
	if dims == 0 || entries == 0 {
		return 0
	}
	q := uint32(1)
	for {
		p := uint64(1)
		for range dims {
			p *= uint64(q + 1)
			if p > uint64(entries) {
				return q
			}
		}
		q++
	}
}
