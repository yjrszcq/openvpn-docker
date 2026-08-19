# REST API v1

REST API 为现有客户端、配置、状态和 runtime service 提供经过认证的远程入口。它不会替代 SQLite 权威状态、operation journal、runtime 锁或本地 `ovpn` CLI。

API 默认关闭，内部仅使用 HTTP，不管理 TLS 证书。远程访问时必须放在 HTTPS 反向代理后面。

启用 API 后可直接访问内置开发文档：

- `http://<OVPN_API_LISTEN>/docs/`：面向前端的完整接口文档。22 条接口各自独立展示自己的完整 HTTP 请求、参数、body 字段、成功返回、错误返回和 JSON 示例，不需要跳到共享模型区拼装。
- `http://<OVPN_API_LISTEN>/docs/openapi.json`：OpenAPI 3.1 规范，可导入 Orval、OpenAPI Generator、NSwag 或 API 客户端。

文档页面和 OpenAPI 文件不要求 API key；实际 `/api/v1/*` 资源仍然必须认证。

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

## 逐接口请求与返回

以下 22 条接口均为独立契约。除 `/healthz` 外，每个请求都必须发送 `Authorization: Bearer <API_KEY>`。示例 UUID、digest 和时间仅用于说明格式。

错误响应都使用以下 JSON 结构；每个接口下方会单独列出它可能返回的状态码：

```json
{"error":{"kind":"client_not_found","message":"client was not found","request_id":"33ba813e-fc63-4af8-b338-7f7486f82202"}}
```

### 1. `GET /healthz`

请求：

```http
GET /healthz HTTP/1.1
Host: vpn-admin.example.com
```

请求体：无。此接口不需要认证。

成功返回 `200 application/json`：

```json
{"status":"ok"}
```

错误返回：`405`，body 使用本节开头的错误结构。

### 2. `GET /api/v1/version`

请求：

```http
GET /api/v1/version HTTP/1.1
Host: vpn-admin.example.com
Authorization: Bearer ovpn_v1.<uuid>.<secret>
```

请求体：无。

成功返回 `200 application/json`：

```json
{
  "version": "4.0.2",
  "data_schema": 4,
  "commit": "dd9b5213f5002a7e69f160e3fd2615e0b3d8d224",
  "build_date": "2026-08-19T09:45:40Z",
  "go_version": "go1.26.5",
  "dependencies": {"sqlite":"github.com/mattn/go-sqlite3 v1.14.48","yaml":"go.yaml.in/yaml/v3 v3.0.4"},
  "compatibility": {"contract_version":1,"adapter":"openvpn-2.7","template_family":"openvpn-2.7","supported_openvpn_versions":["2.7.6"]}
}
```

错误返回：`401`、`405`、`503`，body 使用本节开头的错误结构。

### 3. `GET /api/v1/state`

请求：

```http
GET /api/v1/state HTTP/1.1
Host: vpn-admin.example.com
Authorization: Bearer ovpn_v1.<uuid>.<secret>
```

请求体：无。

成功返回 `200 application/json`：

```json
{"version":1,"state":"HEALTHY","data_schema":4,"instance_id":"bbffeb8c-2d11-4613-874d-b5fcc804a608","revision":12,"scanned_at":"2026-08-19T09:55:07Z","issue_count":0,"pending_operation_count":0}
```

错误返回：`401`、`405`、`500`、`503`，body 使用本节开头的错误结构。

### 4. `GET /api/v1/state/doctor`

请求：

```http
GET /api/v1/state/doctor HTTP/1.1
Host: vpn-admin.example.com
Authorization: Bearer ovpn_v1.<uuid>.<secret>
```

请求体：无。

成功返回 `200 application/json`：

```json
{
  "version":1,
  "state":"DEGRADED_REPAIRABLE",
  "data_schema":4,
  "instance_id":"bbffeb8c-2d11-4613-874d-b5fcc804a608",
  "revision":12,
  "scanned_at":"2026-08-19T09:55:07Z",
  "issue_count":1,
  "pending_operation_count":0,
  "issues":[{"id":"DECLARATIVE_CONFIG_UNAVAILABLE","severity":"repairable","action":"export-config","target":"/etc/ovpn-conf/config.yaml","detail":"declarative configuration is unavailable"}]
}
```

没有问题时 `issues` 字段省略。错误返回：`401`、`405`、`500`、`503`。

### 5. `GET /api/v1/clients`

请求：

```http
GET /api/v1/clients HTTP/1.1
Host: vpn-admin.example.com
Authorization: Bearer ovpn_v1.<uuid>.<secret>
```

请求体：无。

成功返回 `200 application/json`：

```json
{"version":1,"clients":[{"id":"c0d4f871-6ea6-42b3-9e7b-f00cc1ec354e","name":"alice-laptop","status":"active","ipv4":{"mode":"static","address":"10.42.0.30","state":"configured"},"connection":"connected"}]}
```

没有客户端时 `clients` 为 `[]`。错误返回：`401`、`405`、`500`、`503`。

### 6. `POST /api/v1/clients`

请求：

```http
POST /api/v1/clients HTTP/1.1
Host: vpn-admin.example.com
Authorization: Bearer ovpn_v1.<uuid>.<secret>
Content-Type: application/json

{"name":"alice-laptop","ipv4":"auto"}
```

`ipv4` 接受 `auto`、`dynamic` 或静态 IPv4 地址。

成功返回 `201 application/json`，并包含 `Location: /api/v1/clients/{client_id}`：

```json
{"version":1,"operation_id":"7a21e1f8-1ce1-4694-977f-b275eadb8651","client":{"id":"c0d4f871-6ea6-42b3-9e7b-f00cc1ec354e","name":"alice-laptop","status":"active","ipv4":{"mode":"static","address":"10.42.0.2","state":"configured"}},"kick_required":false,"profile_redistribution_required":true}
```

错误返回：`400`、`401`、`409`、`422`、`500`、`503`。

### 7. `GET /api/v1/clients/{client_id}`

请求：

```http
GET /api/v1/clients/c0d4f871-6ea6-42b3-9e7b-f00cc1ec354e HTTP/1.1
Host: vpn-admin.example.com
Authorization: Bearer ovpn_v1.<uuid>.<secret>
```

`client_id` 必须是完整 canonical UUID。请求体：无。

成功返回 `200 application/json`：

```json
{"id":"c0d4f871-6ea6-42b3-9e7b-f00cc1ec354e","name":"alice-laptop","status":"active","ipv4":{"mode":"static","address":"10.42.0.30","state":"configured"}}
```

错误返回：`400`、`401`、`404`、`405`、`500`、`503`。

### 8. `PATCH /api/v1/clients/{client_id}`

请求：

```http
PATCH /api/v1/clients/c0d4f871-6ea6-42b3-9e7b-f00cc1ec354e HTTP/1.1
Host: vpn-admin.example.com
Authorization: Bearer ovpn_v1.<uuid>.<secret>
Content-Type: application/json

{"name":"alice-notebook"}
```

成功返回 `200 application/json`：

```json
{"version":1,"operation_id":"47260b5b-47dd-4f9b-814f-4803668c5934","client":{"id":"c0d4f871-6ea6-42b3-9e7b-f00cc1ec354e","name":"alice-notebook","status":"active","ipv4":{"mode":"static","address":"10.42.0.30","state":"configured"}},"kick_required":false,"profile_redistribution_required":true}
```

错误返回：`400`、`401`、`404`、`405`、`409`、`422`、`500`、`503`。

### 9. `DELETE /api/v1/clients/{client_id}`

请求：

```http
DELETE /api/v1/clients/c0d4f871-6ea6-42b3-9e7b-f00cc1ec354e HTTP/1.1
Host: vpn-admin.example.com
Authorization: Bearer ovpn_v1.<uuid>.<secret>
```

请求体必须为空，不能发送 `{}` 或 `null`。

成功返回 `200 application/json`：

```json
{"version":1,"operation_id":"47260b5b-47dd-4f9b-814f-4803668c5934","client":{"id":"c0d4f871-6ea6-42b3-9e7b-f00cc1ec354e","name":"alice-notebook","status":"deleted","ipv4":{"mode":"none","address":null,"state":"unavailable"}},"kick_required":false,"profile_redistribution_required":false}
```

错误返回：`400`、`401`、`404`、`405`、`409`、`422`、`500`、`503`。

### 10. `GET /api/v1/clients/{client_id}/profile`

请求：

```http
GET /api/v1/clients/c0d4f871-6ea6-42b3-9e7b-f00cc1ec354e/profile HTTP/1.1
Host: vpn-admin.example.com
Authorization: Bearer ovpn_v1.<uuid>.<secret>
```

请求体：无。

成功返回 `200 application/x-openvpn-profile`，不是 JSON：

```http
Content-Type: application/x-openvpn-profile
Content-Disposition: attachment; filename=alice-laptop.ovpn

client
# ovpn-client-id: c0d4f871-6ea6-42b3-9e7b-f00cc1ec354e
...
```

profile 内含私钥，必须按凭据处理。错误返回：`400`、`401`、`404`、`405`、`409`、`422`、`500`、`503`。

### 11. `POST /api/v1/clients/{client_id}/revoke`

请求：

```http
POST /api/v1/clients/c0d4f871-6ea6-42b3-9e7b-f00cc1ec354e/revoke HTTP/1.1
Host: vpn-admin.example.com
Authorization: Bearer ovpn_v1.<uuid>.<secret>
Content-Type: application/json

{"release_ipv4":false}
```

`false` 保留静态地址，`true` 立即释放。

成功返回 `200 application/json`：

```json
{"version":1,"operation_id":"47260b5b-47dd-4f9b-814f-4803668c5934","client":{"id":"c0d4f871-6ea6-42b3-9e7b-f00cc1ec354e","name":"alice-laptop","status":"revoked","ipv4":{"mode":"static","address":"10.42.0.30","state":"retained"}},"kick_required":true,"profile_redistribution_required":false,"runtime":{"status":"ok","result":{"version":1,"client_id":"c0d4f871-6ea6-42b3-9e7b-f00cc1ec354e","client_name":"alice-laptop","was_connected":true,"disconnected":true,"connections":1}}}
```

错误返回：`400`、`401`、`404`、`405`、`409`、`422`、`500`、`503`。

### 12. `POST /api/v1/clients/{client_id}/reissue`

请求：

```http
POST /api/v1/clients/c0d4f871-6ea6-42b3-9e7b-f00cc1ec354e/reissue HTTP/1.1
Host: vpn-admin.example.com
Authorization: Bearer ovpn_v1.<uuid>.<secret>
Content-Type: application/json

{"ipv4":"dynamic"}
```

`ipv4` 接受 `auto`、`dynamic` 或静态 IPv4 地址。

成功返回 `200 application/json`：

```json
{"version":1,"operation_id":"47260b5b-47dd-4f9b-814f-4803668c5934","client":{"id":"c0d4f871-6ea6-42b3-9e7b-f00cc1ec354e","name":"alice-notebook","status":"active","ipv4":{"mode":"dynamic","address":null,"state":"configured"}},"kick_required":true,"profile_redistribution_required":true,"runtime":{"status":"unavailable"}}
```

错误返回：`400`、`401`、`404`、`405`、`409`、`422`、`500`、`503`。

### 13. `PUT /api/v1/clients/{client_id}/ipv4`

请求：

```http
PUT /api/v1/clients/c0d4f871-6ea6-42b3-9e7b-f00cc1ec354e/ipv4 HTTP/1.1
Host: vpn-admin.example.com
Authorization: Bearer ovpn_v1.<uuid>.<secret>
Content-Type: application/json

{"mode":"static","address":"10.42.0.30"}
```

其他合法 body 为 `{"mode":"auto"}` 或 `{"mode":"dynamic"}`，这两种模式禁止发送 `address`。

成功返回 `200 application/json`：

```json
{"version":1,"operation_id":"d41dbfce-b40e-4672-8a62-cb401f8c099c","clients":[{"id":"c0d4f871-6ea6-42b3-9e7b-f00cc1ec354e","name":"alice-laptop","status":"active","ipv4":{"mode":"static","address":"10.42.0.30","state":"configured"}}],"kick_required":["c0d4f871-6ea6-42b3-9e7b-f00cc1ec354e"],"runtime":[{"client_id":"c0d4f871-6ea6-42b3-9e7b-f00cc1ec354e","status":"ok","result":{"version":1,"client_id":"c0d4f871-6ea6-42b3-9e7b-f00cc1ec354e","client_name":"alice-laptop","was_connected":false,"disconnected":false,"connections":0}}]}
```

错误返回：`400`、`401`、`404`、`405`、`409`、`422`、`500`、`503`。

### 14. `DELETE /api/v1/clients/{client_id}/ipv4`

请求：

```http
DELETE /api/v1/clients/c0d4f871-6ea6-42b3-9e7b-f00cc1ec354e/ipv4 HTTP/1.1
Host: vpn-admin.example.com
Authorization: Bearer ovpn_v1.<uuid>.<secret>
```

请求体必须为空。仅 revoked 且保留了静态地址的客户端可调用。

成功返回 `200 application/json`：

```json
{"version":1,"operation_id":"d41dbfce-b40e-4672-8a62-cb401f8c099c","clients":[{"id":"c0d4f871-6ea6-42b3-9e7b-f00cc1ec354e","name":"alice-notebook","status":"revoked","ipv4":{"mode":"none","address":null,"state":"unavailable"}}],"kick_required":[],"runtime":[]}
```

错误返回：`400`、`401`、`404`、`405`、`409`、`422`、`500`、`503`。

### 15. `POST /api/v1/clients/{client_id}/disconnect`

请求：

```http
POST /api/v1/clients/c0d4f871-6ea6-42b3-9e7b-f00cc1ec354e/disconnect HTTP/1.1
Host: vpn-admin.example.com
Authorization: Bearer ovpn_v1.<uuid>.<secret>
```

请求体必须为空。没有在线 session 也返回成功。

成功返回 `200 application/json`：

```json
{"version":1,"client_id":"c0d4f871-6ea6-42b3-9e7b-f00cc1ec354e","client_name":"alice-laptop","was_connected":false,"disconnected":false,"connections":0}
```

错误返回：`400`、`401`、`404`、`405`、`500`、`503`。

### 16. `GET /api/v1/runtime`

请求：

```http
GET /api/v1/runtime HTTP/1.1
Host: vpn-admin.example.com
Authorization: Bearer ovpn_v1.<uuid>.<secret>
```

请求体：无。

成功返回 `200 application/json`：

```json
{"version":1,"daemon":"running","management":"connected","client_count":1,"clients":[{"client_id":"c0d4f871-6ea6-42b3-9e7b-f00cc1ec354e","client_name":"alice-laptop","remote_address":"203.0.113.10:53210","virtual_address":"10.42.0.30"}]}
```

错误返回：`401`、`405`、`500`、`503`。

### 17. `GET /api/v1/runtime/events`

请求：

```http
GET /api/v1/runtime/events?lines=100 HTTP/1.1
Host: vpn-admin.example.com
Authorization: Bearer ovpn_v1.<uuid>.<secret>
```

`lines` 可选，默认 `100`，范围 `0..1000`。请求体：无。

成功返回 `200 application/json`：

```json
{"version":1,"events":[{"timestamp":"2026-08-19T09:55:07Z","event":"client-disconnect","operation":"runtime.disconnect","outcome":"success","client_id":"c0d4f871-6ea6-42b3-9e7b-f00cc1ec354e","client_name":"alice-laptop"}]}
```

不同 event 可能附加额外字段。错误返回：`400`、`401`、`405`、`500`、`503`。

### 18. `GET /api/v1/config/applied`

请求：

```http
GET /api/v1/config/applied HTTP/1.1
Host: vpn-admin.example.com
Authorization: Bearer ovpn_v1.<uuid>.<secret>
```

请求体：无。

成功返回 `200 application/json`：

```json
{
  "revision":12,
  "digest":"489b950c0d134b5d7e8f350b2cb4bfa4d6d163d841f840964baf6d74c65cc8ca",
  "config":{"version":1,"server":{"endpoint":"vpn.example.com","protocol":"udp","family":"auto","port":1194,"client_to_client":true},"ipv4":{"network":"10.42.0.0/24","dynamic_pool_size":64,"nat_enabled":false,"nat_interface":"auto","redirect_gateway":false,"dns":["1.1.1.1"],"routes":["10.20.0.0/16"]},"logging":{"max_bytes":10485760,"backups":5}}
}
```

错误返回：`401`、`405`、`409`、`500`、`503`。

### 19. `GET /api/v1/config/desired`

请求：

```http
GET /api/v1/config/desired HTTP/1.1
Host: vpn-admin.example.com
Authorization: Bearer ovpn_v1.<uuid>.<secret>
```

请求体：无。

成功返回 `200 application/json`，同时返回 `ETag: "<digest>"`：

```json
{
  "digest":"489b950c0d134b5d7e8f350b2cb4bfa4d6d163d841f840964baf6d74c65cc8ca",
  "config":{"version":1,"server":{"endpoint":"vpn.example.com","protocol":"udp","family":"auto","port":1194,"client_to_client":true},"ipv4":{"network":"10.42.0.0/24","dynamic_pool_size":64,"nat_enabled":false,"nat_interface":"auto","redirect_gateway":false,"dns":["1.1.1.1"],"routes":["10.20.0.0/16"]},"logging":{"max_bytes":10485760,"backups":5}}
}
```

错误返回：`401`、`405`、`409`、`422`、`500`、`503`。

### 20. `PUT /api/v1/config/desired`

请求 body 是完整裸 `Configuration`，不能包在 `config` 字段中：

```http
PUT /api/v1/config/desired HTTP/1.1
Host: vpn-admin.example.com
Authorization: Bearer ovpn_v1.<uuid>.<secret>
Content-Type: application/json
If-Match: "489b950c0d134b5d7e8f350b2cb4bfa4d6d163d841f840964baf6d74c65cc8ca"

{"version":1,"server":{"endpoint":"vpn.example.com","protocol":"udp","family":"auto","port":1194,"client_to_client":true},"ipv4":{"network":"10.42.0.0/24","dynamic_pool_size":64,"nat_enabled":false,"nat_interface":"auto","redirect_gateway":false,"dns":["1.1.1.1"],"routes":["10.20.0.0/16"]},"logging":{"max_bytes":10485760,"backups":5}}
```

成功返回 `200 application/json`，同时返回新 `ETag`：

```json
{
  "digest":"489b950c0d134b5d7e8f350b2cb4bfa4d6d163d841f840964baf6d74c65cc8ca",
  "config":{"version":1,"server":{"endpoint":"vpn.example.com","protocol":"udp","family":"auto","port":1194,"client_to_client":true},"ipv4":{"network":"10.42.0.0/24","dynamic_pool_size":64,"nat_enabled":false,"nat_interface":"auto","redirect_gateway":false,"dns":["1.1.1.1"],"routes":["10.20.0.0/16"]},"logging":{"max_bytes":10485760,"backups":5}}
}
```

错误返回：`400`、`401`、`405`、`409`、`422`、`500`、`503`。

### 21. `GET /api/v1/config/plan`

请求：

```http
GET /api/v1/config/plan HTTP/1.1
Host: vpn-admin.example.com
Authorization: Bearer ovpn_v1.<uuid>.<secret>
```

请求体：无。

成功返回 `200 application/json`：

```json
{
  "version":1,
  "instance_id":"bbffeb8c-2d11-4613-874d-b5fcc804a608",
  "configuration":{"initial":false,"current_revision":12,"target_revision":12,"current_digest":"489b950c0d134b5d7e8f350b2cb4bfa4d6d163d841f840964baf6d74c65cc8ca","desired_digest":"489b950c0d134b5d7e8f350b2cb4bfa4d6d163d841f840964baf6d74c65cc8ca","in_sync":true,"changes":[],"impact":{"restart_required":false,"address_remap":false,"firewall_reconcile":false,"profile_redistribution":false,"derived_artifacts":[]}},
  "address_changes":[],
  "artifacts":[],
  "profile_redistribution":[],
  "firewall":{"reconcile":false,"before":null,"after":null}
}
```

错误返回：`401`、`405`、`409`、`422`、`500`、`503`。

### 22. `POST /api/v1/config/apply`

请求中的 digest 和 revision 必须来自刚读取的 desired/plan：

```http
POST /api/v1/config/apply HTTP/1.1
Host: vpn-admin.example.com
Authorization: Bearer ovpn_v1.<uuid>.<secret>
Content-Type: application/json

{"desired_digest":"489b950c0d134b5d7e8f350b2cb4bfa4d6d163d841f840964baf6d74c65cc8ca","current_revision":12,"force":false}
```

成功返回 `200 application/json`：

```json
{
  "version":1,
  "applied":false,
  "activation":{"restart_required":false,"runtime_restarted":false,"profile_redistribution":[]},
  "plan":{"version":1,"instance_id":"bbffeb8c-2d11-4613-874d-b5fcc804a608","configuration":{"initial":false,"current_revision":12,"target_revision":12,"current_digest":"489b950c0d134b5d7e8f350b2cb4bfa4d6d163d841f840964baf6d74c65cc8ca","desired_digest":"489b950c0d134b5d7e8f350b2cb4bfa4d6d163d841f840964baf6d74c65cc8ca","in_sync":true,"changes":[],"impact":{"restart_required":false,"address_remap":false,"firewall_reconcile":false,"profile_redistribution":false,"derived_artifacts":[]}},"address_changes":[],"artifacts":[],"profile_redistribution":[],"firewall":{"reconcile":false,"before":null,"after":null}}
}
```

发生实际变更时 `applied` 为 `true`，并出现 `operation_id`；`activation` 和 `plan` 仍完整返回。错误返回：`400`、`401`、`405`、`409`、`422`、`500`、`503`。

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
