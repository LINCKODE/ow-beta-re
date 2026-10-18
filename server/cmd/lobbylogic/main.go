// lobbylogic is the lobby's game logic. It connects to the lobby gateway
// (lobbyserver), which owns the client connection, so it can be rebuilt and
// restarted at any time without the client noticing (see make logic-watch).
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
	"time"

	"github.com/ardanlabs/conf/v3"

	"owserver/internal/lobbylogic"
	"owserver/internal/logging"
)

// build is set at link time: go build -ldflags "-X main.build=..."
var build = "develop"

func main() {
	if err := run(); err != nil {
		logging.New("logic", slog.LevelInfo).Error("startup", "err", err)
		os.Exit(1)
	}
}

func run() error {
	cfg := struct {
		conf.Version
		Debug          bool          `conf:"help:enable debug logging"`
		GatewayAddr    string        `conf:"default:127.0.0.1:3725,help:the lobby gateway's backend address (lobbyserver --backend-addr)"`
		InstanceAddr   string        `conf:"default:127.0.0.1:3727,help:address the game servers (gameserver / instance --logic-addr) connect to"`
		GameServer     string        `conf:"default:instance,help:the game server new matches go to when no choice is saved: instance or gameserver (R&D); runtime: the R&D / GAME friend requests or !gameserver, saved to --game-server-file"`
		GameServerFile string        `conf:"default:game-server.json,help:where the chosen game server is saved; a saved choice overrides --game-server"`
		OfflineAfter   time.Duration `conf:"default:2s,help:how long the active game server may be unlinked before the title screen's match buttons grey out"`
		Name           string        `conf:"help:the player's name (BattleTag; #1234 added if missing); overrides lobby-identity.json"`
		Icon           string        `conf:"help:the player's icon (name like Mei or GUID); overrides lobby-identity.json"`
	}{
		Version: conf.Version{Build: build, Desc: "Lobby logic for the Overwatch 0.8 client"},
	}

	help, err := conf.Parse("LOGIC", &cfg)
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
	log := logging.New("logic", level)

	out, err := conf.String(&cfg)
	if err != nil {
		return fmt.Errorf("print config: %w", err)
	}
	log.Info("config: " + strings.Join(strings.Fields(out), " "))
	if err := lobbylogic.ConfigureIdentity(cfg.Name, cfg.Icon); err != nil {
		return err
	}

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	active := cfg.GameServer
	saved, err := lobbylogic.LoadActiveServer(cfg.GameServerFile)
	if err != nil {
		return err
	}
	if saved != "" {
		active = saved
		log.Info("game server choice loaded", "server", saved, "file", cfg.GameServerFile)
	}
	inst := &lobbylogic.Instances{Log: log, Active: active, SaveFile: cfg.GameServerFile, Debounce: cfg.OfflineAfter}
	l := &lobbylogic.Logic{Log: log, Handlers: lobbylogic.DefaultHandlers(), Resync: lobbylogic.ResyncSession, Instances: inst}
	inst.OnAvailable = l.AvailabilityChanged
	inst.OnFrame = l.ServerViewFrame
	errs := make(chan error, 2)
	go func() { errs <- inst.Serve(ctx, cfg.InstanceAddr) }()
	go func() { errs <- l.Run(ctx, cfg.GatewayAddr) }()
	for range 2 {
		if err := <-errs; err != nil {
			stop()
			return err
		}
	}
	log.Info("shut down")
	return nil
}
