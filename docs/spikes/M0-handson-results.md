# M0 hands-on results (2026-11-25)

Run by the implementing session on the operator's Windows 11 machine. Copilot CLI **1.0.115**, Docker Desktop with
engine 29.5.3 (Linux containers, cgroup v2). Real model calls were made with the operator's own Copilot login; no
secret was printed or stored. Scratch files lived under the session scratchpad.

Labels: **[V]** observed in this run, **[U]** still unverified.

## Copilot CLI (S1/S2, tests T1 to T7)

| Test | Result |
|---|---|
| T1 headless turn | **[V]** `-p ... -s --no-ask-user --no-auto-update --session-id <uuid> --output-format json` exits 0. Output is JSONL: `session.*`, `assistant.*`, `tool.execution_*`, `model.*` events, ending with one `{"type":"result","sessionId","exitCode","usage":{"premiumRequests",...,"codeChanges":{...}}}`. The `result` event is the completion marker and carries the exit code. |
| T2 session dir | **[V]** `$COPILOT_HOME/session-state/<id>/` holds `events.jsonl`, `workspace.yaml`, `checkpoints/`, `files/`. The directory name is the id Mint passed in `--session-id`, and the `result` event echoes it. |
| T3 resume | **[V]** `--resume=<id>` works headless; the model remembered the file from turn one. **`--session-id <existing id>` also resumes** (same session id in `result`, no error). The design's "mutually exclusive per turn" rule is therefore conservative, not required; keep it. |
| T4 resume failures | **[V]** unknown id: exit 1, stderr `Error: No session, task, or name matched '<id>'`. Bare `--resume`: exit 1, "bare --resume needs an interactive session picker". **`--continue` works headless** (resumes the most recent session, exit 0), which is why Mint refuses it. Both existing launch-arg guards are confirmed. |
| T5 permissions | **[V]** A command Copilot classes as read-only (`echo`) runs with no allow rule. A write or non-read-only shell command with no allow rule is denied: the tool event has `success:false` and `error.code:"denied"`, **but the process still exits 0**. Mint must read denial from events, not from the exit code. **Deny rules win over `--yolo` and `--allow-all-tools`** (confirmed twice, and stated in `copilot --help`). |
| T6 auth | **[V]** `COPILOT_HOME` does **not** isolate credentials on Windows: a fresh empty home with no token, and a deliberately invalid `COPILOT_GITHUB_TOKEN`, both ran successfully, because the CLI falls back to the operator's login in Windows Credential Manager (`...copilot-cli` entries). **[U]** the same test in a container, where no login exists. |
| T7 sandbox | **[V]** `settings.json` with `sandbox.enabled` and `userPolicy.network.allowedHosts` plus `--sandbox` works headless on Windows without a TTY: `curl https://example.com` failed with `CONNECT tunnel failed, response 403` and the tool result says `<sandbox is active and blocked this command's network access>`. Allow-rule matching for PowerShell is by command name as typed, so `shell(curl.exe:*)` did not match; the CLI's help says to write the name as typed. `copilot config --list --json` printed `{}`, so it is not a source of key names. |
| `--help` flags | **[V]** present: `--session-id`, `--resume[=]`, `--continue`, `--sync-session[=on\|off]`, `--max-ai-credits`, `--output-format text\|json`, `--usage-output-file`, `--secret-env-vars`, `--yolo`, `--allow-all`, `--sandbox`, `--deny-tool`. |
| T9 usage | **[V]** `--usage-output-file <path>` writes final usage JSON (`totalPremiumRequestCost`, `totalNanoAiu`, `aiCreditsStatus`, token counts, `codeChanges`). The `result` event also has `usage.premiumRequests`. This is the budget source for M4. |
| T8, T10, T11 | **[U]** not run (proxy and CA, ACP, parallel stability). They gate M5 and the ACP variant, not M4. |

### Decisions

1. `ParseResult` (M4) reads the final `result` event for session id, exit code and usage, and scans
   `tool.execution_complete` events for `error.code == "denied"` to report refused actions. The exit code alone is not
   an outcome.
2. Keep the `--continue` and bare `--resume` refusal. Keep `--session-id` and `--resume` mutually exclusive per turn.
3. **Credential isolation on the host is not real.** A host-sandbox run can use the operator's own Copilot login no
   matter which token Mint supplies. Host runs are operator-trusted. The "scoped credential" guarantee holds only in
   the container runtime, and the M4 preflight must say so. Add a container test (M5) that an invalid token fails
   there.
4. Yolo default: derive `--yolo` from `runs_on: container` in the adapter, never for host runs. The deny list still
   applies under yolo, so the protected-path and `shell(git push)` denies stay effective. Per-agent `args` remains the
   only override.
5. `--max-ai-credits` minimum of 30 stays until a run shows what the CLI enforces **[U]**.

## Docker Desktop (S3 checklist)

| Check | Result |
|---|---|
| `--cap-drop=ALL --security-opt=no-new-privileges --read-only --pids-limit --memory --tmpfs /tmp:noexec` | **[V]** honoured: `CapEff` all zero, `NoNewPrivs: 1`, root filesystem read-only, executing a file from the `noexec` tmpfs fails with `Permission denied`. |
| `--internal` network, direct egress | **[V]** `wget http://1.1.1.1` fails with `Network unreachable`; DNS resolves nothing. |
| Sidecar topology | **[V]** a container connected to both a normal network and the internal network is reachable by name from an internal-only container (ping by container name succeeds). The "internal network plus Mint sidecar" design works on Docker Desktop. |
| Normal bridge egress | **[V]** reaches the internet, so the internal network is what blocks it, not a default. |
| Label based cleanup | **[V]** `docker ps --filter label=mint.run=<key>` finds the container; `docker kill` and `docker rm -f` clean it up. |
| Engine start | **[V]** Docker Desktop was not running at the start; `Start-Process` brought the engine up in about ten seconds. The M5 runner must detect a stopped engine and report it, not fail obscurely. |
| Bind mount speed, image build, proxy with real traffic, CA trust | **[U]** not run. |

## GitHub App (S5/S6/S8, tests T1 to T17)

**[U]** not run. They need a GitHub App created and installed by the operator (browser, private key download), which a
session cannot do. The tests do not block M4 (it uses a PAT per the plan); they block M6.

## Terms

**[U]** The unattended-use read of the Copilot terms still needs a human.
