package cli

import (
	"errors"
	"fmt"
	"os"
	"sort"
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
	Use:   "save <name> [key...]",
	Short: "Save jobs and their deps as a named sequence",
	Args:  cobra.MinimumNArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		resolveHereCtx()
		name := args[0]
		keys := args[1:]
		if len(keys) == 0 {
			keys = []string{"."}
		}
		existing, err := globalDB.GetSequence(name)
		if err != nil && !errors.Is(err, db.ErrSequenceNotFound) {
			return err
		}
		if existing != nil {
			last := existing.Steps[len(existing.Steps)-1]
			fmt.Fprintf(cmd.ErrOrStderr(), "warning: replacing existing sequence %q (was: %s)\n", name, last)
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
		printSeqSteps(seq)
		return nil
	},
}

var seqRunCwd string
var seqRunNotify bool
var seqRunDeps []model.Dep

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
	seqRunCmd.Flags().VarP(depFlag{model.DepAfter, &seqRunDeps}, "after", "a", "run sequence after job completes (any exit code)")
	seqRunCmd.Flags().VarP(depFlag{model.DepAfterSuccess, &seqRunDeps}, "after-success", "A", "run sequence only if job succeeds (exit 0)")
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
	jobs := make([]*model.Job, len(seq.Steps))
	for i, key := range seq.Steps {
		j, err := globalDB.Get(key)
		if err != nil {
			jobs[i] = &model.Job{Key: key, Command: []string{"<unavailable>"}}
		} else {
			jobs[i] = j
		}
	}
	fmt.Fprintf(os.Stdout, "%s\n\n", seq.Name)
	fmt.Print(renderSeqTree(jobs))
}

// seqEdge pairs a child job with the dep kind linking it to its parent.
type seqEdge struct {
	job  *model.Job
	kind model.DepKind
}

// renderSeqTree renders the sequence steps as a dependency tree, with ✓/~ prefixes
// indicating after-success / after dep kinds on each child node.
func renderSeqTree(jobs []*model.Job) string {
	byKey := make(map[string]*model.Job, len(jobs))
	for _, j := range jobs {
		byKey[j.Key] = j
	}

	children := make(map[string][]seqEdge)
	isChild := make(map[string]bool)
	for _, j := range jobs {
		for _, dep := range j.Deps {
			if _, inSeq := byKey[dep.Key]; inSeq {
				children[dep.Key] = append(children[dep.Key], seqEdge{job: j, kind: dep.Kind})
				isChild[j.Key] = true
			}
		}
	}
	for key := range children {
		sort.Slice(children[key], func(i, j int) bool {
			return children[key][i].job.Key < children[key][j].job.Key
		})
	}

	var roots []*model.Job
	for _, j := range jobs {
		if !isChild[j.Key] {
			roots = append(roots, j)
		}
	}

	var buildNode func(j *model.Job, kind model.DepKind, isRoot bool) *tree.Tree
	buildNode = func(j *model.Job, kind model.DepKind, isRoot bool) *tree.Tree {
		label := seqNodeLabel(j, kind, isRoot)
		t := tree.Root(label)
		for _, edge := range children[j.Key] {
			t.Child(buildNode(edge.job, edge.kind, false))
		}
		return t
	}

	var parts []string
	for _, root := range roots {
		parts = append(parts, buildNode(root, "", true).String())
	}
	return strings.Join(parts, "\n") + "\n"
}

func seqNodeLabel(j *model.Job, kind model.DepKind, isRoot bool) string {
	prefix := ""
	if !isRoot {
		switch kind {
		case model.DepAfterSuccess:
			prefix = "✓ "
		case model.DepAfter:
			prefix = "→ "
		}
	}
	key := lipgloss.NewStyle().Foreground(lipgloss.Color("244")).Render(j.Key)
	return fmt.Sprintf("%s%s  %s", prefix, key, displayCmd(j.Command))
}
