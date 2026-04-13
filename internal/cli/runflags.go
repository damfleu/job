package cli

import (
	"job/internal/model"

	"github.com/spf13/cobra"
)

// RunFlags holds the flags shared between the root run command and subcommands
// like retry that also launch jobs (foreground, watch, alias, deps).
type RunFlags struct {
	Foreground bool
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

// addRunFlags registers the shared job-launch flags onto cmd, writing into f.
func addRunFlags(cmd *cobra.Command, f *RunFlags) {
	cmd.Flags().BoolVarP(&f.Foreground, "foreground", "f", false, "run in foreground")
	cmd.Flags().StringVarP(&f.Alias, "key", "k", "", "explicit job key/alias")
	cmd.Flags().VarP(depFlag{model.DepAfter, &f.Deps}, "after", "a", "run after job completes (any exit code)")
	cmd.Flags().VarP(depFlag{model.DepAfterSuccess, &f.Deps}, "after-success", "A", "run only if job succeeds (exit 0)")
}
