package mint

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"

	coreagent "github.com/JanDeDobbeleer/mint/internal/agent"
	"github.com/JanDeDobbeleer/mint/internal/agent/copilot"
	"github.com/JanDeDobbeleer/mint/internal/core"
	rt "github.com/JanDeDobbeleer/mint/internal/runtime"
	"github.com/JanDeDobbeleer/mint/internal/runtime/hostsandbox"
)

const help = `mint supplies a per-repository fine-grained GitHub token to sandboxed Copilot.

Usage:
  mint setup
  mint setup [options]
  mint [COPILOT ARGUMENTS...]

Setup options:
  --remote NAME       Git remote to scope against (default: origin)
  --copilot PATH      Trusted Copilot executable (default: resolved from PATH)

On the first launch in each repository, mint prompts you to create a fine-grained
personal access token restricted to that repository. The token is validated,
encrypted outside the checkout, and reused for later sessions. Mint does not
revoke it when a session ends.

Each launch uses your usual Copilot home and requests the local sandbox.
Setup configures its saved policy to disable automatic Git and gh authentication
and bypass, and mask Mint's credentials. Run 'mint setup' to reapply those
settings. Launches check the policy without changing it and load temporary
instructions for authenticated Git HTTPS commands.
mint uses temporary GitHub CLI configuration and filters COPILOT_GITHUB_TOKEN
from shell and MCP environments.
The environment allowlist drops inherited GitHub tokens and other unapproved variables.
mint disables built-in and discovered project MCP servers, and rejects conflicting
project settings and active project language servers. Your own Copilot configuration
still applies. Built-in file tools are denied access to the Mint configuration
and any legacy key directory; configure OS sandbox denies separately.

Copilot arguments are forwarded unchanged, except flags that disable sandboxing
or override secret filtering are rejected. Only pass arguments you trust.
The Copilot host process runs outside its local command sandbox. mint scopes the
token it supplies; it is not an isolation boundary.
`

func Run(ctx context.Context, args []string, _ string, stdin io.Reader, stdout, stderr io.Writer) error {
	prompts := terminalPrompter{stdin: stdin, stderr: stderr}
	if len(args) > 0 && args[0] == "setup" {
		return setup(ctx, args[1:], stdout, stderr)
	}
	if err := copilot.CheckLaunchArgs(args); err != nil {
		return err
	}
	if home := os.Getenv("COPILOT_HOME"); home != "" && !filepath.IsAbs(home) {
		return errors.New("inherited COPILOT_HOME must be absolute")
	}
	dir, err := configDirectory()
	if err != nil {
		return err
	}
	cfg, err := readConfig(dir)
	if err != nil {
		return err
	}
	repo, err := discoverRepository(ctx, cfg.Remote)
	if err != nil {
		return err
	}
	var (
		cop  = copilot.New(cfg.Copilot)
		host = hostsandbox.Runtime{}
	)
	if _, err := hostsandbox.ProjectConfiguration(repo.Root, cop.RepoConfigPolicy()); err != nil {
		return err
	}
	denied, err := protectPaths(repo, dir, cfg)
	if err != nil {
		return err
	}
	if within(repo.Root, cfg.Copilot) {
		return errors.New("copilot executable must be outside the working tree")
	}
	environ := os.Environ()
	if err := cop.Preflight(ctx, host.Info(repo.Root, environ)); err != nil {
		return err
	}
	if err := host.Preflight(ctx, core.Policy{}, coreagent.Info(cop)); err != nil {
		return err
	}
	credential, err := copilot.ResolveCredential(ctx)
	if err != nil {
		return err
	}
	defer credential.Destroy()
	client := newGitHubClient()
	gitToken, err := repositoryToken(ctx, client, dir, repo, prompts, stderr)
	if err != nil {
		return err
	}
	token := core.NewSecretString(gitToken)
	defer token.Destroy()

	started, err := host.Start(ctx, rt.SessionSpec{
		Agent: cop,
		Request: coreagent.AgentRequest{
			Credentials: map[string]core.Secret{copilot.CredentialName: credential},
			DeniedPaths: denied,
			ExtraArgs:   args,
		},
		Stdin:  stdin,
		Stdout: stdout,
		Stderr: stderr,
		BeforeLaunch: func() error {
			if err := newTerminalUI(stderr).status("+", "Token validated. Launching Copilot (sandbox requested).\n"); err != nil {
				return errors.New("cannot report launch status")
			}
			return nil
		},
		Dir:         repo.Root,
		Repo:        repo.Owner + "/" + repo.Name,
		GitHubToken: token,
		Environ:     environ,
	})
	if err != nil {
		return err
	}
	defer func() { _ = started.Close() }()
	return started.Wait()
}

func setup(ctx context.Context, args []string, stdout, stderr io.Writer) error {
	flags := flag.NewFlagSet("mint setup", flag.ContinueOnError)
	flags.SetOutput(stderr)
	remote := flags.String("remote", "origin", "Git remote")
	copilot := flags.String("copilot", "", "trusted Copilot executable")
	flags.Usage = func() { _, _ = fmt.Fprint(stderr, help) }
	if err := flags.Parse(args); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			return nil
		}
		return errors.New("invalid setup arguments; run 'mint setup --help'")
	}
	if home := os.Getenv("COPILOT_HOME"); home != "" && !filepath.IsAbs(home) {
		return errors.New("inherited COPILOT_HOME must be absolute")
	}
	if len(args) == 0 {
		return configureSetup(ctx, stdout, stderr)
	}
	if flags.NArg() != 0 {
		return errors.New("unexpected setup arguments; run 'mint setup --help'")
	}
	dir, err := configDirectory()
	if err != nil {
		return err
	}
	if _, err := os.Lstat(filepath.Join(dir, "config.json")); err == nil {
		return errors.New("existing configuration is never overwritten by setup; run 'mint setup' to reapply policy")
	} else if !errors.Is(err, os.ErrNotExist) {
		return errors.New("cannot inspect the existing configuration")
	}
	if *copilot == "" {
		*copilot, err = exec.LookPath("copilot")
		if err != nil {
			return errors.New("copilot is required on PATH, or supply --copilot with a trusted executable path")
		}
	}
	binary, err := canonicalPath(*copilot)
	if err != nil {
		return err
	}
	return initializeSetup(ctx, *remote, binary, stdout, stderr)
}

func configureSetup(ctx context.Context, stdout, stderr io.Writer) error {
	dir, err := configDirectory()
	if err != nil {
		return err
	}
	if _, err := os.Lstat(filepath.Join(dir, "config.json")); errors.Is(err, os.ErrNotExist) {
		binary, lookupErr := exec.LookPath("copilot")
		if lookupErr != nil {
			return errors.New("copilot is required on PATH, or supply 'mint setup --copilot PATH'")
		}
		canonical, canonicalErr := canonicalPath(binary)
		if canonicalErr != nil {
			return canonicalErr
		}
		return initializeSetup(ctx, "origin", canonical, stdout, stderr)
	} else if err != nil {
		return errors.New("cannot inspect the existing configuration")
	}
	cfg, err := readConfig(dir)
	if err != nil {
		return err
	}
	ui := newTerminalUI(stdout)
	if err := ui.heading("Setup"); err != nil {
		return errors.New("cannot show setup details")
	}
	if err := copilot.ApplySandboxSettings(ctx, cfg.Copilot, dir, os.Environ(), stderr); err != nil {
		return err
	}
	return ui.status("+", "Configured Copilot credential policy in your existing settings. Tool and cache grants are unchanged.")
}

func initializeSetup(ctx context.Context, remote, binary string, stdout, stderr io.Writer) error {
	ui := newTerminalUI(stdout)
	if err := ui.heading("Setup"); err != nil {
		return errors.New("cannot show setup details")
	}
	dir, err := configDirectory()
	if err != nil {
		return err
	}
	cfg := config{Copilot: binary, Remote: remote}
	if err := cfg.validate(); err != nil {
		return err
	}
	repo, err := discoverRepository(ctx, cfg.Remote)
	if err != nil {
		return err
	}
	if within(repo.Root, cfg.Copilot) {
		return errors.New("copilot executable must be outside the working tree")
	}
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return errors.New("cannot create the Mint configuration directory")
	}
	if _, err := protectPaths(repo, dir, cfg); err != nil {
		return err
	}
	if err := copilot.ApplySandboxSettings(ctx, cfg.Copilot, dir, os.Environ(), stderr); err != nil {
		return err
	}
	if err := writeConfig(dir, cfg); err != nil {
		return err
	}
	if err := ui.status("+", "Configured Mint. Copilot credential policy is ready."); err != nil {
		return err
	}
	return ui.status(">", "Run 'mint' from a repository checkout to add its encrypted token.")
}
