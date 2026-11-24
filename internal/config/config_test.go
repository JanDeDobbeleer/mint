package config

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

const examplePath = "../../docs/mint.example.yaml"

func loadExample(t *testing.T) string {
	t.Helper()
	data, err := os.ReadFile(examplePath)
	require.NoError(t, err)
	// Windows checkouts convert line endings; the snippets in these tests use LF.
	return strings.ReplaceAll(string(data), "\r\n", "\n")
}

func parse(t *testing.T, text string) (*Config, error) {
	t.Helper()
	return Parse([]byte(text), "mint.yaml", Options{})
}

// mutate replaces one snippet of the example, failing the test if the snippet is not there.
func mutate(t *testing.T, old, replacement string) string {
	t.Helper()
	text := loadExample(t)
	require.Contains(t, text, old, "test setup: snippet must exist in the example")
	return strings.Replace(text, old, replacement, 1)
}

func TestExampleConfigurationLoadsAndDecodesEveryArea(t *testing.T) {
	cfg, err := LoadFile(examplePath, Options{})
	require.NoError(t, err)

	assert.Equal(t, 1, cfg.Version)
	assert.Equal(t, 2, cfg.Limits.MaxConcurrentRuns)
	assert.Equal(t, 1, cfg.Limits.PerRuntime["windows-sandbox"])
	assert.Equal(t, 5*time.Minute, cfg.StallTimeout)
	assert.Equal(t, 5, cfg.Retry.MaxAttempts)

	coder := cfg.Agents["coder"]
	assert.Equal(t, []string{"--yolo"}, coder.Args)
	assert.Equal(t, "analyst", coder.Fallback)
	assert.Equal(t, "default", cfg.Agents["fixer"].Sandbox, "an omitted sandbox is default")
	assert.True(t, cfg.Agents["fixer"].Resume)

	project := cfg.Projects["acme-api"]
	assert.True(t, project.Staged)
	assert.Equal(t, "container", project.RunsOn)
	assert.Equal(t, "merge", project.Integration.Strategy)
	assert.Equal(t, 10*time.Minute, project.Integration.Interval)
	assert.Equal(t, "squash", project.Integration.Method)
	assert.Equal(t, "main", project.Integration.Where.Base)
	assert.Len(t, project.Checks, 2)
	assert.Equal(t, "next-branch", cfg.Projects["windows-tool"].Integration.Strategy)

	hook := cfg.Hooks.AfterStage
	require.NotNil(t, hook)
	assert.Equal(t, 30*time.Second, hook.Timeout)
	assert.Equal(t, []string{"pwsh", "-NoProfile", "-File", `C:\mint\hooks\after-stage.ps1`}, hook.Command)

	assert.Equal(t, 5, cfg.Output.Max["comment"])
	assert.Contains(t, cfg.Output.ProtectedPaths, ".github/workflows")

	merge := cfg.Flows["nightly-next"].Stages["ci"]
	assert.Equal(t, "checks_green", merge.Wait)
	assert.True(t, merge.OnFail.Notify)
	assert.Equal(t, 90*time.Minute, merge.Timeout)
	verify := cfg.Flows["issue-to-pr"].Stages["verify"].OnFail
	assert.Equal(t, &OnFail{Retry: "implement", Max: 2}, verify)
}

func TestDefaultsAndBuiltInSandboxes(t *testing.T) {
	cfg, err := parse(t, "version: 1\n")
	require.NoError(t, err)
	assert.Equal(t, 5*time.Minute, cfg.StallTimeout)
	assert.Equal(t, 1, cfg.Limits.MaxConcurrentRuns)
	assert.Equal(t, 10*time.Second, cfg.Retry.BaseDelay)
	assert.Contains(t, cfg.Sandboxes, "default")
	assert.Contains(t, cfg.Sandboxes, "readonly")

	p, err := cfg.Policy("default")
	require.NoError(t, err)
	assert.Contains(t, p.Network.AllowedHosts, "github.com")
	assert.Contains(t, p.Network.AllowedHosts, "api.githubcopilot.com")
	assert.Equal(t, 5*time.Minute, p.Limits.StallTimeout)
	_, err = cfg.Policy("nope")
	require.Error(t, err)
}

// --- strictness ---

func TestUnknownKeysAreErrors(t *testing.T) {
	for name, text := range map[string]string{
		"top level":        "version: 1\nunknwon: 1\n",
		"nested":           mutate(t, "    staged: true\n", "    staged: true\n    stagged: true\n"),
		"inside a stage":   mutate(t, "publish: { action: github.open-pr }", "publish: { action: github.open-pr, nxt: x }"),
		"forbidden field":  "version: 1\nprojects:\n  a:\n    repo: github.com/o/a\n    token: x\n",
		"wrong value type": "version: 1\nlimits:\n  max_concurrent_runs: many\n",
		"duplicate key":    "version: 1\nversion: 1\n",
		"not a mapping":    "- a\n- b\n",
		"second document":  "version: 1\n---\nversion: 1\n",
		"empty":            "",
		"unparsable":       "version: [1\n",
		"bad duration":     "version: 1\nstall_timeout: soon\n",
	} {
		t.Run(name, func(t *testing.T) {
			_, err := parse(t, text)
			require.Error(t, err)
			assert.Contains(t, err.Error(), "mint.yaml")
		})
	}
}

func TestVersionIsRequiredAndChecked(t *testing.T) {
	for _, text := range []string{"", "limits: {}\n", "version: 2\n", "version: 0\n"} {
		_, err := parse(t, text)
		require.Error(t, err, text)
	}
	_, err := parse(t, "limits: {}\n")
	require.ErrorContains(t, err, "version: must be 1")
}

func TestOversizedAndIrregularFilesAreRejected(t *testing.T) {
	dir := t.TempDir()
	big := filepath.Join(dir, "big.yaml")
	require.NoError(t, os.WriteFile(big, []byte("version: 1\n#"+strings.Repeat("x", maxFileSize)), 0o600))
	_, err := LoadFile(big, Options{})
	require.ErrorContains(t, err, "regular file")
	_, err = LoadFile(dir, Options{})
	require.ErrorContains(t, err, "regular file")
	_, err = LoadFile(filepath.Join(dir, "missing.yaml"), Options{})
	require.Error(t, err)
	assert.NotContains(t, err.Error(), dir, "errors name the file, not the directory")
}

// --- reference and rule validation, with paths in the messages ---

func TestValidationErrorsCarryConfigPaths(t *testing.T) {
	tests := []struct{ name, old, replacement, want string }{
		{"unknown sandbox on a project", "    sandbox: build\n    staged", "    sandbox: nope\n    staged", "projects.acme-api.sandbox: unknown sandbox profile"},
		{"unknown sandbox on an agent", "    sandbox: readonly\n    tools: [read]", "    sandbox: nope\n    tools: [read]", "agents.analyst.sandbox: unknown sandbox profile"},
		{"unknown adapter", "  analyst:\n    adapter: copilot", "  analyst:\n    adapter: gemini", "agents.analyst.adapter: unknown adapter"},
		{"missing model", "    model: example-model\n    prompt: analyze", "    prompt: analyze", "agents.analyst.model: a model is required"},
		{"missing prompt", "    prompt: analyze\n", "", "agents.analyst.prompt: a prompt name is required"},
		{"unknown credential", "    credential: copilot-token", "    credential: made-up-token", "agents.fixer.credential: unknown credential name"},
		{"unknown fallback", "    fallback: analyst", "    fallback: ghost", "agents.coder.fallback: unknown agent profile"},
		{"bad workspace", "    prompt: analyze\n", "    prompt: analyze\n    workspace: shared\n", "agents.analyst.workspace"},
		{"unknown agent in a stage", "analyze: { agent: analyst,", "analyze: { agent: ghost,", "flows.issue-to-pr.stages.analyze.agent: unknown agent profile"},
		{"unknown next stage", "next: implement }\n      implement", "next: ghost }\n      implement", "unknown stage \"ghost\""},
		{"unbounded retry", "on_fail: { retry: implement, max: 2 }", "on_fail: { retry: implement, max: 0 }", "on_fail.max"},
		{"retry to nowhere", "on_fail: { retry: implement, max: 2 }", "on_fail: { retry: ghost, max: 2 }", "on_fail.retry"},
		{"stage with two kinds", "publish: { action: github.open-pr }", "publish: { action: github.open-pr, agent: coder }", "exactly one of agent, action or wait"},
		{"stage with no kind", "publish: { action: github.open-pr }", "publish: { next: analyze }", "exactly one of agent, action or wait"},
		{"bad repo", "repo: github.com/me/acme-api", "repo: gitlab.com/me/acme-api", "projects.acme-api.repo"},
		{"duplicate repo", "repo: github.com/me/windows-tool", "repo: github.com/ME/acme-api", "already used by project"},
		{"unknown runtime", "    runs_on: container\n", "    runs_on: vm\n", "projects.acme-api.runs_on: unknown runtime"},
		{"bad sender", `"github:me", trust: maintainer }
      - { match: "email`, `"me", trust: maintainer }
      - { match: "email`, "senders[0].match"},
		{"bad trust", `trust: customer }`, `trust: stranger }`, "senders[1].trust"},
		{"customer on the host runtime", "    runs_on: windows-sandbox\n    senders:\n      - { match: \"github:me\", trust: maintainer }", "    runs_on: windows-sandbox\n    senders:\n      - { match: \"email:x@y.example\", trust: customer }", "maintainer senders only"},
		{"empty check command", "{ name: vet, command: [go, vet, ./...] }", "{ name: vet, command: [] }", "checks[1]"},
		{"bad host in a bundle", "    - registry.example.com\n\nsandboxes", "    - https://registry.example.com/x\n\nsandboxes", "network_bundles.internal-registry"},
		{"unknown bundle", "bundles: [github, copilot, go]", "bundles: [github, cobol]", "unknown bundle \"cobol\""},
		{"ip address as host", "allowed_hosts: [registry.example.com]", "allowed_hosts: [10.0.0.1]", "not a valid host name"},
		{"unknown credential in a sandbox", "    filesystem:\n      readwrite: [/var/cache/build]", "    credentials:\n      made-up: [github.com]\n    filesystem:\n      readwrite: [/var/cache/build]", "unknown credential name"},
		{"wildcard credential host", "    filesystem:\n      readwrite: [/var/cache/build]", "    credentials:\n      copilot-token: [\"*.githubcopilot.com\"]\n    filesystem:\n      readwrite: [/var/cache/build]", "exact host name"},
		{"negative limit", "      pids: 512", "      pids: -1", "limits cannot be negative"},
		{"host runtime above one", "    windows-sandbox: 1", "    windows-sandbox: 2", "one session at a time"},
		{"per-project limit for a missing project", "    acme-api: 1\n  per_repo", "    ghost: 1\n  per_repo", "limits.per_project.ghost"},
		{"retry delays reversed", "  base_delay: 10s\n  max_delay: 5m", "  base_delay: 10m\n  max_delay: 5m", "base_delay must not exceed max_delay"},
		{"hook without command", "    command: [pwsh, -NoProfile, -File, C:\\mint\\hooks\\after-stage.ps1]\n", "    command: []\n", "hooks.after_stage.command"},
		{"unknown output type", "max: { comment: 5,", "max: { gossip: 5,", "output.max.gossip"},
		{"absolute protected path", "protected_paths: [.github/workflows, .mint]", "protected_paths: [/etc, .mint]", "output.protected_paths"},
		{"bad label glob", `label_block: ["release*"]`, `label_block: ["[x"]`, "not a valid label glob"},
		{"unknown trigger project", "project: auto }\n  - { source: github, kind: pr_comment", "project: ghost }\n  - { source: github, kind: pr_comment", "triggers[0].project"},
		{"unknown trigger source", "source: email, to:", "source: pigeon, to:", "triggers[2].source"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := parse(t, mutate(t, tt.old, tt.replacement))
			require.Error(t, err)
			assert.Contains(t, err.Error(), tt.want)
		})
	}
}

func TestIntegrationStrategyIsExclusiveAndComplete(t *testing.T) {
	const merge = "      strategy: merge\n      approvers: [me]\n      interval: 10m\n      method: squash\n"
	tests := []struct{ name, old, replacement, want string }{
		{"no approvers", "      approvers: [me]\n      interval: 10m", "      interval: 10m", "needs at least one approver"},
		{"interval too short", "interval: 10m", "interval: 5s", "at least 1m"},
		{"bad method", "method: squash", "method: fast-forward", "integration.method"},
		{"merge with a schedule", merge, merge + "      schedule: \"0 5 * * *\"\n      tz: UTC\n", "exactly one strategy"},
		{"unknown strategy", "strategy: merge", "strategy: queue", "integration.strategy"},
		{"next-branch without tz", "      tz: Europe/Brussels\n", "", "a time zone is required"},
		{"next-branch bad tz", "tz: Europe/Brussels\n\ntriggers", "tz: Mars/Olympus\n\ntriggers", "unknown time zone"},
		{"next-branch bad cron", `schedule: "0 5 * * *"
      tz: Europe/Brussels
`, `schedule: "daily"
      tz: Europe/Brussels
`, "5-field cron"},
		{"next-branch with a merge method", "      schedule: \"0 5 * * *\"\n      tz: Europe/Brussels\n\ntriggers", "      schedule: \"0 5 * * *\"\n      tz: Europe/Brussels\n      method: squash\n\ntriggers", "belong to merge"},
		{"off with settings", merge, "      strategy: off\n      interval: 10m\n", "strategy is off but strategy settings are present"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := parse(t, mutate(t, tt.old, tt.replacement))
			require.Error(t, err)
			assert.Contains(t, err.Error(), tt.want)
		})
	}

	cfg, err := parse(t, "version: 1\nprojects:\n  a:\n    repo: github.com/o/a\n    integration:\n      strategy: off\n")
	require.NoError(t, err)
	assert.Equal(t, "off", cfg.Projects["a"].Integration.Strategy)
}

func TestFlowGraphRules(t *testing.T) {
	loop := "version: 1\nagents:\n  a: {adapter: copilot, model: m, prompt: p}\nflows:\n  f:\n    on: {source: github}\n    stages:\n      x: {agent: a, next: y}\n      y: {agent: a, next: x}\n"
	_, err := parse(t, loop)
	require.ErrorContains(t, err, "exactly one entry stage")

	cycle := "version: 1\nagents:\n  a: {adapter: copilot, model: m, prompt: p}\nflows:\n  f:\n    on: {source: github}\n    stages:\n      start: {agent: a, next: y}\n      y: {agent: a, next: z}\n      z: {agent: a, next: y}\n"
	_, err = parse(t, cycle)
	require.ErrorContains(t, err, "form a cycle")

	twoEntries := "version: 1\nagents:\n  a: {adapter: copilot, model: m, prompt: p}\nflows:\n  f:\n    on: {source: github}\n    stages:\n      x: {agent: a}\n      y: {agent: a}\n"
	_, err = parse(t, twoEntries)
	require.ErrorContains(t, err, "exactly one entry stage")

	fallbackCycle := "version: 1\nagents:\n  a: {adapter: copilot, model: m, prompt: p, fallback: b}\n  b: {adapter: copilot, model: m, prompt: p, fallback: a}\n"
	_, err = parse(t, fallbackCycle)
	require.ErrorContains(t, err, "fallback profiles form a cycle")

	_, err = parse(t, "version: 1\nflows:\n  f:\n    stages:\n      x: {action: checks}\n")
	require.ErrorContains(t, err, "needs a source or a schedule")
}

func TestOptionsDefineTheRegisteredExtensions(t *testing.T) {
	text := "version: 1\nagents:\n  c: {adapter: claude, model: m, prompt: p}\nprojects:\n  p:\n    repo: github.com/o/p\n    runs_on: gvisor\n"
	_, err := parse(t, text)
	require.Error(t, err)
	cfg, err := Parse([]byte(text), "mint.yaml", Options{Adapters: []string{"copilot", "claude"}, Runtimes: []string{"container", "gvisor"}})
	require.NoError(t, err)
	assert.Equal(t, "claude", cfg.Agents["c"].Adapter)
}

func TestAllProblemsAreReportedTogetherInStableOrder(t *testing.T) {
	text := "version: 3\nagents:\n  b: {adapter: x, prompt: p}\n  a: {adapter: x, model: m}\n"
	_, err := parse(t, text)
	require.Error(t, err)
	lines := strings.Split(err.Error(), "\n")
	assert.Greater(t, len(lines), 4)
	sorted := append([]string(nil), lines[1:]...)
	for i := 1; i < len(sorted); i++ {
		assert.LessOrEqual(t, strings.TrimSpace(sorted[i-1]), strings.TrimSpace(sorted[i]))
	}
	_, again := parse(t, text)
	assert.Equal(t, err.Error(), again.Error())
}

// --- secrets ---

func TestSecretLookingValuesAreRejectedWithoutBeingEchoed(t *testing.T) {
	pem := "-----BEGIN RSA PRIVATE KEY-----\nMIIEowIBAAKCAQEA\n-----END RSA PRIVATE KEY-----"
	for name, value := range map[string]string{
		"classic PAT":      "ghp_" + strings.Repeat("a1B2c3", 6),
		"fine-grained PAT": "github_pat_11ABCDEFG0abcdefghijklmnop",
		"installation":     "ghs_" + strings.Repeat("Zy9x", 9),
		"anthropic key":    "sk-ant-api03-" + strings.Repeat("q", 20),
		"aws key id":       "AKIAIOSFODNN7EXAMPLE",
		"slack":            "xoxb-123456789012-abcdefghijkl",
		"random token":     "q8Zr2LmN7xKp4VtYc9HdJw3BsUe6GfAa",
		"base64url secret": "dGhpcyBpcyBub3QgYSByZWFsIGtleSBidXQgbG9va3Mgb2s_-",
	} {
		t.Run(name, func(t *testing.T) {
			text := "version: 1\nagents:\n  a:\n    adapter: copilot\n    model: m\n    prompt: p\n    args: [\"--flag=" + value + "\", \"" + value + "\"]\n"
			_, err := parse(t, text)
			require.Error(t, err)
			assert.Contains(t, err.Error(), "looks like")
			assert.Contains(t, err.Error(), "line ")
			assert.NotContains(t, err.Error(), value)
		})
	}

	_, err := parse(t, "version: 1\nhooks:\n  on_done:\n    command: [\"echo\", \"|\"]\n    timeout: 1s\nagents:\n  a:\n    adapter: copilot\n    model: m\n    prompt: |\n      "+strings.ReplaceAll(pem, "\n", "\n      ")+"\n")
	require.ErrorContains(t, err, "PEM block")
	assert.NotContains(t, err.Error(), "MIIEow")

	// Sensitive text in a place where the YAML decoder would echo it in a type error.
	_, err = parse(t, "version: 1\nlimits:\n  max_concurrent_runs: ghp_"+strings.Repeat("x9", 12)+"\n")
	require.Error(t, err)
	assert.NotContains(t, err.Error(), "x9x9x9")
	_, err = parse(t, "version: 1\nlimits:\n  max_concurrent_runs: not-a-number-but-harmless-text\n")
	require.Error(t, err)
	assert.NotContains(t, err.Error(), "not-a-number-but-harmless-text", "type errors do not echo values")
}

func TestOrdinaryValuesAreNotMistakenForSecrets(t *testing.T) {
	// The example holds hashes of nothing, paths, URLs, cron lines and long ordinary words.
	_, err := parse(t, loadExample(t))
	require.NoError(t, err)
	for _, value := range []string{
		"github.com/me/a-rather-long-repository-name-for-testing",
		"C:\\\\Users\\\\someone\\\\mint\\\\hooks\\\\after-stage.ps1",
		"internationalizationandlocalizationconsiderations",
		"0123456789012345678901234567890123456789",
		"aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa",
		"mint:issue-to-pr:github:owner/repo#123-extra-words",
	} {
		assert.Empty(t, secretKind(value), value)
	}
}

// --- network ---

func TestBlockedHostsOverrideAllowedAndBundlesResolve(t *testing.T) {
	cfg, err := parse(t, loadExample(t))
	require.NoError(t, err)
	p, err := cfg.Policy("build")
	require.NoError(t, err)
	assert.Contains(t, p.Network.AllowedHosts, "proxy.golang.org", "the go bundle")
	assert.Contains(t, p.Network.AllowedHosts, "registry.example.com")
	assert.Contains(t, p.Network.AllowedHosts, "github.com")
	assert.NotContains(t, p.Network.AllowedHosts, "uploads.github.com", "blocked wins over the github bundle")
	assert.Equal(t, []string{"uploads.github.com"}, p.Network.BlockedHosts)
	assert.IsIncreasing(t, p.Network.AllowedHosts, "sorted for stable output")
	assert.Equal(t, []string{"/var/cache/build"}, p.Filesystem.ReadWrite)
	assert.Equal(t, int64(4096)<<20, p.Limits.MemoryBytes)
	assert.Equal(t, 45*time.Minute, p.Limits.WallClock)

	text := "version: 1\nnetwork_bundles:\n  github: [only.example.com]\nsandboxes:\n  s:\n    network:\n      bundles: [github]\n      blocked_hosts: ['*.example.com']\n"
	cfg, err = parse(t, text)
	require.NoError(t, err)
	p, err = cfg.Policy("s")
	require.NoError(t, err)
	assert.Empty(t, p.Network.AllowedHosts, "an operator bundle replaces the default, and a wildcard block removes covered hosts")
}

func TestHostValidationAndMatching(t *testing.T) {
	for _, h := range []string{"github.com", "api.github.com", "*.githubusercontent.com", "a-b.example.org"} {
		assert.True(t, ValidHost(h), h)
	}
	for _, h := range []string{"", "localhost", "10.0.0.1", "1.2.3.4", "https://x.com", "x.com/path", "x.com:443", "-bad.com", "UPPER.com", "*.*.com", "*", "a..b.com", "exa mple.com"} {
		assert.False(t, ValidHost(h), h)
	}
	assert.True(t, HostMatches("*.example.com", "example.com"))
	assert.True(t, HostMatches("*.example.com", "a.b.example.com"))
	assert.False(t, HostMatches("*.example.com", "badexample.com"))
	assert.True(t, HostMatches("example.com", "example.com"))
	assert.False(t, HostMatches("example.com", "a.example.com"))
}

func TestRetryBackoff(t *testing.T) {
	r := Retry{BaseDelay: 10 * time.Second, MaxDelay: 5 * time.Minute}
	want := []time.Duration{10 * time.Second, 20 * time.Second, 40 * time.Second, 80 * time.Second, 160 * time.Second, 5 * time.Minute, 5 * time.Minute}
	for i, w := range want {
		assert.Equal(t, w, r.Backoff(i+1), "attempt %d", i+1)
	}
	assert.Equal(t, 10*time.Second, r.Backoff(0))
	assert.Equal(t, 5*time.Minute, r.Backoff(1000))
}

// --- reload ---

func writeConfig(t *testing.T, path, text string) {
	t.Helper()
	require.NoError(t, os.WriteFile(path, []byte(text), 0o600))
}

func TestReloadKeepsTheLastGoodConfiguration(t *testing.T) {
	path := filepath.Join(t.TempDir(), "mint.yaml")
	writeConfig(t, path, "version: 1\nstall_timeout: 3m\n")
	loader, err := NewLoader(path, Options{})
	require.NoError(t, err)
	assert.Equal(t, 3*time.Minute, loader.Current().StallTimeout)

	changed, err := loader.Reload()
	require.NoError(t, err)
	assert.False(t, changed, "an unchanged file is not a change")

	writeConfig(t, path, "version: 1\nstall_timeout: 7m\n")
	changed, err = loader.Reload()
	require.NoError(t, err)
	assert.True(t, changed)
	good := loader.Current()
	assert.Equal(t, 7*time.Minute, good.StallTimeout)

	for _, bad := range []string{"version: 1\nstall_timeout: soon\n", "version: 1\nnope: 1\n", "version: 9\n", "", "version: 1\nagents:\n  a: {adapter: x}\n"} {
		writeConfig(t, path, bad)
		changed, err = loader.Reload()
		require.Error(t, err, bad)
		assert.False(t, changed)
		assert.Same(t, good, loader.Current(), "a rejected file never replaces the configuration")
	}

	require.NoError(t, os.Remove(path))
	_, err = loader.Reload()
	require.Error(t, err)
	assert.Same(t, good, loader.Current())

	writeConfig(t, path, "version: 1\nstall_timeout: 9m\n")
	changed, err = loader.Reload()
	require.NoError(t, err)
	assert.True(t, changed)
	assert.Equal(t, 9*time.Minute, loader.Current().StallTimeout)
}

func TestFirstLoadMustSucceed(t *testing.T) {
	path := filepath.Join(t.TempDir(), "mint.yaml")
	_, err := NewLoader(path, Options{})
	require.Error(t, err)
	writeConfig(t, path, "version: 1\nbogus: true\n")
	_, err = NewLoader(path, Options{})
	require.Error(t, err)
}

func TestWatchReportsEachRejectedFileOnceAndEachGoodChange(t *testing.T) {
	path := filepath.Join(t.TempDir(), "mint.yaml")
	writeConfig(t, path, "version: 1\n")
	loader, err := NewLoader(path, Options{})
	require.NoError(t, err)

	var mu sync.Mutex
	var errs []error
	var changes []time.Duration
	ctx, cancel := context.WithCancel(t.Context())
	done := make(chan struct{})
	go func() {
		defer close(done)
		loader.Watch(ctx, 5*time.Millisecond, func(c *Config) {
			mu.Lock()
			changes = append(changes, c.StallTimeout)
			mu.Unlock()
		}, func(err error) {
			mu.Lock()
			errs = append(errs, err)
			mu.Unlock()
		})
	}()
	eventually := func(cond func() bool) {
		require.Eventually(t, func() bool { mu.Lock(); defer mu.Unlock(); return cond() }, 3*time.Second, 5*time.Millisecond)
	}

	writeConfig(t, path, "version: 1\nstall_timeout: nonsense\n")
	eventually(func() bool { return len(errs) == 1 })
	time.Sleep(60 * time.Millisecond)
	mu.Lock()
	assert.Len(t, errs, 1, "the same bad file is reported once, not on every poll")
	mu.Unlock()

	writeConfig(t, path, "version: 1\nstall_timeout: 2m\n")
	eventually(func() bool { return len(changes) == 1 })
	writeConfig(t, path, "version: 1\nstall_timeout: also-bad\n")
	eventually(func() bool { return len(errs) == 2 })

	cancel()
	<-done
	mu.Lock()
	defer mu.Unlock()
	assert.Equal(t, []time.Duration{2 * time.Minute}, changes)
}
