package login

import (
	"bytes"
	"encoding/json"
	"io"
	"log/slog"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"google.golang.org/protobuf/proto"

	protocol "owserver/gen/bgs/protocol"
	authenticationv1 "owserver/gen/bgs/protocol/authentication/v1"
	challengev1 "owserver/gen/bgs/protocol/challenge/v1"
	connectionv1 "owserver/gen/bgs/protocol/connection/v1"
	game_utilitiesv1 "owserver/gen/bgs/protocol/game_utilities/v1"
	"owserver/internal/bgs"
	"owserver/internal/session"
)

// fakeClient plays the 0.8 client's side of the Battle.net connection.
type fakeClient struct {
	t     *testing.T
	conn  net.Conn
	token uint32
}

func (c *fakeClient) call(service string, methodID uint32, req proto.Message) uint32 {
	c.t.Helper()
	c.token++
	header := &protocol.Header{
		ServiceId:   proto.Uint32(0),
		ServiceHash: proto.Uint32(bgs.Hash(service)),
		MethodId:    proto.Uint32(methodID),
		Token:       proto.Uint32(c.token),
	}
	if err := bgs.WriteFrame(c.conn, header, req); err != nil {
		c.t.Fatal(err)
	}
	return c.token
}

func (c *fakeClient) read(into proto.Message) *protocol.Header {
	c.t.Helper()
	f, err := bgs.ReadFrame(c.conn)
	if err != nil {
		c.t.Fatal(err)
	}
	if into != nil {
		if err := proto.Unmarshal(f.Payload, into); err != nil {
			c.t.Fatal(err)
		}
	}
	return f.Header
}

func (c *fakeClient) expectResponse(token uint32) {
	c.t.Helper()
	if h := c.read(nil); h.GetServiceId() != bgs.ResponseServiceID || h.GetToken() != token || h.GetStatus() != 0 {
		c.t.Fatalf("expected OK response to token %d, got %v", token, h)
	}
}

func (c *fakeClient) expectCall(wireName string, methodID uint32, into proto.Message) {
	c.t.Helper()
	if h := c.read(into); h.GetServiceHash() != bgs.Hash(wireName) || h.GetMethodId() != methodID {
		c.t.Fatalf("expected call to %s method %d, got %v", wireName, methodID, h)
	}
}

func TestWebLoginFlow(t *testing.T) {
	log := slog.New(slog.NewTextHandler(io.Discard, nil))
	svc := NewService("", "127.0.0.1:3724", []byte("test-secret"))
	web := httptest.NewServer(svc.WebHandler(log))
	defer web.Close()
	svc.WebURL = web.URL + "/bnetserver/login/"

	srv := bgs.NewServer(log, nil)
	svc.Register(srv)
	clientConn, serverConn := net.Pipe()
	defer clientConn.Close()
	go srv.ServeConn(serverConn)
	c := &fakeClient{t: t, conn: clientConn}

	// 1. Connect
	tok := c.call("bnet.protocol.connection.ConnectionService", 1, &connectionv1.ConnectRequest{UseBindlessRpc: proto.Bool(true)})
	var connected connectionv1.ConnectResponse
	if h := c.read(&connected); h.GetToken() != tok || !connected.GetUseBindlessRpc() {
		t.Fatalf("bad connect response: %v %v", h, &connected)
	}

	// 2-3. Logon, then the external challenge carrying the login URL
	tok = c.call("bnet.protocol.authentication.AuthenticationServer", 1, &authenticationv1.LogonRequest{
		Program: proto.String("Pro"), Platform: proto.String("Win"), WebClientVerification: proto.Bool(true),
	})
	c.expectResponse(tok)
	var challenge challengev1.ChallengeExternalRequest
	c.expectCall("bnet.protocol.challenge.ChallengeNotify", 3, &challenge) // OnExternalChallenge
	if string(challenge.GetPayload()) != svc.WebURL {
		t.Fatalf("challenge payload %q, want %q", challenge.GetPayload(), svc.WebURL)
	}

	// 4. GET the login form
	resp, err := http.Get(string(challenge.GetPayload()))
	if err != nil {
		t.Fatal(err)
	}
	var form struct {
		Type   string
		Inputs []struct {
			InputID   string `json:"input_id"`
			MaxLength *int   `json:"max_length"`
		}
	}
	json.NewDecoder(resp.Body).Decode(&form)
	resp.Body.Close()
	for _, name := range []string{"JSESSIONID=", "web.id="} {
		found := false
		for _, h := range resp.Header.Values("Set-Cookie") {
			// The client reads the value up to the next ';', so one must follow it.
			if strings.HasPrefix(h, name) && strings.Contains(h, ";") {
				found = true
			}
		}
		if !found {
			t.Fatalf("login form response is missing cookie %s...; (headers: %v)", name, resp.Header.Values("Set-Cookie"))
		}
	}
	if form.Type != "LOGIN_FORM" || len(form.Inputs) != 3 || form.Inputs[0].InputID != "account_name" || form.Inputs[0].MaxLength == nil {
		t.Fatalf("bad login form: %+v", form)
	}

	// 5. POST the credentials, formatted like __SubmitLogin
	body := `{ "version": "1.0", "program_id": "Pro", "platform_id": "Win", "inputs": [ ` +
		`{ "input_id": "account_name", "value": "tracer@example.com" }, ` +
		`{ "input_id": "password", "value": "hunter2" }, ` +
		`{ "input_id": "log_in_submit", "value": "Log In" } ] }`
	resp, err = http.Post(string(challenge.GetPayload()), "application/json;charset=utf-8", bytes.NewBufferString(body))
	if err != nil {
		t.Fatal(err)
	}
	var result loginResult
	json.NewDecoder(resp.Body).Decode(&result)
	resp.Body.Close()
	if result.AuthenticationState != "DONE" || result.LoginTicket == "" {
		t.Fatalf("bad login result: %+v", result)
	}

	// 6-7. VerifyWebCredentials, then OnLogonComplete
	tok = c.call("bnet.protocol.authentication.AuthenticationServer", 7, &authenticationv1.VerifyWebCredentialsRequest{
		WebCredentials: []byte(result.LoginTicket),
	})
	c.expectResponse(tok)
	var logon authenticationv1.LogonResult
	c.expectCall("bnet.protocol.authentication.AuthenticationClient", 5, &logon) // OnLogonComplete
	if logon.GetErrorCode() != 0 || logon.GetEmail() != "tracer@example.com" || logon.GetBattleTag() != "tracer#1234" ||
		len(logon.GetGameAccountId()) != 1 || len(logon.GetSessionKey()) != 64 {
		t.Fatalf("bad logon result: %v", &logon)
	}

	// 8. Lobby referral: all six attributes, keys derivable from cid.
	tok = c.call("bnet.protocol.game_utilities.GameUtilities", 1, &game_utilitiesv1.ClientRequest{
		Attribute: []*protocol.Attribute{{Name: proto.String("build"), Value: &protocol.Variant{UintValue: proto.Uint64(24919)}}},
	})
	var referral game_utilitiesv1.ClientResponse
	if h := c.read(&referral); h.GetToken() != tok || h.GetStatus() != 0 {
		t.Fatalf("bad referral response header: %v", h)
	}
	attrs := map[string]*protocol.Variant{}
	for _, a := range referral.GetAttribute() {
		attrs[a.GetName()] = a.GetValue()
	}
	if attrs["hostv4"].GetStringValue() != "127.0.0.1:3724" || attrs["cid"].GetUintValue() == 0 {
		t.Fatalf("bad referral: %v", &referral)
	}
	keys := session.DeriveKeys([]byte("test-secret"), attrs["cid"].GetUintValue())
	for i, name := range []string{"k0", "k1", "k2", "k3"} {
		if !bytes.Equal(attrs[name].GetBlobValue(), keys[i][:]) {
			t.Fatalf("%s is not the derived key", name)
		}
	}

	// The ticket is single-use.
	tok = c.call("bnet.protocol.authentication.AuthenticationServer", 7, &authenticationv1.VerifyWebCredentialsRequest{
		WebCredentials: []byte(result.LoginTicket),
	})
	if h := c.read(nil); h.GetToken() != tok || h.GetStatus() != errorDenied {
		t.Fatalf("reused ticket: expected status %d, got %v", errorDenied, h)
	}
}
