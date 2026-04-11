package core

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"job/internal/model"
)

// completedJob inserts a completed job with the given exit code.
func completedJob(t *testing.T, store interface {
	Insert(*model.Job) error
}, key string, exitCode int) {
	t.Helper()
	now := time.Now().UTC()
	j := &model.Job{
		Key:       key,
		Command:   []string{"true"},
		WorkDir:   "/tmp",
		LogFile:   "/tmp/x.log",
		Status:    model.StatusCompleted,
		Reason:    model.ReasonExited,
		ExitCode:  &exitCode,
		CreatedAt: now,
		StoppedAt: &now,
	}
	require.NoError(t, store.Insert(j))
}

func TestWaitForDepsAfterAny(t *testing.T) {
	store, stateDir := setupRun(t)
	completedJob(t, store, "dep1", 0)
	completedJob(t, store, "dep2", 1) // non-zero but DepAfter so OK

	j := &model.Job{
		Key: "child",
		Deps: []model.Dep{
			{Key: "dep1", Kind: model.DepAfter},
			{Key: "dep2", Kind: model.DepAfter},
		},
	}
	_ = stateDir
	assert.NoError(t, WaitForDeps(store, j))
}

func TestWaitForDepsAfterSuccessOK(t *testing.T) {
	store, stateDir := setupRun(t)
	completedJob(t, store, "dep1", 0)

	j := &model.Job{
		Key:  "child",
		Deps: []model.Dep{{Key: "dep1", Kind: model.DepAfterSuccess}},
	}
	_ = stateDir
	assert.NoError(t, WaitForDeps(store, j))
}

func TestWaitForDepsAfterSuccessFails(t *testing.T) {
	store, stateDir := setupRun(t)
	completedJob(t, store, "dep1", 1) // non-zero exit

	j := &model.Job{
		Key:  "child",
		Deps: []model.Dep{{Key: "dep1", Kind: model.DepAfterSuccess}},
	}
	_ = stateDir
	assert.ErrorIs(t, WaitForDeps(store, j), ErrDepFailed)
}

func TestWaitForDepsNoDeps(t *testing.T) {
	store, stateDir := setupRun(t)
	j := &model.Job{Key: "child"}
	_ = stateDir
	assert.NoError(t, WaitForDeps(store, j))
}
