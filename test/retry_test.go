package integration

import (
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"job/internal/model"
)

func TestRetryBasic(t *testing.T) {
	h := newHarness(t)

	h.run("run", "-f", "echo", "original")
	orig := h.lastJob()
	require.Equal(t, model.StatusCompleted, orig.Status)

	r := h.run("retry", "-v", orig.Key)
	assert.Equal(t, 0, r.exitCode)
	retryKey := strings.TrimSpace(r.stderr)
	require.NotEmpty(t, retryKey)
	assert.NotEqual(t, orig.Key, retryKey, "retry should produce a new key")

	h.waitFor(retryKey, model.StatusCompleted)

	retried, err := h.db.Get(retryKey)
	require.NoError(t, err)
	assert.Equal(t, orig.Command, retried.Command)
	assert.Equal(t, model.StatusCompleted, retried.Status)
	assert.Equal(t, model.ReasonExited, retried.Reason)
	require.NotNil(t, retried.ExitCode)
	assert.Equal(t, 0, *retried.ExitCode)
}

func TestRetryPreservesWorkDir(t *testing.T) {
	h := newHarness(t)
	origDir, err := filepath.EvalSymlinks(t.TempDir())
	require.NoError(t, err)

	h.runFrom(origDir, "run", "-f", "echo", "in-dir")
	orig := h.lastJob()
	assert.Equal(t, origDir, orig.WorkDir)

	// retry from a different directory — workdir should match original
	otherDir := t.TempDir()
	r := h.runFrom(otherDir, "retry", "-v", orig.Key)
	retryKey := strings.TrimSpace(r.stderr)
	require.NotEmpty(t, retryKey)

	h.waitFor(retryKey, model.StatusCompleted)

	retried, err := h.db.Get(retryKey)
	require.NoError(t, err)
	assert.Equal(t, origDir, retried.WorkDir, "retry should use the original job's working directory")
}

func TestRetryForeground(t *testing.T) {
	h := newHarness(t)
	h.run("run", "-f", "echo", "fg retry")
	orig := h.lastJob()

	r := h.run("retry", "-f", orig.Key)
	assert.Equal(t, 0, r.exitCode)
	assert.Contains(t, r.stdout, "fg retry")

	retried := h.lastJob()
	assert.NotEqual(t, orig.Key, retried.Key)
	assert.Equal(t, model.StatusCompleted, retried.Status)
}

func TestRetryNonZeroExit(t *testing.T) {
	h := newHarness(t)
	h.run("run", "-f", "false")
	orig := h.lastJob()

	r := h.run("retry", "-f", orig.Key)
	assert.Equal(t, 1, r.exitCode)

	retried := h.lastJob()
	assert.NotEqual(t, orig.Key, retried.Key)
	require.NotNil(t, retried.ExitCode)
	assert.Equal(t, 1, *retried.ExitCode)
}

func TestRetryWithAlias(t *testing.T) {
	h := newHarness(t)
	h.run("run", "-f", "echo", "aliased retry")
	orig := h.lastJob()

	h.run("retry", "-f", "-k", "retry-alias", orig.Key)

	retried := h.lastJob()
	assert.Equal(t, "retry-alias", retried.Alias)
}

func TestRetryWithDep(t *testing.T) {
	h := newHarness(t)

	h.run("run", "-f", "echo", "original")
	orig := h.lastJob()

	r := h.run("run", "-v", "sleep", "5")
	depKey := strings.TrimSpace(r.stderr)
	h.waitFor(depKey, model.StatusRunning)

	r2 := h.run("retry", "-v", "-a", depKey, orig.Key)
	retryKey := strings.Fields(r2.stderr)[0]
	require.NotEmpty(t, retryKey)

	time.Sleep(200 * time.Millisecond)
	retried, err := h.db.Get(retryKey)
	require.NoError(t, err)
	assert.Equal(t, model.StatusBlocked, retried.Status, "retry should be blocked on its dep")

	h.run("stop", depKey)
	h.waitFor(retryKey, model.StatusCompleted)
}

func TestRetryCwd(t *testing.T) {
	h := newHarness(t)
	origDir, err := filepath.EvalSymlinks(t.TempDir())
	require.NoError(t, err)

	h.runFrom(origDir, "run", "-f", "echo", "original")
	orig := h.lastJob()

	otherDir, err := filepath.EvalSymlinks(t.TempDir())
	require.NoError(t, err)

	r := h.runFrom(otherDir, "retry", "-v", "--cwd", orig.Key)
	retryKey := strings.TrimSpace(r.stderr)
	require.NotEmpty(t, retryKey)
	h.waitFor(retryKey, model.StatusCompleted)

	retried, err := h.db.Get(retryKey)
	require.NoError(t, err)
	assert.Equal(t, otherDir, retried.WorkDir)
}

func TestRetryCwdExplicit(t *testing.T) {
	h := newHarness(t)
	origDir, err := filepath.EvalSymlinks(t.TempDir())
	require.NoError(t, err)

	h.runFrom(origDir, "run", "-f", "echo", "original")
	orig := h.lastJob()

	explicitDir, err := filepath.EvalSymlinks(t.TempDir())
	require.NoError(t, err)

	r := h.run("retry", "-v", "--cwd="+explicitDir, orig.Key)
	retryKey := strings.TrimSpace(r.stderr)
	require.NotEmpty(t, retryKey)
	h.waitFor(retryKey, model.StatusCompleted)

	retried, err := h.db.Get(retryKey)
	require.NoError(t, err)
	assert.Equal(t, explicitDir, retried.WorkDir)
}

func TestRetryRequiresCompleted(t *testing.T) {
	h := newHarness(t)

	r := h.run("run", "-v", "sleep", "5")
	key := strings.TrimSpace(r.stderr)
	h.waitFor(key, model.StatusRunning)

	r2 := h.run("retry", key)
	assert.NotEqual(t, 0, r2.exitCode, "retrying a running job should fail")

	h.run("stop", key)
}
