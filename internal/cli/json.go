package cli

import (
	"encoding/json"
	"os"
	"time"

	"job/internal/model"
)

// depView is the JSON shape of a Dep.
type depView struct {
	Key  string `json:"key"`
	Kind string `json:"kind"`
}

// jobView is the stable JSON shape of a Job, shared across every JSON output mode.
type jobView struct {
	Key       string     `json:"key"`
	Alias     string     `json:"alias"`
	Command   []string   `json:"command"`
	WorkDir   string     `json:"work_dir"`
	LogFile   string     `json:"log_file"`
	Status    string     `json:"status"`
	Reason    string     `json:"reason"`
	Outcome   string     `json:"outcome"`
	ExitCode  *int       `json:"exit_code"`
	PID       int        `json:"pid"`
	Context   string     `json:"context"`
	Automated bool       `json:"automated"`
	CreatedAt time.Time  `json:"created_at"`
	StartedAt *time.Time `json:"started_at"`
	StoppedAt *time.Time `json:"stopped_at"`
	Deps      []depView  `json:"deps"`
}

// jobOutcome collapses a job's lifecycle into a single token a caller can branch on:
// "success" / "failed" / "stopped" / "dep_failed" once completed, otherwise the raw
// (non-terminal) status. Distinct from the display-only jobStatusText in ls.go.
func jobOutcome(j *model.Job) string {
	if j.Status != model.StatusCompleted {
		return string(j.Status)
	}
	switch j.Reason {
	case model.ReasonStopped:
		return "stopped"
	case model.ReasonDepFailed:
		return "dep_failed"
	case model.ReasonExited:
		if j.ExitCode != nil && *j.ExitCode == 0 {
			return "success"
		}
		return "failed"
	default:
		return string(j.Reason)
	}
}

// toJobView converts a Job to its JSON view.
func toJobView(j *model.Job) jobView {
	deps := make([]depView, len(j.Deps))
	for i, d := range j.Deps {
		deps[i] = depView{Key: d.Key, Kind: string(d.Kind)}
	}
	return jobView{
		Key:       j.Key,
		Alias:     j.Alias,
		Command:   j.Command,
		WorkDir:   j.WorkDir,
		LogFile:   j.LogFile,
		Status:    string(j.Status),
		Reason:    string(j.Reason),
		Outcome:   jobOutcome(j),
		ExitCode:  j.ExitCode,
		PID:       j.PID,
		Context:   j.Context,
		Automated: j.Automated,
		CreatedAt: j.CreatedAt,
		StartedAt: j.StartedAt,
		StoppedAt: j.StoppedAt,
		Deps:      deps,
	}
}

// printJSON writes v to stdout as indented JSON.
func printJSON(v any) error {
	enc := json.NewEncoder(os.Stdout)
	enc.SetIndent("", "  ")
	enc.SetEscapeHTML(false)
	return enc.Encode(v)
}
