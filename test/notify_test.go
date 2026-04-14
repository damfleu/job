package integration

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"job/internal/model"
	"job/internal/notify"
)

// setupNotifier creates a harness with an explicit notifier script and returns
// the harness and the path where the notifier writes its JSON payload.
func setupNotifier(t *testing.T) (h *harness, outFile string) {
	t.Helper()
	h = newHarness(t)
	outFile = filepath.Join(t.TempDir(), "notify.json")
	script := h.writeScript("cat > " + outFile)
	h.writeConfig("[[notifier]]\nprogram = \"" + script + "\"\nnotify = \"explicit\"\n")
	return h, outFile
}

func readPayload(t *testing.T, outFile string) notify.Payload {
	t.Helper()
	data, err := os.ReadFile(outFile)
	require.NoError(t, err)
	var p notify.Payload
	require.NoError(t, json.Unmarshal(data, &p))
	return p
}

func TestNotifyForeground(t *testing.T) {
	h, outFile := setupNotifier(t)

	r := h.run("run", "-f", "-n", "echo", "hello")
	assert.Equal(t, 0, r.exitCode)

	p := readPayload(t, outFile)
	assert.Equal(t, []string{"echo", "hello"}, p.Command)
	require.NotNil(t, p.RC)
	assert.Equal(t, 0, *p.RC)
	assert.NotEmpty(t, p.Key)
	assert.NotEmpty(t, p.Elapsed)
}

func TestNotifyForegroundNonZeroExit(t *testing.T) {
	h, outFile := setupNotifier(t)

	h.run("run", "-f", "-n", "false")

	p := readPayload(t, outFile)
	require.NotNil(t, p.RC)
	assert.Equal(t, 1, *p.RC)
}

func TestNotifyBackground(t *testing.T) {
	h, outFile := setupNotifier(t)

	r := h.run("run", "-v", "-n", "echo", "bg notify")
	key := strings.TrimSpace(r.stderr)
	require.NotEmpty(t, key)
	h.waitFor(key, model.StatusCompleted)
	waitForFile(t, outFile)

	p := readPayload(t, outFile)
	assert.Equal(t, []string{"echo", "bg notify"}, p.Command)
	require.NotNil(t, p.RC)
	assert.Equal(t, 0, *p.RC)
}

func TestNotifyAlways(t *testing.T) {
	h := newHarness(t)
	outFile := filepath.Join(t.TempDir(), "notify.json")
	script := h.writeScript("cat > " + outFile)
	h.writeConfig("[[notifier]]\nprogram = \"" + script + "\"\nnotify = \"always\"\n")

	// no -n flag — should still notify because notify = "always"
	h.run("run", "-f", "echo", "always")

	p := readPayload(t, outFile)
	assert.Equal(t, []string{"echo", "always"}, p.Command)
}

func TestNotifyExplicitRequiresFlag(t *testing.T) {
	h := newHarness(t)
	outFile := filepath.Join(t.TempDir(), "notify.json")
	script := h.writeScript("cat > " + outFile)
	h.writeConfig("[[notifier]]\nprogram = \"" + script + "\"\nnotify = \"explicit\"\n")

	// no -n flag — notifier should NOT be called
	h.run("run", "-f", "echo", "no notify")

	_, err := os.ReadFile(outFile)
	assert.True(t, os.IsNotExist(err), "notifier should not have been called without -n")
}
