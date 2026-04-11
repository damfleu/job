package core

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"job/internal/db"
	"job/internal/model"
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

	key, err := store.GetLastKey()
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

	key, _ := store.GetLastKey()
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

	key, _ := store.GetLastKey()
	j, err := store.Get(key)
	require.NoError(t, err)

	content, err := os.ReadFile(j.LogFile)
	require.NoError(t, err)
	assert.Contains(t, string(content), "hello from job")
}

func TestForegroundAlias(t *testing.T) {
	store, stateDir := setupRun(t)

	_, err := CreateAndRunForeground(store, stateDir, []string{"echo", "hi"}, RunOptions{Alias: "myalias"})
	require.NoError(t, err)

	key, _ := store.GetLastKey()
	j, err := store.Get(key)
	require.NoError(t, err)
	assert.Equal(t, "myalias", j.Alias)
}

func TestForegroundRecordsMetadata(t *testing.T) {
	store, stateDir := setupRun(t)

	_, err := CreateAndRunForeground(store, stateDir, []string{"echo", "hi"}, RunOptions{})
	require.NoError(t, err)

	key, _ := store.GetLastKey()
	j, err := store.Get(key)
	require.NoError(t, err)
	assert.NotEmpty(t, j.Hostname)
	assert.NotEmpty(t, j.Username)
	assert.NotEmpty(t, j.WorkDir)
	assert.Equal(t, []string{"echo", "hi"}, j.Command)
}
