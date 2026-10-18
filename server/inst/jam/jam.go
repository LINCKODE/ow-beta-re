// Package jam is the JAM message layer of our servers: typed messages generated
// from the client's own protocol tables (systems/data/jam/catalogue.json, see
// jamgen), the binary codec, the protocol/channel tables, and the two framings the
// client uses: lobby frames on the TCP lobby link (lobby.go) and stream 0 of the
// instance data payload (stream.go).
//
// docs/JAM.md "Our implementation" describes the package.
package jam

//go:generate go run owserver/jamgen gen -catalogue ../../../systems/data/jam/catalogue.json -out messages_gen.go

import (
	"errors"
	"fmt"
	"sort"

	"owserver/inst/bits"
)

// Message is one JAM message. Encode appends it at the writer's bit position;
// Decode reads it from the reader's position and leaves the reader right after
// its last bit (JAM payloads aren't length-prefixed inside the instance stream:
// the layout delimits them).
type Message interface {
	ID() uint16
	Encode(w *bits.Writer)
	Decode(r *bits.Reader) error
}

// Raw is a message without a catalogued layout (or whose payload must be passed
// through untouched): Payload is written byte-aligned, then Bools as bits.
// Decoding a Raw takes the rest of the input, so a Raw can only be the last
// message of an instance packet.
type Raw struct {
	Id      uint16
	Payload []byte
	Bools   []bool
}

func (m *Raw) ID() uint16 { return m.Id }

func (m *Raw) Encode(w *bits.Writer) {
	w.Bytes(m.Payload)
	w.Bools(m.Bools...)
}

func (m *Raw) Decode(r *bits.Reader) error {
	m.Payload = append([]byte(nil), r.Rest()...)
	r.Seek(uint(len(r.Bytes())) * 8)
	return nil
}

// Protocol is a JAM protocol the client registers.
type Protocol struct {
	Name string
	CRC  uint32
	// Message IDs are Base..Max-1; on the wire the index is ID - Base.
	Base, Max uint16
	// Direction from the client's point of view: "in" (server -> client), "out"
	// (client -> server), "both" (negotiation) or "" (dev tools).
	Direction string
	// Transport: "lobby", "instance" or "" (not carried by our servers).
	Transport string
	// ServerType is the peer server type the client registers it for (13 = lobby).
	ServerType uint16
	// InstanceChannel is the channel on the instance stream, -1 if none.
	InstanceChannel int
}

// Protocols returns every protocol the client registers.
func Protocols() []Protocol { return append([]Protocol(nil), protocols...) }

// ProtocolOf returns the protocol a message ID belongs to.
func ProtocolOf(id uint16) (*Protocol, bool) {
	for i := range protocols {
		if p := &protocols[i]; id >= p.Base && id < p.Max {
			return p, true
		}
	}
	return nil, false
}

// ProtocolByName returns the protocol with the given name.
func ProtocolByName(name string) (*Protocol, bool) {
	for i := range protocols {
		if protocols[i].Name == name {
			return &protocols[i], true
		}
	}
	return nil, false
}

// Name returns the message's Go type name ("HeroLockIn"), or "" for an unknown ID.
func Name(id uint16) string { return messageNames[id] }

// Marshal encodes a message on its own, padded to a whole byte: a lobby frame's
// payload (and what lobbylogic passes to the gateway).
func Marshal(m Message) []byte {
	var w bits.Writer
	m.Encode(&w)
	w.Align()
	return append([]byte(nil), w.Buf()...)
}

// ErrTrailing is returned by Unmarshal when bytes are left after the message.
var ErrTrailing = errors.New("jam: trailing bytes after the message")

// Unmarshal decodes a whole lobby payload into a new message of the given ID
// (a *Raw if the ID is unknown). Every byte must be used.
func Unmarshal(id uint16, payload []byte) (Message, error) {
	m, ok := New(id)
	if !ok {
		return &Raw{Id: id, Payload: append([]byte(nil), payload...)}, nil
	}
	r := bits.NewReader(payload)
	if err := m.Decode(r); err != nil {
		return m, fmt.Errorf("%s (%d): %w", Name(id), id, err)
	}
	r.Align()
	if r.Left() > 0 {
		return m, fmt.Errorf("%s (%d): %w (%d)", Name(id), id, ErrTrailing, r.Left()/8)
	}
	return m, nil
}

// LobbyChannels is the channel table of a lobby link: channel 0 = Negotiation,
// then the protocols registered for server type 13 sorted by CRC (the client
// builds its list the same way, FUN_7ff650f0a600, and offers it in
// NegotiateProtocols).
func LobbyChannels() []*Protocol {
	var ps []*Protocol
	for i := range protocols {
		if protocols[i].ServerType == ServerTypeLobby {
			ps = append(ps, &protocols[i])
		}
	}
	sort.Slice(ps, func(i, j int) bool { return ps[i].CRC < ps[j].CRC })
	neg, _ := ProtocolByName("Negotiation")
	return append([]*Protocol{neg}, ps...)
}

// ServerTypeLobby is the server type of the lobby protocols.
const ServerTypeLobby = 13

// Dispatcher routes decoded messages to typed handlers.
//
//	var d jam.Dispatcher[*Conn]
//	jam.On(&d, func(c *Conn, m *jam.HeroLockIn) error { ... })
//	d.Dispatch(conn, msg)
type Dispatcher[C any] struct {
	handlers map[uint16]func(C, Message) error
	// Unhandled is called for messages without a handler (may be nil).
	Unhandled func(C, Message) error
}

// On registers the handler of message type M.
func On[M Message, C any](d *Dispatcher[C], f func(C, M) error) {
	var zero M
	if d.handlers == nil {
		d.handlers = map[uint16]func(C, Message) error{}
	}
	d.handlers[zero.ID()] = func(c C, m Message) error {
		t, ok := m.(M)
		if !ok {
			return fmt.Errorf("jam: message %d is a %T, not a %T", m.ID(), m, zero)
		}
		return f(c, t)
	}
}

// Dispatch calls the handler registered for m's ID.
func (d *Dispatcher[C]) Dispatch(c C, m Message) error {
	if h := d.handlers[m.ID()]; h != nil {
		return h(c, m)
	}
	if d.Unhandled != nil {
		return d.Unhandled(c, m)
	}
	return nil
}
