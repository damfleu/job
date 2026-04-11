package cli

import (
	"github.com/spf13/cobra"

	"job/internal/core"
)

var execCmd = &cobra.Command{
	Use:    "__exec",
	Hidden: true,
	Args:   cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		return core.RunBackground(globalDB, args[0])
	},
}

func init() {
	rootCmd.AddCommand(execCmd)
}
