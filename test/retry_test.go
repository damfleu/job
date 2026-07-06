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

	r := h.run("retry", orig.Key)
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
	r := h.runFrom(otherDir, "retry", orig.Key)
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

	r := h.run("run", "sleep", "5")
	depKey := strings.TrimSpace(r.stderr)
	h.waitFor(depKey, model.StatusRunning)

	r2 := h.run("retry", "-a", depKey, orig.Key)
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

	r := h.runFrom(otherDir, "retry", "--cwd", orig.Key)
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

	r := h.run("retry", "--cwd="+explicitDir, orig.Key)
	retryKey := strings.TrimSpace(r.stderr)
	require.NotEmpty(t, retryKey)
	h.waitFor(retryKey, model.StatusCompleted)

	retried, err := h.db.Get(retryKey)
	require.NoError(t, err)
	assert.Equal(t, explicitDir, retried.WorkDir)
}

func TestRetryRequiresCompleted(t *testing.T) {
	h := newHarness(t)

	r := h.run("run", "sleep", "5")
	key := strings.TrimSpace(r.stderr)
	h.waitFor(key, model.StatusRunning)

	r2 := h.run("retry", key)
	assert.NotEqual(t, 0, r2.exitCode, "retrying a running job should fail")

	h.run("stop", key)
}

func TestRetryCascadeChain(t *testing.T) {
	h := newHarness(t)

	// root fails, which cascades dep_failed down through child and grandchild.
	rootKey := runFg(h, "false")
	h.run("run", "-A", rootKey, "echo", "child")
	childKey := h.lastJob().Key
	h.run("run", "-A", childKey, "echo", "grandchild")
	grandchildKey := h.lastJob().Key

	h.waitFor(childKey, model.StatusCompleted)
	h.waitFor(grandchildKey, model.StatusCompleted)
	child, err := h.db.Get(childKey)
	require.NoError(t, err)
	assert.Equal(t, model.ReasonDepFailed, child.Reason)

	r := h.run("retry", "--cascade", rootKey)
	assert.Equal(t, 0, r.exitCode)

	lines := strings.Split(strings.TrimSpace(r.stdout), "\n")
	require.Len(t, lines, 3, "expected root, child, and grandchild to all be retried")
	newRootKey := strings.Fields(lines[0])[0]
	newChildKey := strings.Fields(lines[1])[0]
	newGrandchildKey := strings.Fields(lines[2])[0]
	assert.NotEqual(t, rootKey, newRootKey)
	assert.NotEqual(t, childKey, newChildKey)
	assert.NotEqual(t, grandchildKey, newGrandchildKey)

	h.waitFor(newRootKey, model.StatusCompleted)
	h.waitFor(newChildKey, model.StatusCompleted)
	h.waitFor(newGrandchildKey, model.StatusCompleted)

	// root still fails (command is unchanged "false"), so the cascade fails again too, but
	// the dep chain must be rewired to point at the newly retried keys, not the old ones.
	newRoot, err := h.db.Get(newRootKey)
	require.NoError(t, err)
	assert.Equal(t, model.ReasonExited, newRoot.Reason)
	require.NotNil(t, newRoot.ExitCode)
	assert.Equal(t, 1, *newRoot.ExitCode)

	newChild, err := h.db.Get(newChildKey)
	require.NoError(t, err)
	assert.Equal(t, model.ReasonDepFailed, newChild.Reason)
	require.Len(t, newChild.Deps, 1)
	assert.Equal(t, newRootKey, newChild.Deps[0].Key)

	newGrandchild, err := h.db.Get(newGrandchildKey)
	require.NoError(t, err)
	assert.Equal(t, model.ReasonDepFailed, newGrandchild.Reason)
	require.Len(t, newGrandchild.Deps, 1)
	assert.Equal(t, newChildKey, newGrandchild.Deps[0].Key)
}

func TestRetryCascadePreservesExternalDep(t *testing.T) {
	h := newHarness(t)

	gateKey := runFg(h, "echo", "gate")
	rootKey := runFg(h, "false")
	h.run("run", "-A", rootKey, "-A", gateKey, "echo", "child")
	childKey := h.lastJob().Key
	h.waitFor(childKey, model.StatusCompleted)

	r := h.run("retry", "--cascade", rootKey)
	assert.Equal(t, 0, r.exitCode)

	lines := strings.Split(strings.TrimSpace(r.stdout), "\n")
	require.Len(t, lines, 2)
	newRootKey := strings.Fields(lines[0])[0]
	newChildKey := strings.Fields(lines[1])[0]

	h.waitFor(newChildKey, model.StatusCompleted)
	newChild, err := h.db.Get(newChildKey)
	require.NoError(t, err)
	require.Len(t, newChild.Deps, 2)
	assert.ElementsMatch(t, []model.Dep{
		{Key: newRootKey, Kind: model.DepAfterSuccess},
		{Key: gateKey, Kind: model.DepAfterSuccess}, // untouched: outside the failed cascade
	}, newChild.Deps)
}

func TestRetryCascadeNoDependents(t *testing.T) {
	h := newHarness(t)

	rootKey := runFg(h, "echo", "original")
	h.waitFor(rootKey, model.StatusCompleted)

	r := h.run("retry", "--cascade", rootKey)
	assert.Equal(t, 0, r.exitCode)

	lines := strings.Split(strings.TrimSpace(r.stdout), "\n")
	require.Len(t, lines, 1)
	newRootKey := strings.Fields(lines[0])[0]
	assert.NotEqual(t, rootKey, newRootKey)

	h.waitFor(newRootKey, model.StatusCompleted)
}

func TestRetryCascadeRejectsForeground(t *testing.T) {
	h := newHarness(t)
	rootKey := runFg(h, "echo", "original")

	r := h.run("retry", "--cascade", "-f", rootKey)
	assert.NotEqual(t, 0, r.exitCode, "--cascade and --foreground should be mutually exclusive")
}
