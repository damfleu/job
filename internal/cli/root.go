package cli

import (
	"fmt"
	"os"
	"path/filepath"

	"github.com/adrg/xdg"
	"github.com/spf13/cobra"

	"job/internal/core"
	"job/internal/db"
	"job/internal/model"
)

var (
	verbose  bool
	runFlags RunFlags
	globalDB *db.DB
)

var rootCmd = &cobra.Command{
	Use:           "job",
	Short:         "Run and track background jobs",
	SilenceUsage:  true,
	SilenceErrors: true,
	Args:          cobra.ArbitraryArgs,
	RunE: func(cmd *cobra.Command, args []string) error {
		if len(args) == 0 {
			return cmd.Help()
		}
		return runJob(args)
	},
	PersistentPreRunE: func(cmd *cobra.Command, args []string) error {
		dir := filepath.Join(stateDir(), "db")
		if err := os.MkdirAll(dir, 0o755); err != nil {
			return fmt.Errorf("creating state dir: %w", err)
		}
		d, err := db.Open(filepath.Join(dir, "job.db"))
		if err != nil {
			return fmt.Errorf("opening db: %w", err)
		}
		globalDB = d
		return nil
	},
	PersistentPostRun: func(cmd *cobra.Command, args []string) {
		if globalDB != nil {
			globalDB.Close()
		}
	},
}

func Execute() {
	if err := rootCmd.Execute(); err != nil {
		fmt.Fprintln(os.Stderr, "error:", err)
		os.Exit(1)
	}
}

func runJob(command []string) error {
	deps, err := resolveDeps(runFlags.Deps)
	if err != nil {
		return err
	}
	opts := core.RunOptions{Alias: runFlags.Alias, Verbose: verbose, Deps: deps}
	if runFlags.Foreground {
		exitCode, err := core.CreateAndRunForeground(globalDB, stateDir(), command, opts)
		if err != nil {
			return err
		}
		if exitCode != 0 {
			os.Exit(exitCode)
		}
		return nil
	}
	key, err := core.CreateAndSpawn(globalDB, stateDir(), command, opts)
	if err != nil {
		return err
	}
	if verbose {
		fmt.Fprintln(os.Stderr, key)
	}
	return nil
}

// resolveDeps resolves each pending dep's key via fuzzy matching, preserving order.
func resolveDeps(pending []model.Dep) ([]model.Dep, error) {
	resolved := make([]model.Dep, len(pending))
	for i, dep := range pending {
		j, err := core.ResolveKey(globalDB, dep.Key)
		if err != nil {
			return nil, fmt.Errorf("resolving dep %q: %w", dep.Key, err)
		}
		resolved[i] = model.Dep{Key: j.Key, Kind: dep.Kind}
	}
	return resolved, nil
}

// keyArg returns the first element of args, or "." if args is empty.
func keyArg(args []string) string {
	if len(args) > 0 {
		return args[0]
	}
	return "."
}

func stateDir() string {
	if dir := os.Getenv("JOB_STATE_DIR"); dir != "" {
		return dir
	}
	return filepath.Join(xdg.DataHome, "job")
}

func init() {
	rootCmd.PersistentFlags().BoolVarP(&verbose, "verbose", "v", false, "print job key and status")
	addRunFlags(rootCmd, &runFlags)
}
