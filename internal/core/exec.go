package core

import (
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"syscall"
	"time"

	"job/internal/db"
	"job/internal/model"
	"job/internal/notify"
	"job/internal/permissions"
)

// RunBackground is called by the __exec child process. It loads the job, runs the command with
// output going to the log file, and records the result.
func RunBackground(store db.JobStore, stateDir, key string, notifiers []notify.Notifier) error {
	j, err := store.Get(key)
	if err != nil {
		return err
	}

	if len(j.Deps) > 0 {
		j.Status = model.StatusBlocked
		_ = store.Update(j) // best-effort: job will wait for deps regardless

		if err := WaitForDeps(store, j); err != nil {
			if errors.Is(err, ErrDepFailed) {
				return markDepFailed(store, j)
			}
			return markInternalError(store, j, err)
		}

		// Re-read in case the job was stopped while waiting for deps.
		current, err := store.Get(key)
		if err != nil {
			return markInternalError(
				store,
				j,
				fmt.Errorf("reloading job after dependencies: %w", err),
			)
		}
		j = current
		if j.Status == model.StatusCompleted {
			return nil
		}
	}

	if err := os.MkdirAll(filepath.Dir(j.LogFile), permissions.DirMode); err != nil {
		return markInternalError(store, j, fmt.Errorf("creating log dir: %w", err))
	}
	lf, err := os.OpenFile(j.LogFile, os.O_WRONLY|os.O_CREATE|os.O_APPEND, permissions.FileMode)
	if err != nil {
		return markInternalError(store, j, fmt.Errorf("opening log file: %w", err))
	}
	defer lf.Close()

	cmd := exec.Command(j.Command[0], j.Command[1:]...)
	cmd.Dir = j.WorkDir
	cmd.Stdout = lf
	cmd.Stderr = lf
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}

	if err := cmd.Start(); err != nil {
		return markLaunchFailed(store, j, fmt.Errorf("starting command: %w", err))
	}

	j.Status = model.StatusRunning
	j.PID = cmd.Process.Pid
	j.PGID = cmd.Process.Pid // pgid == pid when Setpgid=true
	j.StartedAt = new(time.Now().UTC())
	_ = store.Update(j) // best-effort: process is running regardless

	waitErr := cmd.Wait()
	_ = lf.Sync()

	// StopJob records the stopped state before signaling the process. Preserve it
	// instead of racing to overwrite it as a normal exit.
	current, getErr := store.Get(key)
	if getErr == nil && current.Status == model.StatusCompleted && current.Reason == model.ReasonStopped {
		fireNotifiers(stateDir, notifiers, current)
		return nil
	}

	j.Status = model.StatusCompleted
	j.Reason = model.ReasonExited
	j.PID = 0
	j.PGID = 0
	j.StoppedAt = new(time.Now().UTC())

	exitCode := 0
	if waitErr != nil {
		if exitErr, ok := errors.AsType[*exec.ExitError](waitErr); ok {
			exitCode = exitErr.ExitCode()
		}
	}
	j.ExitCode = &exitCode

	// Best-effort: the job completed regardless of whether we can persist the state.
	_ = store.Update(j)
	fireNotifiers(stateDir, notifiers, j)
	return nil
}

func fireNotifiers(stateDir string, notifiers []notify.Notifier, j *model.Job) {
	if len(notifiers) == 0 {
		return
	}
	notifierLog, err := notify.OpenLog(stateDir)
	if err != nil {
		notify.Fire(notifiers, j, nil)
		return
	}
	defer notifierLog.Close()
	notify.Fire(notifiers, j, notifierLog)
}

func markDepFailed(store db.JobStore, j *model.Job) error {
	j.Status = model.StatusCompleted
	j.Reason = model.ReasonDepFailed
	j.StoppedAt = new(time.Now().UTC())
	_ = store.Update(j) // best-effort: dep-failed state is informational
	return nil
}

func markLaunchFailed(store db.JobStore, j *model.Job, launchErr error) error {
	j.Status = model.StatusCompleted
	j.Reason = model.ReasonLaunchFailed
	j.ExitCode = new(1)
	j.StoppedAt = new(time.Now().UTC())
	j.PID = 0
	j.PGID = 0
	if err := store.Update(j); err != nil {
		return errors.Join(launchErr, fmt.Errorf("recording launch failure: %w", err))
	}
	return launchErr
}

func markInternalError(store db.JobStore, j *model.Job, internalErr error) error {
	j.Status = model.StatusCompleted
	j.Reason = model.ReasonInternalError
	j.ExitCode = nil
	j.StoppedAt = new(time.Now().UTC())
	j.PID = 0
	j.PGID = 0
	if err := store.Update(j); err != nil {
		return errors.Join(internalErr, fmt.Errorf("recording internal error: %w", err))
	}
	return internalErr
}
