package sequence_test

import (
	"bytes"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"

	"job/internal/sequence"
)

func TestDefinitionFixturesRoundTrip(t *testing.T) {
	paths, err := filepath.Glob("testdata/*.toml")
	require.NoError(t, err)
	require.NotEmpty(t, paths)
	for _, path := range paths {
		t.Run(filepath.Base(path), func(t *testing.T) {
			want, err := sequence.Load(path)
			require.NoError(t, err)
			var output bytes.Buffer
			require.NoError(t, sequence.Encode(&output, want))
			got, err := sequence.Decode(&output)
			require.NoError(t, err)
			require.Equal(t, want, got)
		})
	}
}

func TestDecodePreservesOptionalValues(t *testing.T) {
	d, err := sequence.Load("testdata/branched.toml")
	require.NoError(t, err)
	require.Equal(t, new(true), d.Automated)
	require.Equal(t, new(false), d.Steps[0].Automated)
	require.Nil(t, d.Steps[1].Automated)
	require.Nil(t, d.Steps[1].CWD)
	require.Nil(t, d.Steps[0].Command)
	require.Nil(t, d.Steps[1].Run)

	d, err = sequence.Load("testdata/minimal.toml")
	require.NoError(t, err)
	require.Nil(t, d.CWD)
	require.Nil(t, d.Automated)
	require.Equal(t, "", d.Steps[0].Command[3], "empty arguments are valid")
}

func TestDecodeRejectsInvalidDefinitions(t *testing.T) {
	const step = "\n[[steps]]\nid = 'build'\ncommand = ['make']\n"
	tests := []struct {
		name, input, wantErr string
	}{
		{"missing version", step, "unsupported sequence version 0"},
		{"unsupported version", "version = 2" + step, "unsupported sequence version 2"},
		{"no steps", "version = 1", "sequence has no steps"},
		{"unknown root key", "version = 1\nname = 'ci'" + step, "unknown sequence keys: name"},
		{"unknown step key", "version = 1" + step + "env = { CI = 'true' }", "unknown sequence keys: steps.env"},
		{"relative sequence cwd", "version = 1\ncwd = './project'" + step, "cwd must be an absolute path"},
		{"empty sequence cwd", "version = 1\ncwd = ''" + step, "cwd must be an absolute path"},
		{"relative step cwd", "version = 1" + step + "cwd = 'project'", `step "build": cwd must be an absolute path`},
		{"empty step cwd", "version = 1" + step + "cwd = ''", `step "build": cwd must be an absolute path`},
		{"missing id", "version = 1\n[[steps]]\nrun = 'true'", "id must not be blank"},
		{"blank id", "version = 1\n[[steps]]\nid = '  '\nrun = 'true'", "id must not be blank"},
		{"duplicate id", "version = 1" + step + step, `duplicate step id "build"`},
		{"both commands", "version = 1" + step + "run = 'true'", "exactly one of run and command"},
		{"neither command", "version = 1\n[[steps]]\nid = 'build'", "exactly one of run and command"},
		{"empty run", "version = 1\n[[steps]]\nid = 'build'\nrun = ''", "run must not be blank"},
		{"blank run", "version = 1\n[[steps]]\nid = 'build'\nrun = '  '", "run must not be blank"},
		{"empty command", "version = 1\n[[steps]]\nid = 'build'\ncommand = []", "command must contain a non-empty program"},
		{"empty program", "version = 1\n[[steps]]\nid = 'build'\ncommand = ['', 'arg']", "command must contain a non-empty program"},
		{"NUL argument", "version = 1\n[[steps]]\nid = 'build'\ncommand = ['echo', \"\\u0000\"]", "command arguments must not contain NUL"},
		{"NUL run", "version = 1\n[[steps]]\nid = 'build'\nrun = \"echo \\u0000\"", "run must not be blank or contain NUL"},
		{"NUL cwd", "version = 1\ncwd = \"/tmp/\\u0000\"" + step, "cwd must be an absolute path without NUL"},
		{"unknown dependency", "version = 1" + step + "after = ['missing']", `step "build": unknown dependency "missing"`},
		{"self dependency", "version = 1" + step + "after_success = ['build']", "cannot depend on itself"},
		{"duplicate dependency", "version = 1" + step + "after = ['test', 'test']\n[[steps]]\nid = 'test'\nrun = 'true'", `dependency "test" appears more than once`},
		{"overlapping dependencies", "version = 1" + step + "after = ['test']\nafter_success = ['test']\n[[steps]]\nid = 'test'\nrun = 'true'", `dependency "test" appears more than once`},
		{"mixed dependency cycle", "version = 1" + step + "after = ['test']\n[[steps]]\nid = 'test'\nrun = 'true'\nafter_success = ['build']", "dependency cycle: build -> test -> build"},
		{"wrong type", "version = 1" + step + "automated = 'false'", "decoding sequence"},
		{"malformed TOML", "[[[", "decoding sequence"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := sequence.Decode(strings.NewReader(tt.input))
			require.ErrorContains(t, err, tt.wantErr)
		})
	}
}

func TestValidateCollectsIndependentErrors(t *testing.T) {
	d := &sequence.Definition{
		Version: 1,
		Steps: []sequence.Step{
			{ID: "build", Command: []string{}, CWD: new("relative")},
			{ID: "test", Run: new("true"), After: []string{"missing"}},
		},
	}
	err := d.Validate()
	require.ErrorContains(t, err, `step "build": command`)
	require.ErrorContains(t, err, `step "build": cwd`)
	require.ErrorContains(t, err, `step "test": unknown dependency "missing"`)
}

func TestValidateAllowsMixedDependencyConditions(t *testing.T) {
	d := &sequence.Definition{
		Version: 1,
		Steps: []sequence.Step{
			{ID: "finish", Run: new("true"), After: []string{"cleanup"}, AfterSuccess: []string{"build"}},
			{ID: "build", Command: []string{"make"}},
			{ID: "cleanup", Run: new("true")},
		},
	}
	require.NoError(t, d.Validate())
}

func TestValidateFindsCycleInDisconnectedBranch(t *testing.T) {
	d := &sequence.Definition{
		Version: 1,
		Steps: []sequence.Step{
			{ID: "independent", Run: new("true")},
			{ID: "downstream", Run: new("true"), After: []string{"a"}},
			{ID: "a", Run: new("true"), After: []string{"b"}},
			{ID: "b", Run: new("true"), AfterSuccess: []string{"a"}},
		},
	}
	err := d.Validate()
	require.EqualError(t, err, "dependency cycle: a -> b -> a")
}

func TestEncodeRejectsInvalidDefinitionBeforeWriting(t *testing.T) {
	var output bytes.Buffer
	err := sequence.Encode(&output, &sequence.Definition{Version: 1})
	require.ErrorContains(t, err, "sequence has no steps")
	require.Empty(t, output.String())
}

func TestLoadReportsPath(t *testing.T) {
	path := filepath.Join(t.TempDir(), "missing.toml")
	_, err := sequence.Load(path)
	require.ErrorContains(t, err, path)
}
