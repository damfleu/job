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
	h.run("run", "-f", "echo", "show test")

	r := h.run("show")
	assert.Equal(t, 0, r.exitCode)
	assert.Contains(t, r.stdout, "echo show test") // command
	assert.Contains(t, r.stdout, "completed")       // status
	assert.Contains(t, r.stdout, "exited")          // reason
	assert.Contains(t, r.stdout, "Exit code:")
	assert.Contains(t, r.stdout, "WorkDir:")
	assert.Contains(t, r.stdout, "Duration:")
}

func TestJobLog(t *testing.T) {
	h := newHarness(t)
	h.run("run", "-f", "echo", "log test output")

	r := h.run("log")
	assert.Equal(t, 0, r.exitCode)
	assert.Contains(t, r.stdout, "log test output")
}

func TestJobLogPath(t *testing.T) {
	h := newHarness(t)
	h.run("run", "-f", "echo", "hi")

	r := h.run("log", "-p")
	assert.Equal(t, 0, r.exitCode)
	path := strings.TrimSpace(r.stdout)
	assert.True(t, filepath.IsAbs(path))
	_, err := os.Stat(path)
	assert.NoError(t, err)
}

func TestJobList(t *testing.T) {
	h := newHarness(t)
	h.run("run", "-f", "echo", "first")
	h.run("run", "-f", "echo", "second")

	r := h.run("list")
	assert.Equal(t, 0, r.exitCode)
	assert.Contains(t, r.stdout, "echo first")
	assert.Contains(t, r.stdout, "echo second")
	assert.Contains(t, r.stdout, "COMMAND") // table header
}

func TestJobListFilter(t *testing.T) {
	h := newHarness(t)
	h.run("run", "-f", "echo", "keep this")
	h.run("run", "-f", "false")

	r := h.run("list", "-f", "echo")
	assert.Equal(t, 0, r.exitCode)
	assert.Contains(t, r.stdout, "echo keep this")
	assert.NotContains(t, r.stdout, "false")
}

func TestJobListLimit(t *testing.T) {
	h := newHarness(t)
	for range 5 {
		h.run("run", "-f", "echo", "job")
	}

	r := h.run("list", "-n", "3")
	assert.Equal(t, 0, r.exitCode)
	assert.Equal(t, 3, strings.Count(r.stdout, "echo job"))
}

func TestJobListInvalidFilter(t *testing.T) {
	h := newHarness(t)
	r := h.run("list", "-f", "a(b")
	assert.NotEqual(t, 0, r.exitCode)
}

func TestJobListRegex(t *testing.T) {
	h := newHarness(t)
	// three distinct commands
	h.run("run", "-f", "echo", "alpha")
	h.run("run", "-f", "echo", "beta")
	h.run("run", "-f", "sh", "-c", "true")

	// anchored: only commands starting with "echo"
	r := h.run("list", "-f", "^echo")
	assert.Equal(t, 0, r.exitCode)
	assert.Contains(t, r.stdout, "echo alpha")
	assert.Contains(t, r.stdout, "echo beta")
	assert.NotContains(t, r.stdout, "sh -c true")

	// flag in command: matches "sh -c" anywhere
	r2 := h.run("list", "-f", `sh -c`)
	assert.Equal(t, 0, r2.exitCode)
	assert.Contains(t, r2.stdout, "sh -c true")
	assert.NotContains(t, r2.stdout, "echo")

	// alternation: either "alpha" or "beta"
	r3 := h.run("list", "-f", "alpha|beta")
	assert.Equal(t, 0, r3.exitCode)
	assert.Contains(t, r3.stdout, "echo alpha")
	assert.Contains(t, r3.stdout, "echo beta")
	assert.NotContains(t, r3.stdout, "sh -c true")
}
