package mint

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestConfigurationPersistence(t *testing.T) {
	tests := []struct {
		name     string
		err      string
		existing bool
		legacy   bool
	}{
		{name: "round trip"},
		{name: "never overwrite", existing: true, err: "never overwritten"},
		{name: "legacy App fields ignored", legacy: true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			dir := t.TempDir()
			cfg := config{Remote: "origin", Copilot: filepath.Join(dir, "copilot")}
			if tt.existing {
				require.NoError(t, os.WriteFile(filepath.Join(dir, "config.json"), []byte("original"), 0o600))
			}
			err := writeConfig(dir, cfg)
			if tt.err != "" {
				require.ErrorContains(t, err, tt.err)
				if tt.existing {
					data, readErr := os.ReadFile(filepath.Join(dir, "config.json"))
					require.NoError(t, readErr)
					assert.Equal(t, "original", string(data))
				}
				return
			}
			require.NoError(t, err)
			want := cfg
			if tt.legacy {
				legacyKey := filepath.Join(dir, "old-key.pem")
				data, marshalErr := json.Marshal(map[string]any{
					"app_id":   42,
					"key_file": legacyKey,
					"remote":   "origin",
					"copilot":  cfg.Copilot,
				})
				require.NoError(t, marshalErr)
				require.NoError(t, os.WriteFile(filepath.Join(dir, "config.json"), data, 0o600))
				want.LegacyKeyFile = legacyKey
			}
			got, err := readConfig(dir)
			require.NoError(t, err)
			assert.Equal(t, want, got)
			data, err := os.ReadFile(filepath.Join(dir, "config.json"))
			require.NoError(t, err)
			assert.NotContains(t, string(data), "PRIVATE KEY")
		})
	}
}
