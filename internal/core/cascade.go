package core

import (
	"fmt"

	"job/internal/db"
	"job/internal/model"
)

// ExpandDependentCascade returns rootKey and every job that transitively failed because of it
// (status completed, reason dep_failed), in topological order (rootKey first). As with
// ExpandSequence, each step's Deps still reference the original job keys; the caller is
// responsible for remapping them to the keys of the newly spawned jobs.
func ExpandDependentCascade(store db.JobStore, rootKey string) ([]SequenceStep, error) {
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

	steps := make([]SequenceStep, len(order))
	for i, key := range order {
		j := subtree[key]
		deps := j.Deps
		if key == rootKey {
			// The root is retried fresh, same as a plain `retry`: it doesn't carry over
			// its original (external) deps, only whatever the caller passes explicitly.
			deps = nil
		}
		steps[i] = SequenceStep{
			OriginalKey: key,
			Command:     j.Command,
			WorkDir:     j.WorkDir,
			Context:     j.Context,
			Deps:        deps,
		}
	}
	return steps, nil
}
