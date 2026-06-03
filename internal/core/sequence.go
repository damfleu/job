package core

import (
	"fmt"
	"sort"
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

	steps, err := topoSort(visited)
	if err != nil {
		return err
	}

	return store.SaveSequence(&model.Sequence{
		Name:      name,
		Steps:     steps,
		CreatedAt: time.Now().UTC(),
	})
}

// SequenceStep describes one step of a sequence as returned by ExpandSequence. Deps still
// reference the original job keys from when the sequence was saved; the caller is responsible
// for remapping them to the keys of the newly spawned jobs.
type SequenceStep struct {
	OriginalKey string
	Command     []string
	WorkDir     string
	Context     string
	Deps        []model.Dep
}

// ExpandSequence loads a named sequence and returns its steps in topological order as plain
// descriptors, without spawning anything. When workDirOverride is non-empty it replaces each
// step's original WorkDir; similarly for contextOverride.
func ExpandSequence(store db.JobStore, name, workDirOverride, contextOverride string) ([]SequenceStep, error) {
	seq, err := store.GetSequence(name)
	if err != nil {
		return nil, err
	}

	steps := make([]SequenceStep, len(seq.Steps))
	for i, key := range seq.Steps {
		j, err := store.Get(key)
		if err != nil {
			return nil, fmt.Errorf("sequence %s: loading step %d (%s): %w", name, i, key, err)
		}
		workDir := j.WorkDir
		if workDirOverride != "" {
			workDir = workDirOverride
		}
		context := j.Context
		if contextOverride != "" {
			context = contextOverride
		}
		steps[i] = SequenceStep{
			OriginalKey: key,
			Command:     j.Command,
			WorkDir:     workDir,
			Context:     context,
			Deps:        j.Deps,
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
