package integration

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestJobShow(t *testing.T) {
	h := newHarness(t)
	h.run("-f", "--", "echo", "show test")

	r := h.run("show")
	assert.Equal(t, 0, r.exitCode)
	assert.Contains(t, r.stdout, "echo show test")
	assert.Contains(t, r.stdout, "completed")
}

func TestJobLog(t *testing.T) {
	h := newHarness(t)
	h.run("-f", "--", "echo", "log test output")

	r := h.run("log")
	assert.Equal(t, 0, r.exitCode)
	assert.Contains(t, r.stdout, "log test output")
}

func TestJobLogPath(t *testing.T) {
	h := newHarness(t)
	h.run("-f", "--", "echo", "hi")

	r := h.run("log", "-p")
	assert.Equal(t, 0, r.exitCode)
	path := strings.TrimSpace(r.stdout)
	assert.True(t, filepath.IsAbs(path))
	_, err := os.Stat(path)
	assert.NoError(t, err)
}

func TestJobList(t *testing.T) {
	h := newHarness(t)
	h.run("-f", "--", "echo", "first")
	h.run("-f", "--", "echo", "second")

	r := h.run("list")
	assert.Equal(t, 0, r.exitCode)
	assert.Contains(t, r.stdout, "echo")
}
