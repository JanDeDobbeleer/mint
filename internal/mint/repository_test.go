package mint

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestRepositoryIdentity(t *testing.T) {
	tests := []struct {
		name   string
		remote string
		err    string
	}{
		{name: "HTTPS", remote: "https://github.com/owner/project.git"},
		{name: "SCP", remote: "git@github.com:owner/project.git"},
		{name: "SSH URL", remote: "ssh://git@github.com/owner/project"},
		{name: "host spoof", remote: "https://github.com.attacker.test/owner/project", err: "github.com"},
		{name: "host credentials spoof", remote: "https://github.com@attacker.test/owner/project", err: "github.com"},
		{name: "embedded credential", remote: "https://secret@github.com/owner/project", err: "credentials"},
		{name: "query credential", remote: "https://github.com/owner/project?token=secret", err: "query"},
		{name: "HTTP", remote: "http://github.com/owner/project", err: "HTTPS or SSH"},
		{name: "port", remote: "https://github.com:8443/owner/project", err: "port"},
		{name: "traversal", remote: "https://github.com/owner/..", err: "invalid"},
		{name: "extra component", remote: "https://github.com/owner/project/other", err: "exactly one"},
		{name: "local path", remote: `D:\owner\project`, err: "HTTPS or SSH"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			repo, err := parseRemote(tt.remote)
			if tt.err != "" {
				require.ErrorContains(t, err, tt.err)
				assert.NotContains(t, err.Error(), "secret")
				return
			}
			require.NoError(t, err)
			assert.Equal(t, "owner", repo.Owner)
			assert.Equal(t, "project", repo.Name)
		})
	}
}

func TestProtectedPathsCannotOverlapWorkingTree(t *testing.T) {
	tests := []struct {
		name     string
		location string
		err      string
	}{
		{name: "separate directory", location: "secrets"},
		{name: "inside repository", location: filepath.Join("repo", "secrets"), err: "configuration directory"},
		{name: "parent of repository", location: ".", err: "configuration directory"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			base, err := canonicalPath(t.TempDir())
			require.NoError(t, err)
			root := filepath.Join(base, "repo")
			configDir := filepath.Join(base, tt.location)
			for _, path := range []string{root, configDir} {
				require.NoError(t, os.MkdirAll(path, 0o700))
			}
			denied, err := protectPaths(repository{Root: root}, configDir, config{})
			if tt.err != "" {
				require.ErrorContains(t, err, tt.err)
				return
			}
			require.NoError(t, err)
			assert.Contains(t, denied, configDir)
		})
	}
}

func TestLegacyPrivateKeyDirectoryRemainsDenied(t *testing.T) {
	base, err := canonicalPath(t.TempDir())
	require.NoError(t, err)
	root := filepath.Join(base, "repo")
	configDir := filepath.Join(base, "config")
	keyDir := filepath.Join(base, "legacy-keys")
	for _, path := range []string{root, configDir, keyDir} {
		require.NoError(t, os.MkdirAll(path, 0o700))
	}
	key := filepath.Join(keyDir, "app.pem")
	require.NoError(t, os.WriteFile(key, []byte("legacy"), 0o600))

	denied, err := protectPaths(repository{Root: root}, configDir, config{LegacyKeyFile: key})
	require.NoError(t, err)
	assert.ElementsMatch(t, []string{configDir, keyDir}, denied)

	require.NoError(t, os.Remove(key))
	denied, err = protectPaths(repository{Root: root}, configDir, config{LegacyKeyFile: key})
	require.NoError(t, err)
	assert.ElementsMatch(t, []string{configDir, keyDir}, denied)
}

func TestGitDiscoveryIgnoresInheritedRepositoryOverrides(t *testing.T) {
	got := gitEnvironment([]string{"PATH=tools", "GIT_DIR=elsewhere", "git_work_tree=other", "GIT_CONFIG_COUNT=1", "GIT_SSH_COMMAND=untrusted"})
	assert.Equal(t, []string{"PATH=tools", "GIT_TERMINAL_PROMPT=0"}, got)
}
