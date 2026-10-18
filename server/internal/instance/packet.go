package instance

import (
	"crypto/hmac"
	"crypto/md5"
	"encoding/binary"
	"fmt"
)

// Packet layout, from the client's connect packets (34 bytes, ~4/s for ~10 s):
//
//	[12] MAC      HMAC-MD5(key, packet[12:])[:12] - confirmed on 231 captured packets
//	u32  header   connID<<4 | low 4 bits (0 in the connect packets: type/flags?)
//	u32  seq      counts up per packet
//	...  body     connect packets: 12 zero bytes + 01 ad
const (
	MACSize    = 12
	HeaderSize = MACSize + 8
)

type Packet struct {
	MAC    []byte
	Header uint32
	Seq    uint32
	Body   []byte
}

func (p Packet) ConnID() uint32 { return p.Header >> 4 }
func (p Packet) Flags() uint32  { return p.Header & 0xF }

func ParsePacket(b []byte) (Packet, error) {
	if len(b) < HeaderSize {
		return Packet{}, fmt.Errorf("packet too short (%d bytes)", len(b))
	}
	return Packet{
		MAC:    b[:MACSize],
		Header: binary.LittleEndian.Uint32(b[MACSize:]),
		Seq:    binary.LittleEndian.Uint32(b[MACSize+4:]),
		Body:   b[HeaderSize:],
	}, nil
}

// MAC is the 12-byte packet MAC over everything after it.
func MAC(key, rest []byte) []byte {
	m := hmac.New(md5.New, key)
	m.Write(rest)
	return m.Sum(nil)[:MACSize]
}

// Verify reports whether the packet's MAC matches key.
func Verify(key, packet []byte) bool {
	return len(packet) >= MACSize && hmac.Equal(packet[:MACSize], MAC(key, packet[MACSize:]))
}

// Sign prepends the MAC to rest (header + seq + body).
func Sign(key, rest []byte) []byte {
	return append(MAC(key, rest), rest...)
}

// Control packets (34 bytes) drive the connection handshake, which works like TCP.
// Client side: UdpConn_SendControlPacket FUN_7ff650e34260 (sent every 250 ms by
// UdpConn_TickControl FUN_7ff650e32a40) and UdpConn_HandleControlFlags FUN_7ff650e34470.
//
//	[12] MAC | u32 connID<<4 | u32 seq | u32 ack | u64 (conn+0xe0, 0 so far) | u8 flags | u8 0xad
//
// The client sends SYN while connecting, answers SYN|ACK with state "connected"
// (then sends ACK every 250 ms as a keepalive), FIN|ACK while closing. It
// drops the connection after 10 s without an answer while connecting.
const (
	ControlSize   = 34
	ControlMarker = 0xad

	FlagSYN = 1
	FlagACK = 2
	FlagFIN = 4
)

// ControlMsg is a parsed control packet.
type ControlMsg struct {
	Seq, Ack uint32
	X        uint64
	Flags    byte
}

// ParseControl reports whether b is a control packet and decodes it.
func ParseControl(b []byte) (ControlMsg, bool) {
	if len(b) != ControlSize || b[ControlSize-1] != ControlMarker {
		return ControlMsg{}, false
	}
	return ControlMsg{
		Seq:   binary.LittleEndian.Uint32(b[16:]),
		Ack:   binary.LittleEndian.Uint32(b[20:]),
		X:     binary.LittleEndian.Uint64(b[24:]),
		Flags: b[32],
	}, true
}

// ControlPacket builds a signed control packet.
func ControlPacket(key []byte, connID uint32, c ControlMsg) []byte {
	b := binary.LittleEndian.AppendUint32(nil, connID<<4)
	b = binary.LittleEndian.AppendUint32(b, c.Seq)
	b = binary.LittleEndian.AppendUint32(b, c.Ack)
	b = binary.LittleEndian.AppendUint64(b, c.X)
	b = append(b, c.Flags, ControlMarker)
	return Sign(key, b)
}

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
