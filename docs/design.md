# Mint daemon: design

Status: draft v2 for implementation (incorporates the documentation research in [spikes/](spikes/)). Companion
documents: [research-spikes.md](research-spikes.md) (status of each spike and the hands-on tests still required) and
[implementation-plan.md](implementation-plan.md) (ordered milestones).

Evidence labels used below: **[V]** verified in documentation, **[I]** inferred, **[U]** unverified and needing a
hands-on test (tracked in the spike files). Several documentation hosts were blocked during research, so most facts
come from excerpts or raw repository sources, not full pages.

## 1. Purpose

Today `mint` launches an interactive GitHub Copilot CLI session in its local sandbox with a fine-grained PAT scoped to
the current repository. This design extends Mint with a long-running daemon (`mint serve`) that:

1. Listens for work (GitHub issues and PR events now; email next, WhatsApp later; schedules).
2. Maps each piece of work to a **project** and a **flow** (analyze, implement, simplify, verify, publish, review loop,
   merge).
3. Runs each agent stage in an isolated **runtime**: a Docker container by default, or the Copilot CLI sandbox on the
   Windows host when the work needs Windows.
4. Gives agents only narrowly scoped, short-lived credentials, injected by a Mint-owned proxy, and applies external
   writes itself after validating them.
5. Treats GitHub as the only durable state: the daemon is restartable at any moment.

The interactive `mint` command keeps working unchanged. Mint always runs its own runners; GitHub Actions-hosted
execution and GitHub Agentic Workflows are out of scope.

### Non-goals (for now)

- Running on GitHub Actions or adopting GitHub Agentic Workflows. Mint owns its runners and execution.
- Replacing the interactive launcher.
- Windows containers, Kubernetes, or remote worker fleets (SSH workers).
- Implementing email, WhatsApp, or Claude adapters in the first milestones. The engine must allow them.
- Hosting a web UI. A read-only status command is enough.

## 2. Principles

1. **Two authorities.** The *repo* owns the environment and the task: `.mint/Dockerfile`, `.mint/workflow.md` and
   `.mint/prompts/` (prompts, default check commands). The *operator* owns trust: projects, sender mappings, egress,
   sandbox profiles, runtime choice, merge authority, hooks, credential names. A repo can never loosen operator policy.
   Hooks are operator configuration and are never read from the repo.
2. **Read repo-owned files from the default branch, never from an agent's workspace.** Otherwise a session can edit
   `.mint/Dockerfile` and get its own change built and run. Changes take effect after merge. A changed Dockerfile hash
   needs one-time operator approval.
3. **All inbound content is untrusted data.** Issue bodies, PR comments, emails, chat messages are sanitized and
   quoted into prompts (section 5.8), never treated as instructions. Trust comes from the *verified sender identity*.
4. **Secrets are a type, not a string.** Never in config files, argv, logs, or child environments. Held in memory
   after a single unlock at daemon start.
5. **The core knows nothing about agents, sources, or runtimes.** It sees interfaces and registries. Each plugin can
   be deleted without touching the core. Copilot specifics live in `internal/agent/copilot` and
   `internal/runtime/hostsandbox` only.
6. **Fail closed.** Every runtime and agent has a preflight. Any doubt aborts the launch. A checker that errors counts
   as *unknown*, never as clean.
7. **GitHub is the system of record.** No run database. The daemon rebuilds its view from GitHub on every start.
8. **Programmatic work is Mint's, creative work is the agent's.** Tests, lint, push, PR creation, merge decisions are
   deterministic actions run by Mint. The agent never performs external writes (section 5.7).
9. **Idempotent, checkpointed stages.** A crash re-runs the current stage from the last pushed state.
10. **Every extension point ships a conformance test suite** so new sources, agents, and runtimes prove the contract.
11. **Do not rely on an agent CLI's own sandbox for the container boundary.** The container (or, on Windows host runs,
    the Copilot sandbox plus trust gates) is the boundary.

## 3. Decisions log

| Decision | Reason |
|---|---|
| Docker container is the default session boundary; Copilot sandbox is the Windows-host option | One boundary independent of the agent CLI. |
| The operator's own account with **fine-grained, repo-scoped PATs split by role; no GitHub App, no machine user** | Repos are only the operator's own, so fine-grained PATs work and stay scoped. Cost: Mint's actions appear as the operator, so GitHub's native Approve is unavailable (no self-approval) and Mint recognises its own events by signed markers, not by account. Approval is an explicit signal (`approval: label` by default). No per-session minting; expiry is tracked. |
| No run store; PR state comment + branch name + commit trailers | The trigger always results in a PR, so GitHub already holds the record. |
| Copilot session ID is chosen by Mint (`--session-id <uuid>`), stored in the state comment before launch; resume is primary, context reconstruction the fallback | Documented flag [V]; removes ID discovery. |
| Credentials never in config; encrypted vault unlocked once; secret values are never accepted as flag values | Flags choose a *source* (prompt, stdin); values in argv leak via process lists and history. |
| Credential injection by a Mint-owned proxy in a sidecar container, not by the agent CLI | Backend-independent. Copilot has `injectHosts`; Claude Code has no equivalent. |
| **Mint pushes, the agent never does** (`push: mint` is the only mode): the agent commits locally, Mint validates the diff, then pushes with the push credential from the host side | Closes the "agent token can write directly" gap found in the prior-art review; the container holds no GitHub credential at all. Agent-side pushing (`push: agent`) is deferred until short-lived, per-repo minted tokens exist (a GitHub App would bring that back); until then PATs are too long-lived to hand to an agent. |
| Docker is driven through the `docker` CLI via `os/exec` behind an interface; the Go SDK is deferred | `github.com/docker/docker` is deprecated; `moby/moby/client` is v0.x, pre-1.0, adds ~78 packages and ~8 MB [V]. |
| Hybrid TLS proxy: MITM and credential injection only on credentialed hosts, opaque CONNECT tunnels for other allowed hosts | Smallest interception surface. Prototype worked with git, gh, node, curl (S4). |
| Flow engine with typed stages (agent / action / wait) | Analyze, implement, simplify, test, publish, review loop, nightly integration share one shape. |
| Integration is a per-project strategy, `merge` (interval watcher) or `next-branch` (nightly), and is the only merge path; the gate is the original PR, with no approver on the `next` PR | Everything flowing into `next` was already approved; the merge account is a `main` bypass actor, safe because no agent holds any GitHub token (D-1 closed). A GitHub merge queue is not used: it is unavailable on personal-account repos. |
| First source after GitHub is email over IMAP, read-only first | No public endpoint, durable mailbox queue, lowest onboarding cost (S9). |

## 4. Architecture

```
Sources ──Event──► Router ──► Flow engine ──► Runtime ──► Agent
(github, email,    (verify,    (stages, actions,   (docker |     (copilot |
 whatsapp, cron)    dedupe,     waits, budgets,     hostsandbox)  claude)
                    sender→project, output checks)
                    trust tier)
     ▲                                  │
     └──── Responder (comment, reply, draft) ◄────┘

Credential broker: vault ► per-session token minting ► injecting proxy sidecar
State: GitHub only (PR state comment, labels, branches, commit trailers)
```

### 4.1 Domain types (package `internal/core`)

```go
type Event struct {
    ID       string      // globally unique per source; dedupe key (email Message-ID, wamid, GitHub event id)
    Source   string      // "github", "email", "whatsapp", "cron"
    Kind     string      // "issue", "pr_review", "pr_comment", "message", "tick", "ci_completed", "run_failed"
    Sender   Identity    // verified principal
    Verified bool        // signature / dmarc=pass / authenticated GitHub event / delivered by Meta
    Thread   ThreadRef
    Subject  string
    Body     string      // untrusted
    Attrs    map[string]string
}

type Identity struct{ Scheme, ID, Display string } // "github:login", "email:a@b.c", "whatsapp:+32..."
type ThreadRef struct{ Source, Key string }
type Message struct{ Text string; Template string; Attrs map[string]string }

type Policy struct {                     // generic sandbox definition
    Filesystem  FSPolicy                 // readonly, readwrite
    Network     NetPolicy                // named bundles (github, go, node, python, copilot), allowed hosts,
                                         // blocked hosts (override allowed), subdomain matching
    Credentials map[string][]string      // credential name -> hosts it may be injected for
    Limits      Limits                   // cpu, memory, pids, wall clock, stall timeout
}

type Secret struct{ /* zeroizable; String/GoString/Format/MarshalJSON/MarshalText redact */ }
```

Typed errors in core: `ErrWindowClosed` (reply outside a messaging window), `ErrRateLimited{RetryAfter}`,
`ErrUnknownChecker`, among others.

### 4.2 Interfaces

```go
type Source interface {
    Name() string
    Run(ctx context.Context, emit func(Event)) error // owns cursor/polling/webhook
    Reply(ctx context.Context, t ThreadRef, m Message) error // called only after approval
}
type Acker interface { Ack(ctx context.Context, e Event) error } // optional, found by type assertion:
                                                                 // marks the source message handled

type Runtime interface {
    Name() string
    Preflight(ctx context.Context, p Policy, agent AgentInfo) error // may reject an agent (e.g. Claude on host)
    Start(ctx context.Context, spec SessionSpec) (Session, error)
}

type Agent interface {
    Name() string
    Preflight(ctx context.Context, rt RuntimeInfo) error     // version gate, token-type check, effective policy
    Command(req AgentRequest) (AgentCommand, error)          // rebuilds the FULL lockdown every turn
    CredentialEnv() map[string]string                        // credential name -> env var
    Capabilities() AgentCaps
    ParseResult(out SessionResult) (AgentOutcome, error)
    RepoConfigPolicy() RepoConfigPolicy                      // which repo files/keys to reject or neutralize
}
type AgentCaps struct {
    CanResume, ChooseSessionID, StructuredUsage, NeedsInputSignal, HostSandbox bool
    Transport string // "prompt-flag" | "acp"
    CostUnit  string // "ai-credits" | "usd"
}
type AgentRequest struct { Prompt string; Policy Policy; SessionID string; Resume bool; Model string /* ... */ }

type Action interface {
    Name() string
    Do(ctx context.Context, rc *RunContext, with map[string]any) (Outcome, error)
}
```

Registries (`RegisterSource`, `RegisterRuntime`, `RegisterAgent`, `RegisterAction`) populate the engine at startup.
Removing Copilot means deleting two packages and their registration lines.

## 5. Flows, runs, and stages

- **Trigger**: matches an event (or cron tick) and starts or resumes a Run.
- **Flow**: a named graph of stages.
- **Run**: one execution of a flow, identified by the work-item key (for example `github:owner/repo#123`). The Run
  record is the PR (section 6).
- **Stage kinds**: `agent` (named profile does creative work), `action` (Mint does deterministic work), `wait` (park
  until an event arrives; no process stays alive).
- **Outcomes** select the next edge: `done`, `fail`, `retry`, `needs_input`, `stalled`, custom labels.

### 5.1 Agent profiles

```yaml
agents:
  analyst:    { adapter: copilot, prompt: analyze,        sandbox: readonly, tools: [read] }
  coder:      { adapter: copilot, prompt: implement,      sandbox: default,  tools: [read, write, "shell(git:*)", "shell(go:*)"] }
  simplifier: { adapter: copilot, prompt: simplify,       sandbox: default }
  fixer:      { adapter: copilot, prompt: address-review, sandbox: default, resume: true }
  reviewer:   { adapter: copilot, prompt: review,         sandbox: readonly, workspace: reuse-readonly,
                fallback: coder }
```

A profile bundles adapter, model (always explicit), prompt, sandbox, tool allowlist, and an optional `fallback`
profile used when the provider returns a rate-limit error after retries. Prompts may come from `.mint/prompts/`
(default branch). Profiles and permissions are operator-owned. A `reviewer` profile may start on the implementer's
retained workspace read-only.

### 5.2 Agent completion and checks

Headless `-p` has no needs-input state and only coarse exit codes [V]. Completion is therefore defined as: process exit
0 **and** a non-empty final response **and** a valid Mint-owned `result.json` sentinel in the run directory
(`status: done | needs_input | failed`, `summary`). The prompt template requires it; a missing sentinel is a failure.
`--output-format json` (JSONL, schema undocumented [U]) is parsed only for diagnostics and usage until the schema is
captured. For Claude Code the final `result` JSON line plays the same role (S10).

After a `done`, Mint runs the `checks` action (test, lint, build commands) inside the same runtime and workspace. On
failure the output is fed back to the same agent session as a new turn, bounded by `max`. Check commands live in
project config; a repo may provide defaults in `.mint/workflow.yml`, but they only run inside the sandbox boundary, and
on the host runtime only if the operator allows it.

`needs_input` posts a structured marker comment and parks the Run (`wait`) with a timeout (default 7 days); on expiry
the Run fails with a notice. Agent approval prompts are never forwarded to a human: anything not pre-approved is
denied.

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

Analysis output is stored in the state comment (or a committed file when large) and passed to later stages as quoted
input.

### 5.4 Flow: PR feedback re-triggers the same Run

```yaml
  pr-feedback:
    on: { source: github, kind: pr_review, state: changes_requested, from: [<operator>], run: existing }
    stages:
      fix:       { agent: fixer, next: verify }
      verify:    { action: checks, on_fail: { retry: fix, max: 2 }, next: push }
      push:      { action: git.push, next: rerequest }
      rerequest: { action: github.request-review, from: [<operator>] }
```

PRs opened by others can start a different flow (an AI review, or nothing) through trigger conditions such as
`author:`. `github.merge` is a flow action and is never agent-requestable.

Approval does not trigger a per-PR merge flow. Merging is owned by the project's **integration strategy** (5.5), which
is the only path by which Mint merges anything.

Guards: approval authority is operator-defined (listed identities, current head SHA; a new push invalidates it);
events matching Mint's signed markers or Mint-applied `mint:*` labels are ignored (the actor is the operator's account, so
actor-based filtering is impossible); iterations per Run are capped; branch protection stays on.

### 5.5 Integration strategies: `merge` and `next-branch`

Merging is a per-project setting. When a project enables a strategy, **every approved PR goes through it**, and Mint
merges nothing outside it. The gate is always the **original pull request**: approved (see below) on its current head SHA, not a draft,
mergeable, and green. Nothing later in the pipeline asks for another
approver.

```yaml
projects:
  acme-api:
    integration:
      strategy: merge              # merge | next-branch | off
      approval: label              # label | review
      approval_label: mint:approved
      approvers: [<operator>]
      where: { draft: false, base: main, not_labeled: hold, checks_green: true }
      # strategy: merge
      interval: 10m                # how often to look for approved PRs
      method: squash
      # strategy: next-branch
      schedule: "0 5 * * *"
      tz: Europe/Brussels
```

**Approval signal.** Because Mint's PRs are authored by the operator's account, GitHub's Approve button is unavailable,
so the default is `approval: label`: the operator applies `mint:approved` to a PR. Rules:

- Mint never applies the approval label itself. A `labeled` timeline event whose actor is in `approvers` counts.
- When Mint first observes the label it records the head SHA as `approved_sha` in the signed state comment. If the head
  changes afterwards (new push) Mint **removes the label**, comments, and the approval is void until re-applied. The
  merge passes `approved_sha`, so a push after approval cannot merge.
- Review feedback uses the same style of explicit signals: a PR review comment (or `/mint fix` comment) from an approver
  starts the `pr-feedback` flow; `mint:changes` can be used as an alternative trigger.
- `approval: review` (native GitHub review state) is also supported, but requires the PR author to differ from the
  approver (a separate machine account), and is off by default.
- Labels can be applied only by users with triage access or above, which on a personal repo means the operator and any
  collaborators the operator added.

**`merge`**: on every `interval` tick, collect PRs (any PR, not only Mint's) that satisfy the gate and merge each one
with the merge token, passing the approved head SHA so a push after approval cannot slip through. Ordering is oldest
approval first.

**`next-branch`**: a scheduled flow that collects the same set once, integrates it onto a branch, and merges that
branch:

```yaml
  nightly-next:
    on: { schedule: "0 5 * * *", tz: Europe/Brussels }
    stages:
      collect: { action: github.collect-prs, next: build }          # records (PR, head SHA) pairs
      build:   { action: git.integrate, branch: next, from: main,
                 strategy: sequential-merge, on_conflict: skip-and-comment, next: pr }
      pr:      { action: github.open-pr, head: next, base: main, next: ci }
      ci:      { wait: checks_green, timeout: 90m, on_fail: notify, next: merge }   # woken by ci_completed
      merge:   { action: github.merge, method: merge, recheck_heads: true }
```

- `next` is rebuilt from `main` each night and force-pushed; only Mint's identity should be able to update it.
- **No approver on the `next` PR.** Everything flowing in was already gated at its own PR, so the `next` → `main`
  merge is trusted by construction. The final merge still waits for the required checks on `next` to be green.
- Each source head SHA is merged with a true merge into `next`, and `next` reaches `main` with a **merge commit**
  (not squash or rebase). GitHub counts a PR as merged when its head is reachable from the base, so the source PRs
  flip to merged [V]; squash and rebase do not flip them [V]. Prerequisites: merge commits allowed and no
  linear-history rule on `main`.
- A PR that receives a push after `next` was built does not flip and stays open; the final head re-check
  (`recheck_heads`) excludes it, or aborts and rebuilds if the excluded PR breaks the build.
- Because this merge skips each source PR's own protections, the pre-merge re-verification of approval, head SHA, and
  checks at collect time and again just before the merge is mandatory.
- Conflicting PRs are excluded with a comment. An optional `resolve-conflicts` agent stage is future work.
- Mint does not create releases. Merging to `main` triggers the repo's release workflow (PAT pushes and merges
  do trigger workflows, unlike `GITHUB_TOKEN` [V]); optionally a `github.dispatch-workflow` action.
- The nightly Run is keyed by date (`next-YYYY-MM-DD` marker on its PR), so a double fire is a no-op.

**How `next` reaches a protected `main` (decision D-1, closed).** `main` keeps its protections; the operator account, as
repository owner or via a ruleset bypass entry (individual users can be bypass actors since May 2026 [V: changelog;
personal-account repos inferred, [U] confirm]), merges the `next` PR as the orchestrator. On a personal repo the owner can
also simply not require reviews. This is acceptable because with `push: mint` (the only mode) the agent session holds no
credential of that account at all: only Mint's own code ever uses the merge PAT. The remaining risk is a bug in Mint's merge action, which is why the collect-time and pre-merge re-verification and the
audit comment on each merged source PR are mandatory. Test T6/T13 still confirm the ruleset behaviour (bypass visible
in the audit log, required checks on `next` enforced).

### 5.6 Operations: scheduling, retries, stalls, reconciliation

- **Concurrency.** `limits.max_concurrent_runs` (global) plus per-project, per-runtime and per-repo caps;
  `windows-sandbox` is capped at 1. Dispatch order: priority label, oldest event, then key. A Run is dispatched only
  if it is not already running or retry-queued in this daemon; the `mint/<key>` branch claim guards other daemons.
- **Stall detection.** Each agent stage has a `stall_timeout` (default 5 min, 0 disables) measured from the last
  agent event or output byte (or stage start). On expiry Mint kills the process tree, records `stalled`, and treats it
  as a retryable failure. Proxy buffering must not mask output (S10). Wall-clock and spend budgets apply in addition.
  Some agents can keep `-p` alive after the final answer; do not assume exit immediately follows the result.
- **Retries.** Failed attempts retry after `min(10s * 2^(attempt-1), 5m)`. A pending retry for a Run is replaced, not
  duplicated. When a retry fires Mint re-reads the work item; if it is closed, unlabelled or merged, the claim is
  released. Rate-limit and 5xx responses honour `Retry-After` first.
- **Reconciliation every tick**, before dispatch: refresh the state of every Run being executed. Closed or merged work
  item: stop the stage and clean the workspace. Label removed, `mint:stop` added, or approval invalidated: stop the
  stage, keep the workspace. A refresh failure leaves running stages untouched and retries next tick.
- **Budgets per Run**: max stages, wall clock, stall timeout, and spend. Spend is computed from the agent's absolute
  cumulative usage, never by summing deltas, and uses an abstract unit with per-agent conversion (Copilot: AI credits
  via `--max-ai-credits`, minimum 30, plus Mint-side turn budgets, because per-run usage is not reliably reported in
  `-p` output [U]; Claude: USD via `--max-budget-usd`).
- **Hooks** (`before_stage`, `after_stage`, `on_fail`, `on_done`, `before_remove`). `before_stage` failure fails the
  attempt; the others are logged and ignored; `after_stage` runs on success, failure, stall, timeout and cancel. Every
  hook has a timeout (default 60 s) and truncated, secret-redacted logs. Hooks never come from the repo.
- **Per-tick preflight.** A failed config reload keeps the last good config and emits an operator-visible error.
  Before each tick Mint re-validates credentials and config; a failure skips dispatch but never reconciliation or stop
  handling.

### 5.7 External effects: typed outputs checked by Mint ("safe outputs")

Agents never perform external writes. Borrowed from gh-aw's design (prior-art review S0), adapted:

- An agent emits typed **output requests** (`comment`, `open-pr`, `reply-draft`, `label`) to a file in the run
  directory. Mint validates each against the project's output policy: type enabled, `max` per Run, target limited to
  the Run's own thread/branch/base, label allow/block globs, length bounds, patch size and **protected paths**
  (`.github/workflows/`, `.mint/`, and operator-listed paths).
- Mint sanitizes each request (neutralize @mentions and closing keywords, URLs outside the allowlist, control
  characters), runs the output checks, and only then applies it with the orchestrator token.
- **Code reaches GitHub only via Mint** (`push: mint`, the only mode). The agent commits locally; the `git.push`
  action validates the diff (size, protected paths, secret scan) and pushes with the push credential from the host
  side. The container holds no GitHub credential, so `gh` and authenticated `git` do not work inside it; everything the
  agent needs from GitHub (issue text, diff, review comments) is passed in through the prompt. A future `push: agent`
  mode would require short-lived, per-repo minted tokens (for example from a GitHub App) and is out of scope until then.
- **Never agent-requestable:** merge (a flow action) and customer email (an operator-approved draft whose recipients
  are fixed by Mint).
- **Layer 1 (always):** secret scan of patch and outputs against vault values and patterns; protected-path and size
  rules. **Layer 2 (optional):** a no-tools, no-credential judge profile returning `prompt_injection`, `secret_leak`,
  `malicious_patch`, `reasons`. On a hit: comments/PRs get a warning marker and a push becomes a draft PR; merges and
  outbound messages are dropped. A judge that errors counts as *unknown*, not clean. The judge is advisory; branch
  protection and the human gate remain the control.
- **Staged mode.** `staged: true` per project or flow renders every output request as a preview comment/log entry
  without applying it; used for the first runs of a new flow or sender tier.

### 5.8 Input sanitization

Before untrusted text is quoted into a prompt Mint neutralizes @mentions and bot triggers, strips control characters,
converts raw HTML, and caps size (default 0.5 MB and 65k lines). Attachments are metadata only and are never
auto-opened.

## 6. State lives in GitHub

- **Run identity and claim**: branch `mint/<key>` and the PR. Claim by `POST git/refs`: 201 means won, 422 "Reference
  already exists" means lost or resume [I: the 422 text is only third-party-seen; test T11]. Distinguish transient 422s.
- **State comment**: one comment on the PR (not the body, which humans edit) authored by Mint's identity, containing
  `flow`, `stage`, `attempt`, budget counters, issue link, `agent_session`, and `seq`. It is **HMAC-signed**
  (key derived from the vault with HKDF; payload includes `kid`, run key, `seq`, and head SHA). Accept a comment only
  if its author matches by numeric `user.id` (plus `performed_via_github_app.id` when present) *and* the HMAC
  verifies. Replay (lower `seq`) and deletion are handled by reconstructing from branch trailers and failing safe.
  Hard state budget 8 KB (GitHub's comment limit is ~64 KB); large analysis output is committed as a file under
  `.mint/runs/` on the run branch.
- **Checkpoints**: each stage updates the comment and pushes its commits (a commit per stage, e.g.
  `mint(simplify): ...`, with `Mint-Run:` trailers). Agent stages commit at their end; Mint pushes (5.7).
- **Before the first commit** there is no PR: open a draft PR early, or record the claim on the issue (label
  `mint:claimed` plus a signed comment). Analysis output goes into the state comment so it survives crashes.
- **Recovery**: at start and each tick, list open PRs with the Mint marker whose `stage` is not `done` and reconcile.
  Rebuild workspaces by cloning the PR branch. On start, remove workspace volumes, agent homes and containers (label
  `mint.run`) of Runs whose PR is closed or merged. Workspace names are sanitized to `[A-Za-z0-9._-]`, and the
  workspace path must resolve (after symlink evaluation) under the workspace root before every launch.
- **Cron**: no last-fire record; the date-keyed PR makes double fires harmless.
- **Non-GitHub sources**: the order is *emit → create tracking issue → `Ack` → mark handled*. The source message ID
  (email `Message-ID`, WhatsApp `wamid`) goes into a machine-readable field of the issue body (`mint-source-id:`);
  dedupe is a search for it. WhatsApp cannot be reconciled after downtime, so a buffering relay is advised.
- **API cost**: reconcile with conditional requests: repo-wide `issues/comments?since`, open-PR list, label list; no
  per-PR polling; the search API only for cross-repo discovery (30/min). 304 responses do not count against the primary
  limit [V]. ETags may change with token rotation [U, T4], so the ETag cache is keyed by (URL, token generation).
  Installation floor is 5,000 requests/hour; roughly 27 repos at a 60 s tick would exhaust it if ETags fail. Respect
  secondary limits (100 concurrent, 900 points/min), serialize writes, and honour `Retry-After`.
- **Stages must be idempotent**: a crash re-runs the current stage from the last pushed state.

### 6.1 Agent sessions and resume

Copilot stores sessions under `COPILOT_HOME/session-state/<id>/events.jsonl` [V]; the cache directory is not moved by
`COPILOT_HOME`. Mint generates the session UUID, passes `--session-id <uuid>` on the first turn (recording it in the
state comment before launch), then `--resume=<uuid>` on later turns. Never use bare `--resume` or `--continue` (bare
`--resume` errors under `-p` [V]).

- `COPILOT_HOME` is a deterministic per-run directory `<state>/agent-home/<run-key>` on the host, bind-mounted into the
  container or used directly by the Windows runtime; also set a writable ephemeral cache dir (`XDG_CACHE_HOME`) and
  `--sync-session=off`. `events.jsonl` is a sensitive transcript.
- Never share the operator's normal Copilot home with daemon runs. Mint writes the sandbox policy into the per-run home.
- Delete a run's agent home when the PR is merged or closed; sweep orphans periodically.
- If the session is missing or resume exits non-zero [U: behaviour unverified], fall back to a fresh session primed
  with the issue, stored analysis, current diff, and review comments, and note the fallback in the state comment.
- Resumed sessions keep untrusted content the agent read earlier; sandbox, token scope, and gates stay in force.
- Claude Code resume does not restore tool, settings, MCP or permission flags, so `Command` rebuilds the whole
  lockdown on every turn (S10).

## 7. Runtimes

### 7.1 Docker runtime (default)

- Image defined by `.mint/Dockerfile` (toolchain only). Mint publishes a small `mint-base` image (non-root user, git,
  gh, CA and proxy environment, agent CLI). Both `.mint/Dockerfile` and any devcontainer may `FROM` it.
- Build and update: `mint image build|update|prune`. Tags `mint/<owner>-<repo>:<dockerfile-hash>`; `update` rebuilds
  with `--pull` when the hash or base digest changes; `serve` can do this on a schedule; `prune` removes old tags.
  Builds get no secrets and no host mounts, and use the default-branch Dockerfile only.
- **Docker access**: Mint drives the `docker` CLI via `os/exec` behind a narrow interface (JSON output, label
  filters, orphan sweep). The Go SDK (`github.com/moby/moby/client`) is deferred. The Docker socket and host `/tmp`
  are never mounted into containers; temp variables are per run; host environment is forwarded only through an
  allowlist.
- **Run settings are Mint's, not the Dockerfile's**: all capabilities dropped, `no-new-privileges`, default seccomp,
  pids/memory/CPU caps, read-only root with tmpfs scratch, non-root user. Repo `devcontainer.json` `runArgs`, `mounts`,
  `privileged`, `initializeCommand`, `features` are never honoured. Hardening flags on Docker Desktop are inferred to
  work [I] and need the Windows test list.
- **Workspace**: a named volume populated by an in-container clone (not a `git worktree`, whose `.git` points back to
  the main repo; avoid `/mnt/c` bind mounts). The per-run agent home is the only host-path mount.
- **Egress topology** (the only design Docker Desktop can enforce [I]): an `--internal` network
  (`mint-int-<run>`) containing the agent container and a **Mint-owned proxy sidecar**, which has a second interface on
  a normal egress network (`mint-egress`). The agent's only route is the proxy; `HTTPS_PROXY` is a convenience and
  `--internal` is the boundary. A host proxy reached via `host.docker.internal` is rejected because it cannot be
  enforced. Whether `--internal` also blocks the host gateway and external DNS on Docker Desktop is unverified [U];
  mitigations if not: do not publish the sidecar, separate egress network, `--dns` to an unroutable address.
- **Agent permissions**: in `-p` mode anything not pre-approved is denied, so profiles use a tool allowlist
  (`--allow-tool='shell(git:*)'`, `write`, ...) plus `--deny-tool` for dangerous commands; `--allow-all-tools` is only
  an option inside the container where the container is the boundary. Also set `--no-ask-user`, `--no-auto-update`,
  `-s`, and an explicit `--model`.
- Optional hardening: Enhanced Container Isolation (Docker Business only) and gVisor/Kata/Firecracker behind the
  same interface. Note the WSL2 shared-kernel caveat.

### 7.2 Windows host runtime (`windows-sandbox`)

For Windows-specific code and integrations. It is today's Copilot sandbox launch, and **supports Copilot only** (the
Claude Code sandbox does not exist on native Windows; the runtime refuses Claude).

- Same preflight as today: sandbox policy check, project-settings checks, env allowlist, isolated `GH_CONFIG_DIR`,
  `--secret-env-vars`, MCP disablement. Preflight reads the *effective* settings (`copilot config`) and fails closed;
  a managed policy such as `sandbox.failIfUnavailable` or `permissions.disableBypassPermissionsMode` can change
  behaviour.
- Selection: project `runs-on: windows-sandbox` or an issue label such as `mint:windows`; operator config decides which
  projects may use it. A label alone never escalates.
- The host process is not a strong boundary (README). Therefore: trusted senders only, concurrency 1, dedicated per-run
  Copilot home, and a plan/approval gate by default.
- The generic `Policy` compiles into Copilot settings: `sandbox.enabled`, `sandbox.userPolicy.network.allowedHosts` and
  `blockedHosts`, `sandbox.credentials.envVars.<NAME>.injectHosts` [V]. `allowLocalNetwork` (needed on Windows for the
  credential proxy) and the project-configuration key names are not in the official pages read [U]; gate them on the
  installed version. Built-in file tools are not OS-enforced, so combine with `--allow-tool` write patterns. Do not use
  `copilot sandbox ca trust` (OS-global).

### 7.3 Credential broker and injecting proxy

- **Identity: the operator's own account with fine-grained PATs, no GitHub App.** Mint is only for the operator's own
  repositories, so each PAT is a fine-grained PAT with the operator as resource owner, restricted to selected
  repositories and minimal permissions (the creation flow Mint already guides with a pre-filled link). The
  collaborator limitation of fine-grained PATs [V] does not apply because the repos are the operator's own.
- **Consequence: the author is the operator.** Mint's commits, PRs, comments and merges all appear as the operator's
  account. Therefore (a) GitHub's native "Approve" is unavailable on Mint's PRs (no self-approval [V]), so approval is an
  explicit signal (section 5.5, `approval: label`); (b) Mint cannot recognise its own events by account, so it uses
  signed state markers and the set of `mint:*` labels it applies (approval labels are never applied by Mint); (c) commits
  carry a Mint author identity and `Mint-Run:` trailers. A dedicated machine-user account remains possible later for
  native reviews (`approval: review`), but then personal-repo access needs a classic PAT (fine-grained cannot be used as a
  collaborator on another user's repo [V]); not chosen.
- **No minting.** PATs are created by the operator, have an operator-chosen expiry (possibly org-limited), and cannot be
  narrowed per session or revoked per run. Mitigations: roles are split across PATs (below); no agent holds any GitHub
  credential (`push: mint`); Mint validates each PAT at start (repo access plus a permission probe), warns N days
  before expiry, and stops dispatch on expiry or a 401. Do not assume token shape (`ghp_`, `github_pat_`); redact by
  exact value.
- **Three PATs by role** (per-endpoint permission mapping is unverified; confirm with `X-Accepted-GitHub-Permissions`):
  - *Orchestrator PAT*: Issues write, Pull requests write, Contents read, Checks read, Commit statuses read. Polling,
    claiming labels, state comments, review requests. Never enters a sandbox.
  - *Push PAT*: Contents write + Pull requests write, held by Mint's `git.push`/`open-pr` actions and never given to an
    agent.
  - *Merge PAT*: Contents + Pull requests write, used by the `merge` strategy per PR and by the `next-branch` merge,
    where the operator account's admin/bypass on `main` applies (5.5). All three PATs belong to the same account; a bypass is
    a property of the *account*, not the token, so the merge PAT is kept in the vault only, never in a sandbox, and
    permissions differ per PAT (the push and orchestrator PATs cannot merge).
  - The agent session itself gets read-only access at most. Nothing is granted Administration, Workflows, secrets, or
    members. Rulesets cannot restrict an actor to `mint/*` and `next`, so the enforceable controls are: no bypass for
    any account whose token an agent can hold (the merge account's bypass on `main` is used only by Mint's merge action),
    required PR/reviews/checks on `main`, optional restrict-creations/updates rulesets on other refs with an operator
    admin bypass, and no `workflows` permission. Push rulesets (path rules) exist only on private/internal repos.
- **Rate limits.** All PATs of one account share that account's budget (5,000 requests/hour floor [I]), so polling cost
  scales with repos per account; ETags are stable because tokens are not rotated.
- **Proxy**: a Mint-written Go forward proxy (stdlib `crypto/tls` and `net/http`; `goproxy` is the fallback if
  h2/websockets prove necessary), run as the sidecar. Because no GitHub credential ever enters a session (`push: mint`),
  **v1 needs no GitHub credential injection**. Its v1 job is the **egress allowlist**: allowed hosts are opaque CONNECT
  tunnels (the model endpoint, package registries, named bundles), everything else gets 403, with a per-run request log.
  The only secret a session holds is the agent CLI's own credential (Copilot `COPILOT_GITHUB_TOKEN`, or the Anthropic
  key). If a placeholder for that credential is verified to work [U], the proxy can inject it in hybrid mode (MITM and
  header injection only on that credentialed host, with a per-session CA whose key lives only in the sidecar's memory;
  the agent container gets only the public certificate and a placeholder); otherwise it is passed as an env var and the
  egress allowlist limits exfiltration. The S4 prototype verified git, gh 2.89, node 22 and curl through the MITM mode
  against a fake upstream (Node needs `NODE_USE_ENV_PROXY=1`); real Copilot traffic is untested [U]. GitHub credential
  injection returns only if a future `push: agent` mode is added.
- Proxy hardening: refuse requests to private, loopback, link-local and Docker gateway addresses (SSRF); pin DNS;
  overwrite any agent-supplied `Authorization`; Basic for git on `github.com`, token/Bearer for `api.github.com`;
  HTTP/1.1 on the MITM leg first; idle and total timeouts; per-run request log (host, allowed/denied) kept with the
  transcript for allowlist tuning; redacted logs. Fallback if a client pins certificates: a credential helper over a
  socket.
- The image sets `HTTPS_PROXY`, `NO_PROXY` for the proxy host, `NODE_USE_ENV_PROXY=1`, and CA variables
  (`SSL_CERT_FILE`, `GIT_SSL_CAINFO`, `NODE_EXTRA_CA_CERTS`, `CURL_CA_BUNDLE`) pointing at a tmpfs path.
- **Per-agent credential notes.** Copilot: `COPILOT_GITHUB_TOKEN` must be a fine-grained PAT with the Copilot Requests
  permission, or a `gho_`/`ghu_` token; classic `ghp_` is rejected [V]; precedence `COPILOT_GITHUB_TOKEN` >
  `GH_TOKEN` > `GITHUB_TOKEN`; always set it explicitly and keep it separate from the repo token; placeholder behaviour
  and `HTTPS_PROXY`/`NODE_EXTRA_CA_CERTS` outside the sandbox are unverified [U]. Claude Code: `ANTHROPIC_API_KEY` is
  always used under `-p`; `HTTPS_PROXY` and `NODE_EXTRA_CA_CERTS` are honoured, SOCKS is not [V]; a placeholder swapped
  by the proxy is likely [I]; `ANTHROPIC_BASE_URL` can avoid TLS interception entirely. Run duration is capped below
  token TTL; longer work restarts the turn with a fresh token via resume.

## 8. Credentials, vault, and unlock

- Config files contain credential *names* only. Config load rejects secret-looking values (`ghp_`, `github_pat_`, PEM
  blocks, high-entropy strings).
- The vault extends the existing design: random store key in `master.enc` under the master password; separate
  encrypted files per credential (PBKDF2 + AES-GCM with associated data bound to the credential name). Named
  credentials: `github-orchestrator-pat`, `github-push-pat`, `github-merge-pat`, `github-operator-pat`, `copilot-token`,
  `email-imap-password`, `email-oauth-refresh-token`, `email-oauth-client-secret`, `whatsapp-token`,
  `whatsapp-app-secret`, `whatsapp-verify-token`, `webhook-secret`.
- **The vault needs a runtime write path**: OAuth refresh tokens rotate, so the unlocked daemon must atomically
  re-encrypt a credential file.
- `mint serve` unlocks once and keeps secrets in memory as `Secret`. Unlock sources are flags that choose a *source*:
  hidden prompt (default), `--password-stdin`. A plain `--password=...` flag is rejected. Optionally later: OS credential
  store.
- `mint creds add|list|rm` manages credentials with hidden prompts; `list` shows names only.
- Secrets never reach argv, child env (except via the allowlisted injection path), logs, or error strings: enforced by
  the type and by tests.

## 9. Sources, routing, and trust

```yaml
projects:
  acme-api:
    repo: github.com/me/acme-api
    runs-on: container          # or windows-sandbox
    sandbox: default
    push: mint                  # only mode for now (the agent never pushes)
    staged: false
    senders:
      - { match: "github:org/maintainers", trust: maintainer }
      - { match: "email:cto@acme.com",     trust: customer }
      - { match: "whatsapp:+32...",        trust: customer }
triggers:
  - { source: github, kind: issue,      label: mint,   project: auto }
  - { source: github, kind: pr_comment, mention: "@mint", project: auto }
  - { source: email,  to: support@me.dev, project: by-sender }
```

- **Router**: verify, dedupe on `Event.ID`, resolve sender → project → trust tier, match a trigger, enqueue. First
  implementation covers GitHub only.
- **Trust tiers map to capabilities.** `maintainer` can have branches pushed (by Mint) to `mint/*`. `customer` gets a
  read-only token and produces a *draft* reply that needs operator approval; the router calls `Reply` only after
  approval. Unknown senders are dropped. Rate caps limit tracking-issue creation per sender.
- **Event vocabulary** for triggers includes `ci_completed` (wakes `wait: checks_green` instead of polling) and
  `run_failed` (drives failure notification through the same flow engine).
- **Verification.**
  - GitHub: authenticated API calls (or signed webhooks).
  - Email: accept only `dmarc=pass` (aligned) in an `Authentication-Results` header whose `authserv-id` matches the
    configured `trusted_authserv` and which sits in the provider-added block; ignore ARC; map identity on the `From`
    address-spec only; `From` alone is spoofable.
  - WhatsApp: verify `X-Hub-Signature-256` on the raw body. `Verified` means "delivered by Meta"; the sender number is
    Meta-asserted. Replies outside the 24-hour window require an approved template (`ErrWindowClosed`).
- **Conversation continuity**: sessions are keyed by `ThreadRef`; follow-ups resume the same Run.
- **First source after GitHub: email over IMAP, read-only first**, then approval-gated replies (IDLE plus a poll
  backstop, `Message-ID` as `Event.ID`, provider-added mailbox label as handled-state; go-imap v2 is beta, so pin it
  behind an internal wrapper). WhatsApp comes later and needs a public HTTPS endpoint (tunnel or buffering relay) and
  has unresolved platform-policy risk for AI providers.

## 10. Configuration layout

| Location | Owner | Contents |
|---|---|---|
| `<config dir>/config.json` | existing | interactive launcher (remote, Copilot path) |
| `<config dir>/mint.yaml` | operator | projects, senders, triggers, flows, agent profiles, sandbox profiles, hooks, budgets, output policy |
| `<config dir>/vault/` | operator | encrypted credentials, `master.enc` |
| `<config dir>/runs/agent-home/<key>/` | daemon | per-run agent homes |
| repo `.mint/Dockerfile` | repo | session image (toolchain only) |
| repo `.mint/workflow.md`, `.mint/prompts/` | repo | prompt templates, default checks |

All repo-owned files are read from the default branch. Operator config is validated on load (unknown keys are
errors). Dynamic reload of operator config and repo prompts is supported with a validation gate (a bad reload keeps the
last good config). A repo prompt changing under a running Run takes effect at the next stage.

## 11. CLI surface

- `mint [copilot args]`, `mint setup`: unchanged.
- `mint run <issue-url|number>`: one-shot Run of `issue-to-pr`; first usable milestone.
- `mint serve`: the daemon (prompts for the master password once).
- `mint image build|update|prune`.
- `mint creds add|list|rm`.
- `mint status`: lists reconciled Runs from GitHub (and daemon health if running).

## 12. Security model

| Asset | Threat | Controls |
|---|---|---|
| Root credentials (PATs, Copilot token) | exfiltration by agent or repo code | vault + `Secret` type; never in child env; no GitHub credential ever enters a session (Copilot's own credential is the only injected secret); PATs split by role; expiry warnings |
| Host filesystem and user profile | agent or build escapes | container isolation (caps dropped, non-root, read-only root, no Docker socket, no host mounts except agent home); Windows runtime only for trusted work |
| Repository | malicious push, workflow tampering | Mint validates and pushes; protected paths (`.github/workflows/`, `.mint/`); no `workflows` permission; rulesets; branch protection; merge only through the project's integration strategy, gated at the original PR; the operator's merge PAT is used only by Mint's merge action and never enters a session |
| Run state | tampering with the state comment | author check by numeric id + HMAC with `seq` |
| Prompt injection via issues, PRs, email | agent follows attacker instructions | untrusted-data quoting and input sanitization; trust tiers; egress allowlist; no secrets in the sandbox; typed output requests; optional judge; read-only token for low trust; outbound messages drafted for approval; staged mode |
| Approvals | forged or stale approval triggers merge | operator-listed identities; head-SHA match; ignore Mint's own events; branch protection retained; pre-merge re-verification for `next` |
| Dockerfile / devcontainer / repo agent config | repo-controlled code execution | default-branch reads; hash approval; Mint-owned run flags; per-adapter `RepoConfigPolicy` (Copilot: reject conflicting settings; Claude: `--setting-sources user`, `--strict-mcp-config`, `--disable-slash-commands`, managed settings baked into the image, because `-p` has no trust dialog and would otherwise run repo hooks, `env`, `apiKeyHelper` and `.mcp.json` servers) |
| Proxy | SSRF, bypass, CA misuse | internal network is the boundary; private-address refusal; name-constrained per-session CA kept in memory; Authorization overwrite; request log |
| Cost | runaway loops | per-Run budgets, stall timeout, iteration caps, `mint:stop` |
| Source spoofing | forged email or message | DMARC-pass from the trusted authserv; webhook signatures; sender allowlist |

Residual risks: shared-kernel container escape (WSL2 caveat; gVisor/Kata/Firecracker behind the same `Runtime`
interface), exfiltration through allowed hosts (keep the allowlist short and log egress), the proxy as a trust surface,
malicious code pushed to a branch (rulesets, review), unknown Copilot unattended-use terms (section 15).

## 13. Observability and failure handling

- Structured logs per Run key (no secrets). Per-run transcript and egress log retained in the agent home until cleanup.
- Failures are reported on the PR/issue (and by an email responder when configured), naming the stage and cause, via the
  `run_failed` event.
- Retries and stalls follow section 5.6. A hung or failing checker, judge, or sink must not take down the daemon and is
  never read as success.
- Graceful shutdown: stop dispatching, let stages checkpoint, kill containers and process trees (Windows Job Objects).
  PATs are not revoked (they are long-lived; the operator revokes them in GitHub).

## 14. Windows specifics

- The daemon host is Windows; Docker (Linux containers) through Docker Desktop/WSL2, endpoint
  `npipe:////./pipe/docker_engine`.
- Process-tree kill via Job Objects; long-path handling for workspaces.
- Webhook-based sources (WhatsApp, optional GitHub webhooks) need a loopback listener plus a tunnel or relay;
  `cloudflared` as a Windows service is the default, a cloud relay the resilient option.
- The existing `ui_windows.go`, MSIX packaging, and AppInstaller flow stay; the daemon ships in the same binary.
- CI already runs Linux, macOS, and Windows.

## 15. Open questions

1. Copilot terms for unattended or multi-user use of a seat, and `COPILOT_GITHUB_TOKEN` licensing. Docs endorse CI and
   headless use with env tokens; the terms text was not reached. A human must read it. Also Anthropic's terms for
   subscription tokens in an unattended service, and WhatsApp AI-provider terms.
2. Whether `--internal` networks on Docker Desktop block the host gateway and external DNS (S3, Windows test).
3. Copilot behaviour with a missing session on `--resume`, `--output-format json` event schema, exit codes, and
   per-run usage reporting (S1 hands-on tests).
4. Whether a placeholder `COPILOT_GITHUB_TOKEN` and model-endpoint proxying (`HTTPS_PROXY`, `NODE_EXTRA_CA_CERTS`)
   work outside the Copilot sandbox.
5. `allowLocalNetwork` and project-configuration key names for the Copilot sandbox on the installed version.
6. Per-endpoint fine-grained PAT permission mapping; label `labeled` timeline-event actor reliability and timing for
   approval; who can edit or delete others' comments; ruleset user-bypass on personal-account repos; whether PAT pushes
   and PR creation need workflow approval; rate-limit sharing across one account's PATs (spike S5).
7. Gmail/Graph backend choice if the operator's mailbox lacks IMAP; Meta business-verification requirements and
   pricing; Graph subscription lifetimes.
8. "Update" scope: this design treats it as session-image refresh; Mint's own binary already updates via AppInstaller.
9. `go.yaml.in/yaml/v3` (already an indirect dependency) for `mint.yaml`.

## 16. Prior art and what we borrow

- **OpenAI Symphony spec** and the **Itervox** Go implementation: poll, per-issue workspace, reconcile, retry,
  stall detection, hook semantics, event vocabulary. Itervox runs Claude Code and Codex, skips permissions in
  high-trust setups, and does not manage scoped credentials; inspiration only.
- **GitHub Agentic Workflows (gh-aw)**: **not adopted**; Mint always runs its own runners. Borrowed: safe outputs
  (typed requests validated and applied by a separate trusted step), protected-path rules, optional threat-detection
  judge, network allowlist ergonomics, per-run egress log. Parts of gh-aw's docs were unreachable (S0), so its
  protected-files rules and agent-side emission mechanism are unverified.
- **GitHub Copilot coding agent**, **Claude Code GitHub Action**, **Open SWE**, **Composio Agent Orchestrator**,
  **Baton**: issue-to-PR automation of varying scope; none combine Copilot CLI, Windows-native sandbox sessions, scoped
  credential injection, multi-source triggers, and this flow model.

## 17. Research index

| Spike | File | Status |
|---|---|---|
| S0 prior art | [S0-prior-art.md](spikes/S0-prior-art.md) | documentation done; gh-aw second pass blocked by host |
| S1/S2 Copilot CLI, ACP | [S1-S2-copilot-cli.md](spikes/S1-S2-copilot-cli.md) | documentation done; hands-on T1 to T11 pending |
| S3/S4 Docker and proxy | [S3-S4-docker-and-proxy.md](spikes/S3-S4-docker-and-proxy.md) | documentation and Linux prototype done; Windows tests pending |
| S5/S6/S8 GitHub platform | [S5-S6-S8-github-platform.md](spikes/S5-S6-S8-github-platform.md) | documentation done; T1 to T17 pending |
| S9 sources | [S9-sources.md](spikes/S9-sources.md) | documentation done; hands-on pending |
| S10 Claude adapter | [S10-claude-adapter.md](spikes/S10-claude-adapter.md) | documentation done; hands-on pending |
| S7 Windows host sandbox | (none) | hands-on only |
