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
  mkdir -p "$HOME_DIR"
  cat <<TXT
Mode: $MODE

Signing in inside a container whose agent profile is $HOME_DIR.

That directory holds both halves of a login: the credentials *and* the account
record. Persisting only the credentials — the layout you get by default — gives
a container that asks you to sign in again, which is fatal for a session
started from a phone.

Sign in with /login, wait for it to confirm, then exit (Ctrl-C twice, or /exit).

TXT
  docker run --rm -it --entrypoint bash \
    -v "$HOME_DIR:$CONFIG_DIR" "$IMAGE" -lc 'claude'

  if [[ -f "$HOME_DIR/.credentials.json" && -f "$HOME_DIR/.claude.json" ]]; then
    echo "Agent profile stored in $HOME_DIR"
    echo "Check it with: ./scripts/verify-remote-control.sh"
  else
    echo "Sign-in did not complete: $HOME_DIR is missing .credentials.json or .claude.json" >&2
    exit 1
  fi
  ;;

token)
  cat <<'TXT'
Mode: token

You will be shown a URL to open, then asked to paste a code back. The result is
a long-lived token that every session container receives as
CLAUDE_CODE_OAUTH_TOKEN, with no profile seeded alongside it.

Note: a token on its own may not be enough for Remote Control, since the agent
also wants an account record it can only get from a sign-in. Run
./scripts/verify-remote-control.sh afterwards; if it drops you into a login
prompt, use RV_AUTH_MODE=seeded instead.

TXT
  # --entrypoint bash: the image's own entrypoint expects a session to clone.
  docker run --rm -it --entrypoint bash "$IMAGE" -lc 'claude setup-token'
  cat <<TXT

Copy the token printed above into $ENV_FILE:

    RV_AUTH_MODE=token
    CLAUDE_CODE_OAUTH_TOKEN=<the token>

then restart the daemon (sudo systemctl restart remotevibed).
TXT
  ;;

*)
  echo "Unknown RV_AUTH_MODE: $MODE (expected seeded, token or shared-home)" >&2
  exit 2
  ;;
esac
