package config

import (
	"errors"
	"os"

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

// Load reads the TOML file at path into a Config, starting from Default().
// If the file does not exist, the default Config is returned with no error.
func Load(path string) (Config, error) {
	cfg := Default()
	_, err := toml.DecodeFile(path, &cfg)
	if err != nil && errors.Is(err, os.ErrNotExist) {
		return cfg, nil
	}
	return cfg, err
}
