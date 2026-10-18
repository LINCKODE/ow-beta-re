// instance is the game instance server (Phase A skeleton, docs/InstanceServer.md):
// the UDP server the client connects to after the lobby's 20600 "game found",
// running matches on a 16 ms tick. Like the R&D game server (cmd/gameserver) it
// hangs off lobbylogic, which hands it the connections it assigns (package
// gamelink); both can be rebuilt and restarted independently (make run).
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

	"owcontent/content"

	"owserver/inst/clock"
	"owserver/inst/control"
	"owserver/inst/game"
	"owserver/inst/match"
	"owserver/inst/server"
	"owserver/inst/sim"
	"owserver/inst/world"
	"owserver/internal/logging"
)

// build is set at link time: go build -ldflags "-X main.build=..."
var build = "develop"

// libs is a hash of the linked C++ archives (Makefile LIBS_HASH), logged at start.
var libs = "unknown"

func main() {
	if err := run(); err != nil {
		logging.New("instance", slog.LevelInfo).Error("startup", "err", err)
		os.Exit(1)
	}
}

func run() error {
	cfg := struct {
		conf.Version
		Debug       bool   `conf:"help:enable debug logging (every JAM message)"`
		Addr        string `conf:"default:0.0.0.0:26600,help:UDP listen address"`
		Advertise   string `conf:"default:127.0.0.1:26600,help:address the client is told to connect to (in 20600)"`
		LogicAddr   string `conf:"default:127.0.0.1:3727,help:lobbylogic's instance port (lobbylogic --instance-addr)"`
		ControlAddr string `conf:"default:127.0.0.1:3729,help:control API address (loopback only)"`
		Batch       bool   `conf:"help:let updates/destroys/Statescript entries share a packet (a create still ends one)"`
		LeadTarget  int    `conf:"default:4,help:client lead to steer to (frames its input arrives early)"`
		LeadBand    int    `conf:"default:1,help:dead band around the lead target (frames)"`
		Content     string `conf:"default:../game/owbetaclient,help:the 0.8 install whose CASC data the Statescript graphs are read from"`
		NoSim       bool   `conf:"help:DEPRECATED (to be removed): don't run hero Statescript on the server; send the start-up only"`
		Sim         bool   `conf:"help:DEPRECATED: the default (kept so old command lines work)"`
		Shadow      bool   `conf:"help:simulate without sending the deltas; keeps per-frame snapshots for the shadow compare (GET /shadow, T052)"`
		Evidence    string `conf:"help:shadow compare evidence (shadowcmp -evidence): states with mismatches are not corrected"`
		DeltaEvery  uint32 `conf:"default:1,help:Statescript delta interval in ticks (the client replays on each)"`
		NoGame      bool   `conf:"help:don't run the gamemode brain and objective graphs on the server (docs/InstanceServer.md §12)"`
		Setup       int    `conf:"default:10,help:setup phase length in seconds before the assault starts"`
	}{
		Version: conf.Version{Build: build, Desc: "Game instance server for the Overwatch 0.8 client"},
	}
	help, err := conf.Parse("INSTANCE", &cfg)
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
	log := logging.New("instance", level)
	out, err := conf.String(&cfg)
	if err != nil {
		return fmt.Errorf("print config: %w", err)
	}
	log.Info("config: "+strings.Join(strings.Fields(out), " "), "libs", libs)

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	mcfg := match.Config{Batch: cfg.Batch, Policy: clock.Policy{Target: cfg.LeadTarget, Band: cfg.LeadBand}, DeltaEvery: cfg.DeltaEvery,
		Shadow: cfg.Shadow}
	if cfg.NoSim {
		log.Warn("--no-sim is deprecated and will be removed: the server simulates hero Statescript by default (docs/InstanceServer.md §10)")
	}
	if cfg.Sim {
		log.Warn("--sim is deprecated: simulating is the default")
	}
	if !cfg.NoSim || cfg.Shadow {
		store, err := content.Open(cfg.Content, nil)
		if err != nil {
			return fmt.Errorf("content (--content): %w", err)
		}
		defer store.Close()
		log.Info("content: " + store.Describe())
		engine := sim.NewEngine(log.With("part", "sim"), store)
		defer engine.Close()
		if cfg.Evidence != "" {
			b, err := os.ReadFile(cfg.Evidence)
			if err != nil {
				return fmt.Errorf("evidence: %w", err)
			}
			n, err := engine.Policy.LoadEvidence(b)
			if err != nil {
				return fmt.Errorf("evidence %s: %w", cfg.Evidence, err)
			}
			log.Info("sim: shadow evidence loaded: states with mismatches are not corrected", "file", cfg.Evidence, "frozen_states", n)
		}
		var prios []uint64 // StackPriority records (GUID type 0x0C4)
		for _, r := range store.Resources() {
			if r.GUID>>52 == 0x0C4 {
				prios = append(prios, r.GUID)
			}
		}
		n, err := engine.LoadStackPriorities(prios)
		if err != nil {
			return fmt.Errorf("stack priorities: %w", err)
		}
		log.Info("sim: stack priorities loaded", "count", n)
		if !cfg.NoGame {
			gcfg := game.Config{SetupFrames: uint32(cfg.Setup * 1000 / 16)}
			glog := log.With("part", "game")
			mcfg.NewGame = func(w *world.World, frame uint32) *game.Game {
				g, err := game.New(glog, engine, w, frame, gcfg)
				if err != nil {
					glog.Error("rules host not started", "map", w.Map.Name, "err", err)
				}
				return g
			}
		}
		mcfg.NewSim = func(*world.World) sim.Host {
			rt := engine.NewRuntime()
			if cfg.Shadow {
				rt.Ring, rt.Unsent = 1024, true
			}
			return rt
		}
	}
	srv := server.New(log, server.Config{
		Addr: cfg.Addr, Advertise: cfg.Advertise, LogicAddr: cfg.LogicAddr, Match: mcfg,
	})
	api := &control.API{Log: log.With("part", "control"), Server: srv}
	errs := make(chan error, 2)
	go func() { errs <- srv.Run(ctx) }()
	go func() { errs <- api.ListenAndServe(ctx, cfg.ControlAddr) }()
	for range 2 {
		if err := <-errs; err != nil {
			stop()
			return err
		}
	}
	log.Info("shut down")
	return nil
}
