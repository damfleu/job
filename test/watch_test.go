package integration

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestWatchMode(t *testing.T) {
	h := newHarness(t)
	r := h.run("run", "-w", "echo", "watch output")
	assert.Equal(t, 0, r.exitCode)
	assert.Contains(t, r.stdout, "watch output")
}

func TestWatchModeNonZeroExit(t *testing.T) {
	h := newHarness(t)
	r := h.run("run", "-w", "false")
	assert.Equal(t, 1, r.exitCode)
}

func TestWatchAndForegroundMutuallyExclusive(t *testing.T) {
	h := newHarness(t)
	r := h.run("run", "-w", "-f", "echo", "hi")
	assert.NotEqual(t, 0, r.exitCode)
}

func TestWatchCommandRequiresTTY(t *testing.T) {
	h := newHarness(t)
	r := h.run("watch")
	assert.NotEqual(t, 0, r.exitCode)
	assert.Contains(t, r.stderr, "watch requires an interactive terminal")
}

func TestWatchCommandValidatesFlagsBeforeTTY(t *testing.T) {
	h := newHarness(t)

	r := h.run("watch", "--filter", "a(b")
	assert.NotEqual(t, 0, r.exitCode)
	assert.Contains(t, r.stderr, "invalid filter")

	r = h.run("watch", "--context", "project", "--any")
	assert.NotEqual(t, 0, r.exitCode)
	assert.Contains(t, r.stderr, "if any flags in the group")

	r = h.run("watch", "--since", "later")
	assert.NotEqual(t, 0, r.exitCode)
	assert.Contains(t, r.stderr, "--since: invalid duration")

	r = h.run("watch", "--since", "-1h")
	assert.NotEqual(t, 0, r.exitCode)
	assert.Contains(t, r.stderr, "--since cannot be negative")
}
