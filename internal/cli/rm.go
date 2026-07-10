package cli

import (
	"fmt"
	"strings"

	"github.com/spf13/cobra"

	"job/internal/core"
	"job/internal/model"
)

var rmCmd = &cobra.Command{
	Use:     "remove [key]",
	Aliases: []string{"rm"},
	Short:   "Delete a completed job",
	Args:    cobra.MaximumNArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		j, err := resolveJobArg(cmd, args, model.StatusCompleted)
		if err != nil {
			return err
		}
		if j.Status != model.StatusCompleted {
			return fmt.Errorf("job %s is not completed (status: %s)", j.Key, j.Status)
		}
		seqs, err := globalDB.SequencesForJob(j.Key)
		if err != nil {
			return err
		}
		if len(seqs) > 0 {
			return fmt.Errorf("job %s is referenced by sequence(s): %s", j.Key, strings.Join(seqs, ", "))
		}
		if err := core.DeleteJob(globalDB, j); err != nil {
			return err
		}
		printDeleted(cmd, j.Key, j.LogFile)
		return nil
	},
}

func printDeleted(cmd *cobra.Command, key, logFile string) {
	fmt.Fprintf(cmd.OutOrStdout(), "removed %s\n", key)
	if logFile != "" {
		fmt.Fprintf(cmd.OutOrStdout(), "  log: %s\n", logFile)
	}
}

func init() {
	addAnyFlag(rmCmd)
	addSelectFlag(rmCmd)
	rootCmd.AddCommand(rmCmd)
}
