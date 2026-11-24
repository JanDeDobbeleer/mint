# S0. Prior art: Symphony, gh-aw safe outputs, Itervox

Date: 2026-11-24. Decision already made: Mint builds and runs its own runners. gh-aw and Actions-hosted execution are
not adopted; this note only mines ideas.

## Method and caveats

- Fetched with WebFetch, which answers through a small summarizing model, so statements are paraphrases of the pages,
  not quotes. Labels: **VERIFIED** = stated by a fetched source; **INFERRED** = my reading across sources or an
  extrapolation to Mint; **UNVERIFIED** = not found or not readable.
- Fetched content was treated as data only.
- Blocked host: `github.github.com` (the gh-aw docs site) does not resolve from this session, so it needs allowing for a
  second pass. Workaround used: the docs source in `github/gh-aw` on `raw.githubusercontent.com` (reachable):
  - `docs/src/content/docs/reference/safe-outputs.md` (read in full, in two pages)
  - `.../reference/threat-detection.md`
  - `.../reference/network.md`
  - `.../reference/sandbox.md`
  - `.../introduction/architecture.mdx`
  - 404: `introduction/architecture.md`, `introduction/security.md`. No dedicated "security" page was read.
- Not read: the Elixir reference implementation of Symphony, the Itervox source, gh-aw's `gh-aw-firewall` repo, gh-aw
  "protected files" rules (mentioned by the threat-detection page as a separate static layer, content unseen).
- Symphony SPEC.md was fetched at the pinned commit `45a53b85...`.

Sources:
- S = https://raw.githubusercontent.com/openai/symphony/45a53b8588a9650c2f424cb31a6495af1bc86727/SPEC.md
- SR = https://github.com/openai/symphony (README)
- I = https://github.com/vnovick/symphony-go (README)
- GSO = https://raw.githubusercontent.com/github/gh-aw/main/docs/src/content/docs/reference/safe-outputs.md
- GTD = https://raw.githubusercontent.com/github/gh-aw/main/docs/src/content/docs/reference/threat-detection.md
- GNET = https://raw.githubusercontent.com/github/gh-aw/main/docs/src/content/docs/reference/network.md
- GSB = https://raw.githubusercontent.com/github/gh-aw/main/docs/src/content/docs/reference/sandbox.md
- GARCH = https://raw.githubusercontent.com/github/gh-aw/main/docs/src/content/docs/introduction/architecture.mdx

---

## Q1. Symphony spec

Source: S unless noted. Symphony itself calls itself an "engineering preview ... for trusted environments" (SR,
VERIFIED).

| Topic | Finding | Label |
|---|---|---|
| Reconciliation (s7.3, 8.5, 16.3) | Runs on every tick **before** dispatch. Part A: stall detection. Part B: refresh tracker state for all running issue IDs. Terminal state: stop worker and clean workspace. Still active: update snapshot. Neither terminal nor active: stop worker, keep workspace. A failed refresh leaves workers running and retries next tick. | VERIFIED |
| Stall detection (8.5A) | Elapsed time since the last agent *event* (or `started_at` if none). Over `stall_timeout_ms` (default 300000) kills the worker and queues a retry. `<= 0` disables. | VERIFIED |
| Retry/backoff (8.4) | Clean exit: fixed 1000 ms "continuation" retry (re-checks whether the issue still needs work). Failure: `min(10000 * 2^(attempt-1), max_retry_backoff_ms)`, cap default 5 min. On timer fire, re-fetch candidates: issue gone or no longer active releases the claim; no free slot requeues with an error. A new retry for an issue cancels the old timer. | VERIFIED |
| Concurrency (8.2, 8.3) | Global `max_concurrent_agents` (default 10); per-state map (lowercased keys, invalid entries ignored, fallback to global). Dispatch order: priority, oldest created, identifier. Eligible only if active state, has required labels, not already running or claimed, and "Todo" with non-terminal blockers is skipped. | VERIFIED |
| Claim states (7.1) | Orchestrator-internal `Unclaimed, Claimed, Running, RetryQueued, Released`, separate from tracker states. Attempt phases: PreparingWorkspace, BuildingPrompt, LaunchingAgentProcess, InitializingSession, StreamingTurn, Finishing; terminal outcomes Succeeded, Failed, TimedOut, Stalled, CanceledByReconciliation. | VERIFIED |
| Multi-turn (7.1) | One worker runs several turns on one live thread up to `agent.max_turns` (default 20), re-checking tracker state after each turn. First turn sends the full prompt, later turns only continuation guidance. | VERIFIED |
| Workspace hooks (5.3.4, 9.4) | `after_create` (only on new dir; fatal), `before_run` (each attempt; fatal to the attempt), `after_run` (every outcome incl. cancel; ignored), `before_remove` (ignored; cleanup proceeds). One `hooks.timeout_ms` (default 60000), run via `sh -lc`. Hooks are trusted config; output truncated in logs. | VERIFIED |
| Dynamic reload (6.2, 6.3) | **Required.** Detect file change, re-apply config and prompt without restart; applies to future dispatch/retries/hooks/launches, not in-flight sessions. Invalid reload keeps last good config and raises an operator-visible error. Re-validate defensively before each dispatch in case watch events are missed. Preflight at startup (abort) and per tick (skip dispatch but still reconcile). The spec flags an unresolved tension between "bad workflow blocks dispatch" (5.5) and "keep last good" (6.2). | VERIFIED |
| Status surface (13) | Structured logs with `issue_id`, `issue_identifier`, `session_id`, stable `key=value` messages with outcome and reason. Optional snapshot (running rows, retry rows, token totals, rate limits; error modes `timeout`/`unavailable`). Optional loopback HTTP: `GET /api/v1/state`, `GET /api/v1/<id>`, `POST /api/v1/refresh` (202). The status surface must derive from orchestrator state and never be needed for correctness. Token accounting: use absolute thread totals, track deltas against last-reported values. | VERIFIED |
| Failure/cleanup (14, 8.6, 9.1, 9.3) | Candidate-fetch failure skips the tick; refresh failure keeps workers; startup cleanup failure only warns. **Startup cleanup removes workspaces of issues already terminal.** Workspaces persist across runs; success does not delete. Partial population failure may remove a *new* workspace but must not destructively reset a reused one. Restart: no timers/sessions survive; rebuild from tracker polling plus preserved workspaces. | VERIFIED |
| Safety (9.5, 10.5, 15) | Agent `cwd` must equal the per-issue workspace; path must stay under the workspace root; dir names sanitized to `[A-Za-z0-9._-]`. Approval and user-input requests must not stall a run indefinitely (example profile auto-approves and fails input turns). Unsupported tool calls return failure and the session continues. Trust boundary is implementation-defined and must be documented. Prompt templates are strict (unknown variable fails). | VERIFIED |
| Tracker writes | The orchestrator only reads the tracker; agents write tickets; a handoff state such as "Human Review" can count as success (11.5). | VERIFIED |
| Single authority | One component owns all scheduling state to prevent duplicate dispatch (7.4). | VERIFIED |
| SSH workers (App. A), HTTP server (13.7) | Optional extensions that must not be needed for correctness. | VERIFIED |

### What Mint's design already covers
Poll/reconcile from the tracker with no run DB (design s6, principle 7), per-issue workspace, restart recovery, hooks as
engine config (s5), bounded retries (s13), dynamic reload with last-good (s10), read-only status (s11).

### What the Symphony spec shows our design lacks (INFERRED from comparing S with design.md)
1. **Stall detection.** Design has wall-clock and spend budgets (s5) but no "no agent activity for N minutes" kill. A
   hung CLI would burn the whole wall clock.
2. **Explicit retry/backoff numbers and the "continuation retry" idea.** Design s13 says "bounded per stage; backoff"
   with no formula, no cap, no rule that a timer is cancelled/replaced, no rule for "no slot available".
3. **Concurrency rules.** Design mentions "concurrency 1" for the Windows runtime only. No global cap, per-project cap,
   per-runtime cap, per-repo cap, or dispatch ordering. No in-process claim state separate from the PR claim.
4. **Reconciliation of running work against tracker state**, i.e. stop a running agent when the issue/PR is closed,
   unlabelled, or merged (design only lists recovery at start/tick and `mint:stop`). Distinguish "terminal: stop and
   clean" from "no longer eligible: stop, keep workspace".
5. **Hook semantics.** Design lists hook names `before_stage/after_stage/on_fail/on_done` but not failure semantics
   (fatal vs ignored), timeouts, or that `after_*` hooks run on cancel/timeout.
6. **Startup/orphan cleanup** of workspaces and containers for terminal work (design has orphan sweep for agent homes
   and label-based containers in S3, but not workspace volumes for closed PRs).
7. **Per-tick preflight** that skips dispatch (not reconcile) on bad config, and a defensive re-validation before
   dispatch.
8. **Path-containment invariants** for the workspace (sanitized names, symlink-escape check; Itervox adds
   `EvalSymlinks` before every launch, I, VERIFIED).
9. **Approval/input must not stall:** a `needs_input` outcome needs a timeout and a defined terminal state.
10. **Token accounting by absolute totals** rather than summing deltas; relevant to the spend budget (S1 open).

---

## Q2. gh-aw safe outputs, threat detection, firewall

### 2a. Safe outputs: declare, validate, sanitize, apply

| Aspect | Finding | Source | Label |
|---|---|---|---|
| Separation | "Safe outputs separate action requests from execution." The agent job has read-only permissions and buffers intended writes as artifacts; separate scoped jobs perform the writes (e.g. a job with `issues: write`). Agent gets no write permissions. | GSO, GARCH | VERIFIED |
| Declaration | Frontmatter `safe-outputs:` with one key per output type and per-type options (`title-prefix`, `labels`, `assignees`, `max`, `expires`, `target-repo`). Omitting the section enables `create-issue` with `max: 1`; `false` disables a type. | GSO | VERIFIED |
| Types | issues/discussions (create/update/close), `create-pull-request`, `update-pull-request`, `merge-pull-request` (experimental), review comments, `submit-pull-request-review`, `push-to-pull-request-branch`, `add-comment`, `hide-comment`, labels, assignment, projects, releases, uploads, `dispatch-workflow`, `create-check-run`, plus always-on `noop`, `report-missing-tool`, `report-missing-data`. | GSO | VERIFIED |
| Quotas | Per-type `max` (default usually 1), `max-labels` 10, `max-links` 50, `max-bot-mentions` 10, `max-patch-size` default 4096 KB (1 to 10240). Dispatches spaced at least 5 s apart. | GSO | VERIFIED |
| Validation | Body length 20 to 65000 chars; placeholder bodies like `test` rejected; labels: `allowed`/`blocked` globs, blocked wins, unknown labels rejected unless `create-if-missing`; `target` of triggering item / number / `*`; `target-repo` + `allowed-repos` (case-sensitive globs); `allowed-events` restricts review types (e.g. `COMMENT` only); `allowed-refs` for dispatch; `*` target-repo disallowed for review types and some others. | GSO | VERIFIED |
| Sanitization | Safe-output sanitization of content: HTML URLs filtered `allowed-only`; closing keywords normalized (`normalize-closing-keywords`); temporary references resolved; @mentions escaped unless user is a verified collaborator or in event context (done in the trusted job); URLs from non-allowlisted domains replaced with `(redacted)`. | GSO, GNET | VERIFIED |
| Input sanitization | Untrusted event text is processed before the agent sees it: neutralize @mentions and bot triggers, convert HTML/XML tags, keep only HTTPS URLs from trusted domains, strip control chars, cap size (0.5 MB, 65k lines). | GARCH | VERIFIED |
| Staged mode | `staged: true` (global or per type): writes skipped, preview in step summary; handlers still read targets to evaluate filters (read perms but no write perms). Flip off to go live. | GSO | VERIFIED |
| Approval | `safe-outputs.authorization` can require external approval before protected operations. Replaying safe outputs needs `admin`/`maintain` access. | GSO | VERIFIED (details of the approval mechanism not read) |
| Failure behaviour | Discussion creation falls back to an issue by default; `report-failure-as-issue` default true; `require-agent-success` (default false) can block publication when the agent step failed. | GSO | VERIFIED |
| Secrets in outputs | Files under `/tmp/gh-aw` are scanned and secret values redacted before artifacts upload, even if earlier steps fail. | GARCH | VERIFIED |
| Agent-side tool | How the agent actually emits requests (an MCP "safe outputs" server writing NDJSON to a file) | not seen in the pages read | UNVERIFIED |

### 2b. Threat detection (GTD)

- **What it checks (VERIFIED):** three booleans: `prompt_injection`, `secret_leak`, `malicious_patch`, plus a `reasons`
  array. Inputs: the agent output items, the git patch (`aw.patch`), and the workflow prompt/context.
- **How it runs (VERIFIED):** a separate job after the agent and before the apply jobs; default on whenever safe
  outputs exist. Uses an AI engine (default: same as main; can be switched or disabled), has its own credit budget
  (default 400) and 10 min timeout, does not inherit the workflow's `runs-on`. Custom `steps` before and `post-steps`
  after AI analysis can run Semgrep, TruffleHog, LlamaGuard etc.
- **Failure classes (VERIFIED):** `threat_detected`, `agent_failure` (detector did not run; assessment incomplete, not
  "clean"), `parse_error`. Detector binaries must match pinned SHA-256 digests, else analysis is skipped and reported
  as `agent_failure`.
- **Outcome modes (VERIFIED):**
  - Warn (default): reviewable outputs (issue, comment, PR) proceed with a warning block and marker; branch pushes
    become *review PRs*; non-reviewable outputs (merges, closing issues) are **not applied**.
  - Strict: detection job fails, nothing applied.
- **Limitations stated by gh-aw (VERIFIED):** AI-based, false positives and misses; warnings are not a merge gate
  (rely on branch protection); provenance labels are metadata only; artifacts are not sanitized by console masking.
- **Static layer (VERIFIED existence, content UNVERIFIED):** "protected files" is a separate rule-based layer, not
  part of the AI check.

### 2c. Network and credential isolation (GNET, GSB, GARCH)

- Default deny. `network: defaults` is a minimal baseline; `network: {}` blocks everything. Ecosystem shorthands
  (`python`, `node`, `go`...) expand to registry domain bundles; subdomains match; one leading wildcard; a `blocked`
  list overrides `allowed`. Typos in single-word entries fail at compile time. (VERIFIED)
- Agent runs in a container on an **internal Docker network**; HTTP/HTTPS goes through a Squid proxy enforcing the
  domain allowlist (AWF). Optional SSL bump gives URL-path filtering but is non-strict only. Network activity is
  logged; `gh aw audit` diagnoses violations. Disabling the firewall is feature-flagged and rejected in strict mode.
  (VERIFIED)
- Docker socket hidden from the agent; system paths read-only, user paths read-write. The default runtime runs the
  agent as the runner user sharing the runner `/tmp`, so a sandbox escape could reach runner tokens; gh-aw recommends a
  microVM profile (`nvx`, fail-closed, no fallback) for workflows untrusted actors can trigger. (VERIFIED)
- **Credential isolation:** model traffic goes via an AWF **API proxy sidecar** that can hold endpoint credentials so
  the agent does not get provider tokens; token exchange (e.g. OpenAI workload identity) happens on the proxy egress
  path. The page states the proxy does **not** authenticate callers running arbitrary code in the agent container.
  (VERIFIED) Temp-dir env vars are excluded per sandbox; env forwarded with compiler-managed exclusions. (VERIFIED)
- The MCP gateway runs MCP servers in isolated containers; its agent ID "is not a strong security boundary", so
  isolation relies on the substrate and permission separation. Experimental. (VERIFIED)
- Budget steering: notices to the agent as credit budget or deadline nears, never changing model. (VERIFIED)

### 2d. Mapping to Mint actions (INFERRED)

Mint is different in one important way: it owns the runner, the proxy, and the token minting, so the "separate trusted
step" is a Go code path in the daemon rather than a second Actions job. The agent never holds a write token for
anything but its own branch (design 7.3), so safe outputs mainly constrain *what the agent can ask Mint to do on
external surfaces*.

| Mint action | Pattern to apply |
|---|---|
| `github.open-pr` | Agent supplies title/body only (plus optionally a label request); Mint pushes the branch with the agent token, then opens the PR with the orchestrator token. Validate: base is the project's configured base; head matches `mint/<key>`; title/body length bounds; labels against `allowed`/`blocked` globs; `max: 1` per Run; patch size cap (`max-patch-size`) and a protected-paths check (workflow files, `.mint/`, CI config) before push. Sanitize body (mentions, closing keywords, non-allowlisted URLs). |
| `comment` (issue/PR) | Agent requests a comment as structured data; Mint validates target is the Run's own thread (`target: triggering` only), length, `max` per stage, mention neutralization, URL allowlist redaction, and posts with the bot identity plus a Mint marker. |
| `github.merge` | **Never an agent request.** Non-reviewable, high-impact: gh-aw itself refuses to apply merges/closes under a threat warning and marks `merge-pull-request` experimental. Keep it a deterministic flow action gated on operator approval + head SHA + green checks (already design 5.4). |
| Outbound replies to customers | Strongest fit. Agent produces a *draft* (structured: recipients fixed by Mint from the thread, never chosen by the agent; subject/body only; attachments disallowed). Mint sanitizes, runs the detection pass, then sends only after operator approval (design 9, 12). Staged mode = dry-run preview for the first N replies per project. |
| Labels/assignments | `allowed`/`blocked` globs; unknown labels rejected. |

Threat detection mapping (INFERRED): a Mint "output check" stage between agent end and apply, in two layers. (1) Static,
deterministic, always on: secret scan of patch and requested outputs against the in-memory vault values and regexes;
patch-size and protected-path rules; URL/mention sanitization. (2) Optional AI judge run as a *separate, read-only,
no-credential agent profile* (no tools, no network except model host) producing `{prompt_injection, secret_leak,
malicious_patch, reasons}`. Follow gh-aw's outcome policy: on detection, reviewable outputs get a warning marker and
pushes become draft/review PRs; non-reviewable (merge, send customer email) are dropped; detector failure is "unknown",
never "clean". Do not treat the AI judge as a gate (gh-aw says so itself); branch protection and the human gate remain
the gate.

---

## Q3. Itervox (vnovick/symphony-go), source I (README only)

- **Scope (VERIFIED):** Go daemon polling Linear or GitHub Issues (or Markdown files), per-issue agents (Claude Code,
  Codex supported; OpenCode and Cursor named but not listed as supported), live dashboard, `itervox watch` read-only
  panel (full TUI removed in v0.3).
- **Automation event types, 14 (VERIFIED):** `cron`; `input_required`; `tracker_comment_added` (with `body_contains` /
  `body_regex`), `issue_entered_state`, `issue_moved_to_backlog`; `run_failed`, `rate_limited`; `pr_opened`,
  `pr_merged`; `pr_check_completed`, `pr_synchronized`, `pr_review_submitted`, `pr_mergeable_changed`;
  `blockers_resolved`. Automations reuse normal profiles, filters, and permissions, no second workflow engine.
- **Review profiles (VERIFIED):** `reviewer_profile` + `auto_review` dispatches a second worker to review the PR.
  Built-in `merge-bot` runs `gh pr view`, `gh pr checks --required`, `gh pr merge` guarded by required checks and block
  labels; agents otherwise never merge. Profiles are directories `SOUL.md` (identity) + `INSTRUCTIONS.md` (rules,
  Liquid). Rate-limited profile can fall back to another profile.
- **Remote workers (VERIFIED):** `agent.ssh_hosts`, `round-robin` or `least-loaded` dispatch, failover to next host,
  `known_hosts` checking; workspace via NFS/rsync/per-host clone; Docker-on-remote via `before_run` hook. Login must be
  done per machine; no credential management.
- **Config (VERIFIED):** `WORKFLOW.md` front matter + Liquid, schema versioned (`itervox_schema_version: 2`, inline
  prompt rejected, migration command writes a `.bak`). `$ENV` secret refs. Hooks like Symphony, but `before_run` is
  per attempt not per turn, `after_run` per turn. Live reload by polling a content hash every second.
- **Design lessons (VERIFIED from README claims):** single-goroutine state machine with a buffered result channel (no
  locks on the hot path); new subprocess per turn with `--resume`; `EvalSymlinks` before each launch; deferred
  workspace cleanup to terminal states so the reviewer reuses the implementer workspace; automation helpers get
  short-lived per-run action grants with `allowed_actions`, not the dashboard token; explicit `<!-- itervox:needs-input
  -->` marker preferred over a heuristic (English-only) fallback; "AI may change what CI checks, never how".
- **Pitfalls (VERIFIED):** runs Claude with `--dangerously-skip-permissions` (high-trust local only; hooks have full
  shell); unauthenticated dashboard behind a tunnel is unsafe and DNS-rebinding allowlist (`allowed_hosts`) must match;
  heuristic input detection; Windows gaps (no tmux, hooks need Git Bash, SSH workers and terminal untested in CI);
  only two tracker types; schema migrations break users; no credential scoping. Stated maturity of the code itself
  (tests, bugs): UNVERIFIED, source not read.

---

## Ideas to adopt

Each lists the design.md section changed and proposed wording.

1. **Stall detection.** Section 5 (Budgets) and 13.
   > "Each agent stage has a `stall_timeout` (default 5 min, 0 disables) measured from the last agent event or output
   > byte (or stage start). On expiry Mint kills the process tree, records outcome `stalled`, and treats it as a
   > retryable failure. Wall-clock and spend budgets apply in addition."

2. **Retry/backoff formula and retry timers.** Section 13.
   > "Failed attempts retry after `min(10s * 2^(attempt-1), 5m)`. A pending retry for a Run is replaced, not
   > duplicated. When a retry fires Mint re-reads the work item; if it is closed, unlabelled or merged, the claim is
   > released instead. Rate-limit and 5xx responses honour `Retry-After` before the formula."

3. **Concurrency rules and ordering.** New 5.6, referenced from 7.2.
   > "`limits.max_concurrent_runs` (global), plus per-project, per-runtime and per-repo caps; `windows-sandbox` is
   > capped at 1. Dispatch order: priority label, oldest event, then key. A Run is dispatched only if not already
   > running or retry-queued in this daemon; the `mint/<key>` branch claim guards other daemons."

4. **Reconcile running Runs against GitHub every tick (Symphony Part B).** Section 6 (Recovery) and 5 (`mint:stop`).
   > "Each tick, before dispatch, Mint refreshes the state of every Run it is executing. Closed or merged work item:
   > stop the stage, clean the workspace. Label `mint` removed, `mint:stop` added, or approval invalidated: stop the
   > stage, keep the workspace. Refresh failure leaves running stages untouched and retries next tick."

5. **Hook semantics.** Section 5 (Hooks).
   > "`before_stage` failure fails the attempt; `after_stage`, `on_fail`, `on_done` and `before_remove` failures are
   > logged and ignored; `after_stage` runs on success, failure, stall, timeout and cancel. Every hook has a timeout
   > (default 60 s) and truncated, secret-redacted logs. Hooks are operator configuration and are never read from the
   > repo."

6. **Startup cleanup and path containment.** Section 6 (Recovery), 7.1, 12.
   > "On start Mint removes workspace volumes, agent homes and containers (label `mint.run`) of Runs whose PR is
   > closed or merged. Workspace names are sanitized to `[A-Za-z0-9._-]`; the workspace path must resolve (after
   > symlink evaluation) under the workspace root before every launch."

7. **Preflight per tick, with last-known-good config.** Section 10.
   > "A failed reload keeps the previous config and emits an operator-visible error. Before each tick Mint
   > re-validates credentials and config; failure skips dispatch but never reconciliation or stop handling."

8. **`needs_input` must terminate.** Section 5 (Outcomes), 5.1.
   > "`needs_input` posts a structured marker comment and parks the Run (`wait`) with a timeout (default 7 days);
   > on expiry the Run fails with a notice. Agent approval prompts are never forwarded to a human; they are denied."

9. **Output-check stage ("safe outputs") before every external effect.** Section 12 (replace the last paragraph), 4.2
   (`Action`), 5 (new stage kind), 9.
   > "Agents never perform external writes. An agent emits typed *output requests* (`comment`, `open-pr`,
   > `reply-draft`, `label`) to a file in the run directory. Mint, in the daemon, validates each against the project's
   > output policy (type enabled, `max` per Run, target limited to the Run's own thread/branch/base, label
   > allow/block globs, length bounds, patch size and protected-path rules), sanitizes it (mentions, closing keywords,
   > URLs outside the allowlist, control characters), runs the output checks, and only then applies it with the
   > orchestrator token. Merge and customer email are never agent-requestable: merge is a flow action; email is an
   > operator-approved draft with recipients fixed by Mint."

10. **Two-layer output check with gh-aw's outcome policy.** Section 12 and 13.
    > "Layer 1 (always): secret scan of patch and outputs against vault values and patterns, protected-path and size
    > rules. Layer 2 (optional): a no-tools, no-credential judge profile returning `prompt_injection`, `secret_leak`,
    > `malicious_patch`, `reasons`. On a hit: comments/PRs get a warning marker and a push becomes a draft PR;
    > merges and outbound messages are dropped. A judge that errors counts as *unknown*, not clean. The judge is
    > advisory; branch protection and the human gate remain the control."

11. **Staged (dry-run) mode.** Section 5 or 9.
    > "`staged: true` per project/flow renders every output request as a preview comment/log entry without applying it;
    > used for the first runs of a new flow or sender tier."

12. **Input sanitization before prompting.** Section 2 principle 3 and 9.
    > "Before quoting untrusted text into a prompt Mint neutralizes @mentions and bot triggers, strips control
    > characters, converts raw HTML, and caps size (default 0.5 MB / 65k lines)."

13. **Per-sandbox temp dirs and env exclusions; hidden Docker socket.** Section 7.1.
    > "Containers never mount the Docker socket or host `/tmp`; temp variables are set per run; host environment is
    > forwarded only through an explicit allowlist."

14. **Network allowlist ergonomics.** Section 4.1 `NetPolicy`, 12.
    > "Allowlists support named bundles (`github`, `go`, `node`, `python`, `copilot`) expanded by Mint, subdomain
    > matching, a `blocked` list that overrides allowed, unknown bundle names are config errors, and a per-run egress
    > log (host, allowed/denied) is kept with the transcript."

15. **Token accounting by absolute totals.** Section 5 (Budgets).
    > "Spend is computed from the agent's absolute cumulative usage, never by summing per-event deltas."

16. **Reviewer as separate profile reusing the workspace; per-run action grants for automations.** Section 5.1 and
    7.3.
    > "An `reviewer` profile may start on the implementer's retained workspace read-only. Helper automations receive
    > short-lived, per-run grants naming the actions they may call (`allowed_actions`), never the orchestrator token."

17. **Event vocabulary to consider for triggers** (Itervox): `pr_check_completed`, `pr_synchronized`,
    `pr_mergeable_changed`, `run_failed`, `rate_limited`, `blockers_resolved`. Section 9 triggers: add
    `ci_completed` (for the `wait: checks_green` stage, instead of polling) and `run_failed` (to drive notification
    via the same flow engine rather than hard-coded failure reporting).

18. **Profile fallback on rate limit.** Section 5.1.
    > "A profile may name `fallback:` used when the provider returns a rate-limit error after retries."

## Ideas to reject and why

- **Actions-hosted jobs for apply/detection** (gh-aw's job graph): contradicts the decision; the Go daemon is the
  trusted step.
- **AI threat detection as a gate**: gh-aw itself documents false positives/negatives and says warnings are not a
  merge gate. Keep it advisory.
- **`merge-pull-request` as an agent-requestable safe output** and Itervox's `merge-bot` as an agent profile: merge is
  non-reviewable and high-impact; keep deterministic, SHA-bound, operator-approved (design 5.4).
- **Agents write to the tracker** (Symphony 11.5, "Human Review" handoff counts as success): Mint's design makes
  Mint own state (HMAC state comment). Agents may only *request* writes.
- **`--dangerously-skip-permissions` / auto-approve everywhere** (Itervox, Symphony example profile): acceptable only
  inside the throwaway container; never on the host.
- **Hooks sourced from the repo `WORKFLOW.md`** (Symphony/Itervox put hooks in the repo file): hooks are arbitrary
  host shell; in Mint they are operator-owned (principle 1).
- **Heuristic needs-input detection** (English-oriented fallback in Itervox): use an explicit marker or ACP stop
  reason only.
- **SSH/remote worker fleets**: already a non-goal; revisit via the `Runtime` interface. If ever added, note Itervox's
  need for per-host logins and unmanaged credentials, which breaks our credential model.
- **Dashboard/web UI with API token**: non-goal; if a status HTTP endpoint is ever added, copy Symphony's
  loopback-by-default and Itervox's DNS-rebinding host check.
- **Squid + SSL bump for URL-path filtering**: gh-aw restricts this to non-strict; Mint's own TLS-terminating proxy
  already sees paths, so no extra component.
- **gh-aw's "agent ID" gateway auth and shared-runner-user container**: gh-aw states these are weak boundaries; Mint
  already uses non-root, cap-dropped containers and notes microVM as a path.
- **Polling `WORKFLOW.md` hash every second**: unnecessary; use a file watcher with a defensive re-check on each
  tick.
- **Symphony's tracker-owned workflow file as the sole config**: conflicts with the two-authority principle.

## Gaps found in our design

1. No stall detection (idle-agent kill); only wall clock and spend.
2. Retry/backoff unspecified (no formula, cap, timer replacement, `Retry-After`, no-slot handling).
3. No global/per-project/per-repo concurrency or dispatch ordering; only "Windows concurrency 1".
4. No tick-time reconciliation of *running* stages against GitHub (closed, unlabelled, merged, approval stale), and no
   distinction between stop-and-clean and stop-and-keep.
5. Hook failure/timeout semantics and run-on-cancel behaviour undefined.
6. No cleanup of workspace volumes for terminal Runs at startup; only agent homes and containers are mentioned.
7. No workspace path-containment/sanitization invariant or symlink check.
8. `needs_input` has no timeout, marker format, or terminal state; agent permission prompts in headless mode not
   addressed.
9. Safe outputs are a single sentence in section 12 ("where practical"); no output schema, per-type limits, target
   restriction, sanitization rules, staged mode, or secret scan of outbound content. The agent token can write contents
   and PRs directly (7.3), so the agent can perform `open-pr`-like effects without a validation point; the design
   should state which writes remain direct (branch push) and what validates the diff (size, protected paths such as
   `.github/workflows` and `.mint/`).
10. No output check between agent end and apply (secret scan, protected paths, injection judge) and no policy for a
    failed or unavailable checker.
11. No input sanitization spec (mentions, bot triggers, HTML, size caps) even though untrusted quoting is a principle.
12. No per-tick preflight that skips dispatch while still reconciling; reload failure behaviour is "keep previous" only
    for config, not for repo prompts changing under a running Run.
13. Budget accounting method is open (delta vs absolute totals).
14. No event for CI completion or for run failure as first-class triggers; `wait: checks_green` implies polling.
15. Reviewer/second-agent pattern (reuse of implementer workspace, read-only) not designed.
16. Egress log per run and a way to diagnose denied hosts (gh-aw `audit`) is absent; needed for tuning allowlists.
17. A hung or unreachable detector/judge, or a failing sink, must not take down the daemon or be read as success
    (gh-aw's `agent_failure` semantics; Symphony 13.2).
18. Second pass needed once `github.github.com` is allowed: gh-aw "protected files" rules, safe-outputs agent-side
    emission mechanism, `authorization` details, and the AWF firewall repo are UNVERIFIED.
