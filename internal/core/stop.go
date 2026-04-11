package core

import (
	"fmt"
	"syscall"
	"time"

	"job/internal/db"
	"job/internal/model"
)

// StopJob sends SIGTERM to the job's process group, waits up to 5 seconds,
// then escalates to SIGKILL. Updates the DB regardless of signal outcome.
func StopJob(store db.JobStore, key string) error {
	j, err := store.Get(key)
	if err != nil {
		return err
	}

	switch j.Status {
	case model.StatusCompleted:
		return fmt.Errorf("job %s is already completed", key)
	case model.StatusRunning:
		if j.PGID != 0 {
			killProcessGroup(j.PGID)
		}
	case model.StatusPending, model.StatusBlocked:
		// no process yet, just mark it stopped
	}

	now := time.Now().UTC()
	j.Status = model.StatusCompleted
	j.Reason = model.ReasonStopped
	j.StoppedAt = &now
	j.PID = 0

	return store.Update(j)
}

// killProcessGroup sends SIGTERM to the process group and waits up to 5s
// before escalating to SIGKILL.
func killProcessGroup(pgid int) {
	_ = syscall.Kill(-pgid, syscall.SIGTERM)

	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		time.Sleep(100 * time.Millisecond)
		if err := syscall.Kill(pgid, 0); err != nil {
			return // process gone
		}
	}

	_ = syscall.Kill(-pgid, syscall.SIGKILL)
}
