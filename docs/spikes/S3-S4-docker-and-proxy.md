# Spikes S3 + S4: Docker from Go, egress control, injecting proxy

Date: 2026-11-24. Scope: documentation research plus small local experiments. No Windows or Docker Desktop was
available. The Linux container had a docker CLI (29.9.3) but no daemon, so no container ran.

Confidence labels: **VERIFIED** (read in a source or reproduced locally), **INFERRED** (reasoned from verified facts),
**UNVERIFIED-needs-Windows-test** (could not confirm; must be tested on Windows + Docker Desktop).

Source note: `docs.docker.com` did not resolve from this session's network (blocked). Docker docs facts below come
from search-result snippets of docs.docker.com pages, not full page reads; they are marked accordingly. Re-fetch those
pages when the host is allowed.

---

## Part A. S3: Docker from Go on Windows

### A1. CLI via os/exec versus the Go SDK

| Point | Answer | Source | Confidence |
|---|---|---|---|
| Module status | `github.com/docker/docker` is deprecated as of Docker v29 (Nov 2025). Replacements: `github.com/moby/moby/client` and `github.com/moby/moby/api`. `moby/moby` itself is not a supported import. | moby README mirror (search result): https://cos.googlesource.com/third_party/docker/+show/refs/heads/cos/master/README.md ; https://github.com/moby/moby | VERIFIED (via mirror; read the upstream README before pinning) |
| Version in use | `client` is v0.6.3 (published 2026-11-13), Apache-2.0, api module v1.57.0. Pre-1.0 means no stability promise; pkg.go.dev says stable means v1. v29 renamed constructors and moved types. | https://pkg.go.dev/github.com/moby/moby/client ; `go list -m` in /tmp/claude-0/s3 | VERIFIED |
| Constructor | `client.New(client.FromEnv)`; the old `NewClientWithOpts` is deprecated. API version negotiation on by default. | pkg.go.dev page above | VERIFIED |
| Windows named pipe | Supported. Source has `client_windows.go` with `DefaultDockerHost = "npipe:////./pipe/docker_engine"`, and an `npipe` case in the transport. Windows build uses `github.com/Microsoft/go-winio`. | module source v0.6.3 (local `go get`) | VERIFIED |
| Dependency weight | A client-only program (New + Ping) pulls 78 non-stdlib packages in `go list -deps`, including OpenTelemetry (otel, otelhttp), go-winio, containerd/errdefs, distribution/reference, go-connections, opencontainers. Binary: 10.2 MB linux/amd64, 10.5 MB windows/amd64, versus 2.2 MB hello-world. | experiment below | VERIFIED |
| Local experiment | `cd /tmp/claude-0/s3 && GOFLAGS=-mod=mod go get github.com/moby/moby/client@latest && GOFLAGS=-mod=mod go build -o s3bin . && GOOS=windows GOARCH=amd64 GOFLAGS=-mod=mod go build -o s3.exe .` builds both; running `s3bin` without a daemon reports "failed to connect to the docker API at unix:///var/run/docker.sock". | local | VERIFIED |
| CLI via os/exec | The CLI is the user-facing contract and the CLI flag set is stable. It resolves the Docker context (Desktop, Podman, remote) and the named pipe for free. Costs: process spawn per call, text output parsing (`--format '{{json .}}'` helps), exit-code-only errors, no streaming events except `docker events`. | general knowledge | INFERRED |
| Podman | Podman exposes a Docker-compatible API; on Windows the Docker-API pipe defaults to `npipe:////./pipe/docker_engine`, or machine pipe `npipe:////./pipe/podman-machine-default`; Podman Desktop says to use `DOCKER_HOST` on Windows. Rootful machine (`podman machine set --rootful`) is suggested for some Docker clients. Internal-network, seccomp and `no-new-privileges` parity under Podman is not documented in what I found. | https://sources.debian.org/src/podman/5.7.0%2Bds2-3/docs/tutorials/podman-for-windows.md ; https://podman-desktop.io/docs/migrating-from-docker/managing-docker-compatibility | VERIFIED for pipe/DOCKER_HOST; UNVERIFIED-needs-Windows-test for networking and hardening parity |

**Assessment.** The SDK is attractive for event streams, log attach, and typed errors, but it is v0.x, was
just reorganised (breaking), and adds about 8 MB and an OpenTelemetry dependency tree. The Mint needs are small:
build, create/start/kill/rm, inspect, logs, label-filtered list, network and volume create/rm. All have
stable CLI equivalents. **Recommendation: start with a thin `Docker` interface in `runtime/docker` implemented by
`os/exec` calling the `docker` CLI**, with JSON `--format` output, and keep the interface narrow enough to swap in
`moby/moby/client` later (INFERRED; decision is a judgment call, not a doc fact). Revisit when `client` reaches v1.

### A2. WSL2 path translation and bind mounts versus volumes

| Point | Answer | Source | Confidence |
|---|---|---|---|
| Bind-mount performance | Docker's WSL 2 best practices say files bind-mounted from the Linux filesystem perform much better than files from the Windows host (`/mnt/c`), and advise against `-v /mnt/c/...`. Windows file sharing crosses an OS boundary (9P). | https://docs.docker.com/desktop/features/wsl/best-practices/ (search snippet); https://www.docker.com/blog/docker-desktop-wsl-2-best-practices/ | VERIFIED (snippet) |
| Named volumes | Named volumes live in Docker Desktop's own Linux data store (ext4 in the VM), not on the Windows filesystem, so they avoid the 9P penalty. Docker's blog says named volumes are fast. | https://www.docker.com/blog/file-sharing-with-docker-desktop/ (snippet) | VERIFIED (snippet) |
| Clone into a volume | Workspace as a named volume per run, populated by a clone inside a container (or a helper `docker run` that mounts the volume), avoids all Windows path translation. Reading results: `git` commands in the container or `docker cp`. | design 7.1 already says volume | INFERRED |
| Path translation | With Docker Desktop and a Windows `docker.exe`, `-v C:\path:/x` is translated by Docker Desktop; MSYS/Git-bash may mangle `/` paths (`MSYS_NO_PATHCONV=1`). Avoiding host bind mounts except the agent home removes the issue. | general knowledge | UNVERIFIED-needs-Windows-test |
| Agent home mount | A small per-run agent home (Copilot state) bind-mounted from Windows is acceptable for performance but should be tested; alternatively use a second named volume and `docker cp` in/out. | | UNVERIFIED-needs-Windows-test |

### A3. Hardening flags on Docker Desktop (WSL2 backend)

Docker Desktop runs the engine in a Linux VM (a WSL2 distro `docker-desktop`), so the Linux engine flags are
processed by the same runc code path. No Docker doc found says Desktop disables any of them.

| Flag | Expected behaviour | Source | Confidence |
|---|---|---|---|
| `--cap-drop=ALL` | Supported by the engine (`--cap-add/--cap-drop` accept `ALL`). | Docker seccomp/security docs (search snippet) https://docs.docker.com/engine/security/seccomp/ | VERIFIED for engine; INFERRED for Desktop |
| `--security-opt no-new-privileges` | Engine feature, blocks setuid/setgid escalation. | same | VERIFIED engine; INFERRED Desktop |
| `--read-only` + `--tmpfs` | Engine feature. | same | VERIFIED engine; INFERRED Desktop |
| `--pids-limit`, `--memory`, `--cpus` | cgroup limits inside the Desktop VM. WSL2 VM has its own global limit via `.wslconfig`; per-container limits still apply within it. | | INFERRED; UNVERIFIED-needs-Windows-test (check `docker inspect` HostConfig and `cat /sys/fs/cgroup/pids.max` inside) |
| Default seccomp | Engine applies the default allowlist profile unless disabled. | https://docs.docker.com/engine/security/seccomp/ (snippet) | VERIFIED engine; INFERRED Desktop (test `grep Seccomp /proc/self/status` = 2 inside container) |
| User namespaces | Not used by default. Running as non-root `--user` is enough for the model. | | INFERRED |

Test inside the container: `grep -E 'Cap(Prm|Eff|Bnd)|NoNewPrivs|Seccomp' /proc/self/status` must show zero caps,
`NoNewPrivs: 1`, `Seccomp: 2`; `touch /x` must fail; `cat /sys/fs/cgroup/pids.max` shows the limit.

### A4. Enhanced Container Isolation (ECI)

| Point | Answer | Source | Confidence |
|---|---|---|---|
| Availability | Docker Business subscription feature, enforced via Settings Management (`admin-settings.json`). Not usable for a personal/Free setup. | https://docs.docker.com/desktop/setup/install/windows-permission-requirements/ (snippet); https://docs.docker.com/desktop/enterprise/hardened-desktop/settings-management/ (snippet) | VERIFIED (snippet) |
| Windows backends | Supported on WSL 2 (since Docker Desktop 4.20) and Hyper-V; Hyper-V gives stronger isolation because WSL 2 distros share one Linux kernel. Not available for Windows containers mode. | https://docs.docker.com/enterprise/security/hardened-desktop/enhanced-container-isolation/limitations/ (snippet); https://www.docker.com/blog/docker-desktop-4-36/ | VERIFIED (snippet) |
| Effect | Runs containers in a user-namespaced runtime (Sysbox) and blocks changing VM-level proxy/network config from containers. Limitation: some options such as `docker build --network=host` do not work. | same | VERIFIED (snippet) |
| Mint stance | Optional hardening, not a dependency. Document it as recommended where a Business licence exists. | | INFERRED |

### A5. Networking: `--internal`, host reachability, sidecar topology

| Point | Answer | Source | Confidence |
|---|---|---|---|
| `--internal` semantics | `docker network create --internal` "restricts external access to the network": no default route/NAT to the outside; containers on it talk to each other (and DNS by container name) only. | https://docs.docker.com/engine/network/drivers/bridge/ ; docker network create reference (search snippets) | VERIFIED (snippet) |
| What internal blocks on Desktop | Not documented. On Linux Engine the mechanism is iptables rules that drop traffic leaving the bridge. Docker Desktop's VM and vpnkit/host proxy layer sits behind the engine, so engine rules still apply to container traffic before it reaches vpnkit. | https://www.docker.com/blog/how-docker-desktop-networking-works-under-the-hood/ (read: outbound container traffic goes via vpnkit to the host) | INFERRED |
| `host.docker.internal` from an internal network | Not documented anywhere I found. It resolves to the host via the `docker.internal` DNS zone served by Desktop (blog above). Whether an `--internal` container can reach that IP (it is outside the bridge subnet) is not answered by docs. | https://www.docker.com/blog/how-docker-desktop-networking-works-under-the-hood/ | **UNVERIFIED-needs-Windows-test** (critical) |
| Sidecar topology | Standard Docker pattern: proxy container attached to two networks (`docker network connect`), one internal (`mint-run-<id>`, agent + proxy) and one normal bridge (`mint-egress`, proxy only). The agent container sits only on the internal network and reaches the proxy by container name. | Docker networking docs (multi-network containers) | INFERRED from engine semantics; UNVERIFIED-needs-Windows-test on Desktop |
| Can the agent bypass the proxy? | On this topology the agent has no route outside the internal network, so non-proxy TCP, UDP and DNS to the internet fail, regardless of whether the tool honours `HTTPS_PROXY`. Bypass requires reaching something that is on a non-internal network: the proxy itself (so the proxy must only forward allowlisted hosts and refuse connections to private/host addresses), published ports, or the Docker socket (never mounted). IPv6 and the Desktop DNS forwarder (container DNS to 127.0.0.11 forwards to host resolvers) are potential side channels: an internal network's embedded DNS is not documented to refuse external names. | | INFERRED; DNS side channel UNVERIFIED-needs-Windows-test |
| Proxy-env-only designs | Setting `HTTPS_PROXY` alone does not enforce anything: processes can ignore it (raw sockets, `curl --noproxy '*'`, tools without proxy support). It is a convenience, not a boundary. | | VERIFIED by definition |
| Host-proxy variant | Proxy on the Windows host reached via `host.docker.internal` requires the container to have a route out to the host; a normal bridge also gives internet access, so enforcement needs an additional firewall layer (iptables inside the VM, or Windows Firewall, which does not see per-container identity). Not enforceable with Docker Desktop alone. | | INFERRED |
| Docker Desktop proxy settings | Desktop's own proxy settings affect host-level traffic and image pulls; container-level proxy is separate. Not relevant to enforcement. | https://docs.docker.com/desktop/settings-and-maintenance/settings/ (snippet) | VERIFIED (snippet) |

**Conclusion**: the sidecar design is the only one that can enforce egress with stock Docker Desktop. Whether the
`--internal` network itself blocks access to the host gateway and to external DNS is exactly what must be tested.

### A6. Image build options

| Point | Answer | Source | Confidence |
|---|---|---|---|
| Secrets | BuildKit `docker build --secret id=x,src=file` plus `RUN --mount=type=secret,id=x` mounts at `/run/secrets/x`; not baked into layers. Mint passes none by design (design 7.1). | https://docs.docker.com/build/building/secrets/ (snippet) | VERIFIED (snippet) |
| `--pull` | Always attempt to pull newer base image. Standard CLI flag. | docker build reference | VERIFIED (well-known); not fetched |
| Build network | `docker build --network=none` is an engine/BuildKit option, but `RUN apt-get`/`npm install` need network, so it applies only to fully vendored builds. Under ECI, `--network=host` does not work. A build cannot be put on Mint's internal network with the default builder; custom BuildKit builder with network config would be required. | https://docs.docker.com/enterprise/security/hardened-desktop/enhanced-container-isolation/limitations/ (snippet) | VERIFIED for ECI limitation; INFERRED otherwise |
| Implication | Builds run with ordinary internet access via the default builder and with repo-controlled `RUN` steps. This is a hash-approved, default-branch-only step (design 7.1/12). Builds are a separate trust surface from runs: no credentials, but they can exfiltrate nothing sensitive because none exist in the build context. | | INFERRED |
| Image digest pinning | `FROM image@sha256:...` standard. | | VERIFIED (well-known) |

### A7. Cleanup and orphans

Label every object `mint.run=<key>` (`--label`), and `docker ps -a --filter label=mint.run`, `docker network ls --filter
label=...`, `docker volume ls --filter label=...` find leaks. Kill on cancel with `docker kill` then `docker rm -f`;
wall-clock limit enforced by Mint, not by the engine. INFERRED (standard CLI features; filters by label are
well-documented). UNVERIFIED-needs-Windows-test only for behaviour when Docker Desktop restarts mid-run.

---

## Part B. S4: Injecting TLS proxy

### B1. Building it in Go

| Point | Answer | Source | Confidence |
|---|---|---|---|
| Stdlib feasibility | Yes. Accept a plain TCP listener, read the `CONNECT host:443` request, check the allowlist, reply `200`, wrap the connection with `tls.Server` using a leaf certificate minted on the fly from the per-session CA, then loop `http.ReadRequest`, rewrite headers, forward over a pooled `http.Transport`, and stream the response back. | prototype below | VERIFIED |
| Prototype result | ~100 lines in `/tmp/claude-0/s4/main.go` (not in the repo). curl, git, gh and node all completed requests through it, saw the per-session CA, and the upstream saw only the injected `Authorization`. | prototype below | VERIFIED |
| HTTP/2 | Advertise only `http/1.1` in `tls.Config.NextProtos` on the MITM side; clients fall back to HTTP/1.1 (all four tools did). HTTP/2 MITM is possible (`http2.Server.ServeConn` from `golang.org/x/net/http2`) but unnecessary unless an agent endpoint requires h2 (gRPC would). Upstream leg can still use h2 via the standard transport. | prototype | VERIFIED (h1 fallback); INFERRED (x/net/http2 option) |
| Streaming / SSE | HTTP/1.1 chunked streaming works if the proxy copies the body with flushing (`io.Copy` plus `Flusher`); do not buffer. WebSocket needs `Upgrade` handling (hijack both sides). Copilot's model endpoint (HTTP streaming) needs testing. | | INFERRED; UNVERIFIED-needs-Windows-test (real Copilot traffic) |
| Per-session CA | Generate an ECDSA P-256 CA with `IsCA`, `KeyUsageCertSign`, 1-2h lifetime, key in memory only; write the public cert into the container. Mint leaf certs with `DNSNames` and short life; cache by host. Name constraints (`PermittedDNSDomains` on the CA) limit damage if the CA key leaks; Go and OpenSSL honour them, but support in other trust stacks varies. | prototype; x509 docs | VERIFIED (generation); INFERRED (name constraints) |
| Libraries | See table B5. | | |

Prototype commands (scratch only):

```
cd /tmp/claude-0/s4 && go build -o proxy . && ./proxy &        # listens 127.0.0.1:18080, writes ca.pem
curl -sS --cacert ca.pem -x http://127.0.0.1:18080 https://api.github.com/user -H 'Authorization: Bearer PLACEHOLDER'
HTTPS_PROXY=http://127.0.0.1:18080 GIT_SSL_CAINFO=$PWD/ca.pem git ls-remote https://github.com/o/r.git
HTTPS_PROXY=http://127.0.0.1:18080 SSL_CERT_FILE=$PWD/ca.pem GH_TOKEN=ghs_PLACEHOLDER GH_HOST=github.com gh api user
NODE_USE_ENV_PROXY=1 HTTPS_PROXY=http://127.0.0.1:18080 NODE_EXTRA_CA_CERTS=$PWD/ca.pem node -e "fetch('https://api.github.com/user').then(r=>r.text()).then(console.log)"
curl -sS --cacert ca.pem -x http://127.0.0.1:18080 https://evil.example/        # -> CONNECT tunnel failed, response 403
```

Observed (git 2.43.0, gh 2.89.0, node 22.23.5, curl on Linux): all four allowed requests succeeded; upstream log
showed `auth="Bearer REAL-INJECTED"` in every case while the clients sent `Bearer PLACEHOLDER` /
`token ghs_PLACEHOLDER` or nothing (git); the non-allowed host was refused with 403 at CONNECT; curl without the CA
failed with exit 60. The upstream was a local fake over plain HTTP, so TLS to the real GitHub, GitHub's real
auth acceptance and rate limits were not exercised.

### B2. How each client picks up CA and proxy

| Client | CA | Proxy | Notes | Source | Confidence |
|---|---|---|---|---|---|
| git (libcurl) | `http.sslCAInfo` or `GIT_SSL_CAINFO` | `http.proxy` or `https_proxy`/`HTTPS_PROXY`/`all_proxy` | `http.proxySSLCAInfo` is only for an https:// proxy. Both mechanisms reproduced. | https://repo.or.cz/git.git/blob/HEAD:/Documentation/config/http.txt ; prototype | VERIFIED |
| gh (Go) | Go honours `SSL_CERT_FILE` / `SSL_CERT_DIR` on Linux (the container). On macOS Go uses the system verifier and ignores them; not a problem for Linux containers. | `HTTPS_PROXY` (Go `ProxyFromEnvironment`) | Reproduced with `SSL_CERT_FILE` + `HTTPS_PROXY`. Alternative: add CA to system store via `update-ca-certificates` in the base image. | prototype; https://claudeissues.com/issue/26466-go-based-cli-tools-gh-terraform-etc-fail-with-tls-error-due-to-built-in-https-pr (macOS caveat, secondary) | VERIFIED on Linux |
| Node | `NODE_EXTRA_CA_CERTS` (PEM file; adds to bundled CAs) | `NODE_USE_ENV_PROXY=1` (or `--use-env-proxy`, Node 22.21+/24.5+; fetch 22.21+/24.0+) plus `HTTPS_PROXY`. Without it Node ignores proxy env. | Reproduced on Node 22.23. Emits an "EnvHttpProxyAgent is experimental" warning. | https://nodejs.org/en/learn/http/enterprise-network-configuration ; prototype | VERIFIED |
| curl | `--cacert`, `CURL_CA_BUNDLE`, `SSL_CERT_FILE` | `-x`, `HTTPS_PROXY` | Reproduced with `--cacert`. | prototype | VERIFIED |
| Copilot CLI | Reads OS trust store and `NODE_EXTRA_CA_CERTS`. | `HTTPS_PROXY`, `https_proxy`, `HTTP_PROXY`, `http_proxy` in that order (one URL for both schemes); `https://` proxy URLs not supported, so use `http://` for the sidecar. | Child processes (MCP servers) may not honour `NODE_EXTRA_CA_CERTS`; install the CA in the system store in the image too. | https://docs.github.com/en/copilot/concepts/network-settings (snippet) ; https://coder.com/docs/ai-coder/ai-gateway/clients/copilot | VERIFIED (docs); UNVERIFIED-needs-Windows-test (real CLI run) |

**Best practice**: in `mint-base`, install the session CA into the system store at container start (the CA is session
specific, so a start hook, or mount at `/usr/local/share/ca-certificates/` and run `update-ca-certificates` as a
non-root-capable step, or ship a writable bundle path) and also set `SSL_CERT_FILE`, `GIT_SSL_CAINFO`,
`NODE_EXTRA_CA_CERTS`, `CURL_CA_BUNDLE` to the same bundle file. With a read-only root, point the variables at a
tmpfs/volume path.

### B3. Do gh and git accept placeholder tokens?

| Point | Answer | Source | Confidence |
|---|---|---|---|
| gh | With `GH_TOKEN=ghs_PLACEHOLDER`, `gh api user` ran and sent `Authorization: token ghs_PLACEHOLDER`; no local token validation. The proxy replaced it. Use a plausible-looking prefix. `gh auth login --with-token` and `gh auth status` validate against the API, so they work through the proxy but should not be needed. | prototype | VERIFIED (gh 2.89.0, fake upstream) |
| gh scopes check | `gh` may warn/refuse for classic tokens lacking scopes (checks `X-OAuth-Scopes`); installation tokens (`ghs_`) have no such header. Keep placeholder shaped like `ghs_...`. | | INFERRED; UNVERIFIED-needs-Windows-test against real GitHub |
| git | git sends no `Authorization` on the first request; GitHub would answer 401 and git would retry with credentials from a helper. When the proxy injects on the first request, no 401 occurs and no credential is needed in the container at all (reproduced: `clientAuth=""`). | prototype | VERIFIED (mechanics); UNVERIFIED-needs-Windows-test (real GitHub) |
| Header form for git | GitHub documents installation tokens for git as `https://x-access-token:TOKEN@github.com/...`, i.e. HTTP Basic with username `x-access-token`. The proxy should inject `Authorization: Basic base64("x-access-token:" + token)` for `github.com` git paths and `Bearer`/`token` for `api.github.com`. Whether Bearer works for git smart HTTP is not documented. | https://docs.github.com/en/enterprise-cloud@latest/apps/creating-github-apps/authenticating-with-a-github-app/authenticating-as-a-github-app-installation | VERIFIED (Basic form); UNVERIFIED-needs-Windows-test (Bearer for git) |
| Strip rule | Proxy must overwrite, not append, `Authorization` and drop `Proxy-Authorization`, cookies for allowed hosts if any. Reproduced overwrite. | prototype | VERIFIED |

### B4. TLS interception risks

- **Certificate pinning**: GitHub tooling (git, gh, Copilot CLI) does not pin; all four honoured the injected CA.
  Pinning in the agent's own tools (some CLIs bundle Mozilla roots, e.g. Bun uses BoringSSL's compiled-in roots and
  ignores `NODE_EXTRA_CA_CERTS`/`SSL_CERT_FILE` per a secondary source) would fail. Test every agent CLI, and fail
  closed (the run reports a TLS error). Source: https://codex.danielvaughan.com/2026/05/08/codex-cli-tls-inspecting-proxies-custom-ca-certificates-enterprise/ (secondary). INFERRED.
- **Trust blast radius**: the session CA key lives only in the daemon; the container gets only the public cert. If the
  agent can write the CA into an alternate trust store that is harmless. The danger is the key leaking; keep it in
  memory, short TTL, name-constrained to the allowlist hosts. VERIFIED design logic; name constraint support INFERRED.
- **MITM sees all plaintext** to allowed hosts, including model prompts. Logs must redact bodies and headers.
- **Allowlist is not an exfil boundary**: an agent can push secrets to GitHub itself (any allowed host). Already in the
  residual risk list (design 12).
- **Proxy as SSRF pivot**: the proxy sits on both networks; it must refuse CONNECT to private, loopback, link-local
  and the Docker gateway/host addresses, and resolve DNS itself (pin the resolved IP, no rebinding). INFERRED.
- **Protocol coverage**: git over SSH, gRPC/h2-only, raw TCP (e.g. DBs) are not covered; the agent profile must use
  HTTPS remotes.
- **Version drift**: Node proxy-env behaviour depends on Node version (22.21+/24.5+); the base image pins it.

### B5. Options compared

| Option | What it is | Pros | Cons | Fit |
|---|---|---|---|---|
| 1. Stdlib MITM proxy (own code) | `net` + `crypto/tls` + `net/http` as in the prototype | No dependency; full control of allowlist, header strip, logging, redaction; ~few hundred lines; prototype worked with 4 clients | Must implement CONNECT, h1 framing, upgrades, streaming, cert cache, timeouts; h2 optional | Recommended base |
| 2. `elazarl/goproxy` | Library: handler with MITM CONNECT, per-host cert cache. BSD-3-Clause. Mature (10 yrs), volunteer-maintained. v1.9.0 added HTTP/2 MITM; a May 2026 release was listed. | Less glue code; websocket handling; request/response hooks | Third-party code on the credential path; history of CVE-style issues is not assessed here; release cadence about every 5 months | Good alternative if h2/websockets needed |
| 3. mitmproxy | Python tool, h1/h2/websocket; Python runtime and addons; licence not confirmed here (believed MIT; verify) | Feature-rich, battle-tested | Python in a Go daemon's deployment on Windows; extra process; harder to ship | Not recommended |
| 4. CONNECT-only allowlist proxy (no MITM) | Tunnels TLS, no injection; allowlist by SNI/hostname | No CA, no pinning risk, simplest, h2 and everything works | The container must hold real credentials for GitHub, breaking the "placeholder only" goal | Only for hosts that need no credential |
| 5. git credential helper over a socket + gh wrapper | Helper in container talks to the daemon over a mounted socket (or TCP on the internal net); daemon returns a token on demand | No TLS interception for GitHub; works with pinned clients | Token lives in process memory/env of git/gh in the container and is visible to the agent; defeats "no secret in the sandbox" (only mitigated by short TTL and one-repo scope); needs a socket mount | Fallback if MITM breaks |
| 6. Hybrid: CONNECT-only for most hosts, MITM only for `github.com`/`api.github.com` (and the Copilot host) | Per-host choice in `Policy.Credentials`: hosts with an injected credential get MITM; allowlist-only hosts get opaque tunnel | Limits interception to hosts that need injection; fewer pinning surprises; smaller blast radius | Two modes to test | **Recommended** |

### B6. Non-MITM alternative for GitHub

Placeholder tokens plus an allowlist-only CONNECT proxy cannot inject (the traffic is encrypted end to end), so the
non-MITM routes for GitHub are (a) option 5 (credential helper or `gh` wrapper that asks the daemon), or (b) scoped
token in the container with the run capped to 1 hour and one repository (this is what the design's two-token split
already limits). Injection only on plain-HTTP internal endpoints works for Mint-owned endpoints (e.g. a Mint broker
API on the internal network) but GitHub itself is HTTPS-only, so it does not apply to GitHub. INFERRED.

---

## Recommended architecture

Per run, Mint creates:

```
 Windows host
 ┌────────────────────────────────────────────────────────────────────────┐
 │ mint daemon (Go)  ── docker CLI (named pipe npipe:////./pipe/docker_engine)
 │      │ holds: vault, root creds, per-session CA key (memory only)       │
 │      ▼                                                                  │
 │ Docker Desktop (WSL2 VM)                                                │
 │                                                                         │
 │  net: mint-int-<run>  (--internal, label mint.run=<key>)                │
 │  ┌───────────────────────────┐        ┌───────────────────────────────┐ │
 │  │ agent container            │  HTTP  │ mint-proxy sidecar            │ │
 │  │ cap-drop ALL, read-only,   │ ─────► │ (Mint-owned image, same       │ │
 │  │ no-new-privileges, nonroot │ :3128  │  Go binary as daemon proxy)   │ │
 │  │ only placeholder tokens,   │        │ allowlist, CONNECT, MITM for  │ │
 │  │ session CA public cert,    │        │ injected hosts, inject creds  │ │
 │  │ HTTPS_PROXY=http://proxy:3128      │ via control channel from      │ │
 │  │ workspace = named volume   │        │ daemon (token per run)        │ │
 │  └───────────────────────────┘        └───────────────┬───────────────┘ │
 │                                                       │ second NIC      │
 │  net: mint-egress (normal bridge, proxy only) ◄───────┘                 │
 │                                                       │                 │
 └───────────────────────────────────────────────────────┼─────────────────┘
                                                         ▼ internet (github.com, api.github.com, Copilot hosts)
```

Proxy approach:

1. The proxy is Mint code (option 1, stdlib; option 2 `goproxy` kept as a fallback if h2/websockets prove necessary).
2. Hybrid mode (option 6): per host in `Policy.Credentials`, MITM and inject; all other allowlisted hosts are opaque
   CONNECT tunnels; everything else gets 403.
3. Run the proxy inside a sidecar container on both networks, so the agent has no route anywhere but the proxy. The
   session CA key and real tokens are delivered to the sidecar (stdin or a short-lived control connection from the
   daemon), never to the agent container or to disk.
4. Proxy hardening: refuse CONNECT/requests to private, loopback, link-local and Docker gateway addresses; pin DNS
   resolution; overwrite `Authorization`; redact logs; HTTP/1.1 only on the MITM leg to begin with; idle and total
   timeouts; per-run request log for audit.
5. Agent image: `mint-base` sets `HTTPS_PROXY`/`https_proxy`, `NO_PROXY` for the proxy host, `NODE_USE_ENV_PROXY=1`,
   and CA variables (`SSL_CERT_FILE`, `GIT_SSL_CAINFO`, `NODE_EXTRA_CA_CERTS`, `CURL_CA_BUNDLE`) to a path on tmpfs;
   git remote HTTPS only; `gh` uses `GH_TOKEN=ghs_` placeholder.
6. Docker access: `os/exec` driving the `docker` CLI behind a narrow interface (A1).

If the Windows test shows that `--internal` networks block neither the host gateway nor external DNS, add
mitigations: do not publish the sidecar to the host, run the egress NIC on a separate network, disable DNS on the
internal network via `--dns` pointing at the proxy's DNS stub or an unroutable address, and rely on the proxy being the
only listener.

## Design impact (recommended edits; not applied)

1. **design 7.1 Egress**: replace "likely needs" with the sidecar topology above, state that `HTTPS_PROXY` is a
   convenience and `--internal` is the boundary; add that the proxy sidecar must be reachable by container name
   and that the host-proxy variant is rejected (cannot be enforced on Docker Desktop).
2. **design 7.1 Docker access**: add "Mint drives the `docker` CLI via `os/exec` behind an interface; the Go SDK
   (`github.com/moby/moby/client`, v0.x, 78 packages, ~8 MB extra) is deferred". Note `github.com/docker/docker` is
   deprecated.
3. **design 7.1 Workspace**: say workspace is a named volume populated by an in-container clone; avoid `/mnt/c`
   bind mounts; agent home is the only host-path mount (and test that).
4. **design 7.3**: describe hybrid proxy mode (MITM only on injected hosts), session CA in memory with name
   constraints, CA delivered to the agent as public cert only, header rewrite rules (Basic for git on `github.com`,
   token/Bearer for `api.github.com`), placeholder shape `ghs_...`, SSRF protections, and Node proxy env
   (`NODE_USE_ENV_PROXY=1`) in the base image. Mention fallback option 5 for clients that pin.
5. **design 12 residual risks**: add the proxy as a trust and SSRF surface; add that `--internal` DNS/host behaviour
   is a to-verify item; add ECI (Business only) as optional hardening and the WSL2 shared-kernel caveat (Hyper-V
   backend is stronger).
6. **design 15 Open questions item 2**: narrow it to the Windows test list below.
7. **plan M5**: split task 5 into "sidecar and networks (`mint-int-<run>`, `mint-egress`)" and "egress test
   harness"; make `mint-base` own CA-install and proxy env; add a task for the docker CLI wrapper
   (interface, JSON output, label filters, orphan sweep); add acceptance tests listed below. S3 exit criteria fit,
   and the S4 exit criteria (git push, gh pr create with dummy token) remain valid but must run against real GitHub.
8. **research-spikes S3**: record the decision CLI vs SDK; S4: record the prototype outcome and the hybrid mode.

## Hands-on tests still required on Windows + Docker Desktop

Run on Windows 11 with Docker Desktop (WSL2 backend), and again with Hyper-V backend and Podman Desktop if supported.

Network enforcement (critical):
- [ ] `docker network create --internal t1`; `docker run --rm --network t1 curlimages/curl -m 5 https://example.com`
      fails; `... -m 5 https://1.1.1.1` fails (no IP route either); UDP and ICMP to 1.1.1.1 fail.
- [ ] On `t1`: `curl -m 5 http://host.docker.internal:<port>` against a host listener bound to `0.0.0.0`. Record
      reachable or not. Also try `gateway.docker.internal`, the bridge gateway IP, and `192.168.65.x` VM addresses.
- [ ] On `t1`: `nslookup example.com` and `nslookup example.com 8.8.8.8`. Does the embedded DNS (127.0.0.11) resolve
      external names? If yes, test DNS tunnelling exposure and try `--dns 0.0.0.0` or a blackhole.
- [ ] IPv6 egress on an internal network (`curl -6`).
- [ ] Sidecar: proxy container on `t1` and `mint-egress`; agent container on `t1` only; agent reaches
      `http://proxy:3128`; `curl --noproxy '*' https://example.com` from the agent fails; `curl -x` allowed host works.
- [ ] Bypass attempts from the agent: connect to the sidecar's other-network IP, to the proxy's listener on the
      egress NIC address, `docker.sock` absence, CONNECT to `host.docker.internal`, `127.0.0.1`, `169.254.169.254`,
      RFC1918 ranges through the proxy (must be refused).
- [ ] Does the proxy reach the internet and GitHub from `mint-egress` behind corporate VPN or proxies (vpnkit)?

Hardening flags:
- [ ] `docker run --cap-drop=ALL --security-opt no-new-privileges --read-only --tmpfs /tmp --pids-limit 256
      --memory 4g --cpus 2 --user 10001` then check `/proc/self/status` (`CapEff` zero, `NoNewPrivs 1`, `Seccomp 2`),
      writes to `/` fail, fork bomb capped, `cat /sys/fs/cgroup/pids.max`.
- [ ] `docker inspect` shows the flags in HostConfig.
- [ ] With ECI enabled (Business) the same run, and note differences.

Volumes and paths:
- [ ] Time `git clone --depth 1` of a mid-size repo plus `npm ci`/`go build` into (a) named volume, (b) bind mount
      of `C:\Users\...\ws`, (c) bind mount from `\\wsl$\...`. Record numbers.
- [ ] Agent home bind mount from Windows path: read/write works, file modes, case sensitivity, path form
      (`C:\...` versus `/mnt/c/...`) with `docker.exe` from PowerShell, cmd, and Git Bash (`MSYS_NO_PATHCONV`).
- [ ] Workspace extraction (`docker cp`, or in-container `git bundle/push`).

Docker driving:
- [ ] Go program with `os/exec docker` (and optionally `moby/moby/client`) works against `npipe:////./pipe/docker_engine`;
      build, run, kill on context cancel, label-based orphan sweep after killing the daemon process mid-run.
- [ ] Behaviour after Docker Desktop restart: do leftover containers/networks get swept?
- [ ] Podman Desktop with `DOCKER_HOST` set: same tests incl. internal network and hardening.
- [ ] `docker build --pull` of `.mint/Dockerfile` with no secrets and no host mounts; cache and time/size.

Proxy and clients against real GitHub (needs a throwaway repo and App installation token):
- [ ] `git clone`/`git push` over HTTPS through the MITM proxy with only the CA and no credentials in the
      container; check that GitHub accepts injected Basic `x-access-token:<token>`, and whether Bearer is accepted.
- [ ] `gh pr create`, `gh api`, `gh auth status` with `GH_TOKEN=ghs_PLACEHOLDER` (and with `ghp_`-shaped placeholder).
- [ ] Copilot CLI (`copilot -p ...`) with `HTTPS_PROXY=http://proxy:3128`, `NODE_EXTRA_CA_CERTS`, `COPILOT_GITHUB_TOKEN`
      placeholder: does it start, do model calls (streaming) and any websocket work, are its hosts listed, does it
      accept an `ghs_`/placeholder credential, what hosts does it contact (`copilot` telemetry, API, token exchange)?
- [ ] Node tools with `NODE_USE_ENV_PROXY=1` on the version in `mint-base`; Go tools; `pip`/`npm`/`go get` through the
      tunnel mode (CONNECT-only hosts).
- [ ] System CA install in `mint-base` with read-only root (tmpfs path) works for git, gh, node, curl, python.
- [ ] HTTP/2: confirm all required endpoints work with the proxy advertising only `http/1.1`.
- [ ] Negative test: request to a non-allowlisted host fails at CONNECT; agent-supplied `Authorization` is replaced;
      redaction check on proxy logs (grep for the real token after a run).
