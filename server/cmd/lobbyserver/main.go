// lobbyserver is the lobby gateway the client connects to after logging in
// (or directly, via --lobbyServer). It keeps the client connection up and
// forwards lobby messages to lobbylogic, which can be restarted freely; its
// control API (Lua eval, raw send, traffic log) is for live experiments.
package main

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"os/signal"
	"strings"
	"syscall"

	"github.com/ardanlabs/conf/v3"

	"owserver/internal/lobby"
	"owserver/internal/logging"
)

// build is set at link time: go build -ldflags "-X main.build=..."
var build = "develop"

func main() {
	if err := run(); err != nil {
		logging.New("lobby", slog.LevelInfo).Error("startup", "err", err)
		os.Exit(1)
	}
}

func run() error {
	cfg := struct {
		conf.Version
		Debug         bool   `conf:"help:enable debug logging"`
		Addr          string `conf:"default:0.0.0.0:3724,help:listen address"`
		SessionSecret string `conf:"default:owserver-dev-secret,mask,help:secret the lobby session keys are derived from; must match the login server's --lobby-session-secret"`
		ClientIDFirst bool   `conf:"default:true,help:put the client JamID before the server JamID in the handshake trailer (confirmed order)"`
		LogKeepalive  bool   `conf:"help:log the client's keepalives"`
		BackendAddr   string `conf:"default:127.0.0.1:3725,help:address lobbylogic connects to"`
		ControlAddr   string `conf:"default:127.0.0.1:3726,help:control API address (loopback only: it has an unauthenticated Lua eval)"`
		LuaInit       string `conf:"default:lua/init.lua,help:Lua script run when the Lua state is created or reset"`
		QuietMsgs     []int  `conf:"default:32002,help:message ids logged only as a summary every 10 s instead of one hex line each (high-rate streams)"`
		ProtocolDump  string `conf:"default:../game/owbetaclient/Prometheus/jam_protocols.json,help:Prometheus JAM protocol dump (message field tables for the control API)"`
	}{
		Version: conf.Version{Build: build, Desc: "Lobby server for the Overwatch 0.8 client"},
	}

	help, err := conf.Parse("LOBBY", &cfg)
	if err != nil {
		if errors.Is(err, conf.ErrHelpWanted) || errors.Is(err, conf.ErrVersionWanted) {
			fmt.Println(help)
			return nil
		}
		return fmt.Errorf("parse config: %w", err)
	}
	level := slog.LevelInfo
	if cfg.Debug {
		level = slog.LevelDebug
	}
	log := logging.New("lobby", level)

	out, err := conf.String(&cfg)
	if err != nil {
		return fmt.Errorf("print config: %w", err)
	}
	log.Info("config: " + strings.Join(strings.Fields(out), " "))

	dump, err := lobby.LoadProtocolDump(cfg.ProtocolDump)
	if err != nil {
		log.Warn("no protocol dump; the control API will list protocols without field tables", "err", err)
	}
	hub := lobby.NewHub(log)
	hub.Quiet = map[uint16]bool{}
	for _, id := range cfg.QuietMsgs {
		hub.Quiet[uint16(id)] = true
	}
	vm := lobby.NewLuaVM(log, hub, cfg.LuaInit, dump)
	srv := &lobby.Server{Log: log, Hub: hub, SessionSecret: []byte(cfg.SessionSecret), ClientIDFirst: cfg.ClientIDFirst, LogKeepalive: cfg.LogKeepalive}
	ctl := &lobby.Control{Hub: hub, Lua: vm, Dump: dump}

	// Handling the signals explicitly also re-enables SIGINT when the process was
	// started with it ignored, as bash does for background jobs (make run).
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	errs := make(chan error, 3)
	go func() { errs <- srv.ListenAndServe(ctx, cfg.Addr) }()
	go func() { errs <- hub.ServeBackend(ctx, cfg.BackendAddr) }()
	go func() { errs <- ctl.ListenAndServe(ctx, cfg.ControlAddr) }()
	for range 3 {
		if err := <-errs; err != nil {
			stop()
			return err
		}
	}
	log.Info("shut down")
	return nil
}
