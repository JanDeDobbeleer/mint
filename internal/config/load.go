package config

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"maps"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"sort"
	"strings"
	"time"

	"go.yaml.in/yaml/v3"
)

// FileName is the operator configuration file inside the Mint configuration directory.
const FileName = "mint.yaml"

const maxFileSize = 1 << 20

// Options name the extension points the configuration may refer to. The config package does not import agents
// or runtimes (design principle 5); the caller passes the registered names. Nil fields use the built-ins.
type Options struct {
	Adapters []string
	Runtimes []string
}

func (o Options) adapters() []string {
	if o.Adapters == nil {
		return []string{"copilot"}
	}
	return o.Adapters
}

func (o Options) runtimes() []string {
	if o.Runtimes == nil {
		return []string{"container", "windows-sandbox"}
	}
	return o.Runtimes
}

// DefaultPath is where mint.yaml lives inside the configuration directory.
func DefaultPath(dir string) string { return filepath.Join(dir, FileName) }

// LoadFile reads, decodes and validates the file at path.
func LoadFile(path string, opts Options) (*Config, error) {
	data, err := readLimited(path)
	if err != nil {
		return nil, err
	}
	return Parse(data, filepath.Base(path), opts)
}

func readLimited(path string) ([]byte, error) {
	info, err := os.Lstat(path)
	if err != nil {
		return nil, fmt.Errorf("cannot read %s: %w", filepath.Base(path), unwrapPath(err))
	}
	if !info.Mode().IsRegular() || info.Size() > maxFileSize {
		return nil, fmt.Errorf("%s must be a regular file of at most %d bytes", filepath.Base(path), maxFileSize)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("cannot read %s: %w", filepath.Base(path), unwrapPath(err))
	}
	return data, nil
}

func unwrapPath(err error) error {
	if pe, ok := errors.AsType[*os.PathError](err); ok {
		return pe.Err
	}
	return err
}

// echoed matches the back-quoted values that yaml.v3 copies into its type errors.
var echoed = regexp.MustCompile("`[^`]*`")

// Parse decodes data strictly and validates it. name labels error messages. Errors carry a path such as
// "projects.acme.sandbox" or a line number, and never contain a configuration value that looks like a secret.
func Parse(data []byte, name string, opts Options) (*Config, error) {
	if len(data) > maxFileSize {
		return nil, fmt.Errorf("%s: file is too large", name)
	}
	var doc yaml.Node
	if err := yaml.Unmarshal(data, &doc); err != nil {
		return nil, fmt.Errorf("%s: %s", name, sanitize(err))
	}
	if problems := scanSecrets(&doc); len(problems) > 0 {
		return nil, joinProblems(name, problems)
	}
	decoder := yaml.NewDecoder(bytes.NewReader(data))
	decoder.KnownFields(true)
	var cfg Config
	if err := decoder.Decode(&cfg); err != nil {
		if errors.Is(err, io.EOF) {
			return nil, fmt.Errorf("%s: the file is empty", name)
		}
		return nil, fmt.Errorf("%s: %s", name, sanitize(err))
	}
	if err := decoder.Decode(new(Config)); !errors.Is(err, io.EOF) {
		return nil, fmt.Errorf("%s: only one YAML document is allowed", name)
	}
	cfg.applyDefaults()
	if problems := cfg.validate(opts); len(problems) > 0 {
		return nil, joinProblems(name, problems)
	}
	return &cfg, nil
}

func sanitize(err error) string {
	return echoed.ReplaceAllString(strings.ReplaceAll(err.Error(), "\n", "; "), "`…`")
}

func joinProblems(name string, problems []error) error {
	var b strings.Builder
	b.WriteString(name + ": invalid configuration")
	for _, p := range problems {
		b.WriteString("\n  " + p.Error())
	}
	return errors.New(b.String())
}

// UnmarshalYAML lets a stage say `on_fail: notify` as well as `on_fail: {retry: x, max: 2}`.
func (o *OnFail) UnmarshalYAML(n *yaml.Node) error {
	if n.Kind == yaml.ScalarNode {
		if n.Value != "notify" {
			return fmt.Errorf("line %d: on_fail must be \"notify\" or a mapping with retry and max", n.Line)
		}
		*o = OnFail{Notify: true}
		return nil
	}
	type plain OnFail
	return n.Decode((*plain)(o))
}

// Built-in sandbox profiles. Operators may redefine them under sandboxes.
func builtinSandboxes() map[string]Sandbox {
	network := Network{Bundles: []string{"github", "copilot"}}
	return map[string]Sandbox{"default": {Network: network}, "readonly": {Network: network}}
}

func (c *Config) applyDefaults() {
	if c.StallTimeout == 0 {
		c.StallTimeout = 5 * time.Minute
	}
	if c.Retry.BaseDelay == 0 {
		c.Retry.BaseDelay = 10 * time.Second
	}
	if c.Retry.MaxDelay == 0 {
		c.Retry.MaxDelay = 5 * time.Minute
	}
	if c.Retry.MaxAttempts == 0 {
		c.Retry.MaxAttempts = 5
	}
	if c.Limits.MaxConcurrentRuns == 0 {
		c.Limits.MaxConcurrentRuns = 1
	}
	sandboxes := builtinSandboxes()
	maps.Copy(sandboxes, c.Sandboxes)
	c.Sandboxes = sandboxes
	for _, h := range []*Hook{c.Hooks.BeforeStage, c.Hooks.AfterStage, c.Hooks.OnFail, c.Hooks.OnDone, c.Hooks.BeforeRemove} {
		if h != nil && h.Timeout == 0 {
			h.Timeout = time.Minute
		}
	}
	for name, p := range c.Projects {
		if p.RunsOn == "" {
			p.RunsOn = "container"
		}
		if p.Sandbox == "" {
			p.Sandbox = "default"
		}
		c.Projects[name] = p
	}
	for name, a := range c.Agents {
		if a.Sandbox == "" {
			a.Sandbox = "default"
		}
		c.Agents[name] = a
	}
}

// problems collects validation errors with config paths, in a stable order.
type problems []error

func (p *problems) add(path, format string, args ...any) {
	*p = append(*p, fmt.Errorf("%s: %s", path, fmt.Sprintf(format, args...)))
}

func sortedKeys[V any](m map[string]V) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}

var namePattern = regexp.MustCompile(`^[a-z][a-z0-9_-]{0,62}$`)

func (c *Config) validate(opts Options) []error {
	var p problems
	if c.Version != SupportedVersion {
		p.add("version", "must be %d (got %d)", SupportedVersion, c.Version)
	}
	c.validateNetwork(&p)
	c.validateSandboxes(&p)
	c.validateAgents(&p, opts)
	c.validateProjects(&p, opts)
	c.validateTriggers(&p)
	c.validateFlows(&p)
	c.validateOperations(&p, opts)
	return p
}

func nameOK(p *problems, path, name string) bool {
	if !namePattern.MatchString(name) {
		p.add(path, "%q is not a valid name (lower-case letters, digits, - and _, starting with a letter)", name)
		return false
	}
	return true
}

func (c *Config) validateNetwork(p *problems) {
	for _, name := range sortedKeys(c.NetworkBundles) {
		path := "network_bundles." + name
		nameOK(p, path, name)
		if len(c.NetworkBundles[name]) == 0 {
			p.add(path, "a bundle needs at least one host")
		}
		for _, h := range c.NetworkBundles[name] {
			if !ValidHost(h) {
				p.add(path, "%q is not a valid host name", h)
			}
		}
	}
}

func (c *Config) validateSandboxes(p *problems) {
	for _, name := range sortedKeys(c.Sandboxes) {
		s := c.Sandboxes[name]
		path := "sandboxes." + name
		nameOK(p, path, name)
		for _, b := range s.Network.Bundles {
			if _, ok := c.bundle(b); !ok {
				p.add(path+".network.bundles", "unknown bundle %q", b)
			}
		}
		for field, hosts := range map[string][]string{"allowed_hosts": s.Network.AllowedHosts, "blocked_hosts": s.Network.BlockedHosts} {
			for _, h := range hosts {
				if !ValidHost(h) {
					p.add(path+".network."+field, "%q is not a valid host name", h)
				}
			}
		}
		for _, cred := range sortedKeys(s.Credentials) {
			if !credentialKnown(cred) {
				p.add(path+".credentials", "unknown credential name %q", cred)
			}
			for _, h := range s.Credentials[cred] {
				if !ValidHost(h) || strings.HasPrefix(h, "*.") {
					p.add(path+".credentials."+cred, "%q must be an exact host name", h)
				}
			}
		}
		for field, paths := range map[string][]string{"readonly": s.Filesystem.ReadOnly, "readwrite": s.Filesystem.ReadWrite} {
			for _, fp := range paths {
				if fp == "" || strings.Contains(fp, "\x00") {
					p.add(path+".filesystem."+field, "paths cannot be empty")
				}
			}
		}
		l := s.Limits
		if l.CPUs < 0 || l.MemoryMB < 0 || l.PIDs < 0 || l.WallClock < 0 || l.StallTimeout < 0 {
			p.add(path+".limits", "limits cannot be negative")
		}
	}
}

func (c *Config) validateAgents(p *problems, opts Options) {
	for _, name := range sortedKeys(c.Agents) {
		a := c.Agents[name]
		path := "agents." + name
		nameOK(p, path, name)
		if !slices.Contains(opts.adapters(), a.Adapter) {
			p.add(path+".adapter", "unknown adapter %q (available: %s)", a.Adapter, strings.Join(opts.adapters(), ", "))
		}
		if a.Model == "" {
			p.add(path+".model", "a model is required: Mint never relies on a default model")
		}
		if a.Prompt == "" {
			p.add(path+".prompt", "a prompt name is required")
		}
		if _, ok := c.Sandboxes[a.Sandbox]; !ok {
			p.add(path+".sandbox", "unknown sandbox profile %q", a.Sandbox)
		}
		if !slices.Contains([]string{"", "new", "reuse", "reuse-readonly"}, a.Workspace) {
			p.add(path+".workspace", "must be new, reuse or reuse-readonly")
		}
		if a.Credential != "" && !credentialKnown(a.Credential) {
			p.add(path+".credential", "unknown credential name %q", a.Credential)
		}
		for _, list := range [][]string{a.Tools, a.Args} {
			if slices.Contains(list, "") {
				p.add(path, "tools and args cannot contain empty entries")
			}
		}
		if a.Fallback != "" {
			if _, ok := c.Agents[a.Fallback]; !ok {
				p.add(path+".fallback", "unknown agent profile %q", a.Fallback)
			}
			c.checkFallbackCycle(p, name)
		}
	}
}

func (c *Config) checkFallbackCycle(p *problems, start string) {
	seen := map[string]bool{start: true}
	for next := c.Agents[start].Fallback; next != ""; next = c.Agents[next].Fallback {
		if seen[next] {
			p.add("agents."+start+".fallback", "fallback profiles form a cycle")
			return
		}
		seen[next] = true
	}
}

var (
	repoPattern   = regexp.MustCompile(`^github\.com/[A-Za-z0-9_.-]+/[A-Za-z0-9_.-]+$`)
	senderSchemes = []string{"github:", "email:", "whatsapp:"}
)

func (c *Config) validateProjects(p *problems, opts Options) {
	repos := map[string]string{}
	for _, name := range sortedKeys(c.Projects) {
		pr := c.Projects[name]
		path := "projects." + name
		nameOK(p, path, name)
		if !repoPattern.MatchString(pr.Repo) {
			p.add(path+".repo", "must look like github.com/owner/name")
		} else if other, dup := repos[strings.ToLower(pr.Repo)]; dup {
			p.add(path+".repo", "already used by project %q", other)
		} else {
			repos[strings.ToLower(pr.Repo)] = name
		}
		if !slices.Contains(opts.runtimes(), pr.RunsOn) {
			p.add(path+".runs_on", "unknown runtime %q (available: %s)", pr.RunsOn, strings.Join(opts.runtimes(), ", "))
		}
		if _, ok := c.Sandboxes[pr.Sandbox]; !ok {
			p.add(path+".sandbox", "unknown sandbox profile %q", pr.Sandbox)
		}
		for i, s := range pr.Senders {
			sp := fmt.Sprintf("%s.senders[%d]", path, i)
			if !slices.ContainsFunc(senderSchemes, func(prefix string) bool { return len(s.Match) > len(prefix) && strings.HasPrefix(s.Match, prefix) }) {
				p.add(sp+".match", "must start with github:, email: or whatsapp: followed by an identity")
			}
			if s.Trust != "maintainer" && s.Trust != "customer" {
				p.add(sp+".trust", "must be maintainer or customer")
			}
			if pr.RunsOn == "windows-sandbox" && s.Trust != "maintainer" {
				p.add(sp+".trust", "the windows-sandbox runtime accepts maintainer senders only (the host is not a strong boundary)")
			}
		}
		for i, ch := range pr.Checks {
			if ch.Name == "" || len(ch.Command) == 0 || slices.Contains(ch.Command, "") {
				p.add(fmt.Sprintf("%s.checks[%d]", path, i), "a check needs a name and a command without empty arguments")
			}
		}
		c.validateIntegration(p, path+".integration", pr.Integration)
		if pr.Output != nil {
			validateOutput(p, path+".output", *pr.Output)
		}
	}
}

func (c *Config) validateIntegration(p *problems, path string, in Integration) {
	switch in.Strategy {
	case "", "off":
		if in.Interval != 0 || in.Schedule != "" || in.Method != "" || len(in.Approvers) != 0 {
			p.add(path, "strategy is off but strategy settings are present; set strategy to merge or next-branch")
		}
		return
	case "merge":
		if in.Interval < time.Minute {
			p.add(path+".interval", "must be at least 1m")
		}
		if in.Schedule != "" || in.TZ != "" {
			p.add(path, "schedule and tz belong to next-branch; a project has exactly one strategy")
		}
		if !slices.Contains([]string{"merge", "squash", "rebase"}, in.Method) {
			p.add(path+".method", "must be merge, squash or rebase")
		}
	case "next-branch":
		if in.Schedule == "" || len(strings.Fields(in.Schedule)) != 5 {
			p.add(path+".schedule", "must be a 5-field cron expression")
		}
		if in.TZ == "" {
			p.add(path+".tz", "a time zone is required")
		} else if _, err := time.LoadLocation(in.TZ); err != nil {
			p.add(path+".tz", "unknown time zone")
		}
		if in.Interval != 0 || in.Method != "" {
			p.add(path, "interval and method belong to merge; next-branch always merges the next branch with a merge commit")
		}
	default:
		p.add(path+".strategy", "must be merge, next-branch or off")
		return
	}
	if len(in.Approvers) == 0 || slices.Contains(in.Approvers, "") {
		p.add(path+".approvers", "an enabled strategy needs at least one approver (native review approvals count)")
	}
}

var outputTypes = []string{"comment", "open-pr", "reply-draft", "label"}

func validateOutput(p *problems, path string, o OutputPolicy) {
	for _, t := range sortedKeys(o.Max) {
		if !slices.Contains(outputTypes, t) {
			p.add(path+".max."+t, "unknown output type (available: %s)", strings.Join(outputTypes, ", "))
		}
		if o.Max[t] < 0 {
			p.add(path+".max."+t, "cannot be negative")
		}
	}
	for _, t := range o.Targets {
		if t != "run-thread" && t != "run-branch" && t != "base" {
			p.add(path+".targets", "unknown target %q (run-thread, run-branch, base)", t)
		}
	}
	for _, g := range slices.Concat(o.LabelAllow, o.LabelBlock) {
		if _, err := filepath.Match(g, "x"); err != nil {
			p.add(path, "%q is not a valid label glob", g)
		}
	}
	for _, pp := range o.ProtectedPaths {
		if pp == "" || filepath.IsAbs(pp) || strings.HasPrefix(pp, "/") || strings.HasPrefix(pp, "\\") || filepath.VolumeName(pp) != "" || strings.Contains(pp, "..") {
			p.add(path+".protected_paths", "%q must be a relative path inside the repository", pp)
		}
	}
	if o.MaxPatchBytes < 0 {
		p.add(path+".max_patch_bytes", "cannot be negative")
	}
}

func (c *Config) validateTriggers(p *problems) {
	for i, t := range c.Triggers {
		path := fmt.Sprintf("triggers[%d]", i)
		if !slices.Contains([]string{"github", "email", "whatsapp", "cron"}, t.Source) {
			p.add(path+".source", "must be github, email, whatsapp or cron")
		}
		if t.Project != "auto" && t.Project != "by-sender" {
			if _, ok := c.Projects[t.Project]; !ok {
				p.add(path+".project", "must be auto, by-sender or the name of a project")
			}
		}
	}
}

func (c *Config) validateFlows(p *problems) {
	for _, name := range sortedKeys(c.Flows) {
		f := c.Flows[name]
		path := "flows." + name
		nameOK(p, path, name)
		if f.On.Schedule == "" && f.On.Source == "" {
			p.add(path+".on", "a flow needs a source or a schedule")
		}
		if f.On.Schedule != "" && len(strings.Fields(f.On.Schedule)) != 5 {
			p.add(path+".on.schedule", "must be a 5-field cron expression")
		}
		if len(f.Stages) == 0 {
			p.add(path+".stages", "a flow needs at least one stage")
			continue
		}
		referenced := map[string]bool{}
		for _, sn := range sortedKeys(f.Stages) {
			s := f.Stages[sn]
			sp := path + ".stages." + sn
			nameOK(p, sp, sn)
			kinds := 0
			for _, v := range []string{s.Agent, s.Action, s.Wait} {
				if v != "" {
					kinds++
				}
			}
			if kinds != 1 {
				p.add(sp, "exactly one of agent, action or wait is required")
			}
			if s.Agent != "" {
				if _, ok := c.Agents[s.Agent]; !ok {
					p.add(sp+".agent", "unknown agent profile %q", s.Agent)
				}
			}
			if s.Next != "" {
				if _, ok := f.Stages[s.Next]; !ok {
					p.add(sp+".next", "unknown stage %q", s.Next)
				}
				referenced[s.Next] = true
			}
			if s.OnFail != nil && !s.OnFail.Notify {
				if _, ok := f.Stages[s.OnFail.Retry]; !ok {
					p.add(sp+".on_fail.retry", "unknown stage %q", s.OnFail.Retry)
				}
				if s.OnFail.Max < 1 || s.OnFail.Max > 10 {
					p.add(sp+".on_fail.max", "a bounded retry needs max between 1 and 10")
				}
			}
			if s.Timeout < 0 {
				p.add(sp+".timeout", "cannot be negative")
			}
		}
		var entries []string
		for _, sn := range sortedKeys(f.Stages) {
			if !referenced[sn] {
				entries = append(entries, sn)
			}
		}
		if len(entries) != 1 {
			p.add(path+".stages", "a flow needs exactly one entry stage that no other stage names as next (found %d); loops go through on_fail with a max", len(entries))
		}
		for _, sn := range sortedKeys(f.Stages) {
			if hasNextCycle(f, sn) {
				p.add(path+".stages."+sn+".next", "next edges form a cycle; use on_fail with a max for loops")
				break
			}
		}
	}
}

func hasNextCycle(f Flow, start string) bool {
	seen := map[string]bool{}
	for cur := start; cur != ""; cur = f.Stages[cur].Next {
		if seen[cur] {
			return true
		}
		seen[cur] = true
	}
	return false
}

func (c *Config) validateOperations(p *problems, opts Options) {
	if c.StallTimeout < 0 {
		p.add("stall_timeout", "cannot be negative")
	}
	r := c.Retry
	if r.BaseDelay < 0 || r.MaxDelay < r.BaseDelay || r.MaxAttempts < 0 {
		p.add("retry", "base_delay must not exceed max_delay, and nothing can be negative")
	}
	l := c.Limits
	if l.MaxConcurrentRuns < 0 || l.PerRepo < 0 {
		p.add("limits", "limits cannot be negative")
	}
	for _, k := range sortedKeys(l.PerRuntime) {
		if !slices.Contains(opts.runtimes(), k) {
			p.add("limits.per_runtime."+k, "unknown runtime")
		}
		if l.PerRuntime[k] < 1 {
			p.add("limits.per_runtime."+k, "must be at least 1")
		}
	}
	if l.PerRuntime["windows-sandbox"] > 1 {
		p.add("limits.per_runtime.windows-sandbox", "the host sandbox runs one session at a time")
	}
	for _, k := range sortedKeys(l.PerProject) {
		if _, ok := c.Projects[k]; !ok {
			p.add("limits.per_project."+k, "unknown project")
		}
		if l.PerProject[k] < 1 {
			p.add("limits.per_project."+k, "must be at least 1")
		}
	}
	for name, h := range map[string]*Hook{"before_stage": c.Hooks.BeforeStage, "after_stage": c.Hooks.AfterStage,
		"on_fail": c.Hooks.OnFail, "on_done": c.Hooks.OnDone, "before_remove": c.Hooks.BeforeRemove} {
		if h == nil {
			continue
		}
		if len(h.Command) == 0 || h.Command[0] == "" {
			p.add("hooks."+name+".command", "a hook needs a command (an argument list, run without a shell)")
		}
		if h.Timeout < 0 {
			p.add("hooks."+name+".timeout", "cannot be negative")
		}
	}
	validateOutput(p, "output", c.Output)
	sort.SliceStable(*p, func(i, j int) bool { return (*p)[i].Error() < (*p)[j].Error() })
}
