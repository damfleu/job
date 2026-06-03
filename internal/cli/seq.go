package cli

import (
	"errors"
	"fmt"
	"os"
	"strings"

	"github.com/charmbracelet/x/term"
	"github.com/jedib0t/go-pretty/v6/table"
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
		resolveHereCtx()
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
		j, err := core.ResolveKey(globalDB, key, hereCtx)
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

var seqRunCwd string
var seqRunNotify bool

var seqRunCmd = &cobra.Command{
	Use:   "run <name>",
	Short: "Replay a sequence",
	Args:  cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		var workDirOverride, contextOverride string
		if seqRunCwd != "" {
			var err error
			workDirOverride, err = resolveCwdFlag(seqRunCwd)
			if err != nil {
				return fmt.Errorf("resolving --cwd: %w", err)
			}
			contextOverride = core.ResolveContext(workDirOverride, globalConfig.Context.Resolvers)
		}
		steps, err := core.ExpandSequence(globalDB, args[0], workDirOverride, contextOverride)
		if err != nil {
			return err
		}

		newKeys := make([]string, len(steps))
		origToNew := make(map[string]string, len(steps))

		for i, step := range steps {
			opts, err := buildRunOptions(step.Command, step.WorkDir, RunFlags{
				Notify:    seqRunNotify,
				Deps:      remapDeps(step.Deps, origToNew),
				Automated: true,
			})
			if err != nil {
				return err
			}
			key, err := core.CreateAndSpawn(globalDB, stateDir(), step.Command, opts)
			if err != nil {
				return fmt.Errorf("sequence: spawning step %d: %w", i, err)
			}
			origToNew[step.OriginalKey] = key
			newKeys[i] = key
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

// remapDeps returns a copy of deps with each key substituted using origToNew.
func remapDeps(deps []model.Dep, origToNew map[string]string) []model.Dep {
	if len(deps) == 0 {
		return deps
	}
	out := make([]model.Dep, len(deps))
	for i, dep := range deps {
		out[i] = model.Dep{Key: origToNew[dep.Key], Kind: dep.Kind}
	}
	return out
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
	addHereFlag(seqSaveCmd)
	addCwdFlag(seqRunCmd, &seqRunCwd, "run steps in a directory instead of their original directories")
	seqRunCmd.Flags().BoolVarP(&seqRunNotify, "notify", "n", false, "notify on completion of each step")
	seqCmd.AddCommand(seqSaveCmd, seqRunCmd, seqListCmd, seqShowCmd, seqRmCmd)
	rootCmd.AddCommand(seqCmd)
}

func printSeqTable(seqs []*model.Sequence) {
	t := table.NewWriter()
	t.SetOutputMirror(os.Stdout)
	t.AppendHeader(table.Row{"NAME", "STEPS", "CREATED"})
	for _, seq := range seqs {
		t.AppendRow(table.Row{seq.Name, fmt.Sprintf("%d", len(seq.Steps)), formatTime(seq.CreatedAt)})
	}
	if !term.IsTerminal(os.Stdout.Fd()) {
		t.RenderTSV()
		return
	}
	t.SetStyle(jobTableStyle())
	t.Render()
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

	t := table.NewWriter()
	t.SetOutputMirror(os.Stdout)
	t.AppendHeader(table.Row{"#", "KEY", "COMMAND", "DEPS"})
	for i, j := range jobs {
		t.AppendRow(table.Row{
			fmt.Sprintf("%d", i),
			j.Key,
			strings.Join(j.Command, " "),
			formatStepDeps(j.Deps, keyToStep),
		})
	}
	fmt.Fprintf(os.Stdout, "%s\n\n", seq.Name)
	if !term.IsTerminal(os.Stdout.Fd()) {
		t.RenderTSV()
		return
	}
	t.SetStyle(jobTableStyle())
	t.Render()
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
