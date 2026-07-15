package integration

import (
	"fmt"
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

func TestJobRemoveEmptyStdinIsNoOp(t *testing.T) {
	h := newHarness(t)
	h.run("run", "-f", "echo", "keep me")
	j := h.lastJob()

	r := h.runWithStdin(" \n\t", "remove", "--yes")
	assert.Equal(t, 0, r.exitCode, "stderr: %s", r.stderr)
	assert.Empty(t, r.stdout)
	assert.Empty(t, r.stderr)

	_, err := h.db.Get(j.Key)
	assert.NoError(t, err, "job should remain when stdin contains no keys")
}

func TestJobRemoveReadsWhitespaceSeparatedKeysFromStdin(t *testing.T) {
	h := newHarness(t)
	h.run("run", "-f", "echo", "one")
	one := h.lastJob()
	h.run("run", "-f", "echo", "two")
	two := h.lastJob()
	h.run("run", "-f", "echo", "three")
	three := h.lastJob()

	input := one.Key + "  " + two.Key + "\n\t" + three.Key + "\n"
	r := h.runWithStdin(input, "remove", "--yes")
	assert.Equal(t, 0, r.exitCode, "stderr: %s", r.stderr)
	assert.Contains(t, r.stderr, "About to delete 3 completed job(s)")
	assert.Contains(t, r.stdout, "removed "+one.Key)
	assert.Contains(t, r.stdout, "removed "+two.Key)
	assert.Contains(t, r.stdout, "removed "+three.Key)

	for _, key := range []string{one.Key, two.Key, three.Key} {
		_, err := h.db.Get(key)
		assert.ErrorIs(t, err, db.ErrNotFound)
	}
}

func TestJobRemoveFromStdinRequiresConfirmationOutsideTerminal(t *testing.T) {
	h := newHarness(t)
	h.run("run", "-f", "echo", "keep me")
	j := h.lastJob()

	r := h.runWithStdin(j.Key+"\n", "remove")
	assert.NotEqual(t, 0, r.exitCode)
	assert.Contains(t, r.stderr, "confirmation requires a terminal")

	_, err := h.db.Get(j.Key)
	assert.NoError(t, err, "job should remain when piped removal is not approved")
}

func TestJobRemoveMultipleJobs(t *testing.T) {
	h := newHarness(t)
	h.run("run", "-f", "echo", "one")
	one := h.lastJob()
	h.run("run", "-f", "echo", "two")
	two := h.lastJob()

	r := h.run("remove", "--yes", one.Key, two.Key)
	assert.Equal(t, 0, r.exitCode, "stderr: %s", r.stderr)
	assert.Contains(t, r.stderr, "About to delete 2 completed job(s)")
	assert.Contains(t, r.stdout, "removed "+one.Key)
	assert.Contains(t, r.stdout, "removed "+two.Key)

	_, err := h.db.Get(one.Key)
	assert.ErrorIs(t, err, db.ErrNotFound)
	_, err = h.db.Get(two.Key)
	assert.ErrorIs(t, err, db.ErrNotFound)
}

func TestJobRemoveBatchPreflightPreventsPartialDeletion(t *testing.T) {
	h := newHarness(t)
	h.run("run", "-f", "echo", "completed")
	completed := h.lastJob()

	run := h.run("run", "sleep", "60")
	runningKey := strings.TrimSpace(run.stderr)
	require.NotEmpty(t, runningKey)
	h.waitFor(runningKey, model.StatusRunning)

	r := h.run("remove", "--yes", completed.Key, runningKey)
	assert.NotEqual(t, 0, r.exitCode)
	assert.Contains(t, r.stderr, "is not completed")

	_, err := h.db.Get(completed.Key)
	assert.NoError(t, err, "completed job should remain after batch preflight fails")
	h.run("stop", runningKey)
}

func TestContextCleanupWithListAndRemove(t *testing.T) {
	h := newHarness(t)
	base := t.TempDir()
	claudeA := filepath.Join(base, "claude-a")
	claudeB := filepath.Join(base, "claude-b")
	terminal := filepath.Join(base, "terminal")
	for _, dir := range []string{claudeA, claudeB, terminal} {
		require.NoError(t, os.Mkdir(dir, 0o755))
	}

	script := h.writeScript("basename \"$PWD\"")
	h.writeConfig(fmt.Sprintf("[context]\nresolvers = [%q]\n", script))
	h.runFrom(claudeA, "run", "-f", "echo", "claude-a")
	jobA := h.lastJob()
	h.runFrom(claudeB, "run", "-f", "echo", "claude-b")
	jobB := h.lastJob()
	h.runFrom(terminal, "run", "-f", "echo", "terminal")
	terminalJob := h.lastJob()

	listed := h.runFrom(terminal, "list", "--context", `^claude-`, "--older-than", "0s", "-n", "0", "--keys")
	require.Equal(t, 0, listed.exitCode, "stderr: %s", listed.stderr)
	keys := strings.Fields(listed.stdout)
	require.ElementsMatch(t, []string{jobA.Key, jobB.Key}, keys)

	removed := h.runFromWithStdin(terminal, listed.stdout, "remove", "--yes")
	assert.Equal(t, 0, removed.exitCode, "stderr: %s", removed.stderr)

	_, err := h.db.Get(jobA.Key)
	assert.ErrorIs(t, err, db.ErrNotFound)
	_, err = h.db.Get(jobB.Key)
	assert.ErrorIs(t, err, db.ErrNotFound)
	_, err = h.db.Get(terminalJob.Key)
	assert.NoError(t, err, "non-matching context should remain")
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
