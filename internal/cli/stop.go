package cli

import (
	"fmt"

	"github.com/spf13/cobra"

	"job/internal/core"
	"job/internal/model"
)

var stopCmd = &cobra.Command{
	Use:   "stop [key]",
	Short: "Stop a running job",
	Args:  cobra.MaximumNArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		j, err := resolveJobArg(cmd, args, model.StatusRunning)
		if err != nil {
			return err
		}

		// Snapshot running deps before stopping so we can warn about them.
		var runningDeps []string
		if j.Status == model.StatusBlocked {
			for _, dep := range j.Deps {
				depJob, err := globalDB.Get(dep.Key)
				if err == nil && depJob.Status != model.StatusCompleted {
					runningDeps = append(runningDeps, dep.Key)
				}
			}
		}

		if err := core.StopJob(globalDB, j.Key); err != nil {
			return err
		}
		fmt.Fprintf(cmd.OutOrStdout(), "stopped %s\n", j.Key)
		for _, depKey := range runningDeps {
			fmt.Fprintf(cmd.OutOrStdout(), "  (dependency %s is still running)\n", depKey)
		}
		return nil
	},
}

func init() {
	addHereFlag(stopCmd)
	addSelectFlag(stopCmd)
	rootCmd.AddCommand(stopCmd)
}
