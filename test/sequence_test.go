package integration

import (
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"job/internal/db"
	"job/internal/model"
)

// runFg runs a job and returns its key.
func runFg(h *harness, args ...string) string {
	h.t.Helper()
	r := h.run(append([]string{"run"}, args...)...)
	return strings.TrimSpace(r.stderr)
}

func TestSequenceSaveAndList(t *testing.T) {
	h := newHarness(t)

	keyA := runFg(h, "echo", "step-a")
	h.run("run", "-A", keyA, "echo", "step-b")
	keyB := h.lastJob().Key

	r := h.run("sequence", "save", "my-seq", keyB)
	assert.Equal(t, 0, r.exitCode)
	assert.Contains(t, r.stdout, "my-seq")
	assert.Contains(t, r.stdout, keyA)
	assert.Contains(t, r.stdout, keyB)

	seq, err := h.db.GetSequence("my-seq")
	require.NoError(t, err)
	assert.Equal(t, []string{keyA, keyB}, seq.Steps)

	r = h.run("sequence", "list")
	assert.Equal(t, 0, r.exitCode)
	assert.Contains(t, r.stdout, "my-seq")
}

func TestSequenceSaveReplacesWithWarning(t *testing.T) {
	h := newHarness(t)

	keyA := runFg(h, "echo", "step-a")
	h.run("sequence", "save", "my-seq", keyA)

	keyB := runFg(h, "echo", "step-b")
	r := h.run("sequence", "save", "my-seq", keyB)
	assert.Equal(t, 0, r.exitCode)
	assert.Contains(t, r.stderr, "warning: replacing existing sequence")
	assert.Contains(t, r.stderr, keyA)

	seq, err := h.db.GetSequence("my-seq")
	require.NoError(t, err)
	assert.Equal(t, []string{keyB}, seq.Steps)
}

func TestSequenceShow(t *testing.T) {
	h := newHarness(t)

	keyA := runFg(h, "echo", "step-a")
	h.run("run", "-A", keyA, "echo", "step-b")
	keyB := h.lastJob().Key

	h.run("sequence", "save", "show-seq", keyB)

	r := h.run("sequence", "show", "show-seq")
	assert.Equal(t, 0, r.exitCode)
	assert.Contains(t, r.stdout, "show-seq")
	assert.Contains(t, r.stdout, keyA)
	assert.Contains(t, r.stdout, keyB)
	assert.Contains(t, r.stdout, "echo step-a")
	assert.Contains(t, r.stdout, "echo step-b")
	assert.Contains(t, r.stdout, "✓")
}

func TestSequenceRun(t *testing.T) {
	h := newHarness(t)

	keyA := runFg(h, "echo", "step-a")
	h.run("run", "-A", keyA, "echo", "step-b")
	keyB := h.lastJob().Key

	h.run("sequence", "save", "run-seq", keyB)

	r := h.run("sequence", "run", "run-seq")
	assert.Equal(t, 0, r.exitCode)

	// Output lists two new keys.
	lines := strings.Split(strings.TrimSpace(r.stdout), "\n")
	require.Len(t, lines, 2)

	newKeyA := strings.Fields(lines[0])[0]
	newKeyB := strings.Fields(lines[1])[0]
	assert.NotEqual(t, keyA, newKeyA)
	assert.NotEqual(t, keyB, newKeyB)

	h.waitFor(newKeyA, model.StatusCompleted)
	h.waitFor(newKeyB, model.StatusCompleted)

	jA, err := h.db.Get(newKeyA)
	require.NoError(t, err)
	assert.Equal(t, model.ReasonExited, jA.Reason)
	assert.Equal(t, 0, *jA.ExitCode)

	jB, err := h.db.Get(newKeyB)
	require.NoError(t, err)
	assert.Equal(t, model.ReasonExited, jB.Reason)
	assert.Equal(t, 0, *jB.ExitCode)
	require.Len(t, jB.Deps, 1)
	assert.Equal(t, newKeyA, jB.Deps[0].Key)
	assert.Equal(t, model.DepAfterSuccess, jB.Deps[0].Kind)
}

func TestSequenceRunAfterSuccessPropagatesFailure(t *testing.T) {
	h := newHarness(t)

	keyA := runFg(h, "false")
	h.run("run", "-A", keyA, "echo", "should-not-run")
	keyB := h.lastJob().Key

	h.run("sequence", "save", "fail-seq", keyB)

	r := h.run("sequence", "run", "fail-seq")
	assert.Equal(t, 0, r.exitCode)

	lines := strings.Split(strings.TrimSpace(r.stdout), "\n")
	require.Len(t, lines, 2)
	newKeyA := strings.Fields(lines[0])[0]
	newKeyB := strings.Fields(lines[1])[0]

	h.waitFor(newKeyA, model.StatusCompleted)
	h.waitFor(newKeyB, model.StatusCompleted)

	jA, err := h.db.Get(newKeyA)
	require.NoError(t, err)
	assert.Equal(t, 1, *jA.ExitCode)

	jB, err := h.db.Get(newKeyB)
	require.NoError(t, err)
	assert.Equal(t, model.ReasonDepFailed, jB.Reason)
}

func TestSequenceRmUnknown(t *testing.T) {
	h := newHarness(t)
	r := h.run("sequence", "rm", "does-not-exist")
	assert.NotEqual(t, 0, r.exitCode)
}

func TestPruneSkipsJobsInSequence(t *testing.T) {
	h := newHarness(t)

	// keyA is the job to protect; keyB is used as the prune cutoff.
	keyA := runFg(h, "echo", "step-a")
	keyB := runFg(h, "echo", "cutoff")
	h.waitFor(keyA, model.StatusCompleted)
	h.waitFor(keyB, model.StatusCompleted)

	h.run("sequence", "save", "prune-seq", keyA)

	// Pruning with keyB as cutoff would include keyA, but it's in a sequence.
	r := h.run("prune", "--before", keyB)
	assert.Equal(t, 0, r.exitCode)
	assert.Contains(t, r.stderr, "referenced by sequence")

	_, err := h.db.Get(keyA)
	require.NoError(t, err, "job should still exist")

	// After removing the sequence, pruning should remove keyA.
	h.run("sequence", "rm", "prune-seq")

	r = h.run("prune", "--before", keyB)
	assert.Equal(t, 0, r.exitCode)

	_, err = h.db.Get(keyA)
	assert.ErrorIs(t, err, db.ErrNotFound, "job should be gone after prune")
}

func TestRmRefusesJobInSequence(t *testing.T) {
	h := newHarness(t)

	keyA := runFg(h, "echo", "step-a")
	h.run("sequence", "save", "rm-seq", keyA)

	r := h.run("remove", keyA)
	assert.NotEqual(t, 0, r.exitCode)
	assert.Contains(t, r.stderr, "referenced by sequence")

	// After removing the sequence, rm should work.
	h.run("sequence", "rm", "rm-seq")
	r = h.run("remove", keyA)
	assert.Equal(t, 0, r.exitCode)
}

func TestSequenceRunCwd(t *testing.T) {
	h := newHarness(t)

	origDir, err := filepath.EvalSymlinks(t.TempDir())
	require.NoError(t, err)
	keyA := runFg(h, "echo", "step-a")
	h.runFrom(origDir, "run", "-f", "-A", keyA, "echo", "step-b")
	keyB := h.lastJob().Key
	h.run("sequence", "save", "cwd-seq", keyB)

	cwdDir, err := filepath.EvalSymlinks(t.TempDir())
	require.NoError(t, err)
	r := h.runFrom(cwdDir, "sequence", "run", "--cwd", "cwd-seq")
	assert.Equal(t, 0, r.exitCode)

	lines := strings.Split(strings.TrimSpace(r.stdout), "\n")
	require.Len(t, lines, 2)
	for _, line := range lines {
		newKey := strings.Fields(line)[0]
		h.waitFor(newKey, model.StatusCompleted)
		j, err := h.db.Get(newKey)
		require.NoError(t, err)
		assert.Equal(t, cwdDir, j.WorkDir)
	}
}

func TestSequenceRunCwdExplicit(t *testing.T) {
	h := newHarness(t)

	origDir, err := filepath.EvalSymlinks(t.TempDir())
	require.NoError(t, err)
	keyA := runFg(h, "echo", "step-a")
	h.runFrom(origDir, "run", "-f", "-A", keyA, "echo", "step-b")
	keyB := h.lastJob().Key
	h.run("sequence", "save", "cwd-explicit-seq", keyB)

	explicitDir, err := filepath.EvalSymlinks(t.TempDir())
	require.NoError(t, err)
	r := h.run("sequence", "run", "--cwd="+explicitDir, "cwd-explicit-seq")
	assert.Equal(t, 0, r.exitCode)

	lines := strings.Split(strings.TrimSpace(r.stdout), "\n")
	require.Len(t, lines, 2)
	for _, line := range lines {
		newKey := strings.Fields(line)[0]
		h.waitFor(newKey, model.StatusCompleted)
		j, err := h.db.Get(newKey)
		require.NoError(t, err)
		assert.Equal(t, explicitDir, j.WorkDir)
	}
}

func TestSequenceSaveMultipleRoots(t *testing.T) {
	h := newHarness(t)

	// A → B
	// A → C   (B and C are both leaves; no shared successor)
	keyA := runFg(h, "echo", "a")

	h.run("run", "-A", keyA, "echo", "b")
	keyB := h.lastJob().Key

	h.run("run", "-A", keyA, "echo", "c")
	keyC := h.lastJob().Key

	r := h.run("sequence", "save", "multi-root", keyB, keyC)
	assert.Equal(t, 0, r.exitCode)

	seq, err := h.db.GetSequence("multi-root")
	require.NoError(t, err)
	require.Len(t, seq.Steps, 3)

	idx := func(key string) int {
		for i, k := range seq.Steps {
			if k == key {
				return i
			}
		}
		t.Fatalf("key %s not found in steps", key)
		return -1
	}
	assert.Less(t, idx(keyA), idx(keyB))
	assert.Less(t, idx(keyA), idx(keyC))

	r = h.run("sequence", "run", "multi-root")
	assert.Equal(t, 0, r.exitCode)

	lines := strings.Split(strings.TrimSpace(r.stdout), "\n")
	require.Len(t, lines, 3)
	for _, line := range lines {
		newKey := strings.Fields(line)[0]
		h.waitFor(newKey, model.StatusCompleted)
		j, err := h.db.Get(newKey)
		require.NoError(t, err)
		assert.Equal(t, model.ReasonExited, j.Reason)
	}
}

func TestSequenceDiamondDependency(t *testing.T) {
	h := newHarness(t)

	//      A
	//    /   \
	//   B     C
	//    \   /
	//      D
	keyA := runFg(h, "echo", "a")

	h.run("run", "-A", keyA, "echo", "b")
	keyB := h.lastJob().Key

	h.run("run", "-A", keyA, "echo", "c")
	keyC := h.lastJob().Key

	h.run("run", "-A", keyB, "-A", keyC, "echo", "d")
	keyD := h.lastJob().Key

	h.run("sequence", "save", "diamond", keyD)

	seq, err := h.db.GetSequence("diamond")
	require.NoError(t, err)
	require.Len(t, seq.Steps, 4)

	// A must come before B and C; B and C must come before D.
	idx := func(key string) int {
		for i, k := range seq.Steps {
			if k == key {
				return i
			}
		}
		t.Fatalf("key %s not found in steps", key)
		return -1
	}
	assert.Less(t, idx(keyA), idx(keyB))
	assert.Less(t, idx(keyA), idx(keyC))
	assert.Less(t, idx(keyB), idx(keyD))
	assert.Less(t, idx(keyC), idx(keyD))

	r := h.run("sequence", "run", "diamond")
	assert.Equal(t, 0, r.exitCode)

	lines := strings.Split(strings.TrimSpace(r.stdout), "\n")
	require.Len(t, lines, 4)
	for _, line := range lines {
		newKey := strings.Fields(line)[0]
		h.waitFor(newKey, model.StatusCompleted)
		j, err := h.db.Get(newKey)
		require.NoError(t, err)
		assert.Equal(t, model.ReasonExited, j.Reason)
	}
}
