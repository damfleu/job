package logfile

import (
	"crypto/md5"
	"fmt"
	"os"
	"path/filepath"
)

// Path returns the log file path for key within stateDir. Logs are sharded into subdirectories
// using the first 2 chars of the MD5 hash of the key to avoid large flat directories.
func Path(stateDir, key string) string {
	hash := fmt.Sprintf("%x", md5.Sum([]byte(key)))
	return filepath.Join(stateDir, "log", hash[:2], key+".log")
}

// Create creates the log file for key within stateDir, making parent directories as needed.
func Create(stateDir, key string) (*os.File, error) {
	path := Path(stateDir, key)
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return nil, fmt.Errorf("logfile: mkdir: %w", err)
	}
	f, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_APPEND, 0o644)
	if err != nil {
		return nil, fmt.Errorf("logfile: create: %w", err)
	}
	return f, nil
}
