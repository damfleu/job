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
