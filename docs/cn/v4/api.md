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

## 路由索引

本节只是快速索引，不作为前端开发契约。逐条接口的请求和返回内容请直接使用运行中服务的 `/docs/`；页面中的每个接口都是独立的“请求 / 返回”单元。

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

## 前端类型索引

下表和 TypeScript 定义用于搜索字段名及复用类型，不替代 `/docs/` 的逐接口请求/返回。除 `/healthz` 外，每个请求都必须发送 `Authorization: Bearer <API_KEY>`。标记为“空 body”的接口不能发送 `{}`、`null` 或任何其他内容。

### System

| 请求 | Path/query/header | JSON body | 成功返回 |
|---|---|---|---|
| `GET /healthz` | 无认证、无 query | 无 | `200 HealthResponse` |
| `GET /api/v1/version` | 无 query | 无 | `200 VersionResponse` |
| `GET /api/v1/state` | 无 query | 无 | `200 StateResponse`，不包含 `issues` |
| `GET /api/v1/state/doctor` | 无 query | 无 | `200 StateResponse`，发现问题时包含 `issues` |

### Clients

所有 `{client_id}` 都必须是完整 canonical UUID，例如 `c0d4f871-6ea6-42b3-9e7b-f00cc1ec354e`，不能使用名称或 UUID 前缀。

| 请求 | Path/query/header | JSON body | 成功返回 |
|---|---|---|---|
| `GET /api/v1/clients` | 无 query | 无 | `200 ClientListResponse` |
| `POST /api/v1/clients` | 无 query | `CreateClientRequest` | `201 ClientMutationResponse`；含 `Location` header |
| `GET /api/v1/clients/{client_id}` | `client_id` path 参数 | 无 | `200 Client` |
| `PATCH /api/v1/clients/{client_id}` | `client_id` path 参数 | `RenameClientRequest` | `200 ClientMutationResponse` |
| `DELETE /api/v1/clients/{client_id}` | `client_id` path 参数 | 空 body | `200 ClientMutationResponse`，`client.status="deleted"` |
| `GET /api/v1/clients/{client_id}/profile` | `client_id` path 参数 | 无 | `200 application/x-openvpn-profile`，不是 JSON |
| `POST /api/v1/clients/{client_id}/revoke` | `client_id` path 参数 | `RevokeClientRequest` | `200 ClientMutationResponse` |
| `POST /api/v1/clients/{client_id}/reissue` | `client_id` path 参数 | `ReissueClientRequest` | `200 ClientMutationResponse` |
| `PUT /api/v1/clients/{client_id}/ipv4` | `client_id` path 参数 | `IPv4Request` | `200 AddressMutationResponse` |
| `DELETE /api/v1/clients/{client_id}/ipv4` | `client_id` path 参数 | 空 body | `200 AddressMutationResponse` |
| `POST /api/v1/clients/{client_id}/disconnect` | `client_id` path 参数 | 空 body | `200 DisconnectResponse`；无在线 session 也是成功 |

### Runtime

| 请求 | Path/query/header | JSON body | 成功返回 |
|---|---|---|---|
| `GET /api/v1/runtime` | 无 query | 无 | `200 RuntimeResponse` |
| `GET /api/v1/runtime/events` | 可选 `lines=0..1000`，默认 `100` | 无 | `200 RuntimeEventsResponse` |

### Configuration

| 请求 | Path/query/header | JSON body | 成功返回 |
|---|---|---|---|
| `GET /api/v1/config/applied` | 无 query | 无 | `200 AppliedConfigurationResponse` |
| `GET /api/v1/config/desired` | 无 query | 无 | `200 DesiredConfigurationResponse`；含 `ETag: "<digest>"` |
| `PUT /api/v1/config/desired` | 必须发送 `If-Match: "<旧 digest>"` | 完整的裸 `Configuration`，不能包在 `config` 字段中 | `200 DesiredConfigurationResponse`；含新 `ETag` |
| `GET /api/v1/config/plan` | 无 query | 无 | `200 ConfigurationPlanResponse` |
| `POST /api/v1/config/apply` | 无 query | `ApplyConfigurationRequest` | `200 ApplyConfigurationResponse` |

### 请求内容

```ts
interface CreateClientRequest {
  name: string;
  // "auto"、"dynamic"，或静态区内的 IPv4 地址
  ipv4: string;
}

interface RenameClientRequest {
  name: string;
}

interface RevokeClientRequest {
  // false 保留当前地址；true 立即释放
  release_ipv4: boolean;
}

interface ReissueClientRequest {
  // "auto"、"dynamic"，或静态区内的 IPv4 地址
  ipv4: string;
}

type IPv4Request =
  | { mode: "auto" }
  | { mode: "dynamic" }
  | { mode: "static"; address: string };

interface ApplyConfigurationRequest {
  desired_digest: string;   // 64 位小写 SHA-256
  current_revision: number; // config/plan.configuration.current_revision
  force?: boolean;          // 默认 false
}
```

请求示例：

```http
POST /api/v1/clients HTTP/1.1
Authorization: Bearer ovpn_v1.<uuid>.<secret>
Content-Type: application/json

{"name":"alice-laptop","ipv4":"auto"}
```

```http
PUT /api/v1/clients/c0d4f871-6ea6-42b3-9e7b-f00cc1ec354e/ipv4 HTTP/1.1
Authorization: Bearer ovpn_v1.<uuid>.<secret>
Content-Type: application/json

{"mode":"static","address":"10.42.0.30"}
```

### 通用返回模型

以下定义与实际 JSON 字段一致。标有 `?` 的字段可能被省略；`null` 与字段省略是不同状态。

```ts
type UUID = string;
type Digest = string;

interface HealthResponse {
  status: "ok";
}

interface APIErrorResponse {
  error: {
    kind: string;
    message: string;
    request_id: UUID;
  };
}

interface VersionResponse {
  version: string;
  data_schema: number;
  commit: string;
  build_date: string;
  go_version: string;
  dependencies: { sqlite: string; yaml: string };
  compatibility: {
    contract_version: number;
    adapter: string;
    template_family: string;
    supported_openvpn_versions: string[];
  };
}

type StateClassification =
  | "EMPTY" | "HEALTHY" | "DEGRADED_REPAIRABLE"
  | "DEGRADED_RECOVERABLE" | "DEGRADED_REISSUABLE"
  | "CRITICAL" | "UNRECOVERABLE";

interface StateIssue {
  id: string;
  severity: "repairable" | "recoverable" | "reissuable" | "critical" | "unrecoverable";
  action: string;
  target?: string;
  owner_id?: string;
  artifact_kind?: string;
  detail: string;
}

interface StateResponse {
  version: 1;
  state: StateClassification;
  data_schema: number;
  instance_id?: UUID;
  revision?: number;
  scanned_at: string;
  issue_count: number;
  pending_operation_count: number;
  issues?: StateIssue[];
}

interface ClientIPv4 {
  mode: "none" | "static" | "dynamic";
  address: string | null;
  state: "configured" | "retained" | "unavailable";
}

interface Client {
  id: UUID;
  name: string;
  status: "active" | "revoked" | "deleted";
  ipv4: ClientIPv4;
  connection?: string;
}

interface ClientListResponse {
  version: 1;
  clients: Client[];
}

interface DisconnectResponse {
  version: 1;
  client_id: UUID;
  client_name: string;
  was_connected: boolean;
  disconnected: boolean;
  connections: number;
}

interface RuntimeOutcome {
  client_id?: UUID;
  status: "ok" | "unavailable";
  result?: DisconnectResponse;
}

interface ClientMutationResponse {
  version: 1;
  operation_id: UUID;
  client: Client;
  kick_required: boolean;
  profile_redistribution_required: boolean;
  runtime?: RuntimeOutcome;
}

interface AddressMutationResponse {
  version: 1;
  operation_id: UUID;
  clients: Client[];
  kick_required: UUID[];
  runtime: RuntimeOutcome[];
}
```

客户端 mutation 返回示例：

```json
{
  "version": 1,
  "operation_id": "47260b5b-47dd-4f9b-814f-4803668c5934",
  "client": {
    "id": "c0d4f871-6ea6-42b3-9e7b-f00cc1ec354e",
    "name": "alice-laptop",
    "status": "revoked",
    "ipv4": {"mode": "static", "address": "10.42.0.30", "state": "retained"}
  },
  "kick_required": true,
  "profile_redistribution_required": false,
  "runtime": {
    "status": "ok",
    "result": {
      "version": 1,
      "client_id": "c0d4f871-6ea6-42b3-9e7b-f00cc1ec354e",
      "client_name": "alice-laptop",
      "was_connected": true,
      "disconnected": true,
      "connections": 1
    }
  }
}
```

### Runtime 返回模型

```ts
interface RuntimeClient {
  client_id: UUID;
  client_name?: string;
  remote_address?: string;
  virtual_address?: string;
}

interface RuntimeResponse {
  version: 1;
  daemon: string;
  management: string;
  client_count: number;
  clients: RuntimeClient[];
}

interface RuntimeEvent {
  timestamp: string;
  event: string;
  operation: string;
  outcome: string;
  client_id?: UUID | null;
  client_name?: string | null;
  // 不同 event 可以附加额外字段
  [key: string]: unknown;
}

interface RuntimeEventsResponse {
  version: 1;
  events: RuntimeEvent[];
}
```

```json
{
  "version": 1,
  "daemon": "running",
  "management": "connected",
  "client_count": 1,
  "clients": [{
    "client_id": "c0d4f871-6ea6-42b3-9e7b-f00cc1ec354e",
    "client_name": "alice-laptop",
    "remote_address": "203.0.113.10:53210",
    "virtual_address": "10.42.0.30"
  }]
}
```

### 配置返回模型

```ts
interface Configuration {
  version: 1;
  server: {
    endpoint: string;
    protocol: "udp" | "tcp";
    family: "auto" | "ipv4" | "ipv6";
    port: number;
    client_to_client: boolean;
  };
  ipv4: {
    network: string;
    dynamic_pool_size: number;
    nat_enabled: boolean;
    nat_interface: string;
    redirect_gateway: boolean;
    dns: string[];
    routes: string[];
  };
  logging: { max_bytes: number; backups: number };
}

interface AppliedConfigurationResponse {
  revision: number;
  digest: Digest;
  config: Configuration;
}

interface DesiredConfigurationResponse {
  digest: Digest;
  config: Configuration;
}

interface ConfigurationComparison {
  initial: boolean;
  current_revision: number;
  target_revision: number;
  current_digest?: Digest;
  desired_digest: Digest;
  in_sync: boolean;
  changes: Array<{ field: string; before: unknown; after: unknown }>;
  impact: {
    restart_required: boolean;
    address_remap: boolean;
    firewall_reconcile: boolean;
    profile_redistribution: boolean;
    derived_artifacts: string[];
  };
}

interface ConfigurationPlanResponse {
  version: 1;
  instance_id: UUID;
  configuration: ConfigurationComparison;
  address_changes: Array<{
    client: { id: UUID; name: string };
    before: { mode: string; address: string | null; state: string };
    after: { mode: string; address: string | null; state: string };
  }>;
  artifacts: Array<{
    owner_kind: string; owner_id: string; kind: string; key: string;
    action: "regenerate" | "delete";
  }>;
  profile_redistribution: Array<{ id: UUID; name: string }>;
  firewall: {
    reconcile: boolean;
    before: FirewallState | null;
    after: FirewallState | null;
  };
}

interface FirewallState {
  network: string;
  nat_enabled: boolean;
  nat_interface: string;
  routes: string[];
}

interface ApplyConfigurationResponse {
  version: 1;
  applied: boolean;
  operation_id?: UUID;
  activation: {
    restart_required: boolean;
    runtime_restarted: boolean;
    profile_redistribution: Array<{ id: UUID; name: string }>;
  };
  plan: ConfigurationPlanResponse;
}
```

完整字段约束、枚举、nullable 状态和每个错误状态以 `/docs/openapi.json` 为准。

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
