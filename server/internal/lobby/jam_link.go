package lobby

import (
	"crypto/rand"
	"encoding/binary"
	"fmt"
	"io"
)

// Protocol is a JAM protocol known to the client (see protocols_gen.go).
type Protocol struct {
	Name       string
	CRC        uint32
	Base, Max  uint16 // message IDs are Base..Max-1; on the wire, index = ID - Base
	ServerType uint16 // peer server type the client registers it for (13 = lobby)
}

func protocolByCRC(crc uint32) *Protocol {
	for i := range protocols {
		if protocols[i].CRC == crc {
			return &protocols[i]
		}
	}
	return nil
}

// ProtocolByName returns the protocol with the given name (as in protocols_gen.go), or nil.
func ProtocolByName(name string) *Protocol {
	for i := range protocols {
		if protocols[i].Name == name {
			return &protocols[i]
		}
	}
	return nil
}

// ProtocolByMsg returns the protocol whose message ID range contains id, or nil.
func ProtocolByMsg(id uint16) *Protocol {
	for i := range protocols {
		if p := &protocols[i]; id >= p.Base && id < p.Max && p.CRC != 0x11d82194 {
			return p
		}
	}
	return nil
}

// Protocols returns every protocol the client knows.
func Protocols() []Protocol { return append([]Protocol(nil), protocols...) }

// ServerTypeLobby is the peer type the client's lobby protocols are registered for.
const ServerTypeLobby = 13

// JamID is JAM's 128-bit service/object address. The client reads the peer's
// server type from bits 40-55 of the first u64 (FUN_7ff650f0a600) and offers
// only the protocols registered for that type in its negotiation message.
type JamID [16]byte

func NewJamID(serverType uint16) JamID {
	var id JamID
	rand.Read(id[:])
	lo := binary.LittleEndian.Uint64(id[:8])
	lo = lo&0xFF000000FFFFFFFF | uint64(serverType)<<40 // type in bits 40-55; keep the other bits random
	binary.LittleEndian.PutUint64(id[:8], lo)
	return id
}

// Frames after the handshake: [u24 BE length][u8 channel][u8 message index][payload].
// (Channels may add header fields via their protocol flags - FUN_7ff650f0a090 -
// but the negotiation channel does not.)

// writeFrame encrypts and writes one frame.
func writeFrame(w io.Writer, jam *Jam, channel, index byte, payload []byte) error {
	n := 2 + len(payload)
	if n > maxFrame {
		return fmt.Errorf("frame too large: %d", n)
	}
	frame := make([]byte, 0, 3+n)
	frame = append(frame, byte(n>>16), byte(n>>8), byte(n), channel, index)
	frame = append(frame, payload...)
	jam.Crypt(frame)
	_, err := w.Write(frame)
	return err
}

// Negotiation protocol (0x11d82194), as the client uses it (link flag +0x82 = 0):
//
//	C->S msg 1  u32 array: the sorted CRCs of the protocols registered for our
//	            server type; the client creates channels 1..n for them in that
//	            order and moves its link to state 3.
//	S->C msg 3  empty: accepted in state 3 (FUN_7ff650f0afe0) -> state 5, link up.
//	C->S msg 4  keepalive, every 10 s once the link is up (no reply needed).
//
// Echoing msg 1 back is wrong: the client's msg 1 handler (FUN_7ff650f0ac90)
// requires state 2 and closes the link in state 3. Msg 2 (protocol descriptors)
// belongs to the other negotiation mode (link flag != 0), which the client does not use.
const (
	negotiationMsgCRCList   = 1
	negotiationMsgAccept    = 3
	negotiationMsgKeepalive = 4
)

func parseCRCList(payload []byte) ([]uint32, error) {
	if len(payload) < 4 {
		return nil, fmt.Errorf("CRC list too short")
	}
	n := binary.LittleEndian.Uint32(payload)
	if uint64(len(payload)) != 4+4*uint64(n) {
		return nil, fmt.Errorf("CRC list of %d entries in %d bytes", n, len(payload))
	}
	crcs := make([]uint32, n)
	for i := range crcs {
		crcs[i] = binary.LittleEndian.Uint32(payload[4+4*i:])
	}
	return crcs, nil
}
