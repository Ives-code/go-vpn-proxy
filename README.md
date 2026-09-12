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
unprivileged user and a hardened service. Upgrades back up the previous binary,
configuration, environment, unit, and managed UFW rules; a failed restart
restores the previous installation automatically. To remove the service while
preserving configuration and state:

```bash
sudo bash deploy/uninstall-ubuntu.sh
```

Add `--purge` only when configuration, state, and the service user should also
be permanently deleted.

## Docker

Copy `docker-compose.example.yml` to `docker-compose.yml`, copy
`config.container.example.yaml` to `config.container.yaml`, create `.env` and
`state/`, then run `docker compose up -d --build`. The container example does
not publish the loopback-only admin port; inspect it from inside the container
namespace or use the native systemd deployment when host-side status access is
required.

## 中文告警配置

在私密环境文件设置 `PUSH_BASE_URL` 为推送服务的地址前缀（包含设备令牌，
不包含标题和正文）。留空则关闭提醒。支持 HTTP/HTTPS；HTTP 地址会以明文
传输推送令牌和消息，服务支持 HTTPS 时建议使用 HTTPS。

- 订阅刷新失败：显示订阅编号和域名；网络故障也会触发，不等同于确认过期。
- 可用节点少于 `low_node_threshold`（默认 30）：发送数量提醒。
- 订阅到期前 7 天：根据响应头 `Subscription-Userinfo` 中的 Unix 秒时间戳
  `expire` 提醒续费，已过期也会提醒；未提供有效时间则显示为未知，无法自动预告。
- 同一故障或同一到期日期在本次进程运行内只提醒一次；恢复后再次异常重新提醒。
  重启会重新评估并可能再次提醒。推送失败按后台检查周期重试。
- 后台每 5 秒检查已完成刷新的状态，订阅本身默认每 30 分钟刷新。
  状态接口的 `subscription_expires_at` 显示各编号的到期日期，零时间表示未知。

## Other behavior

- Clash YAML AnyTLS subscriptions are supported, including TLS/SNI, client
  fingerprint and non-negative session-pool settings (intervals are seconds).

- The first release enables VLESS, AnyTLS, Hysteria2, and Trojan subscription
  nodes plus HTTP upstream proxies;
  unsupported node protocols are rejected explicitly instead of being loaded
  through unused sing-box protocol modules.
- Reality VLESS nodes that omit uTLS are normalized to an enabled Chrome uTLS
  fingerprint because sing-box requires uTLS for Reality clients.
- Existing CONNECT/WebSocket tunnels are not migrated when a node fails.
- HTTP keep-alive requests on one client connection retain its selected node.
- A generic HTTPS probe cannot guarantee that every node can reach every target;
  real dial failures still cause immediate shared ejection and failover.
- A subscription source that fails or returns an empty/invalid body keeps its
  previous successful snapshot.
