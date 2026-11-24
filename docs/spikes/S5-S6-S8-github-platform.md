> **Superseded in part (later decision):** Mint does not use a GitHub App; it uses the operator's own fine-grained PATs
> by role (design 7.3, spike S5 in research-spikes.md). The installation-token, JWT, narrowing, revocation and App
> permission-matrix sections below no longer apply. The rate-limit, merge-semantics, ruleset, comment and `next`
> findings still do, with PAT in place of installation token.

# Spikes S5, S6, S8: GitHub platform behaviour

Date: 2026-11-24. Scope: GitHub App identity (S5), PR state comment / HMAC / reconciliation cost (S6), nightly `next`
integration (S8). Inputs: design.md sections 5, 6, 7.3; research-spikes.md S5/S6/S8; implementation-plan.md M3, M6, M7.

## Method and limits of this research

- `docs.github.com` is **not resolvable from this session** (WebFetch: `getaddrinfo ENOTFOUND`). It must be added to the
  session's allowed network domains before page-level re-verification. No other host was tried after that.
- All evidence therefore comes from WebSearch result excerpts that quote docs.github.com pages (and a few
  community/third-party pages, flagged as such). I did not read any full docs page.
- Labels:
  - **VERIFIED**: stated in an official GitHub docs excerpt returned by search (URL given; re-read the page when
    docs.github.com is reachable).
  - **INFERRED**: follows from verified facts, or is well-known behaviour that no excerpt confirmed this session.
  - **UNVERIFIED**: needs a hands-on test (checklist at the end). Never rely on these.
- Search excerpts of Enterprise Server docs are used only where the REST/App behaviour is shared; flagged "(GHES page)".

---

# S5. GitHub App identity and token minting

## S5.1 Installation token endpoint, narrowing, lifetime

| Question | Answer | Source | Confidence |
|---|---|---|---|
| Endpoint | `POST /app/installations/{installation_id}/access_tokens`, authenticated with an app JWT in `Authorization` | https://docs.github.com/en/apps/creating-github-apps/authenticating-with-a-github-app/generating-an-installation-access-token-for-a-github-app | VERIFIED |
| Lifetime | Installation access tokens expire after 1 hour; response carries expiry, granted permissions, and accessible repositories | same | VERIFIED |
| `repositories` / `repository_ids` narrowing | If neither is given the token reaches every repo the installation was granted; supplying them narrows it | same (GHES page: https://docs.github.com/en/enterprise-server@3.20/apps/creating-github-apps/authenticating-with-a-github-app/generating-an-installation-access-token-for-a-github-app) | VERIFIED |
| `permissions` narrowing | If omitted, the token gets all permissions the app has; it can never be granted permissions the app itself lacks | same (GHES page) | VERIFIED |
| Maximum repos per token (500) and permission downgrade levels (write to read) | Believed true, not seen in an excerpt | n/a | UNVERIFIED |
| JWT format: RS256, `iss` = app/client ID, `exp` at most 10 min, `iat` backdated for clock skew | Standard App auth, not confirmed by an excerpt this session | https://docs.github.com/en/apps/creating-github-apps/authenticating-with-a-github-app/authenticating-as-a-github-app-installation (listed by search, not read) | INFERRED |
| Revocation | `DELETE /installation/token` (called with the installation token itself) revokes it; no excerpt retrieved | n/a | UNVERIFIED (test T2) |
| Token format change | GHES docs state that from 2026-04-27 GitHub began a staged rollout of a stateless token format `ghs_APPID_JWT` for newly minted installation tokens; apps that assume 40-character tokens may break; a temporary header exists for testing | GHES page above | VERIFIED for GHES text; applicability to github.com UNVERIFIED |

Consequences:

- Per-session agent tokens (one repo, minimal permissions) are supported by the API as designed in 7.3.
- The vault/log redaction code must not assume a fixed token length or a `ghs_[A-Za-z0-9]{36}` shape. Treat tokens as
  opaque, possibly long (JWT-shaped) strings; the placeholder substitution and the "reject secret-looking values" check
  (plan M2 task 4) must tolerate this.
- Tokens are bearer secrets with a 1 h cap; run duration cap below TTL (design 7.3) is consistent.

## S5.2 Rate limits and polling cost

| Question | Answer | Source | Confidence |
|---|---|---|---|
| Primary limit for installation tokens | Minimum 5,000 requests/hour per installation; scales with installation size (GHES page: +50/hour per repo above 20, cap 12,500); Enterprise Cloud orgs 15,000. github.com scaling ceiling not confirmed | https://docs.github.com/en/apps/creating-github-apps/registering-a-github-app/rate-limits-for-github-apps | VERIFIED (5,000 floor, 15,000 EC); scaling on github.com INFERRED |
| Is the limit per token or per installation? | Documented as the installation's limit; many narrowed tokens for one installation almost certainly share it | same | INFERRED (test T3 reads `X-RateLimit-*` across two tokens) |
| Secondary limits | At most 100 concurrent requests (shared REST+GraphQL), at most 900 points/min for REST (2,000 for GraphQL); GET costs ~1 point, writes ~5; 403 or 429 on violation, back off; subject to change | https://docs.github.com/en/rest/using-the-rest-api/rate-limits-for-the-rest-api | VERIFIED |
| Content-creation secondary limit (comments/PRs per minute/hour) | Believed to exist (tens per minute); not confirmed | n/a | UNVERIFIED |
| Conditional requests | Authenticated conditional requests (`If-None-Match` with a saved ETag) returning 304 do not count against the primary limit; they only save quota if the endpoint returns 304 | https://docs.github.com/en/rest/using-the-rest-api/best-practices-for-using-the-rest-api | VERIFIED |
| ETag stability across tokens | A community report says a different access token yields a different ETag, so 304 may not come back after token rotation. Not official | https://github.com/github/docs/issues/34689 and https://github.com/orgs/community/discussions/156480 (community) | UNVERIFIED (test T4) |
| Search API | 30 requests/min authenticated for all search endpoints except code search (own limit); 100 items per page; budget is separate from the core REST quota (third-party statement) | https://docs.github.com/en/rest/search/search | VERIFIED (30/min); separate-budget claim INFERRED |
| Search result cap (1,000) | Known for search generally, but only a third-party excerpt for code search was seen | n/a | UNVERIFIED |

Polling cost model (for the reconcile tick; INFERRED arithmetic, replace with measured numbers from T4):

- Per repo per tick: 1 conditional `GET /repos/{o}/{r}/pulls?state=open` + 1 conditional
  `GET /repos/{o}/{r}/issues?labels=mint&state=open` + 1 conditional `GET /repos/{o}/{r}/issues/comments?since=...`.
  That is 3 requests/repo/tick. With zero changes and working ETags, all are 304 (free for the primary limit, still
  subject to secondary concurrency).
- Worst case with ETags broken (token rotation): 3 x N repos x ticks/hour. At a 60 s tick: 180 N per hour, so about
  27 repos exhaust 5,000/h. At a 5 min tick: 36 N per hour, about 138 repos. Therefore: keep one orchestrator
  token per ETag cache generation, persist ETags keyed by (URL, token generation), and re-prime after rotation.
- Use the repo-wide comment listing with `since` rather than per-PR comment listing (one call, not one per PR).
  `since` support on `GET /repos/{o}/{r}/issues/comments` is INFERRED (test T5).
- Search API (`is:pr label:mint`) is the efficient cross-repo discovery tool but is capped at 30/min and has indexing
  lag; use it only for discovery across many repos, never for reconciliation of known PRs.

## S5.3 Can an App-authored PR be approved by the operator and merged by the App?

| Question | Answer | Source | Confidence |
|---|---|---|---|
| Authors cannot approve their own PR | Yes. With the App as PR author, the human operator is a different identity and can approve | https://docs.github.com/en/pull-requests/collaborating-with-pull-requests/reviewing-changes-in-pull-requests/approving-a-pull-request-with-required-reviews | VERIFIED (author rule); App-authored case INFERRED |
| Human approval counts toward required reviews | Reviewer needs write access; bot approvals may not count (community claim only) | https://github.com/orgs/community/discussions/181487 (community) | INFERRED (human approver), bot claim UNVERIFIED and irrelevant to this design |
| App merges the PR under protection/rulesets | Merging with `PUT /repos/{o}/{r}/pulls/{n}/merge` succeeds only if all required reviews and checks are satisfied, unless the App is a bypass actor. Mint design keeps no bypass, so it works when the operator's approval and green checks satisfy the rules | not retrieved | INFERRED (test T6) |
| App on bypass list may still fail to push to a protected branch when PR is required | Community report: bypass did not let the App push directly past "require PR" in one case | https://github.com/orgs/community/discussions/136531 (community) | UNVERIFIED |
| Merge-by-SHA guard (`sha` parameter must match head, else 409) | Believed part of the merge endpoint; not confirmed this session | n/a | UNVERIFIED (test T6) |
| Request the operator's review | `POST /repos/{o}/{r}/pulls/{n}/requested_reviewers`, needs Pull requests write; reviewer must be a collaborator | n/a | INFERRED |
| Dismiss stale approvals / require approval of most recent push | Ruleset/branch-protection options exist; Mint should enable "dismiss stale reviews on push" so GitHub enforces design 5.4's `head_sha_matches_approval` too | n/a | INFERRED (test T6) |
| Review object has `commit_id` | Used for `head_sha_matches_approval` | n/a | INFERRED |

## S5.4 Rulesets: restricting the bot and bypass actors

| Question | Answer | Source | Confidence |
|---|---|---|---|
| Bypass list can include GitHub Apps | Yes: roles, teams, or specific GitHub Apps. Newer "exempt" bypass type silently skips enforcement (different from audit-visible bypass) | https://docs.github.com/en/repositories/configuring-branches-and-merges-in-your-repository/managing-rulesets/about-rulesets and https://github.blog/changelog/2025-09-10-github-ruleset-exemptions-and-repository-insights-updates/ | VERIFIED |
| Multiple rulesets aggregate; most restrictive wins | Yes | about-rulesets page | VERIFIED |
| Push rulesets (file path, extension, size) | Apply to private/internal repos and their fork networks only. Not available for a public repo such as oh-my-posh | https://docs.github.com/en/repositories/configuring-branches-and-merges-in-your-repository/managing-rulesets/creating-rulesets-for-a-repository | VERIFIED (private/internal scope) |
| Restrict an App to push only `mint/*` and `next` | The App's `contents: write` is repo-wide; GitHub App permissions cannot be scoped to branches. Rulesets target refs, not actors, so the available pattern is: (a) on the default branch, require PR + reviews + checks with the App NOT in the bypass list (blocks direct pushes to `main`); (b) optionally a ruleset on all other refs except `mint/**` and `next` with restricted updates/creations, bypass = repository admin role (operator) | not confirmed by an excerpt about "Restrict updates"/"Restrict creations" | INFERRED (test T7) |
| Allow force-push of `next` by the App only | Ruleset on `next` with "block force pushes" and the App as a bypass actor, or no force-push rule on `next` at all plus (b) above | n/a | INFERRED (test T7) |
| Branch deletion of `mint/*` after merge | "Automatically delete head branches" repo setting, or App deletes ref (contents write) | n/a | INFERRED |

Important design conflict (see Design impact D2): the `next` to `main` merge by the App cannot satisfy a "required
approving review" rule unless the operator approves the `next` PR or the App is a bypass actor. Source PRs merged
"indirectly" are marked merged even if their own branch protection was not satisfied (verified in S8), so the `next`
PR's rules are the only gate on `main`.

## S5.5 Do pushes made with an App token trigger Actions?

| Question | Answer | Source | Confidence |
|---|---|---|---|
| `GITHUB_TOKEN` events | Events caused by `GITHUB_TOKEN` do not create new workflow runs, except `workflow_dispatch` and `repository_dispatch` | https://docs.github.com/en/actions/how-tos/write-workflows/choose-when-workflows-run/trigger-a-workflow | VERIFIED |
| App installation token | Docs recommend a GitHub App installation token (or PAT) when a workflow must trigger others, so pushes/PRs/labels by the App do trigger `push`, `pull_request`, `labeled` workflows | same | VERIFIED (recommendation); behaviour on target repos INFERRED (test T8) |
| First-time-contributor "approve and run" gate for bot-authored PRs | Not confirmed | n/a | UNVERIFIED (test T8) |
| Pushes that modify `.github/workflows/*` | Believed to require the `workflows` permission; without it the push is rejected. Keep it ungranted | n/a | INFERRED (test T9) |
| Merging a PR with the App token triggers `push` workflows on `main` (release workflow) | Follows from the App-token rule above (the 5.5 release trigger); a merge made by `GITHUB_TOKEN` would not | same page; auto-merge excerpt notes `GITHUB_TOKEN` merges don't trigger further runs | INFERRED |

## S5.6 Webhooks versus polling

- Polling (conditional requests) needs no public endpoint and survives daemon downtime without lost events; cost model
  in S5.2. Best-practices page recommends webhooks over polling when possible:
  https://docs.github.com/en/rest/using-the-rest-api/best-practices-for-using-the-rest-api (VERIFIED, excerpt via search).
- Webhooks need a reachable HTTPS endpoint (tunnel/relay), signature verification (`X-Hub-Signature-256`), and a
  reconcile poll anyway for missed deliveries. Redelivery API existence: UNVERIFIED.
- Recommendation: M6 ships polling only (ETag, 60 to 120 s tick, per-repo backoff). Webhook receiver is a later
  latency optimisation, with polling kept as the reconciler (INFERRED design judgement).
- Events API with `X-Poll-Interval` header (`GET /repos/{o}/{r}/events`) could cut polling further but has delivery
  lag and is not guaranteed complete: UNVERIFIED; do not rely on it.

---

# S6. PR state comment, HMAC, reconciliation cost

## S6.1 Comment mechanics

| Question | Answer | Source | Confidence |
|---|---|---|---|
| Maximum comment size | Body limit 65,536 characters; API error "Body is too long (maximum is 65536 characters)". A community tracker cites 262,144 bytes, so multi-byte text can hit the byte cap earlier | https://github.com/mshick/add-pr-comment/issues/93 and https://github.com/dead-claudia/github-limits (community) | INFERRED (consistent community reports; not from an official page) |
| Design implication | State JSON should be far below this (budget a few KB); no logs or analysis in the comment beyond a size cap; analysis output stored there (design 5.3) needs truncation plus a pointer (for example a committed file on the branch) | n/a | INFERRED |
| Endpoints | PR comments are issue comments: `POST /repos/{o}/{r}/issues/{n}/comments`, `PATCH /repos/{o}/{r}/issues/comments/{id}` | https://docs.github.com/en/rest/issues/comments | VERIFIED (endpoint family) |
| Who can edit/delete others' comments | Not retrieved. Believed: comment author can edit/delete own; users with write (maintain/admin) can edit and delete others'; a human operator can therefore alter or delete Mint's comment | n/a | UNVERIFIED (test T10) |
| Authorship check | Issue-comment objects carry `user` (login `<slug>[bot]`, `type: "Bot"`, stable numeric `id`) and, believed, `performed_via_github_app` (app id/slug). Not confirmed by an excerpt | https://docs.github.com/en/rest/issues/comments (page listed, fields not seen) | UNVERIFIED (test T10) |
| Edit races | Issue-comment PATCH has no documented optimistic-concurrency token (no If-Match seen). Last write wins | n/a | UNVERIFIED |

Design guidance (INFERRED):

- Verify authorship by numeric `user.id` of the App's bot account (stored from `GET /app` + `GET /users/{slug}[bot]`),
  not by login string alone; additionally require HMAC. If `performed_via_github_app.id` exists, check it too.
- A human with write access can edit the comment body: HMAC detects forged content, but cannot prevent deletion or
  replay of an older valid comment. Include in the signed payload: run key, monotonically increasing `seq`, head SHA
  at checkpoint, and `updated_at` intent; reject any state with `seq` lower than what the branch's last
  `Mint-Run:` commit trailer claims. On missing/invalid comment: mark Run `needs_attention`, rebuild from branch
  commits + trailers, post a new comment (never trust the edited one).
- Because humans edit the PR body, the comment-not-body choice in design section 6 is right.

## S6.2 Atomic claim via ref creation

| Question | Answer | Source | Confidence |
|---|---|---|---|
| Create ref endpoint | `POST /repos/{o}/{r}/git/refs` (`ref`: `refs/heads/mint/...`, `sha`). Documented statuses include 201, 409 (conflict), 422 ("Validation failed, or the endpoint has been spammed") | https://docs.github.com/en/rest/git/refs | VERIFIED (status list) |
| Creating a ref that exists | Returns 422 with message "Reference already exists" (seen in an Octokit error report, not in the docs text) | https://gitlab.com/gitlab-org/gitlab-ce/-/issues/17766 (third-party report) | INFERRED |
| Atomicity under a race | Server-side ref creation is expected to be atomic (one wins, one gets 422) | n/a | UNVERIFIED (test T11 with 20 parallel creates) |
| Spurious "Reference update failed" 422 | Intermittent 422 on valid requests reported; claim code must distinguish "already exists" (read the ref, compare) from transient failure and retry with backoff | https://github.com/github/rest-api-description/issues/4887 (GitHub issue) | INFERRED |

Claim algorithm: `POST git/refs` for `mint/<key>`; 201 = claimed; 422 + `GET ref` exists = lost (or resumed, if the
state comment shows this daemon's lease); other 422/5xx = retry. A plain `git push` of a new branch that already exists
is a non-fast-forward/rejection, but the REST route gives a clean signal.

## S6.3 Fetching efficiently

- Candidate listing: `GET /repos/{o}/{r}/pulls?state=open` (conditional), then filter by head ref prefix `mint/`
  client-side (head filter `head=owner:mint/key` is available for known keys; INFERRED). Labels live on the issue
  side: `GET /repos/{o}/{r}/issues?labels=mint&state=open` (includes PRs; each item has a `pull_request` key). INFERRED.
- Author filtering: issues list `creator=` param (INFERRED); search `is:pr author:app/<slug>` works across repos but is
  rate-limited to 30/min (verified limit, query syntax INFERRED).
- Reconciliation per Run: 1 comment-list call (shared repo-wide with `since`) + PR read only when the ETag changed.
- Cost: see S5.2 model. Target measured numbers in test T4.

## S6.4 Human interference cases (all UNVERIFIED, test T12)

- Edit state comment: HMAC mismatch; handled as above.
- Delete state comment: treat as lost; rebuild from branch + trailers.
- Force-push `mint/<key>`: head SHA no longer matches checkpoint; Run goes to `needs_attention`; approval already
  invalid since a new head.
- Close PR: `closed` event; Mint cancels Run and does not reopen; `mint:stop` equivalent.

## S6.5 HMAC and key derivation

Design-level (not a GitHub question): HKDF-SHA256 from a vault master secret with info = `mint-state-v1`, a key id in the
payload (`kid`) for rotation, verify against current and previous key. Property tests as specified in S6 exit criteria.
Confidence: INFERRED design guidance; no GitHub dependency.

---

# S8. Nightly `next` integration

## S8.1 How GitHub decides a PR is merged

| Question | Answer | Source | Confidence |
|---|---|---|---|
| Indirect merge | A PR is marked merged when its head branch's tip commit becomes reachable from the base branch tip, even if merged externally (another PR, or push to base) | https://docs.github.com/en/pull-requests/reference/pull-request-merges and https://docs.github.com/en/pull-requests/collaborating-with-pull-requests/incorporating-changes-from-a-pull-request/about-pull-request-merges | VERIFIED |
| Squash / rebase | Squash and rebase create new commits on the base, so other PRs holding the original commits are NOT auto-marked merged | same | VERIFIED |
| Branch protection for indirect merges | Indirectly merged PRs are marked merged even if branch protection requirements were not satisfied | same | VERIFIED |
| Timeline link | Changelog: timeline shows a link to the PR that caused indirect merge | https://github.blog/changelog/2023-09-26-more-details-provided-when-a-pull-request-is-merged-indirectly-or-is-still-processing-updates/ | VERIFIED |

Consequences for design 5.5:

- The claim "merge commit so source PRs are marked merged" is correct **only if** (a) `next` is built by actually
  merging each source PR's head commit (`git merge <head sha>` as a true merge commit, not cherry-pick/squash/rebase),
  (b) `next` is merged into `main` with a **merge commit** (not squash/rebase), and (c) the source PR's current head
  equals the SHA that was merged into `next`. If a PR gets a new push after `next` was built, its new tip is not
  reachable from `main`, so it stays open but its old commits are in `main`. Mint must exclude it at build time and
  re-check heads right before merging `next` (INFERRED from the reachability rule).
- Source PRs must target `main` (design `base: main`): reachability is evaluated against the PR's base branch.
- The merge-commit method must be enabled in repo settings and must not conflict with a "require linear history" rule
  (INFERRED; test T13). Mint must use `merge_method: merge` for `next`.
- Because indirect merges skip the source PR's protection, the source-PR approval/CI/hold checks in `collect-prs` are
  the only gate for individual changes. Mint must re-verify approval on current head, green checks, not draft,
  no `hold` label at build time (already in design) and again before merging `next`.
- Issue auto-close keywords (`Fixes #n`) on indirectly merged PRs: behaviour not retrieved; UNVERIFIED (T13).

## S8.2 CI green: check runs vs statuses

| Question | Answer | Source | Confidence |
|---|---|---|---|
| Two systems | Commit statuses (simple pass/fail, `GET /commits/{ref}/status` combined) and check runs (richer, grouped into check suites per App) coexist; no single legacy endpoint merges both (2019 write-up said so; may have changed) | https://docs.github.com/en/pull-requests/collaborating-with-pull-requests/collaborating-on-repositories-with-code-quality-features/about-status-checks ; https://dev.to/gr2m/github-api-how-to-retrieve-the-combined-pull-request-status-from-commit-statuses-check-runs-and-github-action-results-2cen (third-party) | VERIFIED (both exist); single-endpoint claim UNVERIFIED |
| Check suite | Collection of check runs created by a single App for a commit; summarises status/conclusion | https://docs.github.com/en/rest/guides/using-the-rest-api-to-interact-with-checks | VERIFIED |
| Actions workflows | Actions report as check runs (INFERRED, well-known) | n/a | INFERRED |
| Same-name status and check run | If a check run and a status share a name/context, both are required (2018 changelog; may be outdated) | https://developer.github.com/changes/2018-05-07-new-checks-api-public-beta | UNVERIFIED for current behaviour |

Recommended "checks_green" implementation (INFERRED): for head SHA, query both
`GET /repos/{o}/{r}/commits/{sha}/check-runs` (paginate, filter latest per name, require `conclusion` in
success/neutral/skipped, no `queued`/`in_progress`) and `GET /repos/{o}/{r}/commits/{sha}/status` (state `success`
or no statuses). Where possible compare against the repo's required-check list from the ruleset/branch-protection
settings and require those specifically (missing required check = not green, not "no checks"). Treat zero checks
total as not green unless the project config says otherwise. As the final authority, attempt the merge and let GitHub
refuse if rules are unmet (design 5.4 principle). Timeout 90 m with backoff polling on the same ETag-aware calls.
Test T14.

## S8.3 Auto-merge

| Question | Answer | Source | Confidence |
|---|---|---|---|
| Preconditions | Repo setting "Allow auto-merge" enabled AND base branch has a protection rule with at least one requirement; option shown only when PR cannot merge immediately | https://docs.github.com/en/repositories/configuring-branches-and-merges-in-your-repository/configuring-pull-request-merges/managing-auto-merge-for-pull-requests-in-your-repository | VERIFIED |
| API | Enabling per PR is via GraphQL `enablePullRequestAutoMerge`; no REST endpoint for the per-PR setting (community statement); repo-level toggle via REST | https://github.com/orgs/community/discussions/24719 (community) | INFERRED |
| Disabled on push by a user without write | Auto-merge is disabled if someone without write access pushes to the PR | managing-auto-merge page | VERIFIED |
| App installation token permissions for the mutation | Not found | n/a | UNVERIFIED (T15) |

Recommendation: Mint should not depend on auto-merge. Its own wait-for-checks then `PUT merge` is simpler and keeps
control of the head-SHA guard. Auto-merge is an optional later optimisation.

## S8.4 Release trigger and idempotency

- A merge to `main` made with the App token should trigger `push`-based release workflows (S5.5; INFERRED, T8). A merge
  performed by `GITHUB_TOKEN` would not. `workflow_dispatch` is needed only if the release workflow is not
  `push`/`pull_request`-triggered; dispatch is exempt from the `GITHUB_TOKEN` rule (VERIFIED) and needs Actions: write.
- Interrupted after force-push of `next`: key the run by date marker (design) and store in the PR/state comment the
  list of (PR number, merged head SHA) pairs plus `next` tip SHA. On resume compare remote `next` tip to the recorded
  tip; if equal continue, else rebuild deterministically from `main` and the same ordered list. Rebuild ordering:
  ascending PR number (INFERRED deterministic choice). Conflicts: `git merge --no-ff`, on conflict abort that
  merge, skip, comment on that source PR. All INFERRED; prove in the S8 scratch dry run (T13).
- The nightly PR (`next` to `main`) head changes on every force-push; GitHub recomputes `mergeable` lazily, so read
  `mergeable_state` with retry after a push (INFERRED).

---

# Recommended App permission matrix

Principle: grant the App the superset; the broker narrows each minted token. (Repository permissions; Metadata read is
mandatory. Permission names INFERRED from App settings UI; per-endpoint mapping UNVERIFIED because the "Permissions
required for GitHub Apps" page was not retrievable. Confirm with the `X-Accepted-GitHub-Permissions` response header
(mentioned in the permissions page excerpt) during tests T1/T6.)

| Action (design flow) | Endpoint / operation | Permission | Orchestrator token | Agent token |
|---|---|---|---|---|
| Discover issues/PRs, labels | list issues/PRs, labels | Issues: read, Pull requests: read | yes | no |
| Add/remove labels (`mint:*`) | issues labels | Issues: write | yes | no |
| Read PR, reviews, comments | pulls, reviews, issue comments | Pull requests: read, Issues: read | yes | optional read |
| Claim: create `mint/<key>` ref | `POST git/refs` | Contents: write | no (see note) | yes (or orchestrator, decide below) |
| Push commits to `mint/*`, force-push `next` | git push | Contents: write | no | yes |
| Open (draft) PR, update PR | `POST pulls`, `PATCH pulls/{n}` | Pull requests: write | no | yes |
| State comment create/update | issue comments | Issues: write and/or Pull requests: write (PR comments are issue comments; which one suffices is UNVERIFIED) | yes | no |
| Request review | `requested_reviewers` | Pull requests: write | yes | no |
| Merge PR | `PUT pulls/{n}/merge` | Contents: write (believed) and Pull requests: write; confirm | yes (short-lived, repo-scoped, minted at merge time) | no |
| Read CI state | check-runs, combined status | Checks: read, Commit statuses: read | yes | no |
| Read Actions run state / workflow files for diagnostics | actions runs | Actions: read | optional | no |
| `github.dispatch-workflow` (optional) | `workflow_dispatch` | Actions: write | yes, only when configured | no |
| Edit `.github/workflows/*` | push touching workflows | Workflows: write | never granted | never granted |
| Repo settings, rulesets, branch protection | admin | Administration | never granted (operator configures) | never granted |
| Webhooks (later) | App-level webhook, not per-repo | none per repo (App webhook config) | n/a | n/a |
| Dismiss stale approvals | n/a (repo setting) | none | n/a | n/a |

Notes:

- The design's two-token split (7.3): orchestrator token has `issues: write` (labels, comments), `pull_requests: write`,
  `contents: read`, `checks: read`, `statuses: read`, `metadata: read`; it needs `contents: write` only at the moment of
  merge, so mint a separate merge token (repo-scoped, permissions narrowed to merge needs, TTL consumed within the
  step). The design currently says orchestrator is "Issues read, label write, PR read", which is too small for state
  comments, review requests, and merging. See D1.
- The claim (ref create) can use the agent-scoped permission set minted by the orchestrator right before the run, so the
  orchestrator never holds `contents: write` long-term.
- Nothing needs `administration`, `workflows`, `secrets`, or `members`.

---

# Design impact

Recommended edits (not applied):

design.md

- D1 (7.3): replace the orchestrator-token permission list with the matrix above: orchestrator = Issues write, Pull
  requests write, Contents read, Checks read, Commit statuses read; add a third short-lived **merge token** (Contents +
  Pull requests write, one repo) minted at merge time; agent token = Contents write + Pull requests write, one repo.
- D2 (5.4, 5.5): state explicitly how the nightly `next` to `main` merge satisfies `main` protection with "Mint never
  bypasses". Options: (a) ruleset on `main` requiring checks but zero required reviews for PRs whose head is `next`
  (cannot express per-head rules: rulesets target the base ref, so this is likely not expressible); (b) operator
  approves the `next` PR (breaks unattended nightly); (c) App is a ruleset bypass actor for `main` (contradicts the
  principle, but bypass is audit-visible; needs sign-off). Mark as open question, test T6/T13. Also note source PRs are
  marked merged even when their protections were unmet, so the pre-merge re-verification is mandatory.
- D3 (5.5): tighten the merge-commit bullet: true merge of each source head SHA into `next`, `next` merged with
  merge commit, source head must equal the merged SHA at the moment of the final merge; list repo prerequisites (merge
  commits allowed, no linear-history rule on `main`).
- D4 (6): specify authorship check as `user.id` (+ `performed_via_github_app.id` if present), HMAC payload fields
  (`kid`, run key, `seq`, head SHA), replay/delete handling, 64 KB comment cap with a hard 8 KB state budget and a
  committed file for large analysis output.
- D5 (6): claim semantics: `POST git/refs`, 201 = won, 422 "Reference already exists" = lost/resume; distinguish
  transient 422; lease info lives in the comment.
- D6 (12 or 7.3): document that rulesets cannot restrict an App to `mint/*` and `next` by actor; the enforceable
  controls are: no bypass for the App on `main`, required PR/reviews/checks on `main`, optional restrict-creations/updates
  ruleset on other refs with operator admin bypass, and no `workflows` permission. Push rulesets (path restrictions)
  are only available on private/internal repos.
- D7 (7.3 / 8): do not assume token length/shape (stateless `ghs_APPID_JWT` rollout); redaction by exact value, not
  pattern.
- D8 (15 open questions): add: ETag stability across token rotation; bot-authored PR workflow approval gate;
  per-endpoint permission mapping; comment edit/delete rights; per-installation rate limit sharing.

implementation-plan.md

- P1 (M3 GitHub client): ETag cache keyed by (URL, token generation), persisted; conditional-request support is a
  hard requirement; secondary-limit handling (403/429, `Retry-After`, serialised writes, concurrency cap well below
  100); `X-RateLimit-*` metrics per token.
- P2 (M6 task 1): add token narrowing tests (repos + permissions in request body), `DELETE /installation/token` on run
  end, expiry margin, and tolerant token format handling; merge-token minting.
- P3 (M6 task 2): reconcile via repo-wide `issues/comments?since`, open PR list, and label list, all conditional; no
  per-PR polling; search API only for cross-repo discovery with a 30/min budget.
- P4 (M3/M6): state codec carries `kid`, `seq`, head SHA; authorship check by numeric id; property tests for replay
  and deletion; claim via `git/refs` with the 201/422 mapping, covered by a fake GitHub that returns real status codes.
- P5 (M7): `collect-prs` records (PR, head SHA) pairs; `git.integrate` uses true merges of head SHAs; final pre-merge head
  re-check; `wait: checks_green` queries check runs and combined status (and required-check list); merge method
  `merge`; resume by recorded `next` tip; dry-run acceptance adds "PR pushed after build is left open and excluded".
- P6 (Plan section on S5): add the hands-on checklist below as exit criteria; add `docs.github.com` to the research
  session's allowed hosts and re-read the pages cited here before finalising ADRs.
- P7: new ADRs: 0001 two-/three-token model and App permissions; 0002 main-protection strategy for nightly merge;
  0003 polling with ETags (webhooks later).

---

# Hands-on tests still required (scratch repo, App installed)

Setup: private scratch repo (to also test push rulesets) plus a public scratch repo; App with the matrix permissions;
operator account as collaborator.

- [ ] T1 Mint a token with `repositories:[scratch]` and `permissions` narrowed; confirm response `permissions`
  and `repositories`; record token length/prefix (stateless format?); capture `X-Accepted-GitHub-Permissions` on each
  endpoint used (fills the matrix); try requesting a permission the App lacks (expect 422) and a repo not installed.
- [ ] T2 `DELETE /installation/token` returns 204 and the token then gets 401; confirm expiry value is about 1 h.
- [ ] T3 Mint two tokens for the same installation; compare `X-RateLimit-Limit/Remaining`; do they share one budget?
- [ ] T4 ETag test: list PRs/issues/comments with ETag; repeat unchanged (304, and `Remaining` unchanged?); mint a new
  token and repeat with the old ETag (still 304?). Measure requests per reconcile tick for 1, 5, 20 repos.
- [ ] T5 `GET /repos/{o}/{r}/issues/comments?since=` returns PR comments; `issues?labels=&creator=` includes PRs;
  `pulls?head=` filter works; search `is:pr label:mint author:app/<slug>` returns expected results and lag.
- [ ] T6 Merge flow: App opens PR on `mint/x`; ruleset on `main` requires 1 review + checks; operator approves; App merges
  with merge token and `sha` param (wrong sha gives 409?); verify push after approval dismisses it (stale review
  setting); verify App without bypass cannot merge before approval; test `requested_reviewers`.
- [ ] T7 Ruleset experiments: (a) App cannot push `main` directly; (b) restrict-updates on all refs except `mint/**`,
  `next` with admin bypass: App can push `mint/x`, `next` (including force-push) but not `foo`; (c) public vs
  private repo availability of push rulesets.
- [ ] T8 App-token push/PR/label/merge each trigger `push`, `pull_request`, `labeled`, release workflows; compare with
  a `GITHUB_TOKEN` push; check whether bot-authored PRs need "approve and run workflows".
- [ ] T9 Push touching `.github/workflows/*` without `workflows` permission is rejected (message recorded).
- [ ] T10 Comments: App posts state comment; read `user.login/type/id` and `performed_via_github_app`; operator
  (write) edits then deletes it (allowed?); a third-party user with only triage/read tries; confirm `updated_at` and
  edit visibility; concurrent PATCH last-write-wins; 65,536-character boundary with multi-byte characters.
- [ ] T11 Race: 20 parallel `POST git/refs` for the same name from two processes; exactly one 201, others 422 with the
  recorded message; also `git push` race for comparison; observe any spurious "Reference update failed".
- [ ] T12 Human interference: edit/delete comment, force-push `mint/x`, close/reopen PR; record events and
  timestamps seen by polling.
- [ ] T13 `next` dry run: three approved PRs (one conflicting, one with a push after build). `git merge` each head into
  `next`, open PR, merge with merge commit: confirm source PRs flip to merged, the post-build-push PR stays open;
  repeat with squash and rebase to confirm they do not flip; test with "require linear history" on; check `Fixes #n`
  closing; interrupt after force-push and resume; double fire no-op; `mergeable_state` timing.
- [ ] T14 CI green: PR with Actions check runs plus a legacy commit status; confirm combined-status and check-runs
  results, required-check names list from ruleset API, handling of zero checks, re-runs (latest per name).
- [ ] T15 Auto-merge via GraphQL with an installation token (permissions needed); behaviour with required reviews.
- [ ] T16 Secondary limits: burst of 30 writes (comments/labels) to see the content-creation throttle and
  `Retry-After` semantics; confirm concurrency cap behaviour.
- [ ] T17 Re-fetch and re-read the cited docs.github.com pages once the host is allowed; update confidence labels.
