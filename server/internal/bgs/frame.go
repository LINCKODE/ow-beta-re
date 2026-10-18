// Package bgs implements the Battle.net ("bgs") RPC transport spoken by the
// Overwatch 0.8 client on port 1119.
package bgs

import (
	"encoding/binary"
	"fmt"
	"io"

	"google.golang.org/protobuf/proto"

	protocol "owserver/gen/bgs/protocol"
)

// A frame on the wire is:
//
//	[u16 big-endian header length][Header protobuf][payload, Header.size bytes]
//
// This is the layout used by the public bgs emulators (TrinityCore bnetserver,
// Plasmawatch), and the one the 0.8 client speaks.

// ResponseServiceID is the Header.service_id used for all responses.
// The 0.8 client accepts responses with it (Connect, Logon, VerifyWebCredentials).
const ResponseServiceID = 0xFE

// maxPayload caps a single frame's payload to keep a bad length from exhausting memory.
const maxPayload = 16 << 20

type Frame struct {
	Header  *protocol.Header
	Payload []byte
}

func ReadFrame(r io.Reader) (*Frame, error) {
	var lenBuf [2]byte
	if _, err := io.ReadFull(r, lenBuf[:]); err != nil {
		return nil, err
	}
	headerBuf := make([]byte, binary.BigEndian.Uint16(lenBuf[:]))
	if _, err := io.ReadFull(r, headerBuf); err != nil {
		return nil, fmt.Errorf("read header: %w", err)
	}
	header := &protocol.Header{}
	if err := proto.Unmarshal(headerBuf, header); err != nil {
		return nil, fmt.Errorf("decode header: %w", err)
	}
	size := header.GetSize()
	if size > maxPayload {
		return nil, fmt.Errorf("payload size %d exceeds limit", size)
	}
	payload := make([]byte, size)
	if _, err := io.ReadFull(r, payload); err != nil {
		return nil, fmt.Errorf("read payload: %w", err)
	}
	return &Frame{Header: header, Payload: payload}, nil
}

// WriteFrame encodes msg (which may be nil for an empty body), sets header.Size
// accordingly and writes the frame.
func WriteFrame(w io.Writer, header *protocol.Header, msg proto.Message) error {
	var payload []byte
	if msg != nil {
		var err error
		if payload, err = proto.Marshal(msg); err != nil {
			return fmt.Errorf("encode payload: %w", err)
		}
	}
	header.Size = proto.Uint32(uint32(len(payload)))
	headerBuf, err := proto.Marshal(header)
	if err != nil {
		return fmt.Errorf("encode header: %w", err)
	}
	if len(headerBuf) > 0xFFFF {
		return fmt.Errorf("header too large: %d bytes", len(headerBuf))
	}
	buf := make([]byte, 2, 2+len(headerBuf)+len(payload))
	binary.BigEndian.PutUint16(buf, uint16(len(headerBuf)))
	buf = append(buf, headerBuf...)
	buf = append(buf, payload...)
	_, err = w.Write(buf)
	return err
}
