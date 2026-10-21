package sequence

import (
	"bytes"
	"errors"
	"fmt"
	"os"
	"path/filepath"

	"job/internal/permissions"
)

// WriteFile publishes a complete TOML definition in one filesystem operation.
// Without force, an existing path is never overwritten, even by concurrent saves.
func WriteFile(path string, d *Definition, force bool) error {
	var content bytes.Buffer
	if err := Encode(&content, d); err != nil {
		return err
	}
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, permissions.DirMode); err != nil {
		return err
	}
	f, err := os.CreateTemp(dir, ".sequence-*.tmp")
	if err != nil {
		return err
	}
	defer os.Remove(f.Name())
	defer f.Close()
	if _, err := f.Write(content.Bytes()); err != nil {
		return err
	}
	if err := f.Sync(); err != nil {
		return err
	}
	if err := f.Close(); err != nil {
		return err
	}
	if force {
		return os.Rename(f.Name(), path)
	}
	// Linking the completed file has exclusive-create semantics. Rename alone
	// would overwrite a file created between an existence check and publication.
	if err := os.Link(f.Name(), path); err != nil {
		if errors.Is(err, os.ErrExist) {
			return fmt.Errorf("%s already exists; use --force to replace it", path)
		}
		return err
	}
	return nil
}
