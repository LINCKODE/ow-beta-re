// Package lobby implements the lobby gateway the 0.8 client reaches after the
// login server's referral (hostv4).
//
// A connection starts with the negotiation in handshake.go, after which the
// stream is JAM-encrypted in both directions and carries JAM frames:
//
//	[u24 big-endian length][u8 channel][u8 message index][payload]
//
// The gateway only handles the link itself (handshake, cipher, protocol
// negotiation). Every other message goes to the Hub, which hands it to Lua
// handlers and to the logic backend (see wire.go, hub.go). The backend can be
// restarted without the client noticing.
package lobby

import (
	"bufio"
	"context"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net"
	"strings"
	"sync"
)

type Server struct {
	Log *slog.Logger
	Hub *Hub
	// SessionSecret derives the lobby session keys; must match the login server's.
	SessionSecret []byte
	// ClientIDFirst puts the client's JamID before the server's in the handshake
	// trailer, which is the order the client expects (confirmed: with the server's
	// first, the client's negotiation offers only the negotiation protocol itself).
	ClientIDFirst bool
	// LogKeepalive logs the client's keepalives (negotiation message 4, every 10 s).
	LogKeepalive bool
}

// ListenAndServe accepts connections until ctx is cancelled, then returns nil.
func (s *Server) ListenAndServe(ctx context.Context, addr string) error {
	ln, err := net.Listen("tcp", addr)
	if err != nil {
		return err
	}
	go func() {
		<-ctx.Done()
		ln.Close()
	}()
	s.Log.Info("listening for clients", "addr", addr)
	for {
		nc, err := ln.Accept()
		if err != nil {
			if ctx.Err() != nil {
				return nil
			}
			return err
		}
		go s.serve(nc)
	}
}

func (s *Server) serve(nc net.Conn) {
	log := s.Log.With("remote", nc.RemoteAddr().String())
	defer nc.Close()
	log.Info("client connected")

	r := bufio.NewReader(nc)
	rw := struct {
		io.Reader
		io.Writer
	}{r, nc}
	serverID, clientID := NewJamID(ServerTypeLobby), NewJamID(0)
	var trailer [36]byte
	first, second := serverID, clientID
	if s.ClientIDFirst {
		first, second = clientID, serverID
	}
	copy(trailer[0:16], first[:])
	copy(trailer[16:32], second[:])

	sess, err := Handshake(rw, s.SessionSecret, nc.LocalAddr().(*net.TCPAddr), trailer)
	if err != nil {
		log.Error("handshake failed", "err", err)
		return
	}
	l := &link{
		conn:         nc,
		w:            nc,
		sess:         sess,
		hub:          s.Hub,
		logKeepalive: s.LogKeepalive,
		remote:       nc.RemoteAddr().String(),
		clientID:     clientID,
		serverID:     serverID,
		channels:     map[byte]*Protocol{0: protocolByCRC(0x11d82194)},
	}
	s.Hub.add(l)
	defer s.Hub.remove(l)
	l.log = log.With("session", l.id)
	l.log.Info("handshake complete, stream is now JAM-encrypted", "cid", sess.ClientID, "server_jamid", hex.EncodeToString(serverID[:]))

	for {
		frame, err := readFrame(r, sess.Recv)
		if err != nil {
			if errors.Is(err, io.EOF) || errors.Is(err, net.ErrClosed) {
				l.log.Info("client disconnected")
			} else {
				l.log.Error("read failed", "err", err)
			}
			return
		}
		if err := l.handle(frame); err != nil {
			l.log.Error("frame", "err", err)
			return
		}
	}
}

// link is one client connection.
type link struct {
	id           uint64
	log          *slog.Logger
	conn         io.Closer
	hub          *Hub
	sess         *Session
	remote       string
	clientID     JamID // the client's JamID from the handshake trailer
	serverID     JamID
	logKeepalive bool

	mu         sync.Mutex // guards w (the send cipher is stateful), channels and negotiated
	w          io.Writer
	channels   map[byte]*Protocol // channel ID -> protocol, mirroring the client's table
	negotiated bool
}

// send writes one message on the protocol's channel; payload is its binary encoding.
func (l *link) send(protocol string, msgID uint16, payload []byte) error {
	l.mu.Lock()
	defer l.mu.Unlock()
	for ch, p := range l.channels {
		if p.Name == protocol {
			if msgID < p.Base || msgID >= p.Max {
				return fmt.Errorf("message %d is not in %s (%d-%d)", msgID, protocol, p.Base, p.Max-1)
			}
			return writeFrame(l.w, l.sess.Send, ch, byte(msgID-p.Base), payload)
		}
	}
	return fmt.Errorf("protocol %s has no channel on this link", protocol)
}

func (l *link) info() SessionInfo {
	l.mu.Lock()
	defer l.mu.Unlock()
	info := SessionInfo{
		ID:          l.id,
		Remote:      l.remote,
		BnetCID:     l.sess.ClientID,
		ClientJamID: hex.EncodeToString(l.clientID[:]),
		ServerJamID: hex.EncodeToString(l.serverID[:]),
		Negotiated:  l.negotiated,
		Channels:    make([]string, len(l.channels)),
	}
	for ch, p := range l.channels {
		if int(ch) < len(info.Channels) {
			info.Channels[ch] = p.Name
		}
	}
	return info
}

func (l *link) handle(frame []byte) error {
	if len(frame) < 2 {
		return fmt.Errorf("frame too short: %x", frame)
	}
	ch, index, payload := frame[0], frame[1], frame[2:]
	l.mu.Lock()
	proto, negotiated := l.channels[ch], l.negotiated
	l.mu.Unlock()
	if proto == nil && !negotiated {
		// Before negotiation the client may use a channel it created for its own
		// list (seen: channel 1), so anything unknown is the negotiation protocol.
		proto = protocolByCRC(0x11d82194)
	}
	if proto == nil {
		l.log.Warn("frame on unknown channel", "channel", ch, "index", index, "data", hex.EncodeToString(payload))
		return nil
	}
	msgID := proto.Base + uint16(index)

	if proto.CRC == 0x11d82194 {
		switch msgID {
		case negotiationMsgCRCList:
			return l.negotiate(ch, index, payload)
		case negotiationMsgKeepalive:
			if l.logKeepalive {
				l.log.Info("keepalive")
			}
			return nil
		}
		l.log.Warn("unhandled negotiation message", "msg", msgID, "data", hex.EncodeToString(payload))
		return nil
	}
	l.hub.dispatch(l, proto, msgID, payload)
	return nil
}

// negotiate accepts the client's protocol list with message 3 and mirrors the
// channels the client creates for it (1..n, in list order).
func (l *link) negotiate(ch, index byte, payload []byte) error {
	crcs, err := parseCRCList(payload)
	if err != nil {
		return err
	}
	names := make([]string, len(crcs))
	l.mu.Lock()
	for i, crc := range crcs {
		p := protocolByCRC(crc)
		if p == nil {
			p = &Protocol{Name: fmt.Sprintf("unknown-%08x", crc), CRC: crc}
		}
		l.channels[byte(i+1)] = p
		names[i] = fmt.Sprintf("%d=%s", i+1, p.Name)
	}
	accept := byte(negotiationMsgAccept - l.channels[0].Base)
	err = writeFrame(l.w, l.sess.Send, ch, accept, nil)
	l.negotiated = err == nil
	l.mu.Unlock()
	if err != nil {
		return err
	}
	l.log.Info("client offers protocols", "count", len(crcs), "channels", strings.Join(names, " "))
	if len(crcs) == 1 && crcs[0] == 0x11d82194 {
		l.log.Warn("client offered only the negotiation protocol: it does not see a lobby server type in our JamID (try the other JamID order)")
	}
	l.log.Info("accepted protocol list (negotiation msg 3); link is up")
	l.hub.linkUp(l)
	return nil
}

// maxFrame caps a frame's length so a desynced cipher can't make us allocate gigabytes.
const maxFrame = 1 << 20

// readFrame reads and decrypts one [u24 length][payload] frame.
func readFrame(r io.Reader, jam *Jam) ([]byte, error) {
	var hdr [3]byte
	if _, err := io.ReadFull(r, hdr[:]); err != nil {
		return nil, err
	}
	jam.Crypt(hdr[:])
	n := int(hdr[0])<<16 | int(hdr[1])<<8 | int(hdr[2])
	if n > maxFrame {
		return nil, fmt.Errorf("frame length %d too large (cipher out of sync?)", n)
	}
	frame := make([]byte, n)
	if _, err := io.ReadFull(r, frame); err != nil {
		return nil, err
	}
	jam.Crypt(frame)
	return frame, nil
}
