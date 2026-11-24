package hostsandbox

import (
	"bytes"
	"encoding/base64"
	"encoding/json"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/JanDeDobbeleer/mint/internal/agent"
	"github.com/JanDeDobbeleer/mint/internal/agent/copilot"
	"github.com/JanDeDobbeleer/mint/internal/agent/copilot/copilottest"
	"github.com/JanDeDobbeleer/mint/internal/core"
	"github.com/JanDeDobbeleer/mint/internal/hostenv"
	"github.com/JanDeDobbeleer/mint/internal/runtime"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestMain(m *testing.M) {
	if copilottest.IsFake() {
		os.Exit(copilottest.Run())
	}
	os.Exit(m.Run())
}

func canonicalPath(t *testing.T, path string) string {
	t.Helper()
	absolute, err := filepath.Abs(path)
	require.NoError(t, err)
	resolved, err := filepath.EvalSymlinks(absolute)
	require.NoError(t, err)
	return resolved
}

// launch mirrors what the interactive launcher asks of the runtime.
func launch(t *testing.T, r Runtime, binary, root string, extra, denied, environ []string, stdout, stderr *bytes.Buffer) error {
	t.Helper()
	cop := copilot.New(binary)
	s, err := r.Start(t.Context(), runtime.SessionSpec{
		Agent: cop,
		Request: agent.AgentRequest{
			Credentials: map[string]core.Secret{copilot.CredentialName: core.NewSecretString("copilot-account-secret")},
			DeniedPaths: denied,
			ExtraArgs:   extra,
		},
		Stdin: strings.NewReader(""), Stdout: stdout, Stderr: stderr,
		Dir: root, Repo: "owner/project", GitHubToken: core.NewSecretString("repository-test-token"), Environ: environ,
	})
	if err != nil {
		return err
	}
	defer func() { _ = s.Close() }()
	return s.Wait()
}

func TestSessionLaunchesAnIsolatedChildWithoutManagingTokenLifecycle(t *testing.T) {
	binary := copilottest.Binary(t)
	tests := []struct {
		name       string
		prompt     string
		args       []string
		exitCode   int
		startErr   bool
		customHome bool
	}{
		{name: "custom home", customHome: true, args: []string{"-p", "explain this repository", "-s", "--model=example", "--resume", "--", "a b", ""}},
		{name: "default home", args: []string{"-C", "other"}},
		{name: "child failure", prompt: "exit-seven", exitCode: 7},
		{name: "failed launch", startErr: true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			root := canonicalPath(t, t.TempDir())
			keyDir := filepath.Join(t.TempDir(), "private")
			var stdout, stderr bytes.Buffer
			r := Runtime{}
			if tt.startErr {
				r.Execute = func(*exec.Cmd) error { return errors.New("process start failed") }
			}
			args := tt.args
			if tt.prompt != "" {
				args = []string{"--prompt=" + tt.prompt}
			}
			environ := []string{
				"GH_TOKEN=broad-user-secret", "github_token=other-secret", "GH_APP_PRIVATE_KEY=private-key",
				"GH_APP_PRIVATE_KEY_FILE=key.pem", "AWS_SECRET_ACCESS_KEY=cloud-secret",
				"NODE_OPTIONS=--require=untrusted.js", "GIT_CONFIG_COUNT=1",
				"GIT_CONFIG_KEY_0=credential.helper", "GIT_CONFIG_VALUE_0=personal-helper",
				"PATH=" + os.Getenv("PATH"),
				"SYSTEMROOT=" + os.Getenv("SYSTEMROOT"), "TEMP=" + os.TempDir(),
			}
			const settings = `{"sandbox":{"allowDevToolAccess":true},"model":"example"}`
			var home string
			if tt.customHome {
				home = t.TempDir()
				require.NoError(t, os.WriteFile(filepath.Join(home, "settings.json"), []byte(settings), 0o600))
				environ = append(environ, "COPILOT_HOME="+home, "COPILOT_CUSTOM_INSTRUCTIONS_DIRS="+home)
			}
			err := launch(t, r, binary, root, args, []string{keyDir}, environ, &stdout, &stderr)
			assert.NotContains(t, stderr.String(), "secret")
			assert.NotContains(t, stderr.String(), "repository-test-token")
			if tt.startErr {
				require.ErrorContains(t, err, "process start failed")
				return
			}
			if tt.exitCode != 0 {
				var exitErr *exec.ExitError
				require.ErrorAs(t, err, &exitErr)
				assert.Equal(t, tt.exitCode, exitErr.ExitCode())
			}
			if tt.exitCode == 0 {
				require.NoError(t, err)
			}
			var child copilottest.Snapshot
			require.NoError(t, json.Unmarshal(stdout.Bytes(), &child))
			assert.Equal(t, root, child.CWD)
			assert.Equal(t, "repository-test-token", child.Env["GH_TOKEN"])
			gitAuth := base64.StdEncoding.EncodeToString([]byte("x-access-token:repository-test-token"))
			assert.Equal(t, gitAuth, child.Env["MINT_GIT_AUTH"])
			wantDirs := filepath.Join(child.Env["GH_CONFIG_DIR"], "instructions")
			if tt.customHome {
				wantDirs = home + "," + wantDirs
			}
			assert.Equal(t, wantDirs, child.Env["COPILOT_CUSTOM_INSTRUCTIONS_DIRS"])
			assert.Contains(t, child.Instructions, "git \"--config-env=")
			assert.Contains(t, child.Instructions, "gh pr create --head")
			assert.NotContains(t, child.Instructions, gitAuth)
			assert.NotContains(t, child.Instructions, "repository-test-token")
			assert.Equal(t, "owner/project", child.Env["GH_REPO"])
			assert.Equal(t, "copilot-account-secret", child.Env["COPILOT_GITHUB_TOKEN"])
			assert.Equal(t, "1", child.Env["GIT_CONFIG_COUNT"])
			assert.Equal(t, "credential.helper", child.Env["GIT_CONFIG_KEY_0"])
			assert.Empty(t, child.Env["GIT_CONFIG_VALUE_0"])
			for _, name := range []string{"github_token", "GH_APP_PRIVATE_KEY", "GH_APP_PRIVATE_KEY_FILE", "AWS_SECRET_ACCESS_KEY", "NODE_OPTIONS"} {
				assert.NotContains(t, child.Env, name)
			}
			assert.Contains(t, child.Args, "--sandbox")
			assert.Contains(t, child.Args, "--disable-builtin-mcps")
			assert.Contains(t, child.Args, "--secret-env-vars=COPILOT_GITHUB_TOKEN")
			tree := filepath.Join(keyDir, "**")
			assert.Equal(t, append([]string{"--sandbox", "--disable-builtin-mcps", "--secret-env-vars=COPILOT_GITHUB_TOKEN",
				"--deny-tool=read(" + keyDir + ")", "--deny-tool=read(" + tree + ")",
				"--deny-tool=write(" + keyDir + ")", "--deny-tool=write(" + tree + ")"}, args...), child.Args)
			if tt.customHome {
				assert.Equal(t, home, child.Env["COPILOT_HOME"])
				assert.Equal(t, "example", child.Settings["model"])
				assert.Equal(t, map[string]any{"allowDevToolAccess": true}, child.Settings["sandbox"])
				data, readErr := os.ReadFile(filepath.Join(home, "settings.json"))
				require.NoError(t, readErr)
				assert.Equal(t, settings, string(data))
			} else {
				assert.NotContains(t, child.Env, "COPILOT_HOME")
			}
			assert.NoDirExists(t, child.Env["GH_CONFIG_DIR"])
		})
	}
}

func TestStartFailureAndCloseRemoveTheSessionDirectory(t *testing.T) {
	root := canonicalPath(t, t.TempDir())
	cop := copilot.New(copilottest.Binary(t))
	spec := runtime.SessionSpec{
		Agent:  cop,
		Stdout: &bytes.Buffer{}, Stderr: &bytes.Buffer{}, Dir: root,
		Environ: []string{"PATH=" + os.Getenv("PATH")},
	}
	// no credential: the adapter refuses and the runtime must not leave a scratch directory behind
	_, err := Runtime{}.Start(t.Context(), spec)
	require.ErrorContains(t, err, "credential is missing")

	spec.Request.Credentials = map[string]core.Secret{copilot.CredentialName: core.NewSecretString("x")}
	started, err := Runtime{}.Start(t.Context(), spec)
	require.NoError(t, err)
	scratch := started.(*session).scratch
	require.DirExists(t, scratch)
	require.NoError(t, started.Close())
	assert.NoDirExists(t, scratch)
	require.NoError(t, started.Close(), "Close is idempotent")
}

func TestBeforeLaunchErrorAbortsTheProcess(t *testing.T) {
	root := canonicalPath(t, t.TempDir())
	executed := false
	started, err := Runtime{Execute: func(*exec.Cmd) error { executed = true; return nil }}.Start(t.Context(), runtime.SessionSpec{
		Agent: copilot.New(copilottest.Binary(t)),
		Request: agent.AgentRequest{
			Credentials: map[string]core.Secret{copilot.CredentialName: core.NewSecretString("x")},
		},
		BeforeLaunch: func() error { return errors.New("cannot report launch status") },
		Dir:          root, Environ: []string{"PATH=" + os.Getenv("PATH")},
	})
	require.NoError(t, err)
	scratch := started.(*session).scratch
	require.ErrorContains(t, started.Wait(), "cannot report launch status")
	assert.False(t, executed)
	assert.NoDirExists(t, scratch)
}

func TestPreflightRefusesAgentsWithoutTheirOwnSandbox(t *testing.T) {
	r := Runtime{}
	require.NoError(t, r.Preflight(t.Context(), core.Policy{}, agent.Info(copilot.New("copilot"))))
	err := r.Preflight(t.Context(), core.Policy{}, core.AgentInfo{Name: "claude"})
	require.ErrorContains(t, err, "claude")
}

func TestGitKeepsSSHRemoteAndIgnoresOtherCredentialHelpers(t *testing.T) {
	git, err := exec.LookPath("git")
	require.NoError(t, err)
	root := t.TempDir()
	runGit := func(args ...string) string {
		t.Helper()
		command := exec.Command(git, args...)
		output, err := command.CombinedOutput()
		require.NoError(t, err, string(output))
		return strings.TrimSpace(string(output))
	}
	runGit("init", "--quiet", root)
	runGit("-C", root, "remote", "add", "origin", "git@github.com:owner/project.git")
	runGit("-C", root, "config", "credential.helper", "personal-helper")
	runGit("-C", root, "config", "credential.https://github.com.helper", "personal-helper")

	globalConfig := filepath.Join(t.TempDir(), "gitconfig")
	require.NoError(t, os.WriteFile(globalConfig, nil, 0o600))
	environ := hostenv.Filter(os.Environ(), map[string]string{
		"GIT_CONFIG_COUNT":    "1",
		"GIT_CONFIG_KEY_0":    "credential.helper",
		"GIT_CONFIG_VALUE_0":  "",
		"GIT_CONFIG_GLOBAL":   globalConfig,
		"GIT_CONFIG_NOSYSTEM": "1",
		"GIT_TERMINAL_PROMPT": "0",
	})

	urlCommand := exec.Command(git, "-C", root, "remote", "get-url", "origin")
	urlCommand.Env = environ
	urlOutput, err := urlCommand.CombinedOutput()
	require.NoError(t, err, string(urlOutput))
	assert.Equal(t, "git@github.com:owner/project.git", strings.TrimSpace(string(urlOutput)))

	credentialCommand := exec.Command(git, "-C", root, "credential", "fill")
	credentialCommand.Env = environ
	credentialCommand.Stdin = strings.NewReader("protocol=https\nhost=github.com\npath=owner/project.git\n\n")
	credentialOutput, err := credentialCommand.CombinedOutput()
	require.Error(t, err)
	assert.NotContains(t, string(credentialOutput), "personal-helper")
}

func TestProjectConfigurationCannotEnableAdditionalCredentialRoutes(t *testing.T) {
	tests := []struct {
		name    string
		file    string
		content string
		err     string
		servers []string
	}{
		{name: "empty project"},
		{name: "disable remote MCP", file: ".mcp.json", content: `{"mcpServers":{"github":{"type":"http","headers":{"Authorization":"secret"}}}}`,
			servers: []string{"github"}},
		{name: "weaken sandbox", file: filepath.Join(".github", "copilot", "settings.json"),
			content: `{"sandbox":{"enabled":false}}`, err: "conflicts"},
		{name: "enable hooks", file: filepath.Join(".claude", "settings.json"), content: `{"disableAllHooks":false}`, err: "conflicts"},
		{name: "enable plugins", file: filepath.Join(".github", "copilot", "settings.json"),
			content: `{"enabledPlugins":{"extra":true}}`, err: "conflicts"},
		{name: "active language server", file: filepath.Join(".github", "lsp.json"),
			content: `{"lspServers":{"custom":{"command":"untrusted"}}}`, err: "not supported"},
		{name: "disabled language server", file: filepath.Join(".github", "lsp.json"),
			content: `{"lspServers":{"custom":{"disabled":true}}}`},
		{name: "invalid config", file: ".mcp.json", content: "not JSON", err: "valid JSON"},
	}
	policy := copilot.New("copilot").RepoConfigPolicy()
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			root := t.TempDir()
			if tt.file != "" {
				path := filepath.Join(root, tt.file)
				require.NoError(t, os.MkdirAll(filepath.Dir(path), 0o700))
				require.NoError(t, os.WriteFile(path, []byte(tt.content), 0o600))
			}
			servers, err := ProjectConfiguration(root, policy)
			if tt.err != "" {
				require.ErrorContains(t, err, tt.err)
				return
			}
			require.NoError(t, err)
			assert.ElementsMatch(t, tt.servers, servers)
		})
	}
}

func TestStartDisablesProjectMCPServers(t *testing.T) {
	root := canonicalPath(t, t.TempDir())
	require.NoError(t, os.WriteFile(filepath.Join(root, ".mcp.json"), []byte(`{"mcpServers":{"zeta":{},"alpha":{}}}`), 0o600))
	var stdout, stderr bytes.Buffer
	require.NoError(t, launch(t, Runtime{}, copilottest.Binary(t), root, []string{"-s"}, nil,
		[]string{"PATH=" + os.Getenv("PATH"), "SYSTEMROOT=" + os.Getenv("SYSTEMROOT")}, &stdout, &stderr))
	var child copilottest.Snapshot
	require.NoError(t, json.Unmarshal(stdout.Bytes(), &child))
	assert.Equal(t, []string{"--sandbox", "--disable-builtin-mcps", "--secret-env-vars=COPILOT_GITHUB_TOKEN",
		"--disable-mcp-server=alpha", "--disable-mcp-server=zeta", "-s"}, child.Args)
}
