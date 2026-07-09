package core

import (
	"time"

	"job/internal/db"
	"job/internal/model"
)

// WaitForCompletion polls store for each key in turn until it reaches
// model.StatusCompleted (which also covers stopped/dep_failed), returning
// the final records in the same order as keys.
func WaitForCompletion(store db.JobStore, keys []string, poll time.Duration) ([]*model.Job, error) {
	jobs := make([]*model.Job, len(keys))
	for i, key := range keys {
		for {
			j, err := store.Get(key)
			if err != nil {
				return nil, err
			}
			if j.Status == model.StatusCompleted {
				jobs[i] = j
				break
			}
			time.Sleep(poll)
		}
	}
	return jobs, nil
}
