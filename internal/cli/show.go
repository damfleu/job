package cli

import (
	"fmt"
	"strings"
	"time"

	"github.com/spf13/cobra"

	"job/internal/core"
	"job/internal/model"
)

var showCmd = &cobra.Command{
	Use:   "show [key]",
	Short: "Show full details for a job",
	Args:  cobra.MaximumNArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		j, err := core.ResolveKey(globalDB, keyArg(args))
		if err != nil {
			return err
		}
		printJob(j)
		return nil
	},
}

func init() {
	rootCmd.AddCommand(showCmd)
}

func printJob(j *model.Job) {
	field := func(label, value string) {
		fmt.Printf("%-12s %s\n", label+":", value)
	}

	field("Key", j.Key)
	if j.Alias != "" {
		field("Alias", j.Alias)
	}
	field("Command", strings.Join(j.Command, " "))
	field("WorkDir", j.WorkDir)
	field("Log", j.LogFile)
	field("Status", string(j.Status))
	if j.Reason != "" {
		field("Reason", string(j.Reason))
	}
	if j.ExitCode != nil {
		field("Exit code", fmt.Sprintf("%d", *j.ExitCode))
	}
	if j.PID != 0 {
		field("PID", fmt.Sprintf("%d", j.PID))
	}
	field("Host", j.Hostname)
	field("User", j.Username)
	if j.Context != "" {
		field("Context", j.Context)
	}
	field("Created", formatTime(j.CreatedAt))
	if j.StartedAt != nil {
		field("Started", formatTime(*j.StartedAt))
	}
	if j.StoppedAt != nil {
		field("Stopped", formatTime(*j.StoppedAt))
		field("Duration", j.StoppedAt.Sub(j.CreatedAt).Round(time.Millisecond).String())
	}
	if len(j.Deps) > 0 {
		for i, d := range j.Deps {
			label := "Dep"
			if i > 0 {
				label = ""
			}
			field(label, fmt.Sprintf("%s (%s)", d.Key, d.Kind))
		}
	}
}

func formatTime(t time.Time) string {
	return t.Local().Format("2006-01-02 15:04:05")
}
