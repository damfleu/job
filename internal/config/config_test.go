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
		wantErr bool
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
			wantErr: true,
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
			if tt.wantErr {
				require.Error(t, err)
				return
			}
			require.NoError(t, err)
			require.Equal(t, tt.want, got)
		})
	}
}
