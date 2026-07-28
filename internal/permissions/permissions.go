// Package permissions defines the filesystem modes used for owner-only job state.
package permissions

import "os"

const (
	DirMode  os.FileMode = 0o700
	FileMode os.FileMode = 0o600
)
