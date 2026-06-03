package cli

import (
	"fmt"
	"io"
	"os"
	"path/filepath"
	"time"

	"github.com/spf13/cobra"

	"job/internal/core"
	"job/internal/model"
)

// RunFlags holds the flags shared between the root run command and subcommands like retry that also
// launch jobs (foreground, watch, alias, deps).
type RunFlags struct {
	Foreground bool
	Watch      bool
	Notify     bool
	Verbose    bool
	Alias      string
	Deps       []model.Dep // accumulates -a/-A in declaration order
	Automated  bool
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

// buildRunOptions resolves deps and notifiers from f and returns a fully populated RunOptions.
// workDir may be empty, in which case the cwd is used for context resolution (but WorkDir in
// the returned options stays empty so CreateAndSpawn/CreateAndRunForeground can apply their own
// default).
func buildRunOptions(command []string, workDir string, f RunFlags) (core.RunOptions, error) {
	deps, err := resolveDeps(f.Deps)
	if err != nil {
		return core.RunOptions{}, err
	}
	// Build the list of notifier programs to call when the job completes. "always" notifiers fire
	// unconditionally; "explicit" (or unset) notifiers fire only when the user passed -n/--notify.
	var notifiers []string
	for _, n := range globalConfig.Notifiers {
		if n.Notify == "always" || ((n.Notify == "" || n.Notify == "explicit") && f.Notify) {
			notifiers = append(notifiers, n.Program)
		}
	}
	resolvedWorkDir := workDir
	if resolvedWorkDir == "" {
		resolvedWorkDir, _ = os.Getwd()
	}
	return core.RunOptions{
		Alias:     f.Alias,
		Verbose:   f.Verbose,
		Deps:      deps,
		WorkDir:   workDir,
		Notifiers: notifiers,
		Context:   core.ResolveContext(resolvedWorkDir, globalConfig.Context.Resolvers),
		Automated: f.Automated,
	}, nil
}

// launchJob creates and starts a job using command and workDir (empty = cwd), honouring the
// foreground/background choice in f. It is shared by the root run path and retry.
func launchJob(command []string, workDir string, f RunFlags) error {
	opts, err := buildRunOptions(command, workDir, f)
	if err != nil {
		return err
	}
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
		if len(opts.Deps) > 0 {
			fmt.Fprintf(os.Stderr, "%s (%s)\n", key, model.FormatDeps(opts.Deps))
		} else {
			fmt.Fprintln(os.Stderr, key)
		}
	}
	return nil
}

// addCwdFlag registers --cwd onto cmd, writing into v.
func addCwdFlag(cmd *cobra.Command, v *string, usage string) {
	cmd.Flags().StringVar(v, "cwd", "", usage)
	cmd.Flags().Lookup("cwd").NoOptDefVal = "."
}

// resolveCwdFlag resolves a --cwd flag value to an absolute path. Returns ""
// when the flag was not set.
func resolveCwdFlag(val string) (string, error) {
	if val == "" {
		return "", nil
	}
	return filepath.Abs(val)
}

// addRunFlags registers the shared job-launch flags onto cmd, writing into f.
func addRunFlags(cmd *cobra.Command, f *RunFlags) {
	cmd.Flags().BoolVarP(&f.Foreground, "foreground", "f", false, "run in foreground")
	cmd.Flags().BoolVarP(&f.Watch, "watch", "w", false, "run in background and tail log")
	cmd.Flags().BoolVarP(&f.Notify, "notify", "n", false, "notify on completion")
	cmd.Flags().BoolVarP(&f.Verbose, "verbose", "v", false, "print job key")
	cmd.Flags().StringVarP(&f.Alias, "key", "k", "", "explicit job key/alias")
	cmd.Flags().VarP(depFlag{model.DepAfter, &f.Deps}, "after", "a", "run after job completes (any exit code)")
	cmd.Flags().VarP(depFlag{model.DepAfterSuccess, &f.Deps}, "after-success", "A", "run only if job succeeds (exit 0)")
	cmd.Flags().BoolVar(&f.Automated, "automated", false, "mark job as automated (not human-initiated); skips '.' tracking")
	cmd.MarkFlagsMutuallyExclusive("foreground", "watch")
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
