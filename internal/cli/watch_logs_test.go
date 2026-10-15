package cli

import (
	"bytes"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"job/internal/db"
	"job/internal/model"
)

func TestWatchJobStreamsBeforeCompletion(t *testing.T) {
	d := openTestDB(t)
	j := makeTestJob("running", model.StatusRunning)
	j.LogFile = filepath.Join(t.TempDir(), "log")
	require.NoError(t, os.WriteFile(j.LogFile, []byte("existing"), 0o600))
	require.NoError(t, d.Insert(j))

	var output bytes.Buffer
	completed := false
	writer := watchWriterFunc(func(p []byte) (int, error) {
		n, err := output.Write(p)
		if !completed && strings.Contains(output.String(), "existing") {
			// The job cannot finish until its partial line has been streamed.
			completed = true
			f, err := os.OpenFile(j.LogFile, os.O_WRONLY|os.O_APPEND, 0)
			require.NoError(t, err)
			_, err = f.WriteString("\nfinal")
			require.NoError(t, err)
			require.NoError(t, f.Close())
			j.Status = model.StatusCompleted
			j.Reason = model.ReasonExited
			j.ExitCode = new(0)
			require.NoError(t, d.Update(j))
		}
		return n, err
	})
	store := &boundedWatchStore{JobStore: d}
	got, err := watchJob(store, j.Key, writer, 0)
	require.NoError(t, err)
	assert.Equal(t, j.Key, got.Key)
	assert.Equal(t, model.StatusCompleted, got.Status)
	assert.Equal(t, "existing\nfinal", output.String())
}

func TestWatchJobReplaysCompletedLogExactly(t *testing.T) {
	d := openTestDB(t)
	j := makeTestJob("done", model.StatusCompleted)
	j.LogFile = filepath.Join(t.TempDir(), "log")
	// Include a line longer than a Scanner's default limit and no final newline.
	contents := "first\n" + strings.Repeat("x", 100_000) + "\nlast"
	require.NoError(t, os.WriteFile(j.LogFile, []byte(contents), 0o600))
	require.NoError(t, d.Insert(j))

	var output bytes.Buffer
	_, err := watchJob(d, j.Key, &output, time.Millisecond)
	require.NoError(t, err)
	assert.Equal(t, contents, output.String())
}

func TestWatchJobErrors(t *testing.T) {
	d := openTestDB(t)
	j := makeTestJob("done", model.StatusCompleted)
	j.LogFile = filepath.Join(t.TempDir(), "log")
	require.NoError(t, d.Insert(j))

	_, err := watchJob(d, "missing", io.Discard, 0)
	assert.ErrorIs(t, err, db.ErrNotFound)
	_, err = watchJob(d, j.Key, io.Discard, 0)
	assert.ErrorIs(t, err, os.ErrNotExist)

	require.NoError(t, os.WriteFile(j.LogFile, []byte("output"), 0o600))
	writeErr := errors.New("output closed")
	writer := watchWriterFunc(func([]byte) (int, error) { return 0, writeErr })
	_, err = watchJob(d, j.Key, writer, 0)
	assert.ErrorIs(t, err, writeErr)
}

type watchWriterFunc func([]byte) (int, error)

func (f watchWriterFunc) Write(p []byte) (int, error) { return f(p) }

// Bound polling so a streaming regression fails instead of hanging.
type boundedWatchStore struct {
	db.JobStore
	reads int
}

func (s *boundedWatchStore) Get(key string) (*model.Job, error) {
	s.reads++
	if s.reads > 20 {
		return nil, errors.New("job did not complete after streaming its log")
	}
	return s.JobStore.Get(key)
}
