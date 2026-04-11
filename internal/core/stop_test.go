package core

import (
	"os/exec"
	"syscall"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"job/internal/logfile"
	"job/internal/model"
)

// startSleepJob starts a real sleep process, inserts a running job record for
// it, and returns the job key.
func startSleepJob(t *testing.T, store interface {
	Insert(*model.Job) error
	Update(*model.Job) error
}, stateDir string) (string, *exec.Cmd) {
	t.Helper()

	cmd := exec.Command("sleep", "60")
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	require.NoError(t, cmd.Start())

	pid := cmd.Process.Pid
	now := time.Now().UTC()
	key := model.GenerateKey("sleep")
	j := &model.Job{
		Key:       key,
		Command:   []string{"sleep", "60"},
		WorkDir:   t.TempDir(),
		LogFile:   logfile.Path(stateDir, key),
		Status:    model.StatusRunning,
		PID:       pid,
		PGID:      pid,
		CreatedAt: now,
		StartedAt: &now,
	}
	require.NoError(t, store.Insert(j))

	t.Cleanup(func() {
		// best-effort cleanup if test fails before stop
		_ = syscall.Kill(-pid, syscall.SIGKILL)
		_ = cmd.Wait()
	})

	return key, cmd
}

func TestStopRunningJob(t *testing.T) {
	store, stateDir := setupRun(t)
	key, cmd := startSleepJob(t, store, stateDir)

	// Reap the child so it doesn't linger as a zombie after SIGTERM.
	// In real usage __exec calls Wait itself; here the test binary is the parent.
	go cmd.Wait()

	require.NoError(t, StopJob(store, key))

	// process should be gone
	err := syscall.Kill(cmd.Process.Pid, 0)
	assert.ErrorIs(t, err, syscall.ESRCH)

	j, err := store.Get(key)
	require.NoError(t, err)
	assert.Equal(t, model.StatusCompleted, j.Status)
	assert.Equal(t, model.ReasonStopped, j.Reason)
	assert.NotNil(t, j.StoppedAt)
}

func TestStopAlreadyCompleted(t *testing.T) {
	store, stateDir := setupRun(t)

	_, err := CreateAndRunForeground(store, stateDir, []string{"true"}, RunOptions{})
	require.NoError(t, err)

	key, _ := store.GetLastKey()
	err = StopJob(store, key)
	assert.Error(t, err)
}
