// loginserver emulates the Battle.net services the 0.8 client uses to log in
// (ConnectionService, AuthenticationService, ChallengeService, GameUtilitiesService).
package main

import (
	"context"
	"crypto/tls"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"strings"
	"syscall"

	"github.com/ardanlabs/conf/v3"
	"google.golang.org/protobuf/reflect/protoreflect"

	"owserver/internal/bgs"
	"owserver/internal/logging"
	"owserver/internal/login"
)

// build is set at link time: go build -ldflags "-X main.build=..."
var build = "develop"

func main() {
	if err := run(); err != nil {
		logging.New("login", slog.LevelInfo).Error("startup", "err", err)
		os.Exit(1)
	}
}

func run() error {
	cfg := struct {
		conf.Version
		Debug        bool   `conf:"help:enable debug logging"`
		LogKeepalive bool   `conf:"help:log the client's KeepAlive calls"`
		Addr         string `conf:"default:0.0.0.0:1119,help:listen address"`
		TLS          struct {
			Disable  bool   `conf:"help:serve plain TCP instead of TLS (the 0.8 client always uses TLS)"`
			CertFile string `conf:"help:TLS certificate (PEM); a self-signed one is generated if empty"`
			KeyFile  string `conf:"help:TLS private key (PEM)"`
		}
		Lobby struct {
			Host          string `conf:"default:127.0.0.1:3724,help:lobby address (host:port) the client is referred to"`
			SessionSecret string `conf:"default:owserver-dev-secret,mask,help:secret the lobby session keys are derived from; must match the lobby server"`
		}
		Web struct {
			Addr string `conf:"default:0.0.0.0:8081,help:listen address of the web login endpoint"`
			URL  string `conf:"default:http://127.0.0.1:8081/bnetserver/login/,help:web login URL sent to the client; must be reachable from the game"`
			// Empty = the full e-mail/password form.
			DefaultAccount string `conf:"default:player@local,help:account everyone logs in as (empty fields in the login form are accepted); set to empty to require an account name"`
			// With AutoLogin the client shows no login form at all.
			AutoLogin bool `conf:"default:true,help:log in as the default account immediately, without the web login form"`
		}
	}{
		Version: conf.Version{Build: build, Desc: "Battle.net login server for the Overwatch 0.8 client"},
	}

	help, err := conf.Parse("LOGIN", &cfg)
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
	log := logging.New("login", level)

	out, err := conf.String(&cfg)
	if err != nil {
		return fmt.Errorf("print config: %w", err)
	}
	log.Info("config: " + strings.Join(strings.Fields(out), " "))

	var tlsConfig *tls.Config
	if !cfg.TLS.Disable {
		var cert tls.Certificate
		if cfg.TLS.CertFile != "" {
			cert, err = tls.LoadX509KeyPair(cfg.TLS.CertFile, cfg.TLS.KeyFile)
		} else {
			log.Info("generating a self-signed TLS certificate")
			cert, err = bgs.SelfSignedCertificate()
		}
		if err != nil {
			return fmt.Errorf("TLS certificate: %w", err)
		}
		// The client's SChannel dates from 2015; don't rule out older protocol versions.
		tlsConfig = &tls.Config{Certificates: []tls.Certificate{cert}, MinVersion: tls.VersionTLS10}
	}

	svc := login.NewService(cfg.Web.URL, cfg.Lobby.Host, []byte(cfg.Lobby.SessionSecret))
	svc.DefaultAccount = cfg.Web.DefaultAccount
	svc.AutoLogin = cfg.Web.AutoLogin
	srv := bgs.NewServer(log, tlsConfig)
	// Calls without a handler are decoded, logged and left unanswered.
	svc.Register(srv)
	if !cfg.LogKeepalive {
		srv.Quiet = map[protoreflect.FullName]bool{"bgs.protocol.connection.v1.ConnectionService.KeepAlive": true}
	}
	web := &http.Server{Addr: cfg.Web.Addr, Handler: svc.WebHandler(log)}

	// Handling the signals explicitly also re-enables SIGINT when the process was
	// started with it ignored, as bash does for background jobs (make run).
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	errs := make(chan error, 2)
	go func() { errs <- srv.ListenAndServe(ctx, cfg.Addr) }()
	go func() {
		log.Info("web login listening", "addr", cfg.Web.Addr, "url", cfg.Web.URL)
		if err := web.ListenAndServe(); !errors.Is(err, http.ErrServerClosed) {
			errs <- fmt.Errorf("web login: %w", err)
			return
		}
		errs <- nil
	}()
	go func() {
		<-ctx.Done()
		web.Close()
	}()

	// Stop everything as soon as either listener fails or a signal arrives.
	err = <-errs
	stop()
	if err2 := <-errs; err == nil {
		err = err2
	}
	if err != nil {
		return err
	}
	log.Info("shut down")
	return nil
}
