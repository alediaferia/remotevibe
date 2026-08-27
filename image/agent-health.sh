#!/usr/bin/env bash
# Healthy means the agent is attachable *right now*.
#
# Three ways to fail. The pane may have fallen back to its shell, which means
# the agent exited — matching on "not a shell" rather than on a process name
# keeps this independent of how the agent CLI was installed. Or the entrypoint
# may have recorded that the agent is waiting on a sign-in, or that it never
# confirmed Remote Control at all — a dialog awaiting a keypress looks exactly
# like a healthy process, and is not a session your phone can reach.
set -euo pipefail

STATE_DIR="$HOME/.remotevibe"
# Either marker means the pane is waiting on a keypress nobody will give it.
[[ -f "$STATE_DIR/needs-login" || -f "$STATE_DIR/unconfirmed" ]] && exit 1

tmux has-session -t agent 2>/dev/null || exit 1

cmd="$(tmux list-panes -t agent -F '#{pane_current_command}' 2>/dev/null | head -n1)"
case "$cmd" in
  bash | sh | zsh | dash | fish | tmux | "") exit 1 ;;
  *) exit 0 ;;
esac
