package login

import (
	"sync/atomic"
	"time"

	"google.golang.org/protobuf/proto"

	protocol "owserver/gen/bgs/protocol"
	connectionv1 "owserver/gen/bgs/protocol/connection/v1"
	"owserver/internal/bgs"
)

var (
	// serverID identifies this server process; epoch is its start time.
	serverID = &protocol.ProcessId{Label: proto.Uint32(1), Epoch: proto.Uint32(uint32(time.Now().Unix()))}
	// nextClientLabel hands out process IDs to clients that don't bring their own.
	nextClientLabel atomic.Uint32
)

// connect is the first call on every connection. The client waits for the
// response before doing anything else, then sends KeepAlive while idle.
func connect(c *bgs.Conn, req proto.Message) (proto.Message, error) {
	r := req.(*connectionv1.ConnectRequest)

	clientID := r.GetClientId()
	if clientID == nil {
		clientID = &protocol.ProcessId{Label: proto.Uint32(nextClientLabel.Add(1)), Epoch: proto.Uint32(uint32(time.Now().Unix()))}
	}
	if r.GetBindRequest() != nil {
		// Only bindless RPC (services addressed by hash) is implemented.
		c.Log.Warn("client sent a bind request; bound services are not supported")
	}

	return &connectionv1.ConnectResponse{
		ServerId: serverID,
		ClientId: clientID,
		// NOTE: unit assumed to be milliseconds since the Unix epoch.
		ServerTime:     proto.Uint64(uint64(time.Now().UnixMilli())),
		UseBindlessRpc: proto.Bool(r.GetUseBindlessRpc()),
	}, nil
}

// keepAlive returns NO_RESPONSE; it only keeps the connection from timing out.
func keepAlive(*bgs.Conn, proto.Message) (proto.Message, error) {
	return nil, nil
}
