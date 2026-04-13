package cli

import "github.com/spf13/cobra"

var runFlags RunFlags

var runCmd = &cobra.Command{
	Use:     "run [flags] <command> [args...]",
	Aliases: []string{"r"},
	Short:   "Run a command as a job",
	Args:    cobra.ArbitraryArgs,
	RunE: func(cmd *cobra.Command, args []string) error {
		if len(args) == 0 {
			return cmd.Help()
		}
		return launchJob(args, "", runFlags)
	},
}

func init() {
	addRunFlags(runCmd, &runFlags)
	runCmd.Flags().SetInterspersed(false)
	rootCmd.AddCommand(runCmd)
}
