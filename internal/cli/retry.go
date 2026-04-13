package cli

import (
	"fmt"

	"github.com/spf13/cobra"

	"job/internal/core"
	"job/internal/model"
)

var retryFlags RunFlags

var retryCmd = &cobra.Command{
	Use:   "retry [key]",
	Short: "Re-run a completed job",
	Args:  cobra.MaximumNArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		j, err := core.ResolveKey(globalDB, keyArg(args))
		if err != nil {
			return err
		}
		if j.Status != model.StatusCompleted {
			return fmt.Errorf("job %s is not completed (status: %s)", j.Key, j.Status)
		}
		return launchJob(j.Command, j.WorkDir, retryFlags)
	},
}

func init() {
	addRunFlags(retryCmd, &retryFlags)
	rootCmd.AddCommand(retryCmd)
}
