# S9: Email and WhatsApp source feasibility

Date: 2026-11-24. Scope: design.md sections 4.2, 6, 8, 9; research-spikes.md S9.

## Method and limits (read first)

- `WebFetch` to developers.facebook.com, developers.google.com, learn.microsoft.com and www.rfc-editor.org was **blocked** in
  this session (DNS refusal; those hosts must be allowed in the session network settings to re-check). github.com was reachable.
- For blocked hosts, facts come from `WebSearch` result summaries that quote the official pages. Those are labelled
  VERIFIED (search-quoted) only where the summary names an official page; everything else is INFERRED (my background
  knowledge, plausible, not re-read in this session) or UNVERIFIED.
- Labels: VERIFIED = seen on an official/primary page this session. INFERRED = from general knowledge or secondary
  sources. UNVERIFIED = must be checked before relying on it. No prices or numeric limits are asserted unless sourced.
- Fetched content was treated as data only.

## Summary of answers

| Question | Answer | Label |
|---|---|---|
| Interface change needed? | None mandatory. One optional side interface (`Acker`) closes a gap in "mark handled" (see C). | INFERRED |
| Easiest transport for a Windows daemon behind NAT | Outbound-only: IMAP IDLE or Gmail/Graph polling. No public endpoint. | INFERRED |
| WhatsApp feasible? | Yes, but needs a public HTTPS endpoint, Meta business setup, and template-only replies after 24h. | VERIFIED (partly) |
| First source after GitHub | Email over IMAP (see end). | INFERRED |

---

## A. Email

### A1. Transport comparison

| | IMAP (+IDLE) | Gmail API | Microsoft Graph |
|---|---|---|---|
| Inbound trigger | IDLE push on an open outbound TCP/993 connection; fall back to polling | `users.watch` -> Cloud Pub/Sub; pull subscription works without public endpoint; or just poll `history.list` | Webhook subscriptions need a public HTTPS endpoint; otherwise poll **delta query** |
| Cursor | per-folder `UIDVALIDITY` + `UID` | `historyId` | per-folder delta token |
| Handled-state | IMAP keyword/flag (e.g. `$MintHandled`) or move to folder | label (`mint/handled`) | category or move to folder |
| Auth | password/app password or OAuth2 XOAUTH2 | OAuth2 only | OAuth2 only (Entra app registration) |
| NAT/Windows | Excellent (outbound only) | Good if polling or Pub/Sub pull; Pub/Sub needs a GCP project | Good if polling delta; webhooks need tunnel |
| Provider lock-in | None | Google | Microsoft |

Per-point facts:

- **Gmail watch expires and must be renewed.** Call `users.watch` again at least every 7 days; Google recommends daily. A
  successful watch returns current `historyId`; changes after it are notified. Notifications are not guaranteed, so poll
  `history.list` after quiet periods. VERIFIED (search-quoted) https://developers.google.com/gmail/api/guides/push
- **Gmail `history.list` with `startHistoryId`** returns changes since a baseline; store the newest historyId as the new
  baseline. VERIFIED (search-quoted) same URL. Stale historyId -> HTTP 404, fall back to full sync: INFERRED (known behaviour,
  not re-read) https://developers.google.com/gmail/api/guides/sync
- **Gmail scopes.** `gmail.modify` is a *restricted* scope: production use needs OAuth app verification and, if data
  passes through your servers, an annual third-party security assessment (CASA). Apps left in "Testing" are exempt but
  refresh tokens expire after 7 days and the user cap is 100. VERIFIED (search-quoted, partly third-party)
  https://developers.google.com/workspace/gmail/api/auth/scopes and https://support.google.com/cloud/answer/13463817.
  The 7-day figure rests on community/third-party sources: UNVERIFIED against Google primary docs.
  Implication: a personal daemon on Gmail via API either lives in Testing mode (weekly re-consent, painful) or needs
  verification (heavy). **This is a major con for the Gmail API for a single-operator tool.** Google Workspace
  internal-use apps avoid the public verification: INFERRED.
- **Graph mail notifications.** Subscriptions expire and need renewal; conflicting max lifetime in Microsoft pages (4230
  min on the Outlook overview, 10,080 min on the subscription reference). UNVERIFIED which applies; renew conservatively
  (hours). Lifecycle notifications (`reauthorizationRequired`, `missed`) exist and Microsoft suggests delta query to
  recover missed changes. VERIFIED (search-quoted) https://learn.microsoft.com/graph/outlook-change-notifications-overview ,
  https://learn.microsoft.com/graph/change-notifications-lifecycle-events . Webhook delivery needs a public HTTPS URL:
  INFERRED (standard for Graph webhooks); polling delta avoids it.
- **IMAP against Microsoft 365** requires OAuth2 (XOAUTH2); basic auth is disabled in Exchange Online. VERIFIED
  (search-quoted) https://learn.microsoft.com/exchange/clients-and-mobile-in-exchange-online/deprecation-of-basic-authentication-exchange-online
  Tenant admin must also enable IMAP for the mailbox and grant the app `IMAP.AccessAsUser.All` (or service principal
  access): INFERRED.
- **IMAP against Gmail** needs OAuth2 or an app password (requires 2-step verification; availability on Workspace is
  admin-controlled): UNVERIFIED (search returned no Google primary page). Check https://support.google.com/mail/answer/185833.

### A2. IMAP semantics that matter

- **UID/UIDVALIDITY.** A message is identified by (mailbox, UIDVALIDITY, UID). UIDs are strictly ascending within one
  UIDVALIDITY value; if UIDVALIDITY changes the client must discard cached UIDs and resync. INFERRED (RFC 3501 s2.3.1.1,
  well known; page blocked) https://www.rfc-editor.org/rfc/rfc3501#section-2.3.1.1
  Design rule: never use bare UID as `Event.ID`. Use the RFC 5322 `Message-ID` (fallback: hash of `UIDVALIDITY:UID:Date:From`
  when absent or duplicated) and store UIDVALIDITY in the cursor only as an optimisation.
- **IDLE** (RFC 2177): server pushes `EXISTS` while the client holds the command; the client must re-issue IDLE before the
  ~29 min server timeout and tolerate silent NAT drops (also use TCP keepalive and a poll every few minutes as backstop).
  INFERRED https://www.rfc-editor.org/rfc/rfc2177
- **Handled-state.** Custom keywords need `PERMANENTFLAGS` to include `\*`; many servers (incl. Gmail IMAP via labels,
  Exchange) differ. Portable choices: set `\Seen` AND `\Flagged`-free approach is fragile, so prefer **move the message to
  a `Mint/Handled` folder** (UID MOVE, RFC 6851, else COPY+STORE `\Deleted`+EXPUNGE), with keyword as best effort. On Gmail
  IMAP a label is a folder, so copying to `Mint/Handled` applies the label. INFERRED.
- **Source-side handled is only a hint.** Truth is the GitHub tracking issue carrying the Message-ID (design.md s6). On
  startup, search the tracked IDs first; mailbox flags just stop re-listing. Order of operations: emit -> router creates
  tracking issue -> `Ack` -> mark handled. A crash between issue creation and flag causes a re-emit that dedupes on the issue
  lookup. INFERRED (follows from s6).

### A3. Authentication-Results (SPF/DKIM/DMARC) and trust

- RFC 8601: an MTA/receiver records its SPF/DKIM/DMARC verdicts in `Authentication-Results: <authserv-id>; spf=pass ...;
  dkim=pass header.d=...; dmarc=pass header.from=...`. The `authserv-id` identifies the verifying system. Verifiers must
  remove/ignore pre-existing A-R headers claiming their own authserv-id at the trust boundary, and consumers must only
  trust headers added by their own ADMD. VERIFIED (search-quoted, section titled "Removing Existing Header Fields";
  specifics via secondary source) https://www.rfc-editor.org/rfc/rfc8601
- **Pitfalls (all INFERRED):**
  1. Anyone can send a message with a forged `Authentication-Results:` header. Trust only A-R lines whose `authserv-id`
     equals your provider's known id (e.g. Gmail `mx.google.com`, Exchange Online `*.protection.outlook.com`) AND that
     sit above (were prepended after) the provider's `Received` hop. A-R headers are prepended, so scan from the top and take
     only the first block(s) up to the first foreign authserv-id; ignore the rest.
  2. Pass is not alignment. Require `dmarc=pass` (which includes alignment of SPF or DKIM with the `From` domain), or
     `dkim=pass` with `header.d` aligned to the From domain. Plain `spf=pass` on the envelope domain proves nothing about `From`.
  3. `dmarc=none`/no policy domain: DMARC may pass with `p=none`; for customer-tier senders still require aligned pass.
  4. Forwarders and mailing lists break SPF and often DKIM; ARC (`ARC-Authentication-Results`) is advisory and must not be
     trusted unless you trust the sealing party. Decision: unsupported -> tier `unknown` -> dropped.
  5. Gmail IMAP does expose A-R in the raw message; Microsoft Graph exposes headers via `internetMessageHeaders`
     (must `$select` it); the Gmail API returns them via `format=metadata` or `raw`. INFERRED.
  6. Display-name and `Reply-To` tricks: map sender on the parsed `From` address-spec only, lowercased; never on display
     name; ignore `Reply-To` for identity; reply goes to From unless operator edits the draft.
  7. If you self-host the receiving MTA, add your own A-R and strip inbound ones; a hosted mailbox means you rely on the
     provider's stamp.
- Go: `github.com/emersion/go-msgauth` has `authres` (parse A-R), `dkim` (verify) and `dmarc` (record lookup); MIT. VERIFIED
  https://github.com/emersion/go-msgauth . Release status not shown by the fetch: UNVERIFIED. Re-verifying DKIM yourself
  is possible but unnecessary and weaker than the provider's stamp (you lack the original SMTP envelope, so no SPF).
  Recommendation: parse provider A-R; do not re-verify.
- Event mapping: `Verified = dmarc pass (aligned)` from the trusted authserv-id; store raw verdict string in
  `Attrs["auth"]` and `Attrs["authserv"]` for audit.

### A4. Threading

- Use `Message-ID` (unique id), `In-Reply-To` (parent), `References` (ancestry chain) per RFC 5322 s3.6.4. INFERRED
  https://www.rfc-editor.org/rfc/rfc5322
- `ThreadRef.Key` = Message-ID of the **root**: first entry of `References`, else `In-Reply-To`, else own Message-ID.
  Mailbox providers also offer native thread ids (Gmail `threadId`, Graph `conversationId`); prefer RFC headers for
  portability and store the native id in `Attrs`. Subject-based threading only as a last resort (spoofable).
- Replies set `In-Reply-To` = last message's Message-ID and `References` = prior References + that id; keep `Re:` subject.
  Gmail additionally needs `threadId` on send to attach to the thread. INFERRED.

### A5. Drafts versus sending

- Mint policy (design s9): customer replies are drafts needing operator approval. The approval step belongs in GitHub
  (draft text posted as a comment on the tracking issue; operator approves by a label/command), after which `Reply` sends.
  This keeps GitHub as the only durable state.
- Optional courtesy: also save the text into the mailbox Drafts (IMAP `APPEND` with `\Draft`; Gmail `users.drafts.create`;
  Graph `POST /me/messages` creates a draft) so the operator can edit in their mail client. Treat the mailbox draft as
  non-authoritative: the approved text is what's in the GitHub comment. INFERRED.
- Sending needs SMTP submission (587, STARTTLS, XOAUTH2 or app password), Gmail `messages.send`, or Graph `sendMail`. The
  `mail.send` scope is separate and should be omitted until approval flow is proven (least privilege): IMAP read + append
  draft only. INFERRED.

### A6. Attachment and content safety (INFERRED, standard practice)

- Never open, render, preview or pass attachments to an agent by default. Event carries only metadata:
  `Attrs["attachments"]` = JSON list of filename/type/size/sha256 (hash computed while streaming, not stored).
- Cap message size read; strip HTML to text with a safe converter; drop remote content; no link fetching. Body is
  untrusted prompt-injection material: wrap it as data in agent prompts and never grant a customer-tier run write tokens
  (already in s9).
- Content-type is attacker-controlled; do not trust extension or MIME. Archives/Office macros: if an operator opts in to
  fetching, do so in the sandbox runtime with no network, never on the host.
- Cap per-sender rate and total tracking-issue creation per hour to blunt mail-bomb cost attacks.

### A7. Go libraries

| Library | Use | License | Maintenance | Label |
|---|---|---|---|---|
| github.com/emersion/go-imap/v2 | IMAP client (IDLE, UIDPLUS, MOVE, etc. in the v2 client) | MIT | README says v2 "still in development"; search results suggest v2.0.0-beta.x (beta.8 newest seen) on GitHub, no stable v2.0.0 seen | license VERIFIED https://github.com/emersion/go-imap ; version UNVERIFIED |
| github.com/emersion/go-imap (v1) | older stable client | MIT | superseded; v2 recommended for new code (INFERRED) | INFERRED |
| github.com/emersion/go-message | RFC 5322/MIME parse and compose, charset decoding | MIT | releases not shown by fetch | license VERIFIED https://github.com/emersion/go-message |
| github.com/emersion/go-msgauth | A-R parse, DKIM, DMARC lookup | MIT | releases not shown | VERIFIED https://github.com/emersion/go-msgauth |
| github.com/emersion/go-sasl | XOAUTH2/OAUTHBEARER SASL | MIT | INFERRED | UNVERIFIED |
| github.com/emersion/go-smtp | SMTP submission client | MIT | INFERRED | UNVERIFIED |
| google.golang.org/api/gmail/v1 (google-api-go-client) | Gmail API | BSD-3-Clause; repo "in maintenance mode", critical fixes only | VERIFIED https://github.com/googleapis/google-api-go-client ; gmail/v1 presence INFERRED |
| golang.org/x/oauth2 (+ google) | OAuth2 token source/refresh | BSD-3-Clause | INFERRED | INFERRED |
| github.com/microsoftgraph/msgraph-sdk-go | Graph v1.0 SDK | MIT; README says updated 2nd and 4th week of each month | VERIFIED https://github.com/microsoftgraph/msgraph-sdk-go ; known to be very large (slow builds): INFERRED |

Note: beta status of go-imap v2 is an adoption risk; pin an exact version and wrap behind a small internal interface.
Plain REST via `net/http` + `x/oauth2` is a lighter alternative to the Graph SDK: INFERRED.

### A8. OAuth2 refresh tokens and the vault

- Refresh tokens are long-lived bearer credentials; access tokens last about an hour and are kept only in memory as
  `Secret`. INFERRED.
- Providers may **rotate** refresh tokens (Microsoft returns a new one on refresh; the old stays valid for a window).
  Persist the new token atomically **before** using it, write-then-rename, vault file re-encrypted with the same AES-GCM
  scheme. Do not log. INFERRED.
- Revocation/expiry causes (`invalid_grant`): password change, admin revoke, Google Testing-mode 7 day expiry,
  6 months of inactivity (Google; UNVERIFIED), conditional access (Microsoft). The adapter must surface this as a clear
  operator-visible error and an issue on the control repo, not retry forever.
- Use the loopback (127.0.0.1 random port) or device-code flow for the one-time consent in `mint creds add`; the
  daemon then only refreshes. Device flow is supported by Microsoft; Google's device flow has limited scopes (Gmail not
  allowed): INFERRED, check https://developers.google.com/identity/protocols/oauth2/limited-input-device .

Vault entries (extends design s8: `imap-password`):

| Provider | Credential names |
|---|---|
| IMAP password / app password | `email-imap-password` (rename of `imap-password`) |
| IMAP/Graph/Gmail OAuth | `email-oauth-refresh-token`, `email-oauth-client-secret` (if confidential client; public/PKCE clients have none) |
| Non-secret config (not vault) | host, port, username, client id, tenant id, folder names, trusted authserv-id |

---

## B. WhatsApp (Business Platform Cloud API)

### B1. Webhooks and endpoint requirements

- Meta POSTs events (messages, statuses) to a callback URL you register; there is a GET verification handshake with a
  verify token you choose. INFERRED (well-known; page blocked)
  https://developers.facebook.com/documentation/business-messaging/whatsapp/webhooks/overview
- Callback must be **public HTTPS** with a valid certificate: INFERRED. Plain HTTP/self-signed generally rejected.
- **X-Hub-Signature-256**: HMAC-SHA256 of the **raw request body** keyed with the app secret, header form `sha256=<hex>`.
  Compute over raw bytes before JSON parsing; compare in constant time (`hmac.Equal`). VERIFIED (search-quoted)
  https://developers.facebook.com/documentation/business-messaging/whatsapp/webhooks/overview
- Retries: Meta retries failed deliveries for a limited time, so duplicates and out-of-order delivery occur: INFERRED.
  Return 200 fast, enqueue, process async.
- **Dedupe**: each inbound message has an id (`wamid.*`) in `entry[].changes[].value.messages[].id`. Use
  `Event.ID = "wa:" + wamid`. Status callbacks are separate, ignore for `Event`. INFERRED
  https://developers.facebook.com/documentation/business-messaging/whatsapp/reference/webhooks/whatsapp-incoming-webhook-payload
- Missed webhooks while the daemon is down: Meta retries then drops; there is no history API to pull inbound messages
  (INFERRED). Consequence: **WhatsApp cannot be reconciled from Meta after downtime**; use a relay that buffers (below).

### B2. Reaching a home/office Windows machine (INFERRED unless noted)

| Option | Notes |
|---|---|
| Cloudflare Tunnel (`cloudflared`) | Outbound-only; named tunnel keeps a stable hostname; runs as a Windows service. Quick tunnels have a changing URL, a 200-concurrent-request limit and are for testing only. Windows install does not auto-update. VERIFIED (search-quoted) https://developers.cloudflare.com/tunnel/concepts/ and https://developers.cloudflare.com/tunnel/advanced/local-management/as-a-service/windows/ . Requires a domain on Cloudflare: INFERRED. |
| ngrok / Tailscale Funnel | Similar; stable domain on paid or specific tiers: UNVERIFIED terms. |
| Cloud relay (tiny serverless function or VPS) | Receives webhook, verifies signature, appends to a queue; daemon polls/pulls outbound. **Best fit**: survives daemon downtime and keeps the home machine unexposed. Adds a hosted component and a second place holding the app secret. |
| Port-forward + DDNS | Exposes the home machine; not recommended. |

Whichever is used, the daemon's listener must verify the signature itself (defence in depth), bind to loopback, and only
the tunnel/relay talks to it.

### B3. 24-hour window, templates, replies

- A customer-service window opens/resets when the user messages the business; inside it, free-form (non-template)
  messages are allowed; outside it **only template messages** can be sent. VERIFIED (search-quoted)
  https://developers.facebook.com/docs/whatsapp/pricing
- Templates must be pre-created in the WhatsApp Manager and approved by Meta, with a category (utility, marketing,
  authentication) : INFERRED. Operator-approved customer replies fit the window; a reply after the window needs an
  approved template (e.g. "We have an update, please reply to continue"), which limits free-form drafts.
- Design consequence: `Reply` must know the window state. Record `last_inbound_at` in the tracking issue (GitHub state);
  if the window closed, the adapter returns a typed error `ErrWindowClosed` and the responder asks the operator to pick a
  template. No interface change; errors already flow back.

### B4. Account requirements and credentials

- Needs a Meta Business portfolio, a WhatsApp Business Account, a phone number registered to the Cloud API (two-step:
  request/verify code, then register; registration endpoint rate-limited; a 6-digit two-step PIN is set). VERIFIED
  (search-quoted) https://developers.facebook.com/documentation/business-messaging/whatsapp/business-phone-numbers/registration/
- A number already used in the consumer WhatsApp app must be removed from it, or onboarded via Meta's Business App
  coexistence flow: INFERRED/UNVERIFIED (check the "API solutions for WhatsApp Business App users" doc).
- Production tokens: use a **system user permanent token** (temporary tokens expire quickly). VERIFIED (search-quoted)
  https://developers.facebook.com/documentation/business-messaging/whatsapp/get-started
- Business verification (documents, timelines) is needed for higher limits and some features: UNVERIFIED (the search did not
  return the requirements). Check Meta Business Suite docs before committing.
- Policy: since 2026-01-15 the Business Solution terms bar general-purpose AI chatbot providers from the platform; reading
  of scope is disputed and mostly applies to distributing AI assistants. A support/triage flow with human approval is
  likely outside the ban but this is a legal read: UNVERIFIED. Sources (secondary):
  https://techcrunch.com/2025/10/18/whatssapp-changes-its-terms-to-bar-general-purpose-chatbots-from-its-platform ,
  https://respond.io/blog/whatsapp-general-purpose-chatbots-ban . Read the official terms before building.

Vault entries: `whatsapp-token` (already named in design s8; system-user token), `whatsapp-app-secret` (for signatures; do
not reuse `webhook-secret` unless it is the same value by design), `whatsapp-verify-token` (handshake; low sensitivity but
vault anyway). Non-secret: phone-number-id, WABA id, app id, public URL, relay endpoint.

### B5. Costs (high level only)

- Meta moved to **per-message** pricing for template messages (April 2025 per Meta's update notice); utility templates sent
  inside an open window are free; utility outside the window and all marketing templates are billable; non-template
  (free-form) messages are free; messages within a free entry-point window (72 h) are free. VERIFIED (search-quoted)
  https://developers.facebook.com/documentation/business-messaging/whatsapp/pricing
- Rates vary by country and category and change; **no figures asserted**: UNVERIFIED. For a low-volume support daemon the
  cost is expected to be small, but confirm on the pricing page and rate card.
- Third-party "conversation-based" pricing descriptions are outdated or conflicting; ignore them.

### B6. Media

- Inbound media arrives as a media **id**; download is a two-step authenticated GET (resolve id to a short-lived URL, then
  fetch with the bearer token). Media retained by Meta only for a limited time: lifetime UNVERIFIED. INFERRED
  https://developers.facebook.com/documentation/business-messaging/whatsapp/business-phone-numbers/media (verify path).
- Same safety rule as email: record metadata (type, size, id) in `Attrs`; do not download or open by default.

### B7. Alternatives and ToS risk

| Option | Notes | Risk |
|---|---|---|
| BSP / managed relay (Twilio, 360dialog, etc.) | Same Cloud API behind a vendor webhook/REST; simpler onboarding; per-message markup | Vendor cost and data processor; still Meta policy. INFERRED |
| Unofficial multi-device libs (whatsmeow, Baileys) | Link as a companion device; no template/window rules | Violates WhatsApp ToS in practice; number bans reported; **not recommended for customer-facing use**. Ban evidence not found in this search: UNVERIFIED but risk is widely documented. |
| WhatsApp Business App on phone + export | Manual | No automation |
| Telegram / Signal / SMS | Out of scope; Telegram Bot API is simple and polling-friendly (getUpdates) | INFERRED |

---

## C. Proposed `Source` adapters against design.md s4.2

Interface under test:

```go
type Source interface {
    Name() string
    Run(ctx context.Context, emit func(Event)) error
    Reply(ctx context.Context, t ThreadRef, m Message) error
}
```

### C1. Email adapter (`internal/source/email`)

- `Name()` = `"email"`. Config: provider kind (`imap` | `gmail` | `graph`), mailbox, folders, trusted authserv-id, cursor
  location, poll interval. Start with a single backend: `imap`.
- `Run`: connect (TLS 993), select INBOX, list unseen-by-Mint messages (not in `Mint/Handled`), loop with IDLE (re-issue every
  ~20 min) plus a 5 min poll backstop. For each message: fetch headers, parse with go-message, evaluate A-R from the
  trusted authserv-id, build `Event{ID: Message-ID, Source:"email", Kind:"message", Sender: {email, addr, display},
  Verified: dmarc aligned pass, Thread: {email, rootMessageID}, Subject, Body: text (size-capped), Attrs: {auth,
  authserv, attachments, uidvalidity, uid, mailbox}}`. `emit(ev)`. Reconnect with backoff; on UIDVALIDITY change drop
  the cursor and rescan using Message-ID lookups against GitHub.
- Handled marking: after router confirms the tracking issue exists, move to `Mint/Handled` (see gap below).
- `Reply(t, m)`: look up the root and last message by `ThreadRef.Key` (IMAP SEARCH HEADER Message-ID), build the reply
  with `In-Reply-To`/`References` via go-message, then (a) APPEND to Drafts if configured, (b) send via SMTP only when
  `m` is marked approved. Return typed errors.
- Vault: `email-imap-password` or `email-oauth-refresh-token` (+ `email-oauth-client-secret` if confidential).
- Provider variants later: `gmail` (history.list cursor, label as handled-state) and `graph` (delta cursor, category) are
  separate backends behind one internal `mailBackend` interface; **no change to `Source`**.

### C2. WhatsApp adapter (`internal/source/whatsapp`)

- `Name()` = `"whatsapp"`. Two modes: `direct` (adapter runs an `http.Server` on loopback behind a tunnel) and
  `relay` (adapter pulls signed batches from a relay queue; the relay already verified the signature but the adapter
  re-verifies when the relay forwards the raw body and header).
- `Run`: serve GET (verify-token handshake) and POST. POST handler: read raw body (size cap), verify
  `X-Hub-Signature-256` with the app secret using `hmac.Equal`, reject otherwise with 401, return 200 immediately,
  parse, and for each `messages[]` entry call `emit(Event{ID:"wa:"+wamid, Kind:"message", Sender:{"whatsapp",
  "+E164", profile name}, Verified:true (signature ok, number from Meta's `wa_id`), Thread:{"whatsapp", wa_id}, Body: text,
  Attrs:{type, media_id, timestamp, phone_number_id}})`. Non-text types emit metadata only. Verified here means the
  payload came from Meta; the sender number is Meta-asserted, not cryptographic proof of the person.
- `Reply(t, m)`: check window from `m.Attrs`/state (last inbound timestamp supplied by the router from the tracking issue); inside
  window POST a text message; outside return `ErrWindowClosed` unless `m` carries a template name+params. Only invoked after
  operator approval.
- Vault: `whatsapp-token`, `whatsapp-app-secret`, `whatsapp-verify-token`.

### C3. Interface gaps and recommended changes

1. **Mark-handled hook (real gap).** design.md s6 says "each source also marks messages handled on its own side" but
   `Source` has no call that happens after the tracking issue is created. Emitting then marking inside `Run` risks marking
   before the issue exists. Zero-change option: add an **optional** interface discovered by type assertion, so `Source` stays as is:
   ```go
   type Acker interface { Ack(ctx context.Context, e Event) error } // called by router after tracking issue exists
   ```
   GitHub source need not implement it. WhatsApp's `Ack` can optionally send a read receipt/typing indicator (VERIFIED that
   Meta supports marking messages read: UNVERIFIED path). INFERRED.
2. **Draft vs send in `Reply`.** `Message` (type not defined in design.md) needs `Approved bool` or the router must only call
   `Reply` after approval. Prefer the second: core guarantees `Reply` is send-only. The optional mailbox draft is then an adapter
   side effect, or a separate optional `Drafter` interface. No change to `Source`.
3. **`Message` shape.** Needs `Text`, optional `Template{Name, Params}`, and `Attrs map[string]string`. This is a
   core-type definition, not an interface change.
4. **Event.Attrs usage** for auth verdicts and attachment metadata: already supported.
5. **Typed errors** (`ErrWindowClosed`, `ErrAuthExpired`, `ErrNotFound`) belong in `core`. Not an interface change.

Conclusion: `Source` as written (3 methods) is sufficient; add only the optional `Acker`. Goal of "no interface change"
is met in the strict sense. INFERRED (design analysis).

---

## Design impact (recommended edits; files not modified)

design.md:
1. **s4.2**: add the optional `Acker` interface under `Source`, and define `Message` (Text, optional Template, Attrs) and
   the typed errors. State that the router calls `Reply` only after approval.
2. **s6, "Non-GitHub sources"**: specify the order: emit -> tracking issue created -> `Ack` -> mark handled; state that
   Message-ID (email) or `wamid` (WhatsApp) goes in a machine-readable field of the issue body (e.g. `mint-source-id:`) and
   dedupe is a search for it. Add that WhatsApp cannot be reconciled after downtime, so a buffering relay is advised.
3. **s8 credentials list**: replace `imap-password` with `email-imap-password`, add `email-oauth-refresh-token`,
   `email-oauth-client-secret`, `whatsapp-app-secret`, `whatsapp-verify-token`; note refresh-token rotation requires atomic
   re-encryption of the vault file while the daemon is unlocked (so the vault needs a write path at runtime, not just unlock).
4. **s9 Verification**: replace "SPF/DKIM alignment" with: accept only `dmarc=pass` (aligned) in an
   `Authentication-Results` header whose `authserv-id` matches the configured trusted id and that is positioned in the
   provider-added block; ignore ARC; map identity on the `From` address-spec only. Add `trusted_authserv` to email source config.
5. **s9**: add an explicit sentence that WhatsApp `Verified` means "delivered by Meta", sender number is Meta-asserted,
   and customer replies outside the 24-hour window need an approved template.
6. **s12 Security model / s15 Open questions**: add attachment policy (metadata only, never auto-open), body as untrusted
   data, rate caps on tracking-issue creation per sender; add open question on Gmail restricted-scope verification and
   WhatsApp business verification and AI-provider terms.
7. **s14 Windows**: a webhook source needs a loopback listener plus a tunnel or relay; document `cloudflared` as a Windows
   service as the default and the cloud relay as the resilient option.

Plan (research-spikes.md / implementation plan):
- Mark S9 as answered, with three follow-ups: (a) re-fetch blocked primary docs when hosts are allowed (list under
  Method), (b) hands-on test of IDLE + UIDVALIDITY behaviour against the operator's real provider, (c) read Meta's current
  Business Solution Terms and pricing rate card.
- Add milestone "email source (IMAP, read-only)" before "email replies", and a separate milestone "WhatsApp relay".
- Pin go-imap/v2 to an exact beta tag and add an internal wrapper; add a conformance test fixture set of raw `.eml` files
  with forged and genuine A-R headers.

## Recommended first source after GitHub: Email over IMAP

Reasoning:
1. **No public endpoint.** IMAP IDLE is outbound-only, so it works on a Windows machine behind NAT with no tunnel, no relay and no
   new attack surface. WhatsApp requires one (VERIFIED requirement for webhooks).
2. **No downtime loss.** A mailbox is a durable queue with a cursor and a handled-folder; missed messages are picked up on
   restart. WhatsApp webhook events are not replayable (INFERRED), forcing a relay component.
3. **Lowest onboarding friction.** No Meta business setup, number registration, template approval or policy review
   (the AI-provider terms are unresolved: UNVERIFIED). Cost: none beyond the existing mailbox.
4. **Provider-neutral.** Plain IMAP avoids Google's restricted-scope verification (VERIFIED as a burden for `gmail.modify`)
   and the Graph app registration, while Microsoft 365 and Gmail both still allow IMAP with OAuth/app passwords
   (Microsoft OAuth requirement VERIFIED; Gmail app-password status UNVERIFIED). If the operator's mailbox is Gmail or M365
   and IMAP is unavailable, fall back to a second backend (Graph delta polling preferred over Gmail API for a
   single-user tool because it avoids the CASA-style review).
5. **Exercises every new concept at lower risk**: verified sender via A-R, thread keys, tracking issue + Message-ID dedupe,
   handled-state, approval-gated reply. Reuse then carries to WhatsApp.
6. **Caveats**: go-imap v2 is still beta (pin it), A-R parsing must follow the trust rules in A3, and OAuth refresh token
   rotation forces a runtime vault write path.

Start read-only (no SMTP, no `mail.send`), prove the tracking-issue loop, then enable approved replies.
