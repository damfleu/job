package cli

import (
	"fmt"
	"strconv"
	"strings"
	"time"
)

// parseDuration extends time.ParseDuration with support for days (d) and weeks (w).
func parseDuration(s string) (time.Duration, error) {
	if d, err := time.ParseDuration(s); err == nil {
		return d, nil
	}
	lower := strings.ToLower(s)
	if strings.HasSuffix(lower, "w") {
		n, err := strconv.ParseFloat(strings.TrimSuffix(lower, "w"), 64)
		if err != nil {
			return 0, fmt.Errorf("invalid duration %q", s)
		}
		return time.Duration(n * float64(7*24*time.Hour)), nil
	}
	if strings.HasSuffix(lower, "d") {
		n, err := strconv.ParseFloat(strings.TrimSuffix(lower, "d"), 64)
		if err != nil {
			return 0, fmt.Errorf("invalid duration %q", s)
		}
		return time.Duration(n * float64(24*time.Hour)), nil
	}
	return 0, fmt.Errorf("invalid duration %q", s)
}
