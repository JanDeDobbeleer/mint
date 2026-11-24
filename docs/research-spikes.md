# Research spikes

Each spike answers a question that changes the design. Time-box them; write findings to
`docs/spikes/S<n>-<name>.md` with: question, method, result, decision, and any code (throwaway, under
`spikes/` or a scratch branch, never merged into `internal/`). A spike ends with a **decision** that is folded back
into [design.md](design.md). Unverified claims in the design are marked as such and map to a spike here.

Order: S0 first (it can shrink the project), then S1 to S4 in parallel, then the rest as their milestones approach.

---

## S0. Does gh-aw already cover the GitHub-only flows? (go/no-go, ~1 day)

**Why**: GitHub Agentic Workflows (Markdown workflows compiled to Actions, Copilot CLI default engine, containerized
agent behind a firewall, safe outputs) may deliver issue-to-PR, review loops, and scheduled jobs with a better-reviewed
safety model than we would build. This is technical preview software.

**Questions**
- Triggers: issues, labels, PR review events, comments, `schedule`. Can a review event resume the same agent session?
- Multi-stage: analysis → implement → simplify → programmatic tests → PR. Can tests run as deterministic steps?
- Merge: can it merge on approval? On a nightly integration branch (`next`) with green CI?
- Identity and tokens: how are write permissions scoped? Do pushes by its token trigger CI?
- Execution: hosted vs self-hosted runners; Windows runners; can a Windows-specific stage run on a Windows runner?
- Sources beyond GitHub: email/WhatsApp (probably no).

**Method**: read https://github.github.com/gh-aw/ (the session that did the initial research could not reach this
host; allow it or paste the pages), install the extension in a scratch repo, implement the issue-to-PR flow and the
nightly job.

**Decision**: (a) adopt gh-aw for GitHub-only flows and use the Mint daemon only for Windows-native sessions and
non-GitHub sources, (b) build the daemon as designed and borrow ideas, or (c) hybrid. Update design section 1 and the plan.

---

## S1. Copilot CLI headless behaviour

**Questions** (verify against the installed version and docs.github.com Copilot CLI reference)
- Exact flags for non-interactive runs (`-p`, `--allow-all-tools` or finer `--allow-tool`/`--deny-tool`), output
  formats, exit codes for success/failure/needs-input.
- Is there a machine-readable event stream? How do we detect "done" reliably (exit, sentinel file, JSON output)?
- `--resume <id>` and `--continue` with `-p`: do they work? Where are sessions stored (`COPILOT_HOME`)? Can the session
  ID be discovered after a run (output, directory listing)? What happens when the home is missing?
- `COPILOT_HOME` per run: what must exist for auth, sandbox policy (`copilot config --global ...` equivalents), and
  models? Can policy be written by Mint directly into the per-run home?
- Does `--sandbox` work together with `-p`? On Windows headless?
- Authentication with `COPILOT_GITHUB_TOKEN` only: which token types work, can it be a placeholder behind a proxy?
- Premium request accounting: is usage reported per run? Anything usable for budgets?
- Terms/licensing for unattended use of a seat.

**Exit criteria**: a script that runs a prompt headlessly in a temp workspace, detects completion, captures the
session ID, and resumes it for a second turn; written findings on each question.

**Feeds**: `Agent` interface (`Command`, `ParseResult`, `Capabilities`), section 5.2 and 6.1 of the design.

---

## S2. Copilot ACP server

**Questions**: is `copilot --acp` stable enough (public preview) to drive: new session, prompt, stop reason,
`session/load` for resume, permission requests, tool events, usage? Settings are fixed at server start, so how do
per-run policy and credentials work? Does it run inside the sandbox and the container?

**Exit criteria**: a Go prototype speaking ACP over stdio that runs one turn and one resumed turn, with notes on
event types.

**Decision**: headless `-p` for v1 and ACP in a later milestone, or ACP from the start.

---

## S3. Docker from Go on Windows, and forcing egress through the proxy

**Questions**
- `docker` CLI via `os/exec` versus the Docker SDK for Go. Windows named pipe handling, Docker Desktop and Podman Desktop
  compatibility, WSL2 backend path translation for bind mounts.
- Network design: an `--internal` network plus a Mint-owned sidecar proxy container (which has a second interface to the
  outside) versus the proxy on the host reached via `host.docker.internal`. Which one can actually prevent a process in
  the container from bypassing the proxy on Docker Desktop?
- Volume versus bind mount performance for workspaces (Windows file sharing is slow). Clone into a named volume?
- Hardening flags honoured on Docker Desktop: `--cap-drop=ALL`, `no-new-privileges`, read-only root, pids limit,
  seccomp default.
- Cleanup: kill on cancel, orphan detection by label (`mint.run=<key>`).
- Image build: `docker build` with no secrets, `--pull`, digest pinning, build cache control, time and size.

**Exit criteria**: Go prototype that builds an image, runs a container with the hardening flags on an internal network,
proves a direct `curl` to the internet fails while proxied access works, and cleans up on cancel.

---

## S4. Injecting TLS proxy

**Questions**
- Go implementation of a forward proxy with per-session CA (stdlib `crypto/tls` + `httputil`, `CONNECT` handling,
  HTTP/2, websockets/SSE if the agent uses them, streaming bodies).
- Do `git` (`http.sslCAInfo` / `GIT_SSL_CAINFO`), `gh` (`SSL_CERT_FILE`), Node (`NODE_EXTRA_CA_CERTS`), Go, and Copilot CLI
  trust the injected CA through env? Does `gh` or Copilot CLI accept a placeholder token?
- Header injection rules: only on allowed hosts and methods; strip any agent-supplied `Authorization`; log without
  secrets.
- Does the Copilot model endpoint need the proxy, and can its credential also be injected?
- Alternatives: git credential helper over a socket for git, gh wrapper.

**Exit criteria**: proxy prototype with an allowlist and header injection; demo that `git push` and `gh pr create` work
from a container holding only a dummy token; a negative test showing exfil to a non-allowed host fails.

---

## S5. GitHub App identity and token minting

**Questions**
- Creating a personal GitHub App; permissions per flow (contents, pull requests, issues, checks, workflows, actions).
- JWT signing in Go (stdlib), installation token minting with `repositories` and `permissions` narrowing, revocation,
  rate limits.
- Can the app approve/merge? PR author = bot means the operator can approve; confirm branch protection and required
  reviews behave. Can the app request the operator's review?
- Rulesets: restrict the app to pushing `mint/*` and `next`, deny default-branch pushes; bypass lists.
- Do pushes with an App token trigger Actions workflows (they do for App tokens, unlike `GITHUB_TOKEN`)? Confirm for the
  repos involved, including oh-my-posh.
- Webhooks versus polling for events; ETag-based polling cost.

**Exit criteria**: Go prototype that mints a one-repo token, pushes a branch, opens a draft PR, requests review, and
revokes the token.

---

## S6. PR state comment, HMAC, and reconciliation cost

**Questions**
- Comment format (hidden HTML marker + signed JSON), size limits, edit races, how to fetch efficiently (list PRs by
  label/author, ETags, search API limits).
- HMAC key derivation from the vault (HKDF) and rotation.
- Claim races between two daemons (branch creation as the lock): verify behaviour of create-ref conflicts.
- Behaviour when a human edits or deletes the state comment, force-pushes the branch, or closes the PR.
- Reconciliation cadence versus API quota for N repositories.

**Exit criteria**: prototype codec with property tests (tamper detection) and a measured API cost per reconcile tick.

---

## S7. Windows host sandbox runtime, headless

**Questions**
- Does the Copilot sandbox work unattended on Windows (no TTY), with per-run `COPILOT_HOME`, and with the current
  `checkSandboxCredentials` expectations (`allowLocalNetwork`, injectHosts)?
- Concurrency limits, cleanup of process trees (Job Objects), path-length issues in workspaces.
- Which `.mint` check commands are safe to run on the host, and the operator-approval model for them.
- How to express the generic `Policy` in `sandbox.userPolicy`.

**Exit criteria**: running the existing session launch headlessly from a Go test harness on a Windows machine, with
evidence of the sandbox denying an out-of-policy read and write.

---

## S8. Nightly integration semantics (`next`)

**Questions**
- Sequential merge of approved PRs onto a fresh branch from `main`: conflict handling, ordering, partial failure.
- Merge commit versus squash: confirm GitHub marks the source PRs merged when `next` merges with a merge commit.
- Required checks on `next` PR; waiting for checks (check suites API vs status API vs `gh pr checks`), timeouts.
- Release trigger: does merging to `main` start the existing release workflow? Is `workflow_dispatch` needed?
- Idempotency when a run is interrupted after force-pushing `next`.

**Exit criteria**: scripted dry run against a scratch repo with three approved PRs, one conflicting.

---

## S9. Email and WhatsApp feasibility (can run later)

**Questions**
- Email: IMAP IDLE vs Gmail/Graph APIs; reading `Authentication-Results` (SPF/DKIM/DMARC) reliably; threading by
  `Message-ID`/`References`; replying as a draft for approval; mailbox labelling as handled-state.
- WhatsApp: Cloud API webhooks need a public endpoint (tunnel or relay); signature verification
  (`X-Hub-Signature-256`); business account constraints; session windows for replies.
- Sender trust mapping and anti-spoofing; attachment handling (never auto-open).
- Credentials for both in the vault.

**Exit criteria**: a written source-adapter design for each, validating that the `Source` interface needs no changes.

---

## S10. Claude adapter check (low priority)

**Questions**: Claude Code headless flags, resume, sandbox (bubblewrap/Seatbelt; no native Windows), settings that repo
files can inject (hooks, MCP, `apiKeyHelper`) and how to neutralize them, and credential injection through the Mint
proxy (placeholder `ANTHROPIC_API_KEY`).

**Exit criteria**: a one-page confirmation that `Agent` can host it unchanged, listing differences.

---

## Cross-spike deliverables

- A short ADR per decision in `docs/adr/NNNN-title.md`.
- An updated "Open questions" section in the design.
- A list of commands and versions used so results are reproducible (Copilot CLI version, Docker Desktop version, OS).
