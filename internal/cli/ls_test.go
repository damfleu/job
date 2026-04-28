package cli

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"job/internal/db"
	"job/internal/model"
)

func openTestDB(t *testing.T) *db.DB {
	t.Helper()
	d, err := db.Open(":memory:")
	require.NoError(t, err)
	t.Cleanup(func() { d.Close() })
	return d
}

func makeTestJob(key string, status model.Status, deps ...string) *model.Job {
	j := &model.Job{
		Key:       key,
		Command:   []string{"echo", key},
		WorkDir:   "/tmp",
		LogFile:   "/tmp/job.log",
		Status:    status,
		CreatedAt: time.Now().UTC().Truncate(time.Millisecond),
	}
	for _, d := range deps {
		j.Deps = append(j.Deps, model.Dep{Key: d, Kind: model.DepAfter})
	}
	if status == model.StatusCompleted {
		now := time.Now().UTC().Truncate(time.Millisecond)
		j.StoppedAt = &now
		j.Reason = model.ReasonExited
		rc := 0
		j.ExitCode = &rc
	}
	return j
}

func jobKeys(jobs []*model.Job) []string {
	keys := make([]string, len(jobs))
	for i, j := range jobs {
		keys[i] = j.Key
	}
	return keys
}

func TestJobStatusText(t *testing.T) {
	rc0 := 0
	rc1 := 1

	tests := []struct {
		name string
		job  *model.Job
		want string
	}{
		{"running", &model.Job{Status: model.StatusRunning}, "running"},
		{"blocked", &model.Job{Status: model.StatusBlocked}, "blocked"},
		{"pending", &model.Job{Status: model.StatusPending}, "pending"},
		{"exited rc=0", &model.Job{Status: model.StatusCompleted, Reason: model.ReasonExited, ExitCode: &rc0}, "completed"},
		{"exited rc=1", &model.Job{Status: model.StatusCompleted, Reason: model.ReasonExited, ExitCode: &rc1}, "failed"},
		{"exited no rc", &model.Job{Status: model.StatusCompleted, Reason: model.ReasonExited}, "failed"},
		{"stopped", &model.Job{Status: model.StatusCompleted, Reason: model.ReasonStopped}, "stopped"},
		{"dep_failed", &model.Job{Status: model.StatusCompleted, Reason: model.ReasonDepFailed}, "dep_failed"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assert.Equal(t, tt.want, jobStatusText(tt.job))
		})
	}
}

func TestExpandDepsNoDeps(t *testing.T) {
	d := openTestDB(t)
	running := makeTestJob("run1", model.StatusRunning)
	require.NoError(t, d.Insert(running))

	got, err := expandDeps(d, []*model.Job{running})
	require.NoError(t, err)
	assert.ElementsMatch(t, []string{"run1"}, jobKeys(got))
}

func TestExpandDepsCompletedDepIncluded(t *testing.T) {
	d := openTestDB(t)

	dep := makeTestJob("dep1", model.StatusCompleted)
	child := makeTestJob("child1", model.StatusRunning, "dep1")
	require.NoError(t, d.Insert(dep))
	require.NoError(t, d.Insert(child))

	got, err := expandDeps(d, []*model.Job{child})
	require.NoError(t, err)
	assert.ElementsMatch(t, []string{"dep1", "child1"}, jobKeys(got))
}

func TestExpandDepsTransitive(t *testing.T) {
	d := openTestDB(t)

	a := makeTestJob("a", model.StatusCompleted)
	b := makeTestJob("b", model.StatusCompleted, "a")
	c := makeTestJob("c", model.StatusRunning, "b")
	require.NoError(t, d.Insert(a))
	require.NoError(t, d.Insert(b))
	require.NoError(t, d.Insert(c))

	got, err := expandDeps(d, []*model.Job{c})
	require.NoError(t, err)
	assert.ElementsMatch(t, []string{"a", "b", "c"}, jobKeys(got))
}

func TestExpandDepsMissingDepSkipped(t *testing.T) {
	d := openTestDB(t)

	child := makeTestJob("child1", model.StatusRunning, "ghost")
	require.NoError(t, d.Insert(child))

	got, err := expandDeps(d, []*model.Job{child})
	require.NoError(t, err)
	// ghost is not in DB, so only child1 returned
	assert.ElementsMatch(t, []string{"child1"}, jobKeys(got))
}

func TestExpandDepsSharedDepFetchedOnce(t *testing.T) {
	d := openTestDB(t)

	dep := makeTestJob("shared", model.StatusCompleted)
	c1 := makeTestJob("c1", model.StatusRunning, "shared")
	c2 := makeTestJob("c2", model.StatusBlocked, "shared")
	require.NoError(t, d.Insert(dep))
	require.NoError(t, d.Insert(c1))
	require.NoError(t, d.Insert(c2))

	got, err := expandDeps(d, []*model.Job{c1, c2})
	require.NoError(t, err)
	assert.ElementsMatch(t, []string{"shared", "c1", "c2"}, jobKeys(got))
}
