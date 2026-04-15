#!/bin/sh
# Returns the current tmux session name as the context.
# Exits non-zero when not running inside tmux (resolver is skipped).
[ -n "$TMUX" ] || exit 1
tmux display-message -p '#S'
