package cli

import "charm.land/lipgloss/v2"

func truncateTerminalLines(lines []string, width int) {
	for i := range lines {
		lines[i] = truncateTerminalLine(lines[i], width)
	}
}

func truncateTerminalLine(s string, width int) string {
	if lipgloss.Width(s) <= width {
		return s
	}
	if width <= 1 {
		return "…"
	}
	return lipgloss.NewStyle().MaxWidth(width-1).Render(s) + "…"
}
