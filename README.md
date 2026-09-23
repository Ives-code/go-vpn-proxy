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

### Webshare HTTP upstream proxies

For a private file containing one `IPv4:port:username:password` proxy per line,
set `webshare_file: /etc/dual-egress-gateway/webshare-proxies.txt` in the YAML
configuration. Store that file outside the repository with owner `root`, group
`dual-egress`, and mode `0640`. This source is added after the HTTPS subscription
sources and appears in `dual-egress-gateway subscriptions` as `Webshare 文件`.
The existing shared health pool checks these nodes every 30 seconds and removes
failed nodes from new-connection routing on both listeners; recovered nodes can
return. The file is re-read at startup and on each 30-minute subscription refresh.
Invalid updates keep the previous successful in-memory snapshot. Existing tunnels
remain pinned to the node chosen when they connected.


## 固定单节点入口

`cmd/fixed-egress` 提供独立的固定 HTTP/HTTPS/WSS 代理，配置文件必须恰好包含
一个节点，所有连接使用该节点，没有其他节点可供回退。使用环境变量
`PROXY_USERNAME` / `PROXY_PASSWORD` 认证。

```bash
go build -tags "with_quic with_utls with_grpc" -o fixed-egress ./cmd/fixed-egress
./fixed-egress -listen 127.0.0.1:18084 -node /etc/dual-egress-fixed/node.json
```

服务模板为 `deploy/dual-egress-fixed.service`，独立于轮询服务。节点文件包含
凭据，保存在服务器私密目录，不提交到仓库。

## Gateway configuration

HTTPS is required by default. An operator can explicitly permit individual HTTP
subscription URLs by listing their exact URLs in the private environment variable
`HTTP_SUBSCRIPTION_ALLOWLIST` (newline-separated). Other HTTP URLs remain rejected,
and redirects must retain the original scheme and host. HTTP subscription traffic
is unencrypted; use this exception only for intentionally configured sources.

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

### 查看各订阅信息

```bash
./dual-egress-gateway subscriptions
# 自定义管理端口：
./dual-egress-gateway subscriptions -admin http://127.0.0.1:19090
```

显示所有已配置订阅的编号、脱敏地址、刷新结果、生效状态、到期时间，以及
解析/加载/可用/不可用/待检测节点数。地址路径和密钥不会显示。生效表示至少
有一个可用节点；刷新失败但有已加载节点时标记“使用缓存”。到期信息未知时
明确显示未知，已过期与当前节点是否仍可用分别显示。

此命令仅执行只读 `GET /status`，不需要加载环境密钥、不触发刷新、不启动代理。
客户端与服务端都需要包含此功能；查询旧版运行进程时会提示维护时更新重启，
不会猜测缺失的分项数量。汇总是去重节点数，共享节点可计入多条订阅。

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

### 手工导入备用订阅快照

可设置 YAML `subscription_seed_file: /etc/dual-egress-gateway/subscription-seed.json`。
文件为 JSON 数组，每项包含 `source_sha256`（原订阅 URL 的 SHA-256 十六进制）、
`body`（订阅文本）、`hint`（格式提示）、`expires_at`（RFC3339 到期时间）。
文件必须为普通私密文件，不允许其他用户读取；内容包含节点凭据，不可提交到 Git。
启动时会校验并作为缓存加载。网络刷新失败继续使用缓存，刷新成功则在内存中
替换；原 URL、来源编号和失败提醒保留。此手工快照不会自动写回更新，重启仍
从该文件恢复，因此需按需重新导入。健康状态由服务器重新探测。

在私密环境文件设置 `PUSH_BASE_URL` 为推送服务的地址前缀（包含设备令牌，
不包含标题和正文）。留空则关闭提醒。支持 HTTP/HTTPS；HTTP 地址会以明文
传输推送令牌和消息，服务支持 HTTPS 时建议使用 HTTPS。

设置 `NOTIFY_HOST_IP` 为该服务器公网 IP；全部告警的标题和正文都会标明这个 IP。
未设置时使用本机首个非回环 IPv4 地址（云服务器/WSL 中可能是内网地址）。
不会查询代理出口 IP，也不会通过第三方服务自动探测公网地址。

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

- The runtime enables VLESS, AnyTLS, Hysteria2, Trojan, and sing-box JSON Shadowsocks subscription
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
