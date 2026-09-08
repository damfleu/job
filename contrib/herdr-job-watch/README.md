# Agent Job Watch for Herdr

This bundled Herdr plugin toggles a small `job watch` split for the exact
native Codex or Claude session in the focused pane. The watcher follows a new
native session started in the same pane, closes after the source pane
disappears, and delegates all context naming and exact filtering to `job`.

## How it works

`job_watch.py` does not render the job dashboard itself. It is an adapter that
manages a Herdr pane and runs the Go `job watch` TUI inside it. The same script
has two entrypoints:

```text
shortcut
   |
   v
job_watch.py toggle       short-lived controller
   |
   v  opens a Herdr split
job_watch.py watch        long-lived pane supervisor
   |
   v
job watch --since 1h      actual dashboard
```

### Toggle action

Herdr invokes `job_watch.py toggle` when the configured shortcut is pressed.
The action loads `config.json`, reads the focused pane from
`HERDR_PLUGIN_CONTEXT_JSON`, and asks Herdr for the pane's stable terminal ID,
working directory, and native agent session. A pane is watchable only when
Herdr reports an exact Codex or Claude session ID.

Before changing the layout, the action resolves `job_binary` to an absolute
executable path and runs `job watch --help`. The capability probe must succeed
and advertise `--since`.

The action keeps a small registry under `HERDR_PLUGIN_STATE_DIR`, namespaced by
a hash of `HERDR_SOCKET_PATH`. This keeps independent Herdr servers from
sharing watcher state. Registry access uses an exclusive `flock`, so concurrent
shortcut presses cannot open duplicate watchers. Entries contain both pane IDs
and stable terminal IDs, allowing stale or reused panes to be detected.

While holding the registry lock, the action applies toggle semantics:

- when invoked from a registered watcher, it closes that watcher;
- when the focused source already has a live watcher, it closes the watcher;
- when the source has a stale entry, it removes the entry and opens a new one;
- otherwise, it opens the plugin's `watch` pane below the source.

The new pane receives the source pane ID, resolved `job` path, and a fallback
working directory through private `JOB_WATCH_*` environment variables. Herdr
initially creates an even split; the action then adjusts it to the configured
`watcher_fraction`. A resize failure leaves a usable 50/50 split and produces a
notification instead of closing the watcher.

Registry files are owner-only and written using a temporary file, `fsync`, and
atomic replacement. Invalid registry data is moved aside with a `.corrupt`
suffix and reset.

### Watch supervisor

The opened pane runs `job_watch.py watch`. This long-lived supervisor resolves
the source pane again, records its stable terminal ID, renames the watcher to
`jobs · codex` or `jobs · claude`, and starts:

```sh
job watch --since <history_since>
```

The child inherits the pane's terminal streams. Consequently, `job watch`
draws directly in the Herdr pane and receives input such as `q`; Python does
not proxy or parse the TUI.

The supervisor polls the original pane at `poll_interval_ms` and reacts to its
lifecycle:

- if the same native session remains active, the existing child continues;
- if a new Codex or Claude session starts in the same terminal, the supervisor
  stops the old child and launches a new one for the new session;
- if the agent temporarily returns to its shell, the previous watcher remains
  visible while the supervisor waits for another supported session;
- if the source disappears or its pane ID is reused by another terminal for
  `target_missing_polls` consecutive checks, the supervisor stops and exits;
- if `job watch` exits zero, including after `q`, the watcher exits naturally;
- if `job watch` exits nonzero, Herdr displays a concise notification.

On a session change or shutdown, child termination progresses from `SIGINT` to
`SIGTERM` and finally `SIGKILL`, waiting `child_shutdown_grace_ms` between
steps. `SIGINT`, `SIGTERM`, and `SIGHUP` received by the supervisor are also
forwarded to the child.

The supervisor deliberately does not edit the registry as it exits. A later
toggle validates the recorded watcher pane, recognizes the stale entry, and
removes it while holding the registry lock.

### Exact session scoping

The plugin does not implement a second job filter. Before starting `job watch`,
it removes inherited Codex and Claude identity variables and installs only the
identity reported for the source pane:

- Codex receives `CODEX_THREAD_ID`;
- Claude receives `CLAUDECODE=1`, `CLAUDE_CODE=1`, and
  `CLAUDE_CODE_SESSION_ID`.

It then runs plain `job watch`, without `--context`. The normal `job` resolver
chain reads that environment and derives the exact agent-session context. This
keeps context naming and matching in one place and means custom resolver
prefixes continue to work.

Expected failures use separate log-facing and user-facing messages. Detailed
errors go to the plugin log; notifications remain short and avoid including
raw command output or session data.

## Requirements

- Herdr 0.8.2 or newer on macOS or Linux
- Python 3.9 or newer
- `job` v1.15.0 or newer, or a development build whose `job watch --help`
  advertises `--since`
- the Herdr integration for each agent you want to watch:

  ```sh
  herdr integration install codex
  herdr integration install claude
  ```

- the matching `job` context resolver listed before workspace resolvers in
  the normal `job` configuration. The bundled resolver scripts are
  `../context-codex.sh` and `../context-claude.sh`.

For example, copy or link those scripts into a stable location and put them
first in `$XDG_CONFIG_HOME/job/config.toml`:

```toml
[context]
resolvers = [
  "$HOME/.local/bin/context-codex",
  "$HOME/.local/bin/context-claude",
  "$HOME/.local/bin/context-git",
]
```

## Install

For development or a personal checkout, link the plugin directory:

```sh
herdr plugin link /path/to/job/contrib/herdr-job-watch
```

It can also be installed from a public GitHub repository subdirectory:

```sh
herdr plugin install <owner>/<repo>/contrib/herdr-job-watch
```

Add a shortcut to the Herdr configuration. `prefix+alt+j` avoids Herdr's
default `prefix+j` focus-down binding:

```toml
[[keys.command]]
key = "prefix+alt+j"
type = "plugin_action"
command = "job.watch.toggle"
description = "toggle focused agent job watch"
```

Then validate and reload it:

```sh
herdr config check
herdr server reload-config
```

Invoke the shortcut from a Codex or Claude pane to open the watcher. Invoke it
again from either the source or watcher to close it. When the watcher is
focused, `q` exits `job watch` and closes the pane naturally.

## Configuration

Configuration is optional. To customize it, create `config.json` in the
directory printed by:

```sh
herdr plugin config-dir job.watch
```

The defaults are:

```json
{
  "schema_version": 1,
  "watcher_fraction": 0.20,
  "history_since": "1h",
  "poll_interval_ms": 1000,
  "target_missing_polls": 3,
  "child_shutdown_grace_ms": 1000,
  "job_binary": "job",
  "focus_on_open": false
}
```

`watcher_fraction` accepts `0.10` through `0.45`. `history_since` accepts a
`job watch --since` duration, `null`, or an empty string. The poll interval is
250–10000 ms, the missing-poll threshold is 1–30, and child shutdown grace is
100–5000 ms. `job_binary` may be an executable name found on `PATH` or an
absolute path. Set `focus_on_open` to `true` only if you want the new watcher
to take focus.

The watcher inherits the Herdr server's normal `job` configuration and XDG
state paths. Pane-local `JOB_CONFIG_DIR` or `JOB_STATE_DIR` overrides cannot be
read through Herdr's pane API; launch Herdr with the same shared overrides if
you use non-default locations.

## Test

```sh
python3 -m unittest discover -s contrib/herdr-job-watch/tests -v
```
