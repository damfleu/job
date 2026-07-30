package integration

import (
	"fmt"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

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
	assert.Contains(t, r.stdout, "echo step-a")
	assert.Contains(t, r.stdout, "echo step-b")
	assert.NotContains(t, r.stdout, "cwd:")

	seq, err := h.db.GetSequence("my-seq")
	require.NoError(t, err)
	require.Len(t, seq.Steps, 2)
	assert.Equal(t, 1, seq.Steps[0].ID)
	assert.Equal(t, []string{"echo", "step-a"}, seq.Steps[0].Command)
	assert.Equal(t, 2, seq.Steps[1].ID)
	assert.Equal(t, []string{"echo", "step-b"}, seq.Steps[1].Command)
	require.Len(t, seq.Steps[1].Deps, 1)
	assert.Equal(t, 1, seq.Steps[1].Deps[0].StepID)

	r = h.run("sequence", "list")
	assert.Equal(t, 0, r.exitCode)
	assert.Contains(t, r.stdout, "my-seq")

	r = h.run("sequence", "save", "--verbose", "verbose-seq", keyB)
	assert.Equal(t, 0, r.exitCode)
	assert.Equal(t, 1, strings.Count(r.stdout, "cwd:"))
}

func TestSequenceSaveReplacesWithWarning(t *testing.T) {
	h := newHarness(t)

	keyA := runFg(h, "echo", "step-a")
	h.run("sequence", "save", "my-seq", keyA)

	keyB := runFg(h, "echo", "step-b")
	r := h.run("sequence", "save", "my-seq", keyB)
	assert.Equal(t, 0, r.exitCode)
	assert.Contains(t, r.stderr, "warning: replacing existing sequence")

	seq, err := h.db.GetSequence("my-seq")
	require.NoError(t, err)
	require.Len(t, seq.Steps, 1)
	assert.Equal(t, 1, seq.Steps[0].ID)
	assert.Equal(t, []string{"echo", "step-b"}, seq.Steps[0].Command)
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
	assert.Contains(t, r.stdout, "echo step-a")
	assert.Contains(t, r.stdout, "echo step-b")
	assert.NotContains(t, r.stdout, "cwd:")
	assert.Contains(t, r.stdout, "✓")

	r = h.run("sequence", "show", "--verbose", "show-seq")
	assert.Equal(t, 0, r.exitCode)
	assert.Equal(t, 1, strings.Count(r.stdout, "cwd:"))
	assert.Contains(t, r.stdout, "echo step-a")
	assert.Contains(t, r.stdout, "echo step-b")
}

func TestSequenceShowVerboseDisplaysDifferentWorkDirsPerStep(t *testing.T) {
	h := newHarness(t)

	keyA := runFg(h, "echo", "step-a")
	otherDir, err := filepath.EvalSymlinks(t.TempDir())
	require.NoError(t, err)
	h.runFrom(otherDir, "run", "-f", "-A", keyA, "echo", "step-b")
	keyB := h.lastJob().Key
	h.run("sequence", "save", "mixed-cwd-seq", keyB)

	r := h.run("sequence", "show", "--verbose", "mixed-cwd-seq")
	require.Equal(t, 0, r.exitCode, r.stderr)
	assert.Equal(t, 2, strings.Count(r.stdout, "cwd:"))
	assert.Contains(t, r.stdout, otherDir)
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

func TestSequenceRunResolvesContextAtExecutionTime(t *testing.T) {
	h := newHarness(t)

	originalResolver := h.writeScript("echo original-context\n")
	h.writeConfig(fmt.Sprintf("[context]\nresolvers = [%q]\n", originalResolver))

	sourceKey := runFg(h, "echo", "step")
	h.run("sequence", "save", "context-seq", sourceKey)

	currentResolver := h.writeScript("echo current-context\n")
	h.writeConfig(fmt.Sprintf("[context]\nresolvers = [%q]\n", currentResolver))

	r := h.run("sequence", "run", "context-seq")
	require.Equal(t, 0, r.exitCode, r.stderr)
	newKey := strings.Fields(strings.TrimSpace(r.stdout))[0]
	h.waitFor(newKey, model.StatusCompleted)

	replayed, err := h.db.Get(newKey)
	require.NoError(t, err)
	assert.Equal(t, "current-context", replayed.Context)
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

func TestSequenceSaveRequiresKey(t *testing.T) {
	h := newHarness(t)
	r := h.run("sequence", "save", "no-keys")
	assert.NotEqual(t, 0, r.exitCode)
}

func TestRmAllowsJobInSequence(t *testing.T) {
	h := newHarness(t)

	keyA := runFg(h, "echo", "step-a")
	h.run("sequence", "save", "rm-seq", keyA)

	h.waitFor(keyA, model.StatusCompleted)
	r := h.run("remove", "--yes", keyA)
	assert.Equal(t, 0, r.exitCode)

	r = h.run("sequence", "show", "rm-seq")
	assert.Equal(t, 0, r.exitCode)
	assert.Contains(t, r.stdout, "echo step-a")
}

func TestSequenceRunsAfterSourceJobsAreDeleted(t *testing.T) {
	h := newHarness(t)

	keyA := runFg(h, "echo", "step-a")
	h.run("run", "-A", keyA, "echo", "step-b")
	keyB := h.lastJob().Key
	h.run("sequence", "save", "durable-seq", keyB)

	h.waitFor(keyA, model.StatusCompleted)
	h.waitFor(keyB, model.StatusCompleted)
	r := h.run("remove", "--yes", keyA, keyB)
	require.Equal(t, 0, r.exitCode, r.stderr)

	r = h.run("sequence", "show", "durable-seq")
	require.Equal(t, 0, r.exitCode, r.stderr)
	assert.Contains(t, r.stdout, "echo step-a")
	assert.Contains(t, r.stdout, "echo step-b")

	r = h.run("sequence", "run", "durable-seq")
	require.Equal(t, 0, r.exitCode, r.stderr)
	lines := strings.Split(strings.TrimSpace(r.stdout), "\n")
	require.Len(t, lines, 2)
	newKeyA := strings.Fields(lines[0])[0]
	newKeyB := strings.Fields(lines[1])[0]
	h.waitFor(newKeyA, model.StatusCompleted)
	h.waitFor(newKeyB, model.StatusCompleted)

	replayedB, err := h.db.Get(newKeyB)
	require.NoError(t, err)
	require.Len(t, replayedB.Deps, 1)
	assert.Equal(t, newKeyA, replayedB.Deps[0].Key)
	assert.Equal(t, model.DepAfterSuccess, replayedB.Deps[0].Kind)
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
		for i, step := range seq.Steps {
			if step.Command[len(step.Command)-1] == key {
				return i
			}
		}
		t.Fatalf("key %s not found in steps", key)
		return -1
	}
	assert.Less(t, idx("a"), idx("b"))
	assert.Less(t, idx("a"), idx("c"))

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

func TestSequenceRunAfterSuccess(t *testing.T) {
	h := newHarness(t)

	// Build and save a simple two-step sequence.
	keyA := runFg(h, "echo", "step-a")
	h.run("run", "-A", keyA, "echo", "step-b")
	keyB := h.lastJob().Key
	h.run("sequence", "save", "after-seq", keyB)

	// gate is the external job the sequence must wait for.
	gate := runFg(h, "echo", "gate")
	h.waitFor(gate, model.StatusCompleted)

	r := h.run("sequence", "run", "-A", gate, "after-seq")
	assert.Equal(t, 0, r.exitCode)

	lines := strings.Split(strings.TrimSpace(r.stdout), "\n")
	require.Len(t, lines, 2)
	newKeyA := strings.Fields(lines[0])[0]
	newKeyB := strings.Fields(lines[1])[0]

	h.waitFor(newKeyA, model.StatusCompleted)
	h.waitFor(newKeyB, model.StatusCompleted)

	// Root step must depend on the gate.
	jA, err := h.db.Get(newKeyA)
	require.NoError(t, err)
	require.Len(t, jA.Deps, 1)
	assert.Equal(t, gate, jA.Deps[0].Key)
	assert.Equal(t, model.DepAfterSuccess, jA.Deps[0].Kind)

	// Second step depends only on the new root, not the gate.
	jB, err := h.db.Get(newKeyB)
	require.NoError(t, err)
	require.Len(t, jB.Deps, 1)
	assert.Equal(t, newKeyA, jB.Deps[0].Key)
}

func TestSequenceRunAfter(t *testing.T) {
	h := newHarness(t)

	keyA := runFg(h, "echo", "step-a")
	h.run("sequence", "save", "after-any-seq", keyA)

	gate := runFg(h, "false") // exits non-zero
	h.waitFor(gate, model.StatusCompleted)

	r := h.run("sequence", "run", "-a", gate, "after-any-seq")
	assert.Equal(t, 0, r.exitCode)

	lines := strings.Split(strings.TrimSpace(r.stdout), "\n")
	require.Len(t, lines, 1)
	newKey := strings.Fields(lines[0])[0]

	h.waitFor(newKey, model.StatusCompleted)

	j, err := h.db.Get(newKey)
	require.NoError(t, err)
	// -a runs regardless of gate exit code, so the step must have run normally.
	assert.Equal(t, model.ReasonExited, j.Reason)
	require.Len(t, j.Deps, 1)
	assert.Equal(t, gate, j.Deps[0].Key)
	assert.Equal(t, model.DepAfter, j.Deps[0].Kind)
}

func TestSequenceRunAfterSuccessMultipleRoots(t *testing.T) {
	h := newHarness(t)

	// Two independent root steps, both should be gated.
	keyA := runFg(h, "echo", "a")
	keyB := runFg(h, "echo", "b")
	h.run("sequence", "save", "multi-after-seq", keyA, keyB)

	gate := runFg(h, "echo", "gate")
	h.waitFor(gate, model.StatusCompleted)

	r := h.run("sequence", "run", "-A", gate, "multi-after-seq")
	assert.Equal(t, 0, r.exitCode)

	lines := strings.Split(strings.TrimSpace(r.stdout), "\n")
	require.Len(t, lines, 2)

	for _, line := range lines {
		newKey := strings.Fields(line)[0]
		h.waitFor(newKey, model.StatusCompleted)
		j, err := h.db.Get(newKey)
		require.NoError(t, err)
		assert.Equal(t, model.ReasonExited, j.Reason)
		require.Len(t, j.Deps, 1)
		assert.Equal(t, gate, j.Deps[0].Key)
		assert.Equal(t, model.DepAfterSuccess, j.Deps[0].Kind)
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
		for i, step := range seq.Steps {
			if step.Command[len(step.Command)-1] == key {
				return i
			}
		}
		t.Fatalf("key %s not found in steps", key)
		return -1
	}
	assert.Less(t, idx("a"), idx("b"))
	assert.Less(t, idx("a"), idx("c"))
	assert.Less(t, idx("b"), idx("d"))
	assert.Less(t, idx("c"), idx("d"))

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
