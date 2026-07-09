#!/bin/sh
# Emits a per-session context for jobs launched by Claude Code so an agent's
# jobs stay isolated from yours under `--here`. Exits non-zero (resolver
# skipped) outside Claude Code, so your own shells fall through. List first.
[ -n "$CLAUDECODE" ] || exit 1
[ -n "$CLAUDE_CODE_SESSION_ID" ] || exit 1
echo "claude-$CLAUDE_CODE_SESSION_ID"
