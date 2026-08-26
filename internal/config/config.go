// Package config loads remotevibe's configuration from the environment.
//
// Every setting has an RV_-prefixed environment variable. Secrets are read
// into the daemon's own environment so they can be forwarded to containers by
// name (docker run -e NAME) rather than by value, keeping them out of argv.
package config

import (
	"errors"
	"fmt"
	"os"
	"strconv"
	"strings"
)

// Auth modes for the Claude agent inside the container.
const (
	// AuthToken forwards a long-lived OAuth token (from `claude setup-token`)
	// as CLAUDE_CODE_OAUTH_TOKEN. Containers stay stateless.
	AuthToken = "token"
	// AuthSharedHome bind-mounts a host directory as the agent's ~/.claude,
	// shared by every session container. Credentials are created once by
	// scripts/bootstrap-auth.sh and refreshed in place by Claude itself.
	AuthSharedHome = "shared-home"
)

type Config struct {
	Addr     string // listen address, fronted by `tailscale serve`
	StateDir string // host directory for agent home / bootstrap artifacts

	DockerBin       string
	Image           string
	ContainerName   string // prefix for session containers
	WorkspacePrefix string // prefix for per-session workspace volumes
	CPUs            string
	Memory          string

	GitHubToken string // RV_GITHUB_TOKEN (also used inside containers for git)
	GitHubUser  string // optional; only used to label the UI

	AuthMode       string
	PermissionMode string
	Model          string
	SessionPrefix  string // prefix for Remote Control session names
	ExtraArgs      string // appended verbatim to the agent command line

	AllowStopAll bool
}

func env(key, def string) string {
	if v := strings.TrimSpace(os.Getenv(key)); v != "" {
		return v
	}
	return def
}

func envBool(key string, def bool) bool {
	v := strings.TrimSpace(os.Getenv(key))
	if v == "" {
		return def
	}
	b, err := strconv.ParseBool(v)
	if err != nil {
		return def
	}
	return b
}

// Load reads configuration from the environment and validates it.
func Load() (*Config, error) {
	c := &Config{
		Addr:            env("RV_ADDR", "127.0.0.1:8787"),
		StateDir:        env("RV_STATE_DIR", "/var/lib/remotevibe"),
		DockerBin:       env("RV_DOCKER_BIN", "docker"),
		Image:           env("RV_AGENT_IMAGE", "remotevibe/agent:latest"),
		ContainerName:   env("RV_CONTAINER_PREFIX", "rv-"),
		WorkspacePrefix: env("RV_VOLUME_PREFIX", "rv-ws-"),
		CPUs:            env("RV_CPUS", ""),
		Memory:          env("RV_MEMORY", ""),
		GitHubToken:     env("RV_GITHUB_TOKEN", os.Getenv("GITHUB_TOKEN")),
		GitHubUser:      env("RV_GITHUB_USER", ""),
		AuthMode:        env("RV_AUTH_MODE", AuthToken),
		PermissionMode:  env("RV_PERMISSION_MODE", "bypassPermissions"),
		Model:           env("RV_MODEL", ""),
		SessionPrefix:   env("RV_SESSION_PREFIX", ""),
		ExtraArgs:       env("RV_AGENT_EXTRA_ARGS", ""),
		AllowStopAll:    envBool("RV_ALLOW_STOP_ALL", true),
	}

	if c.GitHubToken == "" {
		return nil, errors.New("RV_GITHUB_TOKEN is required (fine-grained PAT with repo read access)")
	}

	switch c.AuthMode {
	case AuthToken:
		if os.Getenv("CLAUDE_CODE_OAUTH_TOKEN") == "" {
			return nil, errors.New("RV_AUTH_MODE=token requires CLAUDE_CODE_OAUTH_TOKEN " +
				"(generate one with `claude setup-token`), or switch to RV_AUTH_MODE=shared-home")
		}
	case AuthSharedHome:
		// The directory is created on demand; bootstrap-auth.sh populates it.
	default:
		return nil, fmt.Errorf("RV_AUTH_MODE must be %q or %q, got %q", AuthToken, AuthSharedHome, c.AuthMode)
	}

	// Forward the GitHub token to containers by name, not by value.
	os.Setenv("RV_GITHUB_TOKEN", c.GitHubToken)

	return c, nil
}

// AgentHomeDir is the host path shared as ~/.claude in shared-home auth mode.
func (c *Config) AgentHomeDir() string {
	return strings.TrimRight(c.StateDir, "/") + "/agent-home"
}
