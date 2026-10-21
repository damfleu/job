package sequence

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// Entry adds the installed identity and source path to a definition.
type Entry struct {
	Name       string
	Path       string
	Definition *Definition
}

// LoadNamed resolves only names in dir. Explicit file paths are accepted by
// validation, but not by commands that operate on installed sequences.
func LoadNamed(dir, name string) (*Entry, error) {
	if name == "" || name == "." || name == ".." || strings.ContainsAny(name, `/\`) {
		return nil, fmt.Errorf("invalid sequence name %q: expected a name, not a file path", name)
	}
	path := filepath.Join(dir, name+".toml")
	d, err := Load(path)
	if err != nil {
		return nil, err
	}
	return &Entry{Name: name, Path: path, Definition: d}, nil
}

// List loads installed sequences in filename order. Valid entries are returned
// alongside any errors, so one broken file does not hide the other sequences.
func List(dir string) ([]Entry, error) {
	paths, err := Files(dir)
	if err != nil {
		return nil, err
	}
	var entries []Entry
	var errs []error
	for _, path := range paths {
		name := strings.TrimSuffix(filepath.Base(path), ".toml")
		entry, err := LoadNamed(dir, name)
		if err != nil {
			errs = append(errs, err)
			continue
		}
		entries = append(entries, *entry)
	}
	return entries, errors.Join(errs...)
}

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
