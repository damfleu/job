package core

import (
	"fmt"
	"sort"

	"job/internal/model"
)

// RunStep describes one step that is ready to be spawned. Dependency keys
// refer to other RunStep IDs and are remapped to new job keys by the caller.
// Retry cascades may additionally retain dependencies on jobs outside the set.
type RunStep struct {
	ID        string
	Command   []string
	WorkDir   string
	Deps      []model.Dep
	Automated bool
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
