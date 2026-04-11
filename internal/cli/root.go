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
	verbose         bool
	foreground      bool
	jobAlias        string
	afterKeys       []string
	afterSuccessKeys []string
	globalDB        *db.DB
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
	deps, err := resolveDeps(afterKeys, afterSuccessKeys)
	if err != nil {
		return err
	}
	opts := core.RunOptions{Alias: jobAlias, Verbose: verbose, Deps: deps}
	if foreground {
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

// keyArg returns the first element of args, or "." if args is empty.
func keyArg(args []string) string {
	if len(args) > 0 {
		return args[0]
	}
	return "."
}

func resolveDeps(after, afterSuccess []string) ([]model.Dep, error) {
	var deps []model.Dep
	for _, k := range after {
		j, err := core.ResolveKey(globalDB, k)
		if err != nil {
			return nil, fmt.Errorf("resolving --after %q: %w", k, err)
		}
		deps = append(deps, model.Dep{Key: j.Key, Kind: model.DepAfter})
	}
	for _, k := range afterSuccess {
		j, err := core.ResolveKey(globalDB, k)
		if err != nil {
			return nil, fmt.Errorf("resolving --after-success %q: %w", k, err)
		}
		deps = append(deps, model.Dep{Key: j.Key, Kind: model.DepAfterSuccess})
	}
	return deps, nil
}

func stateDir() string {
	if dir := os.Getenv("JOB_STATE_DIR"); dir != "" {
		return dir
	}
	return filepath.Join(xdg.DataHome, "job")
}

func init() {
	rootCmd.PersistentFlags().BoolVarP(&verbose, "verbose", "v", false, "print job key and status")
	rootCmd.Flags().BoolVarP(&foreground, "foreground", "f", false, "run in foreground")
	rootCmd.Flags().StringVarP(&jobAlias, "key", "k", "", "explicit job key/alias")
	rootCmd.Flags().StringArrayVarP(&afterKeys, "after", "a", nil, "run after job completes (any exit code)")
	rootCmd.Flags().StringArrayVarP(&afterSuccessKeys, "after-success", "A", nil, "run only if job succeeds (exit 0)")
}
