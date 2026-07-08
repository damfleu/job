package integration

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"job/internal/model"
)

func TestRunningSymbolResolvesToRunningJob(t *testing.T) {
	h := newHarness(t)

	r1 := h.run("run", "sleep", "60")
	runningKey := strings.TrimSpace(r1.stderr)
	require.NotEmpty(t, runningKey)
	h.waitFor(runningKey, model.StatusRunning)

	r := h.run("show", "+")
	assert.Equal(t, 0, r.exitCode)
	assert.Contains(t, r.stdout, runningKey)

	h.run("stop", runningKey)
}

func TestBlockedSymbolResolvesToBlockedJob(t *testing.T) {
	h := newHarness(t)

	r1 := h.run("run", "sleep", "60")
	depKey := strings.TrimSpace(r1.stderr)
	require.NotEmpty(t, depKey)
	h.waitFor(depKey, model.StatusRunning)

	r2 := h.run("run", "-A", depKey, "echo", "queued")
	blockedKey := strings.Fields(r2.stderr)[0]
	require.NotEmpty(t, blockedKey)
	h.waitFor(blockedKey, model.StatusBlocked)

	r := h.run("show", "_")
	assert.Equal(t, 0, r.exitCode)
	assert.Contains(t, r.stdout, blockedKey)

	h.run("stop", depKey)
}

func TestCompletedSymbolResolvesToCompletedJob(t *testing.T) {
	h := newHarness(t)
	h.run("run", "-f", "echo", "done job")
	completedKey := h.lastJob().Key

	r := h.run("show", "=")
	assert.Equal(t, 0, r.exitCode)
	assert.Contains(t, r.stdout, completedKey)
}

func TestSymbolErrorsWhenNoJobMatches(t *testing.T) {
	h := newHarness(t)
	h.run("run", "-f", "echo", "only a completed job exists")

	r := h.run("show", "+")
	assert.NotEqual(t, 0, r.exitCode)
	assert.Contains(t, r.stderr, "no running jobs")
}

// TestBareShowDefaultsToLastRunning confirms that omitting the key entirely
// diverges from "." once a chain leaves a job blocked: "." always tracks the
// most recently created job, while a bare invocation of show/log/stop prefers
// whatever is actually running.
func TestBareShowDefaultsToLastRunning(t *testing.T) {
	h := newHarness(t)

	r1 := h.run("run", "sleep", "60")
	runningKey := strings.TrimSpace(r1.stderr)
	require.NotEmpty(t, runningKey)
	h.waitFor(runningKey, model.StatusRunning)

	r2 := h.run("run", "-A", runningKey, "echo", "queued")
	blockedKey := strings.Fields(r2.stderr)[0]
	require.NotEmpty(t, blockedKey)
	h.waitFor(blockedKey, model.StatusBlocked)

	bare := h.run("show")
	assert.Equal(t, 0, bare.exitCode)
	assert.Contains(t, bare.stdout, runningKey)

	dot := h.run("show", ".")
	assert.Equal(t, 0, dot.exitCode)
	assert.Contains(t, dot.stdout, blockedKey)

	h.run("stop", runningKey)
}

// TestBareRetryDefaultsToLastCompleted confirms retry/rm's bare invocation
// prefers the last completed job over "." (the last created job), which
// would otherwise be the still-running job and fail retry's status check.
func TestBareRetryDefaultsToLastCompleted(t *testing.T) {
	h := newHarness(t)

	h.run("run", "-f", "echo", "the completed job")

	r1 := h.run("run", "sleep", "60")
	runningKey := strings.TrimSpace(r1.stderr)
	require.NotEmpty(t, runningKey)
	h.waitFor(runningKey, model.StatusRunning)

	// a bare retry that picked the running "." job instead would fail its
	// "not completed" status check, so success here proves it picked the
	// completed job.
	r := h.run("retry", "-f")
	assert.Equal(t, 0, r.exitCode, "stderr: %s", r.stderr)
	assert.Contains(t, r.stdout, "the completed job")

	h.run("stop", runningKey)
}

func TestBareDefaultFallsBackToDotWithNoStatusMatch(t *testing.T) {
	h := newHarness(t)
	h.run("run", "-f", "echo", "only job")
	completedKey := h.lastJob().Key

	// no running job exists, so bare show should fall back to "." (the only job)
	r := h.run("show")
	assert.Equal(t, 0, r.exitCode)
	assert.Contains(t, r.stdout, completedKey)
}
