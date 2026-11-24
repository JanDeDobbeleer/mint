// Package config loads and validates the operator's mint.yaml.
//
// The file names things and sets policy; it never holds a secret. Credentials are referenced by their vault
// name. Decoding is strict: an unknown key is an error, so a typo can not silently drop a restriction.
package config

import "time"

// SupportedVersion is the only schema version this build understands.
const SupportedVersion = 1

// Config is the whole operator configuration.
type Config struct {
	Hooks          Hooks               `yaml:"hooks"`
	NetworkBundles map[string][]string `yaml:"network_bundles"`
	Sandboxes      map[string]Sandbox  `yaml:"sandboxes"`
	Agents         map[string]Agent    `yaml:"agents"`
	Projects       map[string]Project  `yaml:"projects"`
	Flows          map[string]Flow     `yaml:"flows"`
	Limits         Limits              `yaml:"limits"`
	Triggers       []Trigger           `yaml:"triggers"`
	Output         OutputPolicy        `yaml:"output"`
	Retry          Retry               `yaml:"retry"`
	StallTimeout   time.Duration       `yaml:"stall_timeout"`
	Version        int                 `yaml:"version"`
}

// Limits caps concurrency. Zero means unset (the defaults apply).
type Limits struct {
	PerProject        map[string]int `yaml:"per_project"`
	PerRuntime        map[string]int `yaml:"per_runtime"`
	MaxConcurrentRuns int            `yaml:"max_concurrent_runs"`
	PerRepo           int            `yaml:"per_repo"`
}

// Retry shapes the backoff min(base * 2^(attempt-1), max).
type Retry struct {
	BaseDelay   time.Duration `yaml:"base_delay"`
	MaxDelay    time.Duration `yaml:"max_delay"`
	MaxAttempts int           `yaml:"max_attempts"`
}

// Hook runs a command, without a shell, around a stage. Hooks are operator configuration and never come from a repo.
type Hook struct {
	Command []string      `yaml:"command"`
	Timeout time.Duration `yaml:"timeout"`
}

// Hooks are the lifecycle hook points of design 5.6.
type Hooks struct {
	BeforeStage  *Hook `yaml:"before_stage"`
	AfterStage   *Hook `yaml:"after_stage"`
	OnFail       *Hook `yaml:"on_fail"`
	OnDone       *Hook `yaml:"on_done"`
	BeforeRemove *Hook `yaml:"before_remove"`
}

// OutputPolicy decides which typed output requests Mint applies (design 5.7).
type OutputPolicy struct {
	Max            map[string]int `yaml:"max"`
	Targets        []string       `yaml:"targets"`
	LabelAllow     []string       `yaml:"label_allow"`
	LabelBlock     []string       `yaml:"label_block"`
	ProtectedPaths []string       `yaml:"protected_paths"`
	MaxPatchBytes  int            `yaml:"max_patch_bytes"`
}

// Sandbox is a named, generic sandbox definition. It is compiled into runtime and agent settings.
type Sandbox struct {
	Credentials map[string][]string `yaml:"credentials"`
	Filesystem  Filesystem          `yaml:"filesystem"`
	Network     Network             `yaml:"network"`
	Limits      SandboxLimits       `yaml:"limits"`
}

// Filesystem lists the paths a session may read or write.
type Filesystem struct {
	ReadOnly  []string `yaml:"readonly"`
	ReadWrite []string `yaml:"readwrite"`
}

// Network is the egress definition: named bundles plus explicit hosts. Blocked hosts override allowed ones.
type Network struct {
	Bundles      []string `yaml:"bundles"`
	AllowedHosts []string `yaml:"allowed_hosts"`
	BlockedHosts []string `yaml:"blocked_hosts"`
}

// SandboxLimits bound one session.
type SandboxLimits struct {
	CPUs         float64       `yaml:"cpus"`
	WallClock    time.Duration `yaml:"wall_clock"`
	StallTimeout time.Duration `yaml:"stall_timeout"`
	MemoryMB     int           `yaml:"memory_mb"`
	PIDs         int           `yaml:"pids"`
}

// Agent is a profile: adapter, model, prompt, sandbox and tool allowlist.
type Agent struct {
	Adapter    string   `yaml:"adapter"`
	Model      string   `yaml:"model"`
	Prompt     string   `yaml:"prompt"`
	Sandbox    string   `yaml:"sandbox"`
	Workspace  string   `yaml:"workspace"`
	Fallback   string   `yaml:"fallback"`
	Credential string   `yaml:"credential"`
	Tools      []string `yaml:"tools"`
	Args       []string `yaml:"args"`
	Resume     bool     `yaml:"resume"`
}

// Project binds a repository to a runtime, a sandbox, trusted senders and an integration strategy.
type Project struct {
	Output      *OutputPolicy `yaml:"output"`
	Repo        string        `yaml:"repo"`
	RunsOn      string        `yaml:"runs_on"`
	Sandbox     string        `yaml:"sandbox"`
	Senders     []Sender      `yaml:"senders"`
	Checks      []Check       `yaml:"checks"`
	Integration Integration   `yaml:"integration"`
	Staged      bool          `yaml:"staged"`
}

// Sender maps a verified identity to a trust tier.
type Sender struct {
	Match string `yaml:"match"`
	Trust string `yaml:"trust"`
}

// Check is a deterministic command Mint runs in the runtime after an agent stage.
type Check struct {
	Name    string   `yaml:"name"`
	Command []string `yaml:"command"`
}

// Integration is the per-project merge strategy. It is the only way Mint merges anything.
type Integration struct {
	Where     Where         `yaml:"where"`
	Strategy  string        `yaml:"strategy"`
	Method    string        `yaml:"method"`
	Schedule  string        `yaml:"schedule"`
	TZ        string        `yaml:"tz"`
	Approvers []string      `yaml:"approvers"`
	Interval  time.Duration `yaml:"interval"`
}

// Where filters the pull requests an integration strategy collects.
type Where struct {
	Base        string `yaml:"base"`
	NotLabeled  string `yaml:"not_labeled"`
	Draft       bool   `yaml:"draft"`
	ChecksGreen bool   `yaml:"checks_green"`
}

// Trigger routes an event to a project.
type Trigger struct {
	Source  string `yaml:"source"`
	Kind    string `yaml:"kind"`
	Label   string `yaml:"label"`
	Mention string `yaml:"mention"`
	To      string `yaml:"to"`
	Project string `yaml:"project"`
}

// Flow is a named graph of stages.
type Flow struct {
	Stages map[string]Stage `yaml:"stages"`
	On     FlowTrigger      `yaml:"on"`
}

// FlowTrigger says which event starts or resumes a Run of the flow.
type FlowTrigger struct {
	Source   string   `yaml:"source"`
	Kind     string   `yaml:"kind"`
	Label    string   `yaml:"label"`
	State    string   `yaml:"state"`
	Run      string   `yaml:"run"`
	Schedule string   `yaml:"schedule"`
	TZ       string   `yaml:"tz"`
	From     []string `yaml:"from"`
}

// Stage is one node: exactly one of Agent, Action or Wait.
type Stage struct {
	With    map[string]string `yaml:"with"`
	OnFail  *OnFail           `yaml:"on_fail"`
	Agent   string            `yaml:"agent"`
	Action  string            `yaml:"action"`
	Wait    string            `yaml:"wait"`
	Next    string            `yaml:"next"`
	Timeout time.Duration     `yaml:"timeout"`
}

// OnFail retries another stage a bounded number of times.
type OnFail struct {
	Retry  string `yaml:"retry"`
	Max    int    `yaml:"max"`
	Notify bool   `yaml:"notify"`
}
