# Gateway Alert Notifications Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use beads-superpowers:subagent-driven-development (recommended) or beads-superpowers:executing-plans to implement this plan task-by-task. Each Task becomes a bead (`bd create -t task --parent <epic-id>`). Steps within tasks use checkbox (`- [ ]`) syntax for human readability.

**Goal:** Notify the operator when a subscription refresh fails or the healthy-node count falls below 30.

**Scope extension:** Also read `Subscription-Userinfo` expiry metadata, expose it
in status, and send a renewal reminder within 7 days of expiry. Delivery runs in
an independent 5-second worker after settled initial refresh. Failed delivery is
retried by that worker; deduplication lasts for the process lifetime.

## Completion evidence (2026-09-12)

Configuration, push client, monitor, expiry metadata, status, and worker integration
are implemented. Package tests and the full race suite pass, as does `go vet`.
Linux build, repository safety, and deployment safety checks pass. An explicit
live test notification was accepted by the configured push service. Both running
proxy ports returned HTTP 204 through the updated WSL process. Metadata inspection
found expiry dates for sources 2/3 (2026-10-12); source 1 is unknown.

The original task steps below record the initial plan; final implementation uses
the independent worker described in the scope extension instead of inline delivery.

**Architecture:** Add an optional secret push client and an edge-triggered alert monitor. The application evaluates the monitor after refreshes and probe cycles while proxy serving remains independent of notification delivery.

**Tech Stack:** Go 1.26, `net/http`, existing YAML/environment configuration, Go test servers.

## Global Constraints

- Never expose complete subscription URLs, node endpoints, credentials, or push tokens.
- Failed notifications must not interrupt proxy traffic, refreshes, or probes.
- Alerts are edge-triggered; failed delivery is retried on the next evaluation.
- `healthy < 30` triggers the default low-node alert.

---

### Task 1: Configuration and Push Client

**Files:**
- Modify: `internal/config/config.go`
- Modify: `internal/config/config_test.go`
- Create: `internal/notify/client.go`
- Create: `internal/notify/client_test.go`

**Interfaces:**
- Produces: `Config.PushBaseURL`, `Config.LowNodeThreshold`, and `notify.Client.Send(context.Context, string, string) error`.

**Acceptance Criteria:**
- Optional push configuration is validated and redacted; outbound paths are escaped, bounded, redirect-safe, and require a 2xx response.

- [ ] Write failing configuration tests for the default threshold, valid push URL, and invalid scheme.
- [ ] Run `go test ./internal/config -run 'Push|Threshold'` and confirm feature-missing failures.
- [ ] Implement the two configuration fields and validation.
- [ ] Write failing `internal/notify` tests for escaping and response handling.
- [ ] Run `go test ./internal/notify` and confirm the package/API is missing.
- [ ] Implement the minimal notification client and rerun both package tests.

### Task 2: Edge-Triggered Alert Monitor

**Files:**
- Create: `internal/alert/monitor.go`
- Create: `internal/alert/monitor_test.go`

**Interfaces:**
- Consumes: a `Sender` with `Send(context.Context, string, string) error`, safe source labels, `map[string]string` source errors, and `pool.Stats`.
- Produces: `Monitor.Evaluate(context.Context, map[string]string, pool.Stats)`.

**Acceptance Criteria:**
- Each source and the low-node condition alert once per failure period, recover silently, and retry a failed delivery.

- [ ] Write failing state-transition and Chinese-message tests.
- [ ] Run `go test ./internal/alert` and confirm the package/API is missing.
- [ ] Implement the minimal monitor.
- [ ] Run `go test ./internal/alert` and confirm all transition tests pass.

### Task 3: Application Integration and Deployment Documentation

**Files:**
- Modify: `internal/app/app.go`
- Modify: `internal/app/app_test.go`
- Modify: `.env.example`
- Modify: `config.example.yaml`
- Modify: `config.container.example.yaml`
- Modify: `README.md`

**Interfaces:**
- Consumes: the config, notification client, monitor, subscription snapshot, and registry statistics.
- Produces: alert evaluation after initial/periodic/manual refresh and periodic probes.

**Acceptance Criteria:**
- Alerts observe settled refresh/probe state, notification errors are non-fatal and safely logged, and deployment examples document the feature without real secrets.

- [ ] Write a failing application test proving evaluations receive post-probe state.
- [ ] Run the targeted application test and confirm the integration is missing.
- [ ] Wire the monitor into `app.New`, `Refresh`, and the probe loop.
- [ ] Update examples and README with secret-safe instructions.
- [ ] Run `gofmt`, `go test ./... -race -count=3`, `go vet ./...`, a CGO-disabled build, repository safety tests, and a WSL live smoke test.
- [ ] Commit only after every verification command succeeds.
