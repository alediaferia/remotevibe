// Package dockerx manages session containers through the docker CLI.
//
// Docker is the only source of truth for which sessions exist: every session
// container carries rv.* labels, so the daemon can be restarted (or the VPS
// rebooted) without losing track of anything. There is no separate registry
// file to drift out of sync.
package dockerx

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"os/exec"
	"strconv"
	"strings"
	"time"
)

const (
	LabelSession = "rv.session"
	LabelRepo    = "rv.repo"
	LabelBranch  = "rv.branch"
	LabelAgent   = "rv.agent"
	LabelName    = "rv.name"
	LabelCreated = "rv.created"
)

type Client struct {
	Bin string
}

func New(bin string) *Client {
	if bin == "" {
		bin = "docker"
	}
	return &Client{Bin: bin}
}

func (c *Client) run(ctx context.Context, args ...string) (string, error) {
	cmd := exec.CommandContext(ctx, c.Bin, args...)
	var out, errb bytes.Buffer
	cmd.Stdout = &out
	cmd.Stderr = &errb
	if err := cmd.Run(); err != nil {
		msg := strings.TrimSpace(errb.String())
		if msg == "" {
			msg = err.Error()
		}
		return out.String(), fmt.Errorf("docker %s: %s", strings.Join(args, " "), msg)
	}
	return out.String(), nil
}

// Ping verifies the daemon can talk to Docker at all.
func (c *Client) Ping(ctx context.Context) error {
	_, err := c.run(ctx, "version", "--format", "{{.Server.Version}}")
	return err
}

// ImageExists reports whether the agent image has been built locally.
func (c *Client) ImageExists(ctx context.Context, image string) bool {
	_, err := c.run(ctx, "image", "inspect", image)
	return err == nil
}

// Container is the subset of `docker inspect` remotevibe cares about.
type Container struct {
	Name    string
	ID      string
	State   string // created, running, exited, ...
	Health  string // "", starting, healthy, unhealthy
	Started time.Time
	Labels  map[string]string
}

type inspectPayload struct {
	ID    string `json:"Id"`
	Name  string `json:"Name"`
	State struct {
		Status    string `json:"Status"`
		StartedAt string `json:"StartedAt"`
		Health    *struct {
			Status string `json:"Status"`
		} `json:"Health"`
	} `json:"State"`
	Config struct {
		Labels map[string]string `json:"Labels"`
	} `json:"Config"`
}

// List returns every container labelled as a remotevibe session.
func (c *Client) List(ctx context.Context) ([]Container, error) {
	out, err := c.run(ctx, "ps", "-a", "--filter", "label="+LabelSession+"=1", "--format", "{{.Names}}")
	if err != nil {
		return nil, err
	}
	var names []string
	for _, line := range strings.Split(strings.TrimSpace(out), "\n") {
		if line = strings.TrimSpace(line); line != "" {
			names = append(names, line)
		}
	}
	if len(names) == 0 {
		return nil, nil
	}
	return c.inspect(ctx, names...)
}

// Inspect returns a single container, or ErrNotFound.
func (c *Client) Inspect(ctx context.Context, name string) (*Container, error) {
	list, err := c.inspect(ctx, name)
	if err != nil {
		return nil, err
	}
	if len(list) == 0 {
		return nil, ErrNotFound
	}
	return &list[0], nil
}

var ErrNotFound = fmt.Errorf("container not found")

func (c *Client) inspect(ctx context.Context, names ...string) ([]Container, error) {
	args := append([]string{"inspect", "--type", "container"}, names...)
	out, err := c.run(ctx, args...)
	if err != nil {
		if strings.Contains(err.Error(), "No such") {
			return nil, nil
		}
		return nil, err
	}
	var payload []inspectPayload
	if err := json.Unmarshal([]byte(out), &payload); err != nil {
		return nil, fmt.Errorf("parse docker inspect: %w", err)
	}
	containers := make([]Container, 0, len(payload))
	for _, p := range payload {
		ct := Container{
			ID:     p.ID,
			Name:   strings.TrimPrefix(p.Name, "/"),
			State:  p.State.Status,
			Labels: p.Config.Labels,
		}
		if p.State.Health != nil {
			ct.Health = p.State.Health.Status
		}
		if t, err := time.Parse(time.RFC3339Nano, p.State.StartedAt); err == nil {
			ct.Started = t
		}
		containers = append(containers, ct)
	}
	return containers, nil
}

// RunSpec describes one `docker run`.
type RunSpec struct {
	Name    string
	Image   string
	Labels  map[string]string
	EnvPass []string          // forwarded by name from the daemon's environment
	Env     map[string]string // non-secret values, passed inline
	Volumes []string          // "src:dst[:opts]"
	CPUs    string
	Memory  string
}

// Run starts a detached session container and returns its ID.
func (c *Client) Run(ctx context.Context, spec RunSpec) (string, error) {
	args := []string{"run", "-d", "--name", spec.Name, "--restart", "unless-stopped", "--init"}
	for k, v := range spec.Labels {
		args = append(args, "--label", k+"="+v)
	}
	for _, k := range spec.EnvPass {
		args = append(args, "-e", k) // value inherited from the daemon process
	}
	for k, v := range spec.Env {
		args = append(args, "-e", k+"="+v)
	}
	for _, v := range spec.Volumes {
		args = append(args, "-v", v)
	}
	if spec.CPUs != "" {
		args = append(args, "--cpus", spec.CPUs)
	}
	if spec.Memory != "" {
		args = append(args, "--memory", spec.Memory)
	}
	args = append(args, spec.Image)

	out, err := c.run(ctx, args...)
	if err != nil {
		return "", err
	}
	return strings.TrimSpace(out), nil
}

// Logs returns the tail of a container's combined output.
func (c *Client) Logs(ctx context.Context, name string, tail int) (string, error) {
	if tail <= 0 {
		tail = 200
	}
	cmd := exec.CommandContext(ctx, c.Bin, "logs", "--tail", fmt.Sprint(tail), name)
	var out bytes.Buffer
	cmd.Stdout = &out
	cmd.Stderr = &out // docker logs interleaves both streams; so do we
	if err := cmd.Run(); err != nil && out.Len() == 0 {
		return "", fmt.Errorf("docker logs %s: %w", name, err)
	}
	return out.String(), nil
}

// Remove stops and deletes a container. Its volumes are kept unless
// removeVolumes is set, so a restarted session resumes the same checkout and
// the same agent conversation.
func (c *Client) Remove(ctx context.Context, name string, volumes []string, removeVolumes bool) error {
	if _, err := c.run(ctx, "rm", "-f", name); err != nil && !strings.Contains(err.Error(), "No such") {
		return err
	}
	if !removeVolumes {
		return nil
	}
	for _, v := range volumes {
		if v == "" {
			continue
		}
		if _, err := c.run(ctx, "volume", "rm", v); err != nil && !strings.Contains(err.Error(), "No such") {
			return err
		}
	}
	return nil
}

// PaneLogs returns the tail of the tmux pane the agent runs in. The pane is
// detached, so nothing it prints — including auth failures, which is exactly
// when you go looking — ever reaches the container's stdout.
func (c *Client) PaneLogs(ctx context.Context, name string, lines int) (string, error) {
	if lines <= 0 {
		lines = 200
	}
	return c.Exec(ctx, name, "tmux", "capture-pane", "-p", "-t", "agent", "-S", fmt.Sprintf("-%d", lines))
}

// Exec runs a command inside a container and returns its stdout.
func (c *Client) Exec(ctx context.Context, name string, argv ...string) (string, error) {
	args := append([]string{"exec", name}, argv...)
	return c.run(ctx, args...)
}

// dfPayload is the shape of `docker system df -v --format '{{json .}}'`: one
// JSON object (not one-per-line) with an array per resource kind. Only
// Volumes matters here.
type dfPayload struct {
	Volumes []struct {
		Name string `json:"Name"`
		Size string `json:"Size"` // human-readable, e.g. "52.32MB", "0B"
	} `json:"Volumes"`
}

// VolumeSizes returns the on-disk size, in bytes, of every Docker volume —
// not just remotevibe's — as reported by `docker system df -v`. That command
// is the only way to get a volume's size short of walking its mountpoint as
// root, but it also scans every volume and image on the host, so callers
// should cache the result rather than call this per request.
func (c *Client) VolumeSizes(ctx context.Context) (map[string]int64, error) {
	out, err := c.run(ctx, "system", "df", "-v", "--format", "{{json .}}")
	if err != nil {
		return nil, err
	}
	var payload dfPayload
	if err := json.Unmarshal([]byte(out), &payload); err != nil {
		return nil, fmt.Errorf("parse docker system df -v: %w", err)
	}
	sizes := make(map[string]int64, len(payload.Volumes))
	for _, v := range payload.Volumes {
		b, err := parseHumanSize(v.Size)
		if err != nil {
			continue // one unparseable entry shouldn't sink the whole lookup
		}
		sizes[v.Name] = b
	}
	return sizes, nil
}

// humanSizeUnits mirrors the decimal (1000-based) suffixes Docker's CLI uses
// when formatting volume sizes — "MB" not "MiB". Longest suffixes are matched
// first so "kB" doesn't get shadowed by a stray "B" check.
var humanSizeUnits = []struct {
	suffix string
	factor float64
}{
	{"PB", 1e15},
	{"TB", 1e12},
	{"GB", 1e9},
	{"MB", 1e6},
	{"kB", 1e3},
	{"B", 1},
}

// parseHumanSize converts a Docker-formatted size like "52.32MB" or "0B"
// into bytes.
func parseHumanSize(s string) (int64, error) {
	s = strings.TrimSpace(s)
	for _, u := range humanSizeUnits {
		if strings.HasSuffix(s, u.suffix) {
			numStr := strings.TrimSuffix(s, u.suffix)
			n, err := strconv.ParseFloat(numStr, 64)
			if err != nil {
				return 0, fmt.Errorf("parse size %q: %w", s, err)
			}
			return int64(n * u.factor), nil
		}
	}
	return 0, fmt.Errorf("unrecognized size format %q", s)
}

// VolumeNames lists every Docker volume whose name starts with one of the
// given prefixes. Filtering client-side (rather than with `docker volume ls
// --filter`) keeps the prefix logic in one place and matches VolumeSizes'
// naming, which does no filtering of its own.
func (c *Client) VolumeNames(ctx context.Context, prefixes ...string) ([]string, error) {
	out, err := c.run(ctx, "volume", "ls", "--format", "{{.Name}}")
	if err != nil {
		return nil, err
	}
	var names []string
	for _, line := range strings.Split(strings.TrimSpace(out), "\n") {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		for _, p := range prefixes {
			if strings.HasPrefix(line, p) {
				names = append(names, line)
				break
			}
		}
	}
	return names, nil
}

// RemoveVolume deletes a single named volume. Used to reclaim orphaned
// volumes whose container was already removed without the purge flag.
// Removing an already-gone volume is treated as success, so callers can
// retry a delete without an extra existence check.
func (c *Client) RemoveVolume(ctx context.Context, name string) error {
	if _, err := c.run(ctx, "volume", "rm", name); err != nil && !strings.Contains(err.Error(), "No such") {
		return err
	}
	return nil
}
