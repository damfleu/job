package sequence_test

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/BurntSushi/toml"
	"github.com/santhosh-tekuri/jsonschema/v6"
	"github.com/stretchr/testify/require"

	"job/internal/sequence"
)

func schemaValue(t *testing.T, value any) any {
	t.Helper()
	data, err := json.Marshal(value)
	require.NoError(t, err)
	decoded, err := jsonschema.UnmarshalJSON(bytes.NewReader(data))
	require.NoError(t, err)
	return decoded
}

func compileSchema(t *testing.T) *jsonschema.Schema {
	t.Helper()
	c := jsonschema.NewCompiler()
	require.NoError(t, c.AddResource("sequence.json", schemaValue(t, sequence.Schema())))
	s, err := c.Compile("sequence.json")
	require.NoError(t, err)
	return s
}

func TestSchemaAcceptsFixturesAndExamples(t *testing.T) {
	s := compileSchema(t)
	paths, err := filepath.Glob("testdata/*.toml")
	require.NoError(t, err)
	for _, path := range paths {
		t.Run(filepath.Base(path), func(t *testing.T) {
			var document map[string]any
			_, err := toml.DecodeFile(path, &document)
			require.NoError(t, err)
			require.NoError(t, s.Validate(schemaValue(t, document)))
		})
	}
	for _, example := range sequence.Schema().Examples {
		require.NoError(t, s.Validate(schemaValue(t, example)))
	}
}

func TestSchemaAndDecoderAgreeOnFieldConstraints(t *testing.T) {
	s := compileSchema(t)
	const step = "\n[[steps]]\nid = 'test'\n"
	tests := []struct {
		name, input string
		valid       bool
	}{
		{"literal command", "version = 1" + step + "command = ['printf', '%s', '', '$HOME']", true},
		{"shell script", "version = 1" + step + "run = 'echo hi | cat'", true},
		{"explicit false", "version = 1\nautomated = false" + step + "run = 'true'\nautomated = false", true},
		{"absolute cwd", "version = 1\ncwd = '/not-required-to-exist'" + step + "run = 'true'\ncwd = '/another-missing-directory'", true},
		{"missing version", step + "run = 'true'", false},
		{"future version", "version = 2" + step + "run = 'true'", false},
		{"missing steps", "version = 1", false},
		{"empty steps", "version = 1\nsteps = []", false},
		{"missing command", "version = 1" + step, false},
		{"both commands", "version = 1" + step + "run = 'true'\ncommand = ['true']", false},
		{"empty array still conflicts with run", "version = 1" + step + "run = 'true'\ncommand = []", false},
		{"empty command", "version = 1" + step + "command = []", false},
		{"empty program", "version = 1" + step + "command = ['', 'argument']", false},
		{"NUL program", "version = 1" + step + "command = [\"\\u0000\"]", false},
		{"NUL argument", "version = 1" + step + "command = ['echo', \"\\u0000\"]", false},
		{"blank run", "version = 1" + step + "run = '   '", false},
		{"NUL run", "version = 1" + step + "run = \"echo \\u0000\"", false},
		{"blank id", "version = 1\n[[steps]]\nid = ' '\nrun = 'true'", false},
		{"missing id", "version = 1\n[[steps]]\nrun = 'true'", false},
		{"relative root cwd", "version = 1\ncwd = 'relative'" + step + "run = 'true'", false},
		{"empty root cwd", "version = 1\ncwd = ''" + step + "run = 'true'", false},
		{"relative step cwd", "version = 1" + step + "run = 'true'\ncwd = 'relative'", false},
		{"empty step cwd", "version = 1" + step + "run = 'true'\ncwd = ''", false},
		{"NUL cwd", "version = 1\ncwd = \"/tmp/\\u0000\"" + step + "run = 'true'", false},
		{"wrong automation type", "version = 1" + step + "run = 'true'\nautomated = 'false'", false},
		{"unknown root field", "version = 1\nname = 'ci'" + step + "run = 'true'", false},
		{"unknown step field", "version = 1" + step + "run = 'true'\nenv = { CI = 'true' }", false},
		{"duplicate dependency", "version = 1" + step + "run = 'true'\nafter = ['build', 'build']\n[[steps]]\nid = 'build'\nrun = 'true'", false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var document map[string]any
			_, err := toml.Decode(tt.input, &document)
			require.NoError(t, err, "test input must be syntactically valid TOML")
			schemaErr := s.Validate(schemaValue(t, document))
			_, decodeErr := sequence.Decode(strings.NewReader(tt.input))
			if tt.valid {
				require.NoError(t, schemaErr)
				require.NoError(t, decodeErr)
			} else {
				require.Error(t, schemaErr)
				require.Error(t, decodeErr)
			}
		})
	}
}

func TestDecoderChecksGraphBeyondSchema(t *testing.T) {
	s := compileSchema(t)
	document := map[string]any{
		"version": 1,
		"steps": []map[string]any{
			{"id": "test", "command": []string{"true"}, "after_success": []string{"missing"}},
		},
	}
	require.NoError(t, s.Validate(schemaValue(t, document)))
	var encoded bytes.Buffer
	require.NoError(t, toml.NewEncoder(&encoded).Encode(document))
	_, err := sequence.Decode(&encoded)
	require.ErrorContains(t, err, `unknown dependency "missing"`)
}

func TestFiles(t *testing.T) {
	dir := t.TempDir()
	for _, name := range []string{"z.toml", "a.toml", "notes.txt", "backup.toml.bak"} {
		require.NoError(t, os.WriteFile(filepath.Join(dir, name), nil, 0o600))
	}
	require.NoError(t, os.Mkdir(filepath.Join(dir, "directory.toml"), 0o700))
	require.NoError(t, os.WriteFile(filepath.Join(dir, "directory.toml", "nested.toml"), nil, 0o600))
	paths, err := sequence.Files(dir)
	require.NoError(t, err)
	require.Equal(t, []string{filepath.Join(dir, "a.toml"), filepath.Join(dir, "z.toml")}, paths)

	paths, err = sequence.Files(filepath.Join(dir, "missing"))
	require.NoError(t, err)
	require.Empty(t, paths)
	_, err = sequence.Files(filepath.Join(dir, "a.toml"))
	require.Error(t, err)
}
