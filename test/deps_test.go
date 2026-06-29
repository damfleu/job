package integration

import (
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"job/internal/model"
)

func TestDepAliasResolvesToMostRecentJob(t *testing.T) {
	h := newHarness(t)

	// old completed job with alias "shared-key"
	h.run("run", "-f", "-k", "shared-key", "echo", "first run")

	// new running job with the same alias
	h.run("run", "-k", "shared-key", "sleep", "2")

	// dep should block on the NEW running job, not the old completed one
	r := h.run("run", "-a", "shared-key", "echo", "child")
	childKey := strings.Fields(r.stderr)[0]
	require.NotEmpty(t, childKey)

	h.waitFor(childKey, model.StatusBlocked)
	j, err := h.db.Get(childKey)
	require.NoError(t, err)
	assert.Equal(t, model.StatusBlocked, j.Status, "child should be blocked on the running job, not the old completed one")
}

func TestDepBlocksUntilDepCompletes(t *testing.T) {
	h := newHarness(t)

	r1 := h.run("run", "-k", "dep-job", "sleep", "2")
	depKey := strings.TrimSpace(r1.stderr)
	require.NotEmpty(t, depKey)

	r2 := h.run("run", "-a", "dep-job", "echo", "child")
	childKey := strings.Fields(r2.stderr)[0]
	require.NotEmpty(t, childKey)

	h.waitFor(childKey, model.StatusBlocked)
	j, err := h.db.Get(childKey)
	require.NoError(t, err)
	assert.Equal(t, model.StatusBlocked, j.Status, "child should be blocked while dep is running")

	h.waitFor(depKey, model.StatusCompleted)
	h.waitFor(childKey, model.StatusCompleted)

	j, err = h.db.Get(childKey)
	require.NoError(t, err)
	assert.Equal(t, model.ReasonExited, j.Reason)
}

func TestDepAfter(t *testing.T) {
	h := newHarness(t)

	r := h.run("run", "echo", "dep job")
	depKey := strings.TrimSpace(r.stderr)

	r2 := h.run("run", "-a", depKey, "echo", "dependent job")
	childKey := strings.Fields(r2.stderr)[0]
	require.NotEmpty(t, childKey)

	h.waitFor(childKey, model.StatusCompleted)

	j, err := h.db.Get(childKey)
	require.NoError(t, err)
	assert.Equal(t, model.StatusCompleted, j.Status)
	assert.Equal(t, model.ReasonExited, j.Reason)
	require.NotNil(t, j.ExitCode)
	assert.Equal(t, 0, *j.ExitCode)
}

func TestDepAfterSuccessFails(t *testing.T) {
	h := newHarness(t)

	r := h.run("run", "false")
	depKey := strings.TrimSpace(r.stderr)

	r2 := h.run("run", "-A", depKey, "echo", "should not run")
	childKey := strings.Fields(r2.stderr)[0]
	require.NotEmpty(t, childKey)

	h.waitFor(childKey, model.StatusCompleted)

	j, err := h.db.Get(childKey)
	require.NoError(t, err)
	assert.Equal(t, model.StatusCompleted, j.Status)
	assert.Equal(t, model.ReasonDepFailed, j.Reason)
	assert.Nil(t, j.ExitCode)
}

func TestStopBlockedJobDoesNotRunAfterDepCompletes(t *testing.T) {
	h := newHarness(t)

	r1 := h.run("run", "sleep", "2")
	depKey := strings.TrimSpace(r1.stderr)
	require.NotEmpty(t, depKey)
	h.waitFor(depKey, model.StatusRunning)

	r2 := h.run("run", "-A", depKey, "echo", "should not run")
	blockedKey := strings.Fields(r2.stderr)[0]
	require.NotEmpty(t, blockedKey)
	h.waitFor(blockedKey, model.StatusBlocked)

	h.run("stop", blockedKey)

	j, err := h.db.Get(blockedKey)
	require.NoError(t, err)
	require.Equal(t, model.ReasonStopped, j.Reason, "job should be stopped before dep completes")

	h.waitFor(depKey, model.StatusCompleted)
	time.Sleep(1500 * time.Millisecond) // poll interval is 1s; give the background process time to misbehave

	j, err = h.db.Get(blockedKey)
	require.NoError(t, err)
	assert.Equal(t, model.ReasonStopped, j.Reason, "stopped job must not run after dep completes")
}

func TestDepMixedOrder(t *testing.T) {
	h := newHarness(t)

	r1 := h.run("run", "echo", "dep-a")
	keyA := strings.TrimSpace(r1.stderr)

	r2 := h.run("run", "echo", "dep-b")
	keyB := strings.TrimSpace(r2.stderr)

	r3 := h.run("run", "echo", "dep-c")
	keyC := strings.TrimSpace(r3.stderr)

	// interleaved: -A, -a, -A — order must be preserved
	r4 := h.run("run", "-A", keyA, "-a", keyB, "-A", keyC, "echo", "child")
	childKey := strings.Fields(r4.stderr)[0]
	require.NotEmpty(t, childKey)

	h.waitFor(childKey, model.StatusCompleted)

	j, err := h.db.Get(childKey)
	require.NoError(t, err)
	assert.Equal(t, model.ReasonExited, j.Reason)
	require.Len(t, j.Deps, 3)
	assert.Equal(t, model.DepAfterSuccess, j.Deps[0].Kind)
	assert.Equal(t, model.DepAfter, j.Deps[1].Kind)
	assert.Equal(t, model.DepAfterSuccess, j.Deps[2].Kind)
}
