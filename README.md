# Dual Egress Gateway

One Go process exposing two authenticated HTTP proxy listeners with independent
round-robin cursors and a shared subscription-backed health registry.

- HTTP workload proxy: port `18080`
- WebSocket/WSS workload proxy: port `18081`
- Loopback-only status API: port `19090`

Each client TCP connection stays on the first upstream node that connects
successfully. Failed nodes are tried in order within the same connection, are
shared as unhealthy by both listeners, and return after a successful probe.

## Configuration

Copy `config.example.yaml` to `config.yaml` and `.env.example` to `.env`.
Real subscription URLs and credentials belong only in `.env`, which is ignored
by Git. `SUBSCRIPTION_URLS` is newline-separated; quote a multi-line value in
shell-compatible environment files.

The checked-in configuration binds to loopback. For LAN deployment, change only
the two proxy listener addresses to `0.0.0.0`; keep `admin_listen` on
`127.0.0.1`. Use a firewall rule restricted to your LAN CIDR.

## Build and test on Ubuntu

Use Go 1.26.8 or a compatible Go 1.26 patch release.

```bash
go test ./... -race
go vet ./...
go build -tags "with_quic with_utls with_grpc" -trimpath \
  -o dual-egress-gateway ./cmd/dual-egress-gateway
```

Run after exporting the variables from `.env`:

```bash
./dual-egress-gateway -config config.yaml
```

Configure ordinary HTTP/HTTPS clients to use `http://SERVER:18080` and clients
opening WebSocket/WSS connections to use `http://SERVER:18081`. Supply the same
proxy username/password on both. WSS uses HTTP CONNECT and remains on the chosen
node for the tunnel lifetime.

## Status and refresh

The following endpoints are intentionally loopback-only:

```bash
curl http://127.0.0.1:19090/healthz
curl http://127.0.0.1:19090/status
curl -X POST http://127.0.0.1:19090/refresh \
  -H "Authorization: Bearer ${ADMIN_TOKEN}"
```

Status output contains only counts, timestamps, active connection counts, and
source numbers. It never returns subscription contents, node endpoints, or
credentials.

## systemd installation

Build the Linux binary, prepare `config.yaml` and `gateway.env`, then run:

```bash
sudo LAN_CIDR=192.168.1.0/24 bash deploy/install-ubuntu.sh \
  ./dual-egress-gateway ./config.yaml ./gateway.env
```

Omit `LAN_CIDR` to leave UFW unchanged. The installer creates a dedicated
unprivileged user and a hardened service. To remove the service while preserving
configuration and state:

```bash
sudo bash deploy/uninstall-ubuntu.sh
```

Add `--purge` only when configuration, state, and the service user should also
be permanently deleted.

## Docker

Copy `docker-compose.example.yml` to `docker-compose.yml`, create `.env`,
`config.yaml`, and `state/`, then run `docker compose up -d --build`. Do not
publish port `19090` beyond loopback.

## Known behavior

- The first release enables VLESS subscription nodes and HTTP upstream proxies;
  unsupported node protocols are rejected explicitly instead of being loaded
  through unused sing-box protocol modules.
- Existing CONNECT/WebSocket tunnels are not migrated when a node fails.
- HTTP keep-alive requests on one client connection retain its selected node.
- A generic HTTPS probe cannot guarantee that every node can reach every target;
  real dial failures still cause immediate shared ejection and failover.
- A subscription source that fails or returns an empty/invalid body keeps its
  previous successful snapshot.
