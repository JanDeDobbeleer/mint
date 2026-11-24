# Mint daemon: design

Status: draft for implementation. Companion documents:
[research-spikes.md](research-spikes.md) (questions to answer before building) and
[implementation-plan.md](implementation-plan.md) (ordered milestones).

## 1. Purpose

Today `mint` launches an interactive GitHub Copilot CLI session in its local sandbox with a fine-grained PAT scoped to
the current repository. This design extends Mint with a long-running daemon (`mint serve`) that:

1. Listens for work (GitHub issues and PR events now; email and WhatsApp later; schedules).
2. Maps each piece of work to a **project** and a **flow** (analyze, implement, simplify, verify, publish, review loop,
   merge).
3. Runs each agent stage in an isolated **runtime**: a Docker container by default, or the Copilot CLI sandbox on the
   Windows host when the work needs Windows.
4. Injects narrowly scoped, short-lived credentials so the agent never holds a root secret.
5. Treats GitHub as the only durable state: the daemon is restartable at any moment.

The interactive `mint` command keeps working unchanged.

The model is inspired by OpenAI's Symphony spec (poll tracker, per-issue workspace, agent run, reconcile), adapted for
Copilot CLI, Windows, GitHub, and multi-source input. See section 16 for prior art.

### Non-goals (for now)

- Running on GitHub Actions or adopting GitHub Agentic Workflows. Mint owns its runners and execution.

- Replacing the interactive launcher.
- Windows containers, Kubernetes, or remote worker fleets.
- Implementing email/WhatsApp/Claude adapters. The engine must allow them; they are not first deliverables.
- Hosting a web UI. A read-only status command is enough.

## 2. Principles

1. **Two authorities.** The *repo* owns the environment and the task: `.mint/Dockerfile`, `.mint/workflow.md`
   (prompts, default check commands). The *operator* (the person running Mint) owns trust: projects, sender mappings,
   egress, sandbox profiles, runtime choice, merge authority, credential names. A repo can never loosen operator policy.
2. **Read repo-owned files from the default branch, never from an agent's workspace.** Otherwise a session can edit
   `.mint/Dockerfile` and get its own change built and run. Changes take effect after merge. A changed Dockerfile hash
   needs one-time operator approval.
3. **All inbound content is untrusted data.** Issue bodies, PR comments, emails, chat messages are quoted into prompts,
   never treated as instructions. Trust comes from the *verified sender identity*, not the content.
4. **Secrets are a type, not a string.** Never in config files, argv, logs, or child environments. Held in memory
   after a single unlock at daemon start.
5. **The core knows nothing about agents, sources, or runtimes.** It sees interfaces and registries. Each plugin can
   be deleted without touching the core. Copilot specifics live in `internal/agent/copilot` and
   `internal/runtime/hostsandbox` only.
6. **Fail closed.** Every runtime and agent has a preflight. Any doubt aborts the launch (the existing
   `checkSandboxCredentials` is the model).
7. **GitHub is the system of record.** No run database. The daemon rebuilds its view from GitHub on every start.
8. **Programmatic work is Mint's, creative work is the agent's.** Tests, lint, push, PR creation, merge decisions are
   deterministic actions run by Mint, not requests to the agent.
9. **Idempotent, checkpointed stages.** A crash re-runs the current stage from the last pushed state.
10. **Every extension point ships a conformance test suite** so new sources, agents, and runtimes prove the contract.

## 3. Decisions log

| Decision | Reason |
|---|---|
| Docker container is the default session boundary; Copilot sandbox is the Windows-host option | One boundary independent of the agent CLI; unattended runs need broad tool permission, which is only safe inside a throwaway container. |
| GitHub App (bot) identity for the daemon, PAT only for `mint run` / interactive | A PAT-authored PR is authored by the operator, who then cannot approve it. App tokens are short-lived and per-repo. Mint previously dropped App keys for interactive UX; the daemon is where they pay off. |
| No run store; PR state comment + branch name + commit trailers | The trigger always results in a PR, so GitHub already holds the record. Restart and multi-machine recovery become trivial. |
| Copilot session ID in the state comment; resume is primary, context reconstruction the fallback | Copilot stores sessions locally under its home; with a deterministic per-run `COPILOT_HOME` the ID is enough. Falls back when the home is gone. |
| Credentials never in config; encrypted vault unlocked by master password at `serve` start | Extends the existing `master.enc` + token file design. Secret *values* are never accepted as flag values (visible in process lists and shell history); flags choose a *source* (prompt, stdin). |
| Credential injection by a Mint-owned proxy, not by the agent CLI | Backend-independent guarantee. Copilot has `injectHosts`; Claude Code does not. |
| Flow engine with typed stages (agent / action / wait) | Analyze, implement, simplify, test, publish, review loop, nightly integration are all the same shape. |

## 4. Architecture

```
Sources ──Event──► Router ──► Flow engine ──► Runtime ──► Agent
(github, email,    (verify,    (stages, actions,   (docker |     (copilot |
 whatsapp, cron)    dedupe,     waits, budgets)     hostsandbox)  claude)
                    sender→project,
                    trust tier)
     ▲                                  │
     └──── Responder (comment, reply, draft) ◄────┘

Credential broker: vault ► per-session token minting ► injecting proxy
State: GitHub only (PR state comment, labels, branches, commit trailers)
```

### 4.1 Domain types (package `internal/core`)

```go
type Event struct {
    ID       string      // globally unique per source; dedupe key
    Source   string      // "github", "email", "whatsapp", "cron"
    Kind     string      // "issue", "pr_review", "pr_comment", "message", "tick"
    Sender   Identity    // verified principal
    Verified bool        // signature / SPF+DKIM / authenticated GitHub event
    Thread   ThreadRef   // issue, PR, mail thread, chat
    Subject  string
    Body     string      // untrusted
    Attrs    map[string]string // labels, review state, head SHA, ...
}

type Identity struct{ Scheme, ID, Display string } // "github:login", "email:a@b.c", "whatsapp:+32..."
type ThreadRef struct{ Source, Key string }

type Policy struct {                     // generic sandbox definition
    Filesystem  FSPolicy                 // readonly, readwrite (workspace-relative tokens allowed)
    Network     NetPolicy                // allowed hosts
    Credentials map[string][]string      // credential name -> hosts it may be injected for
    Limits      Limits                   // cpu, memory, pids, wall clock
}

type Secret struct{ /* zeroizable; String/MarshalJSON/Format redact */ }
```

### 4.2 Interfaces

```go
// Source: inbound events plus replies on the same thread.
type Source interface {
    Name() string
    Run(ctx context.Context, emit func(Event)) error // owns its cursor/polling/webhook
    Reply(ctx context.Context, t ThreadRef, m Message) error
}

// Runtime: where a session executes.
type Runtime interface {
    Name() string
    Preflight(ctx context.Context, p Policy) error
    Start(ctx context.Context, spec SessionSpec) (Session, error)
}

// Agent: the coding CLI. Copilot first; Claude must be addable without touching the core.
type Agent interface {
    Name() string
    Preflight(ctx context.Context, rt RuntimeInfo) error
    Command(req AgentRequest) (AgentCommand, error) // prompt, policy, resume id
    CredentialEnv() map[string]string               // credential name -> env var the CLI expects
    Capabilities() AgentCaps                        // CanResume, StructuredEvents, ...
    ParseResult(out SessionResult) (AgentOutcome, error) // done / needs-input / failed + summary
}

// Action: programmatic stage work.
type Action interface {
    Name() string // "checks", "git.push", "github.open-pr", "github.merge", ...
    Do(ctx context.Context, rc *RunContext, with map[string]any) (Outcome, error)
}
```

Registries (`RegisterSource`, `RegisterRuntime`, `RegisterAgent`, `RegisterAction`) populate the engine at startup.
Removing Copilot means deleting two packages and their registration lines.

## 5. Flows, runs, and stages

- **Trigger**: matches an event (or cron tick) and starts or resumes a Run.
- **Flow**: a named graph of stages.
- **Run**: one execution of a flow. Identity is the work-item key (for example `github:owner/repo#123`). The Run record
  is the PR (section 6).
- **Stage kinds**
  - `agent`: a named agent profile does creative work.
  - `action`: Mint does deterministic work.
  - `wait`: park until an event arrives; no process stays alive.
- **Outcomes** select the next edge (`done`, `fail`, `retry`, `needs_input`, custom labels).
- **Budgets** per Run: max stages, wall clock, and spend (premium requests). Exceeding any fails the Run and notifies.
  A `mint:stop` label cancels a Run.
- **Hooks** (`before_stage`, `after_stage`, `on_fail`, `on_done`) are engine configuration for cross-cutting concerns
  (notify, cleanup, log). They are never agent-adapter code.

### 5.1 Agent profiles

```yaml
agents:
  analyst:    { adapter: copilot, prompt: analyze,        sandbox: readonly }
  coder:      { adapter: copilot, prompt: implement,      sandbox: default  }
  simplifier: { adapter: copilot, prompt: simplify,       sandbox: default  }
  fixer:      { adapter: copilot, prompt: address-review, sandbox: default, resume: true }
```

A profile bundles adapter, model, prompt, sandbox, and tool allowlist. Prompts may come from `.mint/prompts/` in the
repo (default branch). Profiles and permissions are operator-owned.

### 5.2 Agent completion and checks

The agent reports "done" via exit status, ACP stop reason, or a `result.json` sentinel in the run directory (decided by
spike S1/S2). Mint then runs the `checks` action (test, lint, build commands) inside the same runtime and workspace.
On failure the output is fed back to the same agent session as a new turn, bounded by `max`. Check commands live in
project config; a repo may provide defaults in `.mint/workflow.yml` but they only run inside the sandbox boundary, and
on the host runtime only if the operator allows it.

### 5.3 Flow: issue to PR

```yaml
flows:
  issue-to-pr:
    on: { source: github, kind: issue, label: mint }
    stages:
      analyze:   { agent: analyst,    next: implement }   # optional gate: plan approval
      implement: { agent: coder,      next: simplify }
      simplify:  { agent: simplifier, next: verify }
      verify:    { action: checks, on_fail: { retry: implement, max: 2 }, next: publish }
      publish:   { action: github.open-pr }               # PR carries the state comment
```

Analysis output is stored in the state comment and passed to later stages as quoted input.

### 5.4 Flow: PR feedback re-triggers the same Run

```yaml
  pr-feedback:
    on: { source: github, kind: pr_review, state: changes_requested, from: [<operator>], run: existing }
    stages:
      fix:       { agent: fixer, next: verify }
      verify:    { action: checks, on_fail: { retry: fix, max: 2 }, next: push }
      push:      { action: git.push, next: rerequest }
      rerequest: { action: github.request-review, from: [<operator>] }
  pr-approved:
    on: { source: github, kind: pr_review, state: approved, from: [<operator>], run: existing }
    stages:
      merge: { action: github.merge, method: squash,
               require: [head_sha_matches_approval, checks_green] }
```

PRs opened by others can start a different flow (an AI review, or nothing) through trigger conditions such as
`author:`.

Guards:

- Approval authority is operator-defined: only listed identities, only on the current head SHA; a new push
  invalidates it.
- Ignore events authored by Mint's own identity (loop prevention); cap iterations per Run.
- Branch protection stays on. Mint never bypasses it, so GitHub enforces required checks even if Mint's check is wrong.

### 5.5 Flow: nightly `next` integration

```yaml
  nightly-next:
    on: { schedule: "0 5 * * *", tz: Europe/Brussels }
    stages:
      collect: { action: github.collect-prs,
                 where: { approved_by: [<operator>], head_matches_approval: true, draft: false,
                          base: main, not_labeled: hold }, next: build }
      build:   { action: git.integrate, branch: next, from: main,
                 strategy: sequential-merge, on_conflict: skip-and-comment, next: pr }
      pr:      { action: github.open-pr, head: next, base: main, next: ci }
      ci:      { wait: checks_green, timeout: 90m, on_fail: notify, next: merge }
      merge:   { action: github.merge, method: merge-commit }
```

- `next` is rebuilt from `main` each night and force-pushed. A ruleset allows only the bot to push it.
- Use a merge commit (not squash) so GitHub marks the source PRs as merged when their commits reach `main`.
- Conflicting PRs are excluded with a comment. An optional `resolve-conflicts` agent stage is future work.
- Mint does not create releases. Merging to `main` triggers the repo's release workflow; optionally a
  `github.dispatch-workflow` action.
- The nightly Run is keyed by date (`next-YYYY-MM-DD` marker on its PR), so a double fire is a no-op.

## 6. State lives in GitHub

- **Run identity**: branch `mint/<key>` and the PR. Branch creation is atomic and doubles as the claim, so two daemons
  cannot start the same Run.
- **State comment**: one comment authored by Mint's identity on the PR (not the PR body, which humans edit) with
  `flow`, `stage`, `attempt`, budget counters, issue link, and `agent_session`. Content is **HMAC-signed** with a key
  derived from the vault. Accept a comment only if its author is Mint's identity *and* the HMAC verifies.
- **Checkpoints**: each stage updates the comment and pushes its commits (commit per stage, e.g.
  `mint(simplify): ...`, with `Mint-Run:` trailers). Agent stages commit and push at their end.
- **Before the first commit** there is no PR. Open a draft PR as early as possible, or record the claim on the issue
  (label `mint:claimed` plus a signed comment). Analysis output goes into the state comment so it survives crashes.
- **Recovery**: at start and each tick, list open PRs with the Mint marker whose `stage` is not `done` and reconcile.
  Rebuild workspaces by cloning the PR branch.
- **Cron**: no last-fire record; date-keyed PR makes double fires harmless.
- **Non-GitHub sources**: the first action creates a tracking issue or draft PR carrying the source message ID. Dedupe is
  a lookup of that ID. Each source also marks messages handled on its own side (mailbox label, read receipt).
- **API cost**: reconcile with ETags and conditional requests; optional webhook source later.
- **Stages must be idempotent**: a crash re-runs the current stage from the last pushed state.

### 6.1 Agent sessions and resume

Copilot stores sessions locally under its home directory. Mint sets `COPILOT_HOME` to a deterministic per-run
directory `<state>/agent-home/<run-key>` on the host: bind-mounted into the container, or used directly by the
Windows sandbox runtime. The state comment records `agent_session`; the path is derived from the run key.

- Never share the operator's normal Copilot home with daemon runs. Mint writes the sandbox policy into the per-run home.
- Delete a run's agent home when the PR is merged or closed; sweep orphans periodically.
- If the session is missing (machine change, wipe), fall back to a fresh session primed with the issue, stored
  analysis, current diff, and review comments, and note the fallback in the state comment.
- Resumed sessions still contain whatever untrusted content the agent read earlier. Sandbox, token scope, and gates stay
  fully in force on resume.
- `--resume`/`--continue` behaviour in headless and sandboxed modes is unverified (spike S1).

## 7. Runtimes

### 7.1 Docker runtime (default)

- Image defined by `.mint/Dockerfile` (toolchain only: no editor, shells, or prompt extras). Mint publishes a small
  `mint-base` image (non-root user, git, gh, CA hook, agent CLI). Both `.mint/Dockerfile` and any devcontainer may
  `FROM` it so they share a base without sharing bloat.
- Build and update: `mint image build|update|prune`. Images are tagged `mint/<owner>-<repo>:<dockerfile-hash>`.
  `update` rebuilds with `--pull` when the Dockerfile hash or base digest changes; `serve` can do this on a schedule;
  `prune` removes old tags. Builds get no secrets and no host mounts. Build from the default branch version only.
- Run-time settings are Mint's, not the Dockerfile's: all capabilities dropped, `no-new-privileges`, default seccomp,
  pids/memory/CPU caps, read-only root with tmpfs scratch, non-root user. A repo's `devcontainer.json` `runArgs`,
  `mounts`, `privileged`, `initializeCommand`, and `features` are never honoured.
- Workspace: a shallow clone in a volume (not a `git worktree`: its `.git` file points back to the main repo).
- Egress: the container's only route out is Mint's injecting proxy (7.3). On Docker Desktop this likely needs an
  `--internal` network plus a Mint-owned sidecar proxy container (spike S3/S4).
- Inside the container the agent runs with broad tool permission (for Copilot `-p` requires `--allow-all-tools`); the
  blast radius is the container, the scoped token, and the proxy allowlist.

### 7.2 Windows host runtime (`windows-sandbox`)

For Windows-specific code and integrations. It is today's Copilot sandbox launch.

- Same preflight as today: sandbox policy check, `projectConfiguration`, env allowlist, isolated `GH_CONFIG_DIR`,
  `--secret-env-vars`, MCP disablement.
- Selection is declared in the project (`runs-on: windows-sandbox`) or by an issue label such as `mint:windows`; operator
  config decides which projects may use it. A label alone never escalates.
- The host process is not a strong boundary (README states this). Therefore: trusted senders only, concurrency 1,
  dedicated per-run Copilot home, and a plan/approval gate by default.
- The generic `Policy` is compiled into Copilot's `sandbox.userPolicy`, `injectHosts`, and Windows
  `allowLocalNetwork` settings.

### 7.3 Credential broker and injecting proxy

- The root credential lives in the vault (App private key, or an operator PAT for `mint run`). Per-session credentials
  are derived from it: for the App, a ≤1h installation token narrowed to one repo and minimal permissions
  (`POST /app/installations/{id}/access_tokens` with `repositories` and `permissions`), revoked at run end.
- Containers receive only placeholder values. A local forward proxy terminates TLS with a per-session CA, adds
  `Authorization` for GitHub hosts only, and enforces the host allowlist. Hosts and which credential applies to them
  come from `Policy.Credentials`.
- Two-token split: an **orchestrator token** (Issues read, label write, PR read) never enters a sandbox and does polling,
  claiming, and state transitions; an **agent token** (Contents and PR write, scoped to one repo) is the only one the
  session can use.
- Run duration is capped below token TTL. Longer work restarts the turn with a fresh token via resume.
- The Copilot credential (`COPILOT_GITHUB_TOKEN`) and model keys can use the same proxy with dummy values; if a CLI
  refuses a placeholder, pass as an env var and rely on egress restriction.

## 8. Credentials, vault, and unlock

- Config files contain credential *names* only.
- The vault extends the existing design: random store key encrypted in `master.enc` under the master password; separate
  encrypted files per credential (existing PBKDF2 + AES-GCM with associated data). New named credentials:
  `github-app-key`, `github-operator-pat`, `copilot-token`, `imap-password`, `whatsapp-token`, `webhook-secret`.
- `mint serve` unlocks once and keeps secrets in memory as `Secret`.
- Unlock sources are flags that choose a *source*, never carry a value: hidden prompt (default), `--password-stdin`
  (automation). Optionally later: OS credential store. A plain `--password=...` flag is rejected.
- `mint creds add|list|rm` manages credentials with hidden prompts.
- Secrets never reach argv, child env (except via the allowlisted injection path), logs, or error strings. Enforced by
  the type (redacting `String`, `Format`, `MarshalJSON`) and by tests.

## 9. Sources, routing, and trust

```yaml
projects:
  acme-api:
    repo: github.com/me/acme-api
    runs-on: container          # or windows-sandbox
    sandbox: default
    senders:
      - { match: "github:org/maintainers", trust: maintainer }
      - { match: "email:cto@acme.com",     trust: customer }
      - { match: "whatsapp:+32...",        trust: customer }
triggers:
  - { source: github, kind: issue,      label: mint,   project: auto }
  - { source: github, kind: pr_comment, mention: "@mint", project: auto }
  - { source: email,  to: support@me.dev, project: by-sender }
```

- **Router**: verify the event, dedupe on `Event.ID`, resolve sender → project → trust tier, match a trigger, enqueue.
- **Trust tiers map to capabilities.** `maintainer` can push to `mint/*` branches. `customer` gets a read-only token and
  produces a *draft* reply that needs operator approval before it is sent. Unknown senders are dropped.
- **Verification**: email requires SPF/DKIM alignment (`From` is spoofable); WhatsApp requires webhook signature
  verification; GitHub events come through authenticated API calls (or signed webhooks).
- **Conversation continuity**: sessions are keyed by `ThreadRef`; follow-ups resume the same Run.
- First implementation covers GitHub only. Email and WhatsApp must need nothing beyond a new `Source` plus config.

## 10. Configuration layout

| Location | Owner | Contents |
|---|---|---|
| `<config dir>/config.json` | existing | interactive launcher (remote, Copilot path) |
| `<config dir>/mint.yaml` | operator | projects, senders, triggers, flows, agent profiles, sandbox profiles, hooks, budgets |
| `<config dir>/vault/` | operator | encrypted credentials, `master.enc` |
| `<config dir>/runs/agent-home/<key>/` | daemon | per-run agent homes |
| repo `.mint/Dockerfile` | repo | session image (toolchain only) |
| repo `.mint/workflow.md` or `.mint/prompts/` | repo | prompt templates, default checks |

All repo-owned files are read from the default branch. Operator config is validated on load; unknown keys are errors.
Dynamic reload of operator config and repo prompts is supported on change, with a validation gate (a bad reload keeps
the previous good config).

## 11. CLI surface

- `mint [copilot args]`, `mint setup`: unchanged.
- `mint run <issue-url|number>`: one-shot Run of `issue-to-pr`; first usable milestone; uses the PAT vault entry or App.
- `mint serve`: the daemon (prompts for the master password once).
- `mint image build|update|prune`.
- `mint creds add|list|rm`.
- `mint status`: lists reconciled Runs from GitHub (and daemon health if running).

## 12. Security model

| Asset | Threat | Controls |
|---|---|---|
| Root credentials (App key, PAT, Copilot token) | exfiltration by agent or repo code | vault + `Secret` type; never in child env; proxy injection; per-session derived tokens |
| Host filesystem and user profile | agent or build escapes | container isolation (caps dropped, non-root, read-only root, no host mounts except workspace and agent home); Windows runtime only for trusted work |
| Repository | malicious push, workflow tampering | token scoped to one repo and minimal permissions; push to `mint/*` enforced by rulesets; Workflows write off by default; branch protection; human-gated merge by default |
| Run state | tampering with state comment (budget reset, stage skip) | author check + HMAC |
| Prompt injection via issues, PRs, email | agent follows attacker instructions | untrusted-data quoting; trust tiers; egress allowlist; no secrets in the sandbox; read-only token for low trust; outbound messages drafted for approval |
| Approvals | forged or stale approval triggers merge | operator-listed identities; head-SHA match; ignore Mint's own events; branch protection retained |
| Dockerfile / devcontainer / repo settings | repo-controlled code execution at build or run time | default-branch reads; hash approval; Mint-owned run flags; `projectConfiguration`-style rejection of conflicting repo settings |
| Cost | runaway loops | per-Run budgets, iteration caps, `mint:stop` |

Known residual risks: shared-kernel container escape (mitigation path: gVisor, Kata, or Firecracker behind the same
`Runtime` interface), exfiltration through allowed hosts (keep the allowlist short), malicious code pushed to a branch
(rulesets, review).

Use the "safe outputs" pattern (borrowed from gh-aw) where practical: the agent *requests* external actions (comment, label, PR) and a
separate trusted step validates and applies them.

## 13. Observability and failure handling

- Structured logs per Run key (no secrets). Per-run transcript retained in the agent home until cleanup.
- Failures are reported on the PR/issue (and via an email responder when configured), including which stage and why.
- Retries: bounded per stage; backoff for rate limits and transient GitHub errors.
- Graceful shutdown: stop dispatching, let current stages checkpoint, kill containers and process trees (Windows Job
  Objects), revoke installation tokens.

## 14. Windows specifics

- Daemon host is Windows; Docker (Linux containers) is available through Docker Desktop/WSL2.
- Process-tree kill via Job Objects; long-path handling for workspaces; named-pipe Docker endpoint.
- The existing `ui_windows.go`, MSIX packaging, and AppInstaller flow stay; the daemon ships in the same binary.
- Build and test on Linux, macOS, and Windows (CI matrix already does).

## 15. Open questions

1. Copilot CLI headless and ACP behaviour, completion signalling, resume, and whether session history includes
   anything sensitive (S1, S2).
2. Forcing egress through the proxy on Docker Desktop for Windows (S3, S4).
3. Licensing and terms for unattended use of a Copilot seat or `COPILOT_GITHUB_TOKEN`; premium request billing.
4. GitHub App ownership (a personal app installed on selected repos is likely enough) and which permissions per flow.
5. "Update" scope: this design treats it as session-image refresh; Mint's own binary already updates via AppInstaller.
6. Whether to adopt a pure-Go YAML library (`go.yaml.in/yaml/v3` is already an indirect dependency) for `mint.yaml`.

## 16. Prior art and what we borrow

- **OpenAI Symphony spec** (and the Itervox Go implementation): poll, per-issue workspace, `WORKFLOW.md`, reconcile.
  Itervox supports Claude Code and Codex, runs with permissions skipped in high-trust local setups, and does not manage
  scoped credentials; used as inspiration, not a base.
- **GitHub Agentic Workflows (gh-aw)**: Markdown workflows compiled to Actions with Copilot CLI as default engine,
  containerized agent, firewall, safe outputs. **Decision: not adopted.** Mint always runs its own runners; only the
  "safe outputs" idea is borrowed (S0).
- **GitHub Copilot coding agent**, **Claude Code GitHub Action**, **Open SWE**, **Composio Agent Orchestrator**,
  **Baton**: issue-to-PR automation of varying scope; none combine Copilot CLI, Windows-native sandbox sessions,
  scoped credential injection, multi-source triggers, and the flow model above.
