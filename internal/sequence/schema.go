package sequence

import "github.com/invopop/jsonschema"

// Schema describes the decoded TOML document. Graph constraints additionally
// require Validate; JSON Schema cannot enforce arbitrary cross-step references.
func Schema() *jsonschema.Schema {
	r := jsonschema.Reflector{Anonymous: true, ExpandedStruct: true}
	return r.Reflect(&Definition{})
}

// JSONSchemaExtend adds constraints that cannot be expressed with field tags.
func (Definition) JSONSchemaExtend(s *jsonschema.Schema) {
	s.Title = "Job sequence"
	s.Description = "A TOML sequence definition. Its name is the installed filename stem. Validate with job seq validate PATH to also check unique step IDs, dependency references, overlapping dependencies, and cycles. Environment configuration is not supported."
	version, _ := s.Properties.Get("version")
	version.Const = FormatVersion
	s.Examples = []any{Definition{
		Version: FormatVersion,
		Steps: []Step{
			{ID: "build", Command: []string{"make", "build"}},
			{ID: "test", Run: new("make test"), AfterSuccess: []string{"build"}},
		},
	}}
}

// JSONSchemaExtend distinguishes the program from the remaining argv entries,
// which may legitimately be empty strings.
func (Step) JSONSchemaExtend(s *jsonschema.Schema) {
	command, _ := s.Properties.Get("command")
	command.PrefixItems = []*jsonschema.Schema{{Type: "string", MinLength: new(uint64(1)), Pattern: `^[^\x00]*$`}}
	command.Items.Pattern = `^[^\x00]*$`
	run, _ := s.Properties.Get("run")
	run.AllOf = []*jsonschema.Schema{{Pattern: `^[^\x00]*$`}}
}
