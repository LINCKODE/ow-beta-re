package bgs

import (
	"bufio"
	"context"
	"crypto/tls"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net"
	"sync"

	"google.golang.org/protobuf/encoding/prototext"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/reflect/protoreflect"
	"google.golang.org/protobuf/reflect/protoregistry"

	protocol "owserver/gen/bgs/protocol"
)

// Handler serves one RPC method. Return a nil response for methods that
// return NO_RESPONSE; return a *StatusError to reply with an error status.
type Handler func(c *Conn, req proto.Message) (proto.Message, error)

// StatusError makes the server reply with Header.status set to Status.
type StatusError struct{ Status uint32 }

func (e *StatusError) Error() string { return fmt.Sprintf("bgs status %d", e.Status) }

type Server struct {
	Log *slog.Logger
	TLS *tls.Config // nil = plain TCP
	// Quiet lists methods whose calls are not logged (e.g. KeepAlive).
	Quiet    map[protoreflect.FullName]bool
	handlers map[protoreflect.FullName]Handler
}

func NewServer(log *slog.Logger, tlsConfig *tls.Config) *Server {
	return &Server{Log: log, TLS: tlsConfig, handlers: map[protoreflect.FullName]Handler{}}
}

// Handle registers h for a method, e.g. "bgs.protocol.connection.v1.ConnectionService.Connect".
// Methods without a handler are logged and left unanswered.
func (s *Server) Handle(method protoreflect.FullName, h Handler) {
	s.handlers[method] = h
}

// ListenAndServe accepts connections until ctx is cancelled, then returns nil.
func (s *Server) ListenAndServe(ctx context.Context, addr string) error {
	ln, err := net.Listen("tcp", addr)
	if err != nil {
		return err
	}
	if s.TLS != nil {
		ln = tls.NewListener(ln, s.withHelloLogging(s.TLS))
	}
	go func() {
		<-ctx.Done()
		ln.Close()
	}()
	s.Log.Info("listening", "addr", addr, "tls", s.TLS != nil)
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

// withHelloLogging returns a copy of cfg that logs what each client offers in its
// ClientHello, to diagnose version or cipher mismatches with the client's SChannel.
func (s *Server) withHelloLogging(cfg *tls.Config) *tls.Config {
	cfg = cfg.Clone()
	cfg.GetConfigForClient = func(hello *tls.ClientHelloInfo) (*tls.Config, error) {
		versions := make([]string, len(hello.SupportedVersions))
		for i, v := range hello.SupportedVersions {
			versions[i] = tls.VersionName(v)
		}
		ciphers := make([]string, len(hello.CipherSuites))
		for i, c := range hello.CipherSuites {
			ciphers[i] = tls.CipherSuiteName(c)
		}
		s.Log.Debug("TLS client hello", "remote", hello.Conn.RemoteAddr().String(),
			"server_name", hello.ServerName, "versions", versions, "ciphers", ciphers)
		return nil, nil
	}
	return cfg
}

// Conn is one client connection.
type Conn struct {
	Log *slog.Logger
	// State holds per-connection data for handlers (e.g. the logged-in account).
	// Handlers of one connection run sequentially, so it needs no locking.
	State any

	nc      net.Conn
	writeMu sync.Mutex
	tokenMu sync.Mutex
	token   uint32
	after   []func()
}

// AfterResponse runs fn once the response to the current call has been sent,
// e.g. to follow up with a call to the client that must not arrive before it.
func (c *Conn) AfterResponse(fn func()) {
	c.after = append(c.after, fn)
}

func (c *Conn) runAfterResponse() {
	pending := c.after
	c.after = nil
	for _, fn := range pending {
		fn()
	}
}

// ServeConn serves one already-accepted connection until it closes.
func (s *Server) ServeConn(nc net.Conn) { s.serve(nc) }

func (s *Server) serve(nc net.Conn) {
	c := &Conn{Log: s.Log.With("remote", nc.RemoteAddr().String()), nc: nc}
	defer nc.Close()
	c.Log.Info("client connected")

	if tc, ok := nc.(*tls.Conn); ok {
		if err := tc.Handshake(); err != nil {
			c.Log.Error("TLS handshake failed", "err", err)
			return
		}
		state := tc.ConnectionState()
		c.Log.Info("TLS established", "version", tls.VersionName(state.Version), "cipher", tls.CipherSuiteName(state.CipherSuite))
	}

	r := bufio.NewReader(nc)
	if s.TLS == nil {
		// A TLS ClientHello starts with record type 0x16, version 0x03xx.
		if b, err := r.Peek(2); err == nil && b[0] == 0x16 && b[1] == 0x03 {
			c.Log.Warn("client is speaking TLS but the server was started with --tls-disable")
			return
		}
	}

	for {
		f, err := ReadFrame(r)
		if err != nil {
			if errors.Is(err, io.EOF) {
				c.Log.Info("client disconnected")
			} else {
				c.Log.Error("read failed", "err", err)
			}
			return
		}
		s.dispatch(c, f)
	}
}

func (s *Server) dispatch(c *Conn, f *Frame) {
	h := f.Header
	if h.GetServiceId() == ResponseServiceID {
		c.Log.Info("response from client", "token", h.GetToken(), "status", h.GetStatus(), "size", len(f.Payload))
		return
	}

	svc := Services[h.GetServiceHash()]
	if svc == nil {
		c.Log.Warn("call to unknown service", "header", prototext.MarshalOptions{}.Format(h))
		return
	}
	method := svc.Methods[h.GetMethodId()]
	if method == nil {
		c.Log.Warn("call to unknown method", "service", svc.Desc.Name(), "method_id", h.GetMethodId())
		return
	}

	req, err := newMessage(method.Input().FullName())
	if err == nil {
		err = proto.Unmarshal(f.Payload, req)
	}
	if err != nil {
		c.Log.Error("decode request", "method", shortName(method), "err", err)
		return
	}
	if !s.Quiet[method.FullName()] {
		c.Log.Info("call", "method", shortName(method), "token", h.GetToken(), "request", FormatMessage(req))
	}

	handler := s.handlers[method.FullName()]
	if handler == nil {
		c.Log.Warn("unhandled method", "method", shortName(method))
		return
	}
	resp, err := handler(c, req)
	defer c.runAfterResponse()
	if method.Output().FullName() == "bgs.protocol.NO_RESPONSE" {
		if err != nil {
			c.Log.Error("handler failed", "method", shortName(method), "err", err)
		}
		return
	}
	reply := &protocol.Header{ServiceId: proto.Uint32(ResponseServiceID), Token: proto.Uint32(h.GetToken())}
	var statusErr *StatusError
	switch {
	case errors.As(err, &statusErr):
		reply.Status = proto.Uint32(statusErr.Status)
		resp = nil
	case err != nil:
		c.Log.Error("handler failed", "method", shortName(method), "err", err)
		c.after = nil // no response was sent, so nothing may follow it
		return
	}
	if err := c.write(reply, resp); err != nil {
		c.Log.Error("write response", "method", shortName(method), "err", err)
		c.after = nil
	}
}

// Call sends a request to one of the client's listener services
// (AuthenticationListener, ChallengeListener). Responses are only logged for now.
func (c *Conn) Call(method protoreflect.FullName, req proto.Message) error {
	desc, err := protoregistry.GlobalFiles.FindDescriptorByName(method)
	if err != nil {
		return err
	}
	m, ok := desc.(protoreflect.MethodDescriptor)
	if !ok {
		return fmt.Errorf("%s is not a method", method)
	}
	svc := ServiceByName(m.Parent().FullName())
	if svc == nil {
		return fmt.Errorf("%s is not one of the client's services", m.Parent().FullName())
	}
	methodID, ok := MethodID(m)
	if !ok {
		return fmt.Errorf("%s has no known method ID", method)
	}
	c.tokenMu.Lock()
	token := c.token
	c.token++
	c.tokenMu.Unlock()
	header := &protocol.Header{
		ServiceId:   proto.Uint32(0),
		ServiceHash: proto.Uint32(svc.WireHash),
		MethodId:    proto.Uint32(methodID),
		Token:       proto.Uint32(token),
	}
	c.Log.Info("calling client", "method", shortName(m), "token", token, "request", FormatMessage(req))
	return c.write(header, req)
}

func (c *Conn) write(header *protocol.Header, msg proto.Message) error {
	c.writeMu.Lock()
	defer c.writeMu.Unlock()
	return WriteFrame(c.nc, header, msg)
}

func newMessage(name protoreflect.FullName) (proto.Message, error) {
	mt, err := protoregistry.GlobalTypes.FindMessageByName(name)
	if err != nil {
		return nil, err
	}
	return mt.New().Interface(), nil
}

// shortName returns "Service.Method" for log lines.
func shortName(m protoreflect.MethodDescriptor) string {
	return string(m.Parent().Name()) + "." + string(m.Name())
}
