package cli

import (
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/spf13/cobra"

	"job/internal/core"
	"job/internal/model"
)

var pruneCmd = &cobra.Command{
	Use:   "prune",
	Short: "Delete old completed jobs and their log files",
	RunE:  runPrune,
}

var (
	pruneOlderThan string
	pruneBefore    string
	pruneYes       bool
)

func init() {
	pruneCmd.Flags().StringVar(&pruneOlderThan, "older-than", "", "delete jobs older than duration (e.g. 30d, 2w, 24h)")
	pruneCmd.Flags().StringVar(&pruneBefore, "before", "", "delete jobs that completed before this job")
	pruneCmd.MarkFlagsMutuallyExclusive("older-than", "before")
	pruneCmd.MarkFlagsOneRequired("older-than", "before")
	addAnyFlag(pruneCmd)
	pruneCmd.Flags().BoolVarP(&pruneYes, "yes", "y", false, "delete without prompting")
	rootCmd.AddCommand(pruneCmd)
}

func runPrune(cmd *cobra.Command, args []string) error {
	resolveCtx()
	var cutoff time.Time
	switch {
	case pruneOlderThan != "":
		d, err := parseDuration(pruneOlderThan)
		if err != nil {
			return fmt.Errorf("--older-than: %w", err)
		}
		cutoff = time.Now().UTC().Add(-d)
	case pruneBefore != "":
		j, err := core.ResolveKey(globalDB, pruneBefore, hereCtx)
		if err != nil {
			return err
		}
		if j.StoppedAt == nil {
			return fmt.Errorf("job %s has no stop time", j.Key)
		}
		cutoff = *j.StoppedAt
	}

	jobs, err := globalDB.ListCompletedBefore(cutoff, hereCtx)
	if err != nil {
		return err
	}
	deletable := make([]*model.Job, 0, len(jobs))
	skipped := 0
	for _, j := range jobs {
		// Prevent deleting jobs that are referenced by sequences so that sequences remain runnable.
		seqs, err := globalDB.SequencesForJob(j.Key)
		if err != nil {
			return fmt.Errorf("checking refs for %s: %w", j.Key, err)
		}
		if len(seqs) > 0 {
			fmt.Fprintf(cmd.ErrOrStderr(), "skipping %s: referenced by sequence(s): %s\n", j.Key, strings.Join(seqs, ", "))
			skipped++
			continue
		}
		deletable = append(deletable, j)
	}

	if len(deletable) > 0 {
		printPrunePreview(cmd, deletable)
		approved, err := confirmDestructive(cmd, pruneYes)
		if err != nil {
			return err
		}
		if !approved {
			return nil
		}
	}

	pruned := 0
	for _, j := range deletable {
		if err := core.DeleteJob(globalDB, j); err != nil {
			return fmt.Errorf("deleting %s: %w", j.Key, err)
		}
		printDeleted(cmd, j.Key, j.LogFile)
		pruned++
	}
	if skipped > 0 {
		fmt.Fprintf(cmd.OutOrStdout(), "pruned %d job(s), skipped %d referenced by sequences\n", pruned, skipped)
	} else {
		fmt.Fprintf(cmd.OutOrStdout(), "pruned %d job(s)\n", pruned)
	}
	return nil
}

const prunePreviewLimit = 20

func printPrunePreview(cmd *cobra.Command, jobs []*model.Job) {
	w := cmd.ErrOrStderr()
	fmt.Fprintf(w, "About to delete %d completed job(s) and their log files:\n\n", len(jobs))
	limit := min(len(jobs), prunePreviewLimit)
	for _, j := range jobs[:limit] {
		fmt.Fprintf(w, "  %-36s  %s\n", j.Key, displayCmd(j.Command))
	}
	if remaining := len(jobs) - limit; remaining > 0 {
		fmt.Fprintf(w, "  ... and %d more\n", remaining)
	}
	fmt.Fprintln(w)
}

// parseDuration extends time.ParseDuration with support for days (d) and weeks (w).
func parseDuration(s string) (time.Duration, error) {
	if d, err := time.ParseDuration(s); err == nil {
		return d, nil
	}
	lower := strings.ToLower(s)
	if strings.HasSuffix(lower, "w") {
		n, err := strconv.ParseFloat(strings.TrimSuffix(lower, "w"), 64)
		if err != nil {
			return 0, fmt.Errorf("invalid duration %q", s)
		}
		return time.Duration(n * float64(7*24*time.Hour)), nil
	}
	if strings.HasSuffix(lower, "d") {
		n, err := strconv.ParseFloat(strings.TrimSuffix(lower, "d"), 64)
		if err != nil {
			return 0, fmt.Errorf("invalid duration %q", s)
		}
		return time.Duration(n * float64(24*time.Hour)), nil
	}
	return 0, fmt.Errorf("invalid duration %q", s)
}
