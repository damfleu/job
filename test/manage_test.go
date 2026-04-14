package integration

import (
	"os"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"job/internal/db"
	"job/internal/model"
)

func TestJobStop(t *testing.T) {
	h := newHarness(t)
	r := h.run("run", "-v", "sleep", "60")
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

	h.run("remove", j.Key)

	_, err := h.db.Get(j.Key)
	assert.ErrorIs(t, err, db.ErrNotFound)

	_, err = os.Stat(logFile)
	assert.True(t, os.IsNotExist(err), "log file should be deleted after remove")
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

	jobs, err := h.db.ListCompleted(10, "")
	require.NoError(t, err)
	require.Len(t, jobs, 2)
	logFiles := []string{jobs[0].LogFile, jobs[1].LogFile}

	r := h.run("prune", "--older-than", "0s")
	assert.Equal(t, 0, r.exitCode)
	assert.Contains(t, r.stdout, "pruned 2 job(s)")

	remaining, err := h.db.ListCompleted(10, "")
	require.NoError(t, err)
	assert.Empty(t, remaining)

	for _, lf := range logFiles {
		_, err := os.Stat(lf)
		assert.True(t, os.IsNotExist(err), "log file %s should be deleted", lf)
	}
}

func TestPruneBefore(t *testing.T) {
	h := newHarness(t)
	h.run("run", "-f", "echo", "first")
	j1 := h.lastJob()
	h.run("run", "-f", "echo", "second")
	j2 := h.lastJob()

	r := h.run("prune", "--before", j2.Key)
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

func TestDotResolution(t *testing.T) {
	h := newHarness(t)
	h.run("run", "-f", "echo", "dot test")

	r := h.run("show", ".")
	assert.Equal(t, 0, r.exitCode)
	assert.Contains(t, r.stdout, "echo dot test")
}
