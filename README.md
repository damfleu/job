# job

A CLI for running and tracking background jobs with logging, history and dependencies.

## Features

- Run commands as background jobs with persistent logging
- Track job status, exit codes, and output across terminal sessions
- Express dependencies between jobs
- Save and replay job workflows as named sequences
- Notify on job completion

## Installation

See the [Releases](../../releases) page, or build from source (requires Go 1.26+):

```sh
go install .
```

## Quick Start

```sh
# Run a command in the background
job run <command>
job run -- <command>
# Run a command in a specific directory
job run --cwd <dir> -- <command>
# Run in the background and tail its output
job run -w -- <command>
# Run a command after another job has completed
job run -a <key> -- <command>
# Run a command after another job has completed successfully
job run -A <key> -- <command>
# Retry a previous job (supports other `run` flags)
job retry <key>

# List jobs (defaults to running jobs, all jobs otherwise)
job list
# Show details about a job
job show <key>
# Show output of a job
job log <key>
# Show log file of a job
job log -p <key>

# Stop a running/pending job
job stop <key>
# Block until a job completes, then print its key and exit code
job wait <key>
# Delete one or more completed jobs
job rm <key>...
```

Special keys refer to jobs by recency instead of by name, scoped to the current context by default (pass `--any` to reach across all contexts; see [Context](#context)):
- `.` — the most recently started job, of any status
- `+` / `_` / `=` — the most recent running / blocked / completed job (errors if none matches)

You can also omit the key for `job log`, `job show`, and `job stop` to default to the last running job, and for `job retry` to default to the last completed job — falling back to `.` if no job of that status exists. `job rm` accepts explicit key or alias arguments; with no arguments, it reads whitespace-separated job keys from stdin.

Run `job --help` or `job <command> --help` for full usage.

## Concepts

### Job keys

Every job gets a unique key of the form `{unix_ts}_{8hex}_{program}` (e.g. `1712912345_a3f1c8d2_make`). The special key `.` refers to the most recently started human-initiated job in the current context (or globally with `--any`).

Pass `--automated` to `run` to mark a job as automated; those jobs are never a match for `.` or the `+`/`_`/`=` symbols. Useful for scripts and scheduled tasks.

Key resolution order:
1. `.` is the most recent job, scoped to the current context (or globally with `--any`)
2. `+` / `_` / `=` is the most recent running / blocked / completed job, same scoping (hard error if none matches)
3. Exact key or alias match (alias lookup is context-scoped; a full generated key is unique and always resolves globally)
4. Substring match on command, same scoping (active jobs preferred)
5. Prefix match on key (always global: it matches the unique generated key, which context can't disambiguate)

### Dependencies

Jobs can be chained so that one starts only after another finishes.

```sh
job run -- make build
job run -A . -- make test    # run 'make test' only if 'make build' exits 0
job run -a . -- deploy       # run 'deploy' after 'make test' regardless of exit code
```

Use `-a`/`-A` multiple times to specify multiple dependencies. A job whose `--after-success` dependency fails is marked `dep_failed` without running.

### Context

Context resolvers scope jobs to a workspace. Resolution (`.`, `+`/`_`/`=`, aliases, substring matches, and `-a`/`-A` dependency chaining) is scoped to the current context by default; pass `--any` to reach across all contexts instead.

For listings, `--context <regex>` selects matching contexts across workspaces. Anchor both ends for an exact context. It can be combined with the command `--filter`.

Listing results can be passed directly to `rm` for bulk cleanup:

```sh
job list --older-than 30d -n 0 --keys | job rm --yes
```

Spaces, tabs, and newlines all separate keys on stdin. `--yes` is required because piped stdin cannot be used for interactive confirmation.

`run`, `retry`, and `seq run` accept `--cwd [dir]` to run in a specific directory; omitting the argument uses the current directory. See `contrib/` for resolver examples.

`contrib/context-claude.sh` scopes jobs launched by Claude Code to their own `claude-<session-id>` context, isolated from your default view. Together with the `--json` output on `show`/`list`, these are the building blocks a Claude Code skill for `job` would use.

### Sequences

Sequences capture one or more jobs and their combined upstream dependencies as a replayable workflow:

```sh
job run -- make build
job run -A . -- make test
job run -A . -- make deploy

job sequence save deploy .

job seq run deploy
1776289652_45d54ee8_make              make build
1776289652_3f2d7c52_make              make test
1776289652_586ae893_make              make deploy
```

Each replay spawns fresh jobs while preserving the original dependency structure.

Pass multiple keys to `save` to capture branches that share dependencies but have no common successor:

```sh
job sequence save ci keyB keyC
```

Sequences snapshot each job's command, working directory, and dependency structure.
Source jobs can be deleted without affecting the saved sequence.
Context is resolved again when each step starts, using its effective working
directory and the current resolver environment.

### Notifications

Notifiers are programs invoked on job completion. Each is called with a JSON object on stdin:

```json
{"key":"…","command":"…","rc":0,"elapsed":"1m23s"}
```

Two delivery modes are available: `always` (fires unconditionally) and `explicit` (fires only when `--notify`/`-n` is passed to `run`). Configure notifiers in `config.toml`; see [Configuration](#configuration) for details.

## Configuration

Config file: `$XDG_CONFIG_HOME/job/config.toml` (override with `$JOB_CONFIG_DIR`).

```toml
[list]
# Default max completed jobs shown by 'job list' (0 means unlimited)
limit = 20

[context]
# Scripts executed in the job's working directory; first one that exits 0 and
# prints non-empty output wins. Falls back to hostname.
resolvers = [
  "$HOME/.local/bin/context-git",
  "tmux_session",
]

# One or more notifier programs. Each is invoked with a JSON object on stdin:
# {"key":"…","command":"…","rc":0,"elapsed":"1m23s"}
[[notifier]]
program = "notify-send"
notify  = "always"    # "always" | "explicit" (default; requires -n)

[[notifier]]
program = "osascript -e 'display notification …'"
notify  = "explicit"
```

**Environment variables:**

| Variable | Default | Description |
|----------|---------|-------------|
| `JOB_CONFIG_DIR` | `$XDG_CONFIG_HOME/job` | Config file directory |
| `JOB_STATE_DIR` | `$XDG_DATA_HOME/job` | Database and log directory |

## Acknowledgements

Inspired by [wwade/jobrunner](https://github.com/wwade/jobrunner).
