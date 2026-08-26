#!/usr/bin/env bash
# Session container entrypoint.
#
#   1. wire up git credentials from the injected GitHub token
#   2. clone (or fast-forward) the repository into the workspace volume
#   3. start the agent inside a tmux session called "agent"
#   4. stay alive as long as tmux does, so the container is the session
#
# Everything here is agent-agnostic: the command to run arrives as
# RV_AGENT_CMD, rendered by the driver on the daemon side.
set -euo pipefail

log() { printf '[remotevibe] %s\n' "$*"; }
fail() { printf '[remotevibe] ERROR: %s\n' "$*" >&2; exit 1; }

: "${RV_REPO:?RV_REPO (owner/name) is required}"
: "${RV_AGENT_CMD:?RV_AGENT_CMD is required}"
BRANCH="${RV_BRANCH:-}"
STATE_DIR="$HOME/.remotevibe"
mkdir -p "$STATE_DIR"
rm -f "$STATE_DIR/ready"

# --- git credentials --------------------------------------------------------
# One token covers cloning now and pushing later, which is the whole point of
# using a PAT instead of a read-only deploy key.
git config --global user.name  "${RV_GIT_NAME:-remotevibe}"
git config --global user.email "${RV_GIT_EMAIL:-remotevibe@localhost}"
git config --global init.defaultBranch main
git config --global --add safe.directory '*'

if [[ -n "${RV_GITHUB_TOKEN:-}" ]]; then
  umask 077
  printf 'https://x-access-token:%s@github.com\n' "$RV_GITHUB_TOKEN" > "$HOME/.git-credentials"
  git config --global credential.helper store
  export GH_TOKEN="$RV_GITHUB_TOKEN"
  umask 022
else
  log "no RV_GITHUB_TOKEN provided; private repositories will fail to clone"
fi

# --- checkout ---------------------------------------------------------------
REPO_DIR="/workspace/${RV_REPO##*/}"
if [[ -d "$REPO_DIR/.git" ]]; then
  log "reusing existing checkout at $REPO_DIR"
  git -C "$REPO_DIR" remote set-url origin "https://github.com/${RV_REPO}.git"
  git -C "$REPO_DIR" fetch --prune origin || log "fetch failed; continuing with local state"
  if [[ -n "$BRANCH" ]]; then
    git -C "$REPO_DIR" checkout "$BRANCH" 2>/dev/null || log "could not switch to $BRANCH; staying on current branch"
  fi
else
  log "cloning ${RV_REPO}${BRANCH:+ (branch $BRANCH)}"
  clone_args=(--recurse-submodules)
  [[ -n "$BRANCH" ]] && clone_args+=(--branch "$BRANCH")
  git clone "${clone_args[@]}" "https://github.com/${RV_REPO}.git" "$REPO_DIR" \
    || fail "clone failed — check that RV_GITHUB_TOKEN can read ${RV_REPO}"
fi

# --- skip first-run prompts -------------------------------------------------
# The agent starts in an interactive TTY, so onboarding and the workspace-trust
# dialog would otherwise block the session before it ever reaches your phone.
# Best effort: harmless if the key names drift in a future release.
CONFIG="$HOME/.claude.json"
if command -v jq >/dev/null; then
  [[ -f "$CONFIG" ]] || echo '{}' > "$CONFIG"
  tmp="$(mktemp)"
  jq --arg dir "$REPO_DIR" '
      .hasCompletedOnboarding = true
    | .projects = ((.projects // {}) * {($dir): (((.projects // {})[$dir]) // {} | .hasTrustDialogAccepted = true)})
  ' "$CONFIG" > "$tmp" 2>/dev/null && mv "$tmp" "$CONFIG" || rm -f "$tmp"
fi

# --- launch -----------------------------------------------------------------
# The agent runs inside a shell inside tmux rather than as tmux's own command:
# if it exits or is killed, you still have a live shell to relaunch it from
# (docker exec -it <container> tmux attach) instead of losing the container.
tmux new-session -d -s agent -c "$REPO_DIR" -x 200 -y 50
tmux set-option -t agent -g history-limit 20000
log "starting agent: ${RV_AGENT}"
tmux send-keys -t agent "$RV_AGENT_CMD" Enter

# Report readiness only once the agent process is actually up; the daemon turns
# this into the "starting" -> "running" transition on the phone.
for _ in $(seq 1 60); do
  if pgrep -u "$(id -u)" -f "${RV_AGENT:-claude}" >/dev/null 2>&1; then
    touch "$STATE_DIR/ready"
    log "agent is up — open the vendor app on your phone to attach"
    break
  fi
  sleep 2
done
[[ -f "$STATE_DIR/ready" ]] || log "agent did not report in within 120s; leaving the session open for inspection"

# --- supervise --------------------------------------------------------------
term() { log "stopping"; tmux kill-server 2>/dev/null || true; exit 0; }
trap term TERM INT

while tmux has-session -t agent 2>/dev/null; do
  sleep 5
done
log "tmux session ended; container exiting"
