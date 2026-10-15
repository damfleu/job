package integration

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestWaitSuccess(t *testing.T) {
	h := newHarness(t)
	h.run("run", "-k", "ok", "--", "true")

	r := h.run("wait", "ok")
	assert.Equal(t, 0, r.exitCode)
	assert.Contains(t, r.stdout, "success")
	assert.Contains(t, r.stdout, "rc=0")
}

func TestWaitFailure(t *testing.T) {
	h := newHarness(t)
	h.run("run", "-k", "t1", "--", "sh", "-c", "sleep 1; exit 3")

	r := h.run("wait", "t1")
	assert.Equal(t, 3, r.exitCode)
	assert.Contains(t, r.stdout, "failed")
	assert.Contains(t, r.stdout, "rc=3")
}

func TestWaitMultipleAnyFail(t *testing.T) {
	h := newHarness(t)
	h.run("run", "-k", "a", "--", "true")
	h.run("run", "-k", "b", "--", "sh", "-c", "exit 1")

	r := h.run("wait", "a", "b")
	assert.Equal(t, 1, r.exitCode)
	lines := strings.Split(strings.TrimSpace(r.stdout), "\n")
	assert.Len(t, lines, 2)
	assert.Contains(t, r.stdout, "success")
	assert.Contains(t, r.stdout, "failed")
}

func TestWaitMultipleAllSuccess(t *testing.T) {
	h := newHarness(t)
	h.run("run", "-k", "a", "--", "true")
	h.run("run", "-k", "b", "--", "true")

	r := h.run("wait", "a", "b")
	assert.Equal(t, 0, r.exitCode)
}

func TestWaitDefaultsToLastRunning(t *testing.T) {
	h := newHarness(t)
	h.run("run", "--", "true")

	r := h.run("wait")
	assert.Equal(t, 0, r.exitCode)
}

func TestWaitWatchCompletedJob(t *testing.T) {
	for _, tc := range []struct {
		name string
		code int
		cmd  string
	}{
		{"success", 0, "printf 'stdout\n'; printf 'stderr\n' >&2"},
		{"failed", 3, "printf 'stdout\n'; printf 'stderr\n' >&2; exit 3"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			h := newHarness(t)
			require.Equal(t, 0, h.run("run", "-k", "logged", "--", "sh", "-c", tc.cmd).exitCode)
			require.Equal(t, tc.code, h.run("wait", "logged").exitCode)

			r := h.run("wait", "--watch", "logged")
			assert.Equal(t, tc.code, r.exitCode)
			assert.Equal(t, "stdout\nstderr\n", r.stdout)
			assert.Contains(t, r.stderr, tc.name)
		})
	}
}

func TestWaitWatchRejectsMultipleKeys(t *testing.T) {
	h := newHarness(t)
	for _, flag := range []string{"--watch", "-w"} {
		// Reject the arguments before attempting to resolve either key.
		r := h.run("wait", flag, "a", "b")
		assert.Equal(t, 1, r.exitCode)
		assert.Empty(t, r.stdout)
		assert.Contains(t, r.stderr, "--watch accepts at most one job key")
	}
}

func TestWaitWatchDefault(t *testing.T) {
	h := newHarness(t)
	require.Equal(t, 0, h.run("run", "--", "echo", "default output").exitCode)

	r := h.run("wait", "-w")
	assert.Equal(t, 0, r.exitCode)
	assert.Equal(t, "default output\n", r.stdout)
	assert.Contains(t, r.stderr, "success  rc=0")
}

func TestWaitWatchDependencyFailure(t *testing.T) {
	h := newHarness(t)
	require.Equal(t, 0, h.run("run", "-k", "fail", "--", "false").exitCode)
	require.Equal(t, 0, h.run("run", "-k", "blocked", "-A", "fail", "--", "echo", "should not run").exitCode)

	r := h.run("wait", "-w", "blocked")
	assert.Equal(t, 1, r.exitCode)
	assert.Empty(t, r.stdout)
	assert.Contains(t, r.stderr, "dep_failed")
}
