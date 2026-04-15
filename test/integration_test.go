package integration

import (
	"bytes"
	"errors"
	"log"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"

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
	t         *testing.T
	stateDir  string
	configDir string
	db        *db.DB
}

func newHarness(t *testing.T) *harness {
	t.Helper()
	stateDir := t.TempDir()
	configDir := t.TempDir()
	if err := os.MkdirAll(filepath.Join(stateDir, "db"), 0o755); err != nil {
		t.Fatal(err)
	}
	d, err := db.Open(filepath.Join(stateDir, "db", "job.db"))
	require.NoError(t, err)
	t.Cleanup(func() { d.Close() })
	return &harness{t: t, stateDir: stateDir, configDir: configDir, db: d}
}

// writeConfig writes content to the harness config file.
func (h *harness) writeConfig(content string) {
	h.t.Helper()
	err := os.WriteFile(filepath.Join(h.configDir, "config.toml"), []byte(content), 0o644)
	require.NoError(h.t, err)
}

// writeScript writes an executable shell script and returns its path.
func (h *harness) writeScript(content string) string {
	h.t.Helper()
	path := filepath.Join(h.t.TempDir(), "script.sh")
	err := os.WriteFile(path, []byte("#!/bin/sh\n"+content), 0o755)
	require.NoError(h.t, err)
	return path
}

// result holds the output of a CLI invocation.
type result struct {
	stdout   string
	stderr   string
	exitCode int
}

func (h *harness) run(args ...string) result {
	return h.runFrom("", args...)
}

func (h *harness) runFrom(dir string, args ...string) result {
	h.t.Helper()
	cmd := exec.Command(binary, args...)
	cmd.Env = append(os.Environ(), "JOB_STATE_DIR="+h.stateDir, "JOB_CONFIG_DIR="+h.configDir)
	if dir != "" {
		cmd.Dir = dir
	}
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	err := cmd.Run()
	code := 0
	if err != nil {
		if exitErr, ok := errors.AsType[*exec.ExitError](err); ok {
			code = exitErr.ExitCode()
		}
	}
	return result{stdout.String(), stderr.String(), code}
}

// waitFor polls until the job reaches the expected status or the test times out.
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

// waitForFile polls until path exists or the test times out.
func waitForFile(t *testing.T, path string) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		if _, err := os.Stat(path); err == nil {
			return
		}
		time.Sleep(50 * time.Millisecond)
	}
	t.Fatalf("file %s did not appear within 5s", path)
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
