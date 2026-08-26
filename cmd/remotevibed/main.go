// Command remotevibed is the remotevibe control plane.
//
// It serves a small PWA and JSON API on localhost; `tailscale serve` puts it
// on the tailnet with HTTPS. Tapping a repository on the phone starts a
// container that clones it and launches a coding agent in remote-control mode,
// which is then picked up by the vendor's phone app.
package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/alediaferia/remotevibe/internal/config"
	"github.com/alediaferia/remotevibe/internal/dockerx"
	"github.com/alediaferia/remotevibe/internal/ghclient"
	"github.com/alediaferia/remotevibe/internal/httpapi"
	"github.com/alediaferia/remotevibe/web"
)

var version = "dev"

func main() {
	showVersion := flag.Bool("version", false, "print version and exit")
	check := flag.Bool("check", false, "validate configuration and environment, then exit")
	flag.Parse()

	if *showVersion {
		fmt.Println("remotevibed", version)
		return
	}

	log := slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelInfo}))

	cfg, err := config.Load()
	if err != nil {
		log.Error("configuration error", "err", err)
		os.Exit(2)
	}

	docker := dockerx.New(cfg.DockerBin)
	ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer cancel()

	if err := docker.Ping(ctx); err != nil {
		log.Error("cannot reach docker", "err", err)
		os.Exit(2)
	}
	if !docker.ImageExists(ctx, cfg.Image) {
		log.Warn("agent image is missing; sessions will fail to start until it is built",
			"image", cfg.Image, "fix", "make image")
	}
	if cfg.AuthMode == config.AuthSeeded || cfg.AuthMode == config.AuthSharedHome {
		if err := os.MkdirAll(cfg.AgentHomeDir(), 0o700); err != nil {
			log.Error("cannot create agent home", "dir", cfg.AgentHomeDir(), "err", err)
			os.Exit(2)
		}
		for _, f := range []string{".credentials.json", ".claude.json"} {
			if _, err := os.Stat(cfg.AgentHomeDir() + "/" + f); err != nil {
				// Both halves are needed: credentials alone leave the agent
				// asking to sign in, which a phone-started session cannot answer.
				log.Warn("agent profile is incomplete; sessions will stall on a sign-in prompt",
					"dir", cfg.AgentHomeDir(), "missing", f, "fix", "make auth")
			}
		}
	}

	if *check {
		log.Info("configuration OK", "addr", cfg.Addr, "image", cfg.Image, "auth_mode", cfg.AuthMode)
		return
	}

	srv := httpapi.New(cfg, docker, ghclient.New(cfg.GitHubToken), web.Files, log)
	httpSrv := &http.Server{
		Addr:              cfg.Addr,
		Handler:           srv.Handler(),
		ReadHeaderTimeout: 10 * time.Second,
	}

	go func() {
		<-ctx.Done()
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		_ = httpSrv.Shutdown(shutdownCtx)
	}()

	log.Info("remotevibe listening", "addr", cfg.Addr, "auth_mode", cfg.AuthMode, "version", version)
	if err := httpSrv.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
		log.Error("server stopped", "err", err)
		os.Exit(1)
	}
	log.Info("shut down cleanly")
}
