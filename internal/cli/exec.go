package cli

import (
	"fmt"
	"time"

	"github.com/spf13/cobra"

	"job/internal/core"
	"job/internal/notify"
)

var execNotifiers []string
var execNotifierTimeouts []time.Duration

var execCmd = &cobra.Command{
	Use:    "__exec",
	Hidden: true,
	Args:   cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		if len(execNotifiers) != len(execNotifierTimeouts) {
			return fmt.Errorf("invalid internal notifier configuration")
		}
		notifiers := make([]notify.Notifier, len(execNotifiers))
		for i, program := range execNotifiers {
			notifiers[i] = notify.Notifier{Program: program, Timeout: execNotifierTimeouts[i]}
		}
		return core.RunBackground(globalDB, stateDir(), args[0], notifiers)
	},
}

func init() {
	execCmd.Flags().StringArrayVar(&execNotifiers, "notifier", nil, "")
	execCmd.Flags().DurationSliceVar(&execNotifierTimeouts, "notifier-timeout", nil, "")
	rootCmd.AddCommand(execCmd)
}
