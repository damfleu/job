package integration

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

const validSequenceFile = "version = 1\n[[steps]]\nid = 'hello'\ncommand = ['echo', 'hello']\n"

func (h *harness) saveSequence(name string, keys ...string) result {
	h.t.Helper()
	args := []string{"seq", "save", "--output", filepath.Join(h.configDir, "sequences", name+".toml")}
	r := h.run(append(args, keys...)...)
	require.Equal(h.t, 0, r.exitCode, r.stderr)
	return r
}

func (h *harness) writeSequenceFile(name, content string) string {
	h.t.Helper()
	dir := filepath.Join(h.configDir, "sequences")
	require.NoError(h.t, os.MkdirAll(dir, 0o700))
	path := filepath.Join(dir, name)
	require.NoError(h.t, os.WriteFile(path, []byte(content), 0o600))
	return path
}

func TestSequenceSchemaWithoutDatabase(t *testing.T) {
	h := newHarness(t)
	// A file in place of a state directory makes any database initialization fail.
	stateFile := filepath.Join(t.TempDir(), "not-a-directory")
	require.NoError(t, os.WriteFile(stateFile, []byte("leave unchanged"), 0o600))
	r := h.runWithEnv(map[string]string{"JOB_STATE_DIR": stateFile}, "seq", "schema")
	require.Equal(t, 0, r.exitCode, r.stderr)
	require.Empty(t, r.stderr)
	var schema map[string]any
	require.NoError(t, json.Unmarshal([]byte(r.stdout), &schema))
	require.Equal(t, "https://json-schema.org/draft/2020-12/schema", schema["$schema"])
	require.Contains(t, schema, "properties")
	require.Contains(t, schema, "examples")
	contents, err := os.ReadFile(stateFile)
	require.NoError(t, err)
	require.Equal(t, "leave unchanged", string(contents))
}

func TestSequenceValidateFileWithoutExecutionOrDatabase(t *testing.T) {
	h := newHarness(t)
	dir := t.TempDir()
	marker := filepath.Join(dir, "must-not-exist")
	path := filepath.Join(dir, "draft.toml")
	content := fmt.Sprintf("version = 1\ncwd = %q\n[[steps]]\nid = 'marker'\nrun = %q\n", filepath.Join(dir, "missing-cwd"), "touch "+marker)
	require.NoError(t, os.WriteFile(path, []byte(content), 0o600))
	stateDir := filepath.Join(dir, "must-not-create-state")
	r := h.runWithEnv(map[string]string{"JOB_STATE_DIR": stateDir}, "seq", "validate", path)
	require.Equal(t, 0, r.exitCode, r.stderr)
	require.Equal(t, path+": valid\n", r.stdout)
	require.Empty(t, r.stderr)
	require.NoFileExists(t, marker)
	require.NoDirExists(t, stateDir)
}

func TestSequenceValidateArgumentIsAlwaysAPath(t *testing.T) {
	h := newHarness(t)
	h.writeSequenceFile("ci.toml", validSequenceFile)
	dir := t.TempDir()
	r := h.runFrom(dir, "seq", "validate", "ci")
	require.NotZero(t, r.exitCode)
	require.Contains(t, r.stderr, "ci")
	require.Empty(t, r.stdout)

	// Even a bare filename without an extension is accepted when it exists.
	require.NoError(t, os.WriteFile(filepath.Join(dir, "ci"), []byte(validSequenceFile), 0o600))
	r = h.runFrom(dir, "seq", "validate", "ci")
	require.Equal(t, 0, r.exitCode, r.stderr)
	require.Equal(t, "ci: valid\n", r.stdout)

	require.NoError(t, os.WriteFile(filepath.Join(dir, "ci.toml"), []byte(validSequenceFile), 0o600))
	r = h.runFrom(dir, "seq", "validate", "./ci.toml")
	require.Equal(t, 0, r.exitCode, r.stderr)
	require.Contains(t, r.stdout, "./ci.toml: valid")
}

func TestSequenceValidateAllReportsEveryInvalidFile(t *testing.T) {
	h := newHarness(t)
	first := h.writeSequenceFile("a-invalid.toml", "version = 1\n[[steps]]\nid = 'first'\nrun = 'true'\nafter = ['missing']\n")
	good := h.writeSequenceFile("b-valid.toml", validSequenceFile)
	last := h.writeSequenceFile("c-invalid.toml", "version = 1\n[[steps]]\nid = 'last'\ncommand = []\n")
	r := h.run("seq", "validate")
	require.NotZero(t, r.exitCode)
	require.Equal(t, good+": valid\n", r.stdout)
	require.Contains(t, r.stderr, first)
	require.Contains(t, r.stderr, `step "first": unknown dependency "missing"`)
	require.Contains(t, r.stderr, last)
	require.Contains(t, r.stderr, `step "last": command must contain a non-empty program`)
	require.Less(t, strings.Index(r.stderr, first), strings.Index(r.stderr, last))
}

func TestSequenceValidateDiscovery(t *testing.T) {
	h := newHarness(t)
	z := h.writeSequenceFile("z.toml", validSequenceFile)
	a := h.writeSequenceFile("a.toml", validSequenceFile)
	h.writeSequenceFile("ignored.txt", "not valid TOML")
	subdir := filepath.Join(h.configDir, "sequences", "nested.toml")
	require.NoError(t, os.Mkdir(subdir, 0o700))
	require.NoError(t, os.WriteFile(filepath.Join(subdir, "bad.toml"), []byte("not TOML"), 0o600))
	r := h.run("sequence", "validate")
	require.Equal(t, 0, r.exitCode, r.stderr)
	require.Equal(t, a+": valid\n"+z+": valid\n", r.stdout)
	require.Empty(t, r.stderr)
}

func TestSequenceValidateEmptyDirectory(t *testing.T) {
	for _, createDir := range []bool{false, true} {
		t.Run(fmt.Sprint(createDir), func(t *testing.T) {
			h := newHarness(t)
			if createDir {
				require.NoError(t, os.Mkdir(filepath.Join(h.configDir, "sequences"), 0o700))
			}
			r := h.run("seq", "validate")
			require.Equal(t, 0, r.exitCode, r.stderr)
			require.Empty(t, r.stdout)
			require.Equal(t, "no sequences\n", r.stderr)
		})
	}
}

func TestSequenceFileCommandsRejectExtraArguments(t *testing.T) {
	h := newHarness(t)
	for _, args := range [][]string{{"seq", "schema", "extra"}, {"seq", "validate", "one.toml", "two.toml"}} {
		r := h.run(args...)
		require.NotZero(t, r.exitCode)
		require.NotEmpty(t, r.stderr)
	}
}
