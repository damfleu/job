package cli

import (
	"fmt"
	"os"
	"regexp"
	"strings"
	"time"

	"charm.land/lipgloss/v2"
	"charm.land/lipgloss/v2/tree"
	"github.com/charmbracelet/x/term"
	"github.com/jedib0t/go-pretty/v6/table"
	"github.com/jedib0t/go-pretty/v6/text"
	"github.com/spf13/cobra"

	"job/internal/config"
	"job/internal/db"
	"job/internal/model"
)

var (
	lsAll    bool
	lsFilter string
	lsLimit  int
	lsJSON   bool
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

		resolveCtx()
		active, err := globalDB.ListActive(lsFilter, hereCtx)
		if err != nil {
			return err
		}

		if lsJSON {
			completed, err := globalDB.ListCompleted(limit, lsFilter, hereCtx)
			if err != nil {
				return err
			}
			jobs := append(active, completed...)
			views := make([]jobView, len(jobs))
			for i, j := range jobs {
				views[i] = toJobView(j)
			}
			return printJSON(views)
		}

		// -a or no active jobs: show table
		if lsAll || len(active) == 0 {
			var jobs []*model.Job
			if !lsAll {
				// fallback: completed only
				jobs, err = globalDB.ListCompleted(limit, lsFilter, hereCtx)
			} else {
				jobs = append(active, func() []*model.Job {
					c, _ := globalDB.ListCompleted(limit, lsFilter, hereCtx)
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
		all, err := expandDeps(globalDB, active)
		if err != nil {
			return err
		}
		fmt.Print(renderTree(all))
		return nil
	},
}

func init() {
	lsCmd.Flags().BoolVarP(&lsAll, "all", "a", false, "include completed jobs")
	lsCmd.Flags().StringVarP(&lsFilter, "filter", "f", "", "filter by command regex")
	lsCmd.Flags().IntVarP(&lsLimit, "limit", "n", config.Default().List.Limit, "max completed jobs to show")
	lsCmd.Flags().BoolVar(&lsJSON, "json", false, "output as JSON")
	addAnyFlag(lsCmd)
	rootCmd.AddCommand(lsCmd)
}

// renderTree builds a lipgloss tree from a set of active jobs and returns the rendered string.
func renderTree(jobs []*model.Job) string {
	showContext := hasMultipleContexts(jobs)
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
		t := tree.Root(nodeLabel(j, showContext))
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

func nodeLabel(j *model.Job, showContext bool) string {
	label := fmt.Sprintf("%s  %s  %s  %s",
		displayKey(j),
		jobStatusStyle(j).Render(jobStatusText(j)),
		displayCmd(j.Command),
		displayAge(j),
	)
	if showContext {
		return fmt.Sprintf("[%s]  %s", middleEllipsisTrunc(displayContext(j), 24), label)
	}
	return label
}

// expandDeps augments a set of jobs with their transitive completed dependencies.
func expandDeps(d *db.DB, seed []*model.Job) ([]*model.Job, error) {
	byKey := make(map[string]*model.Job, len(seed))
	for _, j := range seed {
		byKey[j.Key] = j
	}

	var pending []string
	for _, j := range seed {
		for _, dep := range j.Deps {
			if _, seen := byKey[dep.Key]; !seen {
				byKey[dep.Key] = nil
				pending = append(pending, dep.Key)
			}
		}
	}

	for len(pending) > 0 {
		fetched, err := d.GetByKeys(pending)
		if err != nil {
			return nil, err
		}
		pending = pending[:0]
		for _, j := range fetched {
			byKey[j.Key] = j
			for _, dep := range j.Deps {
				if _, seen := byKey[dep.Key]; !seen {
					byKey[dep.Key] = nil
					pending = append(pending, dep.Key)
				}
			}
		}
	}

	result := make([]*model.Job, 0, len(byKey))
	for _, j := range byKey {
		if j != nil {
			result = append(result, j)
		}
	}
	return result, nil
}

func printTable(jobs []*model.Job) {
	isTTY := term.IsTerminal(os.Stdout.Fd())
	showContext := hasMultipleContexts(jobs)

	t := table.NewWriter()
	t.SetOutputMirror(os.Stdout)
	header := table.Row{"KEY"}
	if showContext {
		header = append(header, "CONTEXT")
	}
	header = append(header, "STATUS", "RC", "COMMAND", "TIME", "DURATION")
	t.AppendHeader(header)
	for _, j := range jobs {
		status := jobStatusText(j)
		if isTTY {
			status = jobStatusStyle(j).Render(status)
		}
		row := table.Row{displayKeyAlias(j)}
		if showContext {
			row = append(row, displayContext(j))
		}
		row = append(row,
			status,
			displayExitCode(j),
			strings.Join(j.Command, " "),
			displayTimestamp(j),
			displayDuration(j),
		)
		t.AppendRow(row)
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
	if showContext {
		configs = append(configs, table.ColumnConfig{
			Name: "CONTEXT", WidthMax: 24, WidthMin: len("CONTEXT"), WidthMaxEnforcer: middleEllipsisTrunc,
		})
	}
	// Measure natural widths of all non-COMMAND columns to give COMMAND the rest.
	keyW, contextW, statusW, rcW, timeW, durW := len("KEY"), 0, len("STATUS"), len("RC"), len("TIME"), len("DURATION")
	for _, j := range jobs {
		keyW = max(keyW, len(displayKeyAlias(j)))
		if showContext {
			contextW = max(contextW, min(len(displayContext(j)), 24))
		}
		statusW = max(statusW, len(jobStatusText(j)))
		rcW = max(rcW, min(len(displayExitCode(j)), 3))
		timeW = max(timeW, len(displayTimestamp(j)))
		durW = max(durW, len(displayDuration(j)))
	}
	columnCount := 6
	if showContext {
		columnCount++
		contextW = max(contextW, len("CONTEXT"))
	}
	// Each column has two padding characters, plus one border on each side and
	// between columns.
	overhead := 3*columnCount + 1
	cmdMax := termWidth - (keyW + contextW + statusW + rcW + timeW + durW + overhead)
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

// middleEllipsisTrunc preserves both ends of identifiers such as paths and
// session IDs, which often differ near the end.
func middleEllipsisTrunc(s string, maxLen int) string {
	if len(s) <= maxLen {
		return s
	}
	if maxLen <= 3 {
		return s[:maxLen]
	}
	left := (maxLen - 3 + 1) / 2
	right := maxLen - 3 - left
	return s[:left] + "..." + s[len(s)-right:]
}

func hasMultipleContexts(jobs []*model.Job) bool {
	if len(jobs) < 2 {
		return false
	}
	context := jobs[0].Context
	for _, j := range jobs[1:] {
		if j.Context != context {
			return true
		}
	}
	return false
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

func displayContext(j *model.Job) string {
	if j.Context == "" {
		return "-"
	}
	return j.Context
}

func displayCmd(cmd []string) string {
	return ellipsisTrunc(strings.Join(cmd, " "), 60)
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

// jobStatusText returns the display text for the STATUS column.
// Completed jobs show their reason instead of "completed".
func jobStatusText(j *model.Job) string {
	if j.Status == model.StatusCompleted {
		if j.Reason == model.ReasonExited {
			if j.ExitCode != nil && *j.ExitCode == 0 {
				return "completed"
			}
			return "failed"
		}
		return string(j.Reason)
	}
	return string(j.Status)
}

// jobStatusStyle returns the color for the STATUS column.
func jobStatusStyle(j *model.Job) lipgloss.Style {
	switch j.Status {
	case model.StatusRunning:
		return lipgloss.NewStyle().Foreground(lipgloss.Color("4"))
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
			return lipgloss.NewStyle().Foreground(lipgloss.Color("2"))
		}
	}
	return lipgloss.NewStyle()
}
