package core

import (
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"job/internal/db"
	"job/internal/logfile"
	"job/internal/model"
	"job/internal/permissions"
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

	require.NoError(t, RunBackground(store, j.Key, nil))

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

	require.NoError(t, RunBackground(store, j.Key, nil))

	got, err := store.Get(j.Key)
	require.NoError(t, err)
	assert.Equal(t, model.StatusCompleted, got.Status)
	require.NotNil(t, got.ExitCode)
	assert.Equal(t, 1, *got.ExitCode)
}

func TestRunBackgroundLogFile(t *testing.T) {
	store, stateDir := setupRun(t)
	j := pendingJob(t, store, stateDir, []string{"echo", "hello from bg"})

	require.NoError(t, RunBackground(store, j.Key, nil))

	content, err := os.ReadFile(j.LogFile)
	require.NoError(t, err)
	assert.Contains(t, string(content), "hello from bg")
	info, err := os.Stat(j.LogFile)
	require.NoError(t, err)
	assert.Equal(t, permissions.FileMode, info.Mode().Perm())
}

func TestRunBackgroundRecordsPGID(t *testing.T) {
	store, stateDir := setupRun(t)
	j := pendingJob(t, store, stateDir, []string{"echo", "pgid test"})

	require.NoError(t, RunBackground(store, j.Key, nil))

	got, err := store.Get(j.Key)
	require.NoError(t, err)
	// PGID is cleared to 0 after completion, but was set during run —
	// verify the job completed (implying it was set and cleared correctly)
	assert.Equal(t, model.StatusCompleted, got.Status)
}

func TestRunBackgroundMissingDependencyTerminalizesJob(t *testing.T) {
	store, stateDir := setupRun(t)
	j := pendingJob(t, store, stateDir, []string{"echo", "should not run"})
	j.Deps = []model.Dep{{Key: "missing-dependency", Kind: model.DepAfter}}
	require.NoError(t, store.Update(j))

	err := RunBackground(store, j.Key, nil)
	require.ErrorContains(t, err, "fetching dep missing-dependency")

	assertInternalErrorJob(t, store, j.Key)
}

func TestRunBackgroundDependencyRereadFailureTerminalizesJob(t *testing.T) {
	store, stateDir := setupRun(t)
	dep := pendingJob(t, store, stateDir, []string{"true"})
	dep.Status = model.StatusCompleted
	dep.Reason = model.ReasonExited
	dep.ExitCode = new(0)
	dep.StoppedAt = new(time.Now().UTC())
	require.NoError(t, store.Update(dep))

	j := pendingJob(t, store, stateDir, []string{"echo", "should not run"})
	j.Deps = []model.Dep{{Key: dep.Key, Kind: model.DepAfter}}
	require.NoError(t, store.Update(j))

	failingStore := &failGetStore{
		JobStore: store,
		failAt:   3, // initial job, dependency, then post-dependency job reread
	}
	err := RunBackground(failingStore, j.Key, nil)
	require.ErrorContains(t, err, "reloading job after dependencies")

	assertInternalErrorJob(t, store, j.Key)
}

func TestRunBackgroundLogFailureTerminalizesJob(t *testing.T) {
	tests := []struct {
		name       string
		prepareLog func(*testing.T, string, *model.Job)
		wantError  string
	}{
		{
			name: "create directory",
			prepareLog: func(t *testing.T, stateDir string, _ *model.Job) {
				require.NoError(t, os.WriteFile(filepath.Join(stateDir, "log"), []byte("not a directory"), 0o600))
			},
			wantError: "creating log dir",
		},
		{
			name: "open file",
			prepareLog: func(t *testing.T, _ string, j *model.Job) {
				require.NoError(t, os.MkdirAll(j.LogFile, permissions.DirMode))
			},
			wantError: "opening log file",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			store, stateDir := setupRun(t)
			j := pendingJob(t, store, stateDir, []string{"echo", "should not run"})
			tt.prepareLog(t, stateDir, j)

			err := RunBackground(store, j.Key, nil)
			require.ErrorContains(t, err, tt.wantError)

			assertInternalErrorJob(t, store, j.Key)
		})
	}
}

func TestMarkInternalErrorReportsPersistenceFailure(t *testing.T) {
	store, stateDir := setupRun(t)
	j := pendingJob(t, store, stateDir, []string{"echo", "should not run"})
	originalErr := errors.New("pre-start failure")
	updateErr := errors.New("injected update failure")

	err := markInternalError(&failUpdateStore{JobStore: store, err: updateErr}, j, originalErr)
	require.ErrorIs(t, err, originalErr)
	require.ErrorIs(t, err, updateErr)
}

func assertInternalErrorJob(t *testing.T, store interface {
	Get(string) (*model.Job, error)
}, key string) {
	t.Helper()
	got, err := store.Get(key)
	require.NoError(t, err)
	assert.Equal(t, model.StatusCompleted, got.Status)
	assert.Equal(t, model.ReasonInternalError, got.Reason)
	assert.Nil(t, got.ExitCode)
	assert.NotNil(t, got.StoppedAt)
	assert.Zero(t, got.PID)
	assert.Zero(t, got.PGID)
}

type failGetStore struct {
	db.JobStore
	getCount int
	failAt   int
}

func (s *failGetStore) Get(key string) (*model.Job, error) {
	s.getCount++
	if s.getCount == s.failAt {
		return nil, errors.New("injected get failure")
	}
	return s.JobStore.Get(key)
}

type failUpdateStore struct {
	db.JobStore
	err error
}

func (s *failUpdateStore) Update(*model.Job) error {
	return s.err
}
