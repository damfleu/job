package integration

import (
	"bytes"
	"errors"
	"log"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"job/internal/db"
	"job/internal/model"
)

var binary string

func TestMain(m *testing.M) {
	tmp, err := os.MkdirTemp("", "job-integration-*")
	if err != nil {
		log.Fatal(err)
	}
	defer os.RemoveAll(tmp)

	binary = filepath.Join(tmp, "job")
	root := projectRoot()
	cmd := exec.Command("go", "build", "-o", binary, ".")
	cmd.Dir = root
	cmd.Stdout = os.Stderr
	cmd.Stderr = os.Stderr
	if err := cmd.Run(); err != nil {
		log.Fatal("build failed:", err)
	}

	os.Exit(m.Run())
}

// harness holds per-test state.
type harness struct {
	t        *testing.T
	stateDir string
	db       *db.DB
}

func newHarness(t *testing.T) *harness {
	t.Helper()
	stateDir := t.TempDir()
	if err := os.MkdirAll(filepath.Join(stateDir, "db"), 0o755); err != nil {
		t.Fatal(err)
	}
	d, err := db.Open(filepath.Join(stateDir, "db", "job.db"))
	require.NoError(t, err)
	t.Cleanup(func() { d.Close() })
	return &harness{t: t, stateDir: stateDir, db: d}
}

// run executes the job binary with the harness state dir.
type result struct {
	stdout   string
	stderr   string
	exitCode int
}

func (h *harness) run(args ...string) result {
	h.t.Helper()
	cmd := exec.Command(binary, args...)
	cmd.Env = append(os.Environ(), "JOB_STATE_DIR="+h.stateDir)
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	err := cmd.Run()
	code := 0
	if err != nil {
		var exitErr *exec.ExitError
		if errors.As(err, &exitErr) {
			code = exitErr.ExitCode()
		}
	}
	return result{stdout.String(), stderr.String(), code}
}

// waitFor polls until the job reaches the expected status or the test fails.
func (h *harness) waitFor(key string, status model.Status) {
	h.t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		j, err := h.db.Get(key)
		if err == nil && j.Status == status {
			return
		}
		time.Sleep(50 * time.Millisecond)
	}
	h.t.Fatalf("job %s did not reach %s within 5s", key, status)
}

// lastJob returns the most recently started job from the DB.
func (h *harness) lastJob() *model.Job {
	h.t.Helper()
	key, err := h.db.GetLastKey()
	require.NoError(h.t, err)
	require.NotEmpty(h.t, key)
	j, err := h.db.Get(key)
	require.NoError(h.t, err)
	return j
}

// --- Tests ---

func TestForegroundJob(t *testing.T) {
	h := newHarness(t)
	r := h.run("-f", "--", "echo", "hello integration")
	assert.Equal(t, 0, r.exitCode)
	assert.Contains(t, r.stdout, "hello integration")

	j := h.lastJob()
	assert.Equal(t, model.StatusCompleted, j.Status)
	assert.Equal(t, model.ReasonExited, j.Reason)
	require.NotNil(t, j.ExitCode)
	assert.Equal(t, 0, *j.ExitCode)

	content, err := os.ReadFile(j.LogFile)
	require.NoError(t, err)
	assert.Contains(t, string(content), "hello integration")
}

func TestForegroundJobNonZeroExit(t *testing.T) {
	h := newHarness(t)
	r := h.run("-f", "--", "false")
	assert.Equal(t, 1, r.exitCode)

	j := h.lastJob()
	assert.Equal(t, model.StatusCompleted, j.Status)
	require.NotNil(t, j.ExitCode)
	assert.Equal(t, 1, *j.ExitCode)
}

func TestBackgroundJob(t *testing.T) {
	h := newHarness(t)
	r := h.run("-v", "--", "echo", "bg hello")
	assert.Equal(t, 0, r.exitCode)
	key := strings.TrimSpace(r.stderr)
	require.NotEmpty(t, key)

	h.waitFor(key, model.StatusCompleted)

	j, err := h.db.Get(key)
	require.NoError(t, err)
	assert.Equal(t, model.StatusCompleted, j.Status)
	require.NotNil(t, j.ExitCode)
	assert.Equal(t, 0, *j.ExitCode)

	content, err := os.ReadFile(j.LogFile)
	require.NoError(t, err)
	assert.Contains(t, string(content), "bg hello")
}

func TestJobShow(t *testing.T) {
	h := newHarness(t)
	h.run("-f", "--", "echo", "show test")

	r := h.run("show")
	assert.Equal(t, 0, r.exitCode)
	assert.Contains(t, r.stdout, "echo show test")
	assert.Contains(t, r.stdout, "completed")
}

func TestJobLog(t *testing.T) {
	h := newHarness(t)
	h.run("-f", "--", "echo", "log test output")

	r := h.run("log")
	assert.Equal(t, 0, r.exitCode)
	assert.Contains(t, r.stdout, "log test output")
}

func TestJobLogPath(t *testing.T) {
	h := newHarness(t)
	h.run("-f", "--", "echo", "hi")

	r := h.run("log", "-p")
	assert.Equal(t, 0, r.exitCode)
	path := strings.TrimSpace(r.stdout)
	assert.True(t, filepath.IsAbs(path))
	_, err := os.Stat(path)
	assert.NoError(t, err)
}

func TestJobList(t *testing.T) {
	h := newHarness(t)
	h.run("-f", "--", "echo", "first")
	h.run("-f", "--", "echo", "second")

	r := h.run("list")
	assert.Equal(t, 0, r.exitCode)
	assert.Contains(t, r.stdout, "echo")
}

func TestJobStop(t *testing.T) {
	h := newHarness(t)
	r := h.run("-v", "--", "sleep", "60")
	key := strings.TrimSpace(r.stderr)
	require.NotEmpty(t, key)

	// wait for it to be running
	h.waitFor(key, model.StatusRunning)

	h.run("stop", key)

	j, err := h.db.Get(key)
	require.NoError(t, err)
	assert.Equal(t, model.StatusCompleted, j.Status)
	assert.Equal(t, model.ReasonStopped, j.Reason)
}

func TestJobRemove(t *testing.T) {
	h := newHarness(t)
	h.run("-f", "--", "echo", "bye")

	j := h.lastJob()
	h.run("remove", j.Key)

	_, err := h.db.Get(j.Key)
	assert.ErrorIs(t, err, db.ErrNotFound)
}

func TestJobAlias(t *testing.T) {
	h := newHarness(t)
	h.run("-f", "-k", "mybuild", "--", "echo", "aliased")

	j := h.lastJob()
	assert.Equal(t, "mybuild", j.Alias)

	r := h.run("show", "mybuild")
	assert.Equal(t, 0, r.exitCode)
	assert.Contains(t, r.stdout, "mybuild")
}

func TestDotResolution(t *testing.T) {
	h := newHarness(t)
	h.run("-f", "--", "echo", "dot test")

	r := h.run("show", ".")
	assert.Equal(t, 0, r.exitCode)
	assert.Contains(t, r.stdout, "echo dot test")
}

func TestVerboseFlag(t *testing.T) {
	h := newHarness(t)
	r := h.run("-v", "-f", "--", "echo", "verbose")
	assert.Contains(t, r.stderr, "running")
	assert.Contains(t, r.stderr, "done")
}

func TestDepAliasResolvesToMostRecentJob(t *testing.T) {
	h := newHarness(t)

	// old completed job with alias "shared-key"
	h.run("-f", "-k", "shared-key", "--", "echo", "first run")

	// new running job with the same alias
	h.run("-v", "-k", "shared-key", "--", "sleep", "2")

	// dep should block on the NEW running job, not the old completed one
	r := h.run("-v", "-a", "shared-key", "--", "echo", "child")
	childKey := strings.TrimSpace(r.stderr)
	require.NotEmpty(t, childKey)

	time.Sleep(200 * time.Millisecond)
	j, err := h.db.Get(childKey)
	require.NoError(t, err)
	assert.Equal(t, model.StatusBlocked, j.Status, "child should be blocked on the running job, not the old completed one")
}

func TestDepBlocksUntilDepCompletes(t *testing.T) {
	h := newHarness(t)

	// spawn a running job and a child that depends on it
	r1 := h.run("-v", "-k", "dep-job", "--", "sleep", "2")
	depKey := strings.TrimSpace(r1.stderr)
	require.NotEmpty(t, depKey)

	r2 := h.run("-v", "-a", "dep-job", "--", "echo", "child")
	childKey := strings.TrimSpace(r2.stderr)
	require.NotEmpty(t, childKey)

	// dep job is running, so child should be blocked
	time.Sleep(200 * time.Millisecond)
	j, err := h.db.Get(childKey)
	require.NoError(t, err)
	assert.Equal(t, model.StatusBlocked, j.Status, "child should be blocked while dep is running")

	// wait for dep to complete, then child should run
	h.waitFor(depKey, model.StatusCompleted)
	h.waitFor(childKey, model.StatusCompleted)

	j, err = h.db.Get(childKey)
	require.NoError(t, err)
	assert.Equal(t, model.ReasonExited, j.Reason)
}

func TestDepAfter(t *testing.T) {
	h := newHarness(t)

	// run a quick job and capture its key
	r := h.run("-v", "-f", "--", "echo", "dep job")
	depKey := strings.TrimSpace(r.stderr)
	// stderr has "key running\nkey done\n", extract first word of first line
	depKey = strings.Fields(depKey)[0]

	// spawn a job that depends on it
	r2 := h.run("-v", "-a", depKey, "--", "echo", "dependent job")
	childKey := strings.TrimSpace(r2.stderr)
	require.NotEmpty(t, childKey)

	h.waitFor(childKey, model.StatusCompleted)

	j, err := h.db.Get(childKey)
	require.NoError(t, err)
	assert.Equal(t, model.StatusCompleted, j.Status)
	assert.Equal(t, model.ReasonExited, j.Reason)
	require.NotNil(t, j.ExitCode)
	assert.Equal(t, 0, *j.ExitCode)
}

func TestDepAfterSuccessFails(t *testing.T) {
	h := newHarness(t)

	// run a job that fails
	r := h.run("-v", "-f", "--", "false")
	depKey := strings.Fields(strings.TrimSpace(r.stderr))[0]

	// spawn a job with after-success dep
	r2 := h.run("-v", "-A", depKey, "--", "echo", "should not run")
	childKey := strings.TrimSpace(r2.stderr)
	require.NotEmpty(t, childKey)

	h.waitFor(childKey, model.StatusCompleted)

	j, err := h.db.Get(childKey)
	require.NoError(t, err)
	assert.Equal(t, model.StatusCompleted, j.Status)
	assert.Equal(t, model.ReasonDepFailed, j.Reason)
	assert.Nil(t, j.ExitCode)
}

func TestDepMixedOrder(t *testing.T) {
	h := newHarness(t)

	r1 := h.run("-v", "-f", "--", "echo", "dep-a")
	keyA := strings.Fields(strings.TrimSpace(r1.stderr))[0]

	r2 := h.run("-v", "-f", "--", "echo", "dep-b")
	keyB := strings.Fields(strings.TrimSpace(r2.stderr))[0]

	r3 := h.run("-v", "-f", "--", "echo", "dep-c")
	keyC := strings.Fields(strings.TrimSpace(r3.stderr))[0]

	// interleaved: -A, -a, -A — order must be preserved
	r4 := h.run("-v", "-A", keyA, "-a", keyB, "-A", keyC, "--", "echo", "child")
	childKey := strings.TrimSpace(r4.stderr)
	require.NotEmpty(t, childKey)

	h.waitFor(childKey, model.StatusCompleted)

	j, err := h.db.Get(childKey)
	require.NoError(t, err)
	assert.Equal(t, model.ReasonExited, j.Reason)
	require.Len(t, j.Deps, 3)
	assert.Equal(t, model.DepAfterSuccess, j.Deps[0].Kind)
	assert.Equal(t, model.DepAfter, j.Deps[1].Kind)
	assert.Equal(t, model.DepAfterSuccess, j.Deps[2].Kind)
}

// projectRoot walks up from the current directory to find the go.mod.
func projectRoot() string {
	dir, _ := os.Getwd()
	for {
		if _, err := os.Stat(filepath.Join(dir, "go.mod")); err == nil {
			return dir
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			log.Fatal("go.mod not found")
		}
		dir = parent
	}
}
