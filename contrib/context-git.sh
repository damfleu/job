#!/bin/sh
# Returns the root of the current git worktree.
# In a linked worktree this is the worktree directory, not the main repo root.
# Falls back to the main repo root when not in a worktree.
# Exits non-zero when not inside any git repo (resolver is skipped).
git rev-parse --show-toplevel 2>/dev/null
