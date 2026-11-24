package mint

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/JanDeDobbeleer/mint/internal/agent/copilot/copilottest"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestCommandEntryPoints(t *testing.T) {
	base := t.TempDir()
	for _, name := range []string{"APPDATA", "HOME", "XDG_CONFIG_HOME", "COPILOT_HOME"} {
		t.Setenv(name, base)
	}
	tests := []struct {
		name string
		want string
		err  string
		args []string
	}{
		{name: "setup help", args: []string{"setup", "--help"}},
		{name: "removed config command is forwarded", args: []string{"config", "--help"}, err: "cannot read configuration"},
		{name: "removed App setup", args: []string{"setup", "--app-id", "123"}, err: "invalid setup arguments"},
		{name: "removed permission override", args: []string{"setup", "--permissions=contents=read"}, err: "invalid setup arguments"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var stdout, stderr bytes.Buffer
			err := Run(t.Context(), tt.args, "test-version", nil, &stdout, &stderr)
			if tt.err != "" {
				require.ErrorContains(t, err, tt.err)
				return
			}
			require.NoError(t, err)
			assert.Contains(t, stdout.String(), tt.want)
		})
	}
}

func TestCopilotArgumentsReachLaunch(t *testing.T) {
	base := t.TempDir()
	for _, name := range []string{"APPDATA", "HOME", "XDG_CONFIG_HOME"} {
		t.Setenv(name, base)
	}
	for _, args := range [][]string{
		{"--help"}, {"-h"}, {"help"}, {"--version"}, {"copilot"},
		{"--", "--no-sandbox"}, {"--model"}, {"-p", "", "-s"},
	} {
		t.Run(args[0], func(t *testing.T) {
			var stdout, stderr bytes.Buffer
			err := Run(t.Context(), args, "test-version", nil, &stdout, &stderr)
			require.ErrorContains(t, err, "cannot read configuration")
			assert.Empty(t, stdout.String())
		})
	}
}

func TestCopilotArgumentsCannotOverrideSandboxProtections(t *testing.T) {
	for _, arg := range []string{"--no-sandbox", "--sandbox=false", "--secret-env-vars=OTHER"} {
		t.Run(arg, func(t *testing.T) {
			require.ErrorContains(t, Run(t.Context(), []string{arg}, "test-version", nil, nil, nil), "sandbox")
		})
	}
}

func TestSetupInitializesConfigAndAppliesSandboxPolicy(t *testing.T) {
	base := t.TempDir()
	for _, name := range []string{"APPDATA", "HOME", "XDG_CONFIG_HOME"} {
		t.Setenv(name, base)
	}
	t.Setenv("COPILOT_HOME", base)
	root := t.TempDir()
	git, err := exec.LookPath("git")
	require.NoError(t, err)
	command := exec.Command(git, "init", "--quiet", root)
	require.NoError(t, command.Run())
	command = exec.Command(git, "-C", root, "remote", "add", "origin", "https://github.com/owner/project.git")
	require.NoError(t, command.Run())
	t.Chdir(root)
	binary := copilottest.Binary(t)
	binary, err = canonicalPath(binary)
	require.NoError(t, err)
	settingsPath := filepath.Join(base, "settings.json")
	require.NoError(t, os.WriteFile(settingsPath, []byte(`{"theme":"dark","sandbox":{"userPolicy":{"filesystem":{"readonlyPaths":["~/toolchain"]}}}}`), 0o600))
	var stdout, stderr bytes.Buffer
	err = setup(t.Context(), []string{"--copilot=" + binary}, &stdout, &stderr)
	require.NoError(t, err)
	dir, err := configDirectory()
	require.NoError(t, err)
	cfg, err := readConfig(dir)
	require.NoError(t, err)
	assert.Equal(t, "origin", cfg.Remote)
	assert.Equal(t, binary, cfg.Copilot)
	data, err := os.ReadFile(filepath.Join(dir, "config.json"))
	require.NoError(t, err)
	assert.NotContains(t, string(data), "PRIVATE KEY")
	assert.NotContains(t, string(data), `"app_id"`)
	assert.NotContains(t, string(data), `"key_file"`)
	assert.Contains(t, stdout.String(), "Run 'mint' from a repository checkout")
	settings, err := os.ReadFile(settingsPath)
	require.NoError(t, err)
	assert.Contains(t, string(settings), `"theme":"dark"`)
	assert.Contains(t, string(settings), `"readonlyPaths":["~/toolchain"]`)
	assert.Contains(t, string(settings), `"allowBypass":false`)
	assert.Contains(t, string(settings), `"GH_TOKEN"`)
	require.NoError(t, setup(t.Context(), nil, &bytes.Buffer{}, &bytes.Buffer{}))
}

func TestSetupDoesNotOverwriteExistingConfiguration(t *testing.T) {
	base := t.TempDir()
	for _, name := range []string{"APPDATA", "HOME", "XDG_CONFIG_HOME"} {
		t.Setenv(name, base)
	}
	dir, err := configDirectory()
	require.NoError(t, err)
	require.NoError(t, os.MkdirAll(dir, 0o700))
	configPath := filepath.Join(dir, "config.json")
	require.NoError(t, os.WriteFile(configPath, []byte("existing"), 0o600))
	err = setup(t.Context(), []string{"--copilot=unused"}, &bytes.Buffer{}, &bytes.Buffer{})
	require.ErrorContains(t, err, "never overwritten")
	data, err := os.ReadFile(configPath)
	require.NoError(t, err)
	assert.Equal(t, "existing", string(data))
}

func TestSetupRejectsLegacyKeyOptions(t *testing.T) {
	err := setup(t.Context(), []string{"--encrypt-key"}, &bytes.Buffer{}, &bytes.Buffer{})
	require.ErrorContains(t, err, "invalid setup arguments")
}

func TestTerminalPromptsDoNotConsumePipedInput(t *testing.T) {
	input := strings.NewReader("prompt intended for Copilot")
	prompt := terminalPrompter{stdin: input, stderr: &bytes.Buffer{}}
	_, closeTerminal, err := prompt.terminal()
	if err == nil {
		closeTerminal()
	}
	assert.Equal(t, len("prompt intended for Copilot"), input.Len())
}
