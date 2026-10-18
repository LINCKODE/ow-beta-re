package login

import (
	"google.golang.org/protobuf/proto"

	protocol "owserver/gen/bgs/protocol"
	game_utilitiesv1 "owserver/gen/bgs/protocol/game_utilities/v1"
	"owserver/internal/bgs"
	"owserver/internal/session"
)

// processClientRequest answers the client's request for a lobby referral.
//
// The client (FUN_7ff6510c01c0) requires all six attributes, otherwise it keeps
// waiting in WAIT_REFERRAL and re-sends the request every 60 seconds:
//
//	k0, k1, k2, k3  blob_value, exactly 64 bytes each (lobby session keys)
//	cid             uint_value (client ID, the first thing the client sends the lobby)
//	hostv4          string_value (lobby address, host:port)
//
// The request carries attribute "build" (and optionally "segregation_key", "cluster").
func (s *Service) processClientRequest(c *bgs.Conn, req proto.Message) (proto.Message, error) {
	if state(c).account == nil {
		c.Log.Warn("lobby referral requested before logging in")
		return nil, &bgs.StatusError{Status: errorDenied}
	}

	cid := session.NewClientID()
	keys := session.DeriveKeys(s.SessionSecret, cid)
	c.Log.Info("referring client to lobby", "host", s.LobbyHost, "cid", cid)

	attrs := []*protocol.Attribute{
		{Name: proto.String("cid"), Value: &protocol.Variant{UintValue: proto.Uint64(cid)}},
		{Name: proto.String("hostv4"), Value: &protocol.Variant{StringValue: proto.String(s.LobbyHost)}},
	}
	for i, key := range keys {
		attrs = append(attrs, &protocol.Attribute{
			Name:  proto.String("k" + string(rune('0'+i))),
			Value: &protocol.Variant{BlobValue: key[:]},
		})
	}
	return &game_utilitiesv1.ClientResponse{Attribute: attrs}, nil
}
