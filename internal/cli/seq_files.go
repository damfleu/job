package cli

import (
	"encoding/json"
	"errors"
	"fmt"

	"github.com/spf13/cobra"

	"job/internal/sequence"
)

var seqSchemaCmd = &cobra.Command{
	Use:               "schema",
	Short:             "Print JSON Schema for sequence TOML files",
	Args:              cobra.NoArgs,
	PersistentPreRunE: loadConfig,
	RunE: func(cmd *cobra.Command, args []string) error {
		encoder := json.NewEncoder(cmd.OutOrStdout())
		encoder.SetIndent("", "  ")
		return encoder.Encode(sequence.Schema())
	},
}

var seqValidateCmd = &cobra.Command{
	Use:   "validate [path]",
	Short: "Validate a sequence file or all installed sequence files",
	Long: "Validate a sequence TOML file without executing it. The argument is a file path,\n" +
		"not a sequence name. With no argument, validate all TOML files in the config\n" +
		"directory's sequences subdirectory. Working directories need not exist.",
	Args:              cobra.MaximumNArgs(1),
	PersistentPreRunE: loadConfig,
	RunE: func(cmd *cobra.Command, args []string) error {
		paths := args
		if len(paths) == 0 {
			var err error
			paths, err = sequence.Files(sequenceDir())
			if err != nil {
				return err
			}
		}
		if len(paths) == 0 {
			fmt.Fprintln(cmd.ErrOrStderr(), "no sequences")
			return nil
		}
		var errs []error
		for _, path := range paths {
			if _, err := sequence.Load(path); err != nil {
				errs = append(errs, err)
			} else {
				fmt.Fprintf(cmd.OutOrStdout(), "%s: valid\n", path)
			}
		}
		return errors.Join(errs...)
	},
}

func init() {
	seqCmd.AddCommand(seqSchemaCmd, seqValidateCmd)
}
