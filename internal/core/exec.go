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
)

// RunBackground is called by the __exec child process. It loads the job, runs the command with
// output going to the log file, and records the result.
func RunBackground(store db.JobStore, key string, notifiers []string) error {
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
			return err
		}

		// Re-read in case the job was stopped while waiting for deps.
		j, err = store.Get(key)
		if err != nil {
			return err
		}
		if j.Status == model.StatusCompleted {
			return nil
		}
	}

	if err := os.MkdirAll(filepath.Dir(j.LogFile), 0o755); err != nil {
		return fmt.Errorf("creating log dir: %w", err)
	}
	lf, err := os.OpenFile(j.LogFile, os.O_WRONLY|os.O_CREATE|os.O_APPEND, 0o644)
	if err != nil {
		return fmt.Errorf("opening log file: %w", err)
	}
	defer lf.Close()

	cmd := exec.Command(j.Command[0], j.Command[1:]...)
	cmd.Dir = j.WorkDir
	cmd.Stdout = lf
	cmd.Stderr = lf
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}

	if err := cmd.Start(); err != nil {
		return markFailed(store, j, err)
	}

	j.Status = model.StatusRunning
	j.PID = cmd.Process.Pid
	j.PGID = cmd.Process.Pid // pgid == pid when Setpgid=true
	j.StartedAt = new(time.Now().UTC())
	_ = store.Update(j) // best-effort: process is running regardless

	waitErr := cmd.Wait()

	j.Status = model.StatusCompleted
	j.Reason = model.ReasonExited
	j.PID = 0
	j.StoppedAt = new(time.Now().UTC())

	exitCode := 0
	if waitErr != nil {
		if exitErr, ok := errors.AsType[*exec.ExitError](waitErr); ok {
			exitCode = exitErr.ExitCode()
		}
	}
	j.ExitCode = &exitCode

	_ = lf.Sync()
	// Best-effort: the job completed regardless of whether we can persist the state.
	_ = store.Update(j)
	notify.Fire(notifiers, j)
	return nil
}

func markDepFailed(store db.JobStore, j *model.Job) error {
	j.Status = model.StatusCompleted
	j.Reason = model.ReasonDepFailed
	j.StoppedAt = new(time.Now().UTC())
	_ = store.Update(j) // best-effort: dep-failed state is informational
	return nil
}

func markFailed(store db.JobStore, j *model.Job, startErr error) error {
	j.Status = model.StatusCompleted
	j.Reason = model.ReasonExited
	j.ExitCode = new(1)
	j.StoppedAt = new(time.Now().UTC())
	_ = store.Update(j) // best-effort: returning the start error is more informative
	return fmt.Errorf("starting command: %w", startErr)
}
