#!/usr/bin/env bash
# Healthy means the agent is attachable *right now*.
#
# The check is "what is running in the foreground of the agent pane": if the
# agent exited, the pane falls back to its shell, which is precisely the state
# the phone must not be told is a live session. Matching on the process name
# instead would depend on how the agent CLI was installed (a native binary, a
# node wrapper, ...); matching on "not a shell" does not.
set -euo pipefail

tmux has-session -t agent 2>/dev/null || exit 1

cmd="$(tmux list-panes -t agent -F '#{pane_current_command}' 2>/dev/null | head -n1)"
case "$cmd" in
  bash | sh | zsh | dash | fish | tmux | "") exit 1 ;;
  *) exit 0 ;;
esac
