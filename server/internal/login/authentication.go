package login

import (
	"crypto/rand"
	"strings"

	"google.golang.org/protobuf/proto"

	protocol "owserver/gen/bgs/protocol"
	authenticationv1 "owserver/gen/bgs/protocol/authentication/v1"
	challengev1 "owserver/gen/bgs/protocol/challenge/v1"
	"owserver/internal/bgs"
)

// Error codes sent to the client (Header.status / LogonResult.error_code).
// NOTE: values taken from other bgs emulators, unverified against 0.8.
const (
	errorDenied = 3
)

// EntityId high words: type << 56 | region << 32 | program fourcc.
// The client's OnLogonComplete handler (FUN_7ff6510b5580) requires a game account
// with type 0x02, program 0x50726F ("Pro"), a non-zero region and a non-zero low
// word, otherwise it fails with "LogonComplete with no prometheus game accounts".
const (
	accountIDHigh     = 0x0100000000000000
	gameAccountIDHigh = 0x0200000100000000 | 0x50726F
	region            = 1
)

// connState is the per-connection login state (bgs.Conn.State).
type connState struct {
	logon   *authenticationv1.LogonRequest
	account *Account
}

func state(c *bgs.Conn) *connState {
	if c.State == nil {
		c.State = &connState{}
	}
	return c.State.(*connState)
}

// logon starts the web login: acknowledge, then send the client the login URL.
func (s *Service) logon(c *bgs.Conn, req proto.Message) (proto.Message, error) {
	r := req.(*authenticationv1.LogonRequest)
	state(c).logon = r
	if s.AutoLogin && s.DefaultAccount != "" {
		// Skip the web login: complete the logon now, as after VerifyWebCredentials.
		account := newAccount(s.DefaultAccount)
		state(c).account = account
		c.Log.Info("logged in (auto login, no login form)", "account", account.Name, "id", account.ID)
		c.AfterResponse(func() {
			if err := c.Call("bgs.protocol.authentication.v1.AuthenticationListener.OnLogonComplete", logonResult(account)); err != nil {
				c.Log.Error("send logon complete", "err", err)
			}
		})
		return &protocol.NoData{}, nil
	}
	if !r.GetWebClientVerification() {
		c.Log.Warn("client did not ask for web login; only the web login flow is implemented")
	}

	c.AfterResponse(func() {
		// The client downloads the URL in payload; payload_type is not checked.
		err := c.Call("bgs.protocol.challenge.v1.ChallengeListener.OnExternalChallenge", &challengev1.ChallengeExternalRequest{
			PayloadType: proto.String("web_auth_url"),
			Payload:     []byte(s.WebURL),
		})
		if err != nil {
			c.Log.Error("send external challenge", "err", err)
		}
	})
	return &protocol.NoData{}, nil
}

// verifyWebCredentials redeems the login ticket the web login handed out,
// then completes the logon.
func (s *Service) verifyWebCredentials(c *bgs.Conn, req proto.Message) (proto.Message, error) {
	r := req.(*authenticationv1.VerifyWebCredentialsRequest)
	account := s.redeemTicket(string(r.GetWebCredentials()))
	if account == nil {
		c.Log.Warn("unknown or already used login ticket")
		return nil, &bgs.StatusError{Status: errorDenied}
	}
	state(c).account = account
	c.Log.Info("logged in", "account", account.Name, "id", account.ID)

	c.AfterResponse(func() {
		if err := c.Call("bgs.protocol.authentication.v1.AuthenticationListener.OnLogonComplete", logonResult(account)); err != nil {
			c.Log.Error("send logon complete", "err", err)
		}
	})
	return &protocol.NoData{}, nil
}

func logonResult(a *Account) *authenticationv1.LogonResult {
	sessionKey := make([]byte, 64)
	rand.Read(sessionKey)
	name, _, _ := strings.Cut(a.Name, "@")
	return &authenticationv1.LogonResult{
		ErrorCode:       proto.Uint32(0),
		AccountId:       &protocol.EntityId{High: proto.Uint64(accountIDHigh), Low: proto.Uint64(a.ID)},
		GameAccountId:   []*protocol.EntityId{{High: proto.Uint64(gameAccountIDHigh), Low: proto.Uint64(a.ID)}},
		Email:           proto.String(a.Name),
		AvailableRegion: []uint32{region},
		ConnectedRegion: proto.Uint32(region),
		BattleTag:       proto.String(name + "#1234"),
		GeoipCountry:    proto.String("US"),
		SessionKey:      sessionKey,
	}
}
