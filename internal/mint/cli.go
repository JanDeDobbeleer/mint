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
	"strings"
	"time"
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
	if err := checkLaunchArgs(args); err != nil {
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
	if _, err := projectConfiguration(repo.Root); err != nil {
		return err
	}
	denied, err := protectPaths(repo, dir, cfg)
	if err != nil {
		return err
	}
	if within(repo.Root, cfg.Copilot) {
		return errors.New("copilot executable must be outside the working tree")
	}
	if err := checkCopilot(ctx, cfg.Copilot); err != nil {
		return err
	}
	environ := os.Environ()
	if err := checkSandboxCredentials(ctx, cfg.Copilot, repo.Root, environ); err != nil {
		return err
	}
	credential, err := copilotCredential(ctx)
	if err != nil {
		return err
	}
	client := newGitHubClient()
	gitToken, err := repositoryToken(ctx, client, dir, repo, prompts, stderr)
	if err != nil {
		return err
	}

	return (session{
		execute:           (*exec.Cmd).Run,
		stdin:             stdin,
		stdout:            stdout,
		stderr:            stderr,
		cfg:               cfg,
		repo:              repo,
		copilotCredential: credential,
		args:              args,
		denied:            denied,
		environ:           environ,
	}).runWithToken(ctx, gitToken)
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
	if err := applySandboxSettings(ctx, cfg.Copilot, dir, os.Environ(), stderr); err != nil {
		return err
	}
	_, err = fmt.Fprintln(stdout, "Configured Copilot credential policy in your existing settings. Tool and cache grants are unchanged.")
	return err
}

func initializeSetup(ctx context.Context, remote, binary string, stdout, stderr io.Writer) error {
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
	if err := applySandboxSettings(ctx, cfg.Copilot, dir, os.Environ(), stderr); err != nil {
		return err
	}
	if err := writeConfig(dir, cfg); err != nil {
		return err
	}
	_, err = fmt.Fprintln(stdout, "Configured Mint. Run 'mint' from a repository checkout to add its encrypted token.")
	return err
}

func copilotCredential(ctx context.Context) (string, error) {
	if token := os.Getenv("COPILOT_GITHUB_TOKEN"); token != "" {
		if strings.ContainsAny(token, " \t\r\n\x00") {
			return "", errors.New("invalid COPILOT_GITHUB_TOKEN")
		}
		return token, nil
	}
	gh, err := exec.LookPath("gh")
	if err != nil {
		return "", errors.New("GitHub CLI is required")
	}
	authCtx, cancel := context.WithTimeout(ctx, 15*time.Second)
	defer cancel()
	command := exec.CommandContext(authCtx, gh, "auth", "token", "--hostname", "github.com")
	output, err := command.Output()
	if err != nil {
		return "", errors.New("cannot obtain Copilot authentication; set COPILOT_GITHUB_TOKEN or authenticate with 'gh auth login'")
	}
	token := strings.TrimSpace(string(output))
	if token == "" || strings.ContainsAny(token, " \t\r\n\x00") {
		return "", errors.New("GitHub CLI did not return a valid Copilot authentication credential")
	}
	return token, nil
}
