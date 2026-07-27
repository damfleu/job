package core

import (
	"errors"
	"fmt"
	"syscall"
	"time"

	"job/internal/db"
	"job/internal/model"
)

// StopJob sends SIGTERM to the job's process group, waits up to 5 seconds, then escalates to
// SIGKILL. Updates the DB regardless of signal outcome.
func StopJob(store db.JobStore, key string) error {
	j, err := store.Get(key)
	if err != nil {
		return err
	}

	shouldSignal := false
	switch j.Status {
	case model.StatusCompleted:
		return fmt.Errorf("job %s is already completed", key)
	case model.StatusRunning:
		shouldSignal = j.PGID != 0
	case model.StatusPending, model.StatusBlocked:
		// no process yet, just mark it stopped
	}

	j.Status = model.StatusCompleted
	j.Reason = model.ReasonStopped
	j.StoppedAt = new(time.Now().UTC())
	pgid := j.PGID
	j.PID = 0
	j.PGID = 0

	// Persist the user's intent before signaling. The process waiter can then
	// observe and preserve ReasonStopped instead of racing to write ReasonExited.
	updateErr := store.Update(j)
	if shouldSignal {
		killProcessGroup(pgid)
	}
	return updateErr
}

// killProcessGroup sends SIGTERM to the process group and waits up to 5s before escalating to
// SIGKILL.
func killProcessGroup(pgid int) {
	killProcessGroupWithGrace(pgid, 5*time.Second)
}

func killProcessGroupWithGrace(pgid int, gracePeriod time.Duration) {
	_ = syscall.Kill(-pgid, syscall.SIGTERM)

	deadline := time.Now().Add(gracePeriod)
	for time.Now().Before(deadline) {
		time.Sleep(100 * time.Millisecond)
		if err := syscall.Kill(-pgid, 0); errors.Is(err, syscall.ESRCH) {
			return // process group gone
		}
	}

	_ = syscall.Kill(-pgid, syscall.SIGKILL)
}
