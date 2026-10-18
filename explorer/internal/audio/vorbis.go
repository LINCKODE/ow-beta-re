package audio

import (
	"errors"
	"fmt"
	"math"
)

// A Vorbis I decoder (https://xiph.org/vorbis/doc/Vorbis_I_spec.html) for the packets Convert rebuilds: floor 1,
// residues 0–2, mapping 0. Written from the specification; no third-party code.

type codebook struct {
	dims, entries int
	lengths       []uint8
	codes         []uint32 // codeword of each entry, MSB first
	fast          []int32  // fastBits bits of the stream (LSB first) → entry<<5 | length, -1 = longer / invalid
	tree          [][2]int32
	single        int // the only used entry of a one-entry book, else -1
	values        []float32
}

const fastBits = 10

type floor1 struct {
	partClass   []int
	classDims   []int
	classSub    []int
	classMaster []int
	subBooks    [][]int // -1 = none
	mult        int
	xs          []int // X list
	sorted      []int // indices of xs in increasing order
	low, high   []int // neighbours (§9.2.4 / §9.2.5)
}

type residue struct {
	kind            int
	begin, end      int
	partSize        int
	classifications int
	classbook       int
	books           [][8]int // -1 = none
}

type mapping struct {
	mux         []int
	submapFloor []int
	submapResid []int
	couplingMag []int
	couplingAng []int
}

type mode struct {
	long    bool
	mapping int
}

type decoder struct {
	channels, rate int
	bs             [2]int
	books          []codebook
	floors         []floor1
	residues       []residue
	maps           []mapping
	modes          []mode
	modeBits       int
	imdct          [2]*imdct
	win            map[[3]int][]float32
	prev           [][]float32 // right half of the previous windowed block
	prevN          int
	started        bool
}

// ErrUnsupported: a Vorbis feature 0.8's WEMs don't use (floor 0, …).
var ErrUnsupported = errors.New("audio: unsupported Vorbis feature")

// Decode decodes the rebuilt packets to one float32 slice per channel, trimmed to the WEM's sample count.
func (v *Vorbis) Decode() ([][]float32, error) {
	d, err := newDecoder(v.ID, v.Setup)
	if err != nil {
		return nil, err
	}
	out := make([][]float32, d.channels)
	for i := range out {
		out[i] = make([]float32, 0, v.Header.Samples+2048)
	}
	for i, p := range v.Packets {
		if err := d.packet(p, out); err != nil {
			return out, fmt.Errorf("audio: packet %d: %w", i, err)
		}
	}
	if n := v.Header.Samples; n >= 0 {
		for c := range out {
			if len(out[c]) > n {
				out[c] = out[c][:n]
			}
		}
	}
	return out, nil
}

func newDecoder(id, setup []byte) (*decoder, error) {
	d := &decoder{win: map[[3]int][]float32{}}
	r := &bitReader{b: id}
	if r.read(8) != 1 {
		return nil, errors.New("audio: not an identification header")
	}
	r.pos += 48 + 32
	d.channels = int(r.read(8))
	d.rate = int(r.read(32))
	r.pos += 96
	d.bs[0] = 1 << r.read(4)
	d.bs[1] = 1 << r.read(4)
	if d.channels == 0 || d.bs[0] < 64 || d.bs[1] < d.bs[0] || d.bs[1] > 8192 {
		return nil, errors.New("audio: bad identification header")
	}
	d.imdct[0], d.imdct[1] = newIMDCT(d.bs[0]), newIMDCT(d.bs[1])
	return d, d.parseSetup(setup)
}

func (d *decoder) parseSetup(b []byte) error {
	r := &bitReader{b: b}
	if r.read(8) != 5 {
		return errors.New("audio: not a setup header")
	}
	r.pos += 48
	d.books = make([]codebook, r.read(8)+1)
	for i := range d.books {
		if err := d.books[i].parse(r); err != nil {
			return fmt.Errorf("audio: codebook %d: %w", i, err)
		}
	}
	for range r.read(6) + 1 {
		if r.read(16) != 0 {
			return errors.New("audio: time domain type")
		}
	}
	d.floors = make([]floor1, r.read(6)+1)
	for i := range d.floors {
		if t := r.read(16); t != 1 {
			return fmt.Errorf("%w: floor type %d", ErrUnsupported, t)
		}
		if err := d.floors[i].parse(r, len(d.books)); err != nil {
			return err
		}
	}
	d.residues = make([]residue, r.read(6)+1)
	for i := range d.residues {
		re := &d.residues[i]
		re.kind = int(r.read(16))
		if re.kind > 2 {
			return fmt.Errorf("audio: residue type %d", re.kind)
		}
		re.begin, re.end = int(r.read(24)), int(r.read(24))
		re.partSize = int(r.read(24)) + 1
		re.classifications = int(r.read(6)) + 1
		re.classbook = int(r.read(8))
		if re.classbook >= len(d.books) {
			return errors.New("audio: residue classbook")
		}
		casc := make([]int, re.classifications)
		for j := range casc {
			casc[j] = int(r.read(3))
			if r.bit() {
				casc[j] |= int(r.read(5)) << 3
			}
		}
		re.books = make([][8]int, re.classifications)
		for j := range casc {
			for k := range 8 {
				re.books[j][k] = -1
				if casc[j]&(1<<k) != 0 {
					re.books[j][k] = int(r.read(8))
					if re.books[j][k] >= len(d.books) || d.books[re.books[j][k]].values == nil {
						return errors.New("audio: residue book")
					}
				}
			}
		}
	}
	d.maps = make([]mapping, r.read(6)+1)
	for i := range d.maps {
		m := &d.maps[i]
		if r.read(16) != 0 {
			return fmt.Errorf("%w: mapping type", ErrUnsupported)
		}
		submaps := 1
		if r.bit() {
			submaps = int(r.read(4)) + 1
		}
		if r.bit() {
			steps := int(r.read(8)) + 1
			bits := ilog(uint32(d.channels - 1))
			for range steps {
				mg, an := int(r.read(bits)), int(r.read(bits))
				if mg == an || mg >= d.channels || an >= d.channels {
					return errors.New("audio: coupling channels")
				}
				m.couplingMag = append(m.couplingMag, mg)
				m.couplingAng = append(m.couplingAng, an)
			}
		}
		if r.read(2) != 0 {
			return errors.New("audio: mapping reserved")
		}
		m.mux = make([]int, d.channels)
		if submaps > 1 {
			for c := range m.mux {
				m.mux[c] = int(r.read(4))
				if m.mux[c] >= submaps {
					return errors.New("audio: mapping mux")
				}
			}
		}
		for range submaps {
			r.read(8)
			f, re := int(r.read(8)), int(r.read(8))
			if f >= len(d.floors) || re >= len(d.residues) {
				return errors.New("audio: submap floor / residue")
			}
			m.submapFloor = append(m.submapFloor, f)
			m.submapResid = append(m.submapResid, re)
		}
	}
	d.modes = make([]mode, r.read(6)+1)
	for i := range d.modes {
		d.modes[i].long = r.bit()
		r.read(32)
		d.modes[i].mapping = int(r.read(8))
		if d.modes[i].mapping >= len(d.maps) {
			return errors.New("audio: mode mapping")
		}
	}
	d.modeBits = ilog(uint32(len(d.modes) - 1))
	if !r.bit() || r.err != nil {
		return errors.New("audio: setup header framing")
	}
	return nil
}

func float32Unpack(x uint32) float32 {
	m := float64(x & 0x1fffff)
	e := int((x & 0x7fe00000) >> 21)
	if x&0x80000000 != 0 {
		m = -m
	}
	return float32(math.Ldexp(m, e-788))
}

func (c *codebook) parse(r *bitReader) error {
	if r.read(24) != 0x564342 {
		return errors.New("sync pattern")
	}
	c.dims, c.entries = int(r.read(16)), int(r.read(24))
	c.lengths = make([]uint8, c.entries)
	if r.bit() { // ordered
		cur := 0
		l := int(r.read(5)) + 1
		for cur < c.entries {
			n := int(r.read(ilog(uint32(c.entries - cur))))
			if cur+n > c.entries || l > 32 {
				return errors.New("ordered lengths")
			}
			for i := cur; i < cur+n; i++ {
				c.lengths[i] = uint8(l)
			}
			cur += n
			l++
		}
	} else {
		sparse := r.bit()
		for i := range c.lengths {
			if !sparse || r.bit() {
				c.lengths[i] = uint8(r.read(5)) + 1
			}
		}
	}
	switch lt := r.read(4); lt {
	case 0:
	case 1, 2:
		mn, delta := float32Unpack(r.read(32)), float32Unpack(r.read(32))
		vbits := int(r.read(4)) + 1
		seq := r.bit()
		n := c.entries * c.dims
		if lt == 1 {
			n = int(quantvals(uint32(c.entries), uint32(c.dims)))
		}
		mult := make([]uint32, n)
		for i := range mult {
			mult[i] = r.read(vbits)
		}
		if r.err != nil {
			return r.err
		}
		c.values = make([]float32, c.entries*c.dims)
		for e := range c.entries {
			last := float32(0)
			div := 1
			for j := range c.dims {
				var m uint32
				if lt == 1 {
					m = mult[(e/div)%n]
					div *= n
				} else {
					m = mult[e*c.dims+j]
				}
				v := float32(m)*delta + mn + last
				if seq {
					last = v
				}
				c.values[e*c.dims+j] = v
			}
		}
	default:
		return fmt.Errorf("lookup type %d", lt)
	}
	if r.err != nil {
		return r.err
	}
	return c.build()
}

// build assigns the codewords (§3.2.1: each entry takes the lowest free codeword of its length, in entry order).
func (c *codebook) build() error {
	c.codes = make([]uint32, c.entries)
	c.single = -1
	used := 0
	for i, l := range c.lengths {
		if l > 0 {
			used++
			c.single = i
		}
	}
	if used != 1 {
		c.single = -1
	}
	var marker [33]uint32
	for i, l := range c.lengths {
		if l == 0 {
			continue
		}
		entry := marker[l]
		if l < 32 && entry>>l != 0 {
			return errors.New("overspecified codebook")
		}
		c.codes[i] = entry
		for j := int(l); j > 0; j-- {
			if marker[j]&1 != 0 {
				if j == 1 {
					marker[1]++
				} else {
					marker[j] = marker[j-1] << 1
				}
				break
			}
			marker[j]++
		}
		for j := int(l) + 1; j < 33; j++ {
			if marker[j]>>1 == entry {
				entry = marker[j]
				marker[j] = marker[j-1] << 1
			} else {
				break
			}
		}
	}
	c.fast = make([]int32, 1<<fastBits)
	for i := range c.fast {
		c.fast[i] = -1
	}
	c.tree = [][2]int32{{-1, -1}}
	for e, l := range c.lengths {
		if l == 0 {
			continue
		}
		rev := reverse(c.codes[e], int(l))
		if l <= fastBits {
			for k := rev; k < 1<<fastBits; k += 1 << l {
				c.fast[k] = int32(e)<<5 | int32(l)
			}
		}
		node := 0
		for b := int(l) - 1; b >= 0; b-- {
			bit := c.codes[e] >> uint(b) & 1
			if b == 0 {
				c.tree[node][bit] = -2 - int32(e)
				break
			}
			nx := c.tree[node][bit]
			if nx < 0 {
				nx = int32(len(c.tree))
				c.tree[node][bit] = nx
				c.tree = append(c.tree, [2]int32{-1, -1})
			}
			node = int(nx)
		}
	}
	return nil
}

func reverse(v uint32, n int) uint32 {
	var r uint32
	for range n {
		r = r<<1 | v&1
		v >>= 1
	}
	return r
}

// decode reads one entry; -1 at the end of the packet or on an invalid codeword.
func (c *codebook) decode(r *bitReader) int {
	if c.single >= 0 {
		r.read(int(c.lengths[c.single]))
		if r.err != nil {
			return -1
		}
		return c.single
	}
	if r.left() >= fastBits {
		if f := c.fast[r.peek(fastBits)]; f >= 0 {
			r.pos += int(f & 31)
			return int(f >> 5)
		}
	}
	node := int32(0)
	for {
		if r.left() <= 0 {
			r.err = errBits
			return -1
		}
		nx := c.tree[node][r.read(1)]
		if nx == -1 {
			return -1
		}
		if nx < -1 {
			return int(-2 - nx)
		}
		node = nx
	}
}

func (c *codebook) vector(r *bitReader) []float32 {
	e := c.decode(r)
	if e < 0 {
		return nil
	}
	return c.values[e*c.dims : (e+1)*c.dims]
}

func (f *floor1) parse(r *bitReader, books int) error {
	f.partClass = make([]int, r.read(5))
	maxClass := -1
	for i := range f.partClass {
		f.partClass[i] = int(r.read(4))
		maxClass = max(maxClass, f.partClass[i])
	}
	n := maxClass + 1
	f.classDims, f.classSub, f.classMaster = make([]int, n), make([]int, n), make([]int, n)
	f.subBooks = make([][]int, n)
	for c := range n {
		f.classDims[c] = int(r.read(3)) + 1
		f.classSub[c] = int(r.read(2))
		if f.classSub[c] != 0 {
			f.classMaster[c] = int(r.read(8))
			if f.classMaster[c] >= books {
				return errors.New("audio: floor masterbook")
			}
		}
		f.subBooks[c] = make([]int, 1<<f.classSub[c])
		for k := range f.subBooks[c] {
			f.subBooks[c][k] = int(r.read(8)) - 1
			if f.subBooks[c][k] >= books {
				return errors.New("audio: floor subclass book")
			}
		}
	}
	f.mult = int(r.read(2)) + 1
	rb := int(r.read(4))
	f.xs = []int{0, 1 << rb}
	for _, c := range f.partClass {
		for range f.classDims[c] {
			f.xs = append(f.xs, int(r.read(rb)))
		}
	}
	if len(f.xs) > 65 {
		return errors.New("audio: floor with more than 65 points")
	}
	f.sorted = make([]int, len(f.xs))
	for i := range f.sorted {
		f.sorted[i] = i
	}
	for i := 1; i < len(f.sorted); i++ { // insertion sort (≤ 65)
		for j := i; j > 0 && f.xs[f.sorted[j]] < f.xs[f.sorted[j-1]]; j-- {
			f.sorted[j], f.sorted[j-1] = f.sorted[j-1], f.sorted[j]
		}
	}
	f.low, f.high = make([]int, len(f.xs)), make([]int, len(f.xs))
	for i := 2; i < len(f.xs); i++ {
		lo, hi := 0, 1
		for j := range i {
			if f.xs[j] < f.xs[i] && f.xs[j] > f.xs[lo] {
				lo = j
			}
			if f.xs[j] > f.xs[i] && f.xs[j] < f.xs[hi] {
				hi = j
			}
		}
		f.low[i], f.high[i] = lo, hi
	}
	return r.err
}

// decodeFloor reads a floor's Y values (§7.2.3); ok = false: the channel is unused in this packet.
func (d *decoder) decodeFloor(f *floor1, r *bitReader) (ys []int, ok bool) {
	if !r.bit() {
		return nil, false
	}
	rng := [4]int{256, 128, 86, 64}[f.mult-1]
	bits := ilog(uint32(rng - 1))
	ys = make([]int, len(f.xs))
	ys[0], ys[1] = int(r.read(bits)), int(r.read(bits))
	off := 2
	for _, class := range f.partClass {
		cdim, cbits := f.classDims[class], f.classSub[class]
		csub := 1<<cbits - 1
		cval := 0
		if cbits > 0 {
			cval = d.books[f.classMaster[class]].decode(r)
			if cval < 0 {
				return nil, false
			}
		}
		for range cdim {
			book := f.subBooks[class][cval&csub]
			cval >>= cbits
			if book >= 0 {
				v := d.books[book].decode(r)
				if v < 0 {
					return nil, false
				}
				ys[off] = v
			}
			off++
		}
	}
	if r.err != nil {
		return nil, false
	}
	return ys, true
}

func renderPoint(x0, y0, x1, y1, x int) int {
	dy := y1 - y0
	adx := x1 - x0
	ady := dy
	if ady < 0 {
		ady = -ady
	}
	off := ady * (x - x0) / adx
	if dy < 0 {
		return y0 - off
	}
	return y0 + off
}

func renderLine(x0, y0, x1, y1 int, out []float32) {
	dy := y1 - y0
	adx := x1 - x0
	ady := dy
	if ady < 0 {
		ady = -ady
	}
	base := dy / adx
	sy := base + 1
	if dy < 0 {
		sy = base - 1
	}
	ab := base
	if ab < 0 {
		ab = -ab
	}
	ady -= ab * adx
	y := y0
	errv := 0
	if x0 < len(out) {
		out[x0] = invDB[clampY(y)]
	}
	for x := x0 + 1; x < x1 && x < len(out); x++ {
		errv += ady
		if errv >= adx {
			errv -= adx
			y += sy
		} else {
			y += base
		}
		out[x] = invDB[clampY(y)]
	}
}

func clampY(y int) int { return max(0, min(255, y)) }

// synthFloor computes the floor curve (§7.2.4) into out (n/2 values).
func (f *floor1) synth(ys []int, out []float32) {
	rng := [4]int{256, 128, 86, 64}[f.mult-1]
	n := len(f.xs)
	final := make([]int, n)
	step2 := make([]bool, n)
	step2[0], step2[1] = true, true
	final[0], final[1] = ys[0], ys[1]
	for i := 2; i < n; i++ {
		lo, hi := f.low[i], f.high[i]
		pred := renderPoint(f.xs[lo], final[lo], f.xs[hi], final[hi], f.xs[i])
		val := ys[i]
		highroom, lowroom := rng-pred, pred
		room := lowroom * 2
		if highroom < lowroom {
			room = highroom * 2
		}
		if val != 0 {
			step2[lo], step2[hi], step2[i] = true, true, true
			switch {
			case val >= room && highroom > lowroom:
				final[i] = val - lowroom + pred
			case val >= room:
				final[i] = pred - val + highroom - 1
			case val&1 != 0:
				final[i] = pred - (val+1)/2
			default:
				final[i] = pred + val/2
			}
		} else {
			final[i] = pred
		}
	}
	hx, lx := 0, 0
	ly := final[f.sorted[0]] * f.mult
	hy := ly
	for _, i := range f.sorted[1:] {
		if step2[i] {
			hy = final[i] * f.mult
			hx = f.xs[i]
			renderLine(lx, ly, hx, hy, out)
			lx, ly = hx, hy
		}
	}
	for x := hx; x < len(out); x++ {
		out[x] = invDB[clampY(hy)]
	}
}

// decodeResidue adds the residue vectors of the channels in vs (§8.6); skip marks do-not-decode channels.
func (d *decoder) decodeResidue(re *residue, r *bitReader, vs [][]float32, skip []bool) {
	n := len(vs[0])
	if re.kind == 2 {
		all := true
		for _, s := range skip {
			all = all && s
		}
		if all {
			return
		}
		ch := len(vs)
		inter := make([]float32, n*ch)
		d.residuePartitions(re, r, [][]float32{inter}, []bool{false}, 1)
		for i := range n {
			for c := range ch {
				vs[c][i] += inter[i*ch+c]
			}
		}
		return
	}
	d.residuePartitions(re, r, vs, skip, re.kind)
}

func (d *decoder) residuePartitions(re *residue, r *bitReader, vs [][]float32, skip []bool, format int) {
	size := len(vs[0])
	begin, end := min(re.begin, size), min(re.end, size)
	nread := end - begin
	if nread <= 0 {
		return
	}
	parts := nread / re.partSize
	cb := &d.books[re.classbook]
	cpc := cb.dims
	cls := make([][]int, len(vs))
	for j := range cls {
		cls[j] = make([]int, parts+cpc)
	}
	for pass := range 8 {
		for pc := 0; pc < parts; {
			if pass == 0 {
				for j := range vs {
					if skip[j] {
						continue
					}
					t := cb.decode(r)
					if t < 0 {
						return
					}
					for i := cpc - 1; i >= 0; i-- {
						cls[j][i+pc] = t % re.classifications
						t /= re.classifications
					}
				}
			}
			for i := 0; i < cpc && pc < parts; i++ {
				for j, v := range vs {
					if skip[j] {
						continue
					}
					book := re.books[cls[j][pc]][pass]
					if book < 0 {
						continue
					}
					bk := &d.books[book]
					off := begin + pc*re.partSize
					if format == 0 {
						step := re.partSize / bk.dims
						for k := range step {
							vals := bk.vector(r)
							if vals == nil {
								return
							}
							for m, x := range vals {
								v[off+k+m*step] += x
							}
						}
					} else {
						for k := 0; k < re.partSize; {
							vals := bk.vector(r)
							if vals == nil {
								return
							}
							for _, x := range vals {
								if k < re.partSize {
									v[off+k] += x
								}
								k++
							}
						}
					}
				}
				pc++
			}
		}
	}
}

// packet decodes one audio packet and appends the finished samples to out.
func (d *decoder) packet(p []byte, out [][]float32) error {
	r := &bitReader{b: p}
	if r.bit() {
		return errors.New("not an audio packet")
	}
	mi := int(r.read(d.modeBits))
	if mi >= len(d.modes) {
		return fmt.Errorf("mode %d", mi)
	}
	md := d.modes[mi]
	n := d.bs[0]
	prevLong, nextLong := false, false
	if md.long {
		n = d.bs[1]
		prevLong, nextLong = r.bit(), r.bit()
	}
	if r.err != nil {
		return r.err
	}
	m := &d.maps[md.mapping]
	half := n / 2
	floors := make([][]float32, d.channels)
	noResidue := make([]bool, d.channels)
	ys := make([][]int, d.channels)
	for c := range d.channels {
		f := &d.floors[m.submapFloor[m.mux[c]]]
		y, ok := d.decodeFloor(f, r)
		ys[c] = y
		noResidue[c] = !ok
	}
	unused := append([]bool(nil), noResidue...)
	for i := range m.couplingMag {
		a, b := m.couplingMag[i], m.couplingAng[i]
		if !noResidue[a] || !noResidue[b] {
			noResidue[a], noResidue[b] = false, false
		}
	}
	vecs := make([][]float32, d.channels)
	for c := range vecs {
		vecs[c] = make([]float32, half)
	}
	for s := range m.submapFloor {
		var vs [][]float32
		var skip []bool
		for c := range d.channels {
			if m.mux[c] == s {
				vs = append(vs, vecs[c])
				skip = append(skip, noResidue[c])
			}
		}
		if len(vs) > 0 {
			d.decodeResidue(&d.residues[m.submapResid[s]], r, vs, skip)
		}
	}
	for i := len(m.couplingMag) - 1; i >= 0; i-- {
		mg, an := vecs[m.couplingMag[i]], vecs[m.couplingAng[i]]
		for k := range half {
			M, A := mg[k], an[k]
			if M > 0 {
				if A > 0 {
					mg[k], an[k] = M, M-A
				} else {
					an[k], mg[k] = M, M+A
				}
			} else {
				if A > 0 {
					mg[k], an[k] = M, M+A
				} else {
					an[k], mg[k] = M, M-A
				}
			}
		}
	}
	w := d.window(n, prevLong, nextLong, md.long)
	cur := make([][]float32, d.channels)
	for c := range d.channels {
		cur[c] = make([]float32, n)
		if unused[c] {
			continue
		}
		fl := make([]float32, half)
		d.floors[m.submapFloor[m.mux[c]]].synth(ys[c], fl)
		floors[c] = fl
		for k := range half {
			vecs[c][k] *= fl[k]
		}
		d.imdct[b2i(md.long)].inverse(vecs[c], cur[c])
		for k := range n {
			cur[c][k] *= w[k]
		}
	}
	if d.started {
		pn := d.prevN
		count := pn/4 + n/4
		for c := range d.channels {
			for k := range count {
				var s float32
				if k < pn/2 {
					s = d.prev[c][k]
				}
				if ci := k - pn/4 + n/4; ci >= 0 && ci < n {
					s += cur[c][ci]
				}
				out[c] = append(out[c], s)
			}
		}
	}
	d.started = true
	d.prevN = n
	if d.prev == nil {
		d.prev = make([][]float32, d.channels)
	}
	for c := range d.channels {
		d.prev[c] = append(d.prev[c][:0], cur[c][half:]...)
	}
	return nil
}

func b2i(b bool) int {
	if b {
		return 1
	}
	return 0
}

// window is the Vorbis window of an n-sample block (§1.3.2): power-sine slopes, a long block's slope matching a short
// neighbour's width.
func (d *decoder) window(n int, prevLong, nextLong, long bool) []float32 {
	key := [3]int{n, b2i(prevLong || !long), b2i(nextLong || !long)}
	if w, ok := d.win[key]; ok {
		return w
	}
	w := make([]float32, n)
	ls, le, ln := 0, n/2, n/2
	if long && !prevLong {
		ls, le, ln = n/4-d.bs[0]/4, n/4+d.bs[0]/4, d.bs[0]/2
	}
	rs, re, rn := n/2, n, n/2
	if long && !nextLong {
		rs, re, rn = 3*n/4-d.bs[0]/4, 3*n/4+d.bs[0]/4, d.bs[0]/2
	}
	for i := range n {
		switch {
		case i < ls:
		case i < le:
			s := math.Sin((float64(i-ls) + 0.5) / float64(ln) * math.Pi / 2)
			w[i] = float32(math.Sin(math.Pi / 2 * s * s))
		case i < rs:
			w[i] = 1
		case i < re:
			s := math.Sin((float64(i-rs)+0.5)/float64(rn)*math.Pi/2 + math.Pi/2)
			w[i] = float32(math.Sin(math.Pi / 2 * s * s))
		}
	}
	d.win[key] = w
	return w
}

// invDB is floor1_inverse_dB_table (§10.1): a geometric series from 1.0649863e-07 to 1.
var invDB [256]float32

func init() {
	for i := range invDB {
		invDB[i] = float32(math.Exp(math.Log(1.0649863e-07) * float64(255-i) / 255))
	}
}
