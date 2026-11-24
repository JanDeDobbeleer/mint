package docker

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/JanDeDobbeleer/mint/internal/core"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestCompileAppliesTheHardeningFloorToAnEmptyPolicy(t *testing.T) {
	spec, err := Compile(core.Policy{})
	require.NoError(t, err)
	assert.Equal(t, []string{
		"--cap-drop=ALL", "--security-opt=no-new-privileges", "--read-only",
		"--pids-limit=512", "--memory=4096m", "--tmpfs=/tmp:rw,noexec,nosuid,size=256m",
	}, spec.Args)
}

func TestCompileGolden(t *testing.T) {
	spec, err := Compile(core.Policy{
		Network:    core.NetPolicy{AllowedHosts: []string{"github.com", "api.github.com"}, BlockedHosts: []string{"uploads.github.com"}},
		Filesystem: core.FSPolicy{ReadWrite: []string{"/var/cache/build", "/home/agent/.cache"}},
		Limits:     core.Limits{CPUs: 1.5, MemoryBytes: 2048 << 20, PIDs: 128, WallClock: 45 * time.Minute, StallTimeout: 5 * time.Minute},
	})
	require.NoError(t, err)
	got := strings.Join(spec.Args, "\n") + "\n--\n" + strings.Join(spec.ProxyAllowedHosts, ",") + "\n" +
		strings.Join(spec.ProxyBlockedHosts, ",") + "\n" + spec.WallClock.String() + "\n" + spec.StallTimeout.String() + "\n"
	path := filepath.Join("testdata", "run.golden.txt")
	if os.Getenv("MINT_UPDATE_GOLDEN") != "" {
		require.NoError(t, os.WriteFile(path, []byte(got), 0o600))
	}
	want, err := os.ReadFile(path)
	require.NoError(t, err)
	assert.Equal(t, strings.ReplaceAll(string(want), "\r\n", "\n"), got)
}

func TestPolicyCannotWeakenTheFloor(t *testing.T) {
	spec, err := Compile(core.Policy{Limits: core.Limits{PIDs: 1 << 20}, Filesystem: core.FSPolicy{ReadWrite: []string{"/tmp"}}})
	require.NoError(t, err)
	joined := strings.Join(spec.Args, " ")
	for _, must := range []string{"--cap-drop=ALL", "--security-opt=no-new-privileges", "--read-only"} {
		assert.Contains(t, joined, must)
	}
	assert.Equal(t, 1, strings.Count(joined, "--tmpfs=/tmp"), "/tmp is not added twice")
	for _, forbidden := range []string{"--privileged", "--cap-add", "-v ", "--volume", "--mount", "docker.sock", "--network=host", "seccomp=unconfined"} {
		assert.NotContains(t, joined, forbidden)
	}
}

func TestCompileRefusesWhatV1CannotHonour(t *testing.T) {
	for name, policy := range map[string]core.Policy{
		"read-only host paths": {Filesystem: core.FSPolicy{ReadOnly: []string{"/etc"}}},
		"credentials":          {Credentials: map[string][]string{"copilot-token": {"api.githubcopilot.com"}}},
		"relative scratch":     {Filesystem: core.FSPolicy{ReadWrite: []string{"cache"}}},
		"root scratch":         {Filesystem: core.FSPolicy{ReadWrite: []string{"/"}}},
		"unclean scratch":      {Filesystem: core.FSPolicy{ReadWrite: []string{"/a/../etc"}}},
		"windows host path":    {Filesystem: core.FSPolicy{ReadWrite: []string{`C:\Users\me`}}},
	} {
		t.Run(name, func(t *testing.T) {
			_, err := Compile(policy)
			require.Error(t, err)
		})
	}
}
