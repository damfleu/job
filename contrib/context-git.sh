#!/bin/sh
# Returns git-<name>, where <name> is the current worktree root's basename.
# Linked worktrees remain isolated because each worktree has its own root.
# Exits non-zero when not inside any git repo (resolver is skipped).
root=$(git rev-parse --show-toplevel 2>/dev/null) || exit 1
printf 'git-%s\n' "${root##*/}"
