package core

import (
	"errors"
	"os/exec"
	"syscall"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"job/internal/logfile"
	"job/internal/model"
)

func runningJob(t *testing.T, store interface {
	Insert(*model.Job) error
}, stateDir string) *model.Job {
	t.Helper()
	key := model.GenerateKey("sleep")
	now := time.Now().UTC()
	j := &model.Job{
		Key:       key,
		Command:   []string{"sleep", "60"},
		WorkDir:   t.TempDir(),
		LogFile:   logfile.Path(stateDir, key),
		Status:    model.StatusRunning,
		PID:       99999, // fake — not a real process
		PGID:      99999,
		CreatedAt: now,
		StartedAt: &now,
	}
	require.NoError(t, store.Insert(j))
	return j
}

func TestStopRunningJob(t *testing.T) {
	store, stateDir := setupRun(t)
	j := runningJob(t, store, stateDir)

	// PGID 99999 almost certainly doesn't exist, so SIGTERM is a no-op.
	// We're testing the DB transition, not the signal delivery.
	require.NoError(t, StopJob(store, j.Key))

	got, err := store.Get(j.Key)
	require.NoError(t, err)
	assert.Equal(t, model.StatusCompleted, got.Status)
	assert.Equal(t, model.ReasonStopped, got.Reason)
	assert.NotNil(t, got.StoppedAt)
	assert.Equal(t, 0, got.PID)
}

func TestStopKillsProcess(t *testing.T) {
	store, stateDir := setupRun(t)

	cmd := exec.Command("sleep", "60")
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	require.NoError(t, cmd.Start())
	t.Cleanup(func() { cmd.Process.Kill(); cmd.Wait() }) //nolint:errcheck

	pid := cmd.Process.Pid
	pgid, err := syscall.Getpgid(pid)
	require.NoError(t, err)

	now := time.Now().UTC()
	key := model.GenerateKey("sleep")
	j := &model.Job{
		Key:       key,
		Command:   []string{"sleep", "60"},
		WorkDir:   t.TempDir(),
		LogFile:   logfile.Path(stateDir, key),
		Status:    model.StatusRunning,
		PID:       pid,
		PGID:      pgid,
		CreatedAt: now,
		StartedAt: &now,
	}
	require.NoError(t, store.Insert(j))

	require.NoError(t, StopJob(store, key))

	// process should be dead — Wait returns an error (signal kill)
	assert.Error(t, cmd.Wait(), "process should have been killed by StopJob")

	got, err := store.Get(key)
	require.NoError(t, err)
	assert.Equal(t, model.StatusCompleted, got.Status)
	assert.Equal(t, model.ReasonStopped, got.Reason)
	assert.NotNil(t, got.StoppedAt)
	assert.Equal(t, 0, got.PID)
}

func TestKillProcessGroupEscalatesAfterLeaderExits(t *testing.T) {
	// The group leader starts a child that inherits SIGTERM as ignored, then exits.
	// This leaves a live process group without a process whose PID equals the PGID.
	cmd := exec.Command("/bin/sh", "-c", `trap '' TERM; /bin/sh -c 'while :; do sleep 1; done' &`)
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	require.NoError(t, cmd.Start())

	pgid := cmd.Process.Pid
	t.Cleanup(func() { _ = syscall.Kill(-pgid, syscall.SIGKILL) })

	require.NoError(t, cmd.Wait())
	require.ErrorIs(t, syscall.Kill(pgid, 0), syscall.ESRCH, "group leader should be gone")
	require.NoError(t, syscall.Kill(-pgid, 0), "child should keep the process group alive")

	killProcessGroupWithGrace(pgid, 200*time.Millisecond)

	require.Eventually(t, func() bool {
		return errors.Is(syscall.Kill(-pgid, 0), syscall.ESRCH)
	}, 2*time.Second, 10*time.Millisecond, "SIGKILL should remove the remaining group members")
}

func TestStopBlockedJob(t *testing.T) {
	store, stateDir := setupRun(t)

	key := model.GenerateKey("sleep")
	now := time.Now().UTC()
	j := &model.Job{
		Key:       key,
		Command:   []string{"sleep", "60"},
		WorkDir:   t.TempDir(),
		LogFile:   logfile.Path(stateDir, key),
		Status:    model.StatusBlocked,
		CreatedAt: now,
	}
	require.NoError(t, store.Insert(j))

	require.NoError(t, StopJob(store, key))

	got, err := store.Get(key)
	require.NoError(t, err)
	assert.Equal(t, model.StatusCompleted, got.Status)
	assert.Equal(t, model.ReasonStopped, got.Reason)
	assert.NotNil(t, got.StoppedAt)
}

func TestStopAlreadyCompleted(t *testing.T) {
	store, stateDir := setupRun(t)

	_, err := CreateAndRunForeground(store, stateDir, []string{"true"}, RunOptions{})
	require.NoError(t, err)

	key, _ := store.GetLastKeyForContext("")
	assert.Error(t, StopJob(store, key))
}

func TestStopPendingJob(t *testing.T) {
	store, stateDir := setupRun(t)
	j := pendingJob(t, store, stateDir, []string{"sleep", "60"})

	require.NoError(t, StopJob(store, j.Key))

	got, err := store.Get(j.Key)
	require.NoError(t, err)
	assert.Equal(t, model.StatusCompleted, got.Status)
	assert.Equal(t, model.ReasonStopped, got.Reason)
}
