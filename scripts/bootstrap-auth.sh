#!/usr/bin/env bash
# One-time (and occasionally repeated) agent authentication.
#
# remotevibe never touches the Anthropic API: the agent in the container signs
# in to your Claude subscription exactly the way it would on your laptop. That
# sign-in is interactive, so it cannot be automated — this script gives you the
# shortest path through it and tells you where the result has to end up.
set -euo pipefail

cd "$(dirname "$0")/.."
ENV_FILE="${RV_ENV_FILE:-.env}"
[[ -f "$ENV_FILE" ]] && { set -a; . "$ENV_FILE"; set +a; }

IMAGE="${RV_AGENT_IMAGE:-remotevibe/agent:latest}"
MODE="${RV_AUTH_MODE:-token}"
STATE_DIR="${RV_STATE_DIR:-/var/lib/remotevibe}"
HOME_DIR="$STATE_DIR/agent-home"

docker image inspect "$IMAGE" >/dev/null 2>&1 || {
  echo "Agent image $IMAGE is not built yet. Run: make image" >&2
  exit 1
}

case "$MODE" in
token)
  cat <<'TXT'
Mode: token

You will be shown a URL to open, then asked to paste a code back. The result is
a long-lived token that every session container receives as
CLAUDE_CODE_OAUTH_TOKEN — the containers themselves stay stateless.

TXT
  docker run --rm -it "$IMAGE" bash -lc 'claude setup-token'
  cat <<TXT

Copy the token printed above into $ENV_FILE:

    RV_AUTH_MODE=token
    CLAUDE_CODE_OAUTH_TOKEN=<the token>

then restart the daemon (sudo systemctl restart remotevibed).
Re-run this script when the token expires; existing sessions keep their own
credentials until they are stopped.
TXT
  ;;

shared-home)
  mkdir -p "$HOME_DIR"
  cat <<TXT
Mode: shared-home

Signing in inside a container whose ~/.claude is $HOME_DIR. Every session
container bind-mounts that directory, so they all share one credential and
Claude refreshes it in place — no expiry babysitting, but only one sign-in
identity and a shared config file.

Sign in with /login, wait for it to confirm, then exit the session (Ctrl-C
twice, or type /exit).

TXT
  docker run --rm -it -v "$HOME_DIR:/home/vibe/.claude" "$IMAGE" bash -lc 'claude'
  if [[ -f "$HOME_DIR/.credentials.json" ]]; then
    echo "Credentials stored in $HOME_DIR/.credentials.json"
  else
    echo "No credentials file was written — sign-in did not complete." >&2
    exit 1
  fi
  ;;

*)
  echo "Unknown RV_AUTH_MODE: $MODE (expected token or shared-home)" >&2
  exit 2
  ;;
esac
