package config

import (
	"regexp"
	"slices"
	"sort"
	"strings"
)

// DefaultBundles are the named egress bundles. They are starting points, not verified lists: the operator
// overrides or extends them under network_bundles, and the proxy request log (design 7.3) is how they are tuned.
// The copilot bundle in particular is UNVERIFIED until the Docker proxy tests run against real Copilot traffic.
var DefaultBundles = map[string][]string{
	"github":  {"github.com", "api.github.com", "uploads.github.com", "objects.githubusercontent.com", "raw.githubusercontent.com"},
	"go":      {"proxy.golang.org", "sum.golang.org", "storage.googleapis.com"},
	"node":    {"registry.npmjs.org"},
	"python":  {"pypi.org", "files.pythonhosted.org"},
	"copilot": {"api.githubcopilot.com", "api.individual.githubcopilot.com", "api.business.githubcopilot.com", "copilot-proxy.githubusercontent.com"},
}

var hostPattern = regexp.MustCompile(`^(\*\.)?[a-z0-9]([a-z0-9-]*[a-z0-9])?(\.[a-z0-9]([a-z0-9-]*[a-z0-9])?)+$`)

// ValidHost accepts a lower-case DNS name, optionally with a leading "*." that also matches subdomains.
// Schemes, paths, ports and bare IPs are rejected: the allowlist works on names.
func ValidHost(host string) bool {
	if !hostPattern.MatchString(host) {
		return false
	}
	// Reject dotted-quad IPs, which the pattern would otherwise accept as names.
	last := host[strings.LastIndex(host, ".")+1:]
	return strings.Trim(last, "0123456789") != ""
}

// HostMatches reports whether pattern covers host. "*.example.com" matches example.com and any subdomain.
func HostMatches(pattern, host string) bool {
	if rest, ok := strings.CutPrefix(pattern, "*."); ok {
		return host == rest || strings.HasSuffix(host, "."+rest)
	}
	return pattern == host
}

// bundle returns the hosts of a named bundle: the operator's definition wins over the default.
func (c *Config) bundle(name string) ([]string, bool) {
	if hosts, ok := c.NetworkBundles[name]; ok {
		return hosts, true
	}
	hosts, ok := DefaultBundles[name]
	return hosts, ok
}

// EffectiveNetwork resolves bundles and applies "blocked overrides allowed". It returns the sorted allowed hosts
// with every host covered by a blocked pattern removed, and the sorted blocked patterns, which an enforcer must
// still check first because a blocked "*.example.com" also covers hosts added later.
func (c *Config) EffectiveNetwork(n Network) (allowed, blocked []string) {
	set := map[string]bool{}
	for _, name := range n.Bundles {
		hosts, _ := c.bundle(name)
		for _, h := range hosts {
			set[h] = true
		}
	}
	for _, h := range n.AllowedHosts {
		set[h] = true
	}
	for h := range set {
		covered := slices.ContainsFunc(n.BlockedHosts, func(b string) bool { return HostMatches(b, strings.TrimPrefix(h, "*.")) })
		if !covered {
			allowed = append(allowed, h)
		}
	}
	sort.Strings(allowed)
	blocked = slices.Clone(n.BlockedHosts)
	sort.Strings(blocked)
	return allowed, blocked
}
