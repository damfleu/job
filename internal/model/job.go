package model

import "time"

// Status is the lifecycle state of a job.
type Status string

const (
	StatusPending   Status = "pending"
	StatusBlocked   Status = "blocked"
	StatusRunning   Status = "running"
	StatusCompleted Status = "completed"
)

// Reason is why a job completed.
type Reason string

const (
	ReasonExited    Reason = "exited"
	ReasonStopped   Reason = "stopped"
	ReasonDepFailed Reason = "dep_failed"
)

// Job is the record for a single tracked job.
type Job struct {
	Key     string
	Alias   string   // -k flag, optional
	Command []string // program + args
	WorkDir string
	LogFile string

	Status   Status
	Reason   Reason // set on completion
	ExitCode *int   // nil if job never ran

	PID  int // 0 when not running
	PGID int // process group ID

	Hostname string
	Username string
	Context  string

	CreatedAt time.Time
	StartedAt *time.Time
	StoppedAt *time.Time

	Deps []Dep
}

// DepKind is the condition under which a dependency is considered satisfied.
type DepKind string

const (
	DepAfter        DepKind = "after"         // run after dep completes (any exit code)
	DepAfterSuccess DepKind = "after_success" // run only if dep exits with rc=0
)

// Dep is a dependency on another job.
type Dep struct {
	Key  string
	Kind DepKind
}
