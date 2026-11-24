# Implementation plan

Read [design.md](design.md) first (v2, which incorporates the documentation research in [spikes/](spikes/)), and
[research-spikes.md](research-spikes.md) for the status of each spike and the hands-on tests still required. This plan
is written so a fresh session can execute it milestone by milestone. **The hands-on tests in M0 can still change later
milestones; update this file when a decision lands.** Decision D-1 (design 5.5) is closed: Mint's GitHub App is the bypass
actor on `main` and no agent ever holds a GitHub token (Mint always pushes).

## Rules for the executing session

- Work on a feature branch per milestone. Do not open a PR unless asked. Commit with Conventional Commits (CI runs
  commitlint): `feat(core): ...`, `refactor(session): ...`, `docs: ...`, `test: ...`.
- The interactive `mint` command and `mint setup` must keep working and keep their tests green at every commit. Milestone
  1 is behaviour-preserving.
- Checks before every commit (from the README):
  `modernize ./...`, `fieldalignment ./...`, `go test ./...`, `go vet ./...`, `golangci-lint run`, and
  `go build -trimpath -o mint.exe .`. CI also runs on Windows and macOS: no Unix-only code without build tags.
- Go 1.27.4. Avoid new dependencies; each one needs a stated reason in the commit message. `go.yaml.in/yaml/v3` is
  already an indirect dependency and is the expected YAML library.
- Tests use local HTTP servers (`httptest`) and helper subprocesses, never live GitHub or Copilot. Follow the existing
  style in `internal/mint/*_test.go`. Every extension point (Source, Runtime, Agent, Action) gets a reusable
  conformance test.
- Never log, print, or put secrets in errors. Add a test whenever a new type holds a secret.
- Keep repo-owned inputs (`.mint/*`) read from the default branch only (design principle 2).
- Prefer small interfaces defined where they are consumed. No Copilot-specific names outside `internal/agent/copilot`
  and `internal/runtime/hostsandbox` after Milestone 1.

## Where today's code lives (for refactors)

| File | Responsibility | Destination |
|---|---|---|
| `internal/mint/cli.go` | `Run`, `setup`, launch orchestration | stays as CLI wiring; calls new packages |
| `internal/mint/session.go` | `runWithToken`, `childEnvironment`, `checkSandboxCredentials`, `projectConfiguration`, `checkCopilot`, `checkLaunchArgs` | split: env filtering and project-config checks to `internal/runtime/hostsandbox`; Copilot launch/version/policy to `internal/agent/copilot` |
| `internal/mint/sandbox_config.go` | `applySandboxSettings` | `internal/agent/copilot` (policy compiler) |
| `internal/mint/token_store.go`, `master_password.go` | PBKDF2 + AES-GCM store, master password | `internal/vault` (generalized to named credentials) |
| `internal/mint/github.go` | token validation, HTTP client | `internal/source/github` + a shared client |
| `internal/mint/repository.go` | remote parsing, `protectPaths` | `internal/core` + runtime |
| `internal/mint/git-instructions.md` | Copilot session instructions | stays with the Copilot agent package |
| `internal/mint/ui*.go`, `prompt.go` | terminal UI and hidden prompts | unchanged |

## Target package layout

```
main.go
internal/mint/            CLI wiring (setup, interactive launch, run, serve, image, creds, status)
internal/core/            Event, Identity, ThreadRef, Policy, Secret, Outcome, errors
internal/config/          mint.yaml loader, validation, reload
internal/vault/           encrypted named credentials, unlock sources
internal/identity/        GitHub App JWT + installation tokens by role (orchestrator, push, merge); PAT provider for `mint run`
internal/proxy/           egress-allowlisting forward proxy (optional agent-credential injection)
internal/flow/            engine, stages, hooks, budgets, state-comment codec (HMAC)
internal/action/          checks, git.push, github.* actions
internal/router/          verification, dedupe, sender->project, triggers
internal/source/          Source interface + github/ (email/, whatsapp/ later), cron/
internal/runtime/         Runtime interface + docker/, hostsandbox/
internal/agent/           Agent interface + copilot/ (claude/ later)
internal/imagebuild/      .mint/Dockerfile build, hash, update, prune
```

---

## M0. Spikes (blocking decisions)

The documentation research is **done** (files in `docs/spikes/`). What remains in M0 is hands-on verification that needs
real tools, listed per spike in the spike files and summarized in [research-spikes.md](research-spikes.md):

- Copilot CLI on the operator's machine: tests T1 to T11 in `S1-S2-copilot-cli.md` (headless flags, `--session-id`
  and resume, missing-session behaviour, sentinel contract, `COPILOT_HOME` layout, sandbox keys, token types).
- Docker Desktop on Windows: the checklist in `S3-S4-docker-and-proxy.md` (`--internal` network behaviour, hardening
  flags, sidecar topology, proxy against real GitHub and Copilot traffic).
- A scratch repo with the GitHub App installed: tests in `S5-S6-S8-github-platform.md` (token narrowing, ETag stability,
  merge and ruleset behaviour, claim race, `next` dry run).
- S7 (Windows host sandbox, headless).
- A read of the Copilot unattended-use terms (and, for confirmation, test T6/T13 on ruleset bypass behaviour).

**Done when**: each hands-on result is recorded in its spike file with a decision, and `design.md` (sections 5.2, 6.1,
7, 15) and this plan are updated. M1 and M2 do not depend on these results and may start first. Mint always runs its
own runners; gh-aw and Actions-hosted execution are out of scope.

---

## M1. Extract interfaces without behaviour change

**Goal**: introduce `core`, `agent`, `runtime` and move Copilot and Windows-sandbox specifics behind them.

**Tasks**
1. Create `internal/core` with `Policy`, `Identity`, `ThreadRef`, `Event`, `Outcome`, and `Secret`
   (redacting `String`, `GoString`, `Format`, `MarshalJSON`, `MarshalText`, zeroize on `Destroy`).
2. Define `agent.Agent` (with `AgentCaps`, `AgentRequest.SessionID`, `RepoConfigPolicy`) and `runtime.Runtime` (design
   4.2). Implement `agent/copilot` (version check with a recorded tested version, currently 1.0.114; args builder with
   `--sandbox`, `--disable-builtin-mcps`, `--secret-env-vars`, deny-tool flags, instruction injection, policy compile,
   `CredentialEnv`; the builder must also support `-p`, `-s`, `--no-ask-user`, `--no-auto-update`, `--session-id`,
   `--resume=<id>`, `--output-format json`, `--max-ai-credits`, `--allow-tool/--deny-tool/--available-tools/
   --excluded-tools`, `--sync-session=off`, `--model`, and must refuse bare `--resume`/`--continue`) and `runtime/hostsandbox` (env allowlist, isolated `GH_CONFIG_DIR`, project settings checks, MCP
   disablement, process launch with TTY passthrough).
3. Rewire `Run` in `cli.go` to call the interactive path through these packages.
4. Move tests with the code; keep all existing assertions.

**Acceptance**
- `go test ./...` passes with no deleted or weakened assertions; byte-for-byte same child environment and args in the
  existing session tests.
- `grep -ri copilot internal/ --include=*.go -l` lists only `internal/agent/copilot`, `internal/runtime/hostsandbox`,
  tests, and CLI wiring.
- New `Secret` tests: never appears via `fmt` verbs (`%v %+v %#v %s %q`), `json`, `slog`, or error wrapping.

---

## M2. Vault and credentials

**Goal**: named encrypted credentials unlocked once; no secret values in flags.

**Tasks**
1. Generalize `token_store.go` into `internal/vault`: store key in `master.enc`, per-credential files with associated
   data bound to credential name (and repo for repo tokens). Keep the file format backward compatible with existing
   `MINTPAT1` tokens (migration test).
2. Unlock sources: hidden prompt (default), `--password-stdin`. Reject any flag that would carry a secret value.
3. `mint creds add|list|rm <name>` with hidden prompts. `list` shows names and metadata only.
4. Derive an HMAC key from the vault with HKDF for state signing (used in M4).
5. Add a **runtime write path**: atomic re-encryption of one credential file while the vault is unlocked (OAuth
   refresh tokens rotate). Define the credential names in design section 8.

**Acceptance**: existing token-store tests pass unchanged; wrong password never replaces data; `mint creds` prints no
secret; fuzz or table tests for tamper detection (wrong associated data fails).

---

## M3. Operator configuration and policy model

**Goal**: `mint.yaml` with projects, senders, triggers, flows, agent profiles, sandbox profiles; generic `Policy`
compilers.

**Tasks**
1. `internal/config`: strict YAML decode (unknown keys error), validation (references resolve, no secrets, no
   forbidden fields), version field, error messages with paths.
2. Dynamic reload with a validation gate: a bad file keeps the last good config and reports the error.
3. Policy compilers: Copilot (`sandbox.userPolicy`, `injectHosts`, `allowLocalNetwork`) in `agent/copilot`; Docker
   (mounts, tmpfs, limits, allowed hosts for the proxy) stubbed with tests in `runtime/docker`.
4. Reject, at load time, any secret-looking value (high-entropy token patterns, `ghp_`, `github_pat_`, PEM blocks).
5. Config for the new operational rules: `limits` (global, per-project, per-runtime, per-repo), `stall_timeout`, retry
   policy, hooks with timeouts, output policy (`max`, targets, label globs, protected paths), `staged`,
   network bundles (`github`, `go`, `node`, `python`, `copilot`) with `blocked` overriding allowed, profile `fallback`.
6. Copilot policy compiler golden tests cover only the documented keys (`sandbox.enabled`,
   `sandbox.userPolicy.network.allowedHosts/blockedHosts`, `sandbox.credentials.envVars.<NAME>.injectHosts`);
   `allowLocalNetwork` and project-configuration keys are version-gated.

**Acceptance**: golden tests for config decode and compile; reload tests; secret-pattern rejection tests.

---

## M4. Flow engine, PR state, and `mint run`

**Goal**: execute `issue-to-pr` once against a real or fake GitHub using the host sandbox runtime and a PAT. First
usable end-to-end milestone.

**Tasks**
1. `internal/flow`: stage graph, outcomes, budgets, hooks, retries with bounded loops, cancellation, idempotent stage
   execution. Engine uses only interfaces.
2. State-comment codec: hidden marker plus signed JSON (HMAC from M2) carrying `kid`, run key, `seq`, head SHA. Verify
   author by numeric `user.id` (and `performed_via_github_app.id` if present) and the signature on read. Hard 8 KB state
   budget; large analysis is committed as a file. Property tests for tamper, replay, deletion, and size limits.
3. GitHub client: issues, PRs (draft), comments (create/update), labels, refs (atomic branch create as claim: 201 won,
   422 lost), reviews, check runs and commit statuses. **Conditional requests are a hard requirement**: ETag cache keyed
   by (URL, token generation), persisted; handle secondary limits (403/429, `Retry-After`, serialized writes, concurrency
   well below 100); per-token `X-RateLimit-*` metrics. Extract from `github.go`; keep redirect refusal and validation
   behaviour.
4. Actions: `checks` (run commands in the runtime; feed failures back to the agent), `git.push` (**Mint pushes**: it
   validates the diff for size, protected paths and secrets, then pushes using the `--config-env` header pattern from
   `git-instructions.md`, no token in remote URLs), `github.open-pr` (state comment, trailers), `comment`.
   Add the output-request pipeline (design 5.7): typed requests validated against the output policy, sanitized, secret
   scanned, `staged` mode, optional judge stage; unknown/failed checkers count as unknown, not clean.
   Add input sanitization (design 5.8) with injection fixtures.
5. Workspace manager: shallow clone of the PR/default branch into a per-run directory, per-run agent home, cleanup.
6. Prompt rendering: issue and comment text quoted as untrusted data in a fixed template; unit tests that injected
   text cannot close the quote block.
7. `mint run <issue>`: runs `issue-to-pr` with the Copilot agent on the host-sandbox runtime (docker comes in M5).
8. Completion contract: the `result.json` sentinel and its prompt-template requirement; `ParseResult` combines exit
   status, final response, and sentinel (JSONL only for diagnostics). Generate the session UUID, record it in the state
   comment before launch, pass `--session-id`, then `--resume=<id>`.
9. Operations (design 5.6): stall detection, retry/backoff with timer replacement, concurrency caps and dispatch order,
   hook semantics, `needs_input` parking with timeout, startup cleanup and workspace path containment.
10. Fakes for tests: fake GitHub (`httptest`, real status codes including 422 and 304), fake agent helper subprocess
    (emulating exit codes, the sentinel, stalls, and "resume with missing session" failure), fake runtime.

**Acceptance**: with fakes, a full run produces branch, commits with trailers, draft PR, signed state comment; killing
the process mid-stage and re-running resumes from the checkpoint; a tampered state comment is rejected; `checks`
failure feeds output to the agent and stops at `max`.

---

## M5. Docker runtime, session image, and proxy

**Goal**: container sessions behind an egress-allowlisting proxy, holding no GitHub credential. Depends on S3 and S4
decisions.

**Tasks**
1. `internal/imagebuild`: `.mint/Dockerfile` read from the default branch via the API or a clean checkout; content hash;
   approval record (stored in operator config dir, keyed by repo + hash); `mint image build|update|prune`; tags
   `mint/<owner>-<repo>:<hash>`; `--pull` on update; label images with run metadata.
2. `runtime/docker`: start containers with the hardening flags (design 7.1), workspace volume, per-run agent home mount,
   labels for orphan cleanup, wall-clock kill, log capture. Reject any repo-provided `runArgs`/mounts.
3. `internal/proxy`: v1 is an **allowlisting forward proxy** (opaque CONNECT for allowed hosts, 403 otherwise,
   private-address refusal, per-run request log, no secrets in logs, tests for bypass attempts). Optional, only if a
   placeholder agent credential is verified: hybrid mode with MITM and injection on that one host, using a per-session
   name-constrained CA held in memory and delivered to the sidecar over a control channel, `Authorization` overwrite and
   header-stripping tests. No GitHub credential injection in v1. Reference prototype: `docs/spikes/S3-S4-docker-and-proxy.md`.
4. `mint-base` image definition (Dockerfile in `build/`) and docs for deriving from it; agent CLI install step.
5. Docker access: a `docker` CLI wrapper behind a narrow interface (JSON output, label filters, orphan sweep). The Go
   SDK is deferred.
6. Sidecar and networks: `mint-int-<run>` (`--internal`) and `mint-egress`; the proxy runs as a Mint-owned sidecar
   container on both. The agent has no other route.
7. Egress test harness, run on Windows with Docker Desktop: direct requests fail, proxied requests to allowed hosts
   work, host gateway and DNS leaks are probed, SSRF to private addresses is refused.
8. `mint-base` owns the CA install and proxy environment (`HTTPS_PROXY`, `NO_PROXY`, `NODE_USE_ENV_PROXY=1`, CA
   variables on tmpfs); set `COPILOT_HOME` to the mounted per-run home and `XDG_CACHE_HOME` to a writable ephemeral dir.
9. Per-adapter `RepoConfigPolicy` replaces the one hard-coded `projectConfiguration` scan (design 12).

**Acceptance**: integration test (skipped without Docker) proves: placeholder token only in the container, push and PR
creation succeed via the proxy, a request to a non-allowed host fails, container cannot see host paths other than
workspace and agent home, `--cap-drop` and read-only root are in effect; an orphan sweep removes leaked containers.

---

## M6. GitHub source, router, App identity, `mint serve`

**Goal**: the daemon reacts to GitHub activity end to end.

**Tasks**
1. `internal/identity`: GitHub App JWT signing (stdlib), installation tokens narrowed per repo and permission set per role
   (orchestrator, push, merge), `DELETE /installation/token` after use, expiry margin, tolerant token format (no length
   assumptions), an expiry-aware cache; the App private key is loaded from the vault; PAT provider for `mint run`.
   Tests for narrowing (repos and permissions in the request body). Mint's own events are recognised by the App's
   identity. No agent-side GitHub credentials exist by construction (Mint always pushes). Add an enrolment flow for the
   App (private key, App id, installation id) behind the master password.
1a. Approval: use GitHub's native review. An approval counts when the reviewer is in `approvers` and the review's
   `commit_id` equals the current head SHA; changes-requested reviews and approver review comments start `pr-feedback`;
   merges pass the approved head SHA.
2. `source/github`: reconcile through repo-wide `issues/comments?since`, the open-PR list, and the label list, all
   conditional; no per-PR polling; search only for cross-repo discovery within a 30/min budget; emit `Event`s with
   `Verified` set from the authenticated API, including `ci_completed` and `run_failed`; optional webhook receiver later.
3. `internal/router`: dedupe by `Event.ID` (in-memory plus GitHub-side markers), sender→project→trust tier, trigger
   matching, loop prevention (ignore Mint's own events), operator-listed approvers only.
4. Flow `pr-feedback`; the `integration.strategy: merge` watcher (interval tick; collect approved PRs by listed
   approvers on the current head SHA; config validation that a strategy is exclusive and mandatory when enabled);
   `github.request-review`, `github.merge` (match head SHA, require checks,
   honour branch protection).
5. Reconciler: on start and each tick, list open PRs with Mint markers and stage != done, resume them; each tick also
   refreshes running Runs (design 5.6: closed/merged stops and cleans, unlabelled/`mint:stop`/stale approval stops and
   keeps the workspace).
6. `mint serve`: unlock vault once, concurrency caps, graceful shutdown (stop dispatch, checkpoint, kill, revoke
   tokens), structured logs, `mint status`.
7. Budgets and `mint:stop`.

**Acceptance**: with a fake GitHub, issue → PR → changes-requested review → fix → re-review request → approval → merge,
including a daemon restart between every step; stale approval (new push) does not merge; events by the bot itself do not
retrigger; two daemons cannot claim the same issue.

---

## M7. Cron trigger and nightly `next` integration

**Prerequisite**: the S8 dry run (T13) and a protected `main` on a scratch repo with the App as a ruleset bypass actor.

**Tasks**: cron source (timezone aware, in-process scheduler); actions `github.collect-prs` (records PR and head SHA
pairs), `git.integrate` (true merges of head SHAs, resume by recorded `next` tip), `github.open-pr` for `next`,
`wait: checks_green` queried via check runs and combined status including the required-check list (woken by
`ci_completed`), final per-PR head re-check, merge method `merge`, `github.dispatch-workflow` (optional); date-keyed
idempotency; conflict handling per S8.

**Acceptance**: dry run with a fake GitHub and real git repos: three approved PRs, one conflicting → `next` contains two,
conflicting PR gets a comment, a PR pushed after the build is left open and excluded, merge happens only when checks are
green, a second fire the same day is a no-op.

---

## M8. Windows host runtime for the daemon, `runs-on`

**Tasks**: concurrency cap 1; Job Objects for process-tree kill; per-run Copilot home written with the compiled policy;
trust gate (trusted senders only, plan approval by default); `runs-on` resolution from project config and labels with
operator allowlist; Windows CI coverage for the new code paths.

**Acceptance**: unit tests for selection logic (label alone cannot escalate); Windows CI job runs the host runtime tests
with a fake Copilot executable; documented manual test for real sandbox denial (S7).

---

## M9a. Email source (IMAP, read-only first)

**Goal**: prove the source, router, trust, and tracking-issue loop with the lowest-risk external source.

**Tasks**
1. `source/email`: IMAP IDLE plus poll backstop behind an internal wrapper (pin `go-imap/v2` to an exact beta tag);
   `Message-ID` as `Event.ID`; threading via `Message-ID`/`In-Reply-To`/`References`; provider-added mailbox label as
   handled-state through the optional `Acker`.
2. Verification: accept only `dmarc=pass` (aligned) from an `Authentication-Results` header whose `authserv-id` matches
   the configured `trusted_authserv` and that sits in the provider-added block; ignore ARC; map identity on the `From`
   address-spec only. Fixture set of raw `.eml` files with genuine and forged headers.
3. Tracking-issue creation with `mint-source-id:`; dedupe by search; per-sender rate caps; attachments are metadata only.
4. Vault credentials `email-imap-password` or the OAuth trio, with the runtime write path from M2.
5. Then approval-gated replies (drafts the operator approves; `Reply` only after approval).

**Acceptance**: conformance suite passes with a fake mailbox; forged-sender fixtures are dropped; duplicates are
deduped across restarts; hands-on IDLE and `UIDVALIDITY` check against the operator's real provider.

## M9b. WhatsApp relay (later, optional)

Public HTTPS endpoint through `cloudflared` or a buffering relay; `X-Hub-Signature-256` verification on the raw body;
dedupe on `wamid`; 24-hour window handling (`ErrWindowClosed`, approved templates); credentials in the vault. Blocked
on reading Meta's current business terms and the AI-provider policy.

## M9. Extensibility proof

**Tasks**: add an `email` source skeleton and a `claude` agent skeleton, both behind the registries, with the shared
conformance suites and no core changes (any needed core change is a design bug: fix the design). Write
`docs/extending.md`. Optionally a build tag that omits Copilot to prove removability.

**Acceptance**: `go build` and tests pass with the Copilot packages excluded by build tag; the email skeleton passes the
Source conformance tests using a fake mailbox.

---

## M10. Hardening and release

**Tasks**: threat-model tests (prompt-injection fixtures, forged approvals, tampered state, proxy bypass, devcontainer
`runArgs` rejection, Dockerfile hash change requires approval); budget enforcement tests; log redaction test over all
packages; README and `docs/` updates (new commands, config reference, security notes); release pipeline updated to
include any new assets (for example the `mint-base` image); update `SECURITY.md`.

**Acceptance**: all checks green on Windows, macOS, and Linux CI; a documented manual end-to-end walkthrough on
Windows with Docker Desktop.

---

## Dependency order

```
M0 (hands-on) ─────────────────────────────┐ (informs M4, M5, M6, M7)
M1 ─► M2 ─► M3 ─► M4 ─► M5 ─┬─► M6 ─► M7
                            └─► M8
                                 M6 ─► M9a ─► M9b ─► M9 ─► M10
```

M1 and M2 do not depend on the hands-on results and can start immediately; M2 and M3 can overlap after M1. M5 can
start in parallel with M4's later tasks once the Windows Docker tests are done. M7 needs the S8 dry run.

## Starting prompt for the implementation session

> Read `docs/design.md` (v2), `docs/research-spikes.md`, `docs/implementation-plan.md`, and the files in
> `docs/spikes/`. The documentation research is finished; do not redo it and do not evaluate or adopt GitHub Agentic
> Workflows (Mint always runs its own runners). Start with M1 (behaviour-preserving extraction of the `Agent` and
> `Runtime` interfaces) and M2 (vault and `Secret` type), which do not depend on the pending hands-on tests. Where a
> design statement is marked [U] it is unverified: add a test or a version gate instead of assuming it. Do not begin M4
> or later until the operator confirms the M0 hands-on results, and do not begin M7 until the S8 dry run (T13) has been done. Follow
> the "Rules for the executing session" section. Work on a new branch, commit with Conventional Commits, and do not
> open a pull request unless asked.
