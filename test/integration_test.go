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
