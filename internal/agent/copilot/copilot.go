// Package copilot adapts the GitHub Copilot CLI. It is the only package, besides the host sandbox
// runtime, that may know Copilot's flags, files and environment variables.
package copilot

import (
	"context"
	_ "embed"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/JanDeDobbeleer/mint/internal/agent"
	"github.com/JanDeDobbeleer/mint/internal/core"
	"github.com/JanDeDobbeleer/mint/internal/hostenv"
)

const (
	// CredentialName is the vault name of the Copilot credential.
	CredentialName = "copilot-token"
	// CredentialVar is the environment variable the Copilot CLI reads its credential from.
	CredentialVar = "COPILOT_GITHUB_TOKEN"

	// MinimumVersion is the oldest supported CLI; TestedVersion is the newest one Mint's flags were recorded against.
	MinimumVersion = "1.0.112"
	TestedVersion  = "1.0.114"

	// MinimumAICredits is the smallest value --max-ai-credits accepts.
	MinimumAICredits = 30

	gitInstructionsName = "mint-git.instructions.md"
)

//go:embed git-instructions.md
var gitInstructions []byte

// Agent is the Copilot CLI adapter.
type Agent struct{ binary string }

var _ agent.Agent = (*Agent)(nil)

// New returns an adapter for the Copilot executable at binary.
func New(binary string) *Agent { return &Agent{binary: binary} }

func (*Agent) Name() string { return "copilot" }

func (*Agent) Capabilities() agent.AgentCaps {
	return agent.AgentCaps{
		CanResume:       true,
		ChooseSessionID: true,
		HostSandbox:     true,
		Transport:       "prompt-flag",
		CostUnit:        "ai-credits",
	}
}

func (*Agent) CredentialEnv() map[string]string {
	return map[string]string{CredentialName: CredentialVar}
}

// RepoConfigPolicy lists the repository files whose presence can add credential routes to a session.
func (*Agent) RepoConfigPolicy() agent.RepoConfigPolicy {
	return agent.RepoConfigPolicy{
		LSPConfigFile: filepath.Join(".github", "lsp.json"),
		SettingsFiles: []string{
			filepath.Join(".github", "copilot", "settings.json"),
			filepath.Join(".github", "copilot", "settings.local.json"),
			filepath.Join(".claude", "settings.json"),
			filepath.Join(".claude", "settings.local.json"),
		},
		RejectedSettingKeys: []string{"sandbox", "extensions", "enabledPlugins", "extraKnownMarketplaces", "disableAllHooks", "enabledMcpServers"},
		MCPConfigFiles:      []string{".mcp.json", filepath.Join(".github", "mcp.json")},
	}
}

// Preflight checks the installed CLI version and the saved sandbox policy.
func (a *Agent) Preflight(ctx context.Context, rt core.RuntimeInfo) error {
	if err := checkCopilot(ctx, a.binary); err != nil {
		return err
	}
	return CheckSandboxCredentials(ctx, a.binary, rt.Dir, rt.Environ)
}

// ParseResult fails closed until the completion contract (exit status, final response and the
// result.json sentinel) is implemented with the flow engine; see design 5.2.
func (*Agent) ParseResult(core.SessionResult) (core.AgentOutcome, error) {
	return core.AgentOutcome{}, fmt.Errorf("copilot result parsing: %w", errors.ErrUnsupported)
}

// Command builds the full lockdown for one turn. Interactive use leaves Prompt empty and passes the
// operator's own arguments in ExtraArgs, which keeps the exact argument list of earlier releases.
func (a *Agent) Command(req agent.AgentRequest) (core.Command, error) {
	token := req.Credentials[CredentialName]
	if token.IsZero() {
		return core.Command{}, errors.New("the Copilot credential is missing")
	}
	headless := req.Prompt != ""
	if headless {
		if err := rejectSessionPicker(req.ExtraArgs); err != nil {
			return core.Command{}, err
		}
	}
	if req.Resume && req.SessionID == "" {
		return core.Command{}, errors.New("resuming requires an explicit session ID")
	}
	if req.SessionID != "" && strings.ContainsAny(req.SessionID, " \t\r\n\x00=,") {
		return core.Command{}, errors.New("invalid session ID")
	}
	if req.MaxCost != 0 && req.MaxCost < MinimumAICredits {
		return core.Command{}, fmt.Errorf("--max-ai-credits must be at least %d", MinimumAICredits)
	}
	instructionsDir := filepath.Join(req.ScratchDir, "instructions")
	if strings.Contains(instructionsDir, ",") {
		return core.Command{}, errors.New("temporary directory path for Copilot instructions must not contain commas")
	}

	args := []string{"--sandbox", "--disable-builtin-mcps", "--secret-env-vars=" + CredentialVar}
	for _, p := range req.DeniedPaths {
		for _, kind := range []string{"read", "write"} {
			args = append(args, "--deny-tool="+kind+"("+p+")", "--deny-tool="+kind+"("+filepath.Join(p, "**")+")")
		}
	}
	for _, name := range req.DisabledMCPServers {
		args = append(args, "--disable-mcp-server="+name)
	}
	if headless {
		args = append(args, "-p", req.Prompt, "-s", "--no-ask-user", "--no-auto-update", "--sync-session=off")
	}
	if req.Model != "" {
		args = append(args, "--model="+req.Model)
	}
	switch {
	case req.Resume:
		args = append(args, "--resume="+req.SessionID)
	case req.SessionID != "":
		args = append(args, "--session-id="+req.SessionID)
	}
	if req.Structured {
		args = append(args, "--output-format", "json")
	}
	if req.MaxCost != 0 {
		args = append(args, "--max-ai-credits="+strconv.Itoa(req.MaxCost))
	}
	for _, list := range []struct {
		flag  string
		items []string
	}{
		{"--allow-tool", req.AllowTools},
		{"--deny-tool", req.DenyTools},
		{"--available-tools", req.AvailableTools},
		{"--excluded-tools", req.ExcludedTools},
	} {
		for _, item := range list.items {
			args = append(args, list.flag+"="+item)
		}
	}
	args = append(args, req.ExtraArgs...)

	return core.Command{
		Path: a.binary,
		Args: args,
		Env: map[string]string{
			CredentialVar:                      token.Reveal(),
			"COPILOT_DISABLE_LOGIN_SHELL_ENV":  "1",
			"COPILOT_CUSTOM_INSTRUCTIONS_DIRS": sessionInstructionDirs(req.Environ, instructionsDir),
		},
		Files: map[string][]byte{path.Join("instructions", gitInstructionsName): gitInstructions},
	}, nil
}

func sessionInstructionDirs(environ []string, dir string) string {
	var inherited string
	for _, entry := range environ {
		name, value, found := strings.Cut(entry, "=")
		if found && strings.EqualFold(name, "COPILOT_CUSTOM_INSTRUCTIONS_DIRS") {
			inherited = value
		}
	}
	if inherited == "" {
		return dir
	}
	return inherited + "," + dir
}

// rejectSessionPicker refuses the forms of --resume and --continue that need a terminal picker or guess a session.
func rejectSessionPicker(args []string) error {
	for _, arg := range args {
		if arg == "--" {
			return nil
		}
		switch flag, value, hasValue := strings.Cut(arg, "="); flag {
		case "--continue":
			return errors.New("--continue is not allowed in headless runs; resume an explicit session ID")
		case "--resume":
			if !hasValue || value == "" {
				return errors.New("bare --resume is not allowed in headless runs; resume an explicit session ID")
			}
		}
	}
	return nil
}

// CheckLaunchArgs rejects operator-supplied arguments that would weaken Mint's sandbox protections.
func CheckLaunchArgs(args []string) error {
	for _, arg := range args {
		if arg == "--" {
			break
		}
		flag, _, _ := strings.Cut(arg, "=")
		switch flag {
		case "--no-sandbox", "--secret-env-vars":
			return fmt.Errorf("%s would override mint's sandbox protections", flag)
		case "--sandbox":
			if arg != flag && arg != "--sandbox=true" {
				return errors.New("sandboxing is required by mint")
			}
		}
	}
	return nil
}

var copilotVersion = regexp.MustCompile(`GitHub Copilot CLI (\d+)\.(\d+)\.(\d+)`)

func checkCopilot(ctx context.Context, binary string) error {
	probeCtx, cancel := context.WithTimeout(ctx, 15*time.Second)
	defer cancel()
	cmd := exec.CommandContext(probeCtx, binary, "--sandbox", "--version")
	cmd.Env = hostenv.Filter(os.Environ(), nil)
	output, err := cmd.Output()
	if err != nil {
		return errors.New("copilot must support --sandbox; install GitHub Copilot CLI " + MinimumVersion + " or later")
	}
	return validateCopilotVersion(string(output))
}

func validateCopilotVersion(output string) error {
	matches := copilotVersion.FindStringSubmatch(output)
	if len(matches) != 4 {
		return errors.New("cannot identify the GitHub Copilot CLI version")
	}
	major, _ := strconv.Atoi(matches[1])
	minor, _ := strconv.Atoi(matches[2])
	patch, _ := strconv.Atoi(matches[3])
	if major < 1 || (major == 1 && minor == 0 && patch < 112) {
		return errors.New("GitHub Copilot CLI " + MinimumVersion + " or later is required")
	}
	return nil
}

// ResolveCredential returns the credential Copilot authenticates with: COPILOT_GITHUB_TOKEN, else the GitHub CLI's token.
func ResolveCredential(ctx context.Context) (core.Secret, error) {
	if token := os.Getenv(CredentialVar); token != "" {
		if strings.ContainsAny(token, " \t\r\n\x00") {
			return core.Secret{}, errors.New("invalid COPILOT_GITHUB_TOKEN")
		}
		return core.NewSecretString(token), nil
	}
	gh, err := exec.LookPath("gh")
	if err != nil {
		return core.Secret{}, errors.New("GitHub CLI is required")
	}
	authCtx, cancel := context.WithTimeout(ctx, 15*time.Second)
	defer cancel()
	output, err := exec.CommandContext(authCtx, gh, "auth", "token", "--hostname", "github.com").Output()
	if err != nil {
		return core.Secret{}, errors.New("cannot obtain Copilot authentication; set COPILOT_GITHUB_TOKEN or authenticate with 'gh auth login'")
	}
	token := strings.TrimSpace(string(output))
	if token == "" || strings.ContainsAny(token, " \t\r\n\x00") {
		return core.Secret{}, errors.New("GitHub CLI did not return a valid Copilot authentication credential")
	}
	return core.NewSecretString(token), nil
}
