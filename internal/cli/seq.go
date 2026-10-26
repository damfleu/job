package cli

import (
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	"charm.land/lipgloss/v2"
	"charm.land/lipgloss/v2/tree"
	"github.com/charmbracelet/x/term"
	"github.com/jedib0t/go-pretty/v6/table"
	"github.com/spf13/cobra"

	"job/internal/core"
	"job/internal/model"
	"job/internal/sequence"
)

var seqCmd = &cobra.Command{
	Use:     "sequence",
	Aliases: []string{"seq"},
	Short:   "Manage sequences",
	Args:    cobra.NoArgs,
	RunE:    func(cmd *cobra.Command, args []string) error { return cmd.Help() },
}

var seqSaveOutput string
var seqSaveForce bool
var seqSaveCmd = &cobra.Command{
	Use:   "save <key> [key...]",
	Short: "Export jobs and their dependencies as a TOML sequence",
	Args:  cobra.MinimumNArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		if cmd.Flags().Changed("output") && seqSaveOutput == "" {
			return fmt.Errorf("--output requires a non-empty file path")
		}
		if seqSaveForce && seqSaveOutput == "" {
			return fmt.Errorf("--force requires --output")
		}
		resolveCtx()
		jobs := make([]*model.Job, len(args))
		for i, key := range args {
			job, err := core.ResolveKey(globalDB, key, hereCtx)
			if err != nil {
				return err
			}
			jobs[i] = job
		}
		d, err := core.CaptureSequence(globalDB, jobs)
		if err != nil {
			return err
		}
		if seqSaveOutput == "" {
			return sequence.Encode(cmd.OutOrStdout(), d)
		}
		if err := sequence.WriteFile(seqSaveOutput, d, seqSaveForce); err != nil {
			return err
		}
		fmt.Fprintf(cmd.ErrOrStderr(), "saved %s\n", seqSaveOutput)
		return nil
	},
}

var seqRunCwd string
var seqRunNotify bool
var seqRunDeps []model.Dep
var seqRunCmd = &cobra.Command{
	Use:   "run <name>",
	Short: "Run an installed sequence",
	Args:  cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		entry, err := sequence.LoadNamed(sequenceDir(), args[0])
		if err != nil {
			return err
		}
		override, err := resolveCwdFlag(seqRunCwd)
		if err != nil {
			return fmt.Errorf("resolving --cwd: %w", err)
		}
		cwd, err := os.Getwd()
		if err != nil {
			return err
		}
		steps, err := core.PlanSequence(entry.Definition, cwd, override)
		if err != nil {
			return err
		}
		// Check all effective directories after overrides and before launching
		// any step. Schema/file validation intentionally does not access them.
		for _, step := range steps {
			info, err := os.Stat(step.WorkDir)
			if err != nil {
				return fmt.Errorf("step %q: cwd: %w", step.ID, err)
			}
			if !info.IsDir() {
				return fmt.Errorf("step %q: cwd %q is not a directory", step.ID, step.WorkDir)
			}
		}
		keys, err := runSteps(steps, seqRunDeps, seqRunNotify)
		if err != nil {
			return fmt.Errorf("sequence: %w", err)
		}
		printStepKeys(cmd.OutOrStdout(), keys)
		return nil
	},
}

var seqListCmd = &cobra.Command{
	Use:               "list",
	Aliases:           []string{"ls"},
	Short:             "List installed sequences",
	Args:              cobra.NoArgs,
	PersistentPreRunE: loadConfig,
	RunE: func(cmd *cobra.Command, args []string) error {
		entries, err := sequence.List(sequenceDir())
		if len(entries) > 0 {
			printSeqTable(cmd.OutOrStdout(), entries)
		} else if err == nil {
			fmt.Fprintln(cmd.ErrOrStderr(), "no sequences")
		}
		return err
	},
}

var seqShowVerbose bool
var seqShowCmd = &cobra.Command{
	Use:               "show <name>",
	Short:             "Show the steps of an installed sequence",
	Args:              cobra.ExactArgs(1),
	PersistentPreRunE: loadConfig,
	RunE: func(cmd *cobra.Command, args []string) error {
		entry, err := sequence.LoadNamed(sequenceDir(), args[0])
		if err != nil {
			return err
		}
		printSeqSteps(cmd.OutOrStdout(), entry, seqShowVerbose)
		return nil
	},
}

func sequenceDir() string { return filepath.Join(configDir(), "sequences") }

func init() {
	addAnyFlag(seqSaveCmd)
	seqSaveCmd.Flags().StringVarP(&seqSaveOutput, "output", "o", "", "write TOML to a file instead of stdout")
	seqSaveCmd.Flags().BoolVar(&seqSaveForce, "force", false, "replace an existing output file")
	addCwdFlag(seqRunCmd, &seqRunCwd, "override every step's working directory")
	seqRunCmd.Flags().BoolVarP(&seqRunNotify, "notify", "n", false, "notify on completion of each step")
	seqRunCmd.Flags().VarP(depFlag{model.DepAfter, &seqRunDeps}, "after", "a", "run sequence after job completes (any exit code)")
	seqRunCmd.Flags().VarP(depFlag{model.DepAfterSuccess, &seqRunDeps}, "after-success", "A", "run sequence only if job succeeds (exit 0)")
	seqShowCmd.Flags().BoolVarP(&seqShowVerbose, "verbose", "v", false, "show working directories and automation settings")
	seqCmd.AddCommand(seqSaveCmd, seqRunCmd, seqListCmd, seqShowCmd)
	rootCmd.AddCommand(seqCmd)
}

func printSeqTable(w io.Writer, entries []sequence.Entry) {
	t := table.NewWriter()
	t.SetOutputMirror(w)
	t.AppendHeader(table.Row{"NAME", "STEPS", "PATH"})
	for _, entry := range entries {
		t.AppendRow(table.Row{entry.Name, fmt.Sprintf("%d", len(entry.Definition.Steps)), entry.Path})
	}
	if !term.IsTerminal(os.Stdout.Fd()) {
		t.RenderTSV()
		return
	}
	t.SetStyle(jobTableStyle())
	t.Render()
}

func printSeqSteps(w io.Writer, entry *sequence.Entry, verbose bool) {
	d := entry.Definition
	titleStyle := lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("6"))
	labelStyle := lipgloss.NewStyle().Foreground(lipgloss.Color("244"))
	printAttribute := func(label, value string) {
		fmt.Fprintf(w, "%s  %s\n", labelStyle.Render(fmt.Sprintf("%-9s", label)), value)
	}
	fmt.Fprintln(w, titleStyle.Render(entry.Name))
	if d.Description != "" {
		fmt.Fprintln(w)
		fmt.Fprintln(w, d.Description)
	}
	showWorkDir, showAutomated := false, false
	if verbose {
		fmt.Fprintln(w)
		printAttribute("File", entry.Path)
		cwd := stepWorkDir(d, &d.Steps[0])
		for i := range d.Steps[1:] {
			if stepWorkDir(d, &d.Steps[i+1]) != cwd {
				showWorkDir = true
				break
			}
		}
		if !showWorkDir {
			printAttribute("Directory", displayStepWorkDir(cwd))
		}
		automated := stepAutomated(d, &d.Steps[0])
		for i := range d.Steps[1:] {
			if stepAutomated(d, &d.Steps[i+1]) != automated {
				showAutomated = true
				break
			}
		}
		if !showAutomated {
			printAttribute("Automated", fmt.Sprintf("%t", automated))
		}
	}
	fmt.Fprintln(w)
	fmt.Fprint(w, renderSeqTree(d, showWorkDir, showAutomated))
}

func stepWorkDir(d *sequence.Definition, step *sequence.Step) string {
	if step.CWD != nil {
		return *step.CWD
	}
	if d.CWD != nil {
		return *d.CWD
	}
	return ""
}

func stepAutomated(d *sequence.Definition, step *sequence.Step) bool {
	if step.Automated != nil {
		return *step.Automated
	}
	if d.Automated != nil {
		return *d.Automated
	}
	return true
}

type seqEdge struct {
	step *sequence.Step
	kind model.DepKind
}

func renderSeqTree(d *sequence.Definition, showWorkDir, showAutomated bool) string {
	children := make(map[string][]seqEdge)
	isChild := make(map[string]bool)
	for i := range d.Steps {
		step := &d.Steps[i]
		for _, id := range step.After {
			children[id] = append(children[id], seqEdge{step: step, kind: model.DepAfter})
			isChild[step.ID] = true
		}
		for _, id := range step.AfterSuccess {
			children[id] = append(children[id], seqEdge{step: step, kind: model.DepAfterSuccess})
			isChild[step.ID] = true
		}
	}
	var buildNode func(*sequence.Step, model.DepKind) *tree.Tree
	buildNode = func(step *sequence.Step, kind model.DepKind) *tree.Tree {
		t := tree.Root(seqNodeLabel(d, step, kind, showWorkDir, showAutomated))
		for _, edge := range children[step.ID] {
			t.Child(buildNode(edge.step, edge.kind))
		}
		return t
	}
	var parts []string
	for i := range d.Steps {
		step := &d.Steps[i]
		if !isChild[step.ID] {
			parts = append(parts, buildNode(step, "").String())
		}
	}
	return strings.Join(parts, "\n") + "\n"
}

func seqNodeLabel(d *sequence.Definition, step *sequence.Step, kind model.DepKind, showWorkDir, showAutomated bool) string {
	prefix := ""
	switch kind {
	case model.DepAfterSuccess:
		prefix = "✓ "
	case model.DepAfter:
		prefix = "→ "
	}
	command := displayCmd(step.Command)
	if step.Run != nil {
		command = *step.Run
	}
	label := lipgloss.NewStyle().Foreground(lipgloss.Color("244")).Render(step.ID + ":")
	lines := []string{prefix + label + " " + command}
	if showWorkDir {
		lines = append(lines, fmt.Sprintf("  cwd: %s", displayStepWorkDir(stepWorkDir(d, step))))
	}
	if showAutomated {
		lines = append(lines, fmt.Sprintf("  automated: %t", stepAutomated(d, step)))
	}
	return strings.Join(lines, "\n")
}

func displayStepWorkDir(workDir string) string {
	if workDir == "" {
		return "<default>"
	}
	return workDir
}
