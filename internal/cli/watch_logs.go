package cli

import (
	"fmt"
	"io"
	"os"
	"time"

	"job/internal/db"
	"job/internal/model"
)

// watchJob replays and follows a job's log until completion, returning its
// final record. Output is copied verbatim, including partial lines.
func watchJob(store db.JobStore, key string, output io.Writer, poll time.Duration) (*model.Job, error) {
	j, err := store.Get(key)
	if err != nil {
		return nil, err
	}
	f, err := os.Open(j.LogFile)
	if err != nil {
		return nil, fmt.Errorf("opening log for %s: %w", key, err)
	}
	defer f.Close()

	for {
		j, err := store.Get(key)
		if err != nil {
			return nil, err
		}
		// Read after checking status so the final copy includes all output
		// written before completion was recorded.
		if _, err := io.Copy(output, f); err != nil {
			return nil, fmt.Errorf("reading log for %s: %w", key, err)
		}
		if j.Status == model.StatusCompleted {
			return j, nil
		}
		time.Sleep(poll)
	}
}
