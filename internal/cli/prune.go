package cli

import (
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/spf13/cobra"

	"job/internal/core"
)

var pruneCmd = &cobra.Command{
	Use:   "prune",
	Short: "Delete old completed jobs and their log files",
	RunE:  runPrune,
}

var (
	pruneOlderThan string
	pruneBefore    string
)

func init() {
	pruneCmd.Flags().StringVar(&pruneOlderThan, "older-than", "", "delete jobs older than duration (e.g. 30d, 2w, 24h)")
	pruneCmd.Flags().StringVar(&pruneBefore, "before", "", "delete jobs that completed before this job")
	pruneCmd.MarkFlagsMutuallyExclusive("older-than", "before")
	rootCmd.AddCommand(pruneCmd)
}

func runPrune(cmd *cobra.Command, args []string) error {
	var cutoff time.Time
	switch {
	case pruneOlderThan != "":
		d, err := parseDuration(pruneOlderThan)
		if err != nil {
			return fmt.Errorf("--older-than: %w", err)
		}
		cutoff = time.Now().UTC().Add(-d)
	case pruneBefore != "":
		j, err := core.ResolveKey(globalDB, pruneBefore)
		if err != nil {
			return err
		}
		if j.StoppedAt == nil {
			return fmt.Errorf("job %s has no stop time", j.Key)
		}
		cutoff = *j.StoppedAt
	default:
		return fmt.Errorf("one of --older-than or --before is required")
	}

	jobs, err := globalDB.ListCompletedBefore(cutoff)
	if err != nil {
		return err
	}
	for _, j := range jobs {
		if err := core.DeleteJob(globalDB, j); err != nil {
			return fmt.Errorf("deleting %s: %w", j.Key, err)
		}
		printDeleted(cmd, j.Key, j.LogFile)
	}
	fmt.Fprintf(cmd.OutOrStdout(), "pruned %d job(s)\n", len(jobs))
	return nil
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
