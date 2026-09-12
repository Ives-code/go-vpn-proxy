# Dual HTTP Egress Gateway Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use beads-superpowers:subagent-driven-development (recommended) or beads-superpowers:executing-plans to implement this plan task-by-task. Each Task becomes a bead (`bd create -t task --parent <epic-id>`). Steps within tasks use checkbox (`- [x]`) syntax for human readability.

**Goal:** Build one Ubuntu-ready Go daemon with two authenticated HTTP proxy listeners that independently round-robin over a shared, subscription-fed, health-checked sing-box outbound pool and retry the remaining nodes within the same connection attempt.

**Architecture:** A subscription manager produces atomic source snapshots of sanitized `NodeSpec` values. A shared health registry owns node availability and sing-box outbound dialers; two HTTP proxy servers each own a round-robin cursor but use that registry for try-each-once failover. Existing tunnels retain their selected dialer while refresh removes the node only from new assignments.

**Tech Stack:** Go 1.26+, `github.com/sagernet/sing-box` v1.14.0, `gopkg.in/yaml.v3`, Go standard-library HTTP/CONNECT server, Docker, systemd, WSL Ubuntu 24.04.

## Global Constraints

- Work only under `D:/data/yc/dual-egress-gateway`; do not modify sibling files.
- Never commit or log real subscription URLs, node addresses, credentials, or subscription bodies.
- Both public listeners implement authenticated HTTP forward proxy and CONNECT; no SOCKS, TUN, TLS MITM, UDP, or Web UI.
- HTTP and WS listeners share health state but have independent round-robin cursors.
- A connection attempt tries every eligible node at most once; all-node failure returns a generic 502.
- Established CONNECT/WebSocket tunnels never migrate and are not terminated by subscription refresh.
- Checked-in examples default to loopback; LAN exposure is an explicit environment/deployment choice.
- All new logic follows a witnessed RED -> GREEN -> REFACTOR test cycle.
- `bd` is unavailable in this environment; the plan and Git commits are the execution record.

---

### Task 1: Safe project skeleton and configuration

**Files:**
- Create: `.gitignore`
- Create: `go.mod`
- Create: `cmd/dual-egress-gateway/main.go`
- Create: `internal/config/config.go`
- Create: `internal/config/config_test.go`
- Create: `config.example.yaml`
- Create: `.env.example`
- Create: `LICENSE`
- Create: `NOTICE`

**Interfaces:**
- Produces `config.Load(path string, lookupEnv func(string) (string, bool)) (config.Config, error)`.
- Produces `config.Config` with `HTTPListen`, `WSListen`, `AdminListen`, authentication, refresh/probe/dial/drain durations, response-size limit, and subscription URLs.
- Consumers receive secrets as `config.Secret` values whose `String` and `GoString` methods always return `[REDACTED]`.

**Acceptance Criteria:**
- Missing username, password, or subscriptions is rejected.
- Equal HTTP and WS addresses are rejected.
- Admin listener outside loopback is rejected.
- Real secrets never appear through formatting.
- Examples contain no real credentials or URLs and bind proxy listeners to loopback.

- [x] **Step 1: Write failing configuration tests**

```go
func TestLoadRejectsMissingSecrets(t *testing.T) { /* load valid YAML with empty env and require an error */ }
func TestLoadRejectsDuplicateListeners(t *testing.T) { /* use the same address and require an error */ }
func TestLoadRejectsNonLoopbackAdmin(t *testing.T) { /* set 0.0.0.0:19090 and require an error */ }
func TestSecretFormattingIsRedacted(t *testing.T) { /* assert fmt.Sprint and fmt.Sprintf("%#v") contain no secret */ }
```

- [x] **Step 2: Run RED**

Run: `go test ./internal/config -v`

Expected: compilation failure because `Load`, `Config`, and `Secret` do not exist.

- [x] **Step 3: Implement the minimum validated config loader and safe examples**

Implement exact defaults: loopback proxy ports `127.0.0.1:18080` and `127.0.0.1:18081`, admin `127.0.0.1:19090`, refresh `30m`, probe `30s`, dial timeout `10s`, shutdown drain `30s`, maximum subscription body `4 MiB`. Read `PROXY_USERNAME`, `PROXY_PASSWORD`, `SUBSCRIPTION_URLS`, and `ADMIN_TOKEN` from environment only.

- [x] **Step 4: Run GREEN and static checks**

Run: `go test ./internal/config -v && gofmt -w cmd/dual-egress-gateway/main.go internal/config/*.go && go vet ./...`

Expected: all config tests pass and vet exits 0.

- [x] **Step 5: Commit**

```bash
git add .gitignore go.mod cmd internal/config config.example.yaml .env.example LICENSE NOTICE
git commit -m "feat: add secure gateway configuration"
```

### Task 2: Subscription fetch, format detection, and atomic source snapshots

**Files:**
- Create: `internal/subscription/model.go`
- Create: `internal/subscription/fetch.go`
- Create: `internal/subscription/parse.go`
- Create: `internal/subscription/manager.go`
- Create: `internal/subscription/parse_test.go`
- Create: `internal/subscription/manager_test.go`
- Create: `internal/redact/redact.go`
- Create: `internal/redact/redact_test.go`
- Create: `testdata/subscriptions/singbox.json`
- Create: `testdata/subscriptions/clash.yaml`
- Create: `testdata/subscriptions/uris.txt`

**Interfaces:**
- Produces `subscription.NodeSpec{ID, Tag, Type string; Options json.RawMessage; SourceIDs []string}` with ID derived from canonical protocol options, never the display name.
- Produces `subscription.Fetcher.Fetch(ctx, source) ([]byte, Metadata, error)` with timeout, HTTPS-only default, response-size limit, and User-Agent negotiation.
- Produces `subscription.Parse(body []byte, formatHint string) ([]NodeSpec, error)` for sing-box JSON, Clash YAML, Base64 URI list, and plain URI list.
- Produces `subscription.Manager.Refresh(ctx) (subscription.Snapshot, error)` and `Snapshot()`; each source keeps last-known-good data independently.
- `redact.Text` removes URL query/userinfo, credential-shaped URI contents, IP/host node endpoints, and configured secret values.

**Acceptance Criteria:**
- Fixtures parse into stable IDs without exposing credentials.
- Duplicate canonical nodes across sources collapse while retaining both source IDs.
- A failed source refresh retains that source's last-known-good nodes.
- A successful empty response is rejected and cannot erase a source.
- Response bodies beyond 4 MiB fail before allocation grows unbounded.

- [x] **Step 1: Write parser, redaction, and source-isolation tests**

```go
func TestParseSingBoxJSON(t *testing.T) { /* assert protocol types and stable IDs */ }
func TestParseClashYAML(t *testing.T) { /* assert equivalent nodes canonicalize identically */ }
func TestParseURILists(t *testing.T) { /* cover plain and base64 forms */ }
func TestRefreshRetainsFailedSourceSnapshot(t *testing.T) { /* source 1 changes; source 2 returns 500 */ }
func TestRefreshRejectsSuccessfulEmptyPool(t *testing.T) { /* prior nodes remain */ }
func TestRedactionRemovesSecretsAndEndpoints(t *testing.T) { /* assert raw values absent */ }
func FuzzParse(f *testing.F) { /* seed all three fixture formats and assert no panic */ }
```

- [x] **Step 2: Run RED**

Run: `go test ./internal/subscription ./internal/redact -v`

Expected: compilation failure for missing parser, manager, and redactor.

- [x] **Step 3: Implement parsers and per-source transactional manager**

Prefer sing-box JSON requested with `User-Agent: sing-box`; retry format negotiation with `clash.meta` only when the response is not recognized. Port the minimum MIT-licensed conversion logic needed from Easy Proxies and record exact upstream file/commit in `NOTICE`.

- [x] **Step 4: Run GREEN, fuzz seeds, and formatting**

Run: `go test ./internal/subscription ./internal/redact -v && go test ./internal/subscription -run=Fuzz -fuzz=FuzzParse -fuzztime=10s && gofmt -w internal/subscription/*.go internal/redact/*.go`

Expected: all deterministic tests pass; the bounded fuzz run reports no panic.

- [x] **Step 5: Commit**

```bash
git add internal/subscription internal/redact testdata NOTICE
git commit -m "feat: add resilient subscription snapshots"
```

### Task 3: Shared health registry and independent round-robin selectors

**Files:**
- Create: `internal/pool/registry.go`
- Create: `internal/pool/selector.go`
- Create: `internal/pool/registry_test.go`
- Create: `internal/pool/selector_test.go`

**Interfaces:**
- Consumes `subscription.Snapshot`.
- Produces `pool.Registry.Apply(snapshot) pool.Diff`, `MarkFailure(id, error)`, `MarkSuccess(id, latency)`, `Candidates() []pool.NodeLease`, and `ProbeDue(now) []pool.NodeLease`.
- Produces `pool.Selector.Begin(registrySnapshot []pool.NodeLease) *pool.Attempt`; each selector has its own mutex-protected cursor.
- `Attempt.Next()` returns each eligible node once; `Attempt.Commit(id)` advances the parent cursor past the successful node.
- A `NodeLease` retains the runtime node generation until `Close`, allowing refresh drain.

**Acceptance Criteria:**
- Two selectors over one registry rotate independently.
- A failure through either selector immediately removes the node from both selectors' normal candidate lists.
- One attempt never returns the same node twice.
- Committing B after A failed causes the next connection to begin at C.
- Removed nodes remain leased for active connections and close after the final lease.
- When all nodes are unhealthy, one half-open attempt per node is allowed without a retry storm.

- [x] **Step 1: Write failing state-machine tests**

```go
func TestSelectorsHaveIndependentCursors(t *testing.T) { /* HTTP A,B while WS independently A,B */ }
func TestAttemptSkipsFailureAndCommitsAfterSuccess(t *testing.T) { /* A fails, B succeeds, next starts C */ }
func TestFailureIsSharedAcrossSelectors(t *testing.T) { /* HTTP failure removes node from WS candidates */ }
func TestRemovedNodeDrainsUntilLeaseClose(t *testing.T) { /* runtime closes only after active lease */ }
func TestAllDownHalfOpenIsBounded(t *testing.T) { /* at most one concurrent probe per node */ }
```

- [x] **Step 2: Run RED**

Run: `go test ./internal/pool -race -v`

Expected: compilation failure because registry and selectors do not exist.

- [x] **Step 3: Implement concurrency-safe registry, leases, and selectors**

Use ordinary mutexes for compound state transitions; do not use a lock-free cursor that can advance independently of success. Store only sanitized failure classes in status state.

- [x] **Step 4: Run GREEN repeatedly under race detector**

Run: `go test ./internal/pool -race -count=25`

Expected: 25 passing runs and no race reports.

- [x] **Step 5: Commit**

```bash
git add internal/pool
git commit -m "feat: add shared health and independent rotation"
```

### Task 4: sing-box outbound runtime and health probes

**Files:**
- Create: `internal/proxycore/engine.go`
- Create: `internal/proxycore/node.go`
- Create: `internal/proxycore/probe.go`
- Create: `internal/proxycore/engine_test.go`
- Create: `internal/proxycore/probe_test.go`

**Interfaces:**
- Produces `proxycore.Dialer` with `DialContext(ctx, network, address) (net.Conn, error)` and `Close() error`.
- Produces `proxycore.Factory.Build(ctx, subscription.NodeSpec) (proxycore.Dialer, error)`.
- Produces `proxycore.Prober.Probe(ctx, pool.NodeLease) (time.Duration, error)` using an end-to-end HTTPS 204 request through the node.
- Registry runtime generations own a `Dialer`; replacement/close follows lease lifetime.

**Acceptance Criteria:**
- Supported sing-box JSON outbounds build into working dialers without exposing options in errors.
- Unsupported/direct/block/group outbounds are rejected as non-node entries.
- Probe validates TLS and expected status; certificate validation cannot be disabled globally.
- Cancellation and timeouts close partial connections.
- Runtime replacement does not close an actively leased dialer.

- [x] **Step 1: Write failing fake-dialer and minimal real-runtime tests**

```go
func TestFactoryRejectsNonProxyOutbound(t *testing.T) { /* direct/block/urltest rejected */ }
func TestProbeRequiresExpectedStatus(t *testing.T) { /* 200 fails when 204 expected */ }
func TestProbeHonorsCancellation(t *testing.T) { /* stalled dial exits with context */ }
func TestLeasedRuntimeSurvivesReplacement(t *testing.T) { /* close happens after lease */ }
```

- [x] **Step 2: Run RED**

Run: `go test ./internal/proxycore -v`

Expected: compilation failure for missing factory and prober.

- [x] **Step 3: Implement the sing-box adapter using pinned public APIs**

Port only the outbound construction/registration needed from the attributed Easy Proxies integration. Wrap errors at the protocol/type level and pass every message through redaction before logging.

- [x] **Step 4: Run GREEN and dependency audit**

Run: `go test ./internal/proxycore -race -v && go vet ./... && govulncheck ./...`

Expected: tests and vet pass; any reachable vulnerability blocks completion and requires a compatible dependency upgrade.

- [x] **Step 5: Commit**

```bash
git add internal/proxycore go.mod go.sum NOTICE
git commit -m "feat: add sing-box outbound runtime"
```

### Task 5: Connection-pinned authenticated HTTP proxy

**Files:**
- Create: `internal/httpproxy/server.go`
- Create: `internal/httpproxy/auth.go`
- Create: `internal/httpproxy/connect.go`
- Create: `internal/httpproxy/forward.go`
- Create: `internal/httpproxy/server_test.go`
- Create: `internal/httpproxy/connect_test.go`
- Create: `internal/httpproxy/forward_test.go`

**Interfaces:**
- Produces `httpproxy.New(name string, cfg Config, registry *pool.Registry, selector *pool.Selector, logger *slog.Logger) *Server`.
- `Server.Serve(net.Listener)`, `Shutdown(ctx)`, and `ActiveConnections()` support orchestration and draining.
- `ConnContext` attaches a connection session; its first outbound dial runs the try-each-once attempt and commits the successful node.

**Acceptance Criteria:**
- Missing/wrong credentials return 407 and never dial upstream.
- CONNECT, ordinary absolute-form HTTP, and HTTP Upgrade are supported.
- One inbound keep-alive connection retains one node.
- Failed node dial immediately tries the next node in the same client operation.
- All nodes failing returns a generic 502 without endpoint or credential data.
- Bidirectional tunnel copy handles half-close and shutdown without goroutine leaks.

- [x] **Step 1: Write failing protocol tests with fake dialers**

```go
func TestProxyRequiresAuthenticationBeforeDial(t *testing.T) { /* assert 407 and zero dial calls */ }
func TestConnectRetriesUntilNodeSucceeds(t *testing.T) { /* A,B fail; C tunnels */ }
func TestAllNodesFailReturnsRedacted502(t *testing.T) { /* no raw failure detail in body */ }
func TestKeepAlivePinsNode(t *testing.T) { /* two requests over one client TCP conn use one node */ }
func TestUpgradePinsUntilClose(t *testing.T) { /* bidirectional echo stays on selected node */ }
```

- [x] **Step 2: Run RED**

Run: `go test ./internal/httpproxy -race -v`

Expected: compilation failure because proxy server is absent.

- [x] **Step 3: Implement authentication, CONNECT tunneling, HTTP forwarding, and connection sessions**

Use constant-time credential comparison, strip hop-by-hop and proxy authorization headers before forwarding, cap error bodies, and set explicit header/read-idle timeouts without imposing a lifetime timeout on established tunnels.

- [x] **Step 4: Run GREEN under race and leak-sensitive repetition**

Run: `go test ./internal/httpproxy -race -count=20`

Expected: all runs pass without races or hanging goroutines.

- [x] **Step 5: Commit**

```bash
git add internal/httpproxy
git commit -m "feat: add connection-pinned HTTP proxy"
```

### Task 6: Application orchestration, status API, refresh, and graceful shutdown

**Files:**
- Create: `internal/app/app.go`
- Create: `internal/app/app_test.go`
- Create: `internal/admin/server.go`
- Create: `internal/admin/server_test.go`
- Modify: `cmd/dual-egress-gateway/main.go`

**Interfaces:**
- `app.New(config.Config, Dependencies) (*app.App, error)`, `Run(ctx) error`, and `Status() admin.Status`.
- `admin.Status` contains counts, timestamps, listener activity, and redacted source errors only.
- Startup refresh, scheduled refresh, scheduled probes, two listeners, admin server, and shutdown run under one cancellation tree.

**Acceptance Criteria:**
- Both listeners start or the process fails atomically; no half-started service remains.
- The same registry instance and different selector instances are injected into the listeners.
- `/healthz` reports not-ready when no node is usable and ready when one is usable.
- `/status` never contains configured secrets, subscription URLs, node options, addresses, or tags.
- Refresh cannot overlap and shutdown drains active tunnels for the configured period.

- [x] **Step 1: Write failing orchestration tests**

```go
func TestAppSharesRegistryButNotSelectors(t *testing.T) { /* dependency spy checks identity */ }
func TestStartupIsAtomic(t *testing.T) { /* second bind failure closes first listener */ }
func TestStatusIsRedacted(t *testing.T) { /* marshal and search for seeded secrets */ }
func TestShutdownDrainsConnections(t *testing.T) { /* active tunnel completes within drain window */ }
```

- [x] **Step 2: Run RED**

Run: `go test ./internal/app ./internal/admin -race -v`

Expected: compilation failure for missing app/admin packages.

- [x] **Step 3: Implement orchestration and loopback-only admin server**

Use `signal.NotifyContext` in main for SIGINT/SIGTERM. Start refresh immediately, admit only probed nodes, and expose source failures by numeric source ID.

- [x] **Step 4: Run GREEN and the whole suite**

Run: `go test ./... -race && go vet ./...`

Expected: all packages pass and vet exits 0.

- [x] **Step 5: Commit**

```bash
git add cmd internal/app internal/admin
git commit -m "feat: run dual proxy gateway"
```

### Task 7: Linux packaging, WSL integration, and live smoke verification

**Files:**
- Create: `Dockerfile`
- Create: `docker-compose.example.yml`
- Create: `deploy/install-ubuntu.sh`
- Create: `deploy/uninstall-ubuntu.sh`
- Create: `deploy/dual-egress-gateway.service`
- Create: `scripts/smoke-wsl.sh`
- Create: `tests/integration/gateway_test.go`
- Create: `tests/test_deploy.sh`
- Create: `README.md`

**Interfaces:**
- Installer consumes a built Linux binary, config, and mode-0600 environment file; supports Ubuntu 20.04/22.04/24.04.
- WSL smoke script consumes the ignored local `.env`, starts on loopback ports, and prints only redacted counts and pass/fail results.

**Acceptance Criteria:**
- Docker image runs unprivileged and has no embedded secrets.
- systemd service has `NoNewPrivileges=true`, `PrivateTmp=true`, filesystem protections, explicit writable state path, and restart policy.
- Installer does not open UFW unless a LAN CIDR is explicitly provided; rules are restricted to the two proxy ports and that CIDR.
- Local integration test proves independent rotation, same-attempt failover, shared failure state, keep-alive pinning, and tunnel pinning.
- WSL build and tests pass on Ubuntu 24.04.
- Live smoke test uses the first available real subscription without logging its URL/body; the currently HTTP-404 source is reported only as `source 2: HTTP 404`.

- [x] **Step 1: Write failing integration and deployment static tests**

```go
func TestDualListenersRotateIndependently(t *testing.T) { /* fake proxies expose distinct IDs */ }
func TestFailureIsRetriedAndShared(t *testing.T) { /* dead A skipped, B succeeds, other listener omits A */ }
func TestWebSocketTunnelStaysPinned(t *testing.T) { /* echo server identifies one proxy for tunnel lifetime */ }
```

Add a shell test that rejects `0.0.0.0/0`, missing systemd hardening, world-readable env files, and unscoped `ufw allow <port>`.

- [x] **Step 2: Run RED**

Run: `go test ./tests/integration -race -v && bash tests/test_deploy.sh`

Expected: failure because packaging, service, and smoke behavior are absent.

- [x] **Step 3: Implement packaging, deployment scripts, README, and WSL smoke runner**

Document LAN client configuration separately for HTTP and WS proxy ports. Include commands for status, logs, manual refresh, upgrades, rollback, and complete uninstall.

- [x] **Step 4: Run GREEN with full Windows/WSL verification**

Run on WSL Ubuntu 24.04:

```bash
go test ./... -race
go vet ./...
go build -trimpath -ldflags='-s -w' ./cmd/dual-egress-gateway
docker build -t dual-egress-gateway:test .
bash tests/test_deploy.sh
bash scripts/smoke-wsl.sh
```

Expected: unit/integration/deployment tests pass, Linux binary and Docker image build, both local proxy listeners make successful authenticated connections, rotations remain independent, and no secret appears in captured output.

- [x] **Step 5: Run security and repository checks**

Run:

```bash
govulncheck ./...
git grep -nE 'OGJj|e72cd6|subscribe\.php\?key=|get\.sushi2\.cloud/sushi/' -- ':!*.md'
git status --short
```

Expected: no reachable vulnerability; secret grep returns no matches; only intended changes are present.

- [x] **Step 6: Commit**

```bash
git add Dockerfile docker-compose.example.yml deploy scripts tests README.md
git commit -m "feat: package and verify Ubuntu gateway"
```

## Execution Notes

- Before Task 1, inspect the selected Easy Proxies upstream commit and record its immutable commit ID in `NOTICE`.
- The first supplied subscription returns 29 VLESS outbounds. The replacement second source returns 29 sing-box outbounds across AnyTLS, Hysteria2, Trojan, and VLESS; both sources pass the live WSL smoke test. These observations are test-environment state, not committed configuration.
- If the pinned sing-box API cannot support safe independent outbound lifecycles, stop that task and switch to a supervised sing-box subprocess with loopback-only internal listeners; do not weaken connection drain, retry, or secret-handling requirements.

## Completion Evidence

- WSL Ubuntu 24.04 with Go 1.26.8: `go test ./... -race -count=3` passed.
- `go vet ./...` passed.
- `govulncheck ./...` reported 0 reachable vulnerabilities.
- Linux static binary build passed.
- Docker image build passed; runtime user is `65532:65532`.
- ShellCheck completed with no findings for deployment, uninstall, smoke, and test scripts.
- Repository and deployment safety tests passed.
- Live user-subscription smoke test passed on both independent proxy listeners and demonstrated multiple distinct egress addresses without logging them.
- Independent production code review completed with no remaining Critical or Important findings.
