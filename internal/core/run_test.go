package core

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"job/internal/db"
	"job/internal/model"
	"job/internal/permissions"
)

func setupRun(t *testing.T) (*db.DB, string) {
	t.Helper()
	stateDir := t.TempDir()
	store, err := db.Open(filepath.Join(stateDir, "job.db"))
	require.NoError(t, err)
	t.Cleanup(func() { store.Close() })
	return store, stateDir
}

func TestForegroundSuccess(t *testing.T) {
	store, stateDir := setupRun(t)

	exitCode, err := CreateAndRunForeground(store, stateDir, []string{"echo", "hello"}, RunOptions{})
	require.NoError(t, err)
	assert.Equal(t, 0, exitCode)

	key, err := store.GetLastKeyForContext("")
	require.NoError(t, err)
	require.NotEmpty(t, key)

	j, err := store.Get(key)
	require.NoError(t, err)
	assert.Equal(t, model.StatusCompleted, j.Status)
	assert.Equal(t, model.ReasonExited, j.Reason)
	require.NotNil(t, j.ExitCode)
	assert.Equal(t, 0, *j.ExitCode)
	assert.NotNil(t, j.StartedAt)
	assert.NotNil(t, j.StoppedAt)
}

func TestForegroundNonZeroExit(t *testing.T) {
	store, stateDir := setupRun(t)

	exitCode, err := CreateAndRunForeground(store, stateDir, []string{"false"}, RunOptions{})
	require.NoError(t, err) // non-zero exit is not an infra error
	assert.Equal(t, 1, exitCode)

	key, _ := store.GetLastKeyForContext("")
	j, err := store.Get(key)
	require.NoError(t, err)
	assert.Equal(t, model.StatusCompleted, j.Status)
	require.NotNil(t, j.ExitCode)
	assert.Equal(t, 1, *j.ExitCode)
}

func TestForegroundLogFile(t *testing.T) {
	store, stateDir := setupRun(t)

	_, err := CreateAndRunForeground(store, stateDir, []string{"echo", "hello from job"}, RunOptions{})
	require.NoError(t, err)

	key, _ := store.GetLastKeyForContext("")
	j, err := store.Get(key)
	require.NoError(t, err)

	content, err := os.ReadFile(j.LogFile)
	require.NoError(t, err)
	assert.Contains(t, string(content), "hello from job")
	info, err := os.Stat(j.LogFile)
	require.NoError(t, err)
	assert.Equal(t, permissions.FileMode, info.Mode().Perm())
}

func TestForegroundAlias(t *testing.T) {
	store, stateDir := setupRun(t)

	_, err := CreateAndRunForeground(store, stateDir, []string{"echo", "hi"}, RunOptions{Alias: "myalias"})
	require.NoError(t, err)

	key, _ := store.GetLastKeyForContext("")
	j, err := store.Get(key)
	require.NoError(t, err)
	assert.Equal(t, "myalias", j.Alias)
}

func TestForegroundAutomatedDoesNotBecomeDot(t *testing.T) {
	store, stateDir := setupRun(t)

	_, err := CreateAndRunForeground(store, stateDir, []string{"echo", "human"}, RunOptions{})
	require.NoError(t, err)
	humanKey, err := store.GetLastKeyForContext("")
	require.NoError(t, err)

	_, err = CreateAndRunForeground(store, stateDir, []string{"echo", "automated"}, RunOptions{Automated: true})
	require.NoError(t, err)

	key, err := store.GetLastKeyForContext("")
	require.NoError(t, err)
	assert.Equal(t, humanKey, key, "automated job must not become the recency target")
}

func TestForegroundRecordsMetadata(t *testing.T) {
	store, stateDir := setupRun(t)

	_, err := CreateAndRunForeground(store, stateDir, []string{"echo", "hi"}, RunOptions{})
	require.NoError(t, err)

	key, _ := store.GetLastKeyForContext("")
	j, err := store.Get(key)
	require.NoError(t, err)
	assert.NotEmpty(t, j.Hostname)
	assert.NotEmpty(t, j.Username)
	assert.NotEmpty(t, j.WorkDir)
	assert.Equal(t, []string{"echo", "hi"}, j.Command)
}

func TestForegroundCanBeStopped(t *testing.T) {
	store, stateDir := setupRun(t)

	result := make(chan error, 1)
	go func() {
		_, err := CreateAndRunForeground(store, stateDir, []string{"sleep", "60"}, RunOptions{})
		result <- err
	}()

	var running *model.Job
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		key, err := store.GetLastKeyForContext("")
		if err == nil && key != "" {
			j, getErr := store.Get(key)
			if getErr == nil && j.Status == model.StatusRunning {
				running = j
				break
			}
		}
		time.Sleep(20 * time.Millisecond)
	}
	require.NotNil(t, running, "foreground job did not reach running state")
	assert.NotEqual(t, os.Getpid(), running.PID, "PID must identify the command, not the caller")
	assert.NotZero(t, running.PGID)

	require.NoError(t, StopJob(store, running.Key))

	select {
	case err := <-result:
		require.NoError(t, err)
	case <-time.After(5 * time.Second):
		t.Fatal("foreground command continued running after StopJob")
	}

	got, err := store.Get(running.Key)
	require.NoError(t, err)
	assert.Equal(t, model.StatusCompleted, got.Status)
	assert.Equal(t, model.ReasonStopped, got.Reason)
	assert.Zero(t, got.PID)
	assert.Zero(t, got.PGID)
}
