package hostsandbox

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"

	"github.com/JanDeDobbeleer/mint/internal/agent"
)

// ProjectConfiguration inspects repository-owned agent configuration under root before a launch on the host.
// It rejects configuration that could add credential routes and returns the names of repository-defined MCP
// servers, which the agent adapter must switch off.
func ProjectConfiguration(root string, policy agent.RepoConfigPolicy) ([]string, error) {
	if policy.LSPConfigFile != "" {
		data, err := os.ReadFile(filepath.Join(root, policy.LSPConfigFile))
		if err != nil && !errors.Is(err, os.ErrNotExist) {
			return nil, errors.New("cannot inspect project language server configuration")
		}
		if err == nil {
			var document struct {
				Servers map[string]struct {
					Disabled bool `json:"disabled"`
				} `json:"lspServers"`
			}
			if json.Unmarshal(data, &document) != nil {
				return nil, errors.New("project language server configuration must contain valid JSON")
			}
			for _, server := range document.Servers {
				if !server.Disabled {
					return nil, errors.New("project language servers are not supported because their credential filtering is not guaranteed")
				}
			}
		}
	}
	for _, name := range policy.SettingsFiles {
		data, err := os.ReadFile(filepath.Join(root, name))
		if errors.Is(err, os.ErrNotExist) {
			continue
		}
		if err != nil {
			return nil, errors.New("cannot inspect project settings")
		}
		var values map[string]json.RawMessage
		if json.Unmarshal(data, &values) != nil {
			return nil, errors.New("project settings must contain valid JSON")
		}
		for _, key := range policy.RejectedSettingKeys {
			if _, found := values[key]; found {
				return nil, fmt.Errorf("project setting %q conflicts with the isolated launcher policy", key)
			}
		}
	}

	names := make(map[string]bool)
	for _, name := range policy.MCPConfigFiles {
		data, err := os.ReadFile(filepath.Join(root, name))
		if errors.Is(err, os.ErrNotExist) {
			continue
		}
		if err != nil {
			return nil, errors.New("cannot inspect project MCP configuration")
		}
		var document struct {
			Servers map[string]json.RawMessage `json:"mcpServers"`
		}
		if json.Unmarshal(data, &document) != nil {
			return nil, errors.New("project MCP configuration must contain valid JSON")
		}
		for server := range document.Servers {
			if server == "" || strings.ContainsAny(server, "\r\n\x00") {
				return nil, errors.New("invalid project MCP server name")
			}
			names[server] = true
		}
	}
	result := make([]string, 0, len(names))
	for name := range names {
		result = append(result, name)
	}
	slices.Sort(result)
	return result, nil
}
