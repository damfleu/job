package config

import (
	"errors"
	"fmt"
	"os"
	"strings"

	"github.com/BurntSushi/toml"
)

// Config holds all user-configurable settings.
type Config struct {
	List      ListConfig       `toml:"list"`
	Context   ContextConfig    `toml:"context"`
	Notifiers []NotifierConfig `toml:"notifier"`
}

// ContextConfig holds settings for context resolution.
type ContextConfig struct {
	Resolvers []string `toml:"resolvers"`
}

// NotifierConfig holds settings for a single notifier program.
type NotifierConfig struct {
	Program string `toml:"program"`
	Notify  string `toml:"notify"` // "always" | "explicit"; empty means "explicit"
}

// ListConfig holds settings for the list command.
type ListConfig struct {
	Limit int `toml:"limit"`
}

// Default returns a Config populated with application defaults.
func Default() Config {
	return Config{
		List: ListConfig{Limit: 20},
	}
}

// Validate checks constraints that TOML decoding alone cannot enforce.
func (c Config) Validate() error {
	if c.List.Limit < 0 {
		return fmt.Errorf("list.limit cannot be negative")
	}
	for i, notifier := range c.Notifiers {
		if strings.TrimSpace(notifier.Program) == "" {
			return fmt.Errorf("notifier[%d].program cannot be empty", i)
		}
		switch notifier.Notify {
		case "", "always", "explicit":
		default:
			return fmt.Errorf("notifier[%d].notify must be %q or %q, got %q", i, "always", "explicit", notifier.Notify)
		}
	}
	return nil
}

// Load reads the TOML file at path into a Config, starting from Default().
// If the file does not exist, the default Config is returned with no error.
func Load(path string) (Config, error) {
	cfg := Default()
	metadata, err := toml.DecodeFile(path, &cfg)
	if err != nil && errors.Is(err, os.ErrNotExist) {
		return cfg, nil
	}
	if err != nil {
		return cfg, err
	}
	if undecoded := metadata.Undecoded(); len(undecoded) > 0 {
		keys := make([]string, len(undecoded))
		for i, key := range undecoded {
			keys[i] = key.String()
		}
		return cfg, fmt.Errorf("unknown configuration keys: %s", strings.Join(keys, ", "))
	}
	if err := cfg.Validate(); err != nil {
		return cfg, err
	}
	return cfg, nil
}
