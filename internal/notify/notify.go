package notify

import (
	"bytes"
	"encoding/json"
	"os/exec"
	"time"

	"job/internal/model"
)

// Payload is the JSON object sent to each notifier program on stdin.
type Payload struct {
	Key     string   `json:"key"`
	Command []string `json:"command"`
	RC      *int     `json:"rc,omitempty"`
	Elapsed string   `json:"elapsed,omitempty"`
}

// Fire calls each program in programs with a JSON payload on stdin describing j.
// Errors are silently ignored — a broken notifier never fails the job.
func Fire(programs []string, j *model.Job) {
	if len(programs) == 0 {
		return
	}
	data, err := json.Marshal(buildPayload(j))
	if err != nil {
		return
	}
	for _, prog := range programs {
		cmd := exec.Command("sh", "-c", prog)
		cmd.Stdin = bytes.NewReader(data)
		_ = cmd.Run()
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
