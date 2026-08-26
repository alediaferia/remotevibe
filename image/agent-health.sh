#!/usr/bin/env bash
# Healthy means the agent is attachable *right now*.
#
# Two ways to fail. The pane may have fallen back to its shell, which means the
# agent exited — matching on "not a shell" rather than on a process name keeps
# this independent of how the agent CLI was installed. Or the agent may be
# sitting on a sign-in prompt, which the entrypoint detects at startup and
# records; nobody is at the keyboard of a phone-started container, so that is a
# dead session however alive the process looks.
set -euo pipefail

STATE_DIR="$HOME/.remotevibe"
[[ -f "$STATE_DIR/needs-login" ]] && exit 1

tmux has-session -t agent 2>/dev/null || exit 1

cmd="$(tmux list-panes -t agent -F '#{pane_current_command}' 2>/dev/null | head -n1)"
case "$cmd" in
  bash | sh | zsh | dash | fish | tmux | "") exit 1 ;;
  *) exit 0 ;;
esac
