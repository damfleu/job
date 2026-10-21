package sequence

import (
	"errors"
	"fmt"
	"path/filepath"
	"strings"
)

// Validate checks a definition without accessing working directories or
// executing commands. Forward references are allowed.
func (d *Definition) Validate() error {
	var errs []error
	if d.Version != FormatVersion {
		errs = append(errs, fmt.Errorf("unsupported sequence version %d (expected %d)", d.Version, FormatVersion))
	}
	if len(d.Steps) == 0 {
		errs = append(errs, errors.New("sequence has no steps"))
	}
	if err := validateCWD(d.CWD); err != nil {
		errs = append(errs, err)
	}

	ids := make(map[string]int, len(d.Steps))
	for i, step := range d.Steps {
		if strings.TrimSpace(step.ID) == "" {
			errs = append(errs, fmt.Errorf("step %d: id must not be blank", i+1))
		} else if _, exists := ids[step.ID]; exists {
			errs = append(errs, fmt.Errorf("duplicate step id %q", step.ID))
		} else {
			ids[step.ID] = i
		}
	}
	for _, step := range d.Steps {
		if (step.Run != nil) == (step.Command != nil) {
			errs = append(errs, fmt.Errorf("step %q: exactly one of run and command is required", step.ID))
		}
		if step.Run != nil {
			if strings.TrimSpace(*step.Run) == "" || strings.ContainsRune(*step.Run, 0) {
				errs = append(errs, fmt.Errorf("step %q: run must not be blank or contain NUL", step.ID))
			}
		}
		if step.Command != nil {
			if len(step.Command) == 0 || step.Command[0] == "" {
				errs = append(errs, fmt.Errorf("step %q: command must contain a non-empty program", step.ID))
			}
			for _, arg := range step.Command {
				if strings.ContainsRune(arg, 0) {
					errs = append(errs, fmt.Errorf("step %q: command arguments must not contain NUL", step.ID))
					break
				}
			}
		}
		if err := validateCWD(step.CWD); err != nil {
			errs = append(errs, fmt.Errorf("step %q: %w", step.ID, err))
		}
		seen := make(map[string]bool)
		for _, deps := range [][]string{step.After, step.AfterSuccess} {
			for _, id := range deps {
				if seen[id] {
					errs = append(errs, fmt.Errorf("step %q: dependency %q appears more than once", step.ID, id))
				}
				seen[id] = true
				if id == step.ID {
					errs = append(errs, fmt.Errorf("step %q: cannot depend on itself", step.ID))
				} else if _, exists := ids[id]; !exists {
					errs = append(errs, fmt.Errorf("step %q: unknown dependency %q", step.ID, id))
				}
			}
		}
	}
	if err := errors.Join(errs...); err != nil {
		return err
	}
	return d.validateAcyclic(ids)
}

func validateCWD(cwd *string) error {
	if cwd != nil && (!filepath.IsAbs(*cwd) || strings.ContainsRune(*cwd, 0)) {
		return fmt.Errorf("cwd must be an absolute path without NUL, got %q", *cwd)
	}
	return nil
}

func (d *Definition) validateAcyclic(ids map[string]int) error {
	// Track the active dependency path to report the cycle itself, rather than
	// also reporting downstream steps that cannot be scheduled because of it.
	state := make([]uint8, len(d.Steps))
	var path []string
	var visit func(int) error
	visit = func(i int) error {
		switch state[i] {
		case 2:
			return nil
		case 1:
			id := d.Steps[i].ID
			for j, ancestor := range path {
				if ancestor == id {
					return fmt.Errorf("dependency cycle: %s -> %s", strings.Join(path[j:], " -> "), id)
				}
			}
		}
		state[i] = 1
		step := d.Steps[i]
		path = append(path, step.ID)
		for _, deps := range [][]string{step.After, step.AfterSuccess} {
			for _, id := range deps {
				if err := visit(ids[id]); err != nil {
					return err
				}
			}
		}
		path = path[:len(path)-1]
		state[i] = 2
		return nil
	}
	for i := range d.Steps {
		if err := visit(i); err != nil {
			return err
		}
	}
	return nil
}
