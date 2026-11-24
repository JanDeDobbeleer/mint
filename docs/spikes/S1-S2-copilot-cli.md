# S1 and S2: Copilot CLI headless behaviour and ACP server (documentation research)

Date: 2026-11-24. Method: documentation only, no CLI installed or run. Latest stable release seen: 1.0.114 (18 Nov);
pre-releases 1.0.115-0..8 up to 23 Nov (<https://github.com/github/copilot-cli/releases>).

Source note: `docs.github.com` does not resolve from the research session (network allowlist). The same pages were read
as Markdown source from the public `github/docs` repository via `raw.githubusercontent.com`
(`content/copilot/...`). Those files contain unrendered Liquid tags in places, and WebFetch summarises pages through a
small model, so quoted details need the hands-on checks at the end. All fetched content was treated as data.

Source key (all under `https://raw.githubusercontent.com/github/docs/main/content/copilot/`):

- **[PROG]** `reference/copilot-cli-reference/cli-programmatic-reference.md`
- **[CMD]** `reference/copilot-cli-reference/cli-command-reference.md` (only the first 100k of 176k characters were read)
- **[ACP]** `reference/copilot-cli-reference/acp-server.md`
- **[DIR]** `reference/copilot-cli-reference/cli-config-dir-reference.md`
- **[HOWTO]** `how-tos/copilot-cli/automate-copilot-cli/run-cli-programmatically.md`
- **[AUTH]** `how-tos/copilot-cli/set-up-copilot-cli/authenticate-copilot-cli.md`
- **[LIMIT]** `how-tos/copilot-cli/set-up-copilot-cli/set-session-limit.md` (path as fetched: `how-tos/copilot-cli/use-copilot-cli/set-session-limit.md`)
- **[SBX-CFG]** `how-tos/cloud-and-local-sandboxes/configuring-local-sandbox-settings.md`
- **[SBX-USE]** `how-tos/cloud-and-local-sandboxes/using-local-sandboxing.md`
- **[SBX-CON]** `concepts/security-governance-and-network-settings/understanding-local-sandboxing.md`
- **[ACTIONS]** `how-tos/copilot-cli/use-copilot-cli-in-actions.md`
- **[REL]** `https://github.com/github/copilot-cli/releases`
- **[README]** `https://github.com/github/copilot-cli`

Labels: VERIFIED = stated in docs; INFERRED = deduced from docs; UNVERIFIED = needs a hands-on test.

---

## 1. S1: headless behaviour

### 1.1 Flags for non-interactive runs

| Flag | Meaning | Label | Source |
|---|---|---|---|
| `-p PROMPT` / `--prompt=PROMPT` | Run one prompt and exit. | VERIFIED | PROG |
| `-s` / `--silent` | Print only the agent response (no stats or decoration). | VERIFIED | PROG |
| `--no-ask-user` | Disable the `ask_user` tool; the agent works autonomously. It can still ask in plain text. | VERIFIED | PROG, CMD, HOWTO |
| `--model=MODEL`, `COPILOT_MODEL` | Model selection. Precedence: agent `model-policy: required` > `--model` > agent model > `COPILOT_MODEL` > `settings.json` `model` > default. | VERIFIED | PROG |
| `--no-auto-update`, `COPILOT_AUTO_UPDATE=false` | Pin version; otherwise a newer cached version may run. | VERIFIED | PROG |
| `--autopilot`, `--max-autopilot-continues N` (default 5), `--mode interactive/plan/autopilot` | Autopilot continues until `task_complete` is called. | VERIFIED | CMD |
| `--max-ai-credits N` | Soft cap on AI credits; in `-p` the run ends cleanly when reached. Minimum 30. Exit code not specified. | VERIFIED (exit code UNVERIFIED) | LIMIT, CMD |
| `-C DIRECTORY`, `--add-dir` | Working directory; extra allowed paths. | VERIFIED | CMD, PROG |
| `--log-dir`, `--log-level none..all`, `--no-color`, `--no-banner` | Logging and cosmetics. | VERIFIED | CMD |
| `--share[=PATH]`, `--share-gist` | Export transcript as Markdown after a non-interactive run (default `./copilot-session-<ID>.md`). May contain sensitive data. | VERIFIED | PROG |
| `--secret-env-vars=VAR ...` | Redact the named variables from shell and MCP environments. `GITHUB_TOKEN` and `COPILOT_GITHUB_TOKEN` redacted by default. | VERIFIED | PROG, CMD |
| `--disable-builtin-mcps`, `--disable-mcp-server`, `--additional-mcp-config` | MCP control (already used by Mint). | VERIFIED (names) | CMD |
| `--trust-folder` | Trust cwd hooks, workspace MCP and repo plugin settings for one new non-interactive session. Not saved; cannot combine with `--resume`, `--continue`, `--session-id`. | VERIFIED | PROG |
| `--no-experimental`, `--disable-client-telemetry`, `--disallow-temp-dir`, `--bash-env`/`--no-bash-env` | Listed without semantics in what was read. | UNVERIFIED (semantics) | CMD |
| `COPILOT_TASK_WAIT_TIMEOUT_SECONDS` (default 600) | Maximum wait in `-p` (and `-p --autopilot`) for pending background agents or shell commands before exiting. | VERIFIED | CMD |

Notes:

- Piped stdin is ignored if `-p` is also given (HOWTO).
- Project extensions, repo hooks and workspace MCP load in prompt mode only when explicitly enabled
  (`GITHUB_COPILOT_PROMPT_MODE_EXTENSIONS=true`, or trusted folder). This is good for Mint: a repo cannot inject code
  into a headless run by default (CMD, PROG). Label: VERIFIED.

### 1.2 Permission control (`--allow-tool`, `--deny-tool`, and friends)

All VERIFIED in PROG unless noted.

- Blanket: `--allow-all` (alias `--yolo`) = `--allow-all-tools --allow-all-paths --allow-all-urls`; also `COPILOT_ALLOW_ALL=true`.
- Targeted: `--allow-tool=TOOL ...`, `--deny-tool=TOOL ...`, `--allow-url=URL ...`, `--deny-url=URL ...`, `--add-dir=DIR`.
- Restrict the model's toolset: `--available-tools=TOOL ...` (only these), `--excluded-tools=TOOL ...` (remove these).
- Lists are quoted and comma-separated.
- Deny beats allow, "even when --allow-all is set" (search-result summary of PROG; the page text says deny takes
  precedence for URLs). Label for deny-beats-`--allow-all-tools`: INFERRED, test it.
- Tool kinds: `shell`, `write`, `read`, `url`, `memory`, and MCP server names (e.g. `github`).
- Pattern syntax:
  - `shell(git:*)` all git subcommands; `shell(npm test)` exact command.
  - `write(path)`; a bare filename matches any path ending in it.
  - `url(github.com)`, `url(https://*.github.com)`, `url(https://docs.github.com/copilot/*)`.
  - `github(create_issue)` one MCP tool.
  - Wildcards only for `shell` subcommands and, for `url`, at the start of a hostname or end of a path.
- `--assisted-approval` / `COPILOT_ASSISTED_APPROVAL=true`: model-based review auto-approves eligible requests; the rest
  are denied with a reason returned to the agent. Non-interactive only (errors in interactive). It replaces
  `--allow-all-tools` for tool requests. If blocked by enterprise policy it warns on stderr and continues (any
  `--allow-all-tools` then applies unreviewed). Deny rules and sandbox still apply. This is an alternative to blanket
  allow that Mint should evaluate (security win, extra model cost and latency). UNVERIFIED in practice.
- In `-p`, anything not pre-approved is denied automatically; there are no prompts (ACTIONS, HOWTO).
- `permissions.disableBypassPermissionsMode: "disable"` in managed settings turns off all allow-all options (CMD). If an
  operator's machine has this policy, Mint's `--allow-all-tools` will not work. Mint should detect and fail closed.
- The design says `-p` "requires `--allow-all-tools`". That is WRONG per docs: `-p` works with targeted `--allow-tool`
  and denies the rest. Mint can use an allowlist (for example `shell(git:*)`, `shell(go:*)`, `write`) instead of
  allow-all. Label: VERIFIED.

### 1.3 Output formats and machine-readable events

- `--output-format=text` (default) or `json`; `json` emits JSONL (one object per line). VERIFIED (PROG, CMD).
- `--stream=on|off` exists (CMD). VERIFIED (name only).
- **No per-event schema for `-p` is documented** (PROG states this gap). UNVERIFIED. The only documented record is
  `workflow.result` for `copilot workflow run`, which is a different command.
- Indirect evidence (search results, not read at source): the Copilot SDK names events such as `assistant.message`
  (fields `content`, `messageId`) and `session.task_complete` (optional `summary`); the session log
  `session-state/<id>/events.jsonl` uses JSONL. INFERRED that `-p --output-format json` reuses these types. Hands-on
  test required before writing `ParseResult`.
- The exit summary of a `-p` text run prints a hint `copilot --resume=SESSION-ID` (PROG). In text mode this is the only
  documented way to learn the session ID from output. VERIFIED.

### 1.4 Exit codes and done / needs-input signalling

- `-p` exits nonzero if the run is interrupted or the final response is completely empty. No other codes documented.
  VERIFIED (PROG, CMD).
- No documented distinct code for tool denial, auth failure, rate limit, `--max-ai-credits` reached, or network error.
  UNVERIFIED-needs-hands-on-test.
- `copilot workflow run` has codes 0/1/2/130, but workflows do not support `-p`, `--resume`, `--continue`, `--agent`
  (PROG). Not usable for Mint's agent turns. VERIFIED.
- "Needs input": with `--no-ask-user` there is no interactive question; unapproved actions are denied and the agent
  continues (HOWTO, ACTIONS). So `-p` has no needs-input state. Mint must therefore model "needs input" itself, either
  via a sentinel file (`result.json`) that the prompt asks the agent to write, or via the final message. INFERRED.
- Completion signal options: process exit (0 plus non-empty final response), JSONL final events (UNVERIFIED), the
  `task_complete` tool in autopilot (`--autopilot`), or a Mint sentinel. Recommendation: exit status plus sentinel file,
  confirmed by the JSON stream once the schema is captured.
- stderr: MCP connection failures in `-p` text mode print a warning on stderr (1.0.115-2, REL). Errors for incomplete
  runs go to stderr (workflow command only, PROG).

### 1.5 Sessions, resume, `COPILOT_HOME`

- `COPILOT_HOME` replaces the whole `~/.copilot` directory (replaces deprecated `--config-dir`). VERIFIED (DIR, CMD).
- Layout (DIR, VERIFIED):
  - User-editable: `settings.json`, `copilot-instructions.md`, `instructions/`, `mcp-config.json`, `lsp-config.json`,
    `providers.json`, `agents/`, `skills/`, `hooks/`, `extensions/`.
  - CLI-managed: `config.json` (auth, trusted folders, plugin metadata), `permissions-config.json`, `session-state/`,
    `command-history-state/`, `session-store.db` (SQLite, rebuildable), `logs/`, `installed-plugins/`, `plugin-data/`,
    `ide/`, `mcp-oauth-config/`, `mcp-secrets/`.
- Sessions: `session-state/<session-id>/events.jsonl` plus plans, checkpoints and tracked files. These enable
  `--resume` and `--continue`. Session ID format not stated in DIR. VERIFIED (location), UNVERIFIED (ID format; CMD says
  `--session-id` creates a new session only for a valid UUID, so IDs are UUIDs, INFERRED).
- The cache directory is NOT moved by `COPILOT_HOME` (macOS `~/Library/Caches/copilot`, Linux `$XDG_CACHE_HOME/copilot`
  or `~/.cache/copilot`, Windows `%LOCALAPPDATA%/copilot`). VERIFIED (DIR). Consequence: a per-run home does not isolate
  the cache; in containers, make the cache path writable and ephemeral.
- Logs: `logs/process-{timestamp}-{pid}.log` under the home, or `--log-dir`. VERIFIED.
- `--resume[=VALUE]`: VALUE may be a session ID, ID prefix, or name. Bare `--resume` opens a picker needing a TTY; under
  `-p`, a non-TTY `-i`, or piped stdin with multiple sessions, the CLI exits with an error rather than starting a new
  session. VERIFIED (CMD). So `--resume=<full-id> -p "..."` is the expected headless form. INFERRED that it works;
  UNVERIFIED hands-on.
- `--continue`: most recent session in the cwd, else global most recent; conflicts with `--resume`. VERIFIED (CMD). Do
  not use it for the daemon; always pass an explicit ID.
- `--session-id ID`: use an exact ID; creates a new session if the value is a valid UUID and nothing matches. VERIFIED
  (CMD). **This lets Mint choose the session UUID up front** (store it in the state comment before launch) instead of
  discovering it afterwards. Cannot combine with `--resume`, `--continue`, `--connect`, and `--trust-folder` cannot be
  combined with any of the three. Label: VERIFIED for semantics, UNVERIFIED that it works with `-p`.
- On `--resume`, approvals granted earlier in the session are restored; directory approvals and policy-granted approvals
  are not (CMD). INFERRED: re-pass `--allow-tool` flags on every resumed turn.
- `COPILOT_ENABLE_INTERRUPTED_SESSION_RESTORE=1` restores a session that was open when the process died (opt-in). VERIFIED
  (CMD, PROG). Relevant for crash recovery; UNVERIFIED hands-on.
- `--max-ai-credits` with `--resume` sets the resumed session's limit (1.0.115-2, REL). VERIFIED.
- Missing home or missing session directory: DIR says items are created on demand but does not describe a missing
  session on `--resume`. UNVERIFIED (expected: error exit; test it, since design 6.1 relies on a fallback).
- `--sync-session[=on|off]` (CMD) and "synced sessions stored in your GitHub account" (DIR): sessions can be synced to
  GitHub. Mint should pass `--sync-session=off` (INFERRED name and values; test) so that agent transcripts do not leave
  the machine unexpectedly.
- Sensitive content: `events.jsonl`, `--share` output and logs hold prompts, tool output and possibly file contents.
  Treat per-run homes as sensitive; delete at run end (matches design 6.1). INFERRED.

### 1.6 Per-run home contents needed

- Auth: not needed in the home when `COPILOT_GITHUB_TOKEN` (or `GH_TOKEN`/`GITHUB_TOKEN`) is set. Tokens are otherwise in
  the OS keychain or `config.json`. With an empty home and a token env var, auth should work. INFERRED; UNVERIFIED.
- Policy: `settings.json` in the home holds user settings including `sandbox.*` and `model`. Mint can write it
  directly. VERIFIED (location and some keys, see 1.8). Repo-level `.github/copilot/settings.json` does not support
  sandbox settings (SBX-CFG), which is the property Mint relies on (repo cannot loosen the operator policy). VERIFIED.
- Models: no home content needed; model comes from the service. A first-run banner or trust prompt may appear
  (UNVERIFIED in `-p`).
- `COPILOT_OFFLINE=true` skips GitHub auth and telemetry (AUTH). Only meaningful with BYOK.

### 1.7 Authentication

- Precedence: `COPILOT_GITHUB_TOKEN` > `GH_TOKEN` > `GITHUB_TOKEN` > keychain OAuth > `gh auth token` fallback. VERIFIED
  (AUTH, CMD). An env var silently overrides a stored login.
- Supported token types: OAuth `gho_` (Copilot CLI app or GitHub CLI app), fine-grained PAT `github_pat_` with the
  "Copilot Requests" account permission (must belong to a personal account, not an organization), GitHub App user-to-server
  `ghu_` via env var. Not supported: classic PAT `ghp_`. VERIFIED (AUTH, CMD).
- Consequence for the design: `copilot-token` in the vault must be a fine-grained PAT with Copilot Requests, an OAuth
  token, or a `ghu_` token. Installation tokens (`ghs_`) are not listed, so the Mint GitHub App identity cannot be the
  Copilot identity. INFERRED. UNVERIFIED if `ghs_` is rejected.
- **Do not set `GH_TOKEN`/`GITHUB_TOKEN` to the agent's repo token in the same environment without also setting
  `COPILOT_GITHUB_TOKEN`**, otherwise the Copilot CLI would use the repo token (precedence list). INFERRED from the
  precedence rule; relevant to design 7.3 where `gh` and `git` receive tokens. Always set `COPILOT_GITHUB_TOKEN`
  explicitly.
- Placeholder behind a proxy: not documented for the Copilot credential. The CLI probably validates the token against
  GitHub (`/copilot_internal/...`) at startup. UNVERIFIED-needs-hands-on-test (S4).
- In the sandbox, `git` and `gh` receive placeholder credentials and a local proxy substitutes real ones for approved
  HTTPS hosts (SBX-USE). This is the same design Mint wants. VERIFIED. `gh` credentials only for `github.com`,
  `api.github.com`, `uploads.github.com`.
- BYOK: `COPILOT_PROVIDER_API_KEY_COMMAND`, `COPILOT_PROVIDERS_CONFIG` (path to JSON registry), `providers.json` (plaintext
  keys) (CMD, ACP). VERIFIED (names). Lets sessions run without GitHub login.

### 1.8 Sandbox and `-p`, settings keys, `copilot config`

- `--sandbox` / `--no-sandbox` combine with `-p`: "Once enabled, sandboxing also applies to programmatic sessions"
  (SBX-USE: `copilot --sandbox -p "PROMPT"`). VERIFIED. Windows headless specifically: UNVERIFIED (S7).
- Not covered: containers (SBX-USE), headless-specific behaviour (SBX-CFG). Whether the Linux sandbox works inside a
  Docker container (needs namespaces or bubblewrap-like capabilities) is UNVERIFIED and probably unnecessary: in the
  Docker runtime, use `--no-sandbox` (the container is the boundary). Beware: managed policy can force sandboxing even
  with `--no-sandbox` (CMD, SBX-USE).
- Settings file: `settings.json` under the `sandbox` key in the config dir; repo settings do not support sandbox keys
  (SBX-CFG). Keys written out in the docs:
  - `sandbox.enabled`
  - `sandbox.failIfUnavailable` (listed as managed, CMD)
  - `sandbox.allowBypass` (managed; false blocks per-command bypass and disabling)
  - `sandbox.userPolicy.network.allowedHosts` / `blockedHosts` (hostnames, wildcards, IPs, CIDR)
  - `sandbox.userPolicy.seatbelt.keychainAccess` (macOS), `sandbox.userPolicy.seatbelt.extraMachLookups` (macOS, 1.0.115-2)
  - `sandbox.userPolicy.capabilities` (Windows package queries opt-out, 1.0.115-2, REL)
  - `sandbox.credentials.envVars.<NAME>.injectHosts` (SBX-CFG)
  VERIFIED for each.
- Keys Mint's existing code uses (`allowLocalNetwork`, `projectConfiguration`) were NOT found in the official pages read.
  A third-party blog places `allowLocalNetwork` under `sandbox.userPolicy.network` (UNVERIFIED, not official). The docs
  UI has "Allow local network" (default off), "Allow outbound connections" (default on), "Include working directory",
  "Allow dev tool access", "Sandbox MCP servers", "Sandbox LSP servers", "Authenticate git", "Authenticate gh" without
  JSON key names. Mint should read key names from its existing, tested code (`applySandboxSettings`) and `copilot
  config --list --json` on the installed version rather than from docs. UNVERIFIED.
- Filesystem policy: deny-by-default, three levels (read/write, read-only, denied; denied wins; most specific wins).
  Working directory read/write; in a git repo whole repo readable, writes limited to cwd and `.git` (SBX-CON). Built-in
  file read/edit tools are only enforced in software, not by the OS (SBX-CON). VERIFIED. Implication: for the Windows
  host runtime, built-in write tools bypass the OS layer; rely on `--allow-tool=write(...)` patterns and deny rules
  too.
- Sandboxed commands do not start if a denied-path glob matches more than 100 paths (1.0.115-2, REL).
- `copilot config`: flags `--global`, `--repo`, `--local`, `--list`, `--json`, `--rm`; returns exit 1 for an unset key
  (CMD). VERIFIED. Writing `settings.json` directly is equivalent but should be validated with `copilot config --list
  --json` under the per-run `COPILOT_HOME`. UNVERIFIED.
- `/sandbox policy [COMMAND]`, `/sandbox status`, `/sandbox config` are slash commands (interactive); `copilot help
  sandbox` exists (REL). Whether the effective policy can be dumped non-interactively: UNVERIFIED.
- Defaults for the CLI: bypass allowed, MCP and LSP sandboxed, git/gh auth on, working directory included, dev tools on,
  outbound on, local network off (SBX-CFG). "Enable sandbox" default for the CLI not stated.
- Policy precedence: managed/enterprise settings can only restrict; device-managed host rules narrow the allowlist;
  every non-empty user and managed allowlist must permit a destination (SBX-CFG). VERIFIED.

### 1.9 Proxy and CA support

- Sandbox remote proxy setting: HTTP or HTTPS URL without embedded credentials; password may be `${VAR}`; applies only
  while outbound is on. If none is set, the CLI "can inherit `HTTPS_PROXY`, `HTTP_PROXY`, or `ALL_PROXY` from the host"
  (SBX-CFG). VERIFIED for the sandbox path. Outside the sandbox (e.g. `--no-sandbox` in Docker): proxy env var support for
  the CLI's own model traffic is UNVERIFIED (CMD part read does not mention it; Node-based CLI may not honour
  `HTTPS_PROXY` without a flag).
- The CLI's own sandbox proxy has its own CA: `copilot sandbox ca status|create|trust [CA.PEM]|rotate|remove` run as the
  normal user (not root/SYSTEM), stored in the user's CLI home (CMD). `/sandbox ca create|trust` adds it to the OS trust
  store on macOS/Windows; on Windows trusting needs admin and affects other applications (SBX-USE). The CLI supplies a
  temporary bundle to sandboxed tools, "some tools ignore it". VERIFIED. Implication: the CA lives in `COPILOT_HOME`, so
  a per-run home gets a per-run CA, which is good isolation, but trusting it in the OS store (global) is not acceptable
  for the daemon. Avoid `ca trust`.
- `NODE_EXTRA_CA_CERTS`, `SSL_CERT_FILE`, `CURL_CA_BUNDLE`: only evidence is that the OTLP exporter trusts them (1.0.115-2,
  REL). Whether model and API traffic honours `NODE_EXTRA_CA_CERTS`: UNVERIFIED-needs-hands-on-test (S4). The CLI ships
  as a Node package and a standalone binary, so behaviour could differ by install method.
- Windows: programs that ignore proxy settings can bypass sandbox host rules (SBX-CFG). VERIFIED. The Windows sandbox
  network policy is therefore not an exfiltration boundary against hostile code.
- Credential masking covers HTTPS only, not plaintext HTTP, request bodies, URLs or signed requests (SBX-USE). VERIFIED.

### 1.10 Premium requests, AI credits, and budgets

- Two billing models appear in the docs: legacy request-based ("each prompt uses one premium request with the default
  model, other models multiply") and usage-based with AI credits (1 credit = $0.01 per search summary of billing pages,
  not read at source). The CLI docs use "AI Credits". Which applies depends on the account/org. UNVERIFIED for the
  operator's account.
- Budget controls that exist: `--max-ai-credits N` (soft, per response in `-p`, minimum 30, resumes honour it),
  `/limits`, `/goal`. `-p` stops cleanly when reached; exit code unspecified. VERIFIED (LIMIT, CMD).
- Reporting: `/usage` shows per-model token totals and credit consumption (interactive, CMD). No documented machine
  readable usage in `-p` JSON. A community report says a `-p` test with one model showed 0 premium requests (unverified,
  third party). Whether `-p` JSON output or `events.jsonl` includes usage data: UNVERIFIED.
- Design consequence: Mint cannot rely on exact spend reporting. Budgets should be enforced by (a) `--max-ai-credits`
  per turn, (b) counting turns/stages and wall clock in Mint, (c) reading usage from `events.jsonl` if present.

### 1.11 Terms and policy for unattended use

- Docs describe CI/headless use as supported: "Running the CLI in a CI/CD workflow step is a common use case"; env-var
  tokens are "recommended for CI/CD, containers, and non-interactive environments" (HOWTO, AUTH, ACTIONS). VERIFIED.
- Copilot CLI in Actions can use the built-in `GITHUB_TOKEN` billed to the organization when the policy "Allow use of
  Copilot CLI billed to the organization" is enabled, with `copilot-requests: write` (ACTIONS; changelog 2026-07-02).
  Not applicable to Mint (own runners), but shows GitHub endorses automation under a seat or org billing.
- Security warnings: broad access in unattended runs, fork PRs especially risky, use minimal permissions, avoid
  `--allow-all` outside a sandbox (HOWTO, ACTIONS). VERIFIED.
- Organization or enterprise policies can disable the CLI (README).
- The Copilot product terms (GitHub Terms for Additional Products, acceptable use, rate limits for automated access) were
  NOT reached; no source found that prohibits or permits seat sharing, service-account PATs, or multiple parallel
  sessions. UNVERIFIED. A human must read the Copilot terms and, for Business/Enterprise, the org agreement. Open
  question: a PAT is tied to a personal account with a seat; running a daemon for other people's issues on that seat may
  conflict with "one user per seat". Treat as a blocking question for any multi-user use; fine for the operator's own
  repos.

### 1.12 Other relevant findings

- `ghcr.io/github/copilot-cli` container image runs the CLI as a headless server for the Copilot SDK
  (`COPILOT_CONNECTION_TOKEN`, `COPILOT_GITHUB_TOKEN`, listens on a port) (README). VERIFIED. This is a third
  integration option (SDK protocol), besides `-p` and ACP; it supports passing a per-user GitHub token when creating or
  resuming a session. The SDK protocol is not documented in what was read. UNVERIFIED; worth a later spike (S2b).
- `--connect[=SESSION-ID]`, `--ui-server` (REL 1.0.115-7) indicate a server/attach mode. Not researched.
- `copilot update`, `copilot version`, `copilot help environment` exist (CMD). Use `copilot version` in Preflight.
- `--no-auto-update` is essential in containers and for reproducibility.

---

## 2. S2: ACP server

| Question | Answer | Label | Source |
|---|---|---|---|
| Is it stable enough to drive? | Public preview, "subject to change". | VERIFIED | ACP |
| How to start | `copilot --acp`, optional `--stdio` (default) or `--port <n>`; both together are rejected. | VERIFIED | ACP |
| Transport | NDJSON over stdio (stdout reserved for protocol) or TCP (binds `127.0.0.1` by default, multiple clients). | VERIFIED | ACP |
| New session | `initialize` (`protocolVersion`, `clientCapabilities`), then `session/new` with `cwd` and `mcpServers`; "the only session parameters a client can set". | VERIFIED | ACP |
| Prompt and stop reason | `prompt` returns `stopReason` (example `end_turn`); streaming via `session/update` notifications (`agent_message_chunk`, `available_commands_update`). Other stop reasons and tool-call update types are not listed in the page; the protocol standard defines them. | VERIFIED (partial), rest UNVERIFIED | ACP |
| Resume (`session/load`) | Mentioned only in passing: fixed server options apply to sessions the server "creates or loads". Whether `loadSession` capability is advertised, and whether on-disk sessions from `-p` runs can be loaded: UNVERIFIED. | UNVERIFIED | ACP |
| Permission requests | The example client answers `requestPermission` with `cancelled`, refusing tools. The page does not define a permission model. Standard ACP has `session/request_permission` with option kinds; whether Copilot honours `--allow-tool` flags passed at server start is UNVERIFIED. | UNVERIFIED | ACP |
| Tool events | Not documented in the page. Standard ACP `tool_call` / `tool_call_update` expected. | UNVERIFIED | ACP |
| Usage | `/usage` slash command is available over ACP (informational, returns text). No structured usage. | VERIFIED (slash), structured UNVERIFIED | ACP |
| Per-run policy and credentials | Fixed at server start: `--available-tools`, `--excluded-tools`, `--effort`; `--sandbox`/`--no-sandbox` and sandbox settings apply to all sessions including resumed ones. `session/new` cannot set tool filters or reasoning. Model per session: not documented. | VERIFIED | ACP |
| Credentials | Token read from env at server start (standard mechanism); BYOK via `COPILOT_PROVIDER_*` or `COPILOT_PROVIDERS_CONFIG` lets sessions run without GitHub login. Per-session tokens are not part of `session/new`. | INFERRED | ACP |
| Runs in sandbox / container? | Sandbox flags apply; container use not documented. TCP mode is recommended by the docs when the client is in another process or container. | VERIFIED (flags), UNVERIFIED (container) | ACP |
| `--fleet` | Not supported in ACP. | VERIFIED | PROG |
| Slash commands | Sent as a single text block (`/context`, `/session info`). Interactive-only commands (`/resume`, `/login`, `/diff`, ...) are unavailable; unknown commands go to the model as prompts. | VERIFIED | ACP |
| Maturity signals | 1.0.115-4 adds structured clarification questions in ACP editors; 1.0.115-8 changes resumed ACP sessions. Active churn. | VERIFIED | REL |

Implications:

- Per-run policy at server start means one ACP server process per run (or per policy profile). Mint already starts one
  container per run, so a server per run is natural; there is no benefit from a long-lived shared server because
  settings cannot differ per session.
- ACP's tool-call permission requests would give Mint the chance to answer per call (a "safe outputs" style gate), which
  `-p` cannot do. This is the main reason to prefer ACP later, once `session/load` and permission semantics are
  verified.
- Needs-input: ACP has the structured clarification feature (1.0.115-4) and permission requests, which map better to
  Mint's `needs_input` than `-p`. UNVERIFIED details.
- Decision proposal: v1 uses `-p` with `--session-id` and `--resume`; ACP evaluated hands-on later (stability). Keep the
  `Agent` interface transport-neutral (see Design impact).

---

## 3. Design impact (recommended edits; do not apply here)

### design.md

1. Section 7.1 (and 5.2): replace "for Copilot `-p` requires `--allow-all-tools`" with: `-p` denies anything not
   pre-approved; use a profile allowlist (`--allow-tool='shell(git:*)'`, `write`, ...) plus `--deny-tool` for
   dangerous commands, and `--allow-all-tools` only in the Docker runtime where the container is the boundary.
   Consider `--assisted-approval` as a middle option. Also set `--no-ask-user`, `--no-auto-update`, `-s`,
   `--no-experimental`-style pinning, and always an explicit `--model`.
2. Section 6.1: Mint should generate the session UUID and pass `--session-id <uuid>` on the first turn, store it in
   the state comment before launch, then `--resume=<uuid>` on later turns. This removes the need to discover IDs.
   Never use bare `--resume` or `--continue`. Add that the cache directory is not covered by `COPILOT_HOME`, that
   `session-state/<id>/events.jsonl` is the transcript (sensitive), and that `--sync-session=off` should be set.
   Record that missing-session behaviour is unverified and the fallback path must handle "resume exits nonzero".
3. Section 5.2: `-p` has no needs-input state. Define completion as exit 0 plus non-empty final response plus a
   Mint-owned `result.json` sentinel (status: `done|needs_input|failed`, summary) that the prompt template requires;
   treat a missing sentinel as failure. Parse `--output-format json` only for diagnostics and usage until the schema
   is captured.
4. Section 7.3 and 8: the Copilot credential must be a fine-grained PAT (Copilot Requests permission, personal
   account), `gho_` OAuth, or `ghu_`; classic PATs rejected, installation tokens not listed. Env precedence is
   `COPILOT_GITHUB_TOKEN` > `GH_TOKEN` > `GITHUB_TOKEN`; always set `COPILOT_GITHUB_TOKEN` explicitly and keep it
   separate from the repo token. A placeholder is unverified (S4 must test; fallback is env var plus egress
   restriction as already noted). `COPILOT_GITHUB_TOKEN` and `GITHUB_TOKEN` are redacted by default; add
   `--secret-env-vars` for any other secret.
5. Section 7.2: the generic `Policy` compiler targets `sandbox.userPolicy.network.allowedHosts/blockedHosts`,
   `sandbox.credentials.envVars.<NAME>.injectHosts`, `sandbox.enabled`; verify `allowLocalNetwork` and
   `projectConfiguration` key names against the installed version (not in official pages read). Note built-in file
   tools are not OS-enforced, so combine with `--allow-tool` write patterns. Note a managed
   `permissions.disableBypassPermissionsMode` or `sandbox.failIfUnavailable` can break or force settings, so
   Preflight must read effective settings (`copilot config --list --json`) and fail closed.
6. Section 7.1 egress: do not use `copilot sandbox ca trust` (OS-global). Use env CA vars if verified; otherwise the
   container image trusts the Mint CA at build or entrypoint time. Model-endpoint proxy behaviour
   (`HTTPS_PROXY`, `NODE_EXTRA_CA_CERTS`) is unverified outside the sandbox.
7. Section 5 budgets: premium-request spend is not reliably reported. Use `--max-ai-credits` per turn plus Mint-side
   turn and wall-clock budgets; read `events.jsonl` for usage if present. Rename "premium requests" to "AI credits /
   premium requests (account-dependent)".
8. Section 15: add open questions: Copilot terms for daemon use of one seat (blocking for multi-user), SDK server
   container (`ghcr.io/github/copilot-cli`) as an alternative transport, enterprise managed-policy interactions.
9. Section 4.2 `Agent.Capabilities`: add `ChooseSessionID`, `Transport` (`prompt-flag | acp`), `StructuredUsage`.

### implementation-plan.md

1. M0: mark S1/S2 as researched by documentation; the exit criteria script is still pending (section 4 below).
2. M1: the `agent/copilot` args builder must support `-p`, `-s`, `--no-ask-user`, `--no-auto-update`, `--session-id`,
   `--resume=<id>`, `--output-format json`, `--max-ai-credits`, `--allow-tool/--deny-tool/--available-tools/
   --excluded-tools`, `--sync-session=off`, `--model`, and must refuse bare `--resume`/`--continue`.
3. M3: Copilot policy compiler golden tests should cover `settings.json` keys listed in 1.8 only; unknown key names
   (`allowLocalNetwork`, `projectConfiguration`) get a version-gated check.
4. M4: add a `result.json` sentinel contract and prompt-template requirement; `ParseResult` combines exit status,
   sentinel, and optional JSONL. Fake agent helper must emulate exit codes and sentinel, plus a "resume with missing
   session" failure.
5. M5: the Docker runtime sets `COPILOT_HOME` to the mounted per-run home, a writable ephemeral cache dir
   (`XDG_CACHE_HOME`), and `--no-sandbox` only if no managed policy forces it (Preflight detects).
6. Add a task for an ACP adapter variant only after the hands-on tests pass (stability and permission semantics);
   keep the interface transport-neutral.
7. Add a Preflight task: `copilot version` minimum (record the tested version, currently 1.0.114), token type check
   (reject `ghp_`), and reading effective policy.

---

## 4. Hands-on tests still required

Run with the installed CLI in a throwaway directory. Record `copilot version` first. Use a dedicated fine-grained PAT
with Copilot Requests in `COPILOT_GITHUB_TOKEN`; unset `GH_TOKEN` and `GITHUB_TOKEN`.

```sh
export COPILOT_HOME=$(mktemp -d); export XDG_CACHE_HOME=$(mktemp -d)
export COPILOT_GITHUB_TOKEN=...   # prompt, never paste into logs
mkdir ws && cd ws && git init -q
SID=$(uuidgen)

# T1 first turn with caller-chosen id, JSON output, no prompts
copilot -p "Create hello.txt containing hi, then say done" -s --no-ask-user --no-auto-update \
  --session-id $SID --output-format json --allow-tool='write' --deny-tool='shell(rm:*)' \
  > out1.jsonl 2> err1.txt; echo "exit=$?"
# Record: exit code, event types in out1.jsonl, whether final event marks completion, any usage/credits fields,
# whether $COPILOT_HOME/session-state/$SID/events.jsonl exists (T2).

# T2 session dir
ls "$COPILOT_HOME/session-state/"; head -c 600 "$COPILOT_HOME/session-state/$SID/events.jsonl"

# T3 resume in headless mode
copilot -p "What file did you create? Reply with its name" -s --no-ask-user --resume=$SID \
  --output-format json --allow-tool='write' > out2.jsonl; echo "exit=$?"

# T4 resume failures: missing id, bare --resume, --continue, deleted session dir, empty home
copilot -p "x" --resume=$(uuidgen); echo "exit=$?"
copilot -p "x" --resume; echo "exit=$?"
rm -rf "$COPILOT_HOME/session-state/$SID"; copilot -p "x" --resume=$SID; echo "exit=$?"

# T5 permission denial: no --allow-tool, request a shell command; check exit code and stderr/JSON event shape
copilot -p "run: echo hi" --no-ask-user; echo "exit=$?"
# deny precedence: --allow-all-tools --deny-tool='shell(echo:*)' with the same prompt

# T6 other exit codes: bad token (unset COPILOT_GITHUB_TOKEN, expect auth error), classic ghp_ token, placeholder
# token behind a stub HTTPS proxy, empty final response, SIGTERM mid-run, --max-ai-credits=30 on a long task.

# T7 settings and sandbox
copilot config --list --json     # confirm key names (sandbox.userPolicy.network.allowLocalNetwork? projectConfiguration?)
cat > "$COPILOT_HOME/settings.json" <<'EOF'
{"sandbox":{"enabled":true,"userPolicy":{"network":{"allowedHosts":["api.github.com"]}}}}
EOF
copilot --sandbox -p "curl https://example.com and report" --allow-tool='shell' -s; echo "exit=$?"
# Expect denial. Repeat on Windows without TTY (S7) and inside a Linux container with and without --no-sandbox.

# T8 proxy and CA (S4): run with HTTPS_PROXY=http://127.0.0.1:PORT through a logging MITM proxy
# (a) --no-sandbox, NODE_EXTRA_CA_CERTS=ca.pem  (b) --sandbox with settings remote proxy  (c) without CA to see failure mode.
# Check model traffic (api.githubcopilot.com or similar) is visible and whether token validation succeeds with a placeholder.

# T9 billing/usage: run T1 and note whether usage appears in JSON/events.jsonl; compare the org/account usage report
# (premium requests or AI credits) before and after.

# T10 ACP (S2): spawn `copilot --acp --stdio --available-tools="view"`; send initialize, session/new {cwd, mcpServers:[]},
# prompt; log all messages. Then: second process, session/load with a session created by -p (T1), check agentCapabilities.loadSession,
# session/request_permission shape, tool_call updates, stopReason values (end_turn, cancelled, refusal, max_tokens), usage text from `/usage`.
# Test whether --allow-tool at server start suppresses permission requests. Test a TCP server in a container (--port, bind address).

# T11 stability: run T1 ten times in parallel with distinct COPILOT_HOME values and with one shared home (to learn lock behaviour of session-store.db).
```

Also needed from a human: read the GitHub Copilot terms (Additional Product Terms, acceptable use) and the organization's
Copilot policy for unattended and automated use before running the daemon for anyone but the operator.
