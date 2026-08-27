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
CONFIG_DIR="${CLAUDE_CONFIG_DIR:-$HOME/.claude-state}"
mkdir -p "$STATE_DIR" "$CONFIG_DIR"
rm -f "$STATE_DIR/ready" "$STATE_DIR/needs-login" "$STATE_DIR/registered"

# --- agent profile ----------------------------------------------------------
# A session started from a phone cannot answer a sign-in prompt, so the profile
# has to arrive already authenticated. /rv/auth is the canonical one created by
# `make auth`; it is copied in, not mounted, so each session then owns its
# credentials, conversation and project state independently.
if [[ ! -f "$CONFIG_DIR/.credentials.json" && -d /rv/auth ]]; then
  if [[ -f /rv/auth/.credentials.json ]]; then
    log "seeding agent profile from /rv/auth"
    cp -a /rv/auth/. "$CONFIG_DIR"/ 2>/dev/null || log "profile seeding was incomplete; the agent may ask you to sign in"
  else
    log "/rv/auth holds no credentials — run make auth on the host"
  fi
fi

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
# The agent starts in an interactive TTY, so onboarding, the workspace-trust
# dialog and the bypass-permissions warning would each block the session before
# it ever reaches your phone.
# Best effort: harmless if the key names drift in a future release.
CONFIG="$CONFIG_DIR/.claude.json"
if command -v jq >/dev/null; then
  [[ -f "$CONFIG" ]] || echo '{}' > "$CONFIG"
  tmp="$(mktemp)"
  jq --arg dir "$REPO_DIR" '
      .hasCompletedOnboarding = true
    | .bypassPermissionsModeAccepted = true
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

# classify inspects the agent pane and records what it finds. Exactly one of
# three outcomes ends up in $STATE_DIR, and the healthcheck reads them: a
# session that never registers is not a session your phone can reach, however
# alive the process looks.
classify() {
  local pane
  pane="$(tmux capture-pane -p -t agent -S -120 2>/dev/null || true)"

  if grep -qE 'remote.control is active|claude\.ai/code/session' <<<"$pane"; then
    touch "$STATE_DIR/registered"
    rm -f "$STATE_DIR/needs-login" "$STATE_DIR/unconfirmed"
    return 0
  fi

  # The bypass-permissions warning is a one-key dialog that only appears when
  # the profile has not accepted it before. Answering it here keeps a session
  # alive that the config pre-seed above should have prevented anyway.
  if grep -q 'Bypass Permissions mode' <<<"$pane" && grep -q 'Yes, I accept' <<<"$pane"; then
    log "accepting the bypass-permissions warning on your behalf"
    # -l sends the digit literally, and as its own keystroke: a bare
    # `send-keys 2 Enter` can arrive as Enter alone, which would select the
    # highlighted "No, exit" and take the session down.
    tmux send-keys -t agent -l '2'
    sleep 1
    tmux send-keys -t agent Enter
    return 1
  fi

  if grep -qEi 'select login method|log in with your|paste (the )?code|claude\.ai/oauth' <<<"$pane"; then
    touch "$STATE_DIR/needs-login"
    return 1
  fi
  return 1
}

for _ in $(seq 1 60); do
  if classify; then
    touch "$STATE_DIR/ready"
    log "agent registered for remote control — open the vendor app on your phone to attach"
    break
  fi
  if [[ -f "$STATE_DIR/needs-login" ]]; then
    log "the agent is asking to sign in; a phone-started session cannot answer that."
    log "re-run make auth on the host, then start this session again."
    break
  fi
  sleep 2
done

if [[ ! -f "$STATE_DIR/registered" && ! -f "$STATE_DIR/needs-login" ]]; then
  # Running but never confirmed: almost always a prompt waiting for a keypress
  # nobody is there to give. Say so instead of reporting a healthy session.
  touch "$STATE_DIR/unconfirmed"
  log "agent did not confirm remote control within 120s — inspect it with:"
  log "  docker exec -it $(hostname) tmux attach -t agent"
fi

# --- supervise --------------------------------------------------------------
term() { log "stopping"; tmux kill-server 2>/dev/null || true; exit 0; }
trap term TERM INT

# A signal that lands on the sleep (rather than on this shell) must not be
# allowed to take the container down with it — under `set -e` an interrupted
# sleep would otherwise end the supervisor and kill a perfectly good session.
ticks=0
while tmux has-session -t agent 2>/dev/null; do
  # Keep looking until it registers: a session rescued by hand (or by a slow
  # backend) should stop being reported as broken.
  if [[ ! -f "$STATE_DIR/registered" ]] && (( ticks % 6 == 0 )); then
    classify >/dev/null || true
  fi
  ticks=$((ticks + 1))
  sleep 5 || true
done
log "tmux session ended; container exiting"
