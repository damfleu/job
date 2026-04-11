package cli

import (
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
		return core.StopJob(globalDB, j.Key)
	},
}

func init() {
	rootCmd.AddCommand(stopCmd)
}
