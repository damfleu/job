package cli

import (
	"fmt"

	"github.com/spf13/cobra"
)

var runFlags RunFlags
var runCwd string

var runCmd = &cobra.Command{
	Use:     "run [flags] <command> [args...]",
	Aliases: []string{"r"},
	Short:   "Run a command as a job",
	Args:    cobra.ArbitraryArgs,
	RunE: func(cmd *cobra.Command, args []string) error {
		if len(args) == 0 {
			return cmd.Help()
		}
		workDir, err := resolveCwdFlag(runCwd)
		if err != nil {
			return fmt.Errorf("resolving --cwd: %w", err)
		}
		return launchJob(args, workDir, runFlags)
	},
}

func init() {
	addRunFlags(runCmd, &runFlags)
	addCwdFlag(runCmd, &runCwd, "run in a specific directory")
	runCmd.Flags().SetInterspersed(false)
	rootCmd.AddCommand(runCmd)
}
