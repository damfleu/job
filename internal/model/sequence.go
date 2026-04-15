package model

import "time"

// Sequence is a named, ordered list of job keys that can be replayed as a unit.
type Sequence struct {
	Name      string
	Steps     []string // job keys in topological order (deps before dependents)
	CreatedAt time.Time
}
