package model

import (
	"regexp"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

var keyRe = regexp.MustCompile(`^\d+_[0-9a-f]{8}_[a-zA-Z0-9_#+]+$`)

func TestEscapeProgram(t *testing.T) {
	tests := []struct {
		input    string
		expected string
	}{
		{"make", "make"},
		{"go", "go"},
		{"/usr/bin/make", "make"},
		{"./my-script.sh", "my+script+sh"},
		{"my script", "my+script"},
		{"test_runner", "test_runner"},
		{"test#job", "test#job"},
		{"cmd/sub", "sub"},
	}

	for _, tt := range tests {
		t.Run(tt.input, func(t *testing.T) {
			assert.Equal(t, tt.expected, EscapeProgram(tt.input))
		})
	}
}

func TestGenerateKeyFormat(t *testing.T) {
	key := GenerateKey("make")
	require.Regexp(t, keyRe, key, "key %q does not match expected format", key)
}

func TestGenerateKeyContainsEscapedProgram(t *testing.T) {
	tests := []struct {
		program  string
		wantSuffix string
	}{
		{"make", "make"},
		{"./my-script.sh", "my+script+sh"},
		{"/usr/local/bin/go", "go"},
	}

	for _, tt := range tests {
		t.Run(tt.program, func(t *testing.T) {
			key := GenerateKey(tt.program)
			assert.Regexp(t, keyRe, key)
			assert.Contains(t, key, "_"+tt.wantSuffix)
		})
	}
}

func TestGenerateKeyUniqueness(t *testing.T) {
	const n = 200 // 8 hex chars = 2^32 values; collision risk at this scale is negligible
	seen := make(map[string]struct{}, n)
	for i := 0; i < n; i++ {
		key := GenerateKey("test")
		_, dup := seen[key]
		assert.False(t, dup, "duplicate key generated: %s", key)
		seen[key] = struct{}{}
	}
}
