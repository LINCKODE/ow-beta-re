package bgs

import (
	"bytes"
	"io"
	"log/slog"
	"net"
	"strings"
	"testing"

	"google.golang.org/protobuf/proto"

	protocol "owserver/gen/bgs/protocol"
	connectionv1 "owserver/gen/bgs/protocol/connection/v1"
)

func TestServiceHashes(t *testing.T) {
	// Wire hashes: FNV-1a of the original bnet.protocol names. ConnectionService's
	// was observed in traffic; all six occur as constants in the 0.8 binary.
	want := map[string]uint32{
		"bgs.protocol.connection.v1.ConnectionService":          0x65446991,
		"bgs.protocol.authentication.v1.AuthenticationService":  0x0DECFC01,
		"bgs.protocol.authentication.v1.AuthenticationListener": 0x71240E35,
		"bgs.protocol.challenge.v1.ChallengeService":            0xDBBF6F19,
		"bgs.protocol.challenge.v1.ChallengeListener":           0xBBDA171F,
		"bgs.protocol.game_utilities.v1.GameUtilitiesService":   0x3FC1274D,
	}
	if len(serviceList) != len(want) {
		t.Fatalf("got %d services, want %d", len(serviceList), len(want))
	}
	for name, wire := range want {
		s := Services[wire]
		if s == nil || string(s.Desc.FullName()) != name {
			t.Errorf("wire hash %#08x: want %s, got %v", wire, name, s)
			continue
		}
		if Services[Hash(name)] != s {
			t.Errorf("%s: not reachable by its proto name hash", name)
		}
	}
}

func TestFrameRoundTrip(t *testing.T) {
	var buf bytes.Buffer
	in := &connectionv1.EchoRequest{Payload: []byte("ping")}
	header := &protocol.Header{ServiceId: proto.Uint32(0), MethodId: proto.Uint32(3), Token: proto.Uint32(7)}
	if err := WriteFrame(&buf, header, in); err != nil {
		t.Fatal(err)
	}
	f, err := ReadFrame(&buf)
	if err != nil {
		t.Fatal(err)
	}
	out := &connectionv1.EchoRequest{}
	if err := proto.Unmarshal(f.Payload, out); err != nil {
		t.Fatal(err)
	}
	if f.Header.GetToken() != 7 || f.Header.GetMethodId() != 3 || !proto.Equal(in, out) {
		t.Fatalf("round trip mismatch: header %v, body %v", f.Header, out)
	}
}

func TestDispatch(t *testing.T) {
	srv := NewServer(slog.New(slog.NewTextHandler(io.Discard, nil)), nil)
	srv.Handle("bgs.protocol.connection.v1.ConnectionService.Connect", func(c *Conn, req proto.Message) (proto.Message, error) {
		return &connectionv1.ConnectResponse{
			ServerId: &protocol.ProcessId{Label: proto.Uint32(1), Epoch: proto.Uint32(2)},
		}, nil
	})

	client, server := net.Pipe()
	defer client.Close()
	go srv.serve(server)

	svc := ServiceByName("bgs.protocol.connection.v1.ConnectionService")
	req := &protocol.Header{
		ServiceId:   proto.Uint32(0),
		ServiceHash: proto.Uint32(svc.WireHash),
		MethodId:    proto.Uint32(1), // Connect
		Token:       proto.Uint32(42),
	}
	if err := WriteFrame(client, req, &connectionv1.ConnectRequest{}); err != nil {
		t.Fatal(err)
	}
	f, err := ReadFrame(client)
	if err != nil {
		t.Fatal(err)
	}
	if f.Header.GetServiceId() != ResponseServiceID || f.Header.GetToken() != 42 {
		t.Fatalf("unexpected response header: %v", f.Header)
	}
	resp := &connectionv1.ConnectResponse{}
	if err := proto.Unmarshal(f.Payload, resp); err != nil {
		t.Fatal(err)
	}
	if resp.GetServerId().GetEpoch() != 2 {
		t.Fatalf("unexpected response: %v", resp)
	}
}

func TestFormatMessageSummarizesBinaryBytes(t *testing.T) {
	m := &connectionv1.EchoRequest{Payload: []byte{0x00, 0xff, 0x10, 0x80}}
	if got := FormatMessage(m); !strings.Contains(got, `"<4 bytes>"`) {
		t.Fatalf("binary bytes not summarized: %s", got)
	}
	m = &connectionv1.EchoRequest{Payload: []byte("OW-abc123")}
	if got := FormatMessage(m); !strings.Contains(got, `"OW-abc123"`) {
		t.Fatalf("printable bytes should be kept: %s", got)
	}
	if string(m.Payload) != "OW-abc123" {
		t.Fatal("FormatMessage modified its argument")
	}
}

func TestMethodIDsCoverServices(t *testing.T) {
	// Every ID in the table must name a real method of its service.
	for _, s := range serviceList {
		want := methodIDs[s.Desc.FullName()]
		if len(want) == 0 || len(s.Methods) != len(want) {
			t.Errorf("%s: %d methods resolved, table has %d", s.Desc.FullName(), len(s.Methods), len(want))
		}
	}
	m := ServiceByName("bgs.protocol.authentication.v1.AuthenticationListener").Methods[5]
	if m == nil || m.Name() != "OnLogonComplete" {
		t.Fatalf("AuthenticationListener method 5 = %v, want OnLogonComplete", m)
	}
}
