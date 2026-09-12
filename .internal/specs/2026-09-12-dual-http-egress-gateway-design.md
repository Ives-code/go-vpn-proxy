# Dual HTTP Egress Gateway Design

> Date: 2026-09-12
> Status: Approved in conversation
> Target: Ubuntu 20.04/22.04/24.04; development verification on WSL Ubuntu 24.04

## Purpose

Build one Go service that exposes two authenticated HTTP forward-proxy listeners on a LAN. One listener is reserved for ordinary HTTP/HTTPS workloads and one for WebSocket/WSS workloads. Both consume the same subscription-derived proxy nodes and shared health state, but each advances an independent round-robin cursor.

The project lives entirely under `D:/data/yc/dual-egress-gateway`. Existing files in `D:/data/yc` are out of scope and must not be changed.

## Chosen Approach

Use Go with sing-box as the outbound protocol engine. Reuse narrowly selected MIT-licensed parsing, builder, and pool concepts from `jasonwong1991/easy_proxies`, retaining license and attribution. The first release registers only VLESS (the supplied working subscription's protocol) and HTTP upstream proxies, avoiding unused protocol modules and their attack surface; additional protocols require an explicit, tested registry expansion.

The service runs two HTTP proxy servers inside one process:

```text
LAN client
  +-- HTTP proxy listener (default :18080) -- HTTP round-robin cursor --+
  +-- WS proxy listener   (default :18081) -- WS round-robin cursor   --+-- shared node/health registry -- sing-box outbounds
                                                                    |
localhost-only status API (default 127.0.0.1:19090) ----------------+
```

The two labels describe intended workload separation. Both listeners implement the standard HTTP proxy protocol, including CONNECT. A WSS client connects through the WS listener with HTTP CONNECT; an established tunnel stays on its selected node until it closes.

## Connection Semantics

Each accepted client TCP connection receives a connection-scoped routing session. Its listener's cursor determines the first node to try. The gateway attempts each currently eligible node at most once, in round-robin order, until an outbound connection succeeds or every eligible node fails.

When node B is the first successful choice after A fails, the listener cursor advances past B so the next new connection begins with C. HTTP and WS listeners have separate cursors. HTTP keep-alive requests on one accepted TCP connection retain the selected node. WebSocket and CONNECT tunnels never migrate after establishment. If an established upstream dies, that connection closes naturally; migration occurs only on a later client reconnect.

## Shared Node Health

Both listeners use one concurrency-safe registry keyed by a credential-safe stable node ID. A failed dial immediately excludes the node from subsequent new connections in both listeners. The current dial continues trying the remaining snapshot.

A background probe runs every 30 seconds by default. A successful end-to-end request through the node restores it. Health targets and timeouts are configurable; the default probe is an HTTPS request expected to return HTTP 204. Health transitions are logged using only the stable anonymous ID and subscription index.

If all nodes are marked unavailable, the connection attempt performs one controlled half-open pass through every node. This avoids permanent lockout when background checks are temporarily unable to reach their target. Only one half-open attempt per node runs concurrently.

## Subscription Lifecycle

Subscription URLs are supplied through an environment file and are never committed. The loader supports multiple URLs and the formats required by the supplied subscriptions, with extensible support for Base64 URI lists, plain URI lists, and Clash/Mihomo YAML.

Refresh runs every 30 minutes by default and can be triggered at startup. Each source has a last-known-good snapshot:

- fetch/parse failure preserves that source's previous nodes;
- a successful response atomically replaces only that source's snapshot;
- stable nodes preserve health history;
- new nodes begin quarantined and must pass a probe before use;
- removed nodes stop receiving new connections immediately;
- active connections keep their already-built outbound until they close;
- duplicate nodes across sources collapse to one node while retaining source membership.

Refreshes cannot overlap. Timeouts, response-size limits, supported URL schemes, and strict parsing prevent a broken or malicious subscription from exhausting resources or emptying the pool.

## Configuration and Security

Runtime configuration is YAML plus environment variables for secrets:

- `HTTP_LISTEN_ADDR`, default `0.0.0.0:18080` for the Ubuntu example;
- `WS_LISTEN_ADDR`, default `0.0.0.0:18081` for the Ubuntu example;
- `ADMIN_LISTEN_ADDR`, fixed/default `127.0.0.1:19090`;
- `PROXY_USERNAME` and `PROXY_PASSWORD`, both required;
- `SUBSCRIPTION_URLS`, newline-separated and required;
- refresh, probe, dial, shutdown, and maximum-response limits.

The checked-in example binds proxy ports to `127.0.0.1` to fail safely. The Ubuntu deployment environment explicitly opts into LAN listening. Proxy authentication uses constant-time comparison and returns `407 Proxy Authentication Required` without leaking which credential field failed. Logs redact URL userinfo, query strings, node addresses, credentials, and subscription bodies.

The systemd service runs as a dedicated unprivileged user with `NoNewPrivileges`, `PrivateTmp`, a read-only filesystem except its state directory, bounded file descriptors, restart-on-failure, and an EnvironmentFile with mode `0600`. UFW changes are opt-in and restricted to a configured LAN CIDR; the deployment script never opens the proxy ports to all sources implicitly.

## Status and Observability

The loopback-only API provides:

- `GET /healthz`: process readiness and whether at least one node is usable;
- `GET /status`: counts by healthy/unhealthy/quarantined state, active connections per listener, refresh timestamps, and redacted errors;
- `POST /refresh`: authenticated through a separate local admin token or Unix-local access; it never returns subscription contents.

Prometheus support and a Web UI are out of scope for the first version.

## Failure Handling

- Client authentication failure: return 407 without attempting an outbound.
- Unsupported proxy request: return 400/405 and close the connection.
- One node fails: record failure, share the unhealthy state, and try the next node in the same client connection.
- Every node fails: return 502 with a generic message and retain detailed redacted diagnostics locally.
- One subscription fails: retain its last-known-good set.
- All subscriptions fail on first startup: keep admin health endpoint alive, mark readiness false, and retry on schedule.
- Refresh removes a node with active connections: drain naturally; no new assignments.
- Shutdown: stop accepting connections, allow a configurable drain period, then cancel remaining work.

## Testing Strategy

Development follows test-first cycles. Unit tests cover configuration validation, redaction, parsers, stable IDs, snapshot reconciliation, shared health transitions, independent cursors, try-each-once failover, and all-nodes-failed behavior.

Integration tests use local fake HTTP proxies, an HTTP origin, and a WebSocket echo server. They prove:

- HTTP and WS listeners rotate independently;
- a failed proxy is skipped during the same connection attempt;
- the failure is immediately visible to both listeners;
- HTTP keep-alive stays on one selected node;
- WebSocket/CONNECT tunnels stay on one node until closed;
- a recovered node re-enters rotation;
- refresh adds/removes nodes without terminating existing tunnels;
- credentials are required and secrets do not appear in logs/status.

WSL Ubuntu 24.04 supplies the Linux build and end-to-end environment. Live subscription smoke tests fetch the two user-provided URLs from a local ignored environment file, report only source numbers/node counts/protocol counts, then verify both proxy ports against a public IP endpoint without printing node credentials or subscription contents.

## Deliverables

- Go source, unit tests, integration tests, and race tests.
- `config.example.yaml` and `.env.example` containing no real credentials or URLs.
- Dockerfile and Docker Compose example.
- Hardened Ubuntu systemd deployment and uninstall scripts.
- WSL smoke-test script and operator README.
- Upstream license/attribution notices for reused MIT components and sing-box dependencies.

## Explicit Non-Goals

- No modification or deletion of pre-existing files outside the new project directory.
- No transparent interception, TUN device, browser extension, TLS MITM, Web UI, per-node public port, UDP proxying, or mid-connection migration.
- No guarantee that a node that passes a generic probe can access every possible destination; dial failures remain authoritative and trigger immediate failover.
