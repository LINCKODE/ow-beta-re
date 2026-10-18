package lobby

// The gateway (lobbyserver) keeps the client connection and forwards every
// lobby message to a logic backend (lobbylogic) over a local TCP connection
// carrying one JSON WireMessage per line. The backend can be restarted at any
// time: on connect it receives a "hello" listing the live sessions.
//
// gateway -> logic:
//
//	hello  {sessions}                       sent once when the backend connects
//	open   {session, info}                  a client finished negotiation (link up)
//	recv   {session, protocol, msg, payload} a message from the client
//	close  {session}                        the client disconnected
//
// logic -> gateway:
//
//	send       {session, protocol, msg, payload} send a message to the client
//	disconnect {session}                         drop the client's connection
const (
	WireHello      = "hello"
	WireOpen       = "open"
	WireRecv       = "recv"
	WireClose      = "close"
	WireSend       = "send"
	WireDisconnect = "disconnect"
)

type WireMessage struct {
	Type     string        `json:"type"`
	Session  uint64        `json:"session,omitempty"`
	Sessions []SessionInfo `json:"sessions,omitempty"`
	Info     *SessionInfo  `json:"info,omitempty"`
	Protocol string        `json:"protocol,omitempty"`
	Msg      uint16        `json:"msg,omitempty"`
	Payload  []byte        `json:"payload,omitempty"` // base64 in JSON
}

// SessionInfo describes one client connection to the gateway.
type SessionInfo struct {
	ID          uint64   `json:"id"`
	Remote      string   `json:"remote"`
	BnetCID     uint64   `json:"bnet_cid"`     // the cid from the login referral
	ClientJamID string   `json:"client_jamid"` // hex, as sent in the handshake trailer
	ServerJamID string   `json:"server_jamid"` // hex
	Negotiated  bool     `json:"negotiated"`
	Channels    []string `json:"channels"` // index = channel ID
}
