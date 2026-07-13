package integration

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestJobShow(t *testing.T) {
	h := newHarness(t)
	h.run("run", "-f", "echo", "show test")

	r := h.run("show")
	assert.Equal(t, 0, r.exitCode)
	assert.Contains(t, r.stdout, "echo show test") // command
	assert.Contains(t, r.stdout, "completed")      // status
	assert.Contains(t, r.stdout, "exited")         // reason
	assert.Contains(t, r.stdout, "Exit code:")
	assert.Contains(t, r.stdout, "WorkDir:")
	assert.Contains(t, r.stdout, "Duration:")
}

func TestJobLog(t *testing.T) {
	h := newHarness(t)
	h.run("run", "-f", "echo", "log test output")

	r := h.run("log")
	assert.Equal(t, 0, r.exitCode)
	assert.Contains(t, r.stdout, "log test output")
}

func TestJobLogPath(t *testing.T) {
	h := newHarness(t)
	h.run("run", "-f", "echo", "hi")

	r := h.run("log", "-p")
	assert.Equal(t, 0, r.exitCode)
	path := strings.TrimSpace(r.stdout)
	assert.True(t, filepath.IsAbs(path))
	_, err := os.Stat(path)
	assert.NoError(t, err)
}

func TestJobList(t *testing.T) {
	h := newHarness(t)
	h.run("run", "-f", "echo", "first")
	h.run("run", "-f", "echo", "second")

	r := h.run("list")
	assert.Equal(t, 0, r.exitCode)
	assert.Contains(t, r.stdout, "echo first")
	assert.Contains(t, r.stdout, "echo second")
	assert.Contains(t, r.stdout, "COMMAND") // table header
}

func TestJobListFilter(t *testing.T) {
	h := newHarness(t)
	h.run("run", "-f", "echo", "keep this")
	h.run("run", "-f", "false")

	r := h.run("list", "-f", "echo")
	assert.Equal(t, 0, r.exitCode)
	assert.Contains(t, r.stdout, "echo keep this")
	assert.NotContains(t, r.stdout, "false")
}

func TestJobListLimit(t *testing.T) {
	h := newHarness(t)
	for range 5 {
		h.run("run", "-f", "echo", "job")
	}

	r := h.run("list", "-n", "3")
	assert.Equal(t, 0, r.exitCode)
	assert.Equal(t, 3, strings.Count(r.stdout, "echo job"))

	r = h.run("list", "-n", "0")
	assert.Equal(t, 0, r.exitCode)
	assert.Equal(t, 5, strings.Count(r.stdout, "echo job"))

	r = h.run("list", "-n", "-1")
	assert.NotEqual(t, 0, r.exitCode)
	assert.Contains(t, r.stderr, "limit cannot be negative")
}

func TestJobListInvalidFilter(t *testing.T) {
	h := newHarness(t)
	r := h.run("list", "-f", "a(b")
	assert.NotEqual(t, 0, r.exitCode)
}

func TestListDefaultScopedToContext(t *testing.T) {
	h := newHarness(t)
	dirA := t.TempDir()
	dirB := t.TempDir()

	// resolver that outputs the working directory — disambiguates the two dirs
	script := h.writeScript("pwd")
	h.writeConfig(fmt.Sprintf("[context]\nresolvers = [%q]\n", script))

	h.runFrom(dirA, "run", "-f", "echo", "from-a")
	h.runFrom(dirB, "run", "-f", "echo", "from-b")

	r := h.runFrom(dirA, "list")
	assert.Equal(t, 0, r.exitCode)
	assert.Contains(t, r.stdout, "echo from-a")
	assert.NotContains(t, r.stdout, "echo from-b")
	assert.NotContains(t, r.stdout, "CONTEXT")
}

func TestListAnyReachesAcrossContexts(t *testing.T) {
	h := newHarness(t)
	dirA := t.TempDir()
	dirB := t.TempDir()

	// resolver that outputs the working directory — disambiguates the two dirs
	script := h.writeScript("pwd")
	h.writeConfig(fmt.Sprintf("[context]\nresolvers = [%q]\n", script))

	h.runFrom(dirA, "run", "-f", "echo", "from-a")
	h.runFrom(dirB, "run", "-f", "echo", "from-b")

	r := h.runFrom(dirA, "list", "--any")
	assert.Equal(t, 0, r.exitCode)
	assert.Contains(t, r.stdout, "echo from-a")
	assert.Contains(t, r.stdout, "echo from-b")
	assert.Contains(t, r.stdout, "CONTEXT")
	assert.Contains(t, r.stdout, dirA)
	assert.Contains(t, r.stdout, dirB)
}

func TestListContextRegexReachesAcrossMatchingContexts(t *testing.T) {
	h := newHarness(t)
	base := t.TempDir()
	projectA := filepath.Join(base, "project-one")
	projectB := filepath.Join(base, "project-two")
	terminal := filepath.Join(base, "terminal")
	for _, dir := range []string{projectA, projectB, terminal} {
		require.NoError(t, os.Mkdir(dir, 0o755))
	}

	script := h.writeScript("basename \"$PWD\"")
	h.writeConfig(fmt.Sprintf("[context]\nresolvers = [%q]\n", script))
	h.runFrom(projectA, "run", "-f", "echo", "from-project-a")
	h.runFrom(projectB, "run", "-f", "echo", "from-project-b")
	h.runFrom(terminal, "run", "-f", "echo", "from-terminal")

	r := h.runFrom(terminal, "list", "--context", `^project-`)
	assert.Equal(t, 0, r.exitCode)
	assert.Contains(t, r.stdout, "echo from-project-a")
	assert.Contains(t, r.stdout, "echo from-project-b")
	assert.NotContains(t, r.stdout, "echo from-terminal")
}

func TestListContextRegexCanCombineWithCommandFilter(t *testing.T) {
	h := newHarness(t)
	script := h.writeScript("echo project-session")
	h.writeConfig(fmt.Sprintf("[context]\nresolvers = [%q]\n", script))
	h.run("run", "-f", "echo", "keep")
	h.run("run", "-f", "printf", "drop")

	r := h.run("list", "--context", `^project-`, "--filter", "keep")
	assert.Equal(t, 0, r.exitCode)
	assert.Contains(t, r.stdout, "echo keep")
	assert.NotContains(t, r.stdout, "printf drop")
}

func TestListRejectsInvalidContextRegex(t *testing.T) {
	h := newHarness(t)
	r := h.run("list", "--context", "a(b")
	assert.NotEqual(t, 0, r.exitCode)
	assert.Contains(t, r.stderr, "invalid context filter")
}

func TestListRejectsContextWithAny(t *testing.T) {
	h := newHarness(t)
	r := h.run("list", "--context", "project", "--any")
	assert.NotEqual(t, 0, r.exitCode)
}

func TestJobShowContext(t *testing.T) {
	h := newHarness(t)
	script := h.writeScript("echo mycontext")
	h.writeConfig(fmt.Sprintf("[context]\nresolvers = [%q]\n", script))

	h.run("run", "-f", "echo", "ctx test")

	r := h.run("show")
	assert.Equal(t, 0, r.exitCode)
	assert.Contains(t, r.stdout, "mycontext")
}

func TestJobListRegex(t *testing.T) {
	h := newHarness(t)
	// three distinct commands
	h.run("run", "-f", "echo", "alpha")
	h.run("run", "-f", "echo", "beta")
	h.run("run", "-f", "sh", "-c", "true")

	// anchored: only commands starting with "echo"
	r := h.run("list", "-f", "^echo")
	assert.Equal(t, 0, r.exitCode)
	assert.Contains(t, r.stdout, "echo alpha")
	assert.Contains(t, r.stdout, "echo beta")
	assert.NotContains(t, r.stdout, "sh -c true")

	// flag in command: matches "sh -c" anywhere
	r2 := h.run("list", "-f", `sh -c`)
	assert.Equal(t, 0, r2.exitCode)
	assert.Contains(t, r2.stdout, "sh -c true")
	assert.NotContains(t, r2.stdout, "echo")

	// alternation: either "alpha" or "beta"
	r3 := h.run("list", "-f", "alpha|beta")
	assert.Equal(t, 0, r3.exitCode)
	assert.Contains(t, r3.stdout, "echo alpha")
	assert.Contains(t, r3.stdout, "echo beta")
	assert.NotContains(t, r3.stdout, "sh -c true")
}
