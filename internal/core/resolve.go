package core

import (
	"errors"
	"fmt"

	"job/internal/db"
	"job/internal/model"
)

// statusSymbols maps the shell-safe status shorthands to the status they resolve to.
var statusSymbols = map[string]model.Status{
	"+": model.StatusRunning,
	"_": model.StatusBlocked,
	"=": model.StatusCompleted,
}

// statusNouns gives the human-readable noun for each status, used in error messages.
var statusNouns = map[model.Status]string{
	model.StatusRunning:   "running",
	model.StatusBlocked:   "blocked",
	model.StatusCompleted: "completed",
}

// ResolveKey maps a user-supplied input to a job, using these strategies in order:
//  1. "." means the most recently started job (scoped to ctx when non-empty)
//  2. "+" / "_" / "=" mean the most recent running / blocked / completed job
//     (scoped to ctx when non-empty); hard error if no job matches
//  3. Exact match on key or alias
//  4. Substring match on command, scoped to ctx when non-empty (active jobs preferred over completed)
//  5. Prefix match on key
func ResolveKey(store db.JobStore, input, ctx string) (*model.Job, error) {
	if input == "." {
		return resolveDotInContext(store, ctx)
	}

	if status, ok := statusSymbols[input]; ok {
		return resolveByStatus(store, status, ctx)
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
	job, err = store.FindByAlias(input, ctx)
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

func resolveDotInContext(store db.JobStore, ctx string) (*model.Job, error) {
	key, err := store.GetLastKeyForContext(ctx)
	if err != nil {
		return nil, err
	}
	if key == "" {
		if ctx != "" {
			return nil, fmt.Errorf("no jobs in current context %q", ctx)
		}
		return nil, errors.New("no jobs have been run yet")
	}
	return store.Get(key)
}

func resolveByStatus(store db.JobStore, status model.Status, ctx string) (*model.Job, error) {
	key, err := store.GetLastKeyByStatus(status, ctx)
	if err != nil {
		return nil, err
	}
	if key == "" {
		noun := statusNouns[status]
		if ctx != "" {
			return nil, fmt.Errorf("no %s jobs in current context %q", noun, ctx)
		}
		return nil, fmt.Errorf("no %s jobs", noun)
	}
	return store.Get(key)
}

// ResolveDefault resolves the job to act on when no argument was given at all:
// it prefers the last job with the given status, falling back to the last
// created job (the same target as ".") if none matches. Unlike the explicit
// "+"/"_"/"=" symbols, this never hard-errors on a miss — bare invocation is
// a convenience guess, not an explicit request.
func ResolveDefault(store db.JobStore, ctx string, status model.Status) (*model.Job, error) {
	key, err := store.GetLastKeyByStatus(status, ctx)
	if err != nil {
		return nil, err
	}
	if key != "" {
		return store.Get(key)
	}
	return resolveDotInContext(store, ctx)
}
