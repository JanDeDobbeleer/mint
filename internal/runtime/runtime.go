// Package runtime defines where an agent process runs: a container or the Windows host sandbox.
package runtime

import (
	"context"
	"io"

	"github.com/JanDeDobbeleer/mint/internal/agent"
	"github.com/JanDeDobbeleer/mint/internal/core"
)

// Runtime starts agent sessions.
type Runtime interface {
	Name() string
	// Preflight may reject an agent (for example an agent without a host sandbox on the host runtime).
	Preflight(ctx context.Context, p core.Policy, a core.AgentInfo) error
	Info(dir string, environ []string) core.RuntimeInfo
	Start(ctx context.Context, spec SessionSpec) (Session, error)
}

// Session is one prepared agent process.
type Session interface {
	// Wait runs the process to completion and releases the session's resources.
	Wait() error
	// Close releases resources without running; it is safe after Wait.
	Close() error
}

// SessionSpec describes one session to start.
type SessionSpec struct {
	// BeforeLaunch runs immediately before the process starts; an error aborts the launch.
	// GitHubToken is the repository token handed to the session (interactive mode only); Environ is the inherited environment to filter.
	Agent        agent.Agent
	Stdin        io.Reader
	Stdout       io.Writer
	Stderr       io.Writer
	BeforeLaunch func() error
	GitHubToken  core.Secret
	Dir          string
	Repo         string
	Environ      []string
	Request      agent.AgentRequest
}
