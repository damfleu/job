// Package sequence describes file-based sequences independently of job storage
// and execution.
package sequence

// FormatVersion is independent of the job CLI release version.
const FormatVersion = 1

// Definition is the contents of a sequence TOML file. Its name comes from the
// filename and is not part of the definition.
type Definition struct {
	Version     int     `toml:"version" json:"version"`
	Description string  `toml:"description,omitempty" json:"description,omitempty"`
	CWD         *string `toml:"cwd,omitempty" json:"cwd,omitempty"`
	Automated   *bool   `toml:"automated,omitempty" json:"automated,omitempty"`
	Steps       []Step  `toml:"steps" json:"steps"`
}

// Step is a node in the dependency graph. File order does not imply a
// dependency. Pointers distinguish omitted values from explicit empty or false
// values; a nil Command means it was omitted, while an empty slice is invalid.
type Step struct {
	ID           string   `toml:"id" json:"id"`
	Run          *string  `toml:"run,omitempty" json:"run,omitempty"`
	Command      []string `toml:"command,omitempty" json:"command,omitempty"`
	After        []string `toml:"after,omitempty" json:"after,omitempty"`
	AfterSuccess []string `toml:"after_success,omitempty" json:"after_success,omitempty"`
	CWD          *string  `toml:"cwd,omitempty" json:"cwd,omitempty"`
	Automated    *bool    `toml:"automated,omitempty" json:"automated,omitempty"`
}
