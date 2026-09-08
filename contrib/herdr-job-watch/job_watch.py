#!/usr/bin/env python3
"""Herdr plugin that toggles a session-scoped ``job watch`` pane.

The module intentionally uses only the Python standard library.  Most of the
implementation is split into small, injectable pieces so action and supervisor
behavior can be tested without a Herdr server or a real terminal.
"""

from __future__ import annotations

import contextlib
import dataclasses
import fcntl
import hashlib
import json
import os
import re
import shutil
import signal
import subprocess
import sys
import tempfile
import time
from collections.abc import Callable, Iterator, Mapping, MutableMapping, Sequence
from decimal import Decimal, InvalidOperation
from pathlib import Path
from typing import Any

PLUGIN_TITLE = "job watch"
SUPPORTED_AGENTS = frozenset(("codex", "claude"))
AGENT_ENV_KEYS = (
    "CODEX_THREAD_ID",
    "CLAUDECODE",
    "CLAUDE_CODE",
    "CLAUDE_CODE_SESSION_ID",
)
UNKNOWN_CONFIG_KEYS_WARNING = "Ignoring unknown config key: {}"


class PluginError(Exception):
    """An expected error with a session-safe user-facing message."""

    def __init__(self, user_message: str, log_message: str | None = None):
        super().__init__(log_message or user_message)
        self.user_message = user_message
        self.log_message = log_message or user_message


class ConfigError(PluginError):
    pass


class HerdrError(PluginError):
    def __init__(self, operation: str, code: str | None = None):
        self.operation = operation
        self.code = code
        detail = f" (code: {code})" if code else ""
        super().__init__(
            f"Herdr could not {operation}. See the plugin log for details.",
            f"Herdr command failed while {operation}{detail}.",
        )

    @property
    def is_pane_not_found(self) -> bool:
        return self.code in ("pane_not_found", "not_found")


@dataclasses.dataclass(frozen=True)
class Config:
    schema_version: int = 1
    watcher_fraction: float = 0.20
    history_since: str | None = "1h"
    poll_interval_ms: int = 1000
    target_missing_polls: int = 3
    child_shutdown_grace_ms: int = 1000
    job_binary: str = "job"
    focus_on_open: bool = False


@dataclasses.dataclass(frozen=True)
class AgentSession:
    source: str
    agent: str
    kind: str
    value: str

    @property
    def identity(self) -> tuple[str, str, str]:
        return (self.agent, self.kind, self.value)


@dataclasses.dataclass(frozen=True)
class PaneInfo:
    pane_id: str
    terminal_id: str
    cwd: str | None = None
    foreground_cwd: str | None = None
    agent_session: AgentSession | None = None


@dataclasses.dataclass(frozen=True)
class RegistryPaths:
    registry: Path
    lock: Path


@dataclasses.dataclass
class RegistryEntry:
    target_terminal_id: str
    target_pane_id: str
    watcher_terminal_id: str
    watcher_pane_id: str
    created_unix_ms: int

    @classmethod
    def from_data(cls, value: Any) -> RegistryEntry:
        if not isinstance(value, dict):
            raise TypeError("entry is not an object")
        strings = (
            "target_terminal_id",
            "target_pane_id",
            "watcher_terminal_id",
            "watcher_pane_id",
        )
        for field in strings:
            if not isinstance(value.get(field), str) or not value[field]:
                raise ValueError(f"invalid {field}")
        created = value.get("created_unix_ms")
        if isinstance(created, bool) or not isinstance(created, int) or created < 0:
            raise ValueError("invalid created_unix_ms")
        return cls(
            target_terminal_id=value["target_terminal_id"],
            target_pane_id=value["target_pane_id"],
            watcher_terminal_id=value["watcher_terminal_id"],
            watcher_pane_id=value["watcher_pane_id"],
            created_unix_ms=created,
        )

    def to_data(self) -> dict[str, Any]:
        return dataclasses.asdict(self)


def _is_number(value: Any) -> bool:
    return isinstance(value, (int, float)) and not isinstance(value, bool)


# Go's time.ParseDuration accepts one or more decimal number/unit pairs.  job
# additionally accepts a single floating-point day or week value.
_DURATION_NUMBER = r"(?:\d+(?:\.\d*)?|\.\d+)"
_GO_DURATION = re.compile(rf"^[+]?(?:{_DURATION_NUMBER}(?:ns|us|µs|μs|ms|s|m|h))+$")
_GO_DURATION_PART = re.compile(rf"({_DURATION_NUMBER})(ns|us|µs|μs|ms|s|m|h)")
_DAY_WEEK_DURATION = re.compile(rf"^[+]?({_DURATION_NUMBER}(?:[eE][+-]?\d+)?)([dDwW])$")
_MAX_DURATION_NS = Decimal(2**63 - 1)
_DURATION_UNIT_NS = {
    "ns": Decimal(1),
    "us": Decimal(1000),
    "µs": Decimal(1000),
    "μs": Decimal(1000),
    "ms": Decimal(1_000_000),
    "s": Decimal(1_000_000_000),
    "m": Decimal(60 * 1_000_000_000),
    "h": Decimal(60 * 60 * 1_000_000_000),
}


def validate_history_since(value: str | None, field: str = "history_since") -> None:
    if value is None or value == "":
        return
    if not isinstance(value, str):
        raise ConfigError(
            f"Invalid plugin config field '{field}': expected a string or null."
        )
    if value == "0":
        return
    if _GO_DURATION.fullmatch(value):
        total = Decimal(0)
        for number, unit in _GO_DURATION_PART.findall(value.lstrip("+")):
            total += Decimal(number) * _DURATION_UNIT_NS[unit]
        if total <= _MAX_DURATION_NS:
            return
    day_week_match = _DAY_WEEK_DURATION.fullmatch(value)
    if day_week_match:
        try:
            number = Decimal(day_week_match.group(1))
        except InvalidOperation:
            number = Decimal(-1)
        days = Decimal(7) if day_week_match.group(2).lower() == "w" else Decimal(1)
        nanoseconds = number * days * Decimal(24 * 60 * 60 * 1_000_000_000)
        if number.is_finite() and Decimal(0) <= nanoseconds <= _MAX_DURATION_NS:
            return
    raise ConfigError(f"Invalid plugin config field '{field}': unsupported duration.")


def validate_config(data: Any, path: Path) -> Config:
    if not isinstance(data, dict):
        raise ConfigError(f"Invalid plugin config at {path}: expected a JSON object.")

    known = {field.name for field in dataclasses.fields(Config)}
    for key in sorted(set(data) - known):
        print(UNKNOWN_CONFIG_KEYS_WARNING.format(key), file=sys.stderr)

    schema_version = data.get("schema_version", 1)
    if isinstance(schema_version, bool) or schema_version != 1:
        raise ConfigError("Invalid plugin config field 'schema_version': expected 1.")

    watcher_fraction = data.get("watcher_fraction", 0.20)
    if not _is_number(watcher_fraction) or not 0.10 <= float(watcher_fraction) <= 0.45:
        raise ConfigError(
            "Invalid plugin config field 'watcher_fraction': expected 0.10 through 0.45."
        )

    history_since = data.get("history_since", "1h")
    validate_history_since(history_since)

    def bounded_int(name: str, default: int, minimum: int, maximum: int) -> int:
        value = data.get(name, default)
        if (
            isinstance(value, bool)
            or not isinstance(value, int)
            or not minimum <= value <= maximum
        ):
            raise ConfigError(
                f"Invalid plugin config field '{name}': expected an integer from {minimum} through {maximum}."
            )
        return value

    poll_interval_ms = bounded_int("poll_interval_ms", 1000, 250, 10000)
    target_missing_polls = bounded_int("target_missing_polls", 3, 1, 30)
    grace_ms = bounded_int("child_shutdown_grace_ms", 1000, 100, 5000)

    job_binary = data.get("job_binary", "job")
    if not isinstance(job_binary, str) or not job_binary:
        raise ConfigError(
            "Invalid plugin config field 'job_binary': expected a non-empty executable name."
        )
    expanded_binary = os.path.expanduser(job_binary)
    if "/" in expanded_binary and not os.path.isabs(expanded_binary):
        raise ConfigError(
            "Invalid plugin config field 'job_binary': paths containing '/' must be absolute."
        )

    focus_on_open = data.get("focus_on_open", False)
    if not isinstance(focus_on_open, bool):
        raise ConfigError(
            "Invalid plugin config field 'focus_on_open': expected true or false."
        )

    return Config(
        schema_version=1,
        watcher_fraction=float(watcher_fraction),
        history_since=history_since,
        poll_interval_ms=poll_interval_ms,
        target_missing_polls=target_missing_polls,
        child_shutdown_grace_ms=grace_ms,
        job_binary=expanded_binary,
        focus_on_open=focus_on_open,
    )


def load_config(config_dir: Path) -> Config:
    path = config_dir / "config.json"
    try:
        raw = path.read_text(encoding="utf-8")
    except FileNotFoundError:
        return Config()
    except UnicodeDecodeError:
        raise ConfigError(f"Invalid UTF-8 in plugin config at {path}.")
    except OSError as exc:
        raise ConfigError(
            f"Could not read plugin config at {path}.",
            f"Could not read plugin config: {type(exc).__name__}.",
        )
    try:
        data = json.loads(raw)
    except (json.JSONDecodeError, UnicodeDecodeError) as exc:
        raise ConfigError(
            f"Invalid JSON in plugin config at {path}.",
            "Invalid plugin config JSON at line {}, column {}.".format(
                getattr(exc, "lineno", "?"), getattr(exc, "colno", "?")
            ),
        )
    return validate_config(data, path)


def parse_invocation_context(raw: str | None) -> dict[str, Any]:
    if not raw:
        raise PluginError("No focused pane is available for this plugin action.")
    try:
        data = json.loads(raw)
    except json.JSONDecodeError:
        raise PluginError(
            "The Herdr plugin invocation context is invalid.",
            "HERDR_PLUGIN_CONTEXT_JSON was not valid JSON.",
        )
    if not isinstance(data, dict):
        raise PluginError("The Herdr plugin invocation context is invalid.")
    return data


def focused_pane_id(context: Mapping[str, Any]) -> str:
    value = context.get("focused_pane_id")
    if not isinstance(value, str) or not value:
        raise PluginError("No focused pane is available for this plugin action.")
    return value


def parse_pane(data: Any) -> PaneInfo:
    if not isinstance(data, dict):
        raise HerdrError("parse a pane response")
    pane_id = data.get("pane_id")
    terminal_id = data.get("terminal_id")
    if (
        not isinstance(pane_id, str)
        or not pane_id
        or not isinstance(terminal_id, str)
        or not terminal_id
    ):
        raise HerdrError("parse a pane response")

    cwd = (
        data.get("cwd")
        if isinstance(data.get("cwd"), str) and data.get("cwd")
        else None
    )
    foreground_cwd = (
        data.get("foreground_cwd")
        if isinstance(data.get("foreground_cwd"), str) and data.get("foreground_cwd")
        else None
    )
    session_data = data.get("agent_session")
    session = None
    if session_data is not None:
        if not isinstance(session_data, dict):
            raise HerdrError("parse a pane response")
        values = [
            session_data.get(name) for name in ("source", "agent", "kind", "value")
        ]
        if not all(isinstance(value, str) for value in values):
            raise HerdrError("parse a pane response")
        session = AgentSession(*values)
    return PaneInfo(
        pane_id=pane_id,
        terminal_id=terminal_id,
        cwd=cwd,
        foreground_cwd=foreground_cwd,
        agent_session=session,
    )


def require_supported_session(pane: PaneInfo) -> AgentSession:
    session = pane.agent_session
    if (
        session is None
        or session.agent not in SUPPORTED_AGENTS
        or session.kind != "id"
        or not session.value
    ):
        raise PluginError(
            "No Codex or Claude session is available for the focused pane. "
            "Install the Herdr integration or wait for session startup to finish."
        )
    return session


def build_child_environment(
    base: Mapping[str, str], session: AgentSession
) -> dict[str, str]:
    if (
        session.agent not in SUPPORTED_AGENTS
        or session.kind != "id"
        or not session.value
    ):
        raise PluginError(
            "Cannot build a watcher environment without a supported agent session."
        )
    child = dict(base)
    for key in AGENT_ENV_KEYS:
        child.pop(key, None)
    if session.agent == "codex":
        child["CODEX_THREAD_ID"] = session.value
    else:
        child.update(
            {
                "CLAUDECODE": "1",
                "CLAUDE_CODE": "1",
                "CLAUDE_CODE_SESSION_ID": session.value,
            }
        )
    return child


def _extract_error_code(text: str) -> str | None:
    for candidate in (text, text[text.find("{") :] if "{" in text else ""):
        if not candidate:
            continue
        try:
            data = json.loads(candidate)
        except json.JSONDecodeError:
            continue
        if isinstance(data, dict):
            error = data.get("error")
            if isinstance(error, dict) and isinstance(error.get("code"), str):
                return error["code"]
            if isinstance(data.get("code"), str):
                return data["code"]
    return None


class HerdrClient:
    def __init__(
        self,
        binary: str,
        plugin_id: str,
        runner: Callable[..., subprocess.CompletedProcess] = subprocess.run,
        timeout_seconds: float = 5.0,
    ):
        self.binary = binary
        self.plugin_id = plugin_id
        self.runner = runner
        self.timeout_seconds = timeout_seconds

    def _run(self, args: Sequence[str], operation: str) -> Mapping[str, Any]:
        argv = [self.binary] + list(args)
        try:
            result = self.runner(
                argv,
                stdin=subprocess.DEVNULL,
                stdout=subprocess.PIPE,
                stderr=subprocess.PIPE,
                text=True,
                timeout=self.timeout_seconds,
                check=False,
            )
        except (OSError, subprocess.SubprocessError) as exc:
            raise HerdrError(operation, type(exc).__name__)
        if result.returncode != 0:
            raise HerdrError(
                operation, _extract_error_code(result.stderr or result.stdout or "")
            )
        try:
            envelope = json.loads(result.stdout)
        except (TypeError, json.JSONDecodeError):
            raise HerdrError(f"parse the response while attempting to {operation}")
        if not isinstance(envelope, dict) or not isinstance(
            envelope.get("result"), dict
        ):
            raise HerdrError(f"parse the response while attempting to {operation}")
        return envelope["result"]

    def get_pane(self, pane_id: str) -> PaneInfo:
        result = self._run(("pane", "get", pane_id), "resolve a pane")
        if result.get("type") != "pane_info":
            raise HerdrError("parse a pane response")
        return parse_pane(result.get("pane"))

    def open_plugin_pane(
        self,
        target_pane_id: str,
        extra_env: Mapping[str, str],
        focus: bool,
    ) -> PaneInfo:
        args = [
            "plugin",
            "pane",
            "open",
            "--plugin",
            self.plugin_id,
            "--entrypoint",
            "watch",
            "--placement",
            "split",
            "--target-pane",
            target_pane_id,
            "--direction",
            "down",
        ]
        for key, value in extra_env.items():
            args.extend(("--env", f"{key}={value}"))
        args.append("--focus" if focus else "--no-focus")
        result = self._run(args, "open the watcher pane")
        if result.get("type") != "plugin_pane_opened":
            raise HerdrError("parse the response while opening the watcher pane")
        plugin_pane = result.get("plugin_pane")
        if not isinstance(plugin_pane, dict):
            raise HerdrError("parse the response while opening the watcher pane")
        return parse_pane(plugin_pane.get("pane"))

    def resize_pane(
        self, pane_id: str, direction: str, amount: float
    ) -> Mapping[str, Any]:
        return self._run(
            (
                "pane",
                "resize",
                "--pane",
                pane_id,
                "--direction",
                direction,
                "--amount",
                f"{amount:.6g}",
            ),
            "resize the source pane",
        )

    def rename_pane(self, pane_id: str, title: str) -> Mapping[str, Any]:
        return self._run(("pane", "rename", pane_id, title), "rename the watcher pane")

    def focus_plugin_pane(self, pane_id: str) -> Mapping[str, Any]:
        return self._run(("plugin", "pane", "focus", pane_id), "focus the watcher pane")

    def close_plugin_pane(self, pane_id: str) -> Mapping[str, Any]:
        return self._run(("plugin", "pane", "close", pane_id), "close the watcher pane")

    def notify(self, body: str) -> Mapping[str, Any]:
        return self._run(
            ("notification", "show", PLUGIN_TITLE, "--body", body, "--sound", "none"),
            "show a notification",
        )


def resolve_job_binary(config: Config) -> str:
    configured = config.job_binary
    if os.path.isabs(configured):
        path = configured
        if not os.path.isfile(path) or not os.access(path, os.X_OK):
            raise PluginError(
                "The plugin config key 'job_binary' does not name an executable file.",
                "Configured job_binary is not executable.",
            )
    else:
        path = shutil.which(configured) or ""
        if not path:
            raise PluginError(
                "Could not find the executable from plugin config key 'job_binary'.",
                "Could not resolve job_binary through PATH.",
            )
    return os.path.abspath(path)


def preflight_job(
    config: Config, runner: Callable[..., subprocess.CompletedProcess] = subprocess.run
) -> str:
    binary = resolve_job_binary(config)
    try:
        result = runner(
            [binary, "watch", "--help"],
            stdin=subprocess.DEVNULL,
            stdout=subprocess.PIPE,
            stderr=subprocess.PIPE,
            text=True,
            timeout=5,
            check=False,
        )
    except (OSError, subprocess.SubprocessError) as exc:
        raise PluginError(
            "Could not check the configured 'job_binary'.",
            f"job capability probe failed: {type(exc).__name__}.",
        )
    help_text = (result.stdout or "") + (result.stderr or "")
    if result.returncode != 0 or "--since" not in help_text:
        raise PluginError(
            "The configured job binary does not support 'job watch --since'; job v1.15.0 or newer is required.",
            "job capability probe did not advertise --since.",
        )
    return binary


def registry_paths(state_dir: Path, socket_path: str) -> RegistryPaths:
    digest = hashlib.sha256(socket_path.encode("utf-8")).hexdigest()[:16]
    return RegistryPaths(
        registry=state_dir / f"registry-{digest}.json",
        lock=state_dir / f"registry-{digest}.lock",
    )


class Registry:
    def __init__(
        self,
        state_dir: Path,
        socket_path: str,
        clock_ms: Callable[[], int] = lambda: int(time.time() * 1000),
    ):
        self.paths = registry_paths(state_dir, socket_path)
        self.clock_ms = clock_ms

    @contextlib.contextmanager
    def locked(self) -> Iterator[None]:
        try:
            self.paths.lock.parent.mkdir(parents=True, exist_ok=True)
            descriptor = os.open(str(self.paths.lock), os.O_RDWR | os.O_CREAT, 0o600)
            os.fchmod(descriptor, 0o600)
            fcntl.flock(descriptor, fcntl.LOCK_EX)
        except OSError as exc:
            if "descriptor" in locals():
                os.close(descriptor)
            raise PluginError(
                "Could not lock the watcher registry.",
                f"Could not acquire registry lock: {type(exc).__name__}.",
            )
        try:
            yield
        finally:
            try:
                fcntl.flock(descriptor, fcntl.LOCK_UN)
            except OSError:
                pass
            os.close(descriptor)

    def read_locked(self) -> tuple[dict[str, RegistryEntry], Path | None]:
        try:
            raw = self.paths.registry.read_text(encoding="utf-8")
        except FileNotFoundError:
            return {}, None
        except OSError:
            return self._quarantine_locked()
        if not raw.strip():
            return {}, None
        try:
            data = json.loads(raw)
            if not isinstance(data, dict) or data.get("schema_version") != 1:
                raise ValueError("invalid schema")
            raw_watchers = data.get("watchers")
            if not isinstance(raw_watchers, dict):
                raise TypeError("invalid watchers")
            watchers: dict[str, RegistryEntry] = {}
            for key, value in raw_watchers.items():
                if not isinstance(key, str) or not key:
                    raise ValueError("invalid watcher key")
                entry = RegistryEntry.from_data(value)
                if entry.target_terminal_id != key:
                    raise ValueError("watcher key mismatch")
                watchers[key] = entry
            return watchers, None
        except (json.JSONDecodeError, UnicodeDecodeError, ValueError, TypeError):
            return self._quarantine_locked()

    def _quarantine_locked(self) -> tuple[dict[str, RegistryEntry], Path | None]:
        if not self.paths.registry.exists():
            return {}, None
        quarantined = self.paths.registry.with_name(
            f"{self.paths.registry.name}.{self.clock_ms()}.corrupt"
        )
        suffix = 0
        while quarantined.exists():
            suffix += 1
            quarantined = self.paths.registry.with_name(
                f"{self.paths.registry.name}.{self.clock_ms()}.{suffix}.corrupt"
            )
        try:
            os.replace(str(self.paths.registry), str(quarantined))
        except OSError as exc:
            raise PluginError(
                "The watcher registry is corrupt and could not be quarantined.",
                f"Could not quarantine corrupt registry: {type(exc).__name__}.",
            )
        return {}, quarantined

    def write_locked(self, watchers: Mapping[str, RegistryEntry]) -> None:
        data = {
            "schema_version": 1,
            "watchers": {
                key: entry.to_data() for key, entry in sorted(watchers.items())
            },
        }
        encoded = (
            json.dumps(data, sort_keys=True, separators=(",", ":")) + "\n"
        ).encode("utf-8")
        descriptor = -1
        temp_name = ""
        try:
            self.paths.registry.parent.mkdir(parents=True, exist_ok=True)
            descriptor, temp_name = tempfile.mkstemp(
                prefix=f".{self.paths.registry.name}-",
                dir=str(self.paths.registry.parent),
            )
            os.fchmod(descriptor, 0o600)
            with os.fdopen(descriptor, "wb", closefd=True) as stream:
                descriptor = -1
                stream.write(encoded)
                stream.flush()
                os.fsync(stream.fileno())
            os.replace(temp_name, str(self.paths.registry))
        except OSError as exc:
            raise PluginError(
                "Could not update the watcher registry.",
                f"Could not write watcher registry: {type(exc).__name__}.",
            )
        finally:
            if descriptor >= 0:
                os.close(descriptor)
            if temp_name:
                try:
                    os.unlink(temp_name)
                except FileNotFoundError:
                    pass


def safe_notify(client: HerdrClient, body: str) -> None:
    try:
        client.notify(body)
    except PluginError as exc:
        print(f"Notification failed: {exc.log_message}", file=sys.stderr)


def _required_env(env: Mapping[str, str], name: str) -> str:
    value = env.get(name)
    if not value:
        raise PluginError(
            "The Herdr plugin environment is incomplete.",
            f"Required environment variable {name} is missing.",
        )
    return value


def _watcher_cwd(
    pane: PaneInfo, context: Mapping[str, Any], env: Mapping[str, str]
) -> str:
    candidates = (
        pane.foreground_cwd,
        pane.cwd,
        context.get("focused_pane_cwd"),
        context.get("workspace_cwd"),
        env.get("HERDR_PLUGIN_ROOT"),
        str(Path(__file__).resolve().parent),
    )
    for candidate in candidates:
        if isinstance(candidate, str) and candidate:
            return candidate
    return str(Path(__file__).resolve().parent)


def _watcher_is_live(client: HerdrClient, entry: RegistryEntry) -> bool:
    try:
        pane = client.get_pane(entry.watcher_pane_id)
    except HerdrError:
        return False
    return pane.terminal_id == entry.watcher_terminal_id


def _close_registered_watcher(
    client: HerdrClient,
    registry: Registry,
    watchers: MutableMapping[str, RegistryEntry],
    key: str,
) -> None:
    entry = watchers[key]
    try:
        client.close_plugin_pane(entry.watcher_pane_id)
    except HerdrError:
        raise PluginError(
            "The existing watcher could not be closed; no duplicate was opened.",
            "Failed to close an existing watcher; registry entry was retained.",
        )
    del watchers[key]
    registry.write_locked(watchers)


def _toggle_action(
    env: Mapping[str, str],
    client: HerdrClient,
    job_preflight: Callable[[Config], str],
    clock_ms: Callable[[], int],
) -> None:
    config_dir = Path(_required_env(env, "HERDR_PLUGIN_CONFIG_DIR"))
    config = load_config(config_dir)
    context = parse_invocation_context(env.get("HERDR_PLUGIN_CONTEXT_JSON"))
    target_pane_id = focused_pane_id(context)
    target = client.get_pane(target_pane_id)
    state_dir = Path(_required_env(env, "HERDR_PLUGIN_STATE_DIR"))
    socket_path = _required_env(env, "HERDR_SOCKET_PATH")
    registry = Registry(state_dir, socket_path, clock_ms=clock_ms)

    with registry.locked():
        watchers, quarantined = registry.read_locked()
        if quarantined is not None:
            safe_notify(client, "The watcher registry was corrupt and has been reset.")

        for key, entry in list(watchers.items()):
            if entry.watcher_terminal_id == target.terminal_id:
                _close_registered_watcher(client, registry, watchers, key)
                return

        existing = watchers.get(target.terminal_id)
        if existing is not None:
            if _watcher_is_live(client, existing):
                _close_registered_watcher(
                    client, registry, watchers, target.terminal_id
                )
                return
            del watchers[target.terminal_id]
            registry.write_locked(watchers)

        require_supported_session(target)
        job_binary = job_preflight(config)
        cwd = _watcher_cwd(target, context, env)
        watcher = client.open_plugin_pane(
            target_pane_id,
            {
                "JOB_WATCH_TARGET_PANE_ID": target_pane_id,
                "JOB_WATCH_JOB_BIN": job_binary,
                "JOB_WATCH_TARGET_CWD": cwd,
            },
            config.focus_on_open,
        )

        resize_failed = False
        try:
            client.resize_pane(target_pane_id, "down", 0.5 - config.watcher_fraction)
        except HerdrError:
            resize_failed = True
            print(
                "Watcher opened, but the source pane could not be resized.",
                file=sys.stderr,
            )

        watchers[target.terminal_id] = RegistryEntry(
            target_terminal_id=target.terminal_id,
            target_pane_id=target_pane_id,
            watcher_terminal_id=watcher.terminal_id,
            watcher_pane_id=watcher.pane_id,
            created_unix_ms=clock_ms(),
        )
        registry.write_locked(watchers)
        if resize_failed:
            safe_notify(
                client,
                "The watcher opened, but Herdr could not resize it; it remains at 50/50.",
            )


def toggle_action(
    environ: Mapping[str, str] | None = None,
    client: HerdrClient | None = None,
    job_preflight: Callable[[Config], str] = preflight_job,
    clock_ms: Callable[[], int] = lambda: int(time.time() * 1000),
) -> int:
    env = dict(os.environ if environ is None else environ)
    herdr = client or HerdrClient(
        env.get("HERDR_BIN_PATH", "herdr"), env.get("HERDR_PLUGIN_ID", "job.watch")
    )
    try:
        _toggle_action(env, herdr, job_preflight, clock_ms)
        return 0
    except PluginError as exc:
        print(exc.log_message, file=sys.stderr)
        safe_notify(herdr, exc.user_message)
        return 1


class WatchSupervisor:
    def __init__(
        self,
        config: Config,
        target_pane_id: str,
        watcher_pane_id: str,
        job_binary: str,
        client: HerdrClient,
        base_environment: Mapping[str, str],
        process_factory: Callable[..., subprocess.Popen] = subprocess.Popen,
        sleep: Callable[[float], None] = time.sleep,
    ):
        self.config = config
        self.target_pane_id = target_pane_id
        self.watcher_pane_id = watcher_pane_id
        self.job_binary = job_binary
        self.client = client
        self.base_environment = dict(base_environment)
        self.process_factory = process_factory
        self.sleep = sleep
        self.child: subprocess.Popen | None = None
        self.session: AgentSession | None = None
        self.target_terminal_id: str | None = None
        self._stop_signal: int | None = None

    @property
    def poll_seconds(self) -> float:
        return self.config.poll_interval_ms / 1000.0

    @property
    def grace_seconds(self) -> float:
        return self.config.child_shutdown_grace_ms / 1000.0

    def request_stop(self, signum: int) -> None:
        if self._stop_signal is not None:
            return
        self._stop_signal = signum
        if self.child is not None and self.child.poll() is None:
            try:
                self.child.send_signal(signum)
            except OSError:
                pass

    def _initial_target(self) -> PaneInfo:
        last_error: PluginError | None = None
        # The first attempt is immediate; three poll intervals are then allowed
        # for the pane command and native session report to become visible.
        for attempt in range(4):
            try:
                pane = self.client.get_pane(self.target_pane_id)
                require_supported_session(pane)
                return pane
            except PluginError as exc:
                last_error = exc
                if attempt < 3:
                    self.sleep(self.poll_seconds)
        assert last_error is not None
        raise last_error

    def _child_argv(self) -> list[str]:
        argv = [self.job_binary, "watch"]
        if self.config.history_since:
            argv.extend(("--since", self.config.history_since))
        return argv

    def _spawn(self, pane: PaneInfo, session: AgentSession) -> None:
        child_env = build_child_environment(self.base_environment, session)
        cwd = (
            pane.foreground_cwd
            or pane.cwd
            or self.base_environment.get("JOB_WATCH_TARGET_CWD")
            or os.getcwd()
        )
        try:
            self.child = self.process_factory(
                self._child_argv(), cwd=cwd, env=child_env
            )
        except OSError as exc:
            raise PluginError(
                "Could not start job watch. See the plugin log for details.",
                f"Could not spawn job watch: {type(exc).__name__}.",
            )
        self.session = session

    def _rename(self, agent: str) -> None:
        try:
            self.client.rename_pane(self.watcher_pane_id, f"jobs · {agent}")
        except HerdrError as exc:
            print(exc.log_message, file=sys.stderr)

    def _wait_or_escalate(self, initial_signal: int | None) -> None:
        child = self.child
        if child is None or child.poll() is not None:
            return
        if initial_signal is not None:
            try:
                child.send_signal(initial_signal)
            except OSError:
                return
        try:
            child.wait(timeout=self.grace_seconds)
            return
        except subprocess.TimeoutExpired:
            pass
        try:
            child.terminate()
            child.wait(timeout=self.grace_seconds)
            return
        except subprocess.TimeoutExpired:
            pass
        except OSError:
            return
        try:
            child.kill()
            child.wait(timeout=self.grace_seconds)
        except (OSError, subprocess.TimeoutExpired):
            pass

    def _rebind(self, pane: PaneInfo, session: AgentSession) -> None:
        self._wait_or_escalate(signal.SIGINT)
        self._rename(session.agent)
        self._spawn(pane, session)

    def run(self) -> int:
        pane = self._initial_target()
        session = require_supported_session(pane)
        self.target_terminal_id = pane.terminal_id
        self._rename(session.agent)
        self._spawn(pane, session)
        missing = 0

        while True:
            self.sleep(self.poll_seconds)
            if self._stop_signal is not None:
                self._wait_or_escalate(None)
                return 0

            assert self.child is not None
            status = self.child.poll()
            if status is not None:
                if status != 0:
                    safe_notify(
                        self.client, f"job watch failed with exit status {status}."
                    )
                return status

            try:
                current = self.client.get_pane(self.target_pane_id)
                if current.terminal_id != self.target_terminal_id:
                    raise HerdrError(
                        "resolve the original source pane", "terminal_changed"
                    )
                missing = 0
            except HerdrError:
                missing += 1
                if missing >= self.config.target_missing_polls:
                    self._wait_or_escalate(signal.SIGINT)
                    return 0
                continue

            try:
                current_session = require_supported_session(current)
            except PluginError:
                # The agent may have returned to its shell. Keep useful history
                # visible until another supported native session is reported.
                continue
            assert self.session is not None
            if current_session.identity != self.session.identity:
                self._rebind(current, current_session)


def watch_action(
    environ: Mapping[str, str] | None = None,
    client: HerdrClient | None = None,
    process_factory: Callable[..., subprocess.Popen] = subprocess.Popen,
    sleep: Callable[[float], None] = time.sleep,
    install_signal_handlers: bool = True,
) -> int:
    env = dict(os.environ if environ is None else environ)
    herdr = client or HerdrClient(
        env.get("HERDR_BIN_PATH", "herdr"), env.get("HERDR_PLUGIN_ID", "job.watch")
    )
    try:
        config = load_config(Path(_required_env(env, "HERDR_PLUGIN_CONFIG_DIR")))
        target_pane_id = _required_env(env, "JOB_WATCH_TARGET_PANE_ID")
        watcher_pane_id = _required_env(env, "HERDR_PANE_ID")
        job_binary = _required_env(env, "JOB_WATCH_JOB_BIN")
        if not os.path.isabs(job_binary):
            raise PluginError(
                "The watcher received an invalid job binary.",
                "JOB_WATCH_JOB_BIN was not absolute.",
            )
        supervisor = WatchSupervisor(
            config,
            target_pane_id,
            watcher_pane_id,
            job_binary,
            herdr,
            env,
            process_factory=process_factory,
            sleep=sleep,
        )
        previous_handlers: dict[int, Any] = {}
        if install_signal_handlers:
            for signum in (signal.SIGINT, signal.SIGTERM, signal.SIGHUP):
                previous_handlers[signum] = signal.getsignal(signum)
                signal.signal(
                    signum,
                    lambda received, _frame, s=supervisor: s.request_stop(received),
                )
        try:
            return supervisor.run()
        finally:
            for signum, handler in previous_handlers.items():
                signal.signal(signum, handler)
    except PluginError as exc:
        print(exc.log_message, file=sys.stderr)
        safe_notify(herdr, exc.user_message)
        return 1


def main(argv: Sequence[str] | None = None) -> int:
    args = list(sys.argv[1:] if argv is None else argv)
    if args == ["toggle"]:
        return toggle_action()
    if args == ["watch"]:
        return watch_action()
    print("usage: job_watch.py {toggle|watch}", file=sys.stderr)
    return 2


if __name__ == "__main__":
    raise SystemExit(main())
