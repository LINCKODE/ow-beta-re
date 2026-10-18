package jam

import (
	"errors"
	"fmt"
)

// Lobby link framing (after the lobby handshake; the stream cipher around it is
// server/internal/lobby's Jam):
//
//	[u24 BE length][u8 channel][u8 index][payload]      length = 2 + len(payload)
//
// The channel's protocol flags could add header fields (FUN_7ff650f0a090: 0x08 a
// u32 request sequence, 0x10..0x100 an object ID); no protocol the client
// registers for the lobby sets them. Channels come from the negotiation: 0 is
// Negotiation, 1..n the protocols of the client's NegotiateProtocols CRC list in
// its order (= LobbyChannels()).

// MaxLobbyFrame is the largest frame length the gateway accepts.
const MaxLobbyFrame = 1<<24 - 1

// LobbyLink maps a negotiated lobby link's channels to protocols.
type LobbyLink struct {
	channels []*Protocol // by channel
	byCRC    map[uint32]uint8
}

// NewLobbyLink builds the channel table from the client's NegotiateProtocols
// list. CRCs the catalogue doesn't know get no protocol (their messages decode
// as *Raw).
func NewLobbyLink(crcs []uint32) *LobbyLink {
	neg, _ := ProtocolByName("Negotiation")
	l := &LobbyLink{channels: []*Protocol{neg}, byCRC: map[uint32]uint8{neg.CRC: 0}}
	for _, crc := range crcs {
		var p *Protocol
		for i := range protocols {
			if protocols[i].CRC == crc {
				p = &protocols[i]
			}
		}
		if p != nil {
			l.byCRC[crc] = uint8(len(l.channels))
		}
		l.channels = append(l.channels, p)
	}
	return l
}

// DefaultLobbyLink is the link a real client negotiates (LobbyChannels).
func DefaultLobbyLink() *LobbyLink {
	var crcs []uint32
	for _, p := range LobbyChannels()[1:] {
		crcs = append(crcs, p.CRC)
	}
	return NewLobbyLink(crcs)
}

// Channel returns the protocol on a channel (nil if none).
func (l *LobbyLink) Channel(ch uint8) *Protocol {
	if int(ch) < len(l.channels) {
		return l.channels[ch]
	}
	return nil
}

// ChannelOf returns the channel and index of a message ID on this link.
func (l *LobbyLink) ChannelOf(id uint16) (ch, idx uint8, ok bool) {
	p, ok := ProtocolOf(id)
	if !ok {
		return 0, 0, false
	}
	ch, ok = l.byCRC[p.CRC]
	return ch, uint8(id - p.Base), ok
}

// MessageID returns the message ID of a channel and index on this link.
func (l *LobbyLink) MessageID(ch, idx uint8) (uint16, bool) {
	p := l.Channel(ch)
	if p == nil {
		return 0, false
	}
	id := p.Base + uint16(idx)
	return id, id < p.Max
}

// Frame encodes m as a plaintext lobby frame.
func (l *LobbyLink) Frame(m Message) ([]byte, error) {
	ch, idx, ok := l.ChannelOf(m.ID())
	if !ok {
		return nil, fmt.Errorf("jam: message %d has no channel on this link", m.ID())
	}
	return AppendLobbyFrame(nil, ch, idx, Marshal(m))
}

// Parse decodes a plaintext frame (as SplitLobbyFrame returns it) into a message.
func (l *LobbyLink) Parse(ch, idx uint8, payload []byte) (Message, error) {
	id, ok := l.MessageID(ch, idx)
	if !ok {
		return nil, fmt.Errorf("jam: no message for channel %d index %d", ch, idx)
	}
	return Unmarshal(id, payload)
}

// AppendLobbyFrame appends one plaintext frame.
func AppendLobbyFrame(dst []byte, ch, idx uint8, payload []byte) ([]byte, error) {
	n := 2 + len(payload)
	if n > MaxLobbyFrame {
		return dst, fmt.Errorf("jam: lobby frame too large (%d)", n)
	}
	dst = append(dst, byte(n>>16), byte(n>>8), byte(n), ch, idx)
	return append(dst, payload...), nil
}

// ErrShortFrame means b doesn't hold a whole frame yet.
var ErrShortFrame = errors.New("jam: incomplete lobby frame")

// SplitLobbyFrame splits the first plaintext frame off b.
func SplitLobbyFrame(b []byte) (ch, idx uint8, payload, rest []byte, err error) {
	if len(b) < 3 {
		return 0, 0, nil, b, ErrShortFrame
	}
	n := int(b[0])<<16 | int(b[1])<<8 | int(b[2])
	if n < 2 {
		return 0, 0, nil, b, fmt.Errorf("jam: lobby frame length %d", n)
	}
	if len(b) < 3+n {
		return 0, 0, nil, b, ErrShortFrame
	}
	return b[3], b[4], b[5 : 3+n], b[3+n:], nil
}
