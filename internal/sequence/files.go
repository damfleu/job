package sequence

import (
	"errors"
	"os"
	"path/filepath"
)

// Files lists TOML files in dir, sorted by filename, without searching
// subdirectories. A missing directory is an empty collection.
func Files(dir string) ([]string, error) {
	entries, err := os.ReadDir(dir)
	if errors.Is(err, os.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	var paths []string
	for _, entry := range entries {
		if !entry.IsDir() && filepath.Ext(entry.Name()) == ".toml" {
			paths = append(paths, filepath.Join(dir, entry.Name()))
		}
	}
	return paths, nil
}
