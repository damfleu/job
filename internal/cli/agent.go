package cli

import (
	"os"
	"strings"

	"github.com/spf13/cobra"
)

var agentEnvVars = []string{
	"JOB_AGENT",
	"CLAUDECODE",
	"CLAUDE_CODE",
}

var agentSessionEnvVars = []string{
	"CLAUDE_CODE_SESSION_ID",
	"CODEX_THREAD_ID",
}

// runningUnderAgent reports whether job was invoked by a supported coding
// agent or an explicitly identified agent. Claude Code and Codex expose these
// variables to command subprocesses, while JOB_AGENT provides a vendor-neutral
// override.
func runningUnderAgent() bool {
	for _, name := range agentEnvVars {
		if envTruthy(name) {
			return true
		}
	}
	for _, name := range agentSessionEnvVars {
		if envPresent(name) {
			return true
		}
	}
	return false
}

func envTruthy(name string) bool {
	value, ok := os.LookupEnv(name)
	return ok && (value == "1" || strings.EqualFold(value, "true"))
}

func envPresent(name string) bool {
	value, ok := os.LookupEnv(name)
	return ok && value != ""
}

// wantsJSON returns the explicit --json value when the flag was provided;
// otherwise, detected agents default to structured output.
func wantsJSON(cmd *cobra.Command, explicit bool) bool {
	if cmd.Flags().Changed("json") {
		return explicit
	}
	return runningUnderAgent()
}
