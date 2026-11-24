// Package agent defines the contract between Mint and an agent CLI such as GitHub Copilot.
package agent

import (
	"context"

	"github.com/JanDeDobbeleer/mint/internal/core"
)

// Agent adapts one agent CLI. Nothing outside an adapter package may know the CLI's flags or files.
type Agent interface {
	Name() string
	// Preflight runs the version gate and policy checks. Any doubt returns an error (fail closed).
	Preflight(ctx context.Context, rt core.RuntimeInfo) error
	// Command rebuilds the full lockdown for one turn. It performs no I/O.
	Command(req AgentRequest) (core.Command, error)
	// CredentialEnv maps a credential name to the environment variable the agent reads it from.
	CredentialEnv() map[string]string
	Capabilities() AgentCaps
	ParseResult(out core.SessionResult) (core.AgentOutcome, error)
	// RepoConfigPolicy names the repository files and keys that must be rejected or neutralized.
	RepoConfigPolicy() RepoConfigPolicy
}

// AgentCaps declares what an agent supports so the engine never guesses.
type AgentCaps struct {
	Transport        string // "prompt-flag" | "acp"
	CostUnit         string // "ai-credits" | "usd"
	CanResume        bool
	ChooseSessionID  bool
	StructuredUsage  bool
	NeedsInputSignal bool
	HostSandbox      bool // the agent brings an OS sandbox that the host runtime relies on
}

// AgentRequest is everything an agent needs to build one turn. A non-empty Prompt selects headless mode.
type AgentRequest struct {
	// Credentials holds secrets by credential name; the adapter maps them to environment variables.
	Credentials map[string]core.Secret
	// ScratchDir is a private per-session directory created by the runtime.
	ScratchDir     string
	Prompt         string
	SessionID      string
	Model          string
	DenyTools      []string
	Environ        []string
	AllowTools     []string
	AvailableTools []string
	ExcludedTools  []string
	// DeniedPaths are paths the agent file tools must not read or write; DisabledMCPServers are repository MCP servers to switch off.
	DeniedPaths        []string
	DisabledMCPServers []string
	// ExtraArgs pass through unchanged after Mint own arguments (interactive use only).
	ExtraArgs []string
	Policy    core.Policy
	// MaxCost is the spend budget in the agent CostUnit; zero means no budget flag.
	MaxCost    int
	Resume     bool
	Structured bool
}

// RepoConfigPolicy lists repository-owned agent configuration that Mint inspects before launch.
type RepoConfigPolicy struct {
	// LSPConfigFile is a JSON file with an "lspServers" object; any enabled server is rejected.
	LSPConfigFile string
	// SettingsFiles are JSON settings files that must not contain RejectedSettingKeys.
	SettingsFiles       []string
	RejectedSettingKeys []string
	// MCPConfigFiles are JSON files with an "mcpServers" object; the named servers are disabled.
	MCPConfigFiles []string
}

// Info returns what a runtime may know about an agent.
func Info(a Agent) core.AgentInfo {
	return core.AgentInfo{Name: a.Name(), HostSandbox: a.Capabilities().HostSandbox}
}
