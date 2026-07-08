package cli

import (
	"errors"
	"fmt"
	"os"
	"strings"

	huh "charm.land/huh/v2"
	"github.com/charmbracelet/x/term"

	"job/internal/core"
	"job/internal/db"
	"job/internal/model"
)

// resolveJobArgInteractive mirrors ResolveKey's exact-match steps so an
// unambiguous alias/key never triggers the picker even if it also happens
// to substring-match unrelated jobs' commands (those would otherwise crowd
// out the exact match, which Search() can't surface by alias/key at all).
func resolveJobArgInteractive(input, ctx string) (*model.Job, error) {
	if j, err := globalDB.Get(input); err == nil {
		return j, nil
	} else if !errors.Is(err, db.ErrNotFound) {
		return nil, err
	}
	if j, err := globalDB.FindByAlias(input); err == nil {
		return j, nil
	} else if !errors.Is(err, db.ErrNotFound) {
		return nil, err
	}
	candidates, err := candidatesFor(globalDB, input, ctx)
	if err != nil {
		return nil, err
	}
	if len(candidates) <= 1 {
		return core.ResolveKey(globalDB, input, ctx) // unambiguous: identical to non-interactive path
	}
	return pickJob(candidates)
}

// candidatesFor returns the picker's candidate list for input. A bare "."
// means "no filter, just browse recent jobs" — Search's empty-query LIKE
// pattern already matches everything, so no separate query is needed.
func candidatesFor(store db.JobStore, input, ctx string) ([]*model.Job, error) {
	if input == "." {
		return store.Search("", ctx)
	}
	return store.Search(input, ctx)
}

func pickJob(candidates []*model.Job) (*model.Job, error) {
	if !term.IsTerminal(os.Stdin.Fd()) {
		return nil, errors.New("interactive selection requires a terminal")
	}
	options := make([]huh.Option[*model.Job], len(candidates))
	for i, j := range candidates {
		options[i] = huh.NewOption(candidateLabel(j), j)
	}
	var chosen *model.Job
	field := huh.NewSelect[*model.Job]().
		Title(fmt.Sprintf("%d jobs match — pick one", len(candidates))).
		Description(candidateHeader()).
		Options(options...).
		Height(12).
		Value(&chosen)
	// Render the picker to stderr so stdout stays free for the resolved
	// job's output (e.g. `tail -f "$(job log -p -i)"`), which would
	// otherwise break because stdout isn't a terminal in that context.
	err := huh.NewForm(huh.NewGroup(field)).
		WithShowHelp(false).
		WithOutput(os.Stderr).
		Run()
	if err != nil {
		return nil, fmt.Errorf("selecting job: %w", err)
	}
	return chosen, nil
}

const (
	keyColWidth    = 24
	statusColWidth = 10
	timeColWidth   = 19 // len("2006-01-02 15:04:05")
)

func candidateHeader() string {
	return fmt.Sprintf("%-*s  %-*s  %-*s  %s",
		keyColWidth, "KEY", statusColWidth, "STATUS", timeColWidth, "TIME", "COMMAND")
}

// candidateLabel leaves COMMAND untruncated — huh sizes the select field to
// the real terminal width and wraps long option text itself, so pre-cropping
// it here would just discard width huh could otherwise use.
func candidateLabel(j *model.Job) string {
	key := ellipsisTrunc(displayKeyAlias(j), keyColWidth)
	status := jobStatusStyle(j).Render(fmt.Sprintf("%-*s", statusColWidth, jobStatusText(j)))
	ts := fmt.Sprintf("%-*s", timeColWidth, displayTimestamp(j))
	cmd := strings.Join(j.Command, " ")
	return fmt.Sprintf("%-*s  %s  %s  %s", keyColWidth, key, status, ts, cmd)
}
