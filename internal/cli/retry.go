package cli

import (
	"fmt"

	"github.com/spf13/cobra"

	"job/internal/core"
	"job/internal/model"
)

var retryFlags RunFlags
var retryCwd string
var retryCascade bool

var retryCmd = &cobra.Command{
	Use:   "retry [key]",
	Short: "Re-run a completed job",
	Args:  cobra.MaximumNArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		j, err := resolveJobArg(cmd, args, model.StatusCompleted)
		if err != nil {
			return err
		}
		if j.Status != model.StatusCompleted {
			return fmt.Errorf("job %s is not completed (status: %s)", j.Key, j.Status)
		}
		if retryCascade {
			return retryCascadeJob(cmd, j)
		}
		workDir := j.WorkDir
		if retryCwd != "" {
			var err error
			workDir, err = resolveCwdFlag(retryCwd)
			if err != nil {
				return fmt.Errorf("resolving --cwd: %w", err)
			}
		}
		return launchJob(j.Command, workDir, retryFlags)
	},
}

// retryCascadeJob retries j and every job that failed as a result of j's failure (transitively),
// remapping their deps to point at the newly retried keys as it goes.
func retryCascadeJob(cmd *cobra.Command, j *model.Job) error {
	steps, err := core.ExpandDependentCascade(globalDB, j.Key)
	if err != nil {
		return fmt.Errorf("cascade retry: %w", err)
	}
	newKeys, err := runSteps(steps, retryFlags.Deps, retryFlags.Notify)
	if err != nil {
		return fmt.Errorf("cascade retry: %w", err)
	}
	printStepKeys(cmd.OutOrStdout(), newKeys)
	return nil
}

func init() {
	addRunFlags(retryCmd, &retryFlags)
	addHereFlag(retryCmd)
	addSelectFlag(retryCmd)
	addCwdFlag(retryCmd, &retryCwd, "run in a directory instead of the original")
	retryCmd.Flags().BoolVarP(&retryCascade, "cascade", "c", false, "also retry every job that failed as a result of this one")
	retryCmd.MarkFlagsMutuallyExclusive("cascade", "foreground")
	retryCmd.MarkFlagsMutuallyExclusive("cascade", "watch")
	retryCmd.MarkFlagsMutuallyExclusive("cascade", "quiet")
	retryCmd.MarkFlagsMutuallyExclusive("cascade", "key")
	retryCmd.MarkFlagsMutuallyExclusive("cascade", "cwd")
	rootCmd.AddCommand(retryCmd)
}
