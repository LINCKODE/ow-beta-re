package lobbylogic

import (
	"encoding/binary"
	"encoding/hex"
	"fmt"
	"math"
	"strconv"
	"strings"

	"owserver/internal/lobby"
)

// !msg sends an arbitrary lobby message, for probing what each message does in the
// client's UI (docs/LobbyServer.md "Server-driven navigation"). The payload is built
// from tokens, concatenated in order:
//
//	bare hex       raw bytes (spaces/underscores allowed between tokens only)
//	u8:N  i8:N     1 byte (a run of consecutive bool fields packs into one byte, bit 0 first)
//	u16:N i16:N    2 bytes little-endian
//	u32:N i32:N    4 bytes (n:N is the same: a dynamic array's count)
//	u64:N          8 bytes
//	f32:X          IEEE float
//	id:A,B         an ID = two u64
//	str:TEXT       cstring (\s = space)
//
// Numbers take Go syntax (0x.., negative values for the signed forms). "empty" sends a
// message without fields. A payload the client can't decode makes it close the lobby
// link (the client then has to log in again), so check the fields in
// game/owbetaclient/Prometheus/jam_protocols.json first.

func encodeMsgTokens(tokens []string) ([]byte, error) {
	var b []byte
	for _, t := range tokens {
		kind, val, typed := strings.Cut(t, ":")
		if !typed {
			raw, err := hex.DecodeString(strings.TrimPrefix(t, "0x"))
			if err != nil {
				return nil, fmt.Errorf("%q: not hex and no type prefix", t)
			}
			b = append(b, raw...)
			continue
		}
		switch kind {
		case "str":
			b = append(b, strings.ReplaceAll(val, `\s`, " ")...)
			b = append(b, 0)
		case "f32":
			f, err := strconv.ParseFloat(val, 32)
			if err != nil {
				return nil, fmt.Errorf("%q: %w", t, err)
			}
			b = binary.LittleEndian.AppendUint32(b, math.Float32bits(float32(f)))
		case "id":
			x, y, ok := strings.Cut(val, ",")
			if !ok {
				return nil, fmt.Errorf("%q: want id:A,B", t)
			}
			for _, s := range []string{x, y} {
				v, err := strconv.ParseUint(s, 0, 64)
				if err != nil {
					return nil, fmt.Errorf("%q: %w", t, err)
				}
				b = binary.LittleEndian.AppendUint64(b, v)
			}
		default:
			size := map[string]int{"u8": 1, "i8": 1, "u16": 2, "i16": 2, "u32": 4, "i32": 4, "n": 4, "u64": 8}[kind]
			if size == 0 {
				return nil, fmt.Errorf("%q: unknown type %q", t, kind)
			}
			var v uint64
			if strings.HasPrefix(val, "-") {
				s, err := strconv.ParseInt(val, 0, size*8)
				if err != nil {
					return nil, fmt.Errorf("%q: %w", t, err)
				}
				v = uint64(s)
			} else {
				u, err := strconv.ParseUint(val, 0, size*8)
				if err != nil {
					return nil, fmt.Errorf("%q: %w", t, err)
				}
				v = u
			}
			b = binary.LittleEndian.AppendUint64(b, v)[:len(b)+size]
		}
	}
	return b, nil
}

func init() {
	commands["msg"] = command{`!msg <id> tokens|empty - send any ClientInLobby message (malformed = link drop!): hex, u8/i8/u16/i16/u32/i32/n/u64/f32:N, id:A,B, str:TEXT (\s = space)`, func(c *Ctx, a []string) error {
		if len(a) < 1 {
			return fmt.Errorf("usage: !msg <id> [tokens]")
		}
		id, err := strconv.ParseUint(a[0], 0, 16)
		if err != nil {
			return fmt.Errorf("message id: %w", err)
		}
		p := lobby.ProtocolByMsg(uint16(id))
		if p == nil {
			return fmt.Errorf("no protocol contains message %d", id)
		}
		if p.ServerType != lobby.ServerTypeLobby || !strings.HasPrefix(p.Name, "ClientInLobby") {
			return fmt.Errorf("%d is in %s: only ClientInLobby* messages go to the client", id, p.Name)
		}
		tokens := a[1:]
		empty := len(tokens) == 1 && tokens[0] == "empty"
		if empty {
			tokens = nil
		}
		payload, err := encodeMsgTokens(tokens)
		if err != nil {
			return err
		}
		// A payload the client can't decode closes the lobby link (measured: an empty
		// 20400 dropped the session), so an empty payload has to be asked for.
		if len(payload) == 0 && !empty {
			return fmt.Errorf("empty payload: add the fields, or \"empty\" for a message without fields")
		}
		if err := c.Send(p.Name, uint16(id), payload); err != nil {
			return err
		}
		c.Log.Info("!msg sent", "protocol", p.Name, "msg", id, "data", hex.EncodeToString(payload))
		return c.SystemChat(fmt.Sprintf("sent %s %d (%d bytes): %x", p.Name, id, len(payload), payload))
	}}
}
