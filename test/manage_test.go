package integration

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"job/internal/db"
	"job/internal/model"
)

func TestJobStop(t *testing.T) {
	h := newHarness(t)
	r := h.run("run", "sleep", "60")
	key := strings.TrimSpace(r.stderr)
	require.NotEmpty(t, key)

	h.waitFor(key, model.StatusRunning)
	h.run("stop", key)

	j, err := h.db.Get(key)
	require.NoError(t, err)
	assert.Equal(t, model.StatusCompleted, j.Status)
	assert.Equal(t, model.ReasonStopped, j.Reason)
}

func TestJobRemove(t *testing.T) {
	h := newHarness(t)
	h.run("run", "-f", "echo", "bye")

	j := h.lastJob()
	logFile := j.LogFile
	require.NotEmpty(t, logFile)

	h.run("remove", "--yes", j.Key)

	_, err := h.db.Get(j.Key)
	assert.ErrorIs(t, err, db.ErrNotFound)

	_, err = os.Stat(logFile)
	assert.True(t, os.IsNotExist(err), "log file should be deleted after remove")
}

func TestJobRemoveRequiresConfirmationOutsideTerminal(t *testing.T) {
	h := newHarness(t)
	h.run("run", "-f", "echo", "bye")
	j := h.lastJob()

	r := h.run("remove", j.Key)
	assert.NotEqual(t, 0, r.exitCode)
	assert.Contains(t, r.stderr, "confirmation requires a terminal")

	_, err := h.db.Get(j.Key)
	assert.NoError(t, err, "job should remain when removal is not approved")
}

func TestJobRemoveRequiresExplicitKey(t *testing.T) {
	h := newHarness(t)
	h.run("run", "-f", "echo", "keep me")
	j := h.lastJob()

	r := h.run("remove", "--yes")
	assert.NotEqual(t, 0, r.exitCode)
	assert.Contains(t, r.stderr, "accepts 1 arg(s), received 0")

	_, err := h.db.Get(j.Key)
	assert.NoError(t, err, "job should remain when no key is provided")
}

func TestJobAlias(t *testing.T) {
	h := newHarness(t)
	h.run("run", "-f", "-k", "mybuild", "echo", "aliased")

	j := h.lastJob()
	assert.Equal(t, "mybuild", j.Alias)

	r := h.run("show", "mybuild")
	assert.Equal(t, 0, r.exitCode)
	assert.Contains(t, r.stdout, "mybuild")
}

func TestPruneOlderThan(t *testing.T) {
	h := newHarness(t)
	h.run("run", "-f", "echo", "one")
	h.run("run", "-f", "echo", "two")

	jobs, err := h.db.ListCompleted(10, "", "")
	require.NoError(t, err)
	require.Len(t, jobs, 2)
	logFiles := []string{jobs[0].LogFile, jobs[1].LogFile}

	r := h.run("prune", "--yes", "--older-than", "0s")
	assert.Equal(t, 0, r.exitCode)
	assert.Contains(t, r.stdout, "pruned 2 job(s)")

	remaining, err := h.db.ListCompleted(10, "", "")
	require.NoError(t, err)
	assert.Empty(t, remaining)

	for _, lf := range logFiles {
		_, err := os.Stat(lf)
		assert.True(t, os.IsNotExist(err), "log file %s should be deleted", lf)
	}
}

func TestPruneRequiresConfirmationOutsideTerminal(t *testing.T) {
	h := newHarness(t)
	h.run("run", "-f", "echo", "one")
	j := h.lastJob()

	r := h.run("prune", "--older-than", "0s")
	assert.NotEqual(t, 0, r.exitCode)
	assert.Contains(t, r.stderr, "About to delete 1 completed job(s)")
	assert.Contains(t, r.stderr, "confirmation requires a terminal")

	_, err := h.db.Get(j.Key)
	assert.NoError(t, err, "job should remain when pruning is not approved")
}

func TestPruneBefore(t *testing.T) {
	h := newHarness(t)
	h.run("run", "-f", "echo", "first")
	j1 := h.lastJob()
	h.run("run", "-f", "echo", "second")
	j2 := h.lastJob()

	r := h.run("prune", "--yes", "--before", j2.Key)
	assert.Equal(t, 0, r.exitCode)
	assert.Contains(t, r.stdout, "pruned 1 job(s)")

	_, err := h.db.Get(j1.Key)
	assert.ErrorIs(t, err, db.ErrNotFound, "j1 should be pruned")

	_, err = h.db.Get(j2.Key)
	assert.NoError(t, err, "j2 should remain")
}

func TestPruneRequiresFlag(t *testing.T) {
	h := newHarness(t)
	r := h.run("prune")
	assert.NotEqual(t, 0, r.exitCode)
}

func TestStopBlockedJobShowsRunningDep(t *testing.T) {
	h := newHarness(t)

	r1 := h.run("run", "sleep", "60")
	depKey := strings.TrimSpace(r1.stderr)
	require.NotEmpty(t, depKey)
	h.waitFor(depKey, model.StatusRunning)

	r2 := h.run("run", "-A", depKey, "echo", "queued")
	blockedKey := strings.Fields(r2.stderr)[0]
	require.NotEmpty(t, blockedKey)
	h.waitFor(blockedKey, model.StatusBlocked)

	r := h.run("stop", ".")
	assert.Equal(t, 0, r.exitCode)
	assert.Contains(t, r.stdout, "stopped "+blockedKey)
	assert.Contains(t, r.stdout, depKey+" is still running")
}

func TestDotResolution(t *testing.T) {
	h := newHarness(t)
	h.run("run", "-f", "echo", "dot test")

	r := h.run("show", ".")
	assert.Equal(t, 0, r.exitCode)
	assert.Contains(t, r.stdout, "echo dot test")
}

func TestAutomatedJobDoesNotUpdateDot(t *testing.T) {
	h := newHarness(t)

	// Human job — becomes "."
	humanKey := runFg(h, "echo", "human job")

	// Automated job — capture key from stderr, should not steal "."
	r := h.run("run", "--automated", "echo", "automated job")
	automatedKey := strings.TrimSpace(r.stderr)
	require.NotEmpty(t, automatedKey)

	// Verify the automated flag is stored.
	j, err := h.db.Get(automatedKey)
	require.NoError(t, err)
	assert.True(t, j.Automated)

	// "." should still resolve to the human job.
	r = h.run("show", ".")
	assert.Equal(t, 0, r.exitCode)
	assert.Contains(t, r.stdout, humanKey)
}

func TestRunCwd(t *testing.T) {
	h := newHarness(t)

	dir, err := filepath.EvalSymlinks(t.TempDir())
	require.NoError(t, err)

	h.run("run", "-f", "--cwd="+dir, "echo", "in-dir")
	j := h.lastJob()
	assert.Equal(t, dir, j.WorkDir)
}

func TestSequenceRunDoesNotUpdateDot(t *testing.T) {
	h := newHarness(t)

	// Human job — this becomes the last human "."
	lastHumanKey := runFg(h, "echo", "last human job")

	// Create and run a sequence from a separate job.
	seqKey := runFg(h, "echo", "seq step")
	h.run("sequence", "save", "dot-seq", seqKey)
	h.run("sequence", "run", "dot-seq")

	// "." should still resolve to seqKey, not the automated sequence job.
	r := h.run("show", ".")
	assert.Equal(t, 0, r.exitCode)
	assert.Contains(t, r.stdout, seqKey)
	assert.NotContains(t, r.stdout, lastHumanKey)
}
