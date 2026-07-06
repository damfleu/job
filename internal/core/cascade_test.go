package core

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"job/internal/model"
)

// depFailedJob inserts a job that completed because one of its deps failed.
func depFailedJob(t *testing.T, store interface {
	Insert(*model.Job) error
}, key string, deps []model.Dep) {
	t.Helper()
	now := time.Now().UTC()
	j := &model.Job{
		Key:       key,
		Command:   []string{"echo", key},
		WorkDir:   "/tmp",
		LogFile:   "/tmp/x.log",
		Status:    model.StatusCompleted,
		Reason:    model.ReasonDepFailed,
		CreatedAt: now,
		StoppedAt: &now,
		Deps:      deps,
	}
	require.NoError(t, store.Insert(j))
}

func TestExpandDependentCascadeChain(t *testing.T) {
	store, _ := setupRun(t)

	completedJob(t, store, "root", 1) // the job we're retrying, exited non-zero
	depFailedJob(t, store, "child", []model.Dep{{Key: "root", Kind: model.DepAfterSuccess}})
	depFailedJob(t, store, "grandchild", []model.Dep{{Key: "child", Kind: model.DepAfterSuccess}})

	steps, err := ExpandDependentCascade(store, "root")
	require.NoError(t, err)
	require.Len(t, steps, 3)

	// root first, then dependents in dependency order
	assert.Equal(t, "root", steps[0].OriginalKey)
	assert.Equal(t, "child", steps[1].OriginalKey)
	assert.Equal(t, "grandchild", steps[2].OriginalKey)

	// root doesn't carry over its own (external) deps
	assert.Empty(t, steps[0].Deps)
	require.Len(t, steps[1].Deps, 1)
	assert.Equal(t, "root", steps[1].Deps[0].Key)
	require.Len(t, steps[2].Deps, 1)
	assert.Equal(t, "child", steps[2].Deps[0].Key)
}

func TestExpandDependentCascadeIgnoresUnrelatedFailures(t *testing.T) {
	store, _ := setupRun(t)

	completedJob(t, store, "root", 1)
	completedJob(t, store, "other-root", 1)
	depFailedJob(t, store, "child", []model.Dep{{Key: "root", Kind: model.DepAfterSuccess}})
	depFailedJob(t, store, "unrelated-child", []model.Dep{{Key: "other-root", Kind: model.DepAfterSuccess}})

	steps, err := ExpandDependentCascade(store, "root")
	require.NoError(t, err)
	require.Len(t, steps, 2)
	keys := []string{steps[0].OriginalKey, steps[1].OriginalKey}
	assert.ElementsMatch(t, []string{"root", "child"}, keys)
}

func TestExpandDependentCascadePreservesExternalDep(t *testing.T) {
	store, _ := setupRun(t)

	completedJob(t, store, "root", 1)
	completedJob(t, store, "unrelated-succeeded", 0)
	// child depends on both the failed root and an unrelated, already-succeeded job.
	depFailedJob(t, store, "child", []model.Dep{
		{Key: "root", Kind: model.DepAfterSuccess},
		{Key: "unrelated-succeeded", Kind: model.DepAfterSuccess},
	})

	steps, err := ExpandDependentCascade(store, "root")
	require.NoError(t, err)
	require.Len(t, steps, 2)

	childStep := steps[1]
	require.Equal(t, "child", childStep.OriginalKey)
	require.Len(t, childStep.Deps, 2)
	assert.Contains(t, childStep.Deps, model.Dep{Key: "root", Kind: model.DepAfterSuccess})
	assert.Contains(t, childStep.Deps, model.Dep{Key: "unrelated-succeeded", Kind: model.DepAfterSuccess})
}

func TestExpandDependentCascadeNoDependents(t *testing.T) {
	store, _ := setupRun(t)
	completedJob(t, store, "root", 1)

	steps, err := ExpandDependentCascade(store, "root")
	require.NoError(t, err)
	require.Len(t, steps, 1)
	assert.Equal(t, "root", steps[0].OriginalKey)
	assert.Empty(t, steps[0].Deps)
}
