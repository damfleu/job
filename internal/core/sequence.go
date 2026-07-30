package core

import (
	"fmt"
	"sort"
	"strconv"
	"time"

	"job/internal/db"
	"job/internal/model"
)

// SaveSequence walks the transitive dependency graph of all provided jobs, topologically sorts the
// result (deps before dependents), and stores it as a named sequence.
func SaveSequence(store db.JobStore, name string, jobs []*model.Job) error {
	visited := map[string]*model.Job{}
	queue := make([]*model.Job, len(jobs))
	copy(queue, jobs)

	for len(queue) > 0 {
		curr := queue[0]
		queue = queue[1:]
		if _, seen := visited[curr.Key]; seen {
			continue
		}
		visited[curr.Key] = curr
		for _, dep := range curr.Deps {
			if _, seen := visited[dep.Key]; !seen {
				depJob, err := store.Get(dep.Key)
				if err != nil {
					return fmt.Errorf("sequence: fetching dep %s: %w", dep.Key, err)
				}
				queue = append(queue, depJob)
			}
		}
	}

	order, err := topoSort(visited)
	if err != nil {
		return err
	}

	stepIDs := make(map[string]int, len(order))
	for i, key := range order {
		stepIDs[key] = i + 1
	}

	steps := make([]model.SequenceStep, len(order))
	for i, key := range order {
		job := visited[key]
		deps := make([]model.SequenceDep, len(job.Deps))
		for j, dep := range job.Deps {
			stepID, ok := stepIDs[dep.Key]
			if !ok {
				return fmt.Errorf("sequence: dependency %s is outside the captured graph", dep.Key)
			}
			deps[j] = model.SequenceDep{StepID: stepID, Kind: dep.Kind}
		}
		steps[i] = model.SequenceStep{
			ID:      stepIDs[key],
			Command: append([]string(nil), job.Command...),
			WorkDir: job.WorkDir,
			Deps:    deps,
		}
	}
	if err := model.ValidateSequenceSteps(steps); err != nil {
		return fmt.Errorf("sequence: %w", err)
	}

	return store.SaveSequence(&model.Sequence{
		Name:      name,
		Steps:     steps,
		CreatedAt: time.Now().UTC(),
	})
}

// RunStep describes one step that is ready to be spawned. Dependency keys
// refer to other RunStep IDs and are remapped to new job keys by the caller.
// Retry cascades may additionally retain dependencies on jobs outside the set.
type RunStep struct {
	ID      string
	Command []string
	WorkDir string
	Deps    []model.Dep
}

// ExpandSequence loads a named sequence and returns its steps in topological order as plain
// descriptors, without spawning anything. When workDirOverride is non-empty it
// replaces each step's original WorkDir.
func ExpandSequence(store db.JobStore, name, workDirOverride string) ([]RunStep, error) {
	seq, err := store.GetSequence(name)
	if err != nil {
		return nil, err
	}

	steps := make([]RunStep, len(seq.Steps))
	for i, step := range seq.Steps {
		workDir := step.WorkDir
		if workDirOverride != "" {
			workDir = workDirOverride
		}
		deps := make([]model.Dep, len(step.Deps))
		for j, dep := range step.Deps {
			deps[j] = model.Dep{Key: strconv.Itoa(dep.StepID), Kind: dep.Kind}
		}
		steps[i] = RunStep{
			ID:      strconv.Itoa(step.ID),
			Command: append([]string(nil), step.Command...),
			WorkDir: workDir,
			Deps:    deps,
		}
	}
	return steps, nil
}

// topoSort performs Kahn's algorithm on a map of jobs, returning keys ordered so that every
// dependency appears before the jobs that depend on it.
func topoSort(jobs map[string]*model.Job) ([]string, error) {
	inDegree := make(map[string]int, len(jobs))
	dependents := make(map[string][]string, len(jobs))

	for key, job := range jobs {
		if _, ok := inDegree[key]; !ok {
			inDegree[key] = 0
		}
		for _, dep := range job.Deps {
			if _, inSet := jobs[dep.Key]; inSet {
				inDegree[key]++
				dependents[dep.Key] = append(dependents[dep.Key], key)
			}
		}
	}

	ready := make([]string, 0, len(jobs))
	for key, deg := range inDegree {
		if deg == 0 {
			ready = append(ready, key)
		}
	}
	sort.Strings(ready)

	steps := make([]string, 0, len(jobs))
	for len(ready) > 0 {
		curr := ready[0]
		ready = ready[1:]
		steps = append(steps, curr)

		next := dependents[curr]
		sort.Strings(next)
		for _, dep := range next {
			inDegree[dep]--
			if inDegree[dep] == 0 {
				ready = append(ready, dep)
			}
		}
		sort.Strings(ready)
	}

	if len(steps) != len(jobs) {
		return nil, fmt.Errorf("sequence: cycle detected in dependency graph")
	}
	return steps, nil
}
