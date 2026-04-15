package cli

import (
	"errors"
	"fmt"
	"os"
	"strings"

	"github.com/charmbracelet/lipgloss"
	"github.com/charmbracelet/lipgloss/table"
	"github.com/charmbracelet/x/term"
	"github.com/spf13/cobra"

	"job/internal/core"
	"job/internal/db"
	"job/internal/model"
)

var seqCmd = &cobra.Command{
	Use:     "sequence",
	Aliases: []string{"seq"},
	Short:   "Manage sequences",
	RunE: func(cmd *cobra.Command, args []string) error {
		return cmd.Help()
	},
}

var seqSaveCmd = &cobra.Command{
	Use:   "save <name> [key]",
	Short: "Save a job and its deps as a named sequence",
	Args:  cobra.RangeArgs(1, 2),
	RunE: func(cmd *cobra.Command, args []string) error {
		name := args[0]
		key := "."
		if len(args) > 1 {
			key = args[1]
		}
		existing, err := globalDB.GetSequence(name)
		if err != nil && !errors.Is(err, db.ErrSequenceNotFound) {
			return err
		}
		if existing != nil {
			last := existing.Steps[len(existing.Steps)-1]
			fmt.Fprintf(cmd.ErrOrStderr(), "warning: replacing existing sequence %q (was: %s)\n", name, last)
		}
		j, err := core.ResolveKey(globalDB, key)
		if err != nil {
			return err
		}
		if err := core.SaveSequence(globalDB, name, j); err != nil {
			return err
		}
		seq, err := globalDB.GetSequence(name)
		if err != nil {
			return err
		}
		printSeqSteps(seq)
		return nil
	},
}

var seqRunCmd = &cobra.Command{
	Use:   "run <name>",
	Short: "Replay a sequence",
	Args:  cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		newKeys, err := core.RunSequence(globalDB, stateDir(), args[0])
		if err != nil {
			return err
		}
		// Load each new job to display its command alongside the key.
		for _, key := range newKeys {
			j, err := globalDB.Get(key)
			if err != nil {
				fmt.Fprintln(cmd.OutOrStdout(), key)
				continue
			}
			fmt.Fprintf(cmd.OutOrStdout(), "%-36s  %s\n", key, displayCmd(j.Command))
		}
		return nil
	},
}

var seqListCmd = &cobra.Command{
	Use:     "list",
	Aliases: []string{"ls"},
	Short:   "List all sequences",
	RunE: func(cmd *cobra.Command, args []string) error {
		seqs, err := globalDB.ListSequences()
		if err != nil {
			return err
		}
		if len(seqs) == 0 {
			fmt.Fprintln(os.Stderr, "no sequences")
			return nil
		}
		printSeqTable(seqs)
		return nil
	},
}

var seqShowCmd = &cobra.Command{
	Use:   "show <name>",
	Short: "Show the steps of a sequence",
	Args:  cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		seq, err := globalDB.GetSequence(args[0])
		if err != nil {
			return err
		}
		printSeqSteps(seq)
		return nil
	},
}

var seqRmCmd = &cobra.Command{
	Use:     "remove <name>",
	Aliases: []string{"rm"},
	Short:   "Delete a sequence",
	Args:    cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		if err := globalDB.DeleteSequence(args[0]); err != nil {
			return err
		}
		fmt.Fprintf(cmd.OutOrStdout(), "removed sequence %q\n", args[0])
		return nil
	},
}

func init() {
	seqCmd.AddCommand(seqSaveCmd, seqRunCmd, seqListCmd, seqShowCmd, seqRmCmd)
	rootCmd.AddCommand(seqCmd)
}

func printSeqTable(seqs []*model.Sequence) {
	termWidth, _, _ := term.GetSize(os.Stdout.Fd())
	headerStyle := lipgloss.NewStyle().Bold(true)

	t := table.New().
		Border(lipgloss.NormalBorder()).
		BorderStyle(lipgloss.NewStyle().Foreground(lipgloss.Color("240"))).
		StyleFunc(func(row, col int) lipgloss.Style {
			if row == table.HeaderRow {
				return headerStyle
			}
			return lipgloss.NewStyle()
		}).
		Headers("NAME", "STEPS", "CREATED")

	for _, seq := range seqs {
		t.Row(seq.Name, fmt.Sprintf("%d", len(seq.Steps)), formatTime(seq.CreatedAt))
	}

	if termWidth > 0 {
		t.Width(termWidth)
	}
	fmt.Fprintln(os.Stdout, t)
}

func printSeqSteps(seq *model.Sequence) {
	// Load each job to display its command and remap dep keys to step indices.
	jobs := make([]*model.Job, len(seq.Steps))
	keyToStep := make(map[string]int, len(seq.Steps))
	for i, key := range seq.Steps {
		keyToStep[key] = i
		j, err := globalDB.Get(key)
		if err != nil {
			// Job may have been deleted; show the key with a placeholder.
			jobs[i] = &model.Job{Key: key, Command: []string{"<unavailable>"}}
		} else {
			jobs[i] = j
		}
	}

	termWidth, _, _ := term.GetSize(os.Stdout.Fd())
	headerStyle := lipgloss.NewStyle().Bold(true)

	t := table.New().
		Border(lipgloss.NormalBorder()).
		BorderStyle(lipgloss.NewStyle().Foreground(lipgloss.Color("240"))).
		StyleFunc(func(row, col int) lipgloss.Style {
			if row == table.HeaderRow {
				return headerStyle
			}
			return lipgloss.NewStyle()
		}).
		Headers("#", "KEY", "COMMAND", "DEPS")

	for i, j := range jobs {
		t.Row(
			fmt.Sprintf("%d", i),
			j.Key,
			displayCmd(j.Command),
			formatStepDeps(j.Deps, keyToStep),
		)
	}

	if termWidth > 0 {
		t.Width(termWidth)
	}
	fmt.Fprintf(os.Stdout, "%s\n\n", seq.Name)
	fmt.Fprintln(os.Stdout, t)
}

// formatStepDeps renders a job's deps as "after-success 1, after 0" using step
// indices instead of job keys.
func formatStepDeps(deps []model.Dep, keyToStep map[string]int) string {
	if len(deps) == 0 {
		return ""
	}
	parts := make([]string, len(deps))
	for i, dep := range deps {
		kind := strings.ReplaceAll(string(dep.Kind), "_", "-")
		if step, ok := keyToStep[dep.Key]; ok {
			parts[i] = fmt.Sprintf("%s %d", kind, step)
		} else {
			parts[i] = fmt.Sprintf("%s %s", kind, dep.Key)
		}
	}
	return strings.Join(parts, ", ")
}
