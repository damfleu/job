package cli

import (
	"fmt"
	"os"
	"path/filepath"

	"github.com/adrg/xdg"
	"github.com/spf13/cobra"

	"job/internal/config"
	"job/internal/core"
	"job/internal/db"
	"job/internal/model"
)

var version = "dev"

var globalDB *db.DB
var globalConfig config.Config

var rootCmd = &cobra.Command{
	Use:           "job",
	Short:         "Run and track jobs",
	Version:       version,
	SilenceUsage:  true,
	SilenceErrors: true,
	RunE: func(cmd *cobra.Command, args []string) error {
		return cmd.Help()
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
		cfg, err := config.Load(filepath.Join(configDir(), "config.toml"))
		if err != nil {
			return fmt.Errorf("loading config: %w", err)
		}
		globalConfig = cfg
		return nil
	},
	PersistentPostRun: func(cmd *cobra.Command, args []string) {
		if globalDB != nil {
			globalDB.Close()
		}
	},
	CompletionOptions: cobra.CompletionOptions{HiddenDefaultCmd: true},
}

func Execute() {
	if err := rootCmd.Execute(); err != nil {
		fmt.Fprintln(os.Stderr, "error:", err)
		os.Exit(1)
	}
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

func configDir() string {
	if dir := os.Getenv("JOB_CONFIG_DIR"); dir != "" {
		return dir
	}
	return filepath.Join(xdg.ConfigHome, "job")
}

func stateDir() string {
	if dir := os.Getenv("JOB_STATE_DIR"); dir != "" {
		return dir
	}
	return filepath.Join(xdg.DataHome, "job")
}
