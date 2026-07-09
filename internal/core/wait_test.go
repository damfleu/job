package core

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"job/internal/model"
)

func TestWaitForCompletionAlreadyDone(t *testing.T) {
	store, _ := setupRun(t)
	completedJob(t, store, "dep1", 0)
	completedJob(t, store, "dep2", 3)

	jobs, err := WaitForCompletion(store, []string{"dep1", "dep2"}, time.Millisecond)
	require.NoError(t, err)
	require.Len(t, jobs, 2)
	assert.Equal(t, "dep1", jobs[0].Key)
	assert.Equal(t, 0, *jobs[0].ExitCode)
	assert.Equal(t, "dep2", jobs[1].Key)
	assert.Equal(t, 3, *jobs[1].ExitCode)
}

func TestWaitForCompletionPolls(t *testing.T) {
	store, _ := setupRun(t)
	now := time.Now().UTC()
	pending := &model.Job{
		Key:       "pending",
		Command:   []string{"true"},
		WorkDir:   "/tmp",
		LogFile:   "/tmp/x.log",
		Status:    model.StatusRunning,
		CreatedAt: now,
	}
	require.NoError(t, store.Insert(pending))

	go func() {
		time.Sleep(20 * time.Millisecond)
		pending.Status = model.StatusCompleted
		pending.Reason = model.ReasonExited
		rc := 0
		pending.ExitCode = &rc
		_ = store.Update(pending)
	}()

	jobs, err := WaitForCompletion(store, []string{"pending"}, 5*time.Millisecond)
	require.NoError(t, err)
	require.Len(t, jobs, 1)
	assert.Equal(t, model.StatusCompleted, jobs[0].Status)
	assert.Equal(t, 0, *jobs[0].ExitCode)
}
