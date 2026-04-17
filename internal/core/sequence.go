package core

import (
	"fmt"
	"sort"
	"time"

	"job/internal/db"
	"job/internal/model"
)

// SaveSequence walks the transitive dependency graph of job j, topologically sorts the result (deps
// before dependents), and stores it as a named sequence.
func SaveSequence(store db.JobStore, name string, j *model.Job) error {
	visited := map[string]*model.Job{}
	queue := []*model.Job{j}

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

// RunSequence replays a named sequence by spawning each step as a new background job, remapping
// dependency keys from the originals to the newly created jobs. It returns the new job keys in step
// order. When workDirOverride is non-empty it is used as the working directory for every step
// instead of each step's original WorkDir. When contextOverride is non-empty it is used as the
// context for every step instead of each step's original Context.
func RunSequence(store db.JobStore, stateDir, name, workDirOverride, contextOverride string) ([]string, error) {
	seq, err := store.GetSequence(name)
	if err != nil {
		return nil, err
	}

	origJobs := make([]*model.Job, len(seq.Steps))
	for i, key := range seq.Steps {
		j, err := store.Get(key)
		if err != nil {
			return nil, fmt.Errorf("sequence %s: loading step %d (%s): %w", name, i, key, err)
		}
		origJobs[i] = j
	}

	// Map original job key → its position in the steps slice.
	keyToStep := make(map[string]int, len(seq.Steps))
	for i, key := range seq.Steps {
		keyToStep[key] = i
	}

	// Spawn each step in topological order, substituting old dep keys with the keys of the newly
	// spawned jobs.
	newKeys := make([]string, len(seq.Steps))
	for i, orig := range origJobs {
		newDeps := make([]model.Dep, len(orig.Deps))
		for di, dep := range orig.Deps {
			stepIdx, ok := keyToStep[dep.Key]
			if !ok {
				return nil, fmt.Errorf("sequence %s: step %d dep %s is outside the sequence", name, i, dep.Key)
			}
			newDeps[di] = model.Dep{Key: newKeys[stepIdx], Kind: dep.Kind}
		}

		workDir := orig.WorkDir
		if workDirOverride != "" {
			workDir = workDirOverride
		}
		context := orig.Context
		if contextOverride != "" {
			context = contextOverride
		}
		key, err := CreateAndSpawn(store, stateDir, orig.Command, RunOptions{
			WorkDir:   workDir,
			Deps:      newDeps,
			Context:   context,
			Automated: true,
		})
		if err != nil {
			return nil, fmt.Errorf("sequence %s: spawning step %d: %w", name, i, err)
		}
		newKeys[i] = key
	}
	return newKeys, nil
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
