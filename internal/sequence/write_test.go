package sequence_test

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"

	"job/internal/sequence"
)

func TestWriteFileDoesNotClobberWithoutForce(t *testing.T) {
	d, err := sequence.Load("testdata/minimal.toml")
	require.NoError(t, err)
	path := filepath.Join(t.TempDir(), "sequences", "ci.toml")
	require.NoError(t, sequence.WriteFile(path, d, false))
	original, err := os.ReadFile(path)
	require.NoError(t, err)
	d.Description = "replacement"
	require.ErrorContains(t, sequence.WriteFile(path, d, false), "--force")
	unchanged, err := os.ReadFile(path)
	require.NoError(t, err)
	require.Equal(t, original, unchanged)
	require.NoError(t, sequence.WriteFile(path, d, true))
	reloaded, err := sequence.Load(path)
	require.NoError(t, err)
	require.Equal(t, "replacement", reloaded.Description)
	files, err := os.ReadDir(filepath.Dir(path))
	require.NoError(t, err)
	require.Len(t, files, 1, "temporary files are removed on success and failure")

	d.Steps = nil
	require.Error(t, sequence.WriteFile(path, d, true))
	reloaded, err = sequence.Load(path)
	require.NoError(t, err)
	require.Equal(t, "replacement", reloaded.Description, "invalid input must not damage an existing file")
}

func TestConcurrentWritesDoNotOverwrite(t *testing.T) {
	d, err := sequence.Load("testdata/minimal.toml")
	require.NoError(t, err)
	path := filepath.Join(t.TempDir(), "ci.toml")
	results := make(chan error, 2)
	for range 2 {
		go func() { results <- sequence.WriteFile(path, d, false) }()
	}
	a, b := <-results, <-results
	require.NotEqual(t, a == nil, b == nil, "exactly one writer must succeed")
	_, err = sequence.Load(path)
	require.NoError(t, err)
}

func TestLoadNamedRejectsPaths(t *testing.T) {
	for _, name := range []string{"", ".", "..", "../ci", "./ci.toml", "/tmp/ci.toml", `nested\ci`} {
		_, err := sequence.LoadNamed(t.TempDir(), name)
		require.ErrorContains(t, err, "expected a name, not a file path")
	}
}

func TestListIncludesValidEntriesAlongsideErrors(t *testing.T) {
	dir := t.TempDir()
	d, err := sequence.Load("testdata/minimal.toml")
	require.NoError(t, err)
	require.NoError(t, sequence.WriteFile(filepath.Join(dir, "ci.toml"), d, false))
	require.NoError(t, os.WriteFile(filepath.Join(dir, "bad.toml"), []byte("version = 99"), 0o600))
	entries, err := sequence.List(dir)
	require.ErrorContains(t, err, "bad.toml")
	require.Len(t, entries, 1)
	require.Equal(t, "ci", entries[0].Name)
	entry, err := sequence.LoadNamed(dir, "ci")
	require.NoError(t, err)
	require.Equal(t, entries[0], *entry)
}
