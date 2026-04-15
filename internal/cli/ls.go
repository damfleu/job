package cli

import (
	"fmt"
	"os"
	"regexp"
	"strings"
	"time"

	"github.com/charmbracelet/lipgloss"
	"github.com/charmbracelet/lipgloss/tree"
	"github.com/charmbracelet/x/term"
	"github.com/jedib0t/go-pretty/v6/table"
	"github.com/jedib0t/go-pretty/v6/text"
	"github.com/spf13/cobra"

	"job/internal/config"
	"job/internal/core"
	"job/internal/model"
)

var (
	lsAll    bool
	lsFilter string
	lsLimit  int
	lsHere   bool
)

var lsCmd = &cobra.Command{
	Use:     "list",
	Aliases: []string{"ls"},
	Short:   "List jobs",
	RunE: func(cmd *cobra.Command, args []string) error {
		if lsFilter != "" {
			if _, err := regexp.Compile(lsFilter); err != nil {
				return fmt.Errorf("invalid filter: %w", err)
			}
		}

		limit := lsLimit
		if !cmd.Flags().Changed("limit") {
			limit = globalConfig.List.Limit
		}

		var ctx string
		if lsHere {
			cwd, err := os.Getwd()
			if err != nil {
				return fmt.Errorf("getting cwd: %w", err)
			}
			ctx = core.ResolveContext(cwd, globalConfig.Context.Resolvers)
		}

		active, err := globalDB.ListActive(lsFilter, ctx)
		if err != nil {
			return err
		}

		// -a or no active jobs: show table
		if lsAll || len(active) == 0 {
			var jobs []*model.Job
			if !lsAll {
				// fallback: completed only
				jobs, err = globalDB.ListCompleted(limit, lsFilter, ctx)
			} else {
				jobs = append(active, func() []*model.Job {
					c, _ := globalDB.ListCompleted(limit, lsFilter, ctx)
					return c
				}()...)
			}
			if err != nil {
				return err
			}
			if len(jobs) == 0 {
				fmt.Fprintln(os.Stderr, "no jobs")
				return nil
			}
			printTable(jobs)
			return nil
		}

		// active jobs only: tree
		fmt.Print(renderTree(active))
		return nil
	},
}

func init() {
	lsCmd.Flags().BoolVarP(&lsAll, "all", "a", false, "include completed jobs")
	lsCmd.Flags().StringVarP(&lsFilter, "filter", "f", "", "filter by command regex")
	lsCmd.Flags().IntVarP(&lsLimit, "limit", "n", config.Default().List.Limit, "max completed jobs to show")
	lsCmd.Flags().BoolVar(&lsHere, "here", false, "filter to jobs in the current context")
	rootCmd.AddCommand(lsCmd)
}

// renderTree builds a lipgloss tree from a set of active jobs and returns
// the rendered string.
func renderTree(jobs []*model.Job) string {
	byKey := make(map[string]*model.Job, len(jobs))
	for _, j := range jobs {
		byKey[j.Key] = j
	}

	children := make(map[string][]*model.Job)
	isChild := make(map[string]bool)
	for _, j := range jobs {
		for _, dep := range j.Deps {
			if _, active := byKey[dep.Key]; active {
				children[dep.Key] = append(children[dep.Key], j)
				isChild[j.Key] = true
			}
		}
	}

	var roots []*model.Job
	for _, j := range jobs {
		if !isChild[j.Key] {
			roots = append(roots, j)
		}
	}

	var buildNode func(j *model.Job) *tree.Tree
	buildNode = func(j *model.Job) *tree.Tree {
		t := tree.Root(nodeLabel(j))
		for _, kid := range children[j.Key] {
			t.Child(buildNode(kid))
		}
		return t
	}

	var parts []string
	for _, root := range roots {
		parts = append(parts, buildNode(root).String())
	}
	return strings.Join(parts, "\n") + "\n"
}

func nodeLabel(j *model.Job) string {
	return fmt.Sprintf("%s  %s  %s  %s",
		displayKey(j),
		statusStyle(j.Status).Render(string(j.Status)),
		displayCmd(j.Command),
		displayAge(j),
	)
}

func printTable(jobs []*model.Job) {
	isTTY := term.IsTerminal(os.Stdout.Fd())

	t := table.NewWriter()
	t.SetOutputMirror(os.Stdout)
	t.AppendHeader(table.Row{"KEY", "STATUS", "RC", "COMMAND", "TIME", "DURATION"})
	for _, j := range jobs {
		status := jobStatusText(j)
		if isTTY {
			status = jobStatusStyle(j).Render(status)
		}
		t.AppendRow(table.Row{
			displayKeyAlias(j),
			status,
			displayExitCode(j),
			strings.Join(j.Command, " "),
			displayTimestamp(j),
			displayDuration(j),
		})
	}

	if !isTTY {
		t.RenderTSV()
		return
	}

	termWidth, _, _ := term.GetSize(os.Stdout.Fd())
	t.SetStyle(jobTableStyle())
	configs := []table.ColumnConfig{
		{Name: "RC", WidthMax: 3, WidthMin: 1},
	}
	// Measure natural widths of all non-COMMAND columns to give COMMAND the rest.
	keyW, statusW, rcW, timeW, durW := len("KEY"), len("STATUS"), len("RC"), len("TIME"), len("DURATION")
	for _, j := range jobs {
		keyW    = max(keyW, len(displayKeyAlias(j)))
		statusW = max(statusW, len(jobStatusText(j)))
		rcW     = max(rcW, min(len(displayExitCode(j)), 3))
		timeW   = max(timeW, len(displayTimestamp(j)))
		durW    = max(durW, len(displayDuration(j)))
	}
	// 6 cols × 2 padding + 7 border chars (left + 5 separators + right) = 19 overhead.
	cmdMax := termWidth - (keyW + statusW + rcW + timeW + durW + 19)
	if cmdMax >= len("COMMAND") {
		configs = append(configs, table.ColumnConfig{
			Name: "COMMAND", WidthMax: cmdMax, WidthMaxEnforcer: ellipsisTrunc,
		})
	}
	t.SetColumnConfigs(configs)
	t.Render()
}

// ellipsisTrunc truncates s to maxLen characters, appending "..." when cut.
func ellipsisTrunc(s string, maxLen int) string {
	if len(s) <= maxLen {
		return s
	}
	if maxLen <= 3 {
		return s[:maxLen]
	}
	return s[:maxLen-3] + "..."
}

func jobTableStyle() table.Style {
	return table.Style{
		Name: "job",
		Box:  table.StyleBoxLight,
		Color: table.ColorOptions{
			Border: text.Colors{text.FgHiBlack},
			Header: text.Colors{text.Bold},
		},
		Format: table.FormatOptions{
			Header: text.FormatDefault,
		},
		Options: table.OptionsDefault,
	}
}

func displayKey(j *model.Job) string {
	return j.Key
}

func displayKeyAlias(j *model.Job) string {
	if j.Alias != "" {
		return j.Alias
	}
	return j.Key
}

func displayCmd(cmd []string) string {
	return truncate(strings.Join(cmd, " "), 60)
}

func truncate(s string, max int) string {
	if len(s) > max {
		return s[:max-3] + "..."
	}
	return s
}

func displayExitCode(j *model.Job) string {
	if j.ExitCode == nil {
		return ""
	}
	return fmt.Sprintf("%d", *j.ExitCode)
}

func displayTimestamp(j *model.Job) string {
	switch {
	case j.StoppedAt != nil:
		return formatTime(*j.StoppedAt)
	case j.StartedAt != nil:
		return formatTime(*j.StartedAt)
	default:
		return formatTime(j.CreatedAt)
	}
}

func displayDuration(j *model.Job) string {
	if j.StartedAt == nil {
		return ""
	}
	end := time.Now()
	if j.StoppedAt != nil {
		end = *j.StoppedAt
	}
	return end.Sub(*j.StartedAt).Round(time.Millisecond).String()
}

func displayAge(j *model.Job) string {
	var t time.Time
	if j.StartedAt != nil {
		t = *j.StartedAt
	} else {
		t = j.CreatedAt
	}
	return age(t)
}

func age(t time.Time) string {
	d := time.Since(t)
	switch {
	case d < time.Minute:
		return fmt.Sprintf("%ds", int(d.Seconds()))
	case d < time.Hour:
		return fmt.Sprintf("%dm", int(d.Minutes()))
	case d < 24*time.Hour:
		return fmt.Sprintf("%dh", int(d.Hours()))
	default:
		return fmt.Sprintf("%dd", int(d.Hours()/24))
	}
}

// statusStyle is used in the tree view for active jobs.
func statusStyle(s model.Status) lipgloss.Style {
	switch s {
	case model.StatusRunning:
		return lipgloss.NewStyle().Foreground(lipgloss.Color("2"))
	case model.StatusBlocked:
		return lipgloss.NewStyle().Foreground(lipgloss.Color("3"))
	default:
		return lipgloss.NewStyle()
	}
}

// jobStatusText returns the display text for the STATUS column.
// Completed jobs show their reason instead of "completed".
func jobStatusText(j *model.Job) string {
	if j.Status == model.StatusCompleted {
		return string(j.Reason)
	}
	return string(j.Status)
}

// jobStatusStyle returns the color for the STATUS column.
func jobStatusStyle(j *model.Job) lipgloss.Style {
	switch j.Status {
	case model.StatusRunning:
		return lipgloss.NewStyle().Foreground(lipgloss.Color("2"))
	case model.StatusBlocked:
		return lipgloss.NewStyle().Foreground(lipgloss.Color("3"))
	case model.StatusCompleted:
		switch j.Reason {
		case model.ReasonStopped:
			return lipgloss.NewStyle().Foreground(lipgloss.Color("208"))
		case model.ReasonDepFailed:
			return lipgloss.NewStyle().Foreground(lipgloss.Color("1"))
		case model.ReasonExited:
			if j.ExitCode != nil && *j.ExitCode != 0 {
				return lipgloss.NewStyle().Foreground(lipgloss.Color("1"))
			}
			return lipgloss.NewStyle().Foreground(lipgloss.Color("240"))
		}
	}
	return lipgloss.NewStyle()
}

