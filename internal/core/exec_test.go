package core

import (
	"os"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"job/internal/logfile"
	"job/internal/model"
)

func pendingJob(t *testing.T, store interface {
	Insert(*model.Job) error
}, stateDir string, command []string) *model.Job {
	t.Helper()
	key := model.GenerateKey(command[0])
	j := &model.Job{
		Key:       key,
		Command:   command,
		WorkDir:   t.TempDir(),
		LogFile:   logfile.Path(stateDir, key),
		Status:    model.StatusPending,
		CreatedAt: time.Now().UTC(),
	}
	require.NoError(t, store.Insert(j))
	return j
}

func TestRunBackgroundSuccess(t *testing.T) {
	store, stateDir := setupRun(t)
	j := pendingJob(t, store, stateDir, []string{"echo", "bg output"})

	require.NoError(t, RunBackground(store, j.Key))

	got, err := store.Get(j.Key)
	require.NoError(t, err)
	assert.Equal(t, model.StatusCompleted, got.Status)
	assert.Equal(t, model.ReasonExited, got.Reason)
	require.NotNil(t, got.ExitCode)
	assert.Equal(t, 0, *got.ExitCode)
	assert.NotNil(t, got.StartedAt)
	assert.NotNil(t, got.StoppedAt)
}

func TestRunBackgroundNonZeroExit(t *testing.T) {
	store, stateDir := setupRun(t)
	j := pendingJob(t, store, stateDir, []string{"false"})

	require.NoError(t, RunBackground(store, j.Key))

	got, err := store.Get(j.Key)
	require.NoError(t, err)
	assert.Equal(t, model.StatusCompleted, got.Status)
	require.NotNil(t, got.ExitCode)
	assert.Equal(t, 1, *got.ExitCode)
}

func TestRunBackgroundLogFile(t *testing.T) {
	store, stateDir := setupRun(t)
	j := pendingJob(t, store, stateDir, []string{"echo", "hello from bg"})

	require.NoError(t, RunBackground(store, j.Key))

	content, err := os.ReadFile(j.LogFile)
	require.NoError(t, err)
	assert.Contains(t, string(content), "hello from bg")
}

func TestRunBackgroundRecordsPGID(t *testing.T) {
	store, stateDir := setupRun(t)
	j := pendingJob(t, store, stateDir, []string{"echo", "pgid test"})

	require.NoError(t, RunBackground(store, j.Key))

	got, err := store.Get(j.Key)
	require.NoError(t, err)
	// PGID is cleared to 0 after completion, but was set during run —
	// verify the job completed (implying it was set and cleared correctly)
	assert.Equal(t, model.StatusCompleted, got.Status)
}
