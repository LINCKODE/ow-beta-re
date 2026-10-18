package instance

import (
	"context"

	"owserver/internal/gamelink"
)

// RunLink connects to lobbylogic's instance port and serves it until ctx is
// cancelled, reconnecting whenever the connection drops (gamelink.Client, with
// the heartbeat). advertise is the address lobbylogic puts in 20600.
func (s *Server) RunLink(ctx context.Context, logicAddr, advertise string) error {
	c := &gamelink.Client{Log: s.Log, Addr: logicAddr,
		Hello: func() gamelink.Wire {
			return gamelink.Wire{Addr: advertise, Conns: s.Assignments(), Modes: gamelink.AllModes, Maps: gamelink.AllMaps()}
		},
		Handle: func(m gamelink.Wire) {
			if m.Type == gamelink.WireAssign {
				s.Assign(*m.Conn)
			} else {
				s.Release(m.Conn.ConnID)
			}
		},
	}
	return c.Run(ctx)
}
