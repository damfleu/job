package core

import (
	"fmt"

	"job/internal/db"
	"job/internal/model"
)

// ExpandDependentCascade returns rootKey and every job that transitively failed
// because of it (status completed, reason dep_failed), in topological order
// (rootKey first). Dependency keys are remapped when the steps are spawned.
func ExpandDependentCascade(store db.JobStore, rootKey string) ([]RunStep, error) {
	root, err := store.Get(rootKey)
	if err != nil {
		return nil, fmt.Errorf("cascade: fetching root %s: %w", rootKey, err)
	}

	depFailed, err := store.ListDepFailed()
	if err != nil {
		return nil, fmt.Errorf("cascade: listing dep-failed jobs: %w", err)
	}

	subtree := map[string]*model.Job{rootKey: root}
	for added := true; added; {
		added = false
		for _, j := range depFailed {
			if _, ok := subtree[j.Key]; ok {
				continue
			}
			for _, dep := range j.Deps {
				if dep.Kind != model.DepAfterSuccess {
					continue
				}
				if _, inSubtree := subtree[dep.Key]; inSubtree {
					subtree[j.Key] = j
					added = true
					break
				}
			}
		}
	}

	order, err := topoSort(subtree)
	if err != nil {
		return nil, err
	}

	steps := make([]RunStep, len(order))
	for i, key := range order {
		j := subtree[key]
		deps := j.Deps
		if key == rootKey {
			// The root is retried fresh, same as a plain `retry`: it doesn't carry over
			// its original (external) deps, only whatever the caller passes explicitly.
			deps = nil
		}
		steps[i] = RunStep{
			ID:      key,
			Command: j.Command,
			WorkDir: j.WorkDir,
			Deps:    deps,
		}
	}
	return steps, nil
}
