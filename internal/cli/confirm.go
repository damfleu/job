package cli

import (
	"fmt"
	"os"

	huh "charm.land/huh/v2"
	"github.com/charmbracelet/x/term"
	"github.com/spf13/cobra"
)

// confirmDestructive asks for approval before deleting jobs. Commands invoked
// without a terminal must opt in explicitly with --yes so they cannot hang.
func confirmDestructive(cmd *cobra.Command, force bool) (bool, error) {
	if force {
		return true, nil
	}
	if !term.IsTerminal(os.Stdin.Fd()) {
		return false, fmt.Errorf("confirmation requires a terminal; rerun with --yes to proceed")
	}

	approved := false
	field := huh.NewConfirm().
		Title("Proceed?").
		Affirmative("Yes").
		Negative("No").
		Value(&approved)
	err := huh.NewForm(huh.NewGroup(field)).
		WithShowHelp(false).
		WithOutput(cmd.ErrOrStderr()).
		Run()
	if err != nil {
		return false, fmt.Errorf("confirming deletion: %w", err)
	}
	return approved, nil
}
