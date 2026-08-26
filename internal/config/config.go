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

// Auth modes for the agent inside the container.
//
// All three exist because the sign-in is interactive and a session started
// from a phone has nobody to answer it: the question each mode answers is how
// an already-authenticated profile gets into a brand new container.
const (
	// AuthSeeded copies the canonical profile created by scripts/bootstrap-auth.sh
	// into each session's own volume on first start. One sign-in ever, and
	// sessions still own their credentials, conversation and project state
	// separately afterwards.
	AuthSeeded = "seeded"
	// AuthSharedHome bind-mounts one host directory as every container's agent
	// profile. Nothing to re-seed and refreshes are shared, at the cost of
	// concurrent sessions writing to the same config file.
	AuthSharedHome = "shared-home"
)

// ConfigDir is where the image points CLAUDE_CONFIG_DIR: one directory holding
// both the credentials and the account record, so a single volume carries a
// usable login.
const ConfigDir = "/home/vibe/.claude-state"

// SeedDir is where the canonical profile is mounted, read-only, for seeding.
const SeedDir = "/rv/auth"

type Config struct {
	Addr     string // listen address, fronted by `tailscale serve`
	StateDir string // host directory for agent home / bootstrap artifacts

	DockerBin       string
	Image           string
	ContainerName   string // prefix for session containers
	WorkspacePrefix string // prefix for per-session workspace volumes
	HomePrefix      string // prefix for per-session agent-home volumes
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
		HomePrefix:      env("RV_HOME_VOLUME_PREFIX", "rv-home-"),
		CPUs:            env("RV_CPUS", ""),
		Memory:          env("RV_MEMORY", ""),
		GitHubToken:     env("RV_GITHUB_TOKEN", os.Getenv("GITHUB_TOKEN")),
		GitHubUser:      env("RV_GITHUB_USER", ""),
		AuthMode:        env("RV_AUTH_MODE", AuthSeeded),
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
	case "token":
		// Removed rather than deprecated: a `claude setup-token` token carries
		// no account record, and Remote Control stops and asks for a full
		// browser login anyway — measured, not assumed.
		return nil, errors.New("RV_AUTH_MODE=token is no longer supported: a setup-token " +
			"does not satisfy Remote Control, which still asks for a browser sign-in. " +
			"Set RV_AUTH_MODE=seeded and re-run `make auth`")
	case AuthSeeded, AuthSharedHome:
		// The directory is created on demand; bootstrap-auth.sh populates it.
	default:
		return nil, fmt.Errorf("RV_AUTH_MODE must be %q or %q — got %q",
			AuthSeeded, AuthSharedHome, c.AuthMode)
	}

	// Forward the GitHub token to containers by name, not by value.
	os.Setenv("RV_GITHUB_TOKEN", c.GitHubToken)

	return c, nil
}

// AgentHomeDir is the host path holding the canonical agent profile: the source
// for seeding in seeded mode, and the live profile itself in shared-home mode.
func (c *Config) AgentHomeDir() string {
	return strings.TrimRight(c.StateDir, "/") + "/agent-home"
}
