// Package hostsandbox runs an agent directly on the operator's host, relying on the agent's own
// OS sandbox (Copilot's). It is the runtime behind the interactive launcher. The host process is not
// a strong boundary: it filters the environment, isolates GitHub CLI and Git configuration, and
// disables repository-defined MCP servers, but trust gates belong to the caller.
package hostsandbox

import (
	"context"
	"encoding/base64"
	"errors"
	"fmt"
	"io"
	"maps"
	"os"
	"os/exec"
	"path/filepath"
	"sync"

	"github.com/JanDeDobbeleer/mint/internal/core"
	"github.com/JanDeDobbeleer/mint/internal/hostenv"
	"github.com/JanDeDobbeleer/mint/internal/runtime"
)

// Runtime is the Windows host sandbox runtime. Execute defaults to (*exec.Cmd).Run and exists so tests can replace process start.
type Runtime struct {
	Execute func(*exec.Cmd) error
}

var _ runtime.Runtime = Runtime{}

func (Runtime) Name() string { return "windows-sandbox" }

// Preflight refuses agents that bring no sandbox of their own: the host is not a boundary.
func (Runtime) Preflight(_ context.Context, _ core.Policy, a core.AgentInfo) error {
	if !a.HostSandbox {
		return fmt.Errorf("the host sandbox runtime supports only agents with their own OS sandbox, not %q", a.Name)
	}
	return nil
}

func (Runtime) Info(dir string, environ []string) core.RuntimeInfo {
	return core.RuntimeInfo{Name: "windows-sandbox", Dir: dir, Environ: environ, Host: true}
}

// Start prepares an isolated session directory and the filtered child process.
func (r Runtime) Start(ctx context.Context, spec runtime.SessionSpec) (runtime.Session, error) {
	servers, err := ProjectConfiguration(spec.Dir, spec.Agent.RepoConfigPolicy())
	if err != nil {
		return nil, err
	}
	scratch, err := os.MkdirTemp("", "mint-gh-")
	if err != nil {
		return nil, errors.New("cannot create an isolated GitHub CLI configuration directory")
	}
	s := &session{scratch: scratch, stderr: spec.Stderr, before: spec.BeforeLaunch, execute: r.Execute}
	if s.execute == nil {
		s.execute = (*exec.Cmd).Run
	}
	cmd, err := s.prepare(ctx, spec, servers)
	if err != nil {
		s.cleanup()
		return nil, err
	}
	s.cmd = cmd
	return s, nil
}

type session struct {
	execute func(*exec.Cmd) error
	stderr  io.Writer
	before  func() error
	cmd     *exec.Cmd
	scratch string
	once    sync.Once
}

func (s *session) prepare(ctx context.Context, spec runtime.SessionSpec, servers []string) (*exec.Cmd, error) {
	if err := os.WriteFile(filepath.Join(s.scratch, "gitconfig"), nil, 0o600); err != nil {
		return nil, errors.New("cannot create an isolated Git configuration")
	}
	req := spec.Request
	req.ScratchDir = s.scratch
	req.Environ = spec.Environ
	req.DisabledMCPServers = servers
	command, err := spec.Agent.Command(req)
	if err != nil {
		return nil, err
	}
	if err := writeFiles(s.scratch, command.Files); err != nil {
		return nil, err
	}

	overrides := map[string]string{
		"GIT_CONFIG_COUNT":   "1",
		"GIT_CONFIG_KEY_0":   "credential.helper",
		"GIT_CONFIG_VALUE_0": "",
	}
	maps.Copy(overrides, command.Env)
	if token := spec.GitHubToken.Reveal(); token != "" {
		overrides["GH_TOKEN"] = token
		overrides["MINT_GIT_AUTH"] = base64.StdEncoding.EncodeToString([]byte("x-access-token:" + token))
	}
	overrides["GH_CONFIG_DIR"] = s.scratch
	overrides["GH_HOST"] = "github.com"
	if spec.Repo != "" {
		overrides["GH_REPO"] = spec.Repo
	}
	overrides["GH_PROMPT_DISABLED"] = "1"
	overrides["GIT_CONFIG_GLOBAL"] = filepath.Join(s.scratch, "gitconfig")
	overrides["GIT_CONFIG_NOSYSTEM"] = "1"
	overrides["GIT_TERMINAL_PROMPT"] = "0"

	cmd := exec.CommandContext(ctx, command.Path, command.Args...)
	cmd.Dir = spec.Dir
	cmd.Env = hostenv.Filter(spec.Environ, overrides)
	cmd.Stdin = spec.Stdin
	cmd.Stdout = spec.Stdout
	cmd.Stderr = spec.Stderr
	return cmd, nil
}

// writeFiles writes the agent's session files below dir. Names are slash-separated and must stay inside dir.
func writeFiles(dir string, files map[string][]byte) error {
	for name, data := range files {
		local := filepath.Join(dir, filepath.FromSlash(name))
		if !filepath.IsLocal(filepath.FromSlash(name)) {
			return errors.New("invalid session file path")
		}
		if err := os.MkdirAll(filepath.Dir(local), 0o700); err != nil {
			return errors.New("cannot create a session instruction directory")
		}
		if err := os.WriteFile(local, data, 0o600); err != nil {
			return errors.New("cannot create session Git instructions")
		}
	}
	return nil
}

func (s *session) Wait() error {
	defer s.cleanup()
	if s.before != nil {
		if err := s.before(); err != nil {
			return err
		}
	}
	return s.execute(s.cmd)
}

func (s *session) Close() error {
	s.cleanup()
	return nil
}

func (s *session) cleanup() {
	s.once.Do(func() {
		if err := os.RemoveAll(s.scratch); err != nil && s.stderr != nil {
			_, _ = fmt.Fprintln(s.stderr, "mint: could not remove an isolated session directory")
		}
	})
}
