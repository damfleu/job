package cli

import (
	"fmt"

	"github.com/spf13/cobra"

	"job/internal/core"
	"job/internal/model"
)

var rmCmd = &cobra.Command{
	Use:     "remove [key]",
	Aliases: []string{"rm"},
	Short:   "Delete a completed job",
	Args:  cobra.MaximumNArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		j, err := core.ResolveKey(globalDB, keyArg(args))
		if err != nil {
			return err
		}
		if j.Status != model.StatusCompleted {
			return fmt.Errorf("job %s is not completed (status: %s)", j.Key, j.Status)
		}
		return core.DeleteJob(globalDB, j)
	},
}

func init() {
	rootCmd.AddCommand(rmCmd)
}
