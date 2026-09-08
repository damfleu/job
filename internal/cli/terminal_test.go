package cli

import (
	"strings"
	"testing"

	"charm.land/lipgloss/v2"
	"github.com/stretchr/testify/assert"
)

func TestTruncateTerminalLines(t *testing.T) {
	lines := []string{"short", "a line that is too long"}

	truncateTerminalLines(lines, 10)

	assert.Equal(t, "short", lines[0])
	assert.True(t, strings.HasSuffix(lines[1], "…"))
	for _, line := range lines {
		assert.LessOrEqual(t, lipgloss.Width(line), 10)
	}
}

func TestTruncateTerminalLineAtMinimumWidth(t *testing.T) {
	assert.Equal(t, "…", truncateTerminalLine("long", 1))
}
