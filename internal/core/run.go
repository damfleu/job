package core

import (
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"os/signal"
	"os/user"
	"sync/atomic"
	"syscall"
	"time"

	"github.com/charmbracelet/x/term"
	"golang.org/x/sys/unix"

	"job/internal/db"
	"job/internal/logfile"
	"job/internal/model"
	"job/internal/notify"
)

// RunOptions configures job creation.
type RunOptions struct {
	Alias     string
	Deps      []model.Dep
	WorkDir   string   // if empty, defaults to os.Getwd()
	Notifiers []string // programs to call on completion
	Context   string   // workspace context string
	Automated bool     // true when spawned by a script or sequence, not a human
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
		Context:   opts.Context,
		CreatedAt: now,
		Deps:      opts.Deps,
		Automated: opts.Automated,
	}

	if err := store.Insert(j); err != nil {
		return 0, err
	}

	if len(opts.Deps) > 0 {
		j.Status = model.StatusBlocked
		if err := store.Update(j); err != nil {
			return 0, err
		}
		if err := WaitForDeps(store, j); err != nil {
			if errors.Is(err, ErrDepFailed) {
				j.Status = model.StatusCompleted
				j.Reason = model.ReasonDepFailed
				j.StoppedAt = new(time.Now().UTC())
				_ = store.Update(j)
				return 0, fmt.Errorf("dependency failed")
			}
			return 0, err
		}

		// Re-read in case the job was stopped while waiting for deps.
		current, err := store.Get(key)
		if err != nil {
			return 0, err
		}
		if current.Status == model.StatusCompleted {
			return 0, nil
		}
		j = current
	}

	lf, err := logfile.Create(stateDir, key)
	if err != nil {
		return 0, err
	}
	defer lf.Close()

	cmd := exec.Command(command[0], command[1:]...)
	cmd.Dir = workDir
	cmd.Stdin = os.Stdin
	cmd.Stdout = io.MultiWriter(os.Stdout, lf)
	cmd.Stderr = io.MultiWriter(os.Stderr, lf)
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}

	terminalFD := int(os.Stdin.Fd())
	parentPGID := syscall.Getpgrp()
	ownsTerminal := terminalForeground(terminalFD, parentPGID)
	if ownsTerminal {
		cmd.SysProcAttr.Foreground = true
		cmd.SysProcAttr.Ctty = terminalFD
	}

	signals := make(chan os.Signal, 4)
	signal.Notify(signals, os.Interrupt, syscall.SIGTERM, syscall.SIGHUP, syscall.SIGQUIT)

	if err := cmd.Start(); err != nil {
		signal.Stop(signals)
		return 0, markFailed(store, j, err)
	}
	restorePending := ownsTerminal
	if restorePending {
		defer func() {
			if restorePending {
				restoreTerminal(terminalFD, parentPGID)
			}
		}()
	}

	pgid, err := syscall.Getpgid(cmd.Process.Pid)
	if err != nil {
		signal.Stop(signals)
		_ = cmd.Process.Kill()
		_ = cmd.Wait()
		return 0, fmt.Errorf("getting foreground process group: %w", err)
	}

	j.Status = model.StatusRunning
	j.PID = cmd.Process.Pid
	j.PGID = pgid
	j.StartedAt = new(time.Now().UTC())
	if err := store.Update(j); err != nil {
		signal.Stop(signals)
		_ = syscall.Kill(-pgid, syscall.SIGKILL)
		_ = cmd.Wait()
		return 0, err
	}

	var interrupted atomic.Bool
	signalsDone := make(chan struct{})
	go forwardSignals(signals, signalsDone, pgid, &interrupted)

	runErr := cmd.Wait()
	signal.Stop(signals)
	close(signalsDone)
	if restorePending {
		restoreTerminal(terminalFD, parentPGID)
		restorePending = false
	}
	exitCode, terminated := foregroundExit(runErr)
	_ = lf.Sync()

	// StopJob records the stopped state before signaling the process. Re-read it
	// so this waiter does not overwrite that state after the child exits.
	current, err := store.Get(key)
	if err != nil {
		return 0, err
	}
	j = current
	if j.Status != model.StatusCompleted || j.Reason != model.ReasonStopped {
		j.Status = model.StatusCompleted
		if interrupted.Load() || terminated {
			j.Reason = model.ReasonStopped
		} else {
			j.Reason = model.ReasonExited
		}
		j.ExitCode = &exitCode
		j.StoppedAt = new(time.Now().UTC())
		j.PID = 0
		j.PGID = 0

		if err := store.Update(j); err != nil {
			return 0, err
		}
	}

	notify.Fire(opts.Notifiers, j)

	if runErr != nil {
		if _, ok := errors.AsType[*exec.ExitError](runErr); ok {
			return exitCode, nil // non-zero exit is expected, not an infra error
		}
		return exitCode, fmt.Errorf("waiting for foreground command: %w", runErr)
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
		Context:   opts.Context,
		CreatedAt: now,
		Deps:      opts.Deps,
		Automated: opts.Automated,
	}

	if err := store.Insert(j); err != nil {
		return "", err
	}

	// pre-create the log file so it exists before __exec opens it
	lf, err := logfile.Create(stateDir, key)
	if err != nil {
		return "", err
	}

	args := []string{"__exec", key}
	for _, p := range opts.Notifiers {
		args = append(args, "--notifier", p)
	}
	child := exec.Command(os.Args[0], args...)
	child.SysProcAttr = &syscall.SysProcAttr{Setsid: true}
	child.Env = os.Environ()
	child.Stderr = lf
	if err := child.Start(); err != nil {
		lf.Close()
		return "", fmt.Errorf("spawning background job: %w", err)
	}
	lf.Close()

	return key, nil
}

func terminalForeground(fd, pgid int) bool {
	if !term.IsTerminal(uintptr(fd)) {
		return false
	}
	foregroundPGID, err := unix.IoctlGetInt(fd, unix.TIOCGPGRP)
	return err == nil && foregroundPGID == pgid
}

func restoreTerminal(fd, pgid int) {
	wasIgnored := signal.Ignored(syscall.SIGTTOU)
	if !wasIgnored {
		signal.Ignore(syscall.SIGTTOU)
		defer signal.Reset(syscall.SIGTTOU)
	}
	_ = unix.IoctlSetPointerInt(fd, unix.TIOCSPGRP, pgid)
}

func forwardSignals(signals <-chan os.Signal, done <-chan struct{}, pgid int, interrupted *atomic.Bool) {
	for {
		select {
		case sig := <-signals:
			select {
			case <-done:
				return
			default:
			}
			interrupted.Store(true)
			if unixSignal, ok := sig.(syscall.Signal); ok {
				_ = syscall.Kill(-pgid, unixSignal)
			}
		case <-done:
			return
		}
	}
}

func foregroundExit(err error) (exitCode int, terminated bool) {
	if err == nil {
		return 0, false
	}
	exitErr, ok := errors.AsType[*exec.ExitError](err)
	if !ok {
		return 1, false
	}
	status, ok := exitErr.Sys().(syscall.WaitStatus)
	if !ok || !status.Signaled() {
		return exitErr.ExitCode(), false
	}
	sig := status.Signal()
	switch sig {
	case syscall.SIGINT, syscall.SIGTERM, syscall.SIGHUP, syscall.SIGQUIT:
		terminated = true
	}
	return 128 + int(sig), terminated
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
