// Package sequence describes file-based sequences independently of job storage
// and execution.
package sequence

// FormatVersion is independent of the job CLI release version.
const FormatVersion = 1

// Definition is the contents of a sequence TOML file. Its name comes from the
// filename and is not part of the definition.
type Definition struct {
	Version     int     `toml:"version" json:"version" jsonschema_description:"Required file-format version, independent of the CLI release version."`
	Description string  `toml:"description,omitempty" json:"description,omitempty" jsonschema_description:"Optional human-readable description of the sequence."`
	CWD         *string `toml:"cwd,omitempty" json:"cwd,omitempty" jsonschema:"pattern=^/[^\\x00]*$" jsonschema_description:"Absolute default working directory. Omit to use the invocation directory. No tilde or variable expansion. The run --cwd flag overrides every step directory."`
	Automated   *bool   `toml:"automated,omitempty" json:"automated,omitempty" jsonschema:"default=true" jsonschema_description:"Default automation setting for steps. Defaults to true; automated jobs are excluded from ., +, _, and = selection."`
	Steps       []Step  `toml:"steps" json:"steps" jsonschema:"minItems=1" jsonschema_description:"Steps with unique IDs; forward references are allowed. Dependencies determine when steps can run. Steps without dependencies between them may run concurrently."`
}

// Step is a node in the dependency graph. File order does not imply a
// dependency. Pointers distinguish omitted values from explicit empty or false
// values; a nil Command means it was omitted, while an empty slice is invalid.
type Step struct {
	ID           string   `toml:"id" json:"id" jsonschema:"pattern=\\S" jsonschema_description:"Required non-blank ID, unique within this sequence. Used by after and after_success to reference this step."`
	Run          *string  `toml:"run,omitempty" json:"run,omitempty" jsonschema:"oneof_required=run,pattern=\\S" jsonschema_description:"Non-blank shell script executed as sh -c with no additional shell flags. Exactly one of run and command is required."`
	Command      []string `toml:"command,omitempty" json:"command,omitempty" jsonschema:"oneof_required=command,minItems=1" jsonschema_description:"Literal program and arguments, with no shell expansion. The program must be non-empty; subsequent arguments may be empty. Exactly one of run and command is required."`
	After        []string `toml:"after,omitempty" json:"after,omitempty" jsonschema:"uniqueItems=true,pattern=\\S" jsonschema_description:"Wait for all referenced steps to finish regardless of outcome, including stopping or dependency failure. IDs must exist, differ from this step, and not also appear in after_success."`
	AfterSuccess []string `toml:"after_success,omitempty" json:"after_success,omitempty" jsonschema:"uniqueItems=true,pattern=\\S" jsonschema_description:"Require every referenced step to execute successfully; otherwise skip this step. IDs must exist, differ from this step, and not also appear in after. All conditions from both dependency fields must be met."`
	CWD          *string  `toml:"cwd,omitempty" json:"cwd,omitempty" jsonschema:"pattern=^/[^\\x00]*$" jsonschema_description:"Absolute step working directory. Omit to inherit the sequence directory, or the invocation directory if neither specifies cwd. The run --cwd flag overrides this value."`
	Automated    *bool    `toml:"automated,omitempty" json:"automated,omitempty" jsonschema_description:"Override the sequence automation setting. Omit to inherit it; explicit false overrides true."`
}
