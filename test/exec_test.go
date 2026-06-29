package integration

import (
	"os"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"job/internal/model"
)

func TestForegroundJob(t *testing.T) {
	h := newHarness(t)
	r := h.run("run", "-f", "echo", "hello integration")
	assert.Equal(t, 0, r.exitCode)
	assert.Contains(t, r.stdout, "hello integration")

	j := h.lastJob()
	assert.Equal(t, model.StatusCompleted, j.Status)
	assert.Equal(t, model.ReasonExited, j.Reason)
	require.NotNil(t, j.ExitCode)
	assert.Equal(t, 0, *j.ExitCode)

	content, err := os.ReadFile(j.LogFile)
	require.NoError(t, err)
	assert.Contains(t, string(content), "hello integration")
}

func TestForegroundJobNonZeroExit(t *testing.T) {
	h := newHarness(t)
	r := h.run("run", "-f", "false")
	assert.Equal(t, 1, r.exitCode)

	j := h.lastJob()
	assert.Equal(t, model.StatusCompleted, j.Status)
	require.NotNil(t, j.ExitCode)
	assert.Equal(t, 1, *j.ExitCode)
}

func TestBackgroundJob(t *testing.T) {
	h := newHarness(t)
	r := h.run("run", "echo", "bg hello")
	assert.Equal(t, 0, r.exitCode)
	key := strings.TrimSpace(r.stderr)
	require.NotEmpty(t, key)

	h.waitFor(key, model.StatusCompleted)

	j, err := h.db.Get(key)
	require.NoError(t, err)
	assert.Equal(t, model.StatusCompleted, j.Status)
	require.NotNil(t, j.ExitCode)
	assert.Equal(t, 0, *j.ExitCode)

	content, err := os.ReadFile(j.LogFile)
	require.NoError(t, err)
	assert.Contains(t, string(content), "bg hello")
}

func TestQuietFlag(t *testing.T) {
	h := newHarness(t)
	r := h.run("run", "-q", "echo", "quiet")
	assert.Empty(t, r.stderr)
}

func TestDefaultPrintsKey(t *testing.T) {
	h := newHarness(t)
	r := h.run("run", "echo", "hello")
	assert.NotEmpty(t, r.stderr)
}
