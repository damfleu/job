package core

import (
	"bytes"
	"context"
	"os"
	"os/exec"
	"time"
)

// ResolveContext runs each resolver in order with workDir as CWD.
// Returns the first non-empty stdout from a successful exit-0 run.
// Falls back to hostname if no resolver matches.
func ResolveContext(workDir string, resolvers []string) string {
	for _, r := range resolvers {
		if out := runResolver(r, workDir); out != "" {
			return out
		}
	}
	h, _ := os.Hostname()
	return h
}

func runResolver(path, workDir string) string {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	var buf bytes.Buffer
	cmd := exec.CommandContext(ctx, "sh", "-c", path)
	cmd.Dir = workDir
	cmd.Stdout = &buf

	if err := cmd.Run(); err != nil {
		return ""
	}
	return string(bytes.TrimSpace(buf.Bytes()))
}
