package config

import (
	"fmt"
	"time"

	"github.com/JanDeDobbeleer/mint/internal/core"
)

// Policy turns a named sandbox profile into the generic core.Policy that runtimes and agents compile.
// Bundles are resolved and blocked hosts already override allowed ones.
func (c *Config) Policy(sandbox string) (core.Policy, error) {
	s, ok := c.Sandboxes[sandbox]
	if !ok {
		return core.Policy{}, fmt.Errorf("unknown sandbox profile %q", sandbox)
	}
	allowed, blocked := c.EffectiveNetwork(s.Network)
	p := core.Policy{
		Filesystem: core.FSPolicy{ReadOnly: s.Filesystem.ReadOnly, ReadWrite: s.Filesystem.ReadWrite},
		Network:    core.NetPolicy{Bundles: s.Network.Bundles, AllowedHosts: allowed, BlockedHosts: blocked},
		Limits: core.Limits{
			CPUs:         s.Limits.CPUs,
			MemoryBytes:  int64(s.Limits.MemoryMB) << 20,
			PIDs:         s.Limits.PIDs,
			WallClock:    s.Limits.WallClock,
			StallTimeout: s.Limits.StallTimeout,
		},
	}
	if p.Limits.StallTimeout == 0 {
		p.Limits.StallTimeout = c.StallTimeout
	}
	if len(s.Credentials) > 0 {
		p.Credentials = make(map[string][]string, len(s.Credentials))
		for name, hosts := range s.Credentials {
			p.Credentials[name] = append([]string(nil), hosts...)
		}
	}
	return p, nil
}

// Backoff is min(base * 2^(attempt-1), max) for attempt 1, 2, ... (design 5.6).
func (r Retry) Backoff(attempt int) time.Duration {
	if attempt < 1 {
		attempt = 1
	}
	d := r.BaseDelay
	for i := 1; i < attempt && d < r.MaxDelay; i++ {
		d *= 2
	}
	return min(d, r.MaxDelay)
}
