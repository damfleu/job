package cli

import (
	"fmt"
	"io"
	"os"
	"time"

	"job/internal/core"
	"job/internal/model"

	"github.com/spf13/cobra"
)

// RunFlags holds the flags shared between the root run command and subcommands
// like retry that also launch jobs (foreground, watch, alias, deps).
type RunFlags struct {
	Foreground bool
	Watch      bool
	Verbose    bool
	Alias      string
	Deps       []model.Dep // accumulates -a/-A in declaration order
}

// depFlag is a pflag.Value that appends a dep of a fixed kind to a Deps slice.
type depFlag struct {
	kind model.DepKind
	deps *[]model.Dep
}

func (d depFlag) String() string { return "" }
func (d depFlag) Type() string   { return "string" }
func (d depFlag) Set(val string) error {
	*d.deps = append(*d.deps, model.Dep{Key: val, Kind: d.kind})
	return nil
}

// launchJob creates and starts a job using command and workDir (empty = cwd),
// honouring the foreground/background choice in f. It is shared by the root
// run path and retry.
func launchJob(command []string, workDir string, f RunFlags) error {
	deps, err := resolveDeps(f.Deps)
	if err != nil {
		return err
	}
	opts := core.RunOptions{Alias: f.Alias, Verbose: f.Verbose, Deps: deps, WorkDir: workDir}
	if f.Foreground {
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
	if f.Watch {
		j, err := globalDB.Get(key)
		if err != nil {
			return err
		}
		exitCode, err := watchJob(key, j.LogFile)
		if err != nil {
			return err
		}
		if exitCode != 0 {
			os.Exit(exitCode)
		}
		return nil
	}
	if f.Verbose {
		fmt.Fprintln(os.Stderr, key)
	}
	return nil
}

// addRunFlags registers the shared job-launch flags onto cmd, writing into f.
func addRunFlags(cmd *cobra.Command, f *RunFlags) {
	cmd.Flags().BoolVarP(&f.Foreground, "foreground", "f", false, "run in foreground")
	cmd.Flags().BoolVarP(&f.Watch, "watch", "w", false, "run in background and tail log")
	cmd.Flags().BoolVarP(&f.Verbose, "verbose", "v", false, "print job key")
	cmd.Flags().StringVarP(&f.Alias, "key", "k", "", "explicit job key/alias")
	cmd.Flags().VarP(depFlag{model.DepAfter, &f.Deps}, "after", "a", "run after job completes (any exit code)")
	cmd.Flags().VarP(depFlag{model.DepAfterSuccess, &f.Deps}, "after-success", "A", "run only if job succeeds (exit 0)")
}

// watchJob tails logFile to stdout, polling the DB until the job completes.
// Returns the job's exit code.
func watchJob(key, logFile string) (int, error) {
	f, err := os.Open(logFile)
	if err != nil {
		return 0, fmt.Errorf("opening log: %w", err)
	}
	defer f.Close()

	for {
		if _, err := io.Copy(os.Stdout, f); err != nil {
			return 0, err
		}
		j, err := globalDB.Get(key)
		if err != nil {
			return 0, err
		}
		if j.Status == model.StatusCompleted {
			_, _ = io.Copy(os.Stdout, f)
			if j.ExitCode != nil {
				return *j.ExitCode, nil
			}
			return 0, nil
		}
		time.Sleep(100 * time.Millisecond)
	}
}
