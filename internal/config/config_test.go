package config_test

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"

	"job/internal/config"
)

func TestLoad(t *testing.T) {
	tests := []struct {
		name    string
		content string // empty string means no file
		want    config.Config
		wantErr string
	}{
		{
			name:    "missing file returns default config",
			content: "",
			want:    config.Default(),
		},
		{
			name:    "empty file returns default config",
			content: "\n",
			want:    config.Default(),
		},
		{
			name:    "list limit is parsed",
			content: "[list]\nlimit = 5\n",
			want:    config.Config{List: config.ListConfig{Limit: 5}},
		},
		{
			name:    "invalid toml returns error",
			content: "[[[\n",
			wantErr: "expected '.' or ']'",
		},
		{
			name:    "unknown keys are rejected",
			content: "top_level_typo = true\n[list]\nlimt = 5\n",
			wantErr: "unknown configuration keys: top_level_typo, list.limt",
		},
		{
			name:    "negative list limit is rejected",
			content: "[list]\nlimit = -1\n",
			wantErr: "list.limit cannot be negative",
		},
		{
			name:    "empty notifier program is rejected",
			content: "[[notifier]]\nprogram = \"  \"\nnotify = \"always\"\n",
			wantErr: "notifier[0].program cannot be empty",
		},
		{
			name:    "unsupported notifier mode is rejected",
			content: "[[notifier]]\nprogram = \"notify-send\"\nnotify = \"explict\"\n",
			wantErr: `notifier[0].notify must be "always" or "explicit", got "explict"`,
		},
		{
			name:    "supported notifier modes are accepted",
			content: "[[notifier]]\nprogram = \"first\"\n[[notifier]]\nprogram = \"second\"\nnotify = \"explicit\"\n[[notifier]]\nprogram = \"third\"\nnotify = \"always\"\n",
			want: config.Config{
				List: config.ListConfig{Limit: 20},
				Notifiers: []config.NotifierConfig{
					{Program: "first"},
					{Program: "second", Notify: "explicit"},
					{Program: "third", Notify: "always"},
				},
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var path string
			if tt.content == "" {
				path = filepath.Join(t.TempDir(), "config.toml")
			} else {
				path = filepath.Join(t.TempDir(), "config.toml")
				require.NoError(t, os.WriteFile(path, []byte(tt.content), 0o644))
			}

			got, err := config.Load(path)
			if tt.wantErr != "" {
				require.ErrorContains(t, err, tt.wantErr)
				return
			}
			require.NoError(t, err)
			require.Equal(t, tt.want, got)
		})
	}
}
