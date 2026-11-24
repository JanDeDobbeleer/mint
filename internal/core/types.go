package core

import (
	"errors"
	"fmt"
	"time"
)

// Identity is a verified principal such as "github:login" or "email:a@b.c".
type Identity struct{ Scheme, ID, Display string }

// ThreadRef addresses a conversation in a source.
type ThreadRef struct{ Source, Key string }

// Message is an outbound reply.
type Message struct {
	Attrs    map[string]string
	Text     string
	Template string
}

// Event is one unit of inbound work. Body is untrusted.
type Event struct {
	Attrs    map[string]string
	ID       string // globally unique per source; the dedupe key
	Source   string // "github", "email", "whatsapp", "cron"
	Kind     string // "issue", "pr_review", "pr_comment", "message", "tick", "ci_completed", "run_failed"
	Subject  string
	Body     string
	Sender   Identity
	Thread   ThreadRef
	Verified bool
}

// Outcome selects the next edge of a flow stage.
type Outcome string

const (
	OutcomeDone       Outcome = "done"
	OutcomeFail       Outcome = "fail"
	OutcomeRetry      Outcome = "retry"
	OutcomeNeedsInput Outcome = "needs_input"
	OutcomeStalled    Outcome = "stalled"
)

// FSPolicy lists filesystem paths a session may read or write.
type FSPolicy struct {
	ReadOnly  []string
	ReadWrite []string
}

// NetPolicy is a generic egress definition.
type NetPolicy struct {
	Bundles      []string // named bundles: github, go, node, python, copilot
	AllowedHosts []string
	BlockedHosts []string // override allowed
}

// Limits bounds a session.
type Limits struct {
	CPUs         float64
	MemoryBytes  int64
	PIDs         int
	WallClock    time.Duration
	StallTimeout time.Duration
}

// Policy is the generic sandbox definition that each runtime and agent compiles into its own settings.
type Policy struct {
	Credentials map[string][]string // credential name -> hosts it may be injected for
	Filesystem  FSPolicy
	Network     NetPolicy
	Limits      Limits
}

// AgentInfo is what a runtime may know about an agent when it decides whether to host it.
type AgentInfo struct {
	Name        string
	HostSandbox bool // the agent brings its own OS sandbox that the host runtime relies on
}

// RuntimeInfo is what an agent may know about the runtime that hosts it.
type RuntimeInfo struct {
	Name string
	// Dir is the workspace directory the agent will run in.
	Dir string
	// Environ is the inherited environment the runtime forwards from.
	Environ []string
	Host    bool // the runtime runs directly on the operator's host
}

// Command describes the process a runtime must start.
type Command struct {
	Env   map[string]string // overrides on top of the runtime's filtered environment
	Files map[string][]byte // files to write under the session scratch directory (slash-separated relative paths)
	Path  string
	Dir   string
	Args  []string
}

// SessionResult is the raw outcome of one agent process.
type SessionResult struct {
	Output   string
	ExitCode int
}

// AgentOutcome is the interpreted result of one agent turn.
type AgentOutcome struct {
	Summary string
	Status  Outcome
}

var (
	// ErrWindowClosed means a reply was attempted outside a messaging window.
	ErrWindowClosed = errors.New("messaging window closed")
	// ErrUnknownChecker means a checker could not give a verdict; it must never count as clean.
	ErrUnknownChecker = errors.New("unknown checker result")
)

// ErrRateLimited reports a rate limit and when to retry.
type ErrRateLimited struct{ RetryAfter time.Duration }

func (e ErrRateLimited) Error() string {
	return fmt.Sprintf("rate limited; retry after %s", e.RetryAfter)
}
