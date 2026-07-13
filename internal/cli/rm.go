package cli

import (
	"fmt"
	"strings"

	"github.com/spf13/cobra"

	"job/internal/core"
	"job/internal/model"
)

var rmCmd = &cobra.Command{
	Use:     "remove <key>...",
	Aliases: []string{"rm"},
	Short:   "Delete completed jobs",
	Args:    cobra.MinimumNArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		jobs, err := resolveRemoveJobs(cmd, args)
		if err != nil {
			return err
		}
		printRemovePreview(cmd, jobs)
		approved, err := confirmDestructive(cmd, rmYes)
		if err != nil {
			return err
		}
		if !approved {
			return nil
		}
		for _, j := range jobs {
			if err := core.DeleteJob(globalDB, j); err != nil {
				return err
			}
			printDeleted(cmd, j.Key, j.LogFile)
		}
		return nil
	},
}

var rmYes bool

func resolveRemoveJobs(cmd *cobra.Command, args []string) ([]*model.Job, error) {
	jobs := make([]*model.Job, 0, len(args))
	seen := make(map[string]bool, len(args))
	for _, arg := range args {
		j, err := resolveJobArg(cmd, []string{arg}, model.StatusCompleted)
		if err != nil {
			return nil, err
		}
		if seen[j.Key] {
			continue
		}
		if j.Status != model.StatusCompleted {
			return nil, fmt.Errorf("job %s is not completed (status: %s)", j.Key, j.Status)
		}
		seqs, err := globalDB.SequencesForJob(j.Key)
		if err != nil {
			return nil, err
		}
		if len(seqs) > 0 {
			return nil, fmt.Errorf("job %s is referenced by sequence(s): %s", j.Key, strings.Join(seqs, ", "))
		}
		seen[j.Key] = true
		jobs = append(jobs, j)
	}
	return jobs, nil
}

func printRemovePreview(cmd *cobra.Command, jobs []*model.Job) {
	w := cmd.ErrOrStderr()
	fmt.Fprintf(w, "About to delete %d completed job(s) and their log files:\n\n", len(jobs))
	for _, j := range jobs {
		fmt.Fprintf(w, "  %-36s  %s\n", j.Key, displayCmd(j.Command))
	}
	fmt.Fprintln(w)
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
	rmCmd.Flags().BoolVarP(&rmYes, "yes", "y", false, "delete without prompting")
	rootCmd.AddCommand(rmCmd)
}
