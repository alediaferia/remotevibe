// Package agent describes the coding agents remotevibe can launch inside a
// session container.
//
// A driver contributes two things: the environment variables the container
// needs, and the shell command line that starts the agent in "remote control"
// mode — the mode where the session registers itself with the vendor's backend
// so it shows up in the vendor's phone app. Everything else (cloning, tmux,
// supervision) is agent-agnostic and lives in the container entrypoint.
package agent

import (
	"errors"
	"fmt"
	"strings"
)

// Spec is the per-session request a driver renders into a command line.
type Spec struct {
	Repo           string // owner/name
	Branch         string
	SessionName    string // name shown in the phone app
	PermissionMode string
	Model          string
	ExtraArgs      string
}

// Driver launches one kind of agent.
type Driver interface {
	// Name is the stable identifier used by the API ("claude", "codex").
	Name() string
	// Supported reports whether this driver can be used today. A driver that
	// returns an error is still listed by the API, greyed out, with the reason.
	Supported() error
	// Env returns names of environment variables to forward from the daemon's
	// environment into the container (values are never placed in argv).
	Env() []string
	// Command renders the shell command that runs the agent inside tmux.
	Command(s Spec) string
}

// Registry holds every known driver in display order.
var Registry = []Driver{Claude{}, Codex{}}

// Get returns the driver with the given name.
func Get(name string) (Driver, error) {
	if name == "" {
		name = "claude"
	}
	for _, d := range Registry {
		if d.Name() == name {
			return d, nil
		}
	}
	return nil, fmt.Errorf("unknown agent %q", name)
}

// Claude drives Claude Code's Remote Control mode.
type Claude struct{}

func (Claude) Name() string     { return "claude" }
func (Claude) Supported() error { return nil }
func (Claude) Env() []string {
	return []string{"CLAUDE_CODE_OAUTH_TOKEN", "ANTHROPIC_MODEL"}
}

func (Claude) Command(s Spec) string {
	args := []string{"claude", "--remote-control", shellQuote(s.SessionName)}
	if s.PermissionMode != "" {
		args = append(args, "--permission-mode", shellQuote(s.PermissionMode))
	}
	if s.Model != "" {
		args = append(args, "--model", shellQuote(s.Model))
	}
	if extra := strings.TrimSpace(s.ExtraArgs); extra != "" {
		args = append(args, extra)
	}
	return strings.Join(args, " ")
}

// Codex is a placeholder for OpenAI's Codex CLI.
//
// remotevibe's flow needs an equivalent of Remote Control: a locally running
// agent session that registers itself so the vendor's phone app can attach to
// it. Whether Codex exposes that has not been verified — see docs/codex.md.
// The driver exists so the shape of the integration is settled; it refuses to
// start until someone confirms the flag and fills in Command.
type Codex struct{}

func (Codex) Name() string { return "codex" }
func (Codex) Supported() error {
	return errors.New("codex support is not implemented yet: no verified phone-attachable remote session mode (see docs/codex.md)")
}
func (Codex) Env() []string { return []string{"OPENAI_API_KEY"} }
func (Codex) Command(Spec) string {
	return "echo 'codex driver not implemented' >&2; exit 64"
}

// shellQuote wraps a value in single quotes for safe interpolation into the
// command string the entrypoint evaluates.
func shellQuote(v string) string {
	return "'" + strings.ReplaceAll(v, "'", `'\''`) + "'"
}
