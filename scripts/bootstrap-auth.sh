#!/usr/bin/env bash
# One-time (and occasionally repeated) agent authentication.
#
# remotevibe never touches the Anthropic API: the agent in the container signs
# in to your Claude subscription exactly the way it would on your laptop. That
# sign-in is interactive, so it cannot be automated — this script gives you the
# shortest path through it and leaves the result where session containers can
# pick it up.
set -euo pipefail

cd "$(dirname "$0")/.."
ENV_FILE="${RV_ENV_FILE:-.env}"
[[ -f "$ENV_FILE" ]] && { set -a; . "$ENV_FILE"; set +a; }

IMAGE="${RV_AGENT_IMAGE:-remotevibe/agent:latest}"
MODE="${RV_AUTH_MODE:-seeded}"
STATE_DIR="${RV_STATE_DIR:-/var/lib/remotevibe}"
HOME_DIR="$STATE_DIR/agent-home"
CONFIG_DIR=/home/vibe/.claude-state

docker image inspect "$IMAGE" >/dev/null 2>&1 || {
  echo "Agent image $IMAGE is not built yet. Run: make image" >&2
  exit 1
}

case "$MODE" in
seeded | shared-home)
  mkdir -p "$HOME_DIR" 2>/dev/null || {
    echo "Cannot create $HOME_DIR." >&2
    echo "On a VPS: sudo install -d -o \"$(id -un)\" -m 0700 $HOME_DIR" >&2
    echo "On a laptop: set RV_STATE_DIR=\$PWD/state in $ENV_FILE" >&2
    exit 1
  }
  cat <<TXT
Mode: $MODE

Signing in inside a container whose agent profile is $HOME_DIR.

That directory holds both halves of a login: the credentials *and* the account
record. Persisting only the credentials — the layout you get by default — gives
a container that asks you to sign in again, which is fatal for a session
started from a phone.

Sign in with /login, wait for it to confirm, then exit (Ctrl-C twice, or /exit).

It starts in the same permission mode your sessions use, so any warning you
accept here is recorded in the profile they are seeded from. Accepting it in a
throwaway container — a `make verify` run, say — does not carry over.

TXT
  docker run --rm -it --entrypoint bash \
    -v "$HOME_DIR:$CONFIG_DIR" "$IMAGE" \
    -lc "claude --permission-mode ${RV_PERMISSION_MODE:-bypassPermissions}"

  if [[ -f "$HOME_DIR/.credentials.json" && -f "$HOME_DIR/.claude.json" ]]; then
    echo "Agent profile stored in $HOME_DIR"
    echo "Check it with: ./scripts/verify-remote-control.sh"
  else
    echo "Sign-in did not complete: $HOME_DIR is missing .credentials.json or .claude.json" >&2
    exit 1
  fi
  ;;

*)
  echo "Unknown RV_AUTH_MODE: $MODE (expected seeded or shared-home)" >&2
  exit 2
  ;;
esac
