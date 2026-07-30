package cli

import (
	"errors"
	"fmt"
	"os"
	"strings"

	"charm.land/lipgloss/v2"
	"charm.land/lipgloss/v2/tree"
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
	Use:   "save <name> <key> [key...]",
	Short: "Save jobs and their deps as a named sequence",
	Args:  cobra.MinimumNArgs(2),
	RunE: func(cmd *cobra.Command, args []string) error {
		resolveCtx()
		name := args[0]
		keys := args[1:]
		existing, err := globalDB.GetSequence(name)
		if err != nil && !errors.Is(err, db.ErrSequenceNotFound) {
			return err
		}
		if existing != nil {
			fmt.Fprintf(cmd.ErrOrStderr(), "warning: replacing existing sequence %q\n", name)
		}
		jobs := make([]*model.Job, len(keys))
		for i, key := range keys {
			j, err := core.ResolveKey(globalDB, key, hereCtx)
			if err != nil {
				return err
			}
			jobs[i] = j
		}
		if err := core.SaveSequence(globalDB, name, jobs); err != nil {
			return err
		}
		seq, err := globalDB.GetSequence(name)
		if err != nil {
			return err
		}
		printSeqSteps(seq, seqSaveVerbose)
		return nil
	},
}

var seqSaveVerbose bool
var seqRunCwd string
var seqRunNotify bool
var seqRunDeps []model.Dep

var seqRunCmd = &cobra.Command{
	Use:   "run <name>",
	Short: "Replay a sequence",
	Args:  cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		var workDirOverride string
		if seqRunCwd != "" {
			var err error
			workDirOverride, err = resolveCwdFlag(seqRunCwd)
			if err != nil {
				return fmt.Errorf("resolving --cwd: %w", err)
			}
		}
		steps, err := core.ExpandSequence(globalDB, args[0], workDirOverride)
		if err != nil {
			return err
		}

		newKeys, err := runSteps(steps, seqRunDeps, seqRunNotify)
		if err != nil {
			return fmt.Errorf("sequence: %w", err)
		}

		printStepKeys(cmd.OutOrStdout(), newKeys)
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
		printSeqSteps(seq, seqShowVerbose)
		return nil
	},
}

var seqShowVerbose bool

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
	addAnyFlag(seqSaveCmd)
	seqSaveCmd.Flags().BoolVarP(&seqSaveVerbose, "verbose", "v", false, "show step working directories")
	addCwdFlag(seqRunCmd, &seqRunCwd, "run steps in a directory instead of their original directories")
	seqRunCmd.Flags().BoolVarP(&seqRunNotify, "notify", "n", false, "notify on completion of each step")
	seqRunCmd.Flags().VarP(depFlag{model.DepAfter, &seqRunDeps}, "after", "a", "run sequence after job completes (any exit code)")
	seqRunCmd.Flags().VarP(depFlag{model.DepAfterSuccess, &seqRunDeps}, "after-success", "A", "run sequence only if job succeeds (exit 0)")
	seqShowCmd.Flags().BoolVarP(&seqShowVerbose, "verbose", "v", false, "show step working directories")
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

func printSeqSteps(seq *model.Sequence, verbose bool) {
	fmt.Fprintln(os.Stdout, seq.Name)

	showWorkDir := false
	if verbose {
		workDir, commonWorkDir := commonStepValue(seq.Steps, func(step model.SequenceStep) string {
			return displayStepWorkDir(step.WorkDir)
		})

		if commonWorkDir {
			fmt.Fprintf(os.Stdout, "cwd: %s\n", workDir)
		} else {
			showWorkDir = true
		}
	}

	fmt.Fprintln(os.Stdout)
	fmt.Print(renderSeqTree(seq.Steps, showWorkDir))
}

func commonStepValue(
	steps []model.SequenceStep,
	value func(model.SequenceStep) string,
) (string, bool) {
	if len(steps) == 0 {
		return "", false
	}
	first := value(steps[0])
	for _, step := range steps[1:] {
		if value(step) != first {
			return "", false
		}
	}
	return first, true
}

// seqEdge pairs a child step with the dep kind linking it to its parent.
type seqEdge struct {
	step *model.SequenceStep
	kind model.DepKind
}

// renderSeqTree renders the sequence steps as a dependency tree, with ✓/~ prefixes
// indicating after-success / after dep kinds on each child node.
func renderSeqTree(steps []model.SequenceStep, showWorkDir bool) string {
	byID := make(map[int]*model.SequenceStep, len(steps))
	for i := range steps {
		byID[steps[i].ID] = &steps[i]
	}

	children := make(map[int][]seqEdge)
	isChild := make(map[int]bool)
	for i := range steps {
		step := &steps[i]
		for _, dep := range step.Deps {
			if _, inSeq := byID[dep.StepID]; inSeq {
				children[dep.StepID] = append(
					children[dep.StepID],
					seqEdge{step: step, kind: dep.Kind},
				)
				isChild[step.ID] = true
			}
		}
	}

	var roots []*model.SequenceStep
	for i := range steps {
		if !isChild[steps[i].ID] {
			roots = append(roots, &steps[i])
		}
	}

	var buildNode func(step *model.SequenceStep, kind model.DepKind, isRoot bool) *tree.Tree
	buildNode = func(step *model.SequenceStep, kind model.DepKind, isRoot bool) *tree.Tree {
		label := seqNodeLabel(step, kind, isRoot, showWorkDir)
		t := tree.Root(label)
		for _, edge := range children[step.ID] {
			t.Child(buildNode(edge.step, edge.kind, false))
		}
		return t
	}

	var parts []string
	for _, root := range roots {
		parts = append(parts, buildNode(root, "", true).String())
	}
	return strings.Join(parts, "\n") + "\n"
}

func seqNodeLabel(
	step *model.SequenceStep,
	kind model.DepKind,
	isRoot bool,
	showWorkDir bool,
) string {
	prefix := ""
	if !isRoot {
		switch kind {
		case model.DepAfterSuccess:
			prefix = "✓ "
		case model.DepAfter:
			prefix = "→ "
		}
	}
	command := lipgloss.NewStyle().Foreground(lipgloss.Color("244")).Render(displayCmd(step.Command))
	if !showWorkDir {
		return prefix + command
	}

	detailIndent := "  "
	if prefix != "" {
		detailIndent = strings.Repeat(" ", lipgloss.Width(prefix))
	}

	lines := []string{prefix + command}
	lines = append(lines, fmt.Sprintf("%scwd: %s", detailIndent, displayStepWorkDir(step.WorkDir)))
	return strings.Join(lines, "\n")
}

func displayStepWorkDir(workDir string) string {
	if workDir == "" {
		return "<default>"
	}
	return workDir
}
