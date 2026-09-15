/* ==========================================================================
   remotevibe — launcher frontend
   Vanilla JS, no build step. Organized top to bottom as:
     1. State
     2. DOM refs
     3. API layer
     4. Formatting helpers
     5. Banner (errors / offline)
     6. Sessions: render + poll
     7. Session actions: stop, logs
     8. Repos: search + render
     9. Start sheet
     10. Init
   ========================================================================== */

(() => {
  "use strict";

  /* ------------------------------------------------------------------ *
   * 1. State
   * ------------------------------------------------------------------ */

  const state = {
    sessions: [],
    repos: [],
    reposQuery: "",
    pollTimer: null,
    pollInFlight: false,
    // null until the first request settles, so the initial "connecting…" state
    // is always replaced rather than treated as already-connected.
    lastPollFailed: null,
    activeStopConfirm: null, // session id currently in "confirm stop" state
    activePurgeConfirm: null, // session id currently in "confirm stop & delete" state
    startSheetRepo: null,    // repo object the start sheet is open for
    startSheetMode: "repo",  // "repo" | "project" — which fields the start sheet shows
    startSheetAgent: "claude",
    starting: false,
    logsSessionId: null,
    storageOpen: false,
    storageLoaded: false,
    storageOrphans: [],
    activeVolumeConfirm: null, // volume name currently in "confirm delete" state
  };

  /* ------------------------------------------------------------------ *
   * 2. DOM refs
   * ------------------------------------------------------------------ */

  const el = {
    banner: document.getElementById("banner"),
    bannerText: document.getElementById("banner-text"),
    bannerDismiss: document.getElementById("banner-dismiss"),

    statusLine: document.getElementById("status-line"),

    sessionsList: document.getElementById("sessions-list"),
    sessionsEmpty: document.getElementById("sessions-empty"),
    sessionsRefresh: document.getElementById("sessions-refresh"),

    readyCallout: document.getElementById("ready-callout"),
    readyCalloutRepo: document.getElementById("ready-callout-repo"),
    readyCalloutClose: document.getElementById("ready-callout-close"),

    repoSearch: document.getElementById("repo-search"),
    reposList: document.getElementById("repos-list"),
    reposEmpty: document.getElementById("repos-empty"),
    reposLoading: document.getElementById("repos-loading"),

    newProjectOpen: document.getElementById("new-project-open"),

    startSheetBackdrop: document.getElementById("start-sheet-backdrop"),
    startSheet: document.getElementById("start-sheet"),
    startSheetTitle: document.getElementById("start-sheet-title"),
    startSheetClose: document.getElementById("start-sheet-close"),
    startSheetRepoFields: document.getElementById("start-sheet-repo-fields"),
    startSheetRepoName: document.getElementById("start-sheet-repo-name"),
    startSheetRepoMeta: document.getElementById("start-sheet-repo-meta"),
    startBranch: document.getElementById("start-branch"),
    startSheetProjectFields: document.getElementById("start-sheet-project-fields"),
    startProjectName: document.getElementById("start-project-name"),
    startProjectGithub: document.getElementById("start-project-github"),
    startProjectPrivateRow: document.getElementById("start-project-private-row"),
    startProjectPrivate: document.getElementById("start-project-private"),
    startProjectHint: document.getElementById("start-project-hint"),
    agentClaude: document.getElementById("agent-claude"),
    agentCodex: document.getElementById("agent-codex"),
    startSubmit: document.getElementById("start-submit"),
    startSubmitLabel: document.getElementById("start-submit-label"),
    startSubmitSpinner: document.getElementById("start-submit-spinner"),

    logsSheetBackdrop: document.getElementById("logs-sheet-backdrop"),
    logsSheet: document.getElementById("logs-sheet"),
    logsSheetClose: document.getElementById("logs-sheet-close"),
    logsRefresh: document.getElementById("logs-refresh"),
    logsContent: document.getElementById("logs-content"),

    storageToggle: document.getElementById("storage-toggle"),
    storageChevron: document.getElementById("storage-chevron"),
    storageBody: document.getElementById("storage-body"),
    storageLoading: document.getElementById("storage-loading"),
    storageContent: document.getElementById("storage-content"),
    storageTotal: document.getElementById("storage-total"),
    storageReclaimable: document.getElementById("storage-reclaimable"),
    storageOrphansList: document.getElementById("storage-orphans-list"),
    storageOrphansEmpty: document.getElementById("storage-orphans-empty"),
  };

  /* ------------------------------------------------------------------ *
   * 3. API layer
   * ------------------------------------------------------------------ */

  const api = {
    async listRepos(query) {
      const url = new URL("/api/repos", window.location.origin);
      if (query) url.searchParams.set("q", query);
      const res = await apiFetch(url.toString());
      const data = await parseJson(res);
      if (!res.ok) throw new ApiError(data.error || "Failed to load repos", res.status);
      return data.repos || [];
    },

    async listSessions() {
      const res = await apiFetch("/api/sessions");
      const data = await parseJson(res);
      if (!res.ok) throw new ApiError(data.error || "Failed to load sessions", res.status);
      return data.sessions || [];
    },

    async createSession(repo, branch, agent) {
      const res = await apiFetch("/api/sessions", {
        method: "POST",
        headers: { "Content-Type": "application/json" },
        body: JSON.stringify({ repo, branch, agent }),
      });
      const data = await parseJson(res);
      if (res.status === 409) {
        // Session already exists — not a hard failure, surface as "already running".
        return { session: data.session, alreadyExisted: true };
      }
      if (!res.ok) throw new ApiError(data.error || "Failed to start session", res.status);
      return { session: data.session, alreadyExisted: false };
    },

    async createProject(project, agent, { createGithubRepo, isPrivate } = {}) {
      const res = await apiFetch("/api/sessions", {
        method: "POST",
        headers: { "Content-Type": "application/json" },
        body: JSON.stringify({
          project,
          agent,
          create_github_repo: !!createGithubRepo,
          private: !!isPrivate,
        }),
      });
      const data = await parseJson(res);
      if (res.status === 409) {
        return { session: data.session, alreadyExisted: true };
      }
      if (!res.ok) throw new ApiError(data.error || "Failed to create project", res.status);
      return { session: data.session, alreadyExisted: false };
    },

    async deleteSession(id, { purge } = {}) {
      const url = new URL(`/api/sessions/${encodeURIComponent(id)}`, window.location.origin);
      if (purge) url.searchParams.set("purge", "1");
      const res = await apiFetch(url.toString(), { method: "DELETE" });
      if (!res.ok && res.status !== 204) {
        const data = await parseJson(res).catch(() => ({}));
        throw new ApiError(data.error || "Failed to stop session", res.status);
      }
    },

    async getStorage() {
      const res = await apiFetch("/api/storage");
      const data = await parseJson(res);
      if (!res.ok) throw new ApiError(data.error || "Failed to load storage", res.status);
      return data;
    },

    async deleteVolume(name) {
      const res = await apiFetch(`/api/volumes/${encodeURIComponent(name)}`, { method: "DELETE" });
      if (!res.ok && res.status !== 204) {
        const data = await parseJson(res).catch(() => ({}));
        throw new ApiError(data.error || "Failed to delete volume", res.status);
      }
    },

    async getLogs(id) {
      const res = await apiFetch(`/api/sessions/${encodeURIComponent(id)}/logs?tail=200`);
      if (!res.ok) {
        const text = await res.text().catch(() => "");
        let message = "Failed to load logs";
        try { message = JSON.parse(text).error || message; } catch (_) { /* not json */ }
        throw new ApiError(message, res.status);
      }
      return res.text();
    },
  };

  class ApiError extends Error {
    constructor(message, status) {
      super(message);
      this.status = status;
    }
  }

  /** Fetch wrapper that turns network failure into a distinguishable error. */
  async function apiFetch(input, init) {
    try {
      const res = await fetch(input, init);
      setOnline(true);
      return res;
    } catch (err) {
      setOnline(false);
      throw new ApiError("Can't reach remotevibe daemon — check your Tailscale connection.", 0);
    }
  }

  async function parseJson(res) {
    try {
      return await res.json();
    } catch (_) {
      return {};
    }
  }

  /* ------------------------------------------------------------------ *
   * 4. Formatting helpers
   * ------------------------------------------------------------------ */

  function relativeTime(isoString) {
    if (!isoString) return "";
    const then = new Date(isoString).getTime();
    if (Number.isNaN(then)) return "";
    const diffSec = Math.max(0, Math.round((Date.now() - then) / 1000));
    if (diffSec < 5) return "just now";
    if (diffSec < 60) return `${diffSec}s ago`;
    const diffMin = Math.round(diffSec / 60);
    if (diffMin < 60) return `${diffMin}m ago`;
    const diffHr = Math.round(diffMin / 60);
    if (diffHr < 24) return `${diffHr}h ago`;
    const diffDay = Math.round(diffHr / 24);
    if (diffDay < 30) return `${diffDay}d ago`;
    const diffMonth = Math.round(diffDay / 30);
    return `${diffMonth}mo ago`;
  }

  /** Formats bytes as Docker itself does — decimal units, ~3 significant digits. */
  function formatBytes(bytes) {
    if (!Number.isFinite(bytes) || bytes < 0) return "—";
    if (bytes === 0) return "0 B";
    const units = ["B", "kB", "MB", "GB", "TB", "PB"];
    let n = bytes;
    let i = 0;
    while (n >= 1000 && i < units.length - 1) {
      n /= 1000;
      i++;
    }
    const digits = i === 0 ? 0 : n < 10 ? 1 : 0;
    return `${n.toFixed(digits)} ${units[i]}`;
  }

  function escapeHtml(str) {
    return String(str ?? "").replace(/[&<>"']/g, (c) => ({
      "&": "&amp;", "<": "&lt;", ">": "&gt;", '"': "&quot;", "'": "&#39;",
    }[c]));
  }

  /* ------------------------------------------------------------------ *
   * 5. Banner (errors / offline)
   * ------------------------------------------------------------------ */

  function showBanner(message) {
    el.bannerText.textContent = message;
    el.banner.hidden = false;
  }

  function dismissBanner() {
    el.banner.hidden = true;
  }

  function setOnline(isOnline) {
    if (state.lastPollFailed !== null && isOnline === !state.lastPollFailed) return;
    state.lastPollFailed = !isOnline;
    if (isOnline) {
      el.statusLine.textContent = "connected";
      el.statusLine.className = "status-line is-online";
    } else {
      el.statusLine.textContent = "daemon unreachable";
      el.statusLine.className = "status-line is-offline";
    }
  }

  el.bannerDismiss.addEventListener("click", dismissBanner);

  /* ------------------------------------------------------------------ *
   * 6. Sessions: render + poll
   * ------------------------------------------------------------------ */

  const STATUS_LABEL = {
    starting: "starting",
    running: "running",
    stopped: "stopped",
    error: "error",
  };

  function renderSessions() {
    el.sessionsList.innerHTML = "";

    if (state.sessions.length === 0) {
      el.sessionsEmpty.hidden = false;
      return;
    }
    el.sessionsEmpty.hidden = true;

    for (const session of state.sessions) {
      el.sessionsList.appendChild(buildSessionCard(session));
    }
  }

  function buildSessionCard(session) {
    const card = document.createElement("div");
    card.className = "session-card";
    card.dataset.id = session.id;

    const status = STATUS_LABEL[session.status] || session.status;
    const confirmingStop = state.activeStopConfirm === session.id;
    const confirmingPurge = state.activePurgeConfirm === session.id;
    const diskLabel = session.disk_known ? formatBytes(session.disk_bytes) : "—";

    const displayName = session.repo || session.project || "—";

    card.innerHTML = `
      <div class="session-card-top">
        <div>
          <div class="session-repo">${escapeHtml(displayName)}</div>
          <div class="session-meta">
            <span>${escapeHtml(session.repo ? (session.branch || "—") : "new project")}</span>
            <span class="dot">${escapeHtml(session.agent || "claude")}</span>
            <span class="dot">${relativeTime(session.created_at)}</span>
            <span class="dot">${escapeHtml(diskLabel)}</span>
          </div>
        </div>
        <span class="status-pill status-${escapeHtml(session.status)}">${escapeHtml(status)}</span>
      </div>
      ${session.message ? `<div class="session-message">${escapeHtml(session.message)}</div>` : ""}
      <div class="session-actions">
        <button type="button" class="btn" data-action="logs">Logs</button>
        <button type="button" class="btn ${confirmingStop ? "btn-danger-confirm" : ""}" data-action="stop">
          ${confirmingStop ? "Tap again to stop" : "Stop"}
        </button>
        <button type="button" class="btn btn-danger ${confirmingPurge ? "btn-danger-confirm" : ""}" data-action="purge">
          ${confirmingPurge ? "Deletes checkout too" : "Stop & delete"}
        </button>
      </div>
    `;

    card.querySelector('[data-action="logs"]').addEventListener("click", () => openLogsSheet(session.id, displayName));
    card.querySelector('[data-action="stop"]').addEventListener("click", () => handleStopClick(session.id));
    card.querySelector('[data-action="purge"]').addEventListener("click", () => handlePurgeClick(session.id));

    return card;
  }

  async function refreshSessions({ silent } = {}) {
    if (state.pollInFlight) return;
    state.pollInFlight = true;
    try {
      const sessions = await api.listSessions();
      state.sessions = sessions;
      renderSessions();
      setOnline(true);
    } catch (err) {
      if (!silent) showBanner(err.message || "Failed to load sessions");
    } finally {
      state.pollInFlight = false;
    }
  }

  /** Poll cadence: 2s while any session is starting, otherwise 5s. Paused when hidden. */
  function schedulePoll() {
    if (state.pollTimer) clearTimeout(state.pollTimer);
    if (document.hidden) return;

    const hasStarting = state.sessions.some((s) => s.status === "starting");
    const delay = hasStarting ? 2000 : 5000;

    state.pollTimer = setTimeout(async () => {
      await refreshSessions({ silent: true });
      schedulePoll();
    }, delay);
  }

  document.addEventListener("visibilitychange", () => {
    if (document.hidden) {
      if (state.pollTimer) clearTimeout(state.pollTimer);
      return;
    }
    refreshSessions({ silent: true }).then(schedulePoll);
  });

  el.sessionsRefresh.addEventListener("click", () => {
    refreshSessions();
    if (state.storageOpen) loadStorage();
  });

  /* ------------------------------------------------------------------ *
   * 7. Session actions: stop, logs
   * ------------------------------------------------------------------ */

  function handleStopClick(id) {
    if (state.activeStopConfirm === id) {
      state.activeStopConfirm = null;
      doStopSession(id);
      return;
    }
    state.activeStopConfirm = id;
    state.activePurgeConfirm = null;
    renderSessions();
    // Auto-revert the confirm state after a few seconds so it doesn't linger.
    setTimeout(() => {
      if (state.activeStopConfirm === id) {
        state.activeStopConfirm = null;
        renderSessions();
      }
    }, 4000);
  }

  async function doStopSession(id) {
    // Optimistically mark as stopped-in-progress by removing from the list feel;
    // safest is to just re-fetch after the call completes.
    try {
      await api.deleteSession(id);
      await refreshSessions();
    } catch (err) {
      showBanner(err.message || "Failed to stop session");
    }
  }

  function handlePurgeClick(id) {
    if (state.activePurgeConfirm === id) {
      state.activePurgeConfirm = null;
      doPurgeSession(id);
      return;
    }
    state.activePurgeConfirm = id;
    state.activeStopConfirm = null;
    renderSessions();
    setTimeout(() => {
      if (state.activePurgeConfirm === id) {
        state.activePurgeConfirm = null;
        renderSessions();
      }
    }, 4000);
  }

  async function doPurgeSession(id) {
    try {
      await api.deleteSession(id, { purge: true });
      await refreshSessions();
    } catch (err) {
      showBanner(err.message || "Failed to stop and delete session");
    }
  }

  function openLogsSheet(id, repoName) {
    state.logsSessionId = id;
    document.getElementById("logs-sheet-title").textContent = `Logs · ${repoName}`;
    el.logsContent.textContent = "Loading…";
    el.logsSheetBackdrop.hidden = false;
    loadLogs();
  }

  function closeLogsSheet() {
    el.logsSheetBackdrop.hidden = true;
    state.logsSessionId = null;
  }

  async function loadLogs() {
    if (!state.logsSessionId) return;
    try {
      const text = await api.getLogs(state.logsSessionId);
      el.logsContent.textContent = text || "(no output yet)";
      el.logsContent.scrollTop = el.logsContent.scrollHeight;
    } catch (err) {
      el.logsContent.textContent = `Could not load logs: ${err.message}`;
    }
  }

  el.logsSheetClose.addEventListener("click", closeLogsSheet);
  el.logsSheetBackdrop.addEventListener("click", (e) => {
    if (e.target === el.logsSheetBackdrop) closeLogsSheet();
  });
  el.logsRefresh.addEventListener("click", loadLogs);

  /* ------------------------------------------------------------------ *
   * 7b. Storage
   * ------------------------------------------------------------------ */

  function renderOrphans(orphans) {
    el.storageOrphansList.innerHTML = "";

    if (orphans.length === 0) {
      el.storageOrphansEmpty.hidden = false;
      el.storageOrphansList.hidden = true;
      return;
    }
    el.storageOrphansEmpty.hidden = true;
    el.storageOrphansList.hidden = false;

    for (const orphan of orphans) {
      el.storageOrphansList.appendChild(buildOrphanRow(orphan));
    }
  }

  function buildOrphanRow(orphan) {
    const row = document.createElement("div");
    row.className = "storage-orphan-row";
    const confirming = state.activeVolumeConfirm === orphan.volume;

    row.innerHTML = `
      <div class="storage-orphan-main">
        <div class="storage-orphan-name">${escapeHtml(orphan.volume)}</div>
        <div class="storage-orphan-meta">${escapeHtml(orphan.kind)} &middot; ${escapeHtml(orphan.sizeKnown === false ? "—" : formatBytes(orphan.bytes))}</div>
      </div>
      <button type="button" class="btn btn-danger ${confirming ? "btn-danger-confirm" : ""}" data-action="delete-volume">
        ${confirming ? "Confirm" : "Delete"}
      </button>
    `;

    row.querySelector('[data-action="delete-volume"]').addEventListener("click", () => handleVolumeDeleteClick(orphan.volume));
    return row;
  }

  function handleVolumeDeleteClick(name) {
    if (state.activeVolumeConfirm === name) {
      state.activeVolumeConfirm = null;
      doDeleteVolume(name);
      return;
    }
    state.activeVolumeConfirm = name;
    renderOrphans(state.storageOrphans || []);
    setTimeout(() => {
      if (state.activeVolumeConfirm === name) {
        state.activeVolumeConfirm = null;
        renderOrphans(state.storageOrphans || []);
      }
    }, 4000);
  }

  async function doDeleteVolume(name) {
    try {
      await api.deleteVolume(name);
      await loadStorage();
    } catch (err) {
      showBanner(err.message || "Failed to delete volume");
    }
  }

  async function loadStorage() {
    el.storageLoading.hidden = false;
    el.storageContent.hidden = true;
    try {
      const data = await api.getStorage();
      state.storageOrphans = (data.orphans || []).map((o) => ({ ...o, sizeKnown: data.sizes_known !== false }));
      // sizes_known is false until the daemon's first size lookup lands; show
      // a dash rather than a confident zero.
      const known = data.sizes_known !== false;
      el.storageTotal.textContent = known ? formatBytes(data.total_bytes || 0) : "—";
      el.storageReclaimable.textContent = known ? formatBytes(data.reclaimable_bytes || 0) : "—";
      renderOrphans(state.storageOrphans);
      el.storageLoading.hidden = true;
      el.storageContent.hidden = false;
      state.storageLoaded = true;
    } catch (err) {
      el.storageLoading.hidden = true;
      showBanner(err.message || "Failed to load storage");
    }
  }

  function setStorageOpen(isOpen) {
    state.storageOpen = isOpen;
    el.storageToggle.setAttribute("aria-expanded", String(isOpen));
    el.storageBody.hidden = !isOpen;
    if (isOpen && !state.storageLoaded) loadStorage();
  }

  el.storageToggle.addEventListener("click", () => setStorageOpen(!state.storageOpen));

  /* ------------------------------------------------------------------ *
   * 8. Repos: search + render
   * ------------------------------------------------------------------ */

  let searchDebounceTimer = null;

  function buildRepoRow(repo) {
    const row = document.createElement("button");
    row.type = "button";
    row.className = "repo-row";

    row.innerHTML = `
      <div class="repo-row-main">
        <div class="repo-row-name">
          ${repo.private ? lockGlyph() : ""}
          <span>${escapeHtml(repo.name)}</span>
          <span class="repo-row-owner">${escapeHtml(repo.owner)}</span>
        </div>
        ${repo.description ? `<div class="repo-row-desc">${escapeHtml(repo.description)}</div>` : ""}
        <div class="repo-row-meta">
          ${repo.language ? `<span><span class="lang-dot"></span>${escapeHtml(repo.language)}</span>` : ""}
          <span>${escapeHtml(relativeTime(repo.pushed_at))}</span>
        </div>
      </div>
      <svg class="repo-row-chevron" viewBox="0 0 24 24" width="16" height="16" aria-hidden="true">
        <path d="M9 6l6 6-6 6" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round"/>
      </svg>
    `;

    row.addEventListener("click", () => openStartSheet(repo));
    return row;
  }

  function lockGlyph() {
    return `<svg class="lock-glyph" viewBox="0 0 24 24" width="13" height="13" aria-hidden="true">
      <path d="M12 2a4 4 0 0 0-4 4v3H7a1 1 0 0 0-1 1v9a1 1 0 0 0 1 1h10a1 1 0 0 0 1-1v-9a1 1 0 0 0-1-1h-1V6a4 4 0 0 0-4-4Zm0 2a2 2 0 0 1 2 2v3h-4V6a2 2 0 0 1 2-2Z" fill="currentColor"/>
    </svg>`;
  }

  function renderRepos() {
    el.reposList.innerHTML = "";
    el.reposLoading.hidden = true;

    if (state.repos.length === 0) {
      el.reposEmpty.hidden = false;
      el.reposList.hidden = true;
      return;
    }
    el.reposEmpty.hidden = true;
    el.reposList.hidden = false;

    for (const repo of state.repos) {
      el.reposList.appendChild(buildRepoRow(repo));
    }
  }

  async function loadRepos(query) {
    try {
      const repos = await api.listRepos(query);
      state.repos = repos;
      renderRepos();
    } catch (err) {
      el.reposLoading.hidden = true;
      showBanner(err.message || "Failed to load repos");
    }
  }

  el.repoSearch.addEventListener("input", () => {
    const query = el.repoSearch.value.trim();
    state.reposQuery = query;
    if (searchDebounceTimer) clearTimeout(searchDebounceTimer);
    searchDebounceTimer = setTimeout(() => loadRepos(query), 250);
  });

  /* ------------------------------------------------------------------ *
   * 9. Start sheet
   * ------------------------------------------------------------------ */

  function setStartSheetMode(mode) {
    state.startSheetMode = mode;
    el.startSheetRepoFields.hidden = mode !== "repo";
    el.startSheetProjectFields.hidden = mode !== "project";
    el.startSheetTitle.textContent = mode === "project" ? "New project" : "Start session";
  }

  function openStartSheet(repo) {
    state.startSheetRepo = repo;
    state.startSheetAgent = "claude";
    setAgentSelection("claude");
    setStartSheetMode("repo");

    el.startSheetRepoName.textContent = repo.full_name;
    const bits = [];
    if (repo.private) bits.push("private");
    if (repo.language) bits.push(repo.language);
    bits.push(`default: ${repo.default_branch}`);
    el.startSheetRepoMeta.textContent = bits.join(" · ");

    el.startBranch.value = repo.default_branch || "main";
    setStartSubmitting(false);

    el.startSheetBackdrop.hidden = false;
  }

  function openNewProjectSheet() {
    state.startSheetRepo = null;
    state.startSheetAgent = "claude";
    setAgentSelection("claude");
    setStartSheetMode("project");

    el.startProjectName.value = "";
    el.startProjectGithub.checked = false;
    el.startProjectPrivate.checked = true;
    setProjectGithubToggle(false);
    setStartSubmitting(false);

    el.startSheetBackdrop.hidden = false;
    el.startProjectName.focus();
  }

  function setProjectGithubToggle(isOn) {
    el.startProjectPrivateRow.hidden = !isOn;
    el.startProjectHint.textContent = isOn
      ? "Creates a new GitHub repository and clones it — your GitHub token needs permission to create repos."
      : "Creates an empty folder in the workspace — no GitHub repo needed to start. You can push it to GitHub yourself later.";
  }

  el.startProjectGithub.addEventListener("change", () => {
    setProjectGithubToggle(el.startProjectGithub.checked);
  });

  function closeStartSheet() {
    el.startSheetBackdrop.hidden = true;
    state.startSheetRepo = null;
  }

  function setAgentSelection(agent) {
    state.startSheetAgent = agent;
    el.agentClaude.classList.toggle("is-selected", agent === "claude");
    el.agentClaude.setAttribute("aria-checked", String(agent === "claude"));
  }

  function setStartSubmitting(isSubmitting) {
    state.starting = isSubmitting;
    el.startSubmit.disabled = isSubmitting;
    el.startSubmitLabel.textContent = isSubmitting ? "Starting…" : "Start";
    el.startSubmitSpinner.hidden = !isSubmitting;
  }

  async function handleStartSubmit() {
    if (state.starting) return;

    if (state.startSheetMode === "project") {
      const name = el.startProjectName.value.trim();
      if (!name) {
        showBanner("Project name is required");
        return;
      }
      setStartSubmitting(true);
      try {
        const { session, alreadyExisted } = await api.createProject(name, state.startSheetAgent, {
          createGithubRepo: el.startProjectGithub.checked,
          isPrivate: el.startProjectPrivate.checked,
        });
        closeStartSheet();
        await refreshSessions();
        schedulePoll();
        showReadyCallout(session ? (session.repo || session.project) : name, alreadyExisted);
      } catch (err) {
        showBanner(err.message || "Failed to create project");
      } finally {
        setStartSubmitting(false);
      }
      return;
    }

    const repo = state.startSheetRepo;
    if (!repo) return;

    const branch = el.startBranch.value.trim() || repo.default_branch;
    setStartSubmitting(true);

    try {
      const { session, alreadyExisted } = await api.createSession(
        repo.full_name,
        branch,
        state.startSheetAgent
      );
      closeStartSheet();
      await refreshSessions();
      schedulePoll();
      showReadyCallout(session ? session.repo : repo.full_name, alreadyExisted);
    } catch (err) {
      showBanner(err.message || "Failed to start session");
    } finally {
      setStartSubmitting(false);
    }
  }

  function showReadyCallout(repoName, alreadyExisted) {
    el.readyCalloutRepo.textContent = repoName;
    document.querySelector(".ready-callout-title").textContent = alreadyExisted
      ? "Session already running"
      : "Session is ready";
    el.readyCallout.hidden = false;
  }

  el.newProjectOpen.addEventListener("click", openNewProjectSheet);
  el.startSheetClose.addEventListener("click", closeStartSheet);
  el.startSheetBackdrop.addEventListener("click", (e) => {
    if (e.target === el.startSheetBackdrop) closeStartSheet();
  });
  el.startSubmit.addEventListener("click", handleStartSubmit);
  el.readyCalloutClose.addEventListener("click", () => { el.readyCallout.hidden = true; });

  el.agentClaude.addEventListener("click", () => setAgentSelection("claude"));
  // agent-codex stays disabled — no listener needed.

  /* ------------------------------------------------------------------ *
   * 10. Init
   * ------------------------------------------------------------------ */

  async function init() {
    el.reposLoading.hidden = false;
    el.reposList.hidden = true;

    await Promise.all([
      refreshSessions(),
      loadRepos(""),
    ]);

    schedulePoll();
  }

  init();
})();
