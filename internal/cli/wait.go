package cli

import (
	"fmt"
	"os"
	"time"

	"github.com/spf13/cobra"

	"job/internal/core"
	"job/internal/model"
)

const waitPollInterval = 250 * time.Millisecond

var waitCmd = &cobra.Command{
	Use:   "wait [key...]",
	Short: "Block until one or more jobs complete",
	Args:  cobra.ArbitraryArgs,
	RunE: func(cmd *cobra.Command, args []string) error {
		resolveCtx()
		keys, err := resolveWaitKeys(args)
		if err != nil {
			return err
		}

		jobs, err := core.WaitForCompletion(globalDB, keys, waitPollInterval)
		if err != nil {
			return err
		}

		for _, j := range jobs {
			fmt.Println(waitLine(j))
		}

		if code := waitExitCode(jobs); code != 0 {
			os.Exit(code)
		}
		return nil
	},
}

func init() {
	addAnyFlag(waitCmd)
	rootCmd.AddCommand(waitCmd)
}

// resolveWaitKeys resolves args to job keys, scoped to the current context
// unless --any was passed. With no args it falls back to the last running
// job, same default as log/show/stop.
func resolveWaitKeys(args []string) ([]string, error) {
	if len(args) == 0 {
		j, err := core.ResolveDefault(globalDB, hereCtx, model.StatusRunning)
		if err != nil {
			return nil, err
		}
		return []string{j.Key}, nil
	}

	keys := make([]string, len(args))
	for i, arg := range args {
		j, err := core.ResolveKey(globalDB, arg, hereCtx)
		if err != nil {
			return nil, err
		}
		keys[i] = j.Key
	}
	return keys, nil
}

// waitLine formats the convenience summary line printed for a completed job.
func waitLine(j *model.Job) string {
	line := fmt.Sprintf("%s  %s", j.Key, jobOutcome(j))
	if j.ExitCode != nil {
		line += fmt.Sprintf("  rc=%d", *j.ExitCode)
	}
	return line
}

// waitExitCode mirrors the outcome as a process exit code: for a single job,
// its own exit code (or 1 if it didn't exit normally); for multiple jobs, 0
// only if every job exited with rc 0.
func waitExitCode(jobs []*model.Job) int {
	if len(jobs) == 1 {
		return jobExitCode(jobs[0])
	}
	for _, j := range jobs {
		if jobExitCode(j) != 0 {
			return 1
		}
	}
	return 0
}

func jobExitCode(j *model.Job) int {
	if j.Reason == model.ReasonExited && j.ExitCode != nil {
		return *j.ExitCode
	}
	return 1
}
