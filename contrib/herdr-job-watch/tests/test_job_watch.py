import contextlib
import io
import json
import stat
import subprocess
import sys
import tempfile
import threading
import unittest
from pathlib import Path

PLUGIN_DIR = Path(__file__).resolve().parents[1]
sys.path.insert(0, str(PLUGIN_DIR))

import job_watch as jw


def session(agent="codex", value="session-1", kind="id"):
    return jw.AgentSession(source="official", agent=agent, kind=kind, value=value)


def pane(
    pane_id="w1:p1",
    terminal_id="terminal-source",
    agent="codex",
    value="session-1",
    kind="id",
    cwd="/workspace",
    foreground_cwd="/workspace/repo",
):
    native = None if agent is None else session(agent, value, kind)
    return jw.PaneInfo(
        pane_id=pane_id,
        terminal_id=terminal_id,
        cwd=cwd,
        foreground_cwd=foreground_cwd,
        agent_session=native,
    )


def envelope(result):
    return json.dumps({"id": "request", "result": result})


class QueueRunner:
    def __init__(self, results):
        self.results = list(results)
        self.calls = []

    def __call__(self, argv, **kwargs):
        self.calls.append((list(argv), kwargs))
        return self.results.pop(0)


class FakeHerdr:
    def __init__(self, target=None):
        target = target or pane()
        self.panes = {target.pane_id: target}
        self.calls = []
        self.notifications = []
        self.open_error = None
        self.close_error = None
        self.resize_error = None
        self.open_count = 0
        self.live_watchers = 0
        self.max_live_watchers = 0
        self._lock = threading.Lock()
        self.get_hook = None

    def get_pane(self, pane_id):
        self.calls.append(("get", pane_id))
        if self.get_hook is not None:
            hooked = self.get_hook(pane_id)
            if hooked is not None:
                if isinstance(hooked, Exception):
                    raise hooked
                return hooked
        try:
            return self.panes[pane_id]
        except KeyError:
            raise jw.HerdrError("resolve a pane", "pane_not_found")

    def open_plugin_pane(self, target_pane_id, extra_env, focus):
        self.calls.append(("open", target_pane_id, dict(extra_env), focus))
        if self.open_error:
            raise self.open_error
        with self._lock:
            self.open_count += 1
            watcher = pane(
                pane_id=f"w1:p{self.open_count + 1}",
                terminal_id=f"terminal-watcher-{self.open_count}",
                agent=None,
                cwd=extra_env.get("JOB_WATCH_TARGET_CWD"),
                foreground_cwd=extra_env.get("JOB_WATCH_TARGET_CWD"),
            )
            self.panes[watcher.pane_id] = watcher
            self.live_watchers += 1
            self.max_live_watchers = max(self.max_live_watchers, self.live_watchers)
        return watcher

    def resize_pane(self, pane_id, direction, amount):
        self.calls.append(("resize", pane_id, direction, amount))
        if self.resize_error:
            raise self.resize_error
        return {}

    def close_plugin_pane(self, pane_id):
        self.calls.append(("close", pane_id))
        if self.close_error:
            raise self.close_error
        with self._lock:
            if pane_id in self.panes:
                del self.panes[pane_id]
                self.live_watchers = max(0, self.live_watchers - 1)
        return {}

    def rename_pane(self, pane_id, title):
        self.calls.append(("rename", pane_id, title))
        return {}

    def notify(self, body):
        self.notifications.append(body)
        self.calls.append(("notify", body))
        return {}


class FakeProcess:
    def __init__(self, status=None, exit_on_signal=True, exit_after_polls=None):
        self.status = status
        self.exit_on_signal = exit_on_signal
        self.exit_after_polls = exit_after_polls
        self.poll_count = 0
        self.signals = []
        self.terminated = 0
        self.killed = 0
        self.wait_timeouts = []

    def poll(self):
        self.poll_count += 1
        if (
            self.status is None
            and self.exit_after_polls is not None
            and self.poll_count >= self.exit_after_polls
        ):
            self.status = 0
        return self.status

    def send_signal(self, signum):
        self.signals.append(signum)
        if self.exit_on_signal:
            self.status = 0

    def wait(self, timeout=None):
        self.wait_timeouts.append(timeout)
        if self.status is None:
            raise subprocess.TimeoutExpired("job", timeout)
        return self.status

    def terminate(self):
        self.terminated += 1
        if self.exit_on_signal:
            self.status = -15

    def kill(self):
        self.killed += 1
        self.status = -9


class FakeProcessFactory:
    def __init__(self, processes):
        self.processes = list(processes)
        self.calls = []

    def __call__(self, argv, **kwargs):
        self.calls.append((list(argv), kwargs))
        return self.processes.pop(0)


class ConfigTests(unittest.TestCase):
    def test_missing_config_uses_defaults(self):
        with tempfile.TemporaryDirectory() as directory:
            self.assertEqual(jw.load_config(Path(directory)), jw.Config())

    def test_boundaries_and_empty_history_are_valid(self):
        with tempfile.TemporaryDirectory() as directory:
            path = Path(directory) / "config.json"
            data = {
                "schema_version": 1,
                "watcher_fraction": 0.10,
                "history_since": "",
                "poll_interval_ms": 250,
                "target_missing_polls": 30,
                "child_shutdown_grace_ms": 5000,
                "job_binary": "job",
                "focus_on_open": True,
            }
            config = jw.validate_config(data, path)
            self.assertEqual(config.watcher_fraction, 0.10)
            self.assertEqual(config.history_since, "")
            data["watcher_fraction"] = 0.45
            data["poll_interval_ms"] = 10000
            data["target_missing_polls"] = 1
            data["child_shutdown_grace_ms"] = 100
            jw.validate_config(data, path)

    def test_valid_job_duration_forms(self):
        for value in (None, "", "0", "500ms", "1h30m", ".5d", "2W", "1e2d", "1.25h"):
            with self.subTest(value=value):
                jw.validate_history_since(value)

    def test_duration_overflow_is_rejected(self):
        for value in ("999999999999999999999999h", "1e200d"):
            with self.subTest(value=value), self.assertRaises(jw.ConfigError):
                jw.validate_history_since(value)

    def test_every_config_field_is_validated(self):
        invalid = (
            {"schema_version": 2},
            {"schema_version": True},
            {"watcher_fraction": 0.09},
            {"watcher_fraction": 0.46},
            {"watcher_fraction": True},
            {"history_since": "tomorrow"},
            {"history_since": 12},
            {"poll_interval_ms": 249},
            {"poll_interval_ms": 10001},
            {"target_missing_polls": 0},
            {"target_missing_polls": 31},
            {"child_shutdown_grace_ms": 99},
            {"child_shutdown_grace_ms": 5001},
            {"job_binary": ""},
            {"job_binary": "bin/job"},
            {"focus_on_open": "no"},
        )
        for data in invalid:
            with self.subTest(data=data), self.assertRaises(jw.ConfigError):
                jw.validate_config(data, Path("config.json"))

    def test_unknown_keys_warn_without_failing(self):
        stderr = io.StringIO()
        with contextlib.redirect_stderr(stderr):
            config = jw.validate_config({"future_option": 1}, Path("config.json"))
        self.assertEqual(config, jw.Config())
        self.assertIn("future_option", stderr.getvalue())

    def test_invalid_json_mentions_config_path(self):
        with tempfile.TemporaryDirectory() as directory:
            path = Path(directory) / "config.json"
            path.write_text("{", encoding="utf-8")
            with self.assertRaises(jw.ConfigError) as caught:
                jw.load_config(Path(directory))
            self.assertIn(str(path), caught.exception.user_message)


class IdentityTests(unittest.TestCase):
    def test_parse_valid_invocation_context(self):
        parsed = jw.parse_invocation_context('{"focused_pane_id":"w1:p1"}')
        self.assertEqual(jw.focused_pane_id(parsed), "w1:p1")

    def test_reject_missing_focused_pane(self):
        with self.assertRaises(jw.PluginError):
            jw.focused_pane_id({})

    def test_parse_pane_for_codex_and_claude(self):
        for agent in ("codex", "claude"):
            raw = {
                "pane_id": "w:p",
                "terminal_id": "t",
                "cwd": "/work",
                "foreground_cwd": "/work/repo",
                "agent_session": {
                    "source": "official",
                    "agent": agent,
                    "kind": "id",
                    "value": "native",
                },
            }
            parsed = jw.parse_pane(raw)
            self.assertEqual(jw.require_supported_session(parsed).agent, agent)

    def test_reject_unsupported_native_sessions(self):
        cases = (
            pane(agent=None),
            pane(kind="path"),
            pane(value=""),
            pane(agent="gemini"),
        )
        for candidate in cases:
            with self.subTest(candidate=candidate), self.assertRaises(jw.PluginError):
                jw.require_supported_session(candidate)

    def test_codex_environment_removes_claude_markers(self):
        child = jw.build_child_environment(
            {
                "KEEP": "yes",
                "CLAUDECODE": "1",
                "CLAUDE_CODE": "1",
                "CLAUDE_CODE_SESSION_ID": "wrong",
            },
            session("codex", "codex-native"),
        )
        self.assertEqual(child["CODEX_THREAD_ID"], "codex-native")
        self.assertEqual(child["KEEP"], "yes")
        self.assertNotIn("CLAUDECODE", child)
        self.assertNotIn("CLAUDE_CODE", child)
        self.assertNotIn("CLAUDE_CODE_SESSION_ID", child)

    def test_claude_environment_removes_codex_marker(self):
        child = jw.build_child_environment(
            {"CODEX_THREAD_ID": "wrong"}, session("claude", "claude-native")
        )
        self.assertNotIn("CODEX_THREAD_ID", child)
        self.assertEqual(child["CLAUDECODE"], "1")
        self.assertEqual(child["CLAUDE_CODE"], "1")
        self.assertEqual(child["CLAUDE_CODE_SESSION_ID"], "claude-native")


class HerdrClientTests(unittest.TestCase):
    def test_get_pane_parses_standard_envelope(self):
        result = subprocess.CompletedProcess(
            [],
            0,
            envelope(
                {
                    "type": "pane_info",
                    "pane": {
                        "pane_id": "w:p",
                        "terminal_id": "terminal",
                        "agent_session": None,
                    },
                }
            ),
            "",
        )
        runner = QueueRunner([result])
        client = jw.HerdrClient("/bin/herdr", "job.watch", runner=runner)
        self.assertEqual(client.get_pane("w:p").terminal_id, "terminal")
        self.assertEqual(runner.calls[0][0], ["/bin/herdr", "pane", "get", "w:p"])

    def test_open_uses_exact_split_target_environment_and_focus_argv(self):
        result = subprocess.CompletedProcess(
            [],
            0,
            envelope(
                {
                    "type": "plugin_pane_opened",
                    "plugin_pane": {
                        "plugin_id": "job.watch",
                        "entrypoint": "watch",
                        "pane": {"pane_id": "w:p2", "terminal_id": "watcher"},
                    },
                }
            ),
            "",
        )
        runner = QueueRunner([result])
        client = jw.HerdrClient("/bin/herdr", "job.watch", runner=runner)
        client.open_plugin_pane(
            "w:p1",
            {"JOB_WATCH_TARGET_PANE_ID": "w:p1", "JOB_WATCH_JOB_BIN": "/bin/job"},
            False,
        )
        argv = runner.calls[0][0]
        self.assertEqual(
            argv,
            [
                "/bin/herdr",
                "plugin",
                "pane",
                "open",
                "--plugin",
                "job.watch",
                "--entrypoint",
                "watch",
                "--placement",
                "split",
                "--target-pane",
                "w:p1",
                "--direction",
                "down",
                "--env",
                "JOB_WATCH_TARGET_PANE_ID=w:p1",
                "--env",
                "JOB_WATCH_JOB_BIN=/bin/job",
                "--no-focus",
            ],
        )

    def test_error_code_is_parsed_without_exposing_body(self):
        result = subprocess.CompletedProcess(
            [], 1, "", '{"error":{"code":"pane_not_found","message":"secret"}}'
        )
        client = jw.HerdrClient("herdr", "job.watch", runner=QueueRunner([result]))
        with self.assertRaises(jw.HerdrError) as caught:
            client.get_pane("gone")
        self.assertTrue(caught.exception.is_pane_not_found)
        self.assertNotIn("secret", caught.exception.user_message)
        self.assertNotIn("secret", caught.exception.log_message)


class JobPreflightTests(unittest.TestCase):
    def test_rejects_incompatible_job_binary(self):
        result = subprocess.CompletedProcess([], 0, "Usage: job watch", "")
        with self.assertRaises(jw.PluginError):
            jw.preflight_job(
                jw.Config(job_binary="/bin/echo"), runner=QueueRunner([result])
            )

    def test_accepts_capability_compatible_development_build(self):
        result = subprocess.CompletedProcess(
            [], 0, "Usage: job watch [--since duration]", ""
        )
        path = jw.preflight_job(
            jw.Config(job_binary="/bin/echo"), runner=QueueRunner([result])
        )
        self.assertEqual(path, "/bin/echo")


class RegistryTests(unittest.TestCase):
    def test_paths_are_namespaced_by_socket(self):
        one = jw.registry_paths(Path("/state"), "/tmp/herdr-one.sock")
        two = jw.registry_paths(Path("/state"), "/tmp/herdr-two.sock")
        self.assertNotEqual(one.registry, two.registry)
        self.assertRegex(one.registry.name, r"^registry-[0-9a-f]{16}\.json$")

    def test_atomic_write_and_read_use_mode_0600(self):
        with tempfile.TemporaryDirectory() as directory:
            registry = jw.Registry(Path(directory), "/tmp/socket")
            entry = jw.RegistryEntry("target", "w:p1", "watcher", "w:p2", 123)
            with registry.locked():
                registry.write_locked({"target": entry})
                restored, quarantined = registry.read_locked()
            self.assertIsNone(quarantined)
            self.assertEqual(restored["target"], entry)
            mode = stat.S_IMODE(registry.paths.registry.stat().st_mode)
            self.assertEqual(mode, 0o600)
            leftovers = list(Path(directory).glob(".registry-*.json-*"))
            self.assertEqual(leftovers, [])

    def test_corruption_is_quarantined(self):
        with tempfile.TemporaryDirectory() as directory:
            registry = jw.Registry(Path(directory), "/tmp/socket", clock_ms=lambda: 999)
            registry.paths.registry.write_text(
                "contains-native-session-secret", encoding="utf-8"
            )
            with registry.locked():
                restored, quarantined = registry.read_locked()
            self.assertEqual(restored, {})
            self.assertIsNotNone(quarantined)
            self.assertTrue(quarantined.exists())
            self.assertTrue(quarantined.name.endswith(".999.corrupt"))
            self.assertFalse(registry.paths.registry.exists())


class ToggleTests(unittest.TestCase):
    def setUp(self):
        self.temp = tempfile.TemporaryDirectory()
        root = Path(self.temp.name)
        self.config_dir = root / "config"
        self.state_dir = root / "state"
        self.config_dir.mkdir()
        self.state_dir.mkdir()
        self.env = {
            "HERDR_PLUGIN_CONFIG_DIR": str(self.config_dir),
            "HERDR_PLUGIN_STATE_DIR": str(self.state_dir),
            "HERDR_SOCKET_PATH": "/tmp/herdr-test.sock",
            "HERDR_PLUGIN_ID": "job.watch",
            "HERDR_PLUGIN_ROOT": str(PLUGIN_DIR),
            "HERDR_PLUGIN_CONTEXT_JSON": json.dumps(
                {
                    "focused_pane_id": "w1:p1",
                    "focused_pane_cwd": "/fallback/pane",
                    "workspace_cwd": "/fallback/workspace",
                }
            ),
        }
        self.preflight = lambda _config: "/absolute/job"

    def tearDown(self):
        self.temp.cleanup()

    def _registry(self):
        return jw.Registry(self.state_dir, self.env["HERDR_SOCKET_PATH"])

    def _write_entry(self, entry):
        registry = self._registry()
        with registry.locked():
            registry.write_locked({entry.target_terminal_id: entry})

    def _read_entries(self):
        registry = self._registry()
        with registry.locked():
            entries, _ = registry.read_locked()
        return entries

    def test_open_records_mapping_and_default_resize(self):
        herdr = FakeHerdr()
        result = jw.toggle_action(self.env, herdr, self.preflight, clock_ms=lambda: 123)
        self.assertEqual(result, 0)
        opened = next(call for call in herdr.calls if call[0] == "open")
        self.assertEqual(opened[1], "w1:p1")
        self.assertEqual(
            opened[2],
            {
                "JOB_WATCH_TARGET_PANE_ID": "w1:p1",
                "JOB_WATCH_JOB_BIN": "/absolute/job",
                "JOB_WATCH_TARGET_CWD": "/workspace/repo",
            },
        )
        self.assertFalse(opened[3])
        self.assertIn(("resize", "w1:p1", "down", 0.30), herdr.calls)
        entry = self._read_entries()["terminal-source"]
        self.assertEqual(entry.created_unix_ms, 123)

    def test_toggle_live_watcher_closed_from_target(self):
        herdr = FakeHerdr()
        watcher = pane("w1:p2", "terminal-watcher", agent=None)
        herdr.panes[watcher.pane_id] = watcher
        herdr.live_watchers = 1
        entry = jw.RegistryEntry(
            "terminal-source", "w1:p1", "terminal-watcher", "w1:p2", 1
        )
        self._write_entry(entry)
        self.assertEqual(jw.toggle_action(self.env, herdr, self.preflight), 0)
        self.assertIn(("close", "w1:p2"), herdr.calls)
        self.assertEqual(self._read_entries(), {})
        self.assertEqual(herdr.open_count, 0)

    def test_toggle_from_watcher_terminal_closes_itself(self):
        source = pane()
        watcher = pane("w1:p2", "terminal-watcher", agent=None)
        herdr = FakeHerdr(source)
        herdr.panes[watcher.pane_id] = watcher
        herdr.live_watchers = 1
        entry = jw.RegistryEntry(
            "terminal-source", "w1:p1", "terminal-watcher", "w1:p2", 1
        )
        self._write_entry(entry)
        self.env["HERDR_PLUGIN_CONTEXT_JSON"] = json.dumps({"focused_pane_id": "w1:p2"})
        self.assertEqual(jw.toggle_action(self.env, herdr, self.preflight), 0)
        self.assertIn(("close", "w1:p2"), herdr.calls)
        self.assertEqual(self._read_entries(), {})

    def test_stale_mapping_is_removed_and_replaced(self):
        herdr = FakeHerdr()
        entry = jw.RegistryEntry("terminal-source", "w1:p1", "gone", "w1:p9", 1)
        self._write_entry(entry)
        self.assertEqual(jw.toggle_action(self.env, herdr, self.preflight), 0)
        self.assertEqual(herdr.open_count, 1)
        restored = self._read_entries()["terminal-source"]
        self.assertNotEqual(restored.watcher_terminal_id, "gone")

    def test_no_registry_write_after_open_failure(self):
        herdr = FakeHerdr()
        herdr.open_error = jw.HerdrError("open the watcher pane")
        self.assertEqual(jw.toggle_action(self.env, herdr, self.preflight), 1)
        self.assertFalse(self._registry().paths.registry.exists())
        self.assertTrue(herdr.notifications)

    def test_resize_failure_preserves_watcher_and_warns(self):
        herdr = FakeHerdr()
        herdr.resize_error = jw.HerdrError("resize the source pane")
        self.assertEqual(jw.toggle_action(self.env, herdr, self.preflight), 0)
        self.assertIn("terminal-source", self._read_entries())
        self.assertTrue(any("50/50" in body for body in herdr.notifications))

    def test_close_failure_retains_mapping_and_does_not_open_duplicate(self):
        herdr = FakeHerdr()
        watcher = pane("w1:p2", "terminal-watcher", agent=None)
        herdr.panes[watcher.pane_id] = watcher
        entry = jw.RegistryEntry(
            "terminal-source", "w1:p1", "terminal-watcher", "w1:p2", 1
        )
        self._write_entry(entry)
        herdr.close_error = jw.HerdrError("close the watcher pane")
        self.assertEqual(jw.toggle_action(self.env, herdr, self.preflight), 1)
        self.assertIn("terminal-source", self._read_entries())
        self.assertEqual(herdr.open_count, 0)

    def test_unsupported_pane_does_not_mutate_layout(self):
        herdr = FakeHerdr(pane(agent=None))
        self.assertEqual(jw.toggle_action(self.env, herdr, self.preflight), 1)
        self.assertFalse(
            any(call[0] in ("open", "resize", "close") for call in herdr.calls)
        )
        self.assertIn("No Codex or Claude session", herdr.notifications[-1])

    def test_invalid_config_does_not_mutate_layout(self):
        (self.config_dir / "config.json").write_text(
            json.dumps({"watcher_fraction": 0.99}), encoding="utf-8"
        )
        herdr = FakeHerdr()
        self.assertEqual(jw.toggle_action(self.env, herdr, self.preflight), 1)
        self.assertFalse(
            any(call[0] in ("open", "resize", "close") for call in herdr.calls)
        )

    def test_preflight_failure_does_not_mutate_layout(self):
        herdr = FakeHerdr()

        def fail(_config):
            raise jw.PluginError("job is incompatible")

        self.assertEqual(jw.toggle_action(self.env, herdr, fail), 1)
        self.assertFalse(
            any(call[0] in ("open", "resize", "close") for call in herdr.calls)
        )

    def test_rapid_concurrent_toggles_never_open_duplicates(self):
        herdr = FakeHerdr()
        barrier = threading.Barrier(2)
        target_gets = 0
        counter_lock = threading.Lock()

        def synchronize_initial_get(pane_id):
            nonlocal target_gets
            if pane_id != "w1:p1":
                return
            with counter_lock:
                target_gets += 1
                ordinal = target_gets
            if ordinal <= 2:
                barrier.wait(timeout=2)
            return

        herdr.get_hook = synchronize_initial_get
        results = []

        def invoke():
            results.append(jw.toggle_action(self.env, herdr, self.preflight))

        threads = [threading.Thread(target=invoke) for _ in range(2)]
        for thread in threads:
            thread.start()
        for thread in threads:
            thread.join(timeout=5)
        self.assertFalse(any(thread.is_alive() for thread in threads))
        self.assertEqual(results, [0, 0])
        self.assertEqual(herdr.max_live_watchers, 1)
        self.assertEqual(herdr.open_count, 1)


class SequenceHerdr(FakeHerdr):
    def __init__(self, responses):
        super().__init__()
        self.responses = list(responses)

    def get_pane(self, pane_id):
        self.calls.append(("get", pane_id))
        if self.responses:
            response = self.responses.pop(0)
            if isinstance(response, Exception):
                raise response
            return response
        return super().get_pane(pane_id)


class SupervisorTests(unittest.TestCase):
    def config(self, **changes):
        return jw.Config(
            poll_interval_ms=changes.get("poll_interval_ms", 250),
            target_missing_polls=changes.get("target_missing_polls", 2),
            child_shutdown_grace_ms=changes.get("child_shutdown_grace_ms", 100),
            history_since=changes.get("history_since", "1h"),
        )

    def supervisor(
        self, herdr, factory, config=None, base=None, sleep=lambda _seconds: None
    ):
        return jw.WatchSupervisor(
            config or self.config(),
            "w1:p1",
            "w1:p2",
            "/absolute/job",
            herdr,
            base or {},
            process_factory=factory,
            sleep=sleep,
        )

    def test_starts_plain_watch_with_default_history_and_agent_environment(self):
        source = pane(agent="codex", value="native-secret")
        herdr = SequenceHerdr([source])
        factory = FakeProcessFactory([FakeProcess(status=0)])
        status = self.supervisor(
            herdr,
            factory,
            base={"CLAUDE_CODE_SESSION_ID": "wrong", "KEEP": "yes"},
        ).run()
        self.assertEqual(status, 0)
        argv, kwargs = factory.calls[0]
        self.assertEqual(argv, ["/absolute/job", "watch", "--since", "1h"])
        self.assertEqual(kwargs["cwd"], "/workspace/repo")
        self.assertEqual(kwargs["env"]["CODEX_THREAD_ID"], "native-secret")
        self.assertNotIn("CLAUDE_CODE_SESSION_ID", kwargs["env"])
        self.assertIn(("rename", "w1:p2", "jobs · codex"), herdr.calls)

    def test_uses_action_cwd_fallback_when_pane_cwd_is_missing(self):
        source = pane(cwd=None, foreground_cwd=None)
        herdr = SequenceHerdr([source])
        factory = FakeProcessFactory([FakeProcess(status=0)])
        self.supervisor(
            herdr,
            factory,
            base={"JOB_WATCH_TARGET_CWD": "/fallback/repo"},
        ).run()
        self.assertEqual(factory.calls[0][1]["cwd"], "/fallback/repo")

    def test_empty_history_omits_since(self):
        source = pane()
        herdr = SequenceHerdr([source])
        factory = FakeProcessFactory([FakeProcess(status=0)])
        self.supervisor(herdr, factory, self.config(history_since="")).run()
        self.assertEqual(factory.calls[0][0], ["/absolute/job", "watch"])

    def test_rebinds_after_native_session_change(self):
        old = pane(agent="codex", value="old-native")
        new = pane(agent="claude", value="new-native")
        herdr = SequenceHerdr([old, new])
        first = FakeProcess(exit_on_signal=True)
        second = FakeProcess(status=0)
        factory = FakeProcessFactory([first, second])
        status = self.supervisor(herdr, factory).run()
        self.assertEqual(status, 0)
        self.assertEqual(first.signals, [jw.signal.SIGINT])
        self.assertEqual(
            factory.calls[1][1]["env"]["CLAUDE_CODE_SESSION_ID"], "new-native"
        )
        self.assertNotIn("CODEX_THREAD_ID", factory.calls[1][1]["env"])
        self.assertIn(("rename", "w1:p2", "jobs · claude"), herdr.calls)

    def test_temporary_missing_session_identity_is_tolerated(self):
        source = pane()
        no_session = pane(agent=None)
        herdr = SequenceHerdr([source, no_session, source])
        child = FakeProcess(exit_after_polls=3)
        factory = FakeProcessFactory([child])
        self.assertEqual(self.supervisor(herdr, factory).run(), 0)
        self.assertEqual(len(factory.calls), 1)
        self.assertEqual(child.signals, [])

    def test_exits_after_target_missing_threshold(self):
        source = pane()
        missing = jw.HerdrError("resolve a pane", "pane_not_found")
        herdr = SequenceHerdr([source, missing, missing])
        child = FakeProcess(exit_on_signal=True)
        factory = FakeProcessFactory([child])
        self.assertEqual(self.supervisor(herdr, factory).run(), 0)
        self.assertEqual(child.signals, [jw.signal.SIGINT])

    def test_terminal_id_change_is_treated_as_missing_target(self):
        source = pane()
        replacement = pane(terminal_id="reused-terminal")
        herdr = SequenceHerdr([source, replacement])
        child = FakeProcess(exit_on_signal=True)
        factory = FakeProcessFactory([child])
        config = self.config(target_missing_polls=1)
        self.assertEqual(self.supervisor(herdr, factory, config).run(), 0)
        self.assertEqual(child.signals, [jw.signal.SIGINT])

    def test_supervisor_signal_is_forwarded_and_escalates(self):
        source = pane()
        herdr = SequenceHerdr([source])
        child = FakeProcess(exit_on_signal=False)
        factory = FakeProcessFactory([child])
        holder = {}

        def stop_during_sleep(_seconds):
            holder["supervisor"].request_stop(jw.signal.SIGTERM)

        supervisor = self.supervisor(herdr, factory, sleep=stop_during_sleep)
        holder["supervisor"] = supervisor
        self.assertEqual(supervisor.run(), 0)
        self.assertEqual(child.signals, [jw.signal.SIGTERM])
        self.assertEqual(child.terminated, 1)
        self.assertEqual(child.killed, 1)

    def test_nonzero_child_exit_notifies_without_leaking_session(self):
        secret = "native-session-do-not-leak"
        source = pane(value=secret)
        herdr = SequenceHerdr([source])
        factory = FakeProcessFactory([FakeProcess(status=7)])
        self.assertEqual(self.supervisor(herdr, factory).run(), 7)
        self.assertTrue(herdr.notifications)
        self.assertIn("status 7", herdr.notifications[-1])
        self.assertNotIn(secret, herdr.notifications[-1])

    def test_initial_session_is_retried_for_three_intervals(self):
        no_session = pane(agent=None)
        source = pane(agent="claude", value="ready")
        herdr = SequenceHerdr([no_session, no_session, source])
        sleeps = []
        factory = FakeProcessFactory([FakeProcess(status=0)])
        status = self.supervisor(herdr, factory, sleep=sleeps.append).run()
        self.assertEqual(status, 0)
        self.assertEqual(sleeps, [0.25, 0.25, 0.25])
        self.assertEqual(factory.calls[0][1]["env"]["CLAUDE_CODE_SESSION_ID"], "ready")


if __name__ == "__main__":
    unittest.main()
