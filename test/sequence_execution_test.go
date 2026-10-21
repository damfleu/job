package integration

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"

	"job/internal/model"
	"job/internal/sequence"
)

func sequenceJobs(h *harness, r result) []*model.Job {
	h.t.Helper()
	require.Equal(h.t, 0, r.exitCode, r.stderr)
	var jobs []*model.Job
	for _, line := range strings.Split(strings.TrimSpace(r.stdout), "\n") {
		fields := strings.Fields(line)
		require.NotEmpty(h.t, fields)
		h.waitFor(fields[0], model.StatusCompleted)
		job, err := h.db.Get(fields[0])
		require.NoError(h.t, err)
		jobs = append(jobs, job)
	}
	return jobs
}

func TestSequenceSaveDefaultsToStdout(t *testing.T) {
	h := newHarness(t)
	key := runFg(h, "printf", "%s", "", "$HOME")
	r := h.run("seq", "save", key)
	require.Equal(t, 0, r.exitCode, r.stderr)
	require.Empty(t, r.stderr)
	d, err := sequence.Decode(strings.NewReader(r.stdout))
	require.NoError(t, err, r.stdout)
	require.Len(t, d.Steps, 1)
	require.Equal(t, "step-1", d.Steps[0].ID)
	require.Equal(t, []string{"printf", "%s", "", "$HOME"}, d.Steps[0].Command)
	require.NoDirExists(t, filepath.Join(h.configDir, "sequences"))
	for _, flags := range [][]string{{"--force"}, {"--stdout"}, {"--verbose"}, {"--output="}} {
		r := h.run(append([]string{"seq", "save", key}, flags...)...)
		require.NotZero(t, r.exitCode)
		require.Empty(t, r.stdout)
	}
}

func TestSequenceRunsShellAndLiteralArguments(t *testing.T) {
	h := newHarness(t)
	dir := t.TempDir()
	h.writeSequenceFile("commands.toml", fmt.Sprintf(`version = 1
cwd = %q

[[steps]]
id = "shell"
run = "printf '%%s' \"$JOB_SEQUENCE_VALUE\" > shell.txt"
after_success = ["literal"]

[[steps]]
id = "literal"
command = ["printf", "%%s|%%s", "$JOB_SEQUENCE_VALUE", ""]
`, dir))
	r := h.runWithEnv(map[string]string{"JOB_SEQUENCE_VALUE": "expanded value"}, "seq", "run", "commands")
	jobs := sequenceJobs(h, r)
	require.Len(t, jobs, 2)
	for _, job := range jobs {
		require.Equal(t, 0, *job.ExitCode)
	}
	literal, err := os.ReadFile(jobs[0].LogFile)
	require.NoError(t, err)
	require.Equal(t, "$JOB_SEQUENCE_VALUE|", string(literal))
	shell, err := os.ReadFile(filepath.Join(dir, "shell.txt"))
	require.NoError(t, err)
	require.Equal(t, "expanded value", string(shell))
	require.Equal(t, []string{"sh", "-c", `printf '%s' "$JOB_SEQUENCE_VALUE" > shell.txt`}, jobs[1].Command)
	require.Equal(t, jobs[0].Key, jobs[1].Deps[0].Key)
}

func TestSequenceAutomationAndExternalDotAreResolvedBeforeLaunch(t *testing.T) {
	h := newHarness(t)
	gate := runFg(h, "true")
	h.waitFor(gate, model.StatusCompleted)
	h.writeSequenceFile("automation.toml", `version = 1
[[steps]]
id = "default"
command = ["true"]
[[steps]]
id = "manual"
command = ["true"]
automated = false
[[steps]]
id = "inherited"
command = ["true"]
`)
	jobs := sequenceJobs(h, h.run("seq", "run", "-A", ".", "automation"))
	require.Len(t, jobs, 3)
	require.True(t, jobs[0].Automated)
	require.False(t, jobs[1].Automated)
	require.True(t, jobs[2].Automated)
	for _, job := range jobs {
		require.Equal(t, []model.Dep{{Key: gate, Kind: model.DepAfterSuccess}}, job.Deps)
	}
	last, err := h.db.GetLastKeyForContext("")
	require.NoError(t, err)
	require.Equal(t, jobs[1].Key, last)
}

func TestSequenceDefaultsToInvocationDirectory(t *testing.T) {
	h := newHarness(t)
	dir, err := filepath.EvalSymlinks(t.TempDir())
	require.NoError(t, err)
	h.writeSequenceFile("defaults.toml", "version = 1\nautomated = false\n[[steps]]\nid = 'pwd'\ncommand = ['pwd']\n")
	jobs := sequenceJobs(h, h.runFrom(dir, "seq", "run", "defaults"))
	require.Len(t, jobs, 1)
	require.Equal(t, dir, jobs[0].WorkDir)
	require.False(t, jobs[0].Automated)
}

func TestSequenceRunPreflightDoesNotCreatePartialJobs(t *testing.T) {
	for _, suffix := range []string{
		"after = ['missing']",
		"cwd = '/job-test-directory-that-does-not-exist'",
	} {
		t.Run(suffix, func(t *testing.T) {
			h := newHarness(t)
			h.writeSequenceFile("bad.toml", validSequenceFile+"\n[[steps]]\nid = 'bad'\ncommand = ['true']\n"+suffix)
			r := h.run("seq", "run", "bad")
			require.NotZero(t, r.exitCode)
			require.Empty(t, r.stdout)
			active, err := h.db.ListActive("", "")
			require.NoError(t, err)
			require.Empty(t, active)
			completed, err := h.db.ListCompleted(100, "", "")
			require.NoError(t, err)
			require.Empty(t, completed)
		})
	}
}

func TestSequenceCwdOverrideIgnoresUnavailableConfiguredDirectories(t *testing.T) {
	h := newHarness(t)
	dir, err := filepath.EvalSymlinks(t.TempDir())
	require.NoError(t, err)
	require.NoError(t, os.Mkdir(filepath.Join(dir, "actual"), 0o700))
	h.writeSequenceFile("override.toml", `version = 1
cwd = "/job-test-missing-global-cwd"
[[steps]]
id = "global"
command = ["pwd"]
[[steps]]
id = "step"
command = ["pwd"]
cwd = "/job-test-missing-step-cwd"
`)
	jobs := sequenceJobs(h, h.runFrom(dir, "seq", "run", "--cwd=actual", "override"))
	require.Len(t, jobs, 2)
	for _, job := range jobs {
		require.Equal(t, filepath.Join(dir, "actual"), job.WorkDir)
		require.Equal(t, 0, *job.ExitCode)
	}
}

func TestSequenceAfterRunsWhenDependencyWasSkipped(t *testing.T) {
	h := newHarness(t)
	h.writeSequenceFile("failure.toml", `version = 1
[[steps]]
id = "fail"
command = ["false"]
[[steps]]
id = "skipped"
command = ["true"]
after_success = ["fail"]
[[steps]]
id = "cleanup"
command = ["echo", "cleanup"]
after = ["skipped"]
[[steps]]
id = "independent"
command = ["true"]
`)
	jobs := sequenceJobs(h, h.run("seq", "run", "failure"))
	require.Len(t, jobs, 4)
	require.Equal(t, 1, *jobs[0].ExitCode)
	require.Equal(t, model.ReasonDepFailed, jobs[1].Reason)
	require.Nil(t, jobs[1].ExitCode)
	require.Equal(t, 0, *jobs[2].ExitCode)
	require.Equal(t, 0, *jobs[3].ExitCode)
}

func TestSequenceFileEditsDoNotChangeQueuedJobs(t *testing.T) {
	h := newHarness(t)
	release := filepath.Join(t.TempDir(), "release")
	// Release the gate even when an assertion fails so no background job leaks.
	t.Cleanup(func() { _ = os.WriteFile(release, nil, 0o600) })
	gate := runFg(h, "sh", "-c", `while [ ! -f "$1" ]; do sleep 0.05; done`, "gate", release)
	path := h.writeSequenceFile("snapshot.toml", "version = 1\n[[steps]]\nid = 'original'\ncommand = ['echo', 'original']\n")
	r := h.run("seq", "run", "-A", gate, "snapshot")
	require.Equal(t, 0, r.exitCode, r.stderr)
	key := strings.Fields(r.stdout)[0]
	h.waitFor(key, model.StatusBlocked)
	require.NoError(t, os.WriteFile(path, []byte("version = 1\n[[steps]]\nid = 'changed'\ncommand = ['echo', 'changed']\n"), 0o600))
	require.NoError(t, os.WriteFile(release, nil, 0o600))
	jobs := sequenceJobs(h, r)
	require.Equal(t, []string{"echo", "original"}, jobs[0].Command)
	jobs = sequenceJobs(h, h.run("seq", "run", "snapshot"))
	require.Equal(t, []string{"echo", "changed"}, jobs[0].Command)
}

func TestSequenceShowAndListReadFilesWithoutDatabase(t *testing.T) {
	h := newHarness(t)
	path := h.writeSequenceFile("ci.toml", validSequenceFile)
	stateFile := filepath.Join(t.TempDir(), "not-a-directory")
	require.NoError(t, os.WriteFile(stateFile, nil, 0o600))
	env := map[string]string{"JOB_STATE_DIR": stateFile}
	r := h.runWithEnv(env, "seq", "show", "--verbose", "ci")
	require.Equal(t, 0, r.exitCode, r.stderr)
	require.Contains(t, r.stdout, "hello:")
	require.Contains(t, r.stdout, "echo hello")
	require.Contains(t, r.stdout, "automated: true")
	require.Contains(t, r.stdout, path)
	r = h.runWithEnv(env, "seq", "list")
	require.Equal(t, 0, r.exitCode, r.stderr)
	require.Contains(t, r.stdout, path)
	require.NotContains(t, r.stdout, "CREATED")
	h.writeSequenceFile("broken.toml", "version = 99")
	r = h.runWithEnv(env, "seq", "list")
	require.NotZero(t, r.exitCode)
	require.Contains(t, r.stdout, path)
	require.Contains(t, r.stderr, "broken.toml")

	for _, command := range []string{"show", "run"} {
		r := h.run("seq", command, path)
		require.NotZero(t, r.exitCode)
		require.Contains(t, r.stderr, "expected a name, not a file path")
	}
	for _, command := range []string{"rm", "remove"} {
		r := h.run("seq", command, "ci")
		require.NotZero(t, r.exitCode)
		require.FileExists(t, path)
	}
	require.NoError(t, os.Remove(path))
	r = h.runWithEnv(env, "seq", "show", "ci")
	require.NotZero(t, r.exitCode)
}
