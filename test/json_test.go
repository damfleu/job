package integration

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"job/internal/model"
)

func TestShowJSONSuccess(t *testing.T) {
	h := newHarness(t)
	h.run("run", "-f", "true")

	r := h.run("show", "--json")
	assert.Equal(t, 0, r.exitCode)

	var view map[string]any
	require.NoError(t, json.Unmarshal([]byte(r.stdout), &view))
	assert.Equal(t, "success", view["outcome"])
	assert.Equal(t, "completed", view["status"])
	assert.Equal(t, "exited", view["reason"])
	assert.Equal(t, float64(0), view["exit_code"])
}

func TestShowJSONFailed(t *testing.T) {
	h := newHarness(t)
	h.run("run", "-f", "sh", "-c", "exit 3")

	r := h.run("show", "--json")
	assert.Equal(t, 0, r.exitCode)

	var view map[string]any
	require.NoError(t, json.Unmarshal([]byte(r.stdout), &view))
	assert.Equal(t, "failed", view["outcome"])
	assert.Equal(t, float64(3), view["exit_code"])
}

func TestShowJSONStopped(t *testing.T) {
	h := newHarness(t)
	r := h.run("run", "--", "sleep", "5")
	key := strings.TrimSpace(r.stderr)
	h.waitFor(key, model.StatusRunning)

	h.run("stop", key)
	h.waitFor(key, model.StatusCompleted)

	r = h.run("show", key, "--json")
	assert.Equal(t, 0, r.exitCode)

	var view map[string]any
	require.NoError(t, json.Unmarshal([]byte(r.stdout), &view))
	assert.Equal(t, "stopped", view["outcome"])
	assert.Nil(t, view["exit_code"])
}

func TestShowJSONDepFailed(t *testing.T) {
	h := newHarness(t)
	h.run("run", "-f", "-k", "base", "--", "sh", "-c", "exit 1")

	r := h.run("run", "-A", "base", "--", "true")
	depKey := strings.Fields(r.stderr)[0]
	h.waitFor(depKey, model.StatusCompleted)

	r = h.run("show", depKey, "--json")
	assert.Equal(t, 0, r.exitCode)

	var view map[string]any
	require.NoError(t, json.Unmarshal([]byte(r.stdout), &view))
	assert.Equal(t, "dep_failed", view["outcome"])
	assert.Nil(t, view["exit_code"])
}

func TestLsJSON(t *testing.T) {
	h := newHarness(t)
	h.run("run", "-f", "echo", "one")
	h.run("run", "-f", "echo", "two")

	r := h.run("list", "--json")
	assert.Equal(t, 0, r.exitCode)

	var views []map[string]any
	require.NoError(t, json.Unmarshal([]byte(r.stdout), &views))
	assert.Len(t, views, 2)
	for _, v := range views {
		assert.Equal(t, "success", v["outcome"])
	}
}

func TestLsJSONEmpty(t *testing.T) {
	h := newHarness(t)

	r := h.run("list", "--json")
	assert.Equal(t, 0, r.exitCode)
	assert.Equal(t, "[]\n", r.stdout)
}

func TestShowJSONAutomaticForClaude(t *testing.T) {
	h := newHarness(t)
	h.run("run", "-f", "true")

	r := h.runWithEnv(map[string]string{"CLAUDECODE": "1"}, "show")
	assert.Equal(t, 0, r.exitCode)

	var view map[string]any
	require.NoError(t, json.Unmarshal([]byte(r.stdout), &view))
	assert.Equal(t, "success", view["outcome"])
}

func TestLsJSONAutomaticForClaude(t *testing.T) {
	h := newHarness(t)
	h.run("run", "-f", "true")

	r := h.runWithEnv(map[string]string{"CLAUDE_CODE_SESSION_ID": "session-id"}, "list")
	assert.Equal(t, 0, r.exitCode)

	var views []map[string]any
	require.NoError(t, json.Unmarshal([]byte(r.stdout), &views))
	require.Len(t, views, 1)
	assert.Equal(t, "success", views[0]["outcome"])
}

func TestLsJSONAutomaticForCodex(t *testing.T) {
	h := newHarness(t)
	h.run("run", "-f", "true")

	r := h.runWithEnv(map[string]string{"CODEX_THREAD_ID": "thread-id"}, "list")
	assert.Equal(t, 0, r.exitCode)

	var views []map[string]any
	require.NoError(t, json.Unmarshal([]byte(r.stdout), &views))
	require.Len(t, views, 1)
	assert.Equal(t, "success", views[0]["outcome"])
}

func TestShowAutomaticJSONCanBeDisabled(t *testing.T) {
	h := newHarness(t)
	h.run("run", "-f", "true")

	r := h.runWithEnv(map[string]string{"CLAUDECODE": "1"}, "show", "--json=false")
	assert.Equal(t, 0, r.exitCode)
	assert.Contains(t, r.stdout, "Key:")
	assert.False(t, json.Valid([]byte(r.stdout)))
}

func TestLsKeysOverrideAutomaticJSON(t *testing.T) {
	h := newHarness(t)
	h.run("run", "-f", "true")
	key := h.lastJob().Key

	r := h.runWithEnv(map[string]string{"JOB_AGENT": "true"}, "list", "--keys")
	assert.Equal(t, 0, r.exitCode)
	assert.Equal(t, key+"\n", r.stdout)
}
