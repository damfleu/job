package cli

import (
	"fmt"
	"io"
	"os"

	"github.com/spf13/cobra"
)

var logPath bool

var logCmd = &cobra.Command{
	Use:   "log [key]",
	Short: "Display log output for a job",
	Args:  cobra.MaximumNArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		j, err := resolveJobArg(cmd, args)
		if err != nil {
			return err
		}

		if logPath {
			fmt.Println(j.LogFile)
			return nil
		}

		f, err := os.Open(j.LogFile)
		if err != nil {
			return fmt.Errorf("opening log: %w", err)
		}
		defer f.Close()

		_, err = io.Copy(os.Stdout, f)
		return err
	},
}

func init() {
	logCmd.Flags().BoolVarP(&logPath, "path", "p", false, "print log file path only")
	addHereFlag(logCmd)
	rootCmd.AddCommand(logCmd)
}
