package core

import (
	"errors"
	"fmt"

	"job/internal/db"
	"job/internal/model"
)

// ResolveKey maps a user-supplied input to a job, using these strategies in order:
//  1. "." means the most recently started job
//  2. Exact match on key or alias
//  3. Substring match on command (active jobs preferred over completed)
//  4. Prefix match on key
func ResolveKey(store db.JobStore, input string) (*model.Job, error) {
	if input == "." {
		return resolveDot(store)
	}

	// exact key
	job, err := store.Get(input)
	if err == nil {
		return job, nil
	}
	if !errors.Is(err, db.ErrNotFound) {
		return nil, err
	}

	// exact alias
	job, err = store.FindByAlias(input)
	if err == nil {
		return job, nil
	}
	if !errors.Is(err, db.ErrNotFound) {
		return nil, err
	}

	// substring on command and prefer active jobs
	matches, err := store.Search(input)
	if err != nil {
		return nil, err
	}
	if len(matches) > 0 {
		for _, j := range matches {
			if j.Status != model.StatusCompleted {
				return j, nil
			}
		}
		return matches[0], nil
	}

	// prefix on key
	prefixed, err := store.FindByKeyPrefix(input)
	if err != nil {
		return nil, err
	}
	if len(prefixed) > 0 {
		return prefixed[0], nil
	}

	return nil, fmt.Errorf("no job matching %q", input)
}

func resolveDot(store db.JobStore) (*model.Job, error) {
	key, err := store.GetLastKey()
	if err != nil {
		return nil, err
	}
	if key == "" {
		return nil, errors.New("no jobs have been run yet")
	}
	return store.Get(key)
}
