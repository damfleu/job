#!/bin/sh
# Emits a per-thread context for jobs launched by Codex so an agent's jobs
# stay isolated from yours by default. Exits non-zero (resolver skipped)
# outside Codex, so your own shells fall through. List first.
[ -n "${CODEX_THREAD_ID:-}" ] || exit 1
printf 'codex-%s\n' "$CODEX_THREAD_ID"
