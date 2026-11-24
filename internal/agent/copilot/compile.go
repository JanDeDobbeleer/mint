package copilot

import (
	"errors"
	"fmt"
	"slices"
	"strconv"
	"strings"

	"github.com/JanDeDobbeleer/mint/internal/core"
)

// CompileOptions steer the policy compiler.
type CompileOptions struct {
	// Version is the installed CLI version ("1.0.114"). Keys that are not in the official documentation are
	// emitted only when it is known and at least AllowLocalNetworkSince.
	Version string
	// AllowLocalNetwork asks for sandbox.userPolicy.network.allowLocalNetwork (needed on Windows for the credential proxy).
	AllowLocalNetwork bool
}

// AllowLocalNetworkSince is the first version the setup flow has used allowLocalNetwork with. The key is not in the
// official pages read for the design [U]; Mint's own `mint setup` already depends on it from MinimumVersion on.
const AllowLocalNetworkSince = MinimumVersion

// CompilePolicy turns the generic policy into the settings.json fragment for one session. It emits only keys the
// documentation names (design 7.2): sandbox.enabled, sandbox.userPolicy.network.allowedHosts and blockedHosts, and
// sandbox.credentials.envVars.<NAME>.injectHosts. Anything it cannot express is an error, never silently dropped:
// built-in file tools are not enforced by the OS sandbox, so a filesystem policy has to be expressed as tool patterns.
func CompilePolicy(p core.Policy, opts CompileOptions) (map[string]any, error) {
	if len(p.Filesystem.ReadOnly)+len(p.Filesystem.ReadWrite) > 0 {
		return nil, errors.New("copilot cannot compile a filesystem policy: its path keys are not documented and the OS sandbox does not cover built-in file tools; express it with allow-tool and deny-tool patterns")
	}
	network := map[string]any{}
	if len(p.Network.AllowedHosts) > 0 {
		network["allowedHosts"] = slices.Sorted(slices.Values(p.Network.AllowedHosts))
	}
	if len(p.Network.BlockedHosts) > 0 {
		network["blockedHosts"] = slices.Sorted(slices.Values(p.Network.BlockedHosts))
	}
	if opts.AllowLocalNetwork {
		if !versionAtLeast(opts.Version, AllowLocalNetworkSince) {
			return nil, fmt.Errorf("allowLocalNetwork needs Copilot CLI %s or later (installed: %q)", AllowLocalNetworkSince, opts.Version)
		}
		network["allowLocalNetwork"] = true
	}

	sandbox := map[string]any{"enabled": true}
	if len(network) > 0 {
		sandbox["userPolicy"] = map[string]any{"network": network}
	}
	if len(p.Credentials) > 0 {
		vars := map[string]any{}
		env := (&Agent{}).CredentialEnv()
		for name, hosts := range p.Credentials {
			variable, ok := env[name]
			if !ok {
				return nil, fmt.Errorf("copilot does not read the credential %q", name)
			}
			vars[variable] = map[string]any{"injectHosts": slices.Sorted(slices.Values(hosts))}
		}
		sandbox["credentials"] = map[string]any{"envVars": vars}
	}
	return map[string]any{"sandbox": sandbox}, nil
}

// versionAtLeast compares dotted numeric versions. An unparsable or empty version is never "at least".
func versionAtLeast(have, want string) bool {
	h, ok1 := parseVersion(have)
	w, ok2 := parseVersion(want)
	if !ok1 || !ok2 {
		return false
	}
	for i := range 3 {
		if h[i] != w[i] {
			return h[i] > w[i]
		}
	}
	return true
}

func parseVersion(s string) ([3]int, bool) {
	var v [3]int
	parts := strings.Split(strings.TrimPrefix(strings.TrimSpace(s), "v"), ".")
	if len(parts) != 3 {
		return v, false
	}
	for i, part := range parts {
		n, err := strconv.Atoi(part)
		if err != nil || n < 0 {
			return v, false
		}
		v[i] = n
	}
	return v, true
}
