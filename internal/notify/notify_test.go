package notify_test

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"strconv"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"job/internal/model"
	"job/internal/notify"
)

func TestFire(t *testing.T) {
	t.Run("empty programs is a no-op", func(t *testing.T) {
		// should not panic or error
		notify.Fire(nil, &model.Job{Key: "test"}, nil)
	})

	t.Run("program receives correct JSON on stdin", func(t *testing.T) {
		out := filepath.Join(t.TempDir(), "payload.json")
		script := writeScript(t, "#!/bin/sh\ncat > "+out+"\n")

		start := time.Now()
		j := &model.Job{
			Key:       "123_abc_make",
			Command:   []string{"make", "-j8"},
			ExitCode:  new(0),
			StartedAt: &start,
			StoppedAt: new(start.Add(90 * time.Second)),
		}
		notify.Fire([]notify.Notifier{{Program: script, Timeout: time.Second}}, j, nil)

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
		var diagnostics bytes.Buffer
		notify.Fire(
			[]notify.Notifier{{Program: script, Timeout: time.Second}},
			&model.Job{Key: "test", Command: []string{"cmd"}},
			&diagnostics,
		)
		require.Contains(t, diagnostics.String(), "notifier "+strconv.Quote(script)+" failed: exit status 1")
	})

	t.Run("timed out program does not prevent later notifier", func(t *testing.T) {
		out := filepath.Join(t.TempDir(), "second-ran")
		second := writeScript(t, "#!/bin/sh\ntouch "+out+"\n")
		var diagnostics bytes.Buffer

		start := time.Now()
		notify.Fire(
			[]notify.Notifier{
				{Program: "sleep 10 & wait", Timeout: 25 * time.Millisecond},
				{Program: second, Timeout: time.Second},
			},
			&model.Job{Key: "test", Command: []string{"cmd"}},
			&diagnostics,
		)

		require.Less(t, time.Since(start), time.Second)
		require.FileExists(t, out)
		require.Contains(t, diagnostics.String(), `notifier "sleep 10 & wait" timed out after 25ms`)
	})

	t.Run("rc omitted when ExitCode is nil", func(t *testing.T) {
		out := filepath.Join(t.TempDir(), "payload.json")
		script := writeScript(t, "#!/bin/sh\ncat > "+out+"\n")

		notify.Fire([]notify.Notifier{{Program: script, Timeout: time.Second}}, &model.Job{Key: "k", Command: []string{"cmd"}}, nil)

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

		notify.Fire([]notify.Notifier{{Program: script, Timeout: time.Second}}, &model.Job{Key: "k", Command: []string{"cmd"}}, nil)

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
