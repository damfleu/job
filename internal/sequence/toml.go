package sequence

import (
	"fmt"
	"io"
	"os"
	"strings"

	"github.com/BurntSushi/toml"
)

// Decode reads a TOML definition and validates its fields and dependency graph.
func Decode(r io.Reader) (*Definition, error) {
	var d Definition
	metadata, err := toml.NewDecoder(r).Decode(&d)
	if err != nil {
		return nil, fmt.Errorf("decoding sequence: %w", err)
	}
	if unknown := metadata.Undecoded(); len(unknown) > 0 {
		keys := make([]string, len(unknown))
		for i, key := range unknown {
			keys[i] = key.String()
		}
		return nil, fmt.Errorf("unknown sequence keys: %s", strings.Join(keys, ", "))
	}
	if err := d.Validate(); err != nil {
		return nil, err
	}
	return &d, nil
}

// Load reads one file. Errors include the path for directory-wide validation.
func Load(path string) (*Definition, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	d, err := Decode(f)
	if err != nil {
		return nil, fmt.Errorf("%s: %w", path, err)
	}
	return d, nil
}

// Encode writes a validated definition. It preserves explicit false values and
// leaves inherited settings omitted.
func Encode(w io.Writer, d *Definition) error {
	if err := d.Validate(); err != nil {
		return err
	}
	return toml.NewEncoder(w).Encode(d)
}
