package lobby

import "encoding/binary"

// Jam is the lobby stream cipher, ported from earlier community lobby research.
// One instance per direction;
// the keystream runs continuously across all frames of a connection.
//
// Known-answer test (key 00 01 02 ... 1f), first bytes of keystream:
// f07e5df1c8d51c0d754da6ddde34cca01db0ed26334eb94ae277135a0764c6a6 - see jam_test.go.
type Jam struct {
	key     [8]uint32
	s       [16]uint32
	ctrB    uint32
	counter uint32
	ring    [32][8]uint32
	block   [32]byte
	pos     int
}

var (
	icIdx   = [8]int{0, 2, 4, 6, 1, 3, 5, 7}
	iaIdx   = [8]int{5, 7, 0, 2, 4, 6, 1, 3}
	ksWords = [8]int{14, 10, 6, 2, 15, 11, 7, 3}
)

// NewJam creates a cipher for a 32-byte key.
func NewJam(key [32]byte) *Jam {
	j := &Jam{counter: 1, pos: len(Jam{}.block)}
	for i := range j.key {
		j.key[i] = binary.LittleEndian.Uint32(key[4*i:])
	}
	j.absorb(j.key)
	j.absorb(j.key)
	j.permute(32)
	return j
}

// Crypt encrypts or decrypts data in place (XOR with the keystream).
func (j *Jam) Crypt(data []byte) {
	for i := range data {
		if j.pos == len(j.block) {
			j.squeeze()
		}
		data[i] ^= j.block[j.pos]
		j.pos++
	}
}

func (j *Jam) ringAt(offset uint32) *[8]uint32 {
	return &j.ring[((j.counter+offset)&0x3E0)>>5]
}

func rol(x uint32, n uint) uint32 { return x<<n | x>>(32-n) }

func (j *Jam) round(s [16]uint32) [16]uint32 {
	c := j.ctrB
	c1 := [4]uint32{(^s[4] | s[8]) ^ s[12], (^s[5] | s[9]) ^ s[13], (^s[6] | s[10]) ^ s[14], (^s[7] | s[11]) ^ s[15]}
	c2 := [4]uint32{(^s[0] | s[4]) ^ s[8], (^s[1] | s[5]) ^ s[9], (^s[2] | s[6]) ^ s[10], (^s[3] | s[7]) ^ s[11]}
	c3 := [4]uint32{(^s[13] | s[0]) ^ s[4], (^s[14] | s[1]) ^ s[5], (^s[15] | s[2]) ^ s[6], (^c | s[3]) ^ s[7]}
	c4 := [4]uint32{(^s[9] | s[13]) ^ s[0], (^s[10] | s[14]) ^ s[1], (^s[11] | s[15]) ^ s[2], (^s[12] | c) ^ s[3]}
	var o [16]uint32
	o[13], o[1], o[6], o[11] = rol(c1[0], 15), rol(c1[1], 4), rol(c1[2], 2), rol(c1[3], 9)
	o[10], o[15], o[3], o[8] = rol(c2[0], 23), rol(c2[1], 27), rol(c2[2], 8), rol(c2[3], 3)
	o[7], o[12], o[0], o[5] = rol(c3[0], 24), rol(c3[1], 1), rol(c3[2], 10), rol(c3[3], 28)
	o[4], o[9], o[14], o[2] = rol(c4[0], 6), rol(c4[1], 21), rol(c4[2], 13), rol(c4[3], 14)
	return o
}

// core mixes the round output with the 17th word (ctrB mix), as in lobbyserv.py.
func core(sp [16]uint32, mix uint32) [16]uint32 {
	var s17 [17]uint32
	copy(s17[:], sp[:])
	s17[16] = mix
	var out [16]uint32
	for i := range out {
		out[i] = sp[i] ^ s17[(i+1)%17] ^ s17[(i-4+17)%17]
	}
	return out
}

func (j *Jam) absorb(kw [8]uint32) {
	a, b := j.ringAt(0x20), j.ringAt(0x100)
	for k := 0; k < 8; k++ {
		t := a[icIdx[k]]
		a[icIdx[k]] = t ^ kw[k]
		b[iaIdx[k]] ^= t
	}
	sp := j.round(j.s)
	mix := (^j.s[8] | j.s[12]) ^ j.ctrB
	newCtrB := sp[0] ^ sp[12] ^ mix ^ 1
	c := j.ringAt(-0x200 & 0xFFFFFFFF)
	cr := core(sp, mix)
	extra := [16]uint32{kw[3], kw[7], c[6], c[7], kw[2], kw[6], c[4], c[5], kw[1], kw[5], c[2], c[3], kw[0], kw[4], c[0], c[1]}
	for i := range j.s {
		j.s[i] = cr[i] ^ extra[i]
	}
	j.ctrB = newCtrB
	j.counter += 0x20
}

func (j *Jam) permute(count int) {
	for ; count > 0; count-- {
		old := j.s
		mix := (^old[8] | old[12]) ^ j.ctrB
		sp := j.round(old)
		q, p := j.ringAt(0x20), j.ringAt(0x100)
		v41 := [4]uint32{q[0], q[1], q[2], q[3]}
		for k, w := range [4]uint32{old[12], old[13], old[8], old[9]} {
			q[k] ^= w
		}
		p[4] ^= v41[1]
		p[5] ^= v41[0]
		p[6] ^= v41[3]
		p[7] ^= v41[2]
		v42 := [4]uint32{q[4], q[5], q[6], q[7]}
		for k, w := range [4]uint32{old[4], old[5], old[0], old[1]} {
			q[4+k] ^= w
		}
		for k := 0; k < 4; k++ {
			p[k] ^= v42[k]
		}
		t, r := j.ringAt(-0x80&0xFFFFFFFF), j.ringAt(0x200)
		cr := core(sp, mix)
		ringw := [16]uint32{t[6], t[7], r[6], r[7], t[4], t[5], r[4], r[5], t[2], t[3], r[2], r[3], t[0], t[1], r[0], r[1]}
		for i := range j.s {
			j.s[i] = cr[i] ^ ringw[i]
		}
		j.ctrB = sp[0] ^ sp[12] ^ mix ^ 1
		j.counter += 0x20
	}
}

func (j *Jam) squeeze() {
	for i, w := range ksWords {
		binary.LittleEndian.PutUint32(j.block[4*i:], j.s[w])
	}
	j.pos = 0
	j.permute(1)
}
