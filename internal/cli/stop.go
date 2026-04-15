package cli

import (
	"fmt"

	"github.com/spf13/cobra"

	"job/internal/core"
)

var stopCmd = &cobra.Command{
	Use:   "stop [key]",
	Short: "Stop a running job",
	Args:  cobra.MaximumNArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		j, err := core.ResolveKey(globalDB, keyArg(args))
		if err != nil {
			return err
		}
		if err := core.StopJob(globalDB, j.Key); err != nil {
			return err
		}
		fmt.Fprintf(cmd.OutOrStdout(), "stopped %s\n", j.Key)
		return nil
	},
}

func init() {
	rootCmd.AddCommand(stopCmd)
}
