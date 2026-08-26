#!/usr/bin/env bash
# Smoke test: does a containerised agent register for Remote Control *without
# asking anyone to sign in*?
#
# Run it twice. The second run is the one that matters: it uses a fresh profile
# volume, exactly like a container started from your phone for a repo you have
# never opened before. If that run reaches the session without a login prompt,
# the flow works.
set -euo pipefail

cd "$(dirname "$0")/.."
ENV_FILE="${RV_ENV_FILE:-.env}"
[[ -f "$ENV_FILE" ]] && { set -a; . "$ENV_FILE"; set +a; }

IMAGE="${RV_AGENT_IMAGE:-remotevibe/agent:latest}"
MODE="${RV_AUTH_MODE:-seeded}"
STATE_DIR="${RV_STATE_DIR:-/var/lib/remotevibe}"
HOME_DIR="$STATE_DIR/agent-home"
CONFIG_DIR=/home/vibe/.claude-state
SESSION_NAME="${1:-remotevibe-check}"
LAUNCH="claude --remote-control '$SESSION_NAME' --permission-mode ${RV_PERMISSION_MODE:-bypassPermissions}"

args=(run --rm -it --name "rv-verify-$$")
case "$MODE" in
  seeded)
    [[ -f "$HOME_DIR/.credentials.json" ]] || { echo "No profile in $HOME_DIR; run make auth" >&2; exit 1; }
    # Mount the canonical profile read-only and copy it in, the way a real
    # session container does — a bind mount would prove nothing about seeding.
    args+=(-v "$HOME_DIR:/rv/auth:ro")
    LAUNCH="cp -a /rv/auth/. $CONFIG_DIR/ && $LAUNCH"
    ;;
  shared-home)
    [[ -f "$HOME_DIR/.credentials.json" ]] || { echo "No profile in $HOME_DIR; run make auth" >&2; exit 1; }
    args+=(-v "$HOME_DIR:$CONFIG_DIR")
    ;;
  *)
    echo "RV_AUTH_MODE=$MODE is not a mode this tool supports (expected seeded or shared-home)." >&2
    echo "RV_AUTH_MODE=token was removed: a setup-token does not satisfy Remote Control." >&2
    exit 2
    ;;
esac

cat <<TXT
Mode: $MODE
Starting an interactive Claude session named "$SESSION_NAME" in a throwaway
container. Open the Claude app on your phone and look for it.

  - it appears, no login asked -> the flow works; phone-started sessions will too
  - it asks you to sign in      -> the profile is not reaching the container:
                                   check $HOME_DIR holds .credentials.json
                                   *and* .claude.json, then re-run make auth
  - nothing on the phone        -> it registered locally but did not publish;
                                   check the session name in the app

Ctrl-C twice to end the test.

TXT

exec docker "${args[@]}" --entrypoint bash "$IMAGE" -lc "$LAUNCH"
