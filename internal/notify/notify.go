package notify

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"syscall"
	"time"

	"job/internal/model"
	"job/internal/permissions"
)

// DefaultTimeout bounds notifier execution when no timeout is configured.
const DefaultTimeout = 2 * time.Second

// Notifier describes a program to invoke and how long it may run.
type Notifier struct {
	Program string
	Timeout time.Duration
}

// LogPath returns the path of the application log for notifier failures.
func LogPath(stateDir string) string {
	return filepath.Join(stateDir, "notifier.log")
}

// OpenLog opens the application log for notifier failures.
func OpenLog(stateDir string) (*os.File, error) {
	return os.OpenFile(LogPath(stateDir), os.O_WRONLY|os.O_CREATE|os.O_APPEND, permissions.FileMode)
}

// Payload is the JSON object sent to each notifier program on stdin.
type Payload struct {
	Key     string   `json:"key"`
	Command []string `json:"command"`
	RC      *int     `json:"rc,omitempty"`
	Elapsed string   `json:"elapsed,omitempty"`
}

// Fire calls each notifier sequentially with a JSON payload on stdin describing j.
// Failures are written to notifierLog, but never change the job's outcome.
func Fire(notifiers []Notifier, j *model.Job, notifierLog io.Writer) {
	if len(notifiers) == 0 {
		return
	}
	data, err := json.Marshal(buildPayload(j))
	if err != nil {
		writeLog(notifierLog, j.Key, "encoding payload: %v", err)
		return
	}
	for _, notifier := range notifiers {
		timeout := notifier.Timeout
		if timeout <= 0 {
			timeout = DefaultTimeout
		}
		ctx, cancel := context.WithTimeout(context.Background(), timeout)
		cmd := exec.CommandContext(ctx, "sh", "-c", notifier.Program)
		cmd.Stdin = bytes.NewReader(data)
		cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
		cmd.Cancel = func() error {
			return syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL)
		}
		err := cmd.Run()
		if ctx.Err() != nil {
			writeLog(notifierLog, j.Key, "notifier %q timed out after %s", notifier.Program, timeout)
		} else if err != nil {
			writeLog(notifierLog, j.Key, "notifier %q failed: %v", notifier.Program, err)
		}
		cancel()
	}
}

func writeLog(w io.Writer, key, format string, args ...any) {
	if w != nil {
		prefix := fmt.Sprintf("%s job=%q ", time.Now().UTC().Format(time.RFC3339Nano), key)
		_, _ = fmt.Fprintf(w, prefix+format+"\n", args...)
	}
}

func buildPayload(j *model.Job) Payload {
	p := Payload{
		Key:     j.Key,
		Command: j.Command,
		RC:      j.ExitCode,
	}
	if j.StartedAt != nil && j.StoppedAt != nil {
		p.Elapsed = j.StoppedAt.Sub(*j.StartedAt).Round(time.Second).String()
	}
	return p
}
