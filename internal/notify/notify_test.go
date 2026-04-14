package notify_test

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"job/internal/model"
	"job/internal/notify"
)

func TestFire(t *testing.T) {
	t.Run("empty programs is a no-op", func(t *testing.T) {
		// should not panic or error
		notify.Fire(nil, &model.Job{Key: "test"})
	})

	t.Run("program receives correct JSON on stdin", func(t *testing.T) {
		out := filepath.Join(t.TempDir(), "payload.json")
		script := writeScript(t, "#!/bin/sh\ncat > "+out+"\n")

		rc := 0
		start := time.Now()
		stop := start.Add(90 * time.Second)
		j := &model.Job{
			Key:       "123_abc_make",
			Command:   []string{"make", "-j8"},
			ExitCode:  &rc,
			StartedAt: &start,
			StoppedAt: &stop,
		}
		notify.Fire([]string{script}, j)

		data, err := os.ReadFile(out)
		require.NoError(t, err)

		var p notify.Payload
		require.NoError(t, json.Unmarshal(data, &p))
		require.Equal(t, "123_abc_make", p.Key)
		require.Equal(t, []string{"make", "-j8"}, p.Command)
		require.NotNil(t, p.RC)
		require.Equal(t, 0, *p.RC)
		require.Equal(t, "1m30s", p.Elapsed)
	})

	t.Run("failing program does not propagate error", func(t *testing.T) {
		script := writeScript(t, "#!/bin/sh\nexit 1\n")
		// should not panic
		notify.Fire([]string{script}, &model.Job{Key: "test", Command: []string{"cmd"}})
	})

	t.Run("rc omitted when ExitCode is nil", func(t *testing.T) {
		out := filepath.Join(t.TempDir(), "payload.json")
		script := writeScript(t, "#!/bin/sh\ncat > "+out+"\n")

		notify.Fire([]string{script}, &model.Job{Key: "k", Command: []string{"cmd"}})

		data, err := os.ReadFile(out)
		require.NoError(t, err)

		var raw map[string]any
		require.NoError(t, json.Unmarshal(data, &raw))
		_, hasRC := raw["rc"]
		require.False(t, hasRC)
	})

	t.Run("elapsed omitted when times are nil", func(t *testing.T) {
		out := filepath.Join(t.TempDir(), "payload.json")
		script := writeScript(t, "#!/bin/sh\ncat > "+out+"\n")

		notify.Fire([]string{script}, &model.Job{Key: "k", Command: []string{"cmd"}})

		data, err := os.ReadFile(out)
		require.NoError(t, err)

		var raw map[string]any
		require.NoError(t, json.Unmarshal(data, &raw))
		_, hasElapsed := raw["elapsed"]
		require.False(t, hasElapsed)
	})
}

func writeScript(t *testing.T, content string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "notifier.sh")
	require.NoError(t, os.WriteFile(path, []byte(content), 0o755))
	return path
}
