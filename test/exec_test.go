package integration

import (
	"os"
	"os/exec"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"job/internal/model"
)

func TestForegroundJob(t *testing.T) {
	h := newHarness(t)
	r := h.run("run", "-f", "echo", "hello integration")
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
	r := h.run("run", "-f", "false")
	assert.Equal(t, 1, r.exitCode)

	j := h.lastJob()
	assert.Equal(t, model.StatusCompleted, j.Status)
	require.NotNil(t, j.ExitCode)
	assert.Equal(t, 1, *j.ExitCode)
}

func TestForegroundInterruptCleansUp(t *testing.T) {
	h := newHarness(t)
	cmd := exec.Command(binary, "run", "-f", "sleep", "60")
	cmd.Env = append(os.Environ(), "JOB_STATE_DIR="+h.stateDir, "JOB_CONFIG_DIR="+h.configDir)
	require.NoError(t, cmd.Start())

	finished := false
	t.Cleanup(func() {
		if !finished {
			_ = cmd.Process.Kill()
			_ = cmd.Wait()
		}
	})

	var running *model.Job
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		key, err := h.db.GetLastKeyForContext("")
		if err == nil && key != "" {
			j, getErr := h.db.Get(key)
			if getErr == nil && j.Status == model.StatusRunning {
				running = j
				break
			}
		}
		time.Sleep(20 * time.Millisecond)
	}
	require.NotNil(t, running, "foreground job did not reach running state")

	childPID := running.PID
	require.NoError(t, cmd.Process.Signal(os.Interrupt))

	waitResult := make(chan error, 1)
	go func() { waitResult <- cmd.Wait() }()
	select {
	case err := <-waitResult:
		finished = true
		assert.Error(t, err)
	case <-time.After(5 * time.Second):
		_ = cmd.Process.Kill()
		<-waitResult
		finished = true
		t.Fatal("foreground CLI did not exit after interrupt")
	}

	assert.Equal(t, 130, cmd.ProcessState.ExitCode())
	got, err := h.db.Get(running.Key)
	require.NoError(t, err)
	assert.Equal(t, model.StatusCompleted, got.Status)
	assert.Equal(t, model.ReasonStopped, got.Reason)
	assert.Zero(t, got.PID)
	assert.Zero(t, got.PGID)
	assert.Error(t, syscall.Kill(childPID, 0), "foreground child should no longer exist")
}

func TestBackgroundJob(t *testing.T) {
	h := newHarness(t)
	r := h.run("run", "echo", "bg hello")
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

func TestQuietFlag(t *testing.T) {
	h := newHarness(t)
	r := h.run("run", "-q", "echo", "quiet")
	assert.Empty(t, r.stderr)
}

func TestDefaultPrintsKey(t *testing.T) {
	h := newHarness(t)
	r := h.run("run", "echo", "hello")
	assert.NotEmpty(t, r.stderr)
}
