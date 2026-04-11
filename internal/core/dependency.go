package core

import (
	"errors"
	"fmt"
	"time"

	"job/internal/db"
	"job/internal/model"
)

// ErrDepFailed is returned by WaitForDeps when an after_success dependency
// completes with a non-zero exit code.
var ErrDepFailed = errors.New("dependency failed")

// WaitForDeps blocks until all of j's dependencies are satisfied, polling
// the store every second. Returns ErrDepFailed if an after_success dep exits
// non-zero.
func WaitForDeps(store db.JobStore, j *model.Job) error {
	for _, dep := range j.Deps {
		if err := pollDep(store, dep); err != nil {
			return err
		}
	}
	return nil
}

func pollDep(store db.JobStore, dep model.Dep) error {
	for {
		depJob, err := store.Get(dep.Key)
		if err != nil {
			return fmt.Errorf("fetching dep %s: %w", dep.Key, err)
		}
		if depJob.Status == model.StatusCompleted {
			if dep.Kind == model.DepAfterSuccess {
				if depJob.ExitCode == nil || *depJob.ExitCode != 0 {
					return ErrDepFailed
				}
			}
			return nil
		}
		time.Sleep(time.Second)
	}
}
