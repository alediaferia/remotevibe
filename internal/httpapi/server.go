// Package httpapi serves the phone-facing JSON API and the PWA that drives it.
//
// There is no authentication layer here on purpose: remotevibe binds to
// localhost and is exposed to exactly one tailnet by `tailscale serve`. Adding
// a login screen to a single-user, tailnet-only service buys nothing.
package httpapi

import (
	"context"
	"encoding/json"
	"fmt"
	"io/fs"
	"log/slog"
	"net/http"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/alediaferia/remotevibe/internal/agent"
	"github.com/alediaferia/remotevibe/internal/config"
	"github.com/alediaferia/remotevibe/internal/dockerx"
	"github.com/alediaferia/remotevibe/internal/ghclient"
)

// Session is the API representation of one running agent.
type Session struct {
	ID        string    `json:"id"`
	Repo      string    `json:"repo"`
	Branch    string    `json:"branch"`
	Agent     string    `json:"agent"`
	Name      string    `json:"name"`
	Status    string    `json:"status"` // starting | running | stopped | error
	Container string    `json:"container"`
	CreatedAt time.Time `json:"created_at"`
	Message   string    `json:"message,omitempty"`
	DiskBytes int64     `json:"disk_bytes"`
	DiskKnown bool      `json:"disk_known"` // false when the size lookup failed or hasn't run yet
}

type Server struct {
	cfg    *config.Config
	docker *dockerx.Client
	gh     *ghclient.Client
	web    fs.FS
	log    *slog.Logger

	mu    sync.Mutex
	notes map[string]string // sessionID -> last failure message
	busy  map[string]bool   // sessionIDs with a start in flight

	diskMu         sync.Mutex
	diskAt         time.Time
	diskSizes      map[string]int64
	diskErr        error
	diskRefreshing bool
}

// diskCacheTTL bounds how often `docker system df -v` runs: it scans every
// volume and image on the host, and the UI polls sessions every 5s.
const diskCacheTTL = 60 * time.Second

func New(cfg *config.Config, docker *dockerx.Client, gh *ghclient.Client, web fs.FS, log *slog.Logger) *Server {
	return &Server{cfg: cfg, docker: docker, gh: gh, web: web, log: log,
		notes: map[string]string{}, busy: map[string]bool{}}
}

func (s *Server) Handler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /healthz", s.handleHealth)
	mux.HandleFunc("GET /api/repos", s.handleRepos)
	mux.HandleFunc("GET /api/agents", s.handleAgents)
	mux.HandleFunc("GET /api/sessions", s.handleListSessions)
	mux.HandleFunc("POST /api/sessions", s.handleCreateSession)
	mux.HandleFunc("DELETE /api/sessions/{id}", s.handleDeleteSession)
	mux.HandleFunc("GET /api/sessions/{id}/logs", s.handleLogs)
	mux.HandleFunc("GET /api/storage", s.handleStorage)
	mux.HandleFunc("DELETE /api/volumes/{name}", s.handleDeleteVolume)
	mux.Handle("/", http.FileServer(http.FS(s.web)))
	return mux
}

// --- helpers ---------------------------------------------------------------

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}

func writeErr(w http.ResponseWriter, status int, err error) {
	writeJSON(w, status, map[string]string{"error": err.Error()})
}

var slugRe = regexp.MustCompile(`[^a-z0-9]+`)

// slug turns "Owner/My.Repo" into "owner-my-repo": the session id, the
// container suffix and the volume suffix all derive from it, which is what
// makes "start" idempotent per repository.
func slug(repo string) string {
	return strings.Trim(slugRe.ReplaceAllString(strings.ToLower(repo), "-"), "-")
}

func (s *Server) containerName(id string) string { return s.cfg.ContainerName + id }
func (s *Server) volumeName(id string) string    { return s.cfg.WorkspacePrefix + id }

// homeVolume holds the agent's own state (its conversation, above all) so a
// container restart resumes the session instead of silently starting a new one
// under the same name. It is per session, not shared, which keeps concurrent
// sessions off each other's config file.
func (s *Server) homeVolume(id string) string { return s.cfg.HomePrefix + id }

func (s *Server) sessionVolumes(id string) []string {
	return []string{s.volumeName(id), s.homeVolume(id)}
}

// acquire marks a session id as having a start in flight, so two taps cannot
// race past the "does it already exist" check into a duplicate docker run.
func (s *Server) acquire(id string) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.busy[id] {
		return false
	}
	s.busy[id] = true
	return true
}

func (s *Server) release(id string) {
	s.mu.Lock()
	delete(s.busy, id)
	s.mu.Unlock()
}

// --- handlers --------------------------------------------------------------

func (s *Server) handleHealth(w http.ResponseWriter, r *http.Request) {
	resp := map[string]any{"ok": true, "auth_mode": s.cfg.AuthMode, "image": s.cfg.Image}
	if err := s.docker.Ping(r.Context()); err != nil {
		resp["ok"] = false
		resp["error"] = err.Error()
		writeJSON(w, http.StatusServiceUnavailable, resp)
		return
	}
	resp["image_present"] = s.docker.ImageExists(r.Context(), s.cfg.Image)
	writeJSON(w, http.StatusOK, resp)
}

func (s *Server) handleRepos(w http.ResponseWriter, r *http.Request) {
	if r.URL.Query().Get("refresh") == "1" {
		s.gh.Invalidate()
	}
	repos, err := s.gh.List(r.Context(), r.URL.Query().Get("q"))
	if err != nil {
		writeErr(w, http.StatusBadGateway, err)
		return
	}
	if repos == nil {
		repos = []ghclient.Repo{}
	}
	writeJSON(w, http.StatusOK, map[string]any{"repos": repos})
}

func (s *Server) handleAgents(w http.ResponseWriter, _ *http.Request) {
	type item struct {
		Name      string `json:"name"`
		Supported bool   `json:"supported"`
		Reason    string `json:"reason,omitempty"`
	}
	out := make([]item, 0, len(agent.Registry))
	for _, d := range agent.Registry {
		it := item{Name: d.Name(), Supported: true}
		if err := d.Supported(); err != nil {
			it.Supported, it.Reason = false, err.Error()
		}
		out = append(out, it)
	}
	writeJSON(w, http.StatusOK, map[string]any{"agents": out})
}

func (s *Server) handleListSessions(w http.ResponseWriter, r *http.Request) {
	sessions, err := s.sessions(r.Context())
	if err != nil {
		writeErr(w, http.StatusBadGateway, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"sessions": sessions})
}

type createRequest struct {
	Repo   string `json:"repo"`
	Branch string `json:"branch"`
	Agent  string `json:"agent"`
}

func (s *Server) handleCreateSession(w http.ResponseWriter, r *http.Request) {
	var req createRequest
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 8<<10)).Decode(&req); err != nil {
		writeErr(w, http.StatusBadRequest, fmt.Errorf("invalid JSON body: %w", err))
		return
	}
	req.Repo = strings.TrimSpace(req.Repo)
	if req.Repo == "" {
		writeErr(w, http.StatusBadRequest, fmt.Errorf("repo is required"))
		return
	}

	drv, err := agent.Get(strings.TrimSpace(req.Agent))
	if err != nil {
		writeErr(w, http.StatusBadRequest, err)
		return
	}
	if err := drv.Supported(); err != nil {
		writeErr(w, http.StatusNotImplemented, err)
		return
	}

	// The repo must be visible to the token; this also gives us the default
	// branch and a canonical owner/name spelling.
	repo, err := s.gh.Get(r.Context(), req.Repo)
	if err != nil {
		writeErr(w, http.StatusBadRequest, err)
		return
	}
	branch := strings.TrimSpace(req.Branch)
	if branch == "" {
		branch = repo.DefaultBranch
	}

	id := slug(repo.FullName)
	name := s.containerName(id)

	if !s.acquire(id) {
		writeJSON(w, http.StatusConflict, map[string]any{
			"error": "a session for this repository is already starting",
		})
		return
	}
	defer s.release(id)

	// Idempotent start: an existing container for this repo wins. A stopped one
	// is replaced, but its volumes survive, so the checkout and the agent's
	// conversation carry over.
	if existing, err := s.docker.Inspect(r.Context(), name); err == nil && existing != nil {
		if existing.State == "running" {
			writeJSON(w, http.StatusConflict, map[string]any{
				"error":   "a session for this repository is already running",
				"session": s.toSession(*existing, nil, false),
			})
			return
		}
		s.log.Info("replacing stopped session container", "session", id)
		if err := s.docker.Remove(r.Context(), name, nil, false); err != nil {
			writeErr(w, http.StatusInternalServerError,
				fmt.Errorf("could not clear the previous container for this repository: %w", err))
			return
		}
	}

	sessionName := repo.Name
	if s.cfg.SessionPrefix != "" {
		sessionName = s.cfg.SessionPrefix + "/" + repo.Name
	}

	spec := dockerx.RunSpec{
		Name:  name,
		Image: s.cfg.Image,
		Labels: map[string]string{
			dockerx.LabelSession: "1",
			dockerx.LabelRepo:    repo.FullName,
			dockerx.LabelBranch:  branch,
			dockerx.LabelAgent:   drv.Name(),
			dockerx.LabelName:    sessionName,
			dockerx.LabelCreated: time.Now().UTC().Format(time.RFC3339),
		},
		EnvPass: append([]string{"RV_GITHUB_TOKEN", "RV_GIT_NAME", "RV_GIT_EMAIL"}, drv.Env()...),
		Env: map[string]string{
			"RV_REPO":   repo.FullName,
			"RV_BRANCH": branch,
			"RV_AGENT":  drv.Name(),
			"RV_AGENT_CMD": drv.Command(agent.Spec{
				Repo:           repo.FullName,
				Branch:         branch,
				SessionName:    sessionName,
				PermissionMode: s.cfg.PermissionMode,
				Model:          s.cfg.Model,
				ExtraArgs:      s.cfg.ExtraArgs,
			}),
			"RV_SESSION_ID": id,
		},
		Volumes: []string{
			s.volumeName(id) + ":/workspace",
			s.homeVolume(id) + ":" + config.ConfigDir,
		},
		CPUs:   s.cfg.CPUs,
		Memory: s.cfg.Memory,
	}
	switch s.cfg.AuthMode {
	case config.AuthSeeded:
		// The canonical profile is mounted read-only and copied into the
		// session's own volume on first start, so a new repo never lands on a
		// sign-in prompt nobody is there to answer.
		spec.Volumes = append(spec.Volumes, s.cfg.AgentHomeDir()+":"+config.SeedDir+":ro")
	case config.AuthSharedHome:
		// One profile for every session: nothing to seed, refreshes shared, and
		// a config file they all write to.
		spec.Volumes[1] = s.cfg.AgentHomeDir() + ":" + config.ConfigDir
	}

	if !s.docker.ImageExists(r.Context(), s.cfg.Image) {
		writeErr(w, http.StatusPreconditionFailed,
			fmt.Errorf("agent image %s is not built — run `make image` on the VPS", s.cfg.Image))
		return
	}

	if _, err := s.docker.Run(r.Context(), spec); err != nil {
		s.setNote(id, err.Error())
		writeErr(w, http.StatusInternalServerError, err)
		return
	}
	s.clearNote(id)
	s.log.Info("session started", "session", id, "repo", repo.FullName, "branch", branch, "agent", drv.Name())

	ct, err := s.docker.Inspect(r.Context(), name)
	if err != nil || ct == nil {
		writeJSON(w, http.StatusCreated, map[string]any{"session": Session{
			ID: id, Repo: repo.FullName, Branch: branch, Agent: drv.Name(),
			Name: sessionName, Status: "starting", Container: name, CreatedAt: time.Now().UTC(),
		}})
		return
	}
	writeJSON(w, http.StatusCreated, map[string]any{"session": s.toSession(*ct, nil, false)})
}

func (s *Server) handleDeleteSession(w http.ResponseWriter, r *http.Request) {
	id := slug(r.PathValue("id"))
	if id == "" {
		writeErr(w, http.StatusBadRequest, fmt.Errorf("missing session id"))
		return
	}
	dropVolume := r.URL.Query().Get("purge") == "1"
	if err := s.docker.Remove(r.Context(), s.containerName(id), s.sessionVolumes(id), dropVolume); err != nil {
		writeErr(w, http.StatusInternalServerError, err)
		return
	}
	s.clearNote(id)
	s.log.Info("session stopped", "session", id, "purged", dropVolume)
	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) handleLogs(w http.ResponseWriter, r *http.Request) {
	id := slug(r.PathValue("id"))
	tail, _ := strconv.Atoi(r.URL.Query().Get("tail"))
	name := s.containerName(id)
	out, err := s.docker.Logs(r.Context(), name, tail)
	if err != nil {
		writeErr(w, http.StatusNotFound, err)
		return
	}

	// The startup log says whether the clone worked; the tmux pane says whether
	// the agent is happy. Anyone reading logs from a phone needs both.
	var b strings.Builder
	b.WriteString("=== container startup ===\n")
	b.WriteString(out)
	b.WriteString("\n=== agent pane ===\n")
	if pane, err := s.docker.PaneLogs(r.Context(), name, tail); err == nil {
		b.WriteString(pane)
	} else {
		b.WriteString("(unavailable: " + err.Error() + ")\n")
	}

	w.Header().Set("Content-Type", "text/plain; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	_, _ = w.Write([]byte(b.String()))
}

// --- storage -----------------------------------------------------------

// volumeSizes returns the cached volume-size map, refreshing it from Docker
// once diskCacheTTL has elapsed. The second return value is false when the
// underlying `docker system df -v` call failed or has never succeeded, so
// callers can distinguish "genuinely empty" from "unknown".
func (s *Server) volumeSizes(ctx context.Context) (map[string]int64, bool) {
	s.diskMu.Lock()
	sizes, known := s.diskSizes, s.diskSizes != nil
	stale := time.Since(s.diskAt) >= diskCacheTTL
	first := !known && !s.diskRefreshing
	if stale && !s.diskRefreshing {
		s.diskRefreshing = true
		// `docker system df -v` walks every volume and image on the host and
		// can take seconds. Refresh it off the request path and keep serving
		// the previous answer: a size a minute out of date is fine, a session
		// list that stalls behind Docker is not.
		go s.refreshVolumeSizes()
	}
	s.diskMu.Unlock()

	if first {
		// Nothing cached yet and a refresh has only just been kicked off; the
		// caller reports sizes as unknown until it lands.
		return nil, false
	}
	return sizes, known
}

func (s *Server) refreshVolumeSizes() {
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	sizes, err := s.docker.VolumeSizes(ctx)

	s.diskMu.Lock()
	defer s.diskMu.Unlock()
	s.diskRefreshing = false
	if err != nil {
		// Keep serving a stale-but-known map rather than flipping every
		// session to "unknown" because of one failed lookup.
		s.diskErr = err
		s.log.Warn("could not read volume sizes", "err", err)
		return
	}
	s.diskAt, s.diskSizes, s.diskErr = time.Now(), sizes, nil
}

// volumeKind classifies a remotevibe volume name and returns the session id
// it belongs to. ok is false for a name matching neither prefix.
func (s *Server) volumeKind(name string) (id, kind string, ok bool) {
	switch {
	case strings.HasPrefix(name, s.cfg.WorkspacePrefix):
		return strings.TrimPrefix(name, s.cfg.WorkspacePrefix), "workspace", true
	case strings.HasPrefix(name, s.cfg.HomePrefix):
		return strings.TrimPrefix(name, s.cfg.HomePrefix), "profile", true
	default:
		return "", "", false
	}
}

type storageOrphan struct {
	Volume string `json:"volume"`
	Bytes  int64  `json:"bytes"`
	Kind   string `json:"kind"`
}

func (s *Server) handleStorage(w http.ResponseWriter, r *http.Request) {
	containers, err := s.docker.List(r.Context())
	if err != nil {
		writeErr(w, http.StatusBadGateway, err)
		return
	}
	running := map[string]bool{} // session id -> container is running
	exists := map[string]bool{}  // session id -> container exists at all
	for _, ct := range containers {
		id := strings.TrimPrefix(ct.Name, s.cfg.ContainerName)
		exists[id] = true
		if ct.State == "running" {
			running[id] = true
		}
	}

	names, err := s.docker.VolumeNames(r.Context(), s.cfg.WorkspacePrefix, s.cfg.HomePrefix)
	if err != nil {
		writeErr(w, http.StatusBadGateway, err)
		return
	}
	sizes, sizesKnown := s.volumeSizes(r.Context())

	var total, reclaimable int64
	orphans := []storageOrphan{}
	for _, name := range names {
		b := sizes[name]
		total += b
		id, kind, ok := s.volumeKind(name)
		if !ok {
			continue
		}
		switch {
		case !exists[id]:
			orphans = append(orphans, storageOrphan{Volume: name, Bytes: b, Kind: kind})
			reclaimable += b
		case !running[id]:
			// Belongs to a stopped-but-resumable session: not an orphan, but
			// still space that could be reclaimed by purging that session.
			reclaimable += b
		}
	}

	writeJSON(w, http.StatusOK, map[string]any{
		"total_bytes":       total,
		"reclaimable_bytes": reclaimable,
		// Sizes arrive a moment after the first request, once the background
		// lookup lands. Say so, rather than letting the UI report a confident
		// "0 B" for volumes it has simply not measured yet.
		"sizes_known": sizesKnown,
		"orphans":     orphans,
	})
}

func (s *Server) handleDeleteVolume(w http.ResponseWriter, r *http.Request) {
	name := r.PathValue("name")
	id, _, ok := s.volumeKind(name)
	if !ok {
		writeErr(w, http.StatusBadRequest,
			fmt.Errorf("refusing to delete %q: not a remotevibe volume", name))
		return
	}

	ct, err := s.docker.Inspect(r.Context(), s.containerName(id))
	if err != nil && err != dockerx.ErrNotFound {
		writeErr(w, http.StatusBadGateway, err)
		return
	}
	if ct != nil {
		writeErr(w, http.StatusConflict,
			fmt.Errorf("session %q still has a container; stop it before deleting %s", id, name))
		return
	}

	if err := s.docker.RemoveVolume(r.Context(), name); err != nil {
		writeErr(w, http.StatusInternalServerError, err)
		return
	}
	s.log.Info("volume removed", "volume", name)
	w.WriteHeader(http.StatusNoContent)
}

// --- session mapping -------------------------------------------------------

func (s *Server) sessions(ctx context.Context) ([]Session, error) {
	containers, err := s.docker.List(ctx)
	if err != nil {
		return nil, err
	}
	sizes, sizesKnown := s.volumeSizes(ctx)
	out := make([]Session, 0, len(containers))
	for _, ct := range containers {
		out = append(out, s.toSession(ct, sizes, sizesKnown))
	}
	return out, nil
}

func (s *Server) toSession(ct dockerx.Container, sizes map[string]int64, sizesKnown bool) Session {
	id := strings.TrimPrefix(ct.Name, s.cfg.ContainerName)
	sess := Session{
		ID:        id,
		Repo:      ct.Labels[dockerx.LabelRepo],
		Branch:    ct.Labels[dockerx.LabelBranch],
		Agent:     ct.Labels[dockerx.LabelAgent],
		Name:      ct.Labels[dockerx.LabelName],
		Container: ct.Name,
		Message:   s.note(id),
		DiskKnown: sizesKnown,
	}
	if sizesKnown {
		sess.DiskBytes = sizes[s.volumeName(id)] + sizes[s.homeVolume(id)]
	}
	if t, err := time.Parse(time.RFC3339, ct.Labels[dockerx.LabelCreated]); err == nil {
		sess.CreatedAt = t
	} else {
		sess.CreatedAt = ct.Started
	}

	// The container's HEALTHCHECK is what distinguishes "the container is up"
	// from "the agent is actually registered and waiting on the phone".
	switch {
	case ct.State != "running":
		sess.Status = "stopped"
	case ct.Health == "healthy":
		sess.Status = "running"
	case ct.Health == "unhealthy":
		sess.Status = "error"
		if sess.Message == "" {
			sess.Message = s.unhealthyReason(ct.Name)
		}
	default:
		// Docker's health start period means a stuck container reports
		// "starting" for minutes. A sign-in prompt is not progress, so surface
		// it as soon as the entrypoint has had time to notice it.
		if time.Since(ct.Started) > 20*time.Second && s.needsLogin(ct.Name) {
			sess.Status = "error"
			if sess.Message == "" {
				sess.Message = needsLoginMsg
			}
		} else {
			sess.Status = "starting"
		}
	}
	return sess
}

const needsLoginMsg = "the agent is waiting for a sign-in — run `make auth` on the host, then start this session again"

// needsLogin reports whether the container gave up on starting because the
// agent asked to sign in. Nobody is at the keyboard of a container started from
// a phone, so this never resolves on its own and should not be reported as
// progress.
func (s *Server) needsLogin(container string) bool {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	_, err := s.docker.Exec(ctx, container, "test", "-f", "/home/vibe/.remotevibe/needs-login")
	return err == nil
}

// unhealthyReason distinguishes the two ways a session dies: the agent exited,
// or it is stuck on a sign-in prompt.
func (s *Server) unhealthyReason(container string) string {
	if s.needsLogin(container) {
		return needsLoginMsg
	}
	return "the agent process is not running inside the container — check the logs"
}

func (s *Server) setNote(id, msg string) {
	s.mu.Lock()
	s.notes[id] = msg
	s.mu.Unlock()
}

func (s *Server) clearNote(id string) {
	s.mu.Lock()
	delete(s.notes, id)
	s.mu.Unlock()
}

func (s *Server) note(id string) string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.notes[id]
}
