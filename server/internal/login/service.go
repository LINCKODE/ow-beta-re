// Package login implements the Battle.net services behind the login server.
//
// The 0.8 client logs in through Battle.net's web authentication:
//
//  1. ConnectionService.Connect
//  2. AuthenticationService.Logon (web_client_verification = true)
//  3. server -> ChallengeListener.OnExternalChallenge, payload = login URL
//  4. client GETs the URL and draws a login form from the JSON (see web.go)
//  5. client POSTs the entered credentials, gets back a login ticket
//  6. AuthenticationService.VerifyWebCredentials(ticket)
//  7. server -> AuthenticationListener.OnLogonComplete(account, game accounts, ...)
//  8. GameUtilitiesService.ProcessClientRequest -> lobby referral (see game_utilities.go)
package login

import (
	"crypto/rand"
	"encoding/hex"
	"hash/fnv"
	"strings"
	"sync"

	"owserver/internal/bgs"
)

// Service holds the login server's state shared between the Battle.net RPC
// handlers and the web login endpoint.
type Service struct {
	// WebURL is the login URL sent to the client in the external challenge.
	WebURL string
	// LobbyHost is the lobby address (host:port) the client is referred to.
	LobbyHost string
	// SessionSecret derives the lobby session keys; the lobby server needs the same one.
	SessionSecret []byte
	// DefaultAccount, when set, logs everyone in as this account: the web login
	// form (if shown) accepts empty fields, and with AutoLogin no form is shown.
	DefaultAccount string
	// AutoLogin completes the logon as DefaultAccount right at Logon, skipping the
	// web login (no login form in the client).
	AutoLogin bool

	mu      sync.Mutex
	tickets map[string]*Account // login ticket -> account, consumed by VerifyWebCredentials
}

// Account is a logged-in Battle.net account.
// NOTE: there is no account database yet; any name and password are accepted.
type Account struct {
	Name string // what was typed into the account field (e-mail)
	ID   uint64
}

func NewService(webURL, lobbyHost string, sessionSecret []byte) *Service {
	return &Service{WebURL: webURL, LobbyHost: lobbyHost, SessionSecret: sessionSecret, tickets: map[string]*Account{}}
}

// Register installs the login server's RPC handlers.
func (s *Service) Register(srv *bgs.Server) {
	srv.Handle("bgs.protocol.connection.v1.ConnectionService.Connect", connect)
	srv.Handle("bgs.protocol.connection.v1.ConnectionService.KeepAlive", keepAlive)
	srv.Handle("bgs.protocol.authentication.v1.AuthenticationService.Logon", s.logon)
	srv.Handle("bgs.protocol.authentication.v1.AuthenticationService.VerifyWebCredentials", s.verifyWebCredentials)
	srv.Handle("bgs.protocol.game_utilities.v1.GameUtilitiesService.ProcessClientRequest", s.processClientRequest)
}

// issueTicket creates a single-use login ticket for an account name.
// newAccount returns the account for a name; the ID is stable per name across restarts.
func newAccount(name string) *Account {
	h := fnv.New64a()
	h.Write([]byte(strings.ToLower(name)))
	return &Account{Name: name, ID: h.Sum64() & 0x7FFFFFFF}
}

func (s *Service) issueTicket(name string) string {
	account := newAccount(name)

	buf := make([]byte, 16)
	rand.Read(buf)
	ticket := "OW-" + hex.EncodeToString(buf)

	s.mu.Lock()
	s.tickets[ticket] = account
	s.mu.Unlock()
	return ticket
}

// redeemTicket returns the ticket's account and invalidates the ticket.
func (s *Service) redeemTicket(ticket string) *Account {
	s.mu.Lock()
	defer s.mu.Unlock()
	account := s.tickets[ticket]
	delete(s.tickets, ticket)
	return account
}
