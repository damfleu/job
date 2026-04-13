package integration

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"job/internal/model"
)

func TestDepAliasResolvesToMostRecentJob(t *testing.T) {
	h := newHarness(t)

	// old completed job with alias "shared-key"
	h.run("-f", "-k", "shared-key", "--", "echo", "first run")

	// new running job with the same alias
	h.run("-v", "-k", "shared-key", "--", "sleep", "2")

	// dep should block on the NEW running job, not the old completed one
	r := h.run("-v", "-a", "shared-key", "--", "echo", "child")
	childKey := strings.TrimSpace(r.stderr)
	require.NotEmpty(t, childKey)

	h.waitFor(childKey, model.StatusBlocked)
	j, err := h.db.Get(childKey)
	require.NoError(t, err)
	assert.Equal(t, model.StatusBlocked, j.Status, "child should be blocked on the running job, not the old completed one")
}

func TestDepBlocksUntilDepCompletes(t *testing.T) {
	h := newHarness(t)

	r1 := h.run("-v", "-k", "dep-job", "--", "sleep", "2")
	depKey := strings.TrimSpace(r1.stderr)
	require.NotEmpty(t, depKey)

	r2 := h.run("-v", "-a", "dep-job", "--", "echo", "child")
	childKey := strings.TrimSpace(r2.stderr)
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

	r := h.run("-v", "-f", "--", "echo", "dep job")
	// stderr has "key running\nkey done\n", extract first word
	depKey := strings.Fields(strings.TrimSpace(r.stderr))[0]

	r2 := h.run("-v", "-a", depKey, "--", "echo", "dependent job")
	childKey := strings.TrimSpace(r2.stderr)
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

	r := h.run("-v", "-f", "--", "false")
	depKey := strings.Fields(strings.TrimSpace(r.stderr))[0]

	r2 := h.run("-v", "-A", depKey, "--", "echo", "should not run")
	childKey := strings.TrimSpace(r2.stderr)
	require.NotEmpty(t, childKey)

	h.waitFor(childKey, model.StatusCompleted)

	j, err := h.db.Get(childKey)
	require.NoError(t, err)
	assert.Equal(t, model.StatusCompleted, j.Status)
	assert.Equal(t, model.ReasonDepFailed, j.Reason)
	assert.Nil(t, j.ExitCode)
}

func TestDepMixedOrder(t *testing.T) {
	h := newHarness(t)

	r1 := h.run("-v", "-f", "--", "echo", "dep-a")
	keyA := strings.Fields(strings.TrimSpace(r1.stderr))[0]

	r2 := h.run("-v", "-f", "--", "echo", "dep-b")
	keyB := strings.Fields(strings.TrimSpace(r2.stderr))[0]

	r3 := h.run("-v", "-f", "--", "echo", "dep-c")
	keyC := strings.Fields(strings.TrimSpace(r3.stderr))[0]

	// interleaved: -A, -a, -A — order must be preserved
	r4 := h.run("-v", "-A", keyA, "-a", keyB, "-A", keyC, "--", "echo", "child")
	childKey := strings.TrimSpace(r4.stderr)
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
