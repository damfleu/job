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

// RunBackground is called by the __exec child process. It loads the job,
// runs the command with output going to the log file, and records the result.
func RunBackground(store db.JobStore, key string, notifiers []string) error {
	j, err := store.Get(key)
	if err != nil {
		return err
	}

	if len(j.Deps) > 0 {
		j.Status = model.StatusBlocked
		_ = store.Update(j)

		if err := WaitForDeps(store, j); err != nil {
			if errors.Is(err, ErrDepFailed) {
				return markDepFailed(store, j)
			}
			return err
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

	startedAt := time.Now().UTC()
	j.Status = model.StatusRunning
	j.PID = cmd.Process.Pid
	j.PGID = cmd.Process.Pid // pgid == pid when Setpgid=true
	j.StartedAt = &startedAt
	_ = store.Update(j)

	waitErr := cmd.Wait()

	stoppedAt := time.Now().UTC()
	j.Status = model.StatusCompleted
	j.Reason = model.ReasonExited
	j.PID = 0
	j.StoppedAt = &stoppedAt

	exitCode := 0
	if waitErr != nil {
		var exitErr *exec.ExitError
		if errors.As(waitErr, &exitErr) {
			exitCode = exitErr.ExitCode()
		}
	}
	j.ExitCode = &exitCode

	_ = lf.Sync()
	_ = store.Update(j)
	notify.Fire(notifiers, j)
	return nil
}

func markDepFailed(store db.JobStore, j *model.Job) error {
	stoppedAt := time.Now().UTC()
	j.Status = model.StatusCompleted
	j.Reason = model.ReasonDepFailed
	j.StoppedAt = &stoppedAt
	_ = store.Update(j)
	return nil
}

func markFailed(store db.JobStore, j *model.Job, startErr error) error {
	stoppedAt := time.Now().UTC()
	code := 1
	j.Status = model.StatusCompleted
	j.Reason = model.ReasonExited
	j.ExitCode = &code
	j.StoppedAt = &stoppedAt
	_ = store.Update(j)
	return fmt.Errorf("starting command: %w", startErr)
}
