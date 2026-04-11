package logfile

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestPath(t *testing.T) {
	p := Path("/state", "1712912345_a3f1c8d2_make")

	// rooted under stateDir/log/
	assert.True(t, strings.HasPrefix(p, "/state/log/"))
	// 2-char hash subdir
	dir := filepath.Dir(p)
	assert.Len(t, filepath.Base(dir), 2)
	// filename is key + .log
	assert.Equal(t, "1712912345_a3f1c8d2_make.log", filepath.Base(p))
}

func TestPathHashSubdir(t *testing.T) {
	// same key always maps to same subdir
	p1 := Path("/state", "mykey")
	p2 := Path("/state", "mykey")
	assert.Equal(t, p1, p2)

	// different keys can map to different subdirs (not guaranteed, but with these two they differ)
	pa := Path("/state", "aaa")
	pb := Path("/state", "zzz")
	assert.NotEqual(t, filepath.Dir(pa), filepath.Dir(pb))
}

func TestCreate(t *testing.T) {
	dir := t.TempDir()
	key := "1712912345_a3f1c8d2_make"

	f, err := Create(dir, key)
	require.NoError(t, err)
	defer f.Close()

	// file exists at the expected path
	assert.Equal(t, Path(dir, key), f.Name())
	_, err = os.Stat(f.Name())
	require.NoError(t, err)

	// parent directory was created
	_, err = os.Stat(filepath.Dir(f.Name()))
	require.NoError(t, err)
}

func TestCreateIdempotent(t *testing.T) {
	dir := t.TempDir()
	key := "mykey"

	f1, err := Create(dir, key)
	require.NoError(t, err)
	f1.Close()

	f2, err := Create(dir, key)
	require.NoError(t, err)
	f2.Close()
}
