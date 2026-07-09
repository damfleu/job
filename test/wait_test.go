package integration

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
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
