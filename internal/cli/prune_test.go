package cli

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestParseDuration(t *testing.T) {
	tests := []struct {
		input string
		want  time.Duration
	}{
		{"30d", 30 * 24 * time.Hour},
		{"2w", 2 * 7 * 24 * time.Hour},
		{"24h", 24 * time.Hour},
		{"90m", 90 * time.Minute},
		{"1d", 24 * time.Hour},
	}
	for _, tt := range tests {
		t.Run(tt.input, func(t *testing.T) {
			got, err := parseDuration(tt.input)
			require.NoError(t, err)
			assert.Equal(t, tt.want, got)
		})
	}
}

func TestParseDurationInvalid(t *testing.T) {
	for _, s := range []string{"", "abc", "10x", "w"} {
		_, err := parseDuration(s)
		assert.Error(t, err, "expected error for %q", s)
	}
}
