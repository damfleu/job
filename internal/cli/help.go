package cli

import "github.com/spf13/cobra"

const keyAliasesHelp = `
Key aliases:
  .  most recently started job (any status)
  +  most recent running job
  _  most recent blocked job
  =  most recent completed job

  Scoped to the current context; use --any to search all contexts.
  Automated jobs are excluded. +, _, and = error if no job matches.
`

func init() {
	for _, cmd := range []*cobra.Command{
		logCmd, showCmd, stopCmd, waitCmd, retryCmd, rmCmd, seqSaveCmd,
	} {
		cmd.SetHelpTemplate(cmd.HelpTemplate() + keyAliasesHelp)
	}
}
