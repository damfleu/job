package cli

import "testing"

func clearAgentDetectionEnv(t *testing.T) {
	t.Helper()
	for _, name := range agentEnvVars {
		t.Setenv(name, "")
	}
	for _, name := range agentSessionEnvVars {
		t.Setenv(name, "")
	}
}

func TestRunningUnderAgentSignals(t *testing.T) {
	tests := []struct {
		name  string
		env   string
		value string
	}{
		{name: "job override one", env: "JOB_AGENT", value: "1"},
		{name: "job override true", env: "JOB_AGENT", value: "TRUE"},
		{name: "claude code", env: "CLAUDECODE", value: "1"},
		{name: "claude code compatibility", env: "CLAUDE_CODE", value: "true"},
		{name: "claude session", env: "CLAUDE_CODE_SESSION_ID", value: "session-id"},
		{name: "codex thread", env: "CODEX_THREAD_ID", value: "thread-id"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			clearAgentDetectionEnv(t)
			t.Setenv(tt.env, tt.value)
			if !runningUnderAgent() {
				t.Fatalf("expected %s=%q to enable automatic agent output", tt.env, tt.value)
			}
		})
	}
}

func TestRunningUnderAgentIgnoresFalsyValues(t *testing.T) {
	for _, value := range []string{"", "0", "false", "yes", " true "} {
		t.Run(value, func(t *testing.T) {
			clearAgentDetectionEnv(t)
			t.Setenv("CLAUDECODE", value)
			if runningUnderAgent() {
				t.Fatalf("expected CLAUDECODE=%q not to enable automatic agent output", value)
			}
		})
	}
}

func TestRunningUnderAgentNoSignal(t *testing.T) {
	clearAgentDetectionEnv(t)
	if runningUnderAgent() {
		t.Fatal("expected no agent to be detected")
	}
}
