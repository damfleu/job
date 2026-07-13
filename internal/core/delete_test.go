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
)

func completedJobWithLog(t *testing.T, store *db.DB, logFile string) *model.Job {
	t.Helper()
	key := model.GenerateKey("echo")
	now := time.Now().UTC()
	j := &model.Job{
		Key:       key,
		Command:   []string{"echo", "hi"},
		WorkDir:   t.TempDir(),
		LogFile:   logFile,
		Status:    model.StatusCompleted,
		Reason:    model.ReasonExited,
		ExitCode:  new(0),
		CreatedAt: now,
		StartedAt: &now,
		StoppedAt: &now,
	}
	require.NoError(t, store.Insert(j))
	return j
}

func TestDeleteJobRemovesDBRecord(t *testing.T) {
	store, _ := setupRun(t)
	j := completedJobWithLog(t, store, "")

	require.NoError(t, DeleteJob(store, j))

	_, err := store.Get(j.Key)
	assert.ErrorIs(t, err, db.ErrNotFound)
}

func TestDeleteJobRemovesLogFile(t *testing.T) {
	store, _ := setupRun(t)

	logFile := filepath.Join(t.TempDir(), "job.log")
	require.NoError(t, os.WriteFile(logFile, []byte("output"), 0o644))

	j := completedJobWithLog(t, store, logFile)
	require.NoError(t, DeleteJob(store, j))

	_, err := os.Stat(logFile)
	assert.True(t, os.IsNotExist(err), "log file should be deleted")
}

func TestDeleteJobMissingLogFileIsOK(t *testing.T) {
	store, _ := setupRun(t)
	j := completedJobWithLog(t, store, "/nonexistent/path/job.log")

	require.NoError(t, DeleteJob(store, j))

	_, err := store.Get(j.Key)
	assert.ErrorIs(t, err, db.ErrNotFound)
}

func TestDeleteJobRejectsIncompleteJobs(t *testing.T) {
	for _, status := range []model.Status{
		model.StatusPending,
		model.StatusBlocked,
		model.StatusRunning,
	} {
		t.Run(string(status), func(t *testing.T) {
			store, _ := setupRun(t)
			j := completedJobWithLog(t, store, "")
			j.Status = status

			err := DeleteJob(store, j)
			require.Error(t, err)
			assert.Contains(t, err.Error(), "is not completed")

			_, getErr := store.Get(j.Key)
			assert.NoError(t, getErr, "job should remain after rejected deletion")
		})
	}
}
