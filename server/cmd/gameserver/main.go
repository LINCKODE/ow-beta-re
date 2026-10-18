// gameserver is the game (instance) server the client connects to over UDP after
// the lobby's "game found" (ClientInLobbyInstance 20600). It hangs off lobbylogic,
// which hands it the connections it assigns, so both can be rebuilt and
// restarted independently (see make run).
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

	"owserver/internal/instance"
	"owserver/internal/logging"
)

// build is set at link time: go build -ldflags "-X main.build=..."
var build = "develop"

func main() {
	if err := run(); err != nil {
		logging.New("game", slog.LevelInfo).Error("startup", "err", err)
		os.Exit(1)
	}
}

func run() error {
	cfg := struct {
		conf.Version
		Debug       bool   `conf:"help:enable debug logging (logs every packet)"`
		Addr        string `conf:"default:0.0.0.0:26500,help:UDP listen address"`
		Advertise   string `conf:"default:127.0.0.1:26500,help:address the client is told to connect to (in 20600)"`
		LogicAddr   string `conf:"default:127.0.0.1:3727,help:lobbylogic's instance port (lobbylogic --instance-addr)"`
		ControlAddr string `conf:"default:127.0.0.1:3728,help:control API address (loopback only)"`
	}{
		Version: conf.Version{Build: build, Desc: "Game (instance) server for the Overwatch 0.8 client"},
	}

	help, err := conf.Parse("GAME", &cfg)
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
	log := logging.New("game", level)

	out, err := conf.String(&cfg)
	if err != nil {
		return fmt.Errorf("print config: %w", err)
	}
	log.Info("config: " + strings.Join(strings.Fields(out), " "))

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	srv := &instance.Server{Log: log}
	ctl := &instance.Control{Server: srv}
	errs := make(chan error, 3)
	go func() { errs <- srv.ListenAndServe(ctx, cfg.Addr) }()
	go func() { errs <- srv.RunLink(ctx, cfg.LogicAddr, cfg.Advertise) }()
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
