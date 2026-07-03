package core

import (
	"errors"
	"fmt"

	"job/internal/db"
	"job/internal/model"
)

// ResolveKey maps a user-supplied input to a job, using these strategies in order:
//  1. "." means the most recently started job (scoped to ctx when non-empty)
//  2. Exact match on key or alias
//  3. Substring match on command, scoped to ctx when non-empty (active jobs preferred over completed)
//  4. Prefix match on key
func ResolveKey(store db.JobStore, input, ctx string) (*model.Job, error) {
	if input == "." {
		if ctx != "" {
			return resolveDotInContext(store, ctx)
		}
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
	matches, err := store.Search(input, ctx)
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

func resolveDotInContext(store db.JobStore, ctx string) (*model.Job, error) {
	key, err := store.GetLastKeyForContext(ctx)
	if err != nil {
		return nil, err
	}
	if key == "" {
		return nil, fmt.Errorf("no jobs in current context %q", ctx)
	}
	return store.Get(key)
}
