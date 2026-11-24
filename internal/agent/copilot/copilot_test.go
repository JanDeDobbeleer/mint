package copilot

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/JanDeDobbeleer/mint/internal/agent"
	"github.com/JanDeDobbeleer/mint/internal/agent/copilot/copilottest"
	"github.com/JanDeDobbeleer/mint/internal/core"
	"github.com/JanDeDobbeleer/mint/internal/hostenv"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestMain(m *testing.M) {
	if copilottest.IsFake() {
		os.Exit(copilottest.Run())
	}
	os.Exit(m.Run())
}

func TestInstructionsNameMatchesTheFake(t *testing.T) {
	assert.Equal(t, copilottest.InstructionsName, gitInstructionsName)
}

func TestSupportedCopilotVersions(t *testing.T) {
	tests := []struct {
		name    string
		version string
		valid   bool
	}{
		{name: "minimum", version: "GitHub Copilot CLI 1.0.112.", valid: true},
		{name: "future", version: "GitHub Copilot CLI 2.0.0.", valid: true},
		{name: "older", version: "GitHub Copilot CLI 1.0.111."},
		{name: "unrelated executable", version: "something else 1.0.112"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := validateCopilotVersion(tt.version)
			if tt.valid {
				require.NoError(t, err)
				return
			}
			require.Error(t, err)
		})
	}
}

func TestRecordedVersionsAreConsistent(t *testing.T) {
	assert.NoError(t, validateCopilotVersion("GitHub Copilot CLI "+MinimumVersion+"."))
	assert.NoError(t, validateCopilotVersion("GitHub Copilot CLI "+TestedVersion+"."))
	assert.Equal(t, "1.0.114", TestedVersion)
}

const scopedSandboxSettings = `{"sandbox":{"allowBypass":false,"auth":{"git":false,"gh":false},"credentials":{"envVars":{"GH_TOKEN":{"injectHosts":["github.com","uploads.github.com","api.github.com"]},"MINT_GIT_AUTH":{"injectHosts":["github.com"]}}}}}`

func TestSandboxCredentialCheckUsesSavedSettings(t *testing.T) {
	binary := copilottest.Binary(t)
	tests := []struct{ name, settings, wantErr string }{
		{"scoped settings", scopedSandboxSettings, ""},
		{"git auth enabled", strings.Replace(scopedSandboxSettings, `"git":false`, `"git":true`, 1), "sandbox.auth.git"},
		{"default gh", strings.Replace(scopedSandboxSettings, `,"gh":false`, "", 1), "sandbox.auth.gh"},
		{"default bypass", strings.Replace(scopedSandboxSettings, `"allowBypass":false,`, "", 1), "sandbox.allowBypass"},
		{"missing token mapping", strings.Replace(scopedSandboxSettings, `"GH_TOKEN"`, `"OTHER_TOKEN"`, 1), "injectHosts"},
		{"extra host", strings.Replace(scopedSandboxSettings, `"api.github.com"`, `"api.github.com","example.com"`, 1), "injectHosts"},
		{"missing Git credential mapping", strings.Replace(scopedSandboxSettings, `"MINT_GIT_AUTH"`, `"OTHER_AUTH"`, 1), "MINT_GIT_AUTH"},
		{"no sandbox settings", `{}`, "sandbox.auth.git"},
	}
	if os.PathSeparator == '\\' {
		tests = append(tests, struct{ name, settings, wantErr string }{
			"Windows credential proxy disabled",
			strings.Replace(scopedSandboxSettings, `"allowBypass":false`, `"allowBypass":false,"userPolicy":{"network":{"allowLocalNetwork":false}}`, 1),
			"sandbox.userPolicy.network.allowLocalNetwork",
		})
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			check := checkSandboxSettingsFile(t, binary, tt.settings)
			if tt.wantErr == "" {
				require.NoError(t, check)
			} else {
				require.ErrorContains(t, check, tt.wantErr)
			}
		})
	}
}

func TestCopilotReadsSavedSandboxPolicy(t *testing.T) {
	binary := os.Getenv("MINT_TEST_COPILOT")
	if binary == "" {
		t.Skip("set MINT_TEST_COPILOT to opt into the installed CLI configuration check")
	}
	require.NoError(t, checkSandboxSettingsFile(t, binary, scopedSandboxSettings))
}

func TestCopilotDiscoversSessionGitInstructions(t *testing.T) {
	binary := os.Getenv("MINT_TEST_COPILOT")
	if binary == "" {
		t.Skip("set MINT_TEST_COPILOT to opt into the installed CLI instruction check")
	}
	dir := t.TempDir()
	path := filepath.Join(dir, gitInstructionsName)
	require.NoError(t, os.WriteFile(path, gitInstructions, 0o600))
	cmd := exec.CommandContext(t.Context(), binary, "instruction", "list", "--json")
	cmd.Dir = t.TempDir()
	cmd.Env = hostenv.Filter(os.Environ(), map[string]string{
		"COPILOT_HOME":                     t.TempDir(),
		"COPILOT_CUSTOM_INSTRUCTIONS_DIRS": dir,
	})
	output, err := cmd.CombinedOutput()
	require.NoError(t, err, string(output))
	encoded, err := json.Marshal(path)
	require.NoError(t, err)
	assert.Contains(t, string(output), string(encoded))
}

// The flags below were taken from documentation, not from a run: the names of --sync-session, --session-id,
// --max-ai-credits and --output-format are version-gated by this opt-in check against the installed CLI (design 5.2, 6.1).
func TestInstalledCopilotKnowsTheHeadlessFlags(t *testing.T) {
	binary := os.Getenv("MINT_TEST_COPILOT")
	if binary == "" {
		t.Skip("set MINT_TEST_COPILOT to opt into the installed CLI flag check")
	}
	output, err := exec.CommandContext(t.Context(), binary, "--help").CombinedOutput()
	require.NoError(t, err, string(output))
	for _, flag := range []string{"--sync-session", "--session-id", "--resume", "--max-ai-credits", "--output-format",
		"--no-ask-user", "--no-auto-update", "--allow-tool", "--deny-tool", "--available-tools", "--excluded-tools", "--secret-env-vars"} {
		assert.Contains(t, string(output), flag)
	}
}

func TestApplySandboxSettingsWithCopilot(t *testing.T) {
	binary := os.Getenv("MINT_TEST_COPILOT")
	if binary == "" {
		t.Skip("set MINT_TEST_COPILOT to opt into the installed CLI configuration check")
	}
	home := t.TempDir()
	t.Setenv("COPILOT_HOME", home)
	path := filepath.Join(home, "settings.json")
	const initial = `{"theme":"dark","sandbox":{"allowDevToolAccess":false,"allowBypass":true,
"auth":{"git":false,"gh":true},
"credentials":{"envVars":{"GH_TOKEN":{"injectHosts":["old.example.com"]},"OTHER_TOKEN":{"injectHosts":["keep.example.com"]}}},
"userPolicy":{"filesystem":{"readonlyPaths":["~/toolchain"],"readwritePaths":["~/build-cache"]},"network":{"allowLocalNetwork":false}}}}`
	require.NoError(t, os.WriteFile(path, []byte(initial), 0o600))
	var stderr bytes.Buffer
	require.NoError(t, ApplySandboxSettings(t.Context(), binary, home, os.Environ(), &stderr), stderr.String())
	data, err := os.ReadFile(path)
	require.NoError(t, err)
	var settings map[string]any
	require.NoError(t, json.Unmarshal(data, &settings))
	assert.Equal(t, "dark", settings["theme"])
	sandbox := settings["sandbox"].(map[string]any)
	assert.Equal(t, false, sandbox["allowDevToolAccess"])
	assert.Equal(t, map[string]any{"git": false, "gh": false}, sandbox["auth"])
	userPolicy := sandbox["userPolicy"].(map[string]any)
	assert.Equal(t, map[string]any{"readonlyPaths": []any{"~/toolchain"}, "readwritePaths": []any{"~/build-cache"}}, userPolicy["filesystem"])
	assert.Equal(t, os.PathSeparator == '\\', userPolicy["network"].(map[string]any)["allowLocalNetwork"])
	envVars := sandbox["credentials"].(map[string]any)["envVars"].(map[string]any)
	assert.Equal(t, map[string]any{"injectHosts": []any{"github.com"}}, envVars["MINT_GIT_AUTH"])
	assert.Equal(t, map[string]any{"injectHosts": []any{"keep.example.com"}}, envVars["OTHER_TOKEN"])
	require.NoError(t, ApplySandboxSettings(t.Context(), binary, home, os.Environ(), &stderr), stderr.String())
	unchanged, err := os.ReadFile(path)
	require.NoError(t, err)
	assert.Equal(t, data, unchanged)
}

func checkSandboxSettingsFile(t *testing.T, binary, settings string) error {
	t.Helper()
	home := t.TempDir()
	t.Setenv("COPILOT_HOME", home)
	require.NoError(t, os.WriteFile(filepath.Join(home, "settings.json"), []byte(settings), 0o600))
	return CheckSandboxCredentials(t.Context(), binary, t.TempDir(), os.Environ())
}

func TestApplySandboxSettingsAgainstTheFakeCLI(t *testing.T) {
	binary := copilottest.Binary(t)
	home := t.TempDir()
	t.Setenv("COPILOT_HOME", home)
	var stderr bytes.Buffer
	require.NoError(t, ApplySandboxSettings(t.Context(), binary, home, os.Environ(), &stderr), stderr.String())
	require.NoError(t, CheckSandboxCredentials(t.Context(), binary, home, os.Environ()))
}

// --- Command builder ---

func request(mutate func(*agent.AgentRequest)) agent.AgentRequest {
	req := agent.AgentRequest{
		Credentials: map[string]core.Secret{CredentialName: core.NewSecretString("copilot-secret")},
		ScratchDir:  filepath.Join("scratch", "run"),
	}
	if mutate != nil {
		mutate(&req)
	}
	return req
}

func TestCommandInteractiveKeepsTheLegacyArgumentList(t *testing.T) {
	cmd, err := New("copilot").Command(request(func(r *agent.AgentRequest) {
		r.DeniedPaths = []string{"keys"}
		r.DisabledMCPServers = []string{"github"}
		r.ExtraArgs = []string{"-p", "hello", "--resume", "--", "a b"}
	}))
	require.NoError(t, err)
	tree := filepath.Join("keys", "**")
	assert.Equal(t, []string{
		"--sandbox", "--disable-builtin-mcps", "--secret-env-vars=COPILOT_GITHUB_TOKEN",
		"--deny-tool=read(keys)", "--deny-tool=read(" + tree + ")",
		"--deny-tool=write(keys)", "--deny-tool=write(" + tree + ")",
		"--disable-mcp-server=github", "-p", "hello", "--resume", "--", "a b",
	}, cmd.Args)
	assert.Equal(t, "copilot", cmd.Path)
	assert.Equal(t, "copilot-secret", cmd.Env["COPILOT_GITHUB_TOKEN"])
	assert.Equal(t, "1", cmd.Env["COPILOT_DISABLE_LOGIN_SHELL_ENV"])
	assert.Equal(t, filepath.Join("scratch", "run", "instructions"), cmd.Env["COPILOT_CUSTOM_INSTRUCTIONS_DIRS"])
	assert.Equal(t, gitInstructions, cmd.Files["instructions/"+gitInstructionsName])
}

func TestCommandHeadlessFirstTurnAndResume(t *testing.T) {
	const id = "0b6d4c0a-27a1-4d0e-9a53-33ad2a4c7a10"
	first, err := New("copilot").Command(request(func(r *agent.AgentRequest) {
		r.Prompt = "implement it"
		r.SessionID = id
		r.Model = "example-model"
		r.Structured = true
		r.MaxCost = 30
		r.AllowTools = []string{"write", "shell(git:*)"}
		r.DenyTools = []string{"shell(rm:*)"}
		r.AvailableTools = []string{"view"}
		r.ExcludedTools = []string{"web"}
	}))
	require.NoError(t, err)
	assert.Equal(t, []string{
		"--sandbox", "--disable-builtin-mcps", "--secret-env-vars=COPILOT_GITHUB_TOKEN",
		"-p", "implement it", "-s", "--no-ask-user", "--no-auto-update", "--sync-session=off",
		"--model=example-model", "--session-id=" + id, "--output-format", "json", "--max-ai-credits=30",
		"--allow-tool=write", "--allow-tool=shell(git:*)", "--deny-tool=shell(rm:*)",
		"--available-tools=view", "--excluded-tools=web",
	}, first.Args)

	next, err := New("copilot").Command(request(func(r *agent.AgentRequest) {
		r.Prompt = "address review"
		r.SessionID = id
		r.Resume = true
	}))
	require.NoError(t, err)
	assert.Contains(t, next.Args, "--resume="+id)
	assert.NotContains(t, next.Args, "--session-id="+id, "--session-id cannot be combined with --resume")
	assert.Contains(t, next.Args, "--sandbox", "the full lockdown is rebuilt on every turn")
}

func TestCommandRefusesUnsafeSessionAndBudgetInput(t *testing.T) {
	tests := []struct {
		name   string
		mutate func(*agent.AgentRequest)
		want   string
	}{
		{"resume without ID", func(r *agent.AgentRequest) { r.Prompt = "x"; r.Resume = true }, "explicit session ID"},
		{"bare resume in headless passthrough", func(r *agent.AgentRequest) { r.Prompt = "x"; r.ExtraArgs = []string{"--resume"} }, "bare --resume"},
		{"empty resume value", func(r *agent.AgentRequest) { r.Prompt = "x"; r.ExtraArgs = []string{"--resume="} }, "bare --resume"},
		{"continue in headless passthrough", func(r *agent.AgentRequest) { r.Prompt = "x"; r.ExtraArgs = []string{"--continue"} }, "--continue"},
		{"budget below the documented minimum", func(r *agent.AgentRequest) { r.Prompt = "x"; r.MaxCost = 29 }, "at least 30"},
		{"missing credential", func(r *agent.AgentRequest) { r.Credentials = nil }, "credential is missing"},
		{"comma in scratch path", func(r *agent.AgentRequest) { r.ScratchDir = "a,b" }, "commas"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := New("copilot").Command(request(tt.mutate))
			require.ErrorContains(t, err, tt.want)
		})
	}

	// An explicit session after the terminator is data for the prompt, not a flag.
	_, err := New("copilot").Command(request(func(r *agent.AgentRequest) {
		r.Prompt = "x"
		r.ExtraArgs = []string{"--", "--resume"}
	}))
	require.NoError(t, err)
}

func TestCommandInheritedInstructionDirectoriesAreExtended(t *testing.T) {
	cmd, err := New("copilot").Command(request(func(r *agent.AgentRequest) {
		r.Environ = []string{"copilot_custom_instructions_dirs=/inherited"}
	}))
	require.NoError(t, err)
	assert.Equal(t, "/inherited,"+filepath.Join("scratch", "run", "instructions"), cmd.Env["COPILOT_CUSTOM_INSTRUCTIONS_DIRS"])
}

func TestCommandNeverPlacesTheCredentialInArguments(t *testing.T) {
	cmd, err := New("copilot").Command(request(func(r *agent.AgentRequest) { r.Prompt = "x" }))
	require.NoError(t, err)
	assert.NotContains(t, strings.Join(cmd.Args, " "), "copilot-secret")
	assert.NotContains(t, fmt.Sprintf("%+v", request(nil)), "copilot-secret")
}

func TestLaunchArgumentsCannotOverrideSandboxProtections(t *testing.T) {
	for _, arg := range []string{"--no-sandbox", "--sandbox=false", "--secret-env-vars=OTHER", "--secret-env-vars"} {
		require.Error(t, CheckLaunchArgs([]string{arg}), arg)
	}
	require.NoError(t, CheckLaunchArgs([]string{"--sandbox", "--sandbox=true", "--", "--no-sandbox"}))
}

func TestAdapterContract(t *testing.T) {
	a := New("copilot")
	assert.Equal(t, "copilot", a.Name())
	caps := a.Capabilities()
	assert.True(t, caps.CanResume)
	assert.True(t, caps.ChooseSessionID)
	assert.True(t, caps.HostSandbox)
	assert.Equal(t, "ai-credits", caps.CostUnit)
	assert.Equal(t, map[string]string{"copilot-token": "COPILOT_GITHUB_TOKEN"}, a.CredentialEnv())
	_, err := a.ParseResult(core.SessionResult{ExitCode: 0, Output: "done"})
	require.Error(t, err, "result parsing must fail closed until the sentinel contract exists")
	assert.True(t, errors.Is(err, errors.ErrUnsupported))
}

func TestPreflightChecksVersionAndPolicyAgainstTheFakeCLI(t *testing.T) {
	binary := copilottest.Binary(t)
	home := t.TempDir()
	t.Setenv("COPILOT_HOME", home)
	require.NoError(t, os.WriteFile(filepath.Join(home, "settings.json"), []byte(scopedSandboxSettings), 0o600))
	rt := core.RuntimeInfo{Name: "windows-sandbox", Dir: t.TempDir(), Environ: os.Environ(), Host: true}
	require.NoError(t, New(binary).Preflight(t.Context(), rt))

	require.NoError(t, os.WriteFile(filepath.Join(home, "settings.json"), []byte(`{"sandbox":{}}`), 0o600))
	require.ErrorContains(t, New(binary).Preflight(t.Context(), rt), "sandbox.auth.git")
	require.Error(t, New(filepath.Join(t.TempDir(), "missing")).Preflight(t.Context(), rt))
}

func TestCommandAppendsConfiguredArgumentsBeforePassthroughButStillGuardsTheSandbox(t *testing.T) {
	cmd, err := New("copilot").Command(request(func(r *agent.AgentRequest) {
		r.ConfigArgs = []string{"--yolo", "--effort=high"}
		r.ExtraArgs = []string{"-s"}
	}))
	require.NoError(t, err)
	assert.Equal(t, []string{"--sandbox", "--disable-builtin-mcps", "--secret-env-vars=COPILOT_GITHUB_TOKEN",
		"--yolo", "--effort=high", "-s"}, cmd.Args)

	for _, arg := range []string{"--no-sandbox", "--sandbox=false", "--secret-env-vars=X"} {
		_, err := New("copilot").Command(request(func(r *agent.AgentRequest) { r.ConfigArgs = []string{arg} }))
		require.ErrorContains(t, err, "configured agent arguments", arg)
	}
	_, err = New("copilot").Command(request(func(r *agent.AgentRequest) {
		r.Prompt = "x"
		r.ConfigArgs = []string{"--continue"}
	}))
	require.ErrorContains(t, err, "--continue")
}
