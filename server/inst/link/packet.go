package link

import (
	"crypto/hmac"
	"crypto/md5"
	"encoding/binary"
	"fmt"
)

// Every datagram (both directions) starts with the same 34-byte header; a control
// packet is only the header, a data packet has flag 1 in the header word and the
// payload (package wire) after it:
//
//	[12] MAC      HMAC-MD5(key, packet[12:])[:12]; key = 20600's 20-byte key
//	u32  word     connID<<4 | kind (0 control, 1 data)
//	u32  seq      the sender's packet sequence number (control and data share it)
//	u32  ack      the newest seq received from the peer
//	u64  mask     bit i: seq ack-1-i received too (FUN_7ff650e34a20)
//	u8   flags    SYN 1, ACK 2, FIN 4
//	u8   0xad
//
// Client side: UdpConn_SendControlPacket FUN_7ff650e34260 (every 250 ms from
// UdpConn_TickControl FUN_7ff650e32a40), UdpConn_HandleControlFlags FUN_7ff650e34470.
// The client drops datagrams under 34 bytes.
const (
	MACSize    = 12
	HeaderSize = 34
	marker     = 0xad

	FlagSYN = 1
	FlagACK = 2
	FlagFIN = 4

	kindData = 1
)

// Header is a parsed packet header.
type Header struct {
	ConnID uint32
	Kind   uint32 // low 4 bits of the word: 0 control, 1 data
	Seq    uint32
	Ack    uint32
	Mask   uint64
	Flags  byte
}

func (h Header) Data() bool { return h.Kind&kindData != 0 }

// ConnIDOf reads the connection ID of a datagram without checking anything else.
func ConnIDOf(b []byte) (uint32, bool) {
	if len(b) < MACSize+4 {
		return 0, false
	}
	return binary.LittleEndian.Uint32(b[MACSize:]) >> 4, true
}

// ParseHeader parses a datagram's header (the MAC is not checked: Verify).
func ParseHeader(b []byte) (Header, error) {
	if len(b) < HeaderSize {
		return Header{}, fmt.Errorf("datagram too short (%d bytes)", len(b))
	}
	if b[HeaderSize-1] != marker {
		return Header{}, fmt.Errorf("no 0xad marker")
	}
	word := binary.LittleEndian.Uint32(b[12:])
	return Header{
		ConnID: word >> 4,
		Kind:   word & 0xf,
		Seq:    binary.LittleEndian.Uint32(b[16:]),
		Ack:    binary.LittleEndian.Uint32(b[20:]),
		Mask:   binary.LittleEndian.Uint64(b[24:]),
		Flags:  b[32],
	}, nil
}

// AppendHeader appends h's unsigned header (everything after the MAC).
func AppendHeader(b []byte, h Header) []byte {
	b = binary.LittleEndian.AppendUint32(b, h.ConnID<<4|h.Kind)
	b = binary.LittleEndian.AppendUint32(b, h.Seq)
	b = binary.LittleEndian.AppendUint32(b, h.Ack)
	b = binary.LittleEndian.AppendUint64(b, h.Mask)
	return append(b, h.Flags, marker)
}

// MAC is the 12-byte packet MAC over everything after it.
func MAC(key, rest []byte) []byte {
	m := hmac.New(md5.New, key)
	m.Write(rest)
	return m.Sum(nil)[:MACSize]
}

// Verify reports whether the datagram's MAC matches key.
func Verify(key, b []byte) bool {
	return len(b) >= MACSize && hmac.Equal(b[:MACSize], MAC(key, b[MACSize:]))
}

// Sign prepends the MAC to rest (header + payload).
func Sign(key, rest []byte) []byte { return append(MAC(key, rest), rest...) }

// FlagNames renders control flags for logs, e.g. "SYN|ACK".
func FlagNames(f byte) string {
	s := ""
	for _, n := range []struct {
		bit  byte
		name string
	}{{FlagSYN, "SYN"}, {FlagACK, "ACK"}, {FlagFIN, "FIN"}} {
		if f&n.bit != 0 {
			if s != "" {
				s += "|"
			}
			s += n.name
		}
	}
	if s == "" {
		return "0"
	}
	return s
}

// window tracks the peer's packet sequence numbers the way the client's ack
// processing reads them: ack is the newest seq received, bit i of mask means seq
// ack-1-i was received too.
type window struct {
	ack  uint32
	mask uint64
	any  bool
}

func (w *window) add(seq uint32) {
	switch d := int32(seq - w.ack); {
	case !w.any:
		w.ack, w.mask, w.any = seq, 0, true
	case d > 0:
		if d >= 64 {
			w.mask = 0
		} else {
			w.mask = w.mask<<d | 1<<(d-1)
		}
		w.ack = seq
	case d < 0 && d >= -64:
		w.mask |= 1 << (-d - 1)
	}
}

// acked reports whether the peer's (ack, mask) covers seq.
func acked(ack uint32, mask uint64, seq uint32) bool {
	d := int32(ack - seq)
	return d == 0 || d > 0 && d <= 64 && mask>>(d-1)&1 != 0
}
