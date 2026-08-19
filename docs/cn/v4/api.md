# REST API v1

REST API 为现有客户端、配置、状态和 runtime service 提供经过认证的远程入口。它不会替代 SQLite 权威状态、operation journal、runtime 锁或本地 `ovpn` CLI。

API 默认关闭，内部仅使用 HTTP，不管理 TLS 证书。远程访问时必须放在 HTTPS 反向代理后面。

## 启用 API

在在线 `openvpn` 服务中设置非空监听地址：

```yaml
services:
  openvpn:
    environment:
      OVPN_API_LISTEN: 127.0.0.1:11940
      OVPN_API_CORS_ORIGINS: https://vpn-admin.example.com
```

项目 Compose 使用 host network，因此 `127.0.0.1:11940` 只在宿主机 loopback 上暴露 API。`OVPN_API_LISTEN` 为空时不会启动 API 进程。不要把这些变量加入 `openvpn-maintenance`。

`OVPN_API_CORS_ORIGINS` 可选，使用逗号分隔的精确浏览器 origin。通配符、带路径或凭据的 origin、空列表项都会被拒绝。同源前端或非浏览器客户端不需要 CORS。

最小 Caddy 边界如下：

```caddyfile
vpn-admin.example.com {
    reverse_proxy 127.0.0.1:11940
}
```

反向代理必须提供 HTTPS、保留 `Authorization` header，并设置适当的网络访问控制。不建议将 API 直接绑定到公网 `0.0.0.0` 地址。

## API key

API key 只能通过本地 CLI 创建、列出和删除：

```bash
docker exec openvpn ovpn api key create frontend-production
docker exec openvpn ovpn api key list
docker exec openvpn ovpn api key delete frontend-production --yes
```

完整 key 只在创建时输出一次。为避免终端历史或输出采集，可直接写入新的 mode-`0600` 文件：

```bash
docker exec openvpn \
  ovpn api key create frontend-production --output /etc/openvpn/frontend.key
```

key 格式为 `ovpn_v1.<uuid>.<secret>`。SQLite 只保存其 SHA-256 摘要。删除后下一次请求立即失效；历史审计只保留 key UUID，不保留认证材料。

使用 Bearer header 发送 key：

```bash
curl --fail --silent --show-error \
  -H "Authorization: Bearer $OVPN_API_KEY" \
  https://vpn-admin.example.com/api/v1/state
```

不要通过 URL、query string、Cookie 或请求体传递 key。

## HTTP 契约

- 基础路径为 `/api/v1`。
- JSON 使用 UTF-8 和 `snake_case` 字段。
- JSON mutation 必须设置 `Content-Type: application/json`。
- 未知字段、重复字段、`null`、尾随文档、错误类型和超过 1 MiB 的 body 都会被拒绝。
- 路径必须使用完整客户端 UUID，不能使用可变名称或 UUID 前缀。
- 除 runtime event history 的 `lines` 外，所有 query 参数都会被拒绝。
- 每个响应都包含 `X-Request-ID`、`Cache-Control: no-store` 和 `X-Content-Type-Options: nosniff`。
- `/healthz` 无需认证，只报告 API 进程存活。
- profile 下载使用 `application/x-openvpn-profile` 和 attachment 文件名。

## 资源

| Method | Path | 用途 |
|---|---|---|
| `GET` | `/healthz` | 最小无认证存活检查。 |
| `GET` | `/api/v1/version` | 构建、数据 schema 和 OpenVPN 兼容信息。 |
| `GET` | `/api/v1/state` | 实例状态摘要。 |
| `GET` | `/api/v1/state/doctor` | SQLite、PKI 和 artifact 详细诊断。 |
| `GET` | `/api/v1/clients` | 列出 active 和 revoked 客户端。 |
| `POST` | `/api/v1/clients` | 创建客户端。 |
| `GET` | `/api/v1/clients/{id}` | 使用完整 UUID 查询客户端。 |
| `PATCH` | `/api/v1/clients/{id}` | 客户端改名。 |
| `DELETE` | `/api/v1/clients/{id}` | 删除本地凭据并保留 UUID tombstone。 |
| `GET` | `/api/v1/clients/{id}/profile` | 下载 active 客户端 profile。 |
| `POST` | `/api/v1/clients/{id}/revoke` | 吊销客户端证书。 |
| `POST` | `/api/v1/clients/{id}/reissue` | 重签凭据和 profile。 |
| `PUT` | `/api/v1/clients/{id}/ipv4` | 设置 IPv4 意图。 |
| `DELETE` | `/api/v1/clients/{id}/ipv4` | 释放 revoked 客户端保留的静态地址。 |
| `POST` | `/api/v1/clients/{id}/disconnect` | 断开当前 session。 |
| `GET` | `/api/v1/runtime` | runtime 与在线客户端状态。 |
| `GET` | `/api/v1/runtime/events?lines=N` | 读取最近 0 到 1000 条结构化事件。 |
| `GET` | `/api/v1/config/applied` | 读取 SQLite applied revision。 |
| `GET` | `/api/v1/config/desired` | 读取规范化 desired YAML 及 digest。 |
| `PUT` | `/api/v1/config/desired` | 验证并原子替换 desired YAML。 |
| `GET` | `/api/v1/config/plan` | 规划 desired 到 applied 的变更。 |
| `POST` | `/api/v1/config/apply` | 在线应用当前 desired 配置。 |

## 客户端请求

创建客户端时，`ipv4` 接受 `auto`、`dynamic` 或一个静态 IPv4 地址：

```json
{"name":"laptop","ipv4":"auto"}
```

创建成功返回 `201 Created` 和 `Location: /api/v1/clients/{id}`。其他 mutation body 如下：

| 操作 | JSON body |
|---|---|
| 改名 | `{"name":"new-name"}` |
| 吊销 | `{"release_ipv4":false}` |
| 重签 | `{"ipv4":"dynamic"}` |
| 自动 IPv4 | `{"mode":"auto"}` |
| 动态 IPv4 | `{"mode":"dynamic"}` |
| 静态 IPv4 | `{"mode":"static","address":"10.42.0.20"}` |

删除、IPv4 release 和 disconnect 不接受 body。重签的 IPv4 选择与创建相同。

部分已提交的客户端或地址变更需要断开当前 session。如果持久 mutation 已提交但 broker 不可用，响应仍保持成功，并将 `runtime.status` 报告为 `unavailable`。直接 disconnect 在 runtime control 不可用时返回 `503`。

profile 内含私钥。应将 profile 响应视为凭据，不能进入浏览器缓存、日志、分析系统或普通下载目录。

## 配置工作流

配置使用乐观并发的三步流程：

```text
PUT desired -> GET plan -> POST apply
```

先读取 desired 配置。响应 body 包含 `digest` 和 `config`，同一 digest 还会作为带引号的 `ETag` 返回：

```bash
curl -H "Authorization: Bearer $OVPN_API_KEY" \
  https://vpn-admin.example.com/api/v1/config/desired
```

替换 desired YAML 时，请求 body 必须是完整规范化的 `config` 对象，并在 `If-Match` 中发送之前读取的 digest：

```http
PUT /api/v1/config/desired HTTP/1.1
Authorization: Bearer ...
Content-Type: application/json
If-Match: "<64-character-lowercase-digest>"

{
  "version": 1,
  "server": {
    "endpoint": "vpn.example.com",
    "protocol": "udp",
    "family": "auto",
    "port": 1194,
    "client_to_client": true
  },
  "ipv4": {
    "network": "10.42.0.0/24",
    "dynamic_pool_size": 64,
    "nat_enabled": false,
    "nat_interface": "auto",
    "redirect_gateway": false,
    "dns": [],
    "routes": []
  },
  "logging": {"max_bytes": 10485760, "backups": 5}
}
```

API 使用与 YAML 相同的领域规则验证输入，通过原子 rename 写入规范 mode-`0600` YAML，并返回新的 digest 和 `ETag`。digest 过期时返回 `409`，文件不会被替换。

检查 `/api/v1/config/plan` 后，使用对应 desired digest 和 plan 中的 `configuration.current_revision` 执行 apply：

```json
{
  "desired_digest": "<64-character-lowercase-digest>",
  "current_revision": 12,
  "force": false
}
```

Apply 会执行常规健康预检，请求 supervisor 暂停 OpenVPN 与 broker，再次检查 digest/revision，在独占锁下运行现有 journal transaction，最后重启受管 runtime。API 进程在此期间保持可用。`force: true` 只能绕过经过确认的健康预检误报，不能绕过 schema、路径、锁、并发或 recovery 检查。

digest 变化、applied revision 变化、mutation 锁占用或 plan 状态过期返回 `409`；supervisor 不可用返回 `503`。

## 错误

错误使用稳定 envelope，不包含内部路径、SQL、请求体、凭据或 Go cause：

```json
{
  "error": {
    "kind": "client_not_found",
    "message": "client was not found",
    "request_id": "33ba813e-fc63-4af8-b338-7f7486f82202"
  }
}
```

| Status | 含义 |
|---|---|
| `400` | 路径、query、JSON、media type、body 或并发 header 无效。 |
| `401` | API key 缺失、格式错误、未知或已删除。 |
| `404` | 资源或客户端不存在。 |
| `405` | Method 不允许；可检查 `Allow` header。 |
| `409` | digest、revision、状态、约束、锁或 recovery 冲突。 |
| `422` | JSON 有效，但客户端、IPv4 或配置语义无效。 |
| `503` | 认证存储、OpenVPN、broker、supervisor 或必要依赖不可用。 |
| `500` | 未分类内部错误。 |

可使用 `request_id` 关联失败请求，但不要记录 Authorization header 或 body。

## 安全边界

- 不需要远程管理时保持 `OVPN_API_LISTEN` 为空。
- 绑定 loopback 或私有管理网络，在可信反向代理终止 TLS。
- 除 API 认证外，还应独立限制反向代理和宿主机防火墙访问。
- 每个集成使用独立 API key，并及时删除不用的 key。
- API key 和 profile 不能进入日志、浏览器存储、telemetry、源码仓库或聊天系统。
- SQLite、YAML、PKI、artifact 和 key 必须作为一个协调的运维单元备份；REST API 不是备份接口。
- API v1 不提供用户、session、RBAC、key scope、key 过期、异步任务或内置 TLS。
