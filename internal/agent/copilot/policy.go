package copilot

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"slices"
	"time"

	"github.com/JanDeDobbeleer/mint/internal/hostenv"
)

var githubTokenHosts = [...]string{"api.github.com", "github.com", "uploads.github.com"}

// ApplySandboxSettings writes Mint's credential policy into the operator's saved Copilot settings.
func ApplySandboxSettings(ctx context.Context, binary, dir string, environ []string, stderr io.Writer) error {
	if CheckSandboxCredentials(ctx, binary, dir, environ) == nil {
		return nil
	}
	const hostsKey = "sandbox.credentials.envVars.GH_TOKEN.injectHosts"
	const gitHostsKey = "sandbox.credentials.envVars.MINT_GIT_AUTH.injectHosts"
	edits := [][]string{
		{"sandbox.allowBypass", "false"},
		{"sandbox.auth.git", "false"},
		{"sandbox.auth.gh", "false"},
		{"--rm", hostsKey},
		{"--rm", gitHostsKey},
		{gitHostsKey, "github.com"},
	}
	for _, host := range githubTokenHosts {
		edits = append(edits, []string{hostsKey, host})
	}
	if os.PathSeparator == '\\' {
		edits = append(edits, []string{"sandbox.userPolicy.network.allowLocalNetwork", "true"})
	}
	env := hostenv.Filter(environ, nil)
	for _, edit := range edits {
		editCtx, cancel := context.WithTimeout(ctx, 15*time.Second)
		cmd := exec.CommandContext(editCtx, binary, append([]string{"config", "--global"}, edit...)...)
		cmd.Dir = dir
		cmd.Env = env
		cmd.Stderr = stderr
		err := cmd.Run()
		cancel()
		if err != nil {
			return fmt.Errorf("cannot apply Copilot setting %q: %w", edit[0], err)
		}
	}
	return CheckSandboxCredentials(ctx, binary, dir, environ)
}

// CheckSandboxCredentials verifies the saved Copilot settings against Mint's credential policy.
func CheckSandboxCredentials(ctx context.Context, binary, root string, environ []string) error {
	probeCtx, cancel := context.WithTimeout(ctx, 15*time.Second)
	defer cancel()
	cmd := exec.CommandContext(probeCtx, binary, "config", "sandbox")
	cmd.Dir = root
	cmd.Env = hostenv.Filter(environ, nil)
	output, err := cmd.Output()
	var policy struct {
		Auth struct {
			Git *bool `json:"git"`
			GH  *bool `json:"gh"`
		} `json:"auth"`
		Credentials struct {
			EnvVars map[string]struct {
				InjectHosts []string `json:"injectHosts"`
			} `json:"envVars"`
		} `json:"credentials"`
		UserPolicy struct {
			Network struct {
				AllowLocalNetwork *bool `json:"allowLocalNetwork"`
			} `json:"network"`
		} `json:"userPolicy"`
		AllowBypass *bool `json:"allowBypass"`
	}
	if err != nil || json.Unmarshal(output, &policy) != nil ||
		policy.Auth.Git == nil || *policy.Auth.Git || policy.Auth.GH == nil || *policy.Auth.GH {
		return errors.New("saved Copilot settings must explicitly disable sandbox.auth.git and sandbox.auth.gh and configure scoped credential masking; run 'mint setup'")
	}
	if policy.AllowBypass == nil || *policy.AllowBypass {
		return errors.New("sandbox.allowBypass must be false in saved Copilot settings; run 'mint setup'")
	}
	if allow := policy.UserPolicy.Network.AllowLocalNetwork; os.PathSeparator == '\\' && allow != nil && !*allow {
		return errors.New("sandbox.userPolicy.network.allowLocalNetwork must not be disabled on Windows; run 'mint setup' to enable access to the credential proxy")
	}
	hosts := policy.Credentials.EnvVars["GH_TOKEN"].InjectHosts
	slices.Sort(hosts)
	if !slices.Equal(slices.Compact(hosts), githubTokenHosts[:]) {
		return errors.New("sandbox.credentials.envVars.GH_TOKEN.injectHosts must contain exactly github.com, api.github.com, and uploads.github.com; run 'mint setup'")
	}
	gitHosts := policy.Credentials.EnvVars["MINT_GIT_AUTH"].InjectHosts
	slices.Sort(gitHosts)
	if !slices.Equal(slices.Compact(gitHosts), []string{"github.com"}) {
		return errors.New("sandbox.credentials.envVars.MINT_GIT_AUTH.injectHosts must contain only github.com; run 'mint setup'")
	}
	return nil
}
