package cli

import (
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/charmbracelet/lipgloss"
	"github.com/charmbracelet/lipgloss/tree"
	"github.com/jedib0t/go-pretty/v6/table"
	"github.com/jedib0t/go-pretty/v6/text"
	"github.com/spf13/cobra"

	"job/internal/model"
)

var lsAll bool

var lsCmd = &cobra.Command{
	Use:     "list",
	Aliases: []string{"ls"},
	Short:   "List jobs",
	RunE: func(cmd *cobra.Command, args []string) error {
		active, err := globalDB.ListActive()
		if err != nil {
			return err
		}

		// -a or no active jobs: show table
		if lsAll || len(active) == 0 {
			var jobs []*model.Job
			if !lsAll {
				// fallback: completed only
				jobs, err = globalDB.ListCompleted(20)
			} else {
				jobs = append(active, func() []*model.Job {
					c, _ := globalDB.ListCompleted(20)
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
	t := table.NewWriter()
	t.SetOutputMirror(os.Stdout)
	t.SetStyle(table.StyleLight)
	t.AppendHeader(table.Row{"KEY", "STATUS", "COMMAND", "AGE"})
	for _, j := range jobs {
		t.AppendRow(table.Row{
			displayKey(j),
			statusColor(j.Status).Sprint(string(j.Status)),
			displayCmd(j.Command),
			displayAge(j),
		})
	}
	t.Render()
}

func displayKey(j *model.Job) string {
	if j.Alias != "" {
		return j.Alias
	}
	return j.Key
}

func displayCmd(cmd []string) string {
	s := strings.Join(cmd, " ")
	if len(s) > 50 {
		return s[:47] + "..."
	}
	return s
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

// statusColor is used in the table view (go-pretty colors).
func statusColor(s model.Status) text.Colors {
	switch s {
	case model.StatusRunning:
		return text.Colors{text.FgGreen}
	case model.StatusBlocked:
		return text.Colors{text.FgYellow}
	case model.StatusCompleted:
		return text.Colors{text.FgHiBlack}
	default:
		return text.Colors{text.FgHiWhite}
	}
}
