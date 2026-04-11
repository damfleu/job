package cli

import (
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/jedib0t/go-pretty/v6/table"
	"github.com/jedib0t/go-pretty/v6/text"
	"github.com/spf13/cobra"

	"job/internal/model"
)

var lsAll bool

var lsCmd = &cobra.Command{
	Use:     "ls",
	Aliases: []string{"list"},
	Short:   "List jobs",
	RunE: func(cmd *cobra.Command, args []string) error {
		jobs, err := globalDB.ListActive()
		if err != nil {
			return err
		}

		if lsAll {
			completed, err := globalDB.ListCompleted(20)
			if err != nil {
				return err
			}
			jobs = append(jobs, completed...)
		}

		if len(jobs) == 0 {
			fmt.Fprintln(os.Stderr, "no jobs")
			return nil
		}

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
		return nil
	},
}

func init() {
	lsCmd.Flags().BoolVarP(&lsAll, "all", "a", false, "include completed jobs")
	rootCmd.AddCommand(lsCmd)
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
	switch {
	case j.StartedAt != nil:
		t = *j.StartedAt
	default:
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
