#!/usr/bin/env bash
# Healthy once tmux is serving the agent pane and the entrypoint has confirmed
# the agent process came up.
set -euo pipefail
tmux has-session -t agent 2>/dev/null || exit 1
test -f "$HOME/.remotevibe/ready" || exit 1
exit 0
