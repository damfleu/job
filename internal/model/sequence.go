package model

import (
	"fmt"
	"time"
)

const SequenceFormatVersion = 2

// Sequence is a named, self-contained workflow definition that can be replayed
// as a unit.
type Sequence struct {
	Name      string
	Steps     []SequenceStep
	CreatedAt time.Time
}

// SequenceStep is one persisted step in a sequence. ID is local to the
// sequence and dependencies refer to other local step IDs.
type SequenceStep struct {
	ID      int           `json:"id"`
	Command []string      `json:"command"`
	WorkDir string        `json:"work_dir"`
	Deps    []SequenceDep `json:"deps,omitempty"`
}

// SequenceDep is a dependency edge between two steps in the same sequence.
type SequenceDep struct {
	StepID int     `json:"step_id"`
	Kind   DepKind `json:"kind"`
}

// ValidateSequenceSteps validates both the contents of a sequence definition
// and its stored topological order.
func ValidateSequenceSteps(steps []SequenceStep) error {
	if len(steps) == 0 {
		return fmt.Errorf("sequence has no steps")
	}

	seen := make(map[int]struct{}, len(steps))
	for i, step := range steps {
		if step.ID <= 0 {
			return fmt.Errorf("step %d has invalid ID %d", i+1, step.ID)
		}
		if _, exists := seen[step.ID]; exists {
			return fmt.Errorf("duplicate step ID %d", step.ID)
		}
		if len(step.Command) == 0 {
			return fmt.Errorf("step %d has an empty command", step.ID)
		}
		for _, dep := range step.Deps {
			if dep.StepID == step.ID {
				return fmt.Errorf("step %d depends on itself", step.ID)
			}
			if dep.Kind != DepAfter && dep.Kind != DepAfterSuccess {
				return fmt.Errorf("step %d has invalid dependency kind %q", step.ID, dep.Kind)
			}
			if _, exists := seen[dep.StepID]; !exists {
				return fmt.Errorf(
					"step %d depends on missing or later step %d",
					step.ID,
					dep.StepID,
				)
			}
		}
		seen[step.ID] = struct{}{}
	}
	return nil
}
