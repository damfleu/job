package core

import (
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"os/user"
	"syscall"
	"time"

	"job/internal/db"
	"job/internal/logfile"
	"job/internal/model"
	"job/internal/notify"
)

// RunOptions configures job creation.
type RunOptions struct {
	Alias     string
	Verbose   bool
	Deps      []model.Dep
	WorkDir   string   // if empty, defaults to os.Getwd()
	Notifiers []string // programs to call on completion
}

// CreateAndRunForeground creates a job record, runs the command in the current process (blocking),
// tees output to the terminal and the log file, and records the result. Returns the command's exit
// code; infrastructure errors are returned as the error value.
func CreateAndRunForeground(store db.JobStore, stateDir string, command []string, opts RunOptions) (int, error) {
	key := model.GenerateKey(command[0])

	workDir := opts.WorkDir
	if workDir == "" {
		var err error
		workDir, err = os.Getwd()
		if err != nil {
			return 0, fmt.Errorf("getting work dir: %w", err)
		}
	}

	now := time.Now().UTC()
	j := &model.Job{
		Key:       key,
		Alias:     opts.Alias,
		Command:   command,
		WorkDir:   workDir,
		LogFile:   logfile.Path(stateDir, key),
		Status:    model.StatusPending,
		Hostname:  hostname(),
		Username:  username(),
		CreatedAt: now,
		Deps:      opts.Deps,
	}

	if err := store.Insert(j); err != nil {
		return 0, err
	}
	if err := store.SetLastKey(key); err != nil {
		return 0, err
	}

	if len(opts.Deps) > 0 {
		j.Status = model.StatusBlocked
		if err := store.Update(j); err != nil {
			return 0, err
		}
		if err := WaitForDeps(store, j); err != nil {
			if errors.Is(err, ErrDepFailed) {
				stoppedAt := time.Now().UTC()
				j.Status = model.StatusCompleted
				j.Reason = model.ReasonDepFailed
				j.StoppedAt = &stoppedAt
				_ = store.Update(j)
				return 0, fmt.Errorf("dependency failed")
			}
			return 0, err
		}
	}

	lf, err := logfile.Create(stateDir, key)
	if err != nil {
		return 0, err
	}
	defer lf.Close()

	startedAt := time.Now().UTC()
	j.Status = model.StatusRunning
	j.PID = os.Getpid()
	j.StartedAt = &startedAt
	if err := store.Update(j); err != nil {
		return 0, err
	}

	if opts.Verbose {
		fmt.Fprintf(os.Stderr, "%s running\n", key)
	}

	exitCode, runErr := runForeground(command, workDir, lf)

	stoppedAt := time.Now().UTC()
	j.Status = model.StatusCompleted
	j.Reason = model.ReasonExited
	j.ExitCode = &exitCode
	j.StoppedAt = &stoppedAt
	j.PID = 0

	_ = lf.Sync()

	if err := store.Update(j); err != nil {
		return 0, err
	}

	notify.Fire(opts.Notifiers, j)

	if opts.Verbose {
		fmt.Fprintf(os.Stderr, "%s done (exit %d)\n", key, exitCode)
	}

	if runErr != nil {
		return exitCode, nil // non-zero exit is expected, not an infra error
	}
	return 0, nil
}

// CreateAndSpawn creates a job record and launches it as a detached background
// process (job __exec <key>). Returns immediately; the child updates the DB.
func CreateAndSpawn(store db.JobStore, stateDir string, command []string, opts RunOptions) (string, error) {
	key := model.GenerateKey(command[0])

	workDir := opts.WorkDir
	if workDir == "" {
		var err error
		workDir, err = os.Getwd()
		if err != nil {
			return "", fmt.Errorf("getting work dir: %w", err)
		}
	}

	now := time.Now().UTC()
	j := &model.Job{
		Key:       key,
		Alias:     opts.Alias,
		Command:   command,
		WorkDir:   workDir,
		LogFile:   logfile.Path(stateDir, key),
		Status:    model.StatusPending,
		Hostname:  hostname(),
		Username:  username(),
		CreatedAt: now,
		Deps:      opts.Deps,
	}

	if err := store.Insert(j); err != nil {
		return "", err
	}
	if err := store.SetLastKey(key); err != nil {
		return "", err
	}

	// pre-create the log file so it exists before __exec opens it
	lf, err := logfile.Create(stateDir, key)
	if err != nil {
		return "", err
	}
	lf.Close()

	args := []string{"__exec", key}
	for _, p := range opts.Notifiers {
		args = append(args, "--notifier", p)
	}
	child := exec.Command(os.Args[0], args...)
	child.SysProcAttr = &syscall.SysProcAttr{Setsid: true}
	child.Env = os.Environ()
	if err := child.Start(); err != nil {
		return "", fmt.Errorf("spawning background job: %w", err)
	}

	return key, nil
}

func runForeground(command []string, workDir string, lf *os.File) (int, error) {
	cmd := exec.Command(command[0], command[1:]...)
	cmd.Dir = workDir
	cmd.Stdin = os.Stdin
	cmd.Stdout = io.MultiWriter(os.Stdout, lf)
	cmd.Stderr = io.MultiWriter(os.Stderr, lf)

	if err := cmd.Run(); err != nil {
		var exitErr *exec.ExitError
		if errors.As(err, &exitErr) {
			return exitErr.ExitCode(), err
		}
		return 1, err
	}
	return 0, nil
}

func hostname() string {
	h, _ := os.Hostname()
	return h
}

func username() string {
	u, err := user.Current()
	if err != nil {
		return ""
	}
	return u.Username
}
