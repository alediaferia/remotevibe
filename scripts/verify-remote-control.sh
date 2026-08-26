#!/usr/bin/env bash
# Smoke test: does a containerised agent actually show up in the phone app?
#
# Run this once after `make auth`, before trusting the daemon. It answers the
# single question the whole design rests on — whether Remote Control registers
# correctly with the credentials you chose — without involving the daemon, the
# API, or GitHub.
set -euo pipefail

cd "$(dirname "$0")/.."
ENV_FILE="${RV_ENV_FILE:-.env}"
[[ -f "$ENV_FILE" ]] && { set -a; . "$ENV_FILE"; set +a; }

IMAGE="${RV_AGENT_IMAGE:-remotevibe/agent:latest}"
MODE="${RV_AUTH_MODE:-token}"
STATE_DIR="${RV_STATE_DIR:-/var/lib/remotevibe}"
NAME="rv-verify-$$"
SESSION_NAME="${1:-remotevibe-check}"

args=(run --rm --name "$NAME" -it)
case "$MODE" in
  token)
    [[ -n "${CLAUDE_CODE_OAUTH_TOKEN:-}" ]] || { echo "CLAUDE_CODE_OAUTH_TOKEN is empty; run make auth" >&2; exit 1; }
    args+=(-e CLAUDE_CODE_OAUTH_TOKEN)
    ;;
  shared-home)
    [[ -f "$STATE_DIR/agent-home/.credentials.json" ]] || { echo "No credentials in $STATE_DIR/agent-home; run make auth" >&2; exit 1; }
    args+=(-v "$STATE_DIR/agent-home:/home/vibe/.claude")
    ;;
esac

cat <<TXT
Starting an interactive Claude session named "$SESSION_NAME" in a throwaway
container. Now open the Claude app on your phone and look for it.

  - it appears        -> your auth mode works; the daemon will work too
  - it does not       -> try the other RV_AUTH_MODE and run this again
  - login prompt      -> credentials did not reach the container

Ctrl-C twice to end the test.

TXT

exec docker "${args[@]}" --entrypoint bash "$IMAGE" \
  -lc "claude --remote-control '$SESSION_NAME' --permission-mode ${RV_PERMISSION_MODE:-bypassPermissions}"
