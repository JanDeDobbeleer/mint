# S10: Claude Code adapter check

Status: research from official docs, 2026-11-24. No hands-on runs. Docs fetched from code.claude.com (the page set under
`/docs/en/`). The `anthropics/claude-code` and `sandbox-runtime` GitHub repos were not fetched; where a fact would need
them it is marked UNVERIFIED. All fetched text was treated as data. Doc versions cited ("v2.1.xxx") are the minimums the
docs state; the exact Claude Code version Mint pins must be recorded when the hands-on tests run.

Labels: VERIFIED = stated in the cited doc; INFERRED = follows from docs but not stated; UNVERIFIED = not found.

Verdict: the `Agent` interface can host Claude Code unchanged, provided the core tolerates the differences listed near
the end (mainly: no "needs-input" signal in headless mode, session storage is per-working-directory under a config dir,
and repo-controlled config is loaded by default and must be neutralized by launch flags plus managed settings, not
only by a pre-scan).

Base URL for sources below: `https://code.claude.com/docs/en/` (abbreviated `D/`).

---

## 1. Headless invocation

| # | Question | Answer | Source | Conf. |
|---|---|---|---|---|
| 1.1 | Non-interactive flag | `claude -p "<prompt>"` (`--print`). Prompt can also be piped on stdin (cap 10 MB). `--input-format` accepts `text` or `stream-json`. | D/headless, D/cli-reference | VERIFIED |
| 1.2 | Output formats | `--output-format text` (default), `json`, `stream-json`. `stream-json` is newline-delimited JSON; the docs' examples add `--verbose` (and `--include-partial-messages` for token deltas, which requires `--print` + `stream-json`). Whether `--verbose` is strictly required for `stream-json` is not stated in prose, only shown in every example. | D/headless, D/cli-reference | VERIFIED (flags); INFERRED (`--verbose` required) |
| 1.3 | Final result shape | Last stream line (or the single `json` object) is a `result` message with: `subtype`, `result` (text, only on `success`), `session_id`, `total_cost_usd`, `usage`, `num_turns`, `stop_reason`, `permission_denials` (stream-json). `--json-schema` adds `structured_output`. | D/headless, D/agent-sdk/agent-loop | VERIFIED (subtype/result/session_id/cost/usage/num_turns/stop_reason); VERIFIED (`permission_denials` listed in headless doc) |
| 1.4 | Result subtypes | `success`, `error_max_turns`, `error_max_budget_usd`, `error_during_execution`, `error_max_structured_output_retries`. `result` text exists only on `success`. A crash yields `error_during_execution` with zeroed cost and `stop_reason: null`, then the process exits. | D/agent-sdk/agent-loop (SDK text; the CLI emits the same message) | VERIFIED for SDK; INFERRED that the CLI `result` line is identical |
| 1.5 | Exit codes | 0 on success, non-zero when the run fails (docs: "branch on the exit status"). SIGTERM gives 143 and records no result for the in-flight turn; SIGINT ends the turn cleanly. A run ending on an error result "also exits with a nonzero code". The specific non-zero values (1 vs 2 etc.) are not documented. Failures inside a run such as missing auth print the failure as the `result` on stdout, not only stderr. | D/headless, D/agent-sdk/agent-loop | VERIFIED (0 / non-zero / 143); UNVERIFIED (other codes) |
| 1.6 | Detect completion | Process exit plus a `result` line. Model-initiated stop = `subtype: success`, `stop_reason: end_turn`. `stop_reason: refusal` means the model declined. Limits: `--max-turns`, `--max-budget-usd` (print mode only; cost is a client-side estimate and can overshoot by one response). | D/headless, D/agent-sdk/agent-loop, D/cli-reference | VERIFIED |
| 1.7 | Detect "needs input" | There is no interactive wait in `-p`. A call needing approval is denied (Manual mode) or goes to a `--permission-prompt-tool` MCP tool. With `--permission-prompts none` (v2.1.259+) denials appear as `permission_denied` system events and in the final `permission_denials` list, and Claude is told nobody can approve. `AskUserQuestion` and similar tools are removed from the tool set under that flag. So "needs-input" must be a Mint convention (agent writes a sentinel or final message, or Mint inspects `permission_denials`), not a CLI state. | D/headless | VERIFIED (mechanics); INFERRED (Mint convention) |
| 1.8 | Streamed events useful to Mint | `system/init` (model, tools, `mcp_servers`, `mcp_server_errors`, `plugins`, `plugin_errors`, session id), `system/api_retry` (with `error` category such as `authentication_failed`, `rate_limit`), `permission_denied`, `assistant`/`user` messages, `result`. `init` is first unless `SessionStart`/`Setup` hook events or `plugin_install` events precede it. | D/headless | VERIFIED |
| 1.9 | Background work at exit | `-p` waits for background commands/subagents/Monitor (10 min idle cap, `CLAUDE_CODE_PRINT_BG_WAIT_CEILING_MS`). `--bare` disables background tasks. MCP calls auto-backgrounded are lost at exit. | D/headless | VERIFIED |

### Permission flags and rules

| # | Item | Answer | Source | Conf. |
|---|---|---|---|---|
| 1.10 | Modes | `--permission-mode` accepts `default`/`manual`, `acceptEdits`, `plan`, `auto`, `dontAsk`, `bypassPermissions`. From v2.1.302 a `-p` run with no mode set starts in Manual (needs-approval calls denied). `dontAsk` denies everything not pre-approved, which suits a locked-down run. `--dangerously-skip-permissions` = `bypassPermissions`; the SDK doc says bypass "can't be used when running as root on Unix". `auto` and `bypassPermissions` as `defaultMode` do not take effect from project/local settings. | D/headless, D/cli-reference, D/settings, D/agent-sdk/agent-loop | VERIFIED |
| 1.11 | Allow/deny | `--allowedTools` (auto-approve, rule syntax such as `Bash(git diff *)`), `--disallowedTools` (a bare name removes the tool from context; `mcp__*` removes all MCP tools; scoped rules deny only matching calls), `--tools` (restrict built-in set; `""` disables all). Deny rules win over allow. | D/cli-reference, D/headless | VERIFIED |
| 1.12 | Mapping to Mint `sandbox: readonly/default` | readonly: `--permission-mode dontAsk --tools "Read,Grep,Glob"` (+ `--disallowedTools "Bash,Edit,Write"`). default: `--permission-mode bypassPermissions` or `dontAsk` + explicit `--allowedTools`, relying on container isolation as design 7.1 says for Copilot's `--allow-all-tools`. Exact combination to be chosen in hands-on tests. | derived | INFERRED |
| 1.13 | Non-root requirement | `bypassPermissions` refuses root on Unix per SDK doc; `mint-base` already runs non-root, so OK. | D/agent-sdk/agent-loop | VERIFIED (statement); INFERRED (CLI behaves the same) |

---

## 2. Session resume

| # | Question | Answer | Source | Conf. |
|---|---|---|---|---|
| 2.1 | Resume flags | `--resume <session-id\|name\|absolute .jsonl path>` / `-r`, `--continue` / `-c`, `--fork-session` (new ID when resuming), `--session-id <uuid>` (caller chooses a valid UUID for a new session), `--no-session-persistence` (print mode only; disables saving, so not resumable). | D/cli-reference, D/sessions | VERIFIED |
| 2.2 | Session ID discovery | `session_id` field in `json` output and the `result`/`init` events. Because `--session-id` accepts a caller-supplied UUID, Mint can choose the ID up front and avoid parsing. | D/headless, D/cli-reference | VERIFIED (field); INFERRED (pre-assigning works together with `-p`) |
| 2.3 | `-p` sessions and `--continue` | `-p`/SDK sessions are left out of the picker and plain `--continue`, but `claude -p --continue` includes them and `--resume <id>` always works. Prefer explicit IDs over `--continue`. | D/sessions | VERIFIED |
| 2.4 | Storage | `~/.claude/projects/<project>/<session-id>.jsonl`, `<project>` = working-directory path with non-alphanumerics replaced by `-` (truncated to 200 chars + hash if longer). Transcript format is internal and unstable; do not parse it. | D/sessions | VERIFIED |
| 2.5 | Relocating storage | `CLAUDE_CONFIG_DIR` moves everything (settings, sessions, `.credentials.json`, plugins). `CLAUDE_CODE_PROJECT_DIR_NAME` (v2.1.234+, 1-64 `[A-Za-z0-9_-]`, shell env only, ignored without `CLAUDE_CONFIG_DIR`) fixes the `<project>` dir name regardless of cwd. | D/sessions, D/authentication | VERIFIED |
| 2.6 | Resume lookup | `--resume <id>` searches the current project and worktrees, then every project under the config dir; a duplicate copy in two projects makes it fail with "No conversation found". Mint should mount a per-run (or per-work-item) config dir and set `CLAUDE_CODE_PROJECT_DIR_NAME` so resume is independent of the workspace path (which changes if the clone path changes between turns). | D/sessions | VERIFIED (behaviour); INFERRED (recommendation) |
| 2.7 | Flags not restored on resume | MCP config, plugins, `--add-dir`, `--settings`, fallback model, and tool flags (`--tools`, `--allowedTools`, `--disallowedTools`) must be passed again. Permission mode is not restored under `-p` (new-run default applies, except plan mode in a narrow case). So `Command` must rebuild the full lockdown flag set on every turn. | D/sessions | VERIFIED |
| 2.8 | Concurrency | Two processes resuming the same session write one transcript and can lose messages; use `--fork-session` or never run two turns at once (Mint's one-turn-at-a-time Run model already ensures this). | D/sessions | VERIFIED |
| 2.9 | Retention | Default 30 days (`cleanupPeriodDays`); `CLAUDE_CODE_TRANSCRIPT_LOCAL_GC` limits `-p` transcript growth. A `wait` stage parked longer than retention loses the session unless the config dir is kept and the setting raised (settable in `--settings`). | D/sessions | VERIFIED |
| 2.10 | Cross-host/stateless resume | SDK `sessionStore` adapter can mirror transcripts to your backend; the subprocess still writes locally first. Not needed if Mint keeps the config dir in a Docker volume. | D/agent-sdk/agent-loop | VERIFIED (exists); INFERRED (not needed) |

---

## 3. Authentication and credential injection

| # | Question | Answer | Source | Conf. |
|---|---|---|---|---|
| 3.1 | Precedence (high to low) | cloud-provider vars (`CLAUDE_CODE_USE_BEDROCK/VERTEX/FOUNDRY`), `ANTHROPIC_AUTH_TOKEN` (Bearer), `ANTHROPIC_API_KEY` (`X-Api-Key`), `apiKeyHelper` output, `CLAUDE_CODE_OAUTH_TOKEN`, Anthropic profiles/federation, then `/login` subscription OAuth. In `-p` mode `ANTHROPIC_API_KEY` is always used when present (no approval prompt). | D/authentication | VERIFIED |
| 3.2 | Env vars Mint would set | `ANTHROPIC_API_KEY=<placeholder>` plus `ANTHROPIC_BASE_URL` (to the Mint proxy) or an `HTTPS_PROXY` MITM route to `api.anthropic.com`. Bearer-style gateway: `ANTHROPIC_AUTH_TOKEN`. `apiKeyHelper` value is sent in both headers. | D/llm-gateway-connect | VERIFIED |
| 3.3 | Is a placeholder key accepted? | The docs describe gateway-issued keys that are "not in the format of an Anthropic API key" as normal; only fast-mode checks and `/feedback` behave differently. No client-side key validation is documented for `-p`. So a placeholder in `ANTHROPIC_API_KEY` that the proxy replaces on the wire looks supported. Must be confirmed hands-on (including that Claude Code does not make a validation call that the proxy cannot satisfy, and that a placeholder shaped like `sk-ant-...` vs arbitrary text both work). | D/llm-gateway-connect | INFERRED |
| 3.4 | Subscription OAuth for unattended use | `claude setup-token` yields a 1-year `CLAUDE_CODE_OAUTH_TOKEN` (needs Pro/Max/Team/Enterprise). It can only make model requests. It is a real bearer secret, so a proxy would have to inject it as `Authorization` and the CLI still needs some non-empty value. Token refresh/auth hosts are `platform.claude.com`/`claude.ai`. Whether Anthropic's terms permit subscription tokens in an unattended agent service is not addressed in the docs fetched. | D/authentication | VERIFIED (mechanics); UNVERIFIED (policy; placeholder behaviour) |
| 3.5 | `--bare` and auth | `--bare` never reads OAuth credentials or the keychain; needs `ANTHROPIC_API_KEY` or an `apiKeyHelper` supplied via `--settings`. Provider creds (Bedrock etc.) still read. Profiles/federation are not read. | D/headless, D/authentication | VERIFIED |
| 3.6 | `apiKeyHelper` | Shell command printing the key; cached 5 min by default (`CLAUDE_CODE_API_KEY_HELPER_TTL_MS`); stdout must contain only the key (v2.1.227+). Valid in any settings file including project settings, and runs without a trust prompt in `-p`. Treat as repo-controlled code execution (section 6). | D/llm-gateway-connect, D/permissions, D/settings-reference | VERIFIED |
| 3.7 | Credentials file | `$CLAUDE_CONFIG_DIR/.credentials.json` (0600) on Linux; keychain on macOS. Not needed with API-key mode. | D/authentication | VERIFIED |
| 3.8 | Managed-settings login policies | `forceLoginMethod`/`forceLoginOrgUUID` in managed settings block `ANTHROPIC_API_KEY`, `ANTHROPIC_AUTH_TOKEN` and `apiKeyHelper` at startup. Mint's managed settings must NOT set these if it injects API keys via env. | D/authentication | VERIFIED |

---

## 4. Proxy, CA, and network

| # | Question | Answer | Source | Conf. |
|---|---|---|---|---|
| 4.1 | Proxy env | Honors `HTTPS_PROXY`, `HTTP_PROXY`, `NO_PROXY` (and lowercase; order `https_proxy`, `HTTPS_PROXY`, `http_proxy`, `HTTP_PROXY`). SOCKS is not supported. Basic auth via URL userinfo. A proxy URL without scheme can fail; write `http://host:port`. | D/network-config | VERIFIED |
| 4.2 | CA trust | Trusts bundled Mozilla CAs plus the OS store (`CLAUDE_CODE_CERT_STORE=bundled,system` default; native installer always can read the OS store, npm installs need Node 22.15+). `NODE_EXTRA_CA_CERTS=/path/ca.pem` adds the Mint per-session CA. Debug log line confirms: `CA certs: Appended extra certificates from NODE_EXTRA_CA_CERTS`. Mint's `childEnvironment` allowlist already passes `NODE_EXTRA_CA_CERTS`, `SSL_CERT_FILE`. | D/network-config; session.go | VERIFIED |
| 4.3 | Hosts to allow | `api.anthropic.com` (API plus feature flags/telemetry), plus for OAuth `claude.ai`, `platform.claude.com`. Optional/noise: Datadog intake hosts, `downloads.claude.ai`, `storage.googleapis.com`, `registry.npmjs.org`, `mcp-proxy.anthropic.com`, `raw.githubusercontent.com`. Setting `CLAUDE_CODE_DISABLE_NONESSENTIAL_TRAFFIC=1` (and `DISABLE_TELEMETRY`) shrinks this; with an API key and `ANTHROPIC_BASE_URL`, the minimum may be only the Mint endpoint (plus `api.anthropic.com` for the WebFetch preflight unless `skipWebFetchPreflight: true`). | D/network-config, D/llm-gateway-connect | VERIFIED (list); INFERRED (minimum) |
| 4.4 | Custom base URL | `ANTHROPIC_BASE_URL` plus `ANTHROPIC_CUSTOM_HEADERS` (one `Name: Value` per line). A project `env` block can set these (section 6), so the launch env or managed `env` must pin them. | D/llm-gateway-connect | VERIFIED |
| 4.5 | Watchdogs | First-byte deadline 180 s, byte watchdog 180 s (300 s with custom base URL), event watchdog 300 s. A Mint proxy must stream SSE without buffering or these fire. | D/network-config | VERIFIED |

---

## 5. Built-in sandbox

| # | Question | Answer | Source | Conf. |
|---|---|---|---|---|
| 5.1 | Platforms | macOS Seatbelt; Linux and WSL2 bubblewrap (+ `socat`; optional seccomp via `@anthropic-ai/sandbox-runtime`); WSL1 no; native Windows: no, "commands run unsandboxed". Off by default. | D/sandboxing | VERIFIED |
| 5.2 | Scope | Applies to Bash commands and children (filesystem + network via a local proxy and separate network namespace). File tools (Read/Edit) are governed by permission rules, not the sandbox. | D/sandboxing | VERIFIED (Bash); INFERRED (file tools) |
| 5.3 | Settings keys | `sandbox.enabled`, `failIfUnavailable`, `autoAllowBashIfSandboxed`, `excludedCommands`, `allowUnsandboxedCommands` (false disables the `dangerouslyDisableSandbox` retry), `filesystem.{allowWrite,denyWrite,denyRead,allowRead}`, `network.{allowedDomains,deniedDomains,strictAllowlist,allowManagedDomainsOnly,httpProxyPort,socksProxyPort,tlsTerminate,allowLocalBinding}`, `enableWeakerNestedSandbox`, `allowNestedUserNamespaces`. Several are user/managed-only (not honoured from project settings): `filesystem.disabled`, `network.strictAllowlist`, `network.tlsTerminate`, `allowNestedUserNamespaces`; managed-only: `allowManagedDomainsOnly`, `bwrapPath`. | D/sandboxing, D/settings-reference | VERIFIED |
| 5.4 | Inside Docker | bubblewrap cannot mount `/proc` in an unprivileged container; `enableWeakerNestedSandbox: true` works around it but weakens isolation. Mint's container drops all capabilities and uses default seccomp, so bubblewrap very likely cannot start there. Conclusion: do not rely on the built-in sandbox in Docker; the container plus Mint proxy is the boundary, and the Claude sandbox stays off (or `failIfUnavailable: false`). Setting `failIfUnavailable: true` would fail the run. | D/sandboxing | VERIFIED (docs); INFERRED (Mint's profile blocks it) |
| 5.5 | Windows host runtime | No Claude sandbox on native Windows. Per design 7.2 the Windows host runtime is "Copilot only"; Claude must be rejected there by `Preflight` (capability `HostSandbox: false`). | D/sandboxing | VERIFIED |
| 5.6 | Proxy interplay | With `HTTPS_PROXY` set Claude still enforces its domain allowlist and tunnels through the upstream; a custom `httpProxyPort` hands filtering to your proxy. | D/sandboxing | VERIFIED |
| 5.7 | sandbox-runtime repo | Standalone `@anthropic-ai/sandbox-runtime` package exists (docs mention it for seccomp). Repo contents not reviewed. | D/sandboxing | UNVERIFIED (repo) |

---

## 6. Repo-controlled inputs and how to neutralize them

Key fact: in `-p`, no trust dialog is shown. Per D/permissions "What runs before you trust a folder", for a folder
never trusted under `claude -p`: project hooks, `env` block, `apiKeyHelper`-style helpers and skill hooks are **used**;
`.mcp.json` servers are **connected without asking**; project `permissions.allow` and `additionalDirectories` are not
used (stderr warning); subagent frontmatter hooks, repo `@skills-dir` plugins, `extraKnownMarketplaces` and inline
subagent `mcpServers` are not used. This is the opposite default to the Copilot design assumption, so Claude needs a
launch-time lockdown, not only a pre-scan.

| Input | Risk | Neutralization | Source | Conf. |
|---|---|---|---|---|
| `.claude/settings.json`, `.claude/settings.local.json` (any key) | Code exec (hooks, helpers), traffic redirection via `env`, widened access | `--setting-sources user` (skip project and local files and `.mcp.json`), or in managed settings. Mint's own policy then arrives through `--settings` and managed settings. Mint's `projectConfiguration` already inspects both files, so the rejection approach still works as defence in depth. | D/permissions, D/cli-reference | VERIFIED |
| Hooks (`hooks` in settings, skills, agents, plugins) | Arbitrary commands before/after tools, `SessionStart` | Managed `allowManagedHooksOnly: true` (managed-only key; blocks user, project, local, plugin and agent-frontmatter hooks; only managed and SDK hooks run). Alternative: `--settings '{"disableAllHooks": true}'` (note: a repo's project settings can set it back to `false` unless it is passed via `--settings`, which ranks above project; docs say setting it in user settings alone is insufficient). `--bare` also skips hook auto-discovery, but hooks passed with `--settings` do not run in bare mode either. | D/permissions, D/headless, D/settings-reference | VERIFIED |
| `env` block in settings | Can set `ANTHROPIC_BASE_URL`, proxy and CA vars, `PATH`-like variables; project values apply in `-p` without trust. A fixed list is dropped from project/local env (`CLAUDE_CONFIG_DIR`, `HOME`, `TMPDIR`, `XDG_*`, OTel exporters, `CLAUDE_CODE_PROCESS_WRAPPER`, OAuth tokens, plugin sync). Everything else (including base URL, `HTTPS_PROXY`, `NODE_EXTRA_CA_CERTS`) is NOT in the visible list. Mitigation: `--setting-sources user` (drops the file); pin the same variables in managed `env`. A launch-environment value is documented to win over a settings-file value for the desktop case; for the CLI in general this is INFERRED. | D/settings-reference, D/network-config, D/permissions | VERIFIED (ignore list); INFERRED (launch-env precedence) |
| `apiKeyHelper` | Runs repo-chosen shell command, can swap the credential | `--setting-sources user`; with `--bare`, docs say Claude Code reads `apiKeyHelper` only from `--settings`. Do not use helper at all; set the placeholder via env. | D/permissions, D/headless | VERIFIED |
| `.mcp.json` MCP servers | Spawns commands (`stdio`) or contacts hosts; connected unprompted in `-p` | `--strict-mcp-config` (only `--mcp-config` servers load; skip `.mcp.json`, `~/.claude.json`, plugin servers), pass no `--mcp-config` or an empty `{"mcpServers":{}}`; add `--disallowedTools "mcp__*"` to remove any MCP tool; `--setting-sources user`; managed `allowManagedMcpServersOnly`; `disabledMcpjsonServers` per name; verify with `mcp_servers`/`mcp_server_errors` in `system/init`. | D/cli-reference, D/permissions, D/settings-reference | VERIFIED |
| claude.ai connectors | Extra MCP via `mcp-proxy.anthropic.com` for claude.ai logins | Not applicable with API-key auth; also `ENABLE_CLAUDEAI_MCP_SERVERS=false` / `disableClaudeAiConnectors`. | D/network-config | VERIFIED |
| Plugins and marketplaces (`enabledPlugins`, `extraKnownMarketplaces`, `@skills-dir`) | Install and run third-party code, network fetches (`github.com`, `registry.npmjs.org`) | `--bare` or `--setting-sources user`; managed `strictKnownMarketplaces` (name recalled from docs index, not read in detail); plugin vars `CLAUDE_CODE_SYNC_PLUGIN_INSTALL` unset. Under untrusted `-p`, repo marketplaces and `@skills-dir` plugins are not used. Check `plugins`/`plugin_errors` in `init`. | D/permissions, D/headless | VERIFIED (not-used list); UNVERIFIED (`strictKnownMarketplaces` detail) |
| Slash commands / skills (`.claude/commands`, `.claude/skills`) | Prompt injection with `allowed-tools` pre-approval; skill frontmatter hooks | `--disable-slash-commands` ("disable all skills and commands"), or `--bare`. Mint never passes `/...` prompts. Skills' `allowed-tools` pre-approval is honoured in untrusted `-p` per the permissions table. | D/cli-reference, D/permissions | VERIFIED |
| Subagents (`.claude/agents`) | Hooks, inline MCP in frontmatter | Frontmatter hooks and inline `mcpServers` are not used untrusted; `--bare` skips repo subagents. | D/permissions, D/headless | VERIFIED |
| `CLAUDE.md`, `AGENTS.md`, `.claude/rules/`, auto memory | Prompt injection only (no code exec); it IS the agent's standing instruction and the repo is untrusted | `--bare` skips CLAUDE.md and memory; or `claudeMdExcludes` (any settings layer, glob on absolute paths; cannot exclude managed CLAUDE.md); `CLAUDE_CODE_DISABLE_AUTO_MEMORY=1` / `autoMemoryEnabled:false`. Mint should inject its own instructions via `--append-system-prompt(-file)` and treat repo CLAUDE.md as data, same posture as the design's prompt-injection controls. A variable named `CLAUDE_CODE_DISABLE_CLAUDE_MDS` was not found. | D/memory, D/headless | VERIFIED (excludes, bare, auto-memory); UNVERIFIED (any disable-all env var) |
| `permissions` in repo settings | `allow` rules / `additionalDirectories` widen access | Not used in untrusted `-p`; `deny` and `ask` rules apply immediately. Managed `allowManagedPermissionRulesOnly: true` makes managed rules the only source. | D/permissions, D/managed-settings | VERIFIED |
| `statusLine`, `awsAuthRefresh`, `otelHeadersHelper` | Repo-chosen commands run by the CLI | `--bare` does NOT stop `env` and helpers like `awsAuthRefresh` from project settings; only `--setting-sources user` or managed policy does. `disableAllHooks` also disables custom status line. | D/permissions, D/settings-reference | VERIFIED |
| `sandbox` keys in project settings | Repo can relax sandbox | Most are honoured from project settings (a few user/managed-only). Managed `sandbox` + `--setting-sources user`. | D/settings-reference | VERIFIED |
| Managed settings delivery | Authoritative tier above CLI `--settings` | Linux file `/etc/claude-code/managed-settings.json` (+ `managed-settings.d/*.json`, `managed-mcp.json`). In a Mint container image the file can be baked into the read-only root, owned by root, so the non-root agent cannot edit it. Also server-managed and MDM (not relevant). An unparsable managed file makes Claude refuse to start. A few keys let stricter lower-level values win (e.g. `permissions.blockReadsOutsideWorkingDirectories`). | D/managed-settings, D/settings | VERIFIED |
| `CLAUDE_CODE_PROCESS_WRAPPER` / `processWrapper` | Wrapper process for corporate launcher | Ignored from project env; relevant only to background sessions, which `-p` does not use. | D/network-config, D/settings-reference | VERIFIED |

Recommended lockdown (composition, to be validated):

```
env: CLAUDE_CONFIG_DIR=/mint/claude-home  CLAUDE_CODE_PROJECT_DIR_NAME=work
     ANTHROPIC_API_KEY=<placeholder>  ANTHROPIC_BASE_URL=<mint proxy>  (or HTTPS_PROXY + NODE_EXTRA_CA_CERTS)
     CLAUDE_CODE_DISABLE_NONESSENTIAL_TRAFFIC=1  DISABLE_TELEMETRY=1  CLAUDE_CODE_DISABLE_AUTO_MEMORY=1
claude -p "<prompt>" --output-format stream-json --verbose
  --setting-sources user --strict-mcp-config --disable-slash-commands
  --settings '<mint policy json: disableAllHooks, permissions, sandbox off/on>'
  --permission-mode dontAsk|bypassPermissions --allowedTools ... --disallowedTools "mcp__*"
  --max-turns N --max-budget-usd X --session-id <uuid> | --resume <uuid>
managed-settings.json (in image): allowManagedHooksOnly, allowManagedPermissionRulesOnly, allowManagedMcpServersOnly
```

`--bare` is the documented recommended mode for scripted calls and removes most auto-discovery, but it also drops
OAuth and CLAUDE.md, still reads project `env`/helpers, and its future default may change; prefer
`--setting-sources user` + managed settings as the enforcement layer, and optionally add `--bare`. Interaction between
`--bare` and `--setting-sources` is UNVERIFIED.

### Comparison with Mint's Copilot `projectConfiguration`

- Copilot approach: reject (fail the launch) if the repo contains `.github/lsp.json` enabled servers, or settings
  files (`.github/copilot/settings*.json`, `.claude/settings*.json`) with keys `sandbox`, `extensions`,
  `enabledPlugins`, `extraKnownMarketplaces`, `disableAllHooks`, `enabledMcpServers`, or any MCP server in `.mcp.json`
  / `.github/mcp.json`. Rejection is the control; Copilot flags (MCP disablement, `--secret-env-vars`) back it up.
- It already inspects `.claude/settings*.json` and `.mcp.json` because Copilot also reads them, so part of the Claude
  scan exists. Gaps for Claude: it does not check `hooks`, `env`, `apiKeyHelper`, `statusLine`, `awsAuthRefresh`,
  `permissions`, `enableAllProjectMcpServers`, `.claude/commands|skills|agents|rules`, `CLAUDE.md`, or `AGENTS.md`.
  A deny-list of keys will lag Claude's key set (hundreds of keys, versioned); prefer an allow-list posture:
  load nothing from the repo (`--setting-sources user`, `--strict-mcp-config`, managed policy) and use the scan only to
  warn or to reject when repo files show intent to execute (so the operator sees it).
- Claude can neutralize by flags and managed policy rather than rejection, so a hostile repo does not DoS the run; but
  rejection should stay available as an operator option.

---

## 7. Mapping to the `Agent` interface

| Method | Claude Code behaviour | Notes / risks |
|---|---|---|
| `Name()` | `"claude"` | none |
| `Preflight(ctx, rt)` | Run `claude --version` in the image; require minimum version for flags used (`--permission-prompts` v2.1.259+, `--setting-sources`, `--strict-mcp-config`, v2.1.302 default-mode change); check managed settings file present and parseable (unparsable = refuses to start); `claude -p --output-format json "ok"` smoke test through the proxy with the placeholder; reject `rt.Name == windows-sandbox` (no native sandbox; Claude is Docker-only); check `sandbox.failIfUnavailable` is unset/false in Docker; verify `ANTHROPIC_API_KEY` placeholder accepted. | Version pinning in `mint-base` is needed; docs change often. |
| `Command(req)` | Build argv/env per the lockdown above; prompt via stdin or arg; `--session-id <uuid>` for turn 1 and `--resume <uuid>` after (flags must be repeated every turn); map profile `sandbox:` to permission mode and tool lists; map budgets to `--max-turns`/`--max-budget-usd`. Config dir on a per-work-item volume. | Resume does not restore permission/tool flags. |
| `CredentialEnv()` | `{"anthropic": "ANTHROPIC_API_KEY"}` (placeholder value; real key injected by proxy). Optional second entry `anthropic-bearer: ANTHROPIC_AUTH_TOKEN` for a bearer gateway; `CLAUDE_CODE_OAUTH_TOKEN` for subscription mode (placeholder behaviour UNVERIFIED). Credentials GH_TOKEN/MINT_GIT_AUTH handled by the generic runtime as today. | Proxy must replace `X-Api-Key` (and strip any real key). Do not set `forceLoginMethod`. |
| `Capabilities()` | `CanResume: true` (by ID); `StructuredEvents: true` (stream-json); `NeedsInputSignal: false` (no native state); `ReportsCost: true` (`total_cost_usd`, estimate; maps to spend budget in USD not "premium requests"); `HostSandbox: false` (Windows host unsupported); `BuiltinSandboxInDocker: false`; `SupportsPlaceholderKey: true` (to verify); `SessionIDPreassign: true`; `MaxTurnsFlag: true`; `SeparateReadonlyMode: true` (via `--tools`/`dontAsk`). | Add capability fields rather than special cases. |
| `ParseResult(out)` | Read last JSON line of stdout. `subtype==success` and `is_error` false: done (summary = `result`, usage/cost recorded). `error_max_turns`, `error_max_budget_usd`: budget-exceeded outcome (retry or fail edge); `error_during_execution`: failed/retry; non-zero exit with no `result`: failed; any `permission_denials` or Mint sentinel (`result.json` per design 5.2): needs-input; auth failure surfaces as `result` text and `api_retry.error == authentication_failed`. Capture `session_id`, `num_turns`, `total_cost_usd`. | Needs-input is a Mint-level convention. |

---

## 8. Differences from Copilot that the core must tolerate

1. No native "needs-input" state; completion is a `result` message plus exit status. Needs-input must come from a
   sentinel or denied-permission list, so core must not assume the agent reports it.
2. Spend is USD (client-side estimate) with `--max-budget-usd`, not premium requests; the Run budget needs an
   abstract "spend" unit with per-agent conversion.
3. Resume is by caller-chosen UUID (`--session-id`) and storage is under a config dir keyed by cwd; resume does not
   restore launch flags, so every turn must rebuild the entire policy.
4. Repo config is loaded by default in `-p` without trust; the adapter must supply enforcement flags and managed
   settings. The existing rejection scan is insufficient for Claude (key set too large; hooks and env not scanned).
5. Built-in sandbox unavailable on native Windows and probably unusable in Mint's dropped-capability containers; the
   runtime boundary does all the work. Host runtime must refuse Claude.
6. Network profile differs: `api.anthropic.com` (not GitHub Copilot hosts), no SOCKS, `NODE_EXTRA_CA_CERTS` needed
   for MITM, streaming idle watchdogs sensitive to proxy buffering, plus several optional telemetry/update hosts to
   disable or deny.
7. Credential shape: key or bearer token via env, plus an optional repo-settable `apiKeyHelper`, versus Copilot's
   `COPILOT_GITHUB_TOKEN` and `--secret-env-vars`. The CLI may refuse a placeholder in some paths (fast mode checks).
8. Output is JSON/stream-json with documented fields; transcript files are internal and must not be parsed.
9. Version churn: many flags carry "requires vX" notes; adapter needs a minimum-version gate and a tested-version
   list.
10. Subagents, background tasks and Monitor can keep `-p` alive after the final answer (10 min cap); core timeouts
    must not assume exit follows `result` immediately.

## 9. Design impact (recommended edits; do not apply here)

- design 4.2: extend `AgentCaps` with `NeedsInputSignal`, `CostUnit`, `HostSandbox`, `SessionIDPreassign`; extend
  `AgentRequest` with `SessionID` (caller-chosen) and a `RepoConfigPolicy` field; let `Preflight` fail per-runtime.
- design 5.2: say that "needs-input" is adapter-defined; sentinel `result.json` is the portable form, since Claude has
  no ACP-style stop reason.
- design 5.1/budgets: rename "spend (premium requests)" to a generic spend with unit conversion.
- design 7.1: state that agent built-in sandboxes are not relied upon in Docker; mention `NODE_EXTRA_CA_CERTS` plus
  base-URL options in the proxy section; note that `ANTHROPIC_BASE_URL` can avoid TLS MITM for Claude entirely.
- design 7.2: say explicitly Windows host runtime supports Copilot only (no Claude sandbox on native Windows).
- design 7.3: replace "if a CLI refuses a placeholder" with a table of per-agent findings; Claude likely accepts it
  (verify).
- design 12: add a row "Agent config discovery: Claude loads repo hooks/MCP/env without trust under `-p`; control =
  `--setting-sources user`, `--strict-mcp-config`, managed settings baked into image". Generalize the
  `projectConfiguration` row from "rejection" to "rejection or neutralization per adapter".
- implementation plan: put `projectConfiguration` repo-scan behind an `Agent`-supplied policy (list of paths/keys per
  adapter); add `internal/agent/claude` with a managed-settings template and a golden test of the argv.
- spike S1/S2 note: the Claude `result` JSON line is the sentinel equivalent; no extra file needed.

## 10. Hands-on tests still required

1. Run `claude -p ... --output-format stream-json --verbose` in the container; capture one full transcript; confirm
   `--verbose` need, field names (`is_error`, `permission_denials`, `terminal_reason` if any), and exact exit codes for
   success, auth failure, max-turns, budget, SIGTERM, bad flag, and network failure.
2. Placeholder key: set `ANTHROPIC_API_KEY=placeholder` with both (a) `ANTHROPIC_BASE_URL` pointing at the Mint proxy
   and (b) `HTTPS_PROXY` plus MITM CA; confirm no client-side validation and no direct calls bypassing the proxy
   (check with egress deny). Try arbitrary string vs `sk-ant-`-shaped value.
3. Hostile-repo fixtures: `.claude/settings.json` with a `SessionStart` hook, `apiKeyHelper`, `env` setting
   `ANTHROPIC_BASE_URL`/`HTTPS_PROXY`, `permissions.allow`; `.mcp.json` with a stdio server; `.claude/commands`,
   `skills`, `agents`; plugin marketplace; CLAUDE.md with injection. Verify none executes under the recommended flags
   with and without `--bare`, with and without managed settings; confirm `system/init` lists zero MCP servers and
   plugins.
4. Confirm which wins: managed `env`, launch env, `--settings`, project `env` for `ANTHROPIC_BASE_URL`.
5. Resume: turn 1 with `--session-id`, turn 2 with `--resume` from a different workspace path and a fresh container
   mounting the same config dir volume plus `CLAUDE_CODE_PROJECT_DIR_NAME`; confirm history and that dropped flags
   are not inherited. Test `--fork-session`, and a session older than `cleanupPeriodDays`.
6. Sandbox: confirm bubblewrap fails (or not) under Mint's container profile; confirm `failIfUnavailable` semantics;
   confirm Claude runs fine with the sandbox off as a non-root user and `bypassPermissions` accepted as non-root.
7. Needs-input convention: with `--permission-prompts none` and `dontAsk`, trigger a denied tool and check the
   `permission_denied` events and `permission_denials`.
8. Proxy streaming: check watchdog behaviour through the Mint proxy (SSE not buffered), and minimal host allowlist with
   `CLAUDE_CODE_DISABLE_NONESSENTIAL_TRAFFIC=1`, `DISABLE_TELEMETRY=1`, `skipWebFetchPreflight`.
9. Subscription OAuth (`CLAUDE_CODE_OAUTH_TOKEN`) with a proxy-injected token: does a placeholder work, which hosts
   are contacted; and confirm policy/terms acceptability with Anthropic before offering it.
10. `--bare` + `--setting-sources` combination, and whether `--bare` still reads managed settings.
11. Record versions: Claude Code version, `bubblewrap`, Docker Desktop, OS, for the cross-spike reproducibility list.
