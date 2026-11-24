# Research spikes

Each spike answers a question that changes the design. Time-box them; write findings to
`docs/spikes/S<n>-<name>.md` with: question, method, result, decision, and any code (throwaway, under
`spikes/` or a scratch branch, never merged into `internal/`). A spike ends with a **decision** that is folded back
into [design.md](design.md). Unverified claims in the design are marked as such and map to a spike here.

Order: S0 (a short design-input review), S1 to S4 in parallel, then the rest as their milestones approach.

## Status (updated after the documentation research)

All spikes except S7 now have documentation findings in `docs/spikes/`. Documentation hosts for docs.github.com,
github.github.com, docs.docker.com, developers.facebook.com and others were blocked, so findings come from search
excerpts and raw repository sources and carry VERIFIED / INFERRED / UNVERIFIED labels. What is left is hands-on.

| Spike | Findings file | Decision recorded | Remaining |
|---|---|---|---|
| S0 prior art | `spikes/S0-prior-art.md` | 18 ideas adopted (design 5.5 to 5.8, 12, 13); gh-aw rejected as a platform | second pass on gh-aw "protected files" and agent-side emission once reachable |
| S1/S2 Copilot CLI, ACP | `spikes/S1-S2-copilot-cli.md` | `-p` plus allowlist (no `--allow-all-tools` needed); Mint-chosen `--session-id`; sentinel `result.json`; ACP deferred | hands-on T1 to T11 |
| S3 Docker | `spikes/S3-S4-docker-and-proxy.md` | `docker` CLI via `os/exec`; SDK deferred; `--internal` network plus Mint sidecar | Windows and Docker Desktop checklist |
| S4 proxy | same | hybrid proxy (MITM only on credentialed hosts); prototype works on Linux against a fake upstream | real GitHub and Copilot traffic; Windows |
| S5 GitHub App | `spikes/S5-S6-S8-github-platform.md` | three-token model; D-1 closed: Mint's App is a `main` bypass actor, `next-branch` requires `push: mint` | tests T1 to T17 |
| S6 state comment | same | numeric-id author check plus HMAC with `seq`; 8 KB budget | codec prototype and property tests (code) |
| S7 Windows host sandbox | none | none | hands-on only |
| S8 `next` integration | same as S5 | merge commit of head SHAs; recheck heads | T13 dry run |
| S9 sources | `spikes/S9-sources.md` | email over IMAP first, read-only then approval-gated; WhatsApp later via relay | IDLE/UIDVALIDITY on the real provider; Meta terms |
| S10 Claude adapter | `spikes/S10-claude-adapter.md` | `Agent` interface hosts Claude unchanged; repo-config lockdown via flags and managed settings; host runtime refuses Claude | hands-on tests |

---

## S0. Design-input review: safe outputs and Symphony spec (~half a day)

**Decision already made**: Mint builds and runs its own runners. GitHub Agentic Workflows (gh-aw) and GitHub Actions
execution are out of scope. This spike only mines prior art for ideas.

**Questions**
- gh-aw "safe outputs": how are requested actions (comment, label, PR) declared, validated, and applied by a separate
  trusted step? Can the pattern fit Mint's actions (`github.open-pr`, `comment`, `github.merge`, outbound replies)?
- gh-aw threat detection between agent output and the apply step: what is checked, and is any of it worth copying?
- OpenAI Symphony SPEC.md: reconciliation, stall detection, retry/backoff, workspace hooks, dynamic reload. Anything
  the design misses?
- Itervox (Go Symphony implementation): event types and automation triggers worth matching.

**Method**: read https://github.github.com/gh-aw/ (the initial research session could not reach this host; allow it or
paste pages), the Symphony SPEC.md, and the Itervox README. No installation or prototype needed.

**Decision**: a short list of adopted ideas, folded into design sections 5, 12, and 13.

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
