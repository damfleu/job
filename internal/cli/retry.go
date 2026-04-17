package cli

import (
	"fmt"

	"github.com/spf13/cobra"

	"job/internal/core"
	"job/internal/model"
)

var retryFlags RunFlags
var retryCwd bool

var retryCmd = &cobra.Command{
	Use:   "retry [key]",
	Short: "Re-run a completed job",
	Args:  cobra.MaximumNArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		resolveHereCtx()
		j, err := core.ResolveKey(globalDB, keyArg(args), hereCtx)
		if err != nil {
			return err
		}
		if j.Status != model.StatusCompleted {
			return fmt.Errorf("job %s is not completed (status: %s)", j.Key, j.Status)
		}
		workDir := j.WorkDir
		if retryCwd {
			workDir = ""
		}
		return launchJob(j.Command, workDir, retryFlags)
	},
}

func init() {
	addRunFlags(retryCmd, &retryFlags)
	addHereFlag(retryCmd)
	retryCmd.Flags().BoolVar(&retryCwd, "cwd", false, "run in the current directory instead of the original")
	rootCmd.AddCommand(retryCmd)
}
