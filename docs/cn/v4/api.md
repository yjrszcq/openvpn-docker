# REST API v1

REST API 为现有客户端、配置、状态和 runtime service 提供经过认证的远程入口。它不会替代 SQLite 权威状态、operation journal、runtime 锁或本地 `ovpn` CLI。

API 默认关闭，内部仅使用 HTTP，不管理 TLS 证书。远程访问时必须放在 HTTPS 反向代理后面。

启用 API 后可直接访问内置开发文档：

- `http://<OVPN_API_LISTEN>/docs/`：面向前端的完整接口文档。22 条接口各自独立展示自己的完整 HTTP 请求、参数、body 字段、成功返回、错误返回和 JSON 示例，不需要跳到共享模型区拼装。页面默认跟随浏览器语言（中文浏览器使用中文，其他语言使用英文），也可通过右上角手动切换并保留选择。
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

以下 22 条接口均为独立完整契约。每条接口分别列出 Header、Path、Query 和 JSON Body 参数，字段表包含位置、类型、必填、格式、示例、约束及用途；每个返回状态也分别列出内容类型、示例和字段。除 `/healthz` 外，所有请求都必须发送 Bearer API key。

### 1. `GET /healthz`

Check API process liveness. Unauthenticated liveness check. It does not report OpenVPN or storage health.

#### 请求参数

无请求参数，也不接受请求体。

#### 请求示例

```http
GET /healthz HTTP/1.1
```

#### 返回

##### `200 OK`

API process is alive.

内容类型：`application/json`

返回示例：

```json
{
  "status": "ok"
}
```

返回字段：

| 字段 | 类型 | 必填 | 格式 / 示例 / 约束 | 用途说明 |
|---|---|---|---|---|
| `status` | `string` | 是 | 固定值: "ok" | - |

##### `405 Method Not Allowed`

HTTP method is not accepted; inspect the Allow header.

内容类型：`application/json`

返回示例：

```json
{
  "error": {
    "kind": "method_not_allowed",
    "message": "method is not allowed",
    "request_id": "33ba813e-fc63-4af8-b338-7f7486f82202"
  }
}
```

返回字段：

| 字段 | 类型 | 必填 | 格式 / 示例 / 约束 | 用途说明 |
|---|---|---|---|---|
| `error` | `object` | 是 | - | - |
| `error.kind` | `string` | 是 | 示例: "method_not_allowed" | - |
| `error.message` | `string` | 是 | 示例: "method is not allowed" | - |
| `error.request_id` | `string` | 是 | 格式: uuid；示例: "33ba813e-fc63-4af8-b338-7f7486f82202" | - |

### 2. `GET /api/v1/version`

Read build and compatibility metadata

#### 请求参数

##### Header 参数

| 字段 | 位置 | 类型 | 必填 | 格式 / 示例 / 约束 | 用途说明 |
|---|---|---|---|---|---|
| `Authorization` | `header` | `string` | 是 | Bearer ovpn_v1.<uuid>.<secret> | API 身份认证凭据。Bearer 后填写服务生成的 API Key。 |

#### 请求示例

```http
GET /api/v1/version HTTP/1.1
Authorization: Bearer ovpn_v1.<uuid>.<secret>
```

#### 返回

##### `200 OK`

Build and compatibility metadata.

内容类型：`application/json`

返回示例：

```json
{
  "version": "4.0.2",
  "data_schema": 4,
  "commit": "dd9b5213f5002a7e69f160e3fd2615e0b3d8d224",
  "build_date": "2026-08-19T09:45:40Z",
  "go_version": "go1.26.5",
  "dependencies": {
    "sqlite": "github.com/mattn/go-sqlite3 v1.14.48",
    "yaml": "go.yaml.in/yaml/v3 v3.0.4"
  },
  "compatibility": {
    "contract_version": 1,
    "adapter": "openvpn-2.7",
    "template_family": "openvpn-2.7",
    "supported_openvpn_versions": [
      "2.7.6"
    ]
  }
}
```

返回字段：

| 字段 | 类型 | 必填 | 格式 / 示例 / 约束 | 用途说明 |
|---|---|---|---|---|
| `version` | `string` | 是 | 示例: "4.0.2" | - |
| `data_schema` | `integer` | 是 | 示例: 4 | - |
| `commit` | `string` | 是 | 示例: "dd9b5213f5002a7e69f160e3fd2615e0b3d8d224" | - |
| `build_date` | `string` | 是 | 示例: "2026-08-19T09:45:40Z" | - |
| `go_version` | `string` | 是 | 示例: "go1.26.5" | - |
| `dependencies` | `object` | 是 | - | - |
| `dependencies.sqlite` | `string` | 是 | 示例: "github.com/mattn/go-sqlite3 v1.14.48" | - |
| `dependencies.yaml` | `string` | 是 | 示例: "go.yaml.in/yaml/v3 v3.0.4" | - |
| `compatibility` | `object` | 是 | - | - |
| `compatibility.contract_version` | `integer` | 是 | 示例: 1 | - |
| `compatibility.adapter` | `string` | 是 | 示例: "openvpn-2.7" | - |
| `compatibility.template_family` | `string` | 是 | 示例: "openvpn-2.7" | - |
| `compatibility.supported_openvpn_versions` | `string[]` | 是 | 示例: ["2.7.6"] | - |

##### `401 Unauthorized`

Bearer API key is missing, malformed, unknown, or deleted.

内容类型：`application/json`

返回示例：

```json
{
  "error": {
    "kind": "unauthenticated",
    "message": "API key is missing or invalid",
    "request_id": "33ba813e-fc63-4af8-b338-7f7486f82202"
  }
}
```

返回字段：

| 字段 | 类型 | 必填 | 格式 / 示例 / 约束 | 用途说明 |
|---|---|---|---|---|
| `error` | `object` | 是 | - | - |
| `error.kind` | `string` | 是 | 示例: "unauthenticated" | - |
| `error.message` | `string` | 是 | 示例: "API key is missing or invalid" | - |
| `error.request_id` | `string` | 是 | 格式: uuid；示例: "33ba813e-fc63-4af8-b338-7f7486f82202" | - |

##### `405 Method Not Allowed`

HTTP method is not accepted; inspect the Allow header.

内容类型：`application/json`

返回示例：

```json
{
  "error": {
    "kind": "method_not_allowed",
    "message": "method is not allowed",
    "request_id": "33ba813e-fc63-4af8-b338-7f7486f82202"
  }
}
```

返回字段：

| 字段 | 类型 | 必填 | 格式 / 示例 / 约束 | 用途说明 |
|---|---|---|---|---|
| `error` | `object` | 是 | - | - |
| `error.kind` | `string` | 是 | 示例: "method_not_allowed" | - |
| `error.message` | `string` | 是 | 示例: "method is not allowed" | - |
| `error.request_id` | `string` | 是 | 格式: uuid；示例: "33ba813e-fc63-4af8-b338-7f7486f82202" | - |

##### `503 Service Unavailable`

Authentication storage, PKI, OpenVPN, broker, supervisor, or another required dependency is unavailable.

内容类型：`application/json`

返回示例：

```json
{
  "error": {
    "kind": "runtime_unavailable",
    "message": "OpenVPN runtime is unavailable",
    "request_id": "33ba813e-fc63-4af8-b338-7f7486f82202"
  }
}
```

返回字段：

| 字段 | 类型 | 必填 | 格式 / 示例 / 约束 | 用途说明 |
|---|---|---|---|---|
| `error` | `object` | 是 | - | - |
| `error.kind` | `string` | 是 | 示例: "runtime_unavailable" | - |
| `error.message` | `string` | 是 | 示例: "OpenVPN runtime is unavailable" | - |
| `error.request_id` | `string` | 是 | 格式: uuid；示例: "33ba813e-fc63-4af8-b338-7f7486f82202" | - |

### 3. `GET /api/v1/state`

Read instance health summary

#### 请求参数

##### Header 参数

| 字段 | 位置 | 类型 | 必填 | 格式 / 示例 / 约束 | 用途说明 |
|---|---|---|---|---|---|
| `Authorization` | `header` | `string` | 是 | Bearer ovpn_v1.<uuid>.<secret> | API 身份认证凭据。Bearer 后填写服务生成的 API Key。 |

#### 请求示例

```http
GET /api/v1/state HTTP/1.1
Authorization: Bearer ovpn_v1.<uuid>.<secret>
```

#### 返回

##### `200 OK`

Instance state summary.

内容类型：`application/json`

返回示例：

```json
{
  "version": 1,
  "state": "HEALTHY",
  "data_schema": 4,
  "instance_id": "bbffeb8c-2d11-4613-874d-b5fcc804a608",
  "revision": 12,
  "scanned_at": "2026-08-19T09:55:07Z",
  "issue_count": 0,
  "pending_operation_count": 0
}
```

返回字段：

| 字段 | 类型 | 必填 | 格式 / 示例 / 约束 | 用途说明 |
|---|---|---|---|---|
| `version` | `integer` | 是 | 固定值: 1 | - |
| `state` | `string` | 是 | 可选值: "EMPTY", "HEALTHY", "DEGRADED_REPAIRABLE", "DEGRADED_RECOVERABLE", "DEGRADED_REISSUABLE", "CRITICAL", "UNRECOVERABLE" | - |
| `data_schema` | `integer` | 是 | 示例: 4 | - |
| `instance_id` | `string` | 否 | 格式: uuid；示例: "bbffeb8c-2d11-4613-874d-b5fcc804a608" | - |
| `revision` | `integer` | 否 | 最小值: 1；示例: 12 | - |
| `scanned_at` | `string` | 是 | 格式: date-time；示例: "2026-08-19T09:55:07Z" | - |
| `issue_count` | `integer` | 是 | 最小值: 0 | - |
| `pending_operation_count` | `integer` | 是 | 最小值: 0 | - |
| `issues` | `object[]` | 否 | - | - |
| `issues[].id` | `string` | 否 | 示例: "string" | - |
| `issues[].severity` | `string` | 否 | 可选值: "repairable", "recoverable", "reissuable", "critical", "unrecoverable" | - |
| `issues[].action` | `string` | 否 | 示例: "string" | - |
| `issues[].target` | `string` | 否 | 示例: "string" | - |
| `issues[].owner_id` | `string` | 否 | 示例: "string" | - |
| `issues[].artifact_kind` | `string` | 否 | 示例: "string" | - |
| `issues[].detail` | `string` | 否 | 示例: "string" | - |

##### `401 Unauthorized`

Bearer API key is missing, malformed, unknown, or deleted.

内容类型：`application/json`

返回示例：

```json
{
  "error": {
    "kind": "unauthenticated",
    "message": "API key is missing or invalid",
    "request_id": "33ba813e-fc63-4af8-b338-7f7486f82202"
  }
}
```

返回字段：

| 字段 | 类型 | 必填 | 格式 / 示例 / 约束 | 用途说明 |
|---|---|---|---|---|
| `error` | `object` | 是 | - | - |
| `error.kind` | `string` | 是 | 示例: "unauthenticated" | - |
| `error.message` | `string` | 是 | 示例: "API key is missing or invalid" | - |
| `error.request_id` | `string` | 是 | 格式: uuid；示例: "33ba813e-fc63-4af8-b338-7f7486f82202" | - |

##### `405 Method Not Allowed`

HTTP method is not accepted; inspect the Allow header.

内容类型：`application/json`

返回示例：

```json
{
  "error": {
    "kind": "method_not_allowed",
    "message": "method is not allowed",
    "request_id": "33ba813e-fc63-4af8-b338-7f7486f82202"
  }
}
```

返回字段：

| 字段 | 类型 | 必填 | 格式 / 示例 / 约束 | 用途说明 |
|---|---|---|---|---|
| `error` | `object` | 是 | - | - |
| `error.kind` | `string` | 是 | 示例: "method_not_allowed" | - |
| `error.message` | `string` | 是 | 示例: "method is not allowed" | - |
| `error.request_id` | `string` | 是 | 格式: uuid；示例: "33ba813e-fc63-4af8-b338-7f7486f82202" | - |

##### `500 Internal Server Error`

Unclassified internal failure with no implementation details exposed.

内容类型：`application/json`

返回示例：

```json
{
  "error": {
    "kind": "internal_error",
    "message": "request could not be completed",
    "request_id": "33ba813e-fc63-4af8-b338-7f7486f82202"
  }
}
```

返回字段：

| 字段 | 类型 | 必填 | 格式 / 示例 / 约束 | 用途说明 |
|---|---|---|---|---|
| `error` | `object` | 是 | - | - |
| `error.kind` | `string` | 是 | 示例: "internal_error" | - |
| `error.message` | `string` | 是 | 示例: "request could not be completed" | - |
| `error.request_id` | `string` | 是 | 格式: uuid；示例: "33ba813e-fc63-4af8-b338-7f7486f82202" | - |

##### `503 Service Unavailable`

Authentication storage, PKI, OpenVPN, broker, supervisor, or another required dependency is unavailable.

内容类型：`application/json`

返回示例：

```json
{
  "error": {
    "kind": "runtime_unavailable",
    "message": "OpenVPN runtime is unavailable",
    "request_id": "33ba813e-fc63-4af8-b338-7f7486f82202"
  }
}
```

返回字段：

| 字段 | 类型 | 必填 | 格式 / 示例 / 约束 | 用途说明 |
|---|---|---|---|---|
| `error` | `object` | 是 | - | - |
| `error.kind` | `string` | 是 | 示例: "runtime_unavailable" | - |
| `error.message` | `string` | 是 | 示例: "OpenVPN runtime is unavailable" | - |
| `error.request_id` | `string` | 是 | 格式: uuid；示例: "33ba813e-fc63-4af8-b338-7f7486f82202" | - |

### 4. `GET /api/v1/state/doctor`

Read detailed instance diagnostics. Returns the state summary plus an issues array when diagnostic issues exist.

#### 请求参数

##### Header 参数

| 字段 | 位置 | 类型 | 必填 | 格式 / 示例 / 约束 | 用途说明 |
|---|---|---|---|---|---|
| `Authorization` | `header` | `string` | 是 | Bearer ovpn_v1.<uuid>.<secret> | API 身份认证凭据。Bearer 后填写服务生成的 API Key。 |

#### 请求示例

```http
GET /api/v1/state/doctor HTTP/1.1
Authorization: Bearer ovpn_v1.<uuid>.<secret>
```

#### 返回

##### `200 OK`

Detailed instance diagnostics. `issues` is omitted when empty.

内容类型：`application/json`

返回示例：

```json
{
  "version": 1,
  "state": "DEGRADED_REPAIRABLE",
  "data_schema": 4,
  "instance_id": "bbffeb8c-2d11-4613-874d-b5fcc804a608",
  "revision": 12,
  "scanned_at": "2026-08-19T09:55:07Z",
  "issue_count": 1,
  "pending_operation_count": 0,
  "issues": [
    {
      "id": "DECLARATIVE_CONFIG_UNAVAILABLE",
      "severity": "repairable",
      "action": "export-config",
      "target": "/etc/ovpn-conf/config.yaml",
      "detail": "declarative configuration is unavailable"
    }
  ]
}
```

返回字段：

| 字段 | 类型 | 必填 | 格式 / 示例 / 约束 | 用途说明 |
|---|---|---|---|---|
| `version` | `integer` | 是 | 固定值: 1 | - |
| `state` | `string` | 是 | 可选值: "EMPTY", "HEALTHY", "DEGRADED_REPAIRABLE", "DEGRADED_RECOVERABLE", "DEGRADED_REISSUABLE", "CRITICAL", "UNRECOVERABLE" | - |
| `data_schema` | `integer` | 是 | 示例: 4 | - |
| `instance_id` | `string` | 否 | 格式: uuid；示例: "bbffeb8c-2d11-4613-874d-b5fcc804a608" | - |
| `revision` | `integer` | 否 | 最小值: 1；示例: 12 | - |
| `scanned_at` | `string` | 是 | 格式: date-time；示例: "2026-08-19T09:55:07Z" | - |
| `issue_count` | `integer` | 是 | 最小值: 0；示例: 1 | - |
| `pending_operation_count` | `integer` | 是 | 最小值: 0 | - |
| `issues` | `object[]` | 否 | 示例: [{"id":"DECLARATIVE_CONFIG_UNAVAILABLE","severity":"repairable","action":"export-config","target":"/etc/ovpn-conf/config.yaml","detail":"declarative configuration is unavailable"}] | - |
| `issues[].id` | `string` | 否 | 示例: "DECLARATIVE_CONFIG_UNAVAILABLE" | - |
| `issues[].severity` | `string` | 否 | 可选值: "repairable", "recoverable", "reissuable", "critical", "unrecoverable" | - |
| `issues[].action` | `string` | 否 | 示例: "export-config" | - |
| `issues[].target` | `string` | 否 | 示例: "/etc/ovpn-conf/config.yaml" | - |
| `issues[].owner_id` | `string` | 否 | 示例: "string" | - |
| `issues[].artifact_kind` | `string` | 否 | 示例: "string" | - |
| `issues[].detail` | `string` | 否 | 示例: "declarative configuration is unavailable" | - |

##### `401 Unauthorized`

Bearer API key is missing, malformed, unknown, or deleted.

内容类型：`application/json`

返回示例：

```json
{
  "error": {
    "kind": "unauthenticated",
    "message": "API key is missing or invalid",
    "request_id": "33ba813e-fc63-4af8-b338-7f7486f82202"
  }
}
```

返回字段：

| 字段 | 类型 | 必填 | 格式 / 示例 / 约束 | 用途说明 |
|---|---|---|---|---|
| `error` | `object` | 是 | - | - |
| `error.kind` | `string` | 是 | 示例: "unauthenticated" | - |
| `error.message` | `string` | 是 | 示例: "API key is missing or invalid" | - |
| `error.request_id` | `string` | 是 | 格式: uuid；示例: "33ba813e-fc63-4af8-b338-7f7486f82202" | - |

##### `405 Method Not Allowed`

HTTP method is not accepted; inspect the Allow header.

内容类型：`application/json`

返回示例：

```json
{
  "error": {
    "kind": "method_not_allowed",
    "message": "method is not allowed",
    "request_id": "33ba813e-fc63-4af8-b338-7f7486f82202"
  }
}
```

返回字段：

| 字段 | 类型 | 必填 | 格式 / 示例 / 约束 | 用途说明 |
|---|---|---|---|---|
| `error` | `object` | 是 | - | - |
| `error.kind` | `string` | 是 | 示例: "method_not_allowed" | - |
| `error.message` | `string` | 是 | 示例: "method is not allowed" | - |
| `error.request_id` | `string` | 是 | 格式: uuid；示例: "33ba813e-fc63-4af8-b338-7f7486f82202" | - |

##### `500 Internal Server Error`

Unclassified internal failure with no implementation details exposed.

内容类型：`application/json`

返回示例：

```json
{
  "error": {
    "kind": "internal_error",
    "message": "request could not be completed",
    "request_id": "33ba813e-fc63-4af8-b338-7f7486f82202"
  }
}
```

返回字段：

| 字段 | 类型 | 必填 | 格式 / 示例 / 约束 | 用途说明 |
|---|---|---|---|---|
| `error` | `object` | 是 | - | - |
| `error.kind` | `string` | 是 | 示例: "internal_error" | - |
| `error.message` | `string` | 是 | 示例: "request could not be completed" | - |
| `error.request_id` | `string` | 是 | 格式: uuid；示例: "33ba813e-fc63-4af8-b338-7f7486f82202" | - |

##### `503 Service Unavailable`

Authentication storage, PKI, OpenVPN, broker, supervisor, or another required dependency is unavailable.

内容类型：`application/json`

返回示例：

```json
{
  "error": {
    "kind": "runtime_unavailable",
    "message": "OpenVPN runtime is unavailable",
    "request_id": "33ba813e-fc63-4af8-b338-7f7486f82202"
  }
}
```

返回字段：

| 字段 | 类型 | 必填 | 格式 / 示例 / 约束 | 用途说明 |
|---|---|---|---|---|
| `error` | `object` | 是 | - | - |
| `error.kind` | `string` | 是 | 示例: "runtime_unavailable" | - |
| `error.message` | `string` | 是 | 示例: "OpenVPN runtime is unavailable" | - |
| `error.request_id` | `string` | 是 | 格式: uuid；示例: "33ba813e-fc63-4af8-b338-7f7486f82202" | - |

### 5. `GET /api/v1/clients`

List active, revoked, and deleted clients

#### 请求参数

##### Header 参数

| 字段 | 位置 | 类型 | 必填 | 格式 / 示例 / 约束 | 用途说明 |
|---|---|---|---|---|---|
| `Authorization` | `header` | `string` | 是 | Bearer ovpn_v1.<uuid>.<secret> | API 身份认证凭据。Bearer 后填写服务生成的 API Key。 |

#### 请求示例

```http
GET /api/v1/clients HTTP/1.1
Authorization: Bearer ovpn_v1.<uuid>.<secret>
```

#### 返回

##### `200 OK`

Client list.

内容类型：`application/json`

返回示例：

```json
{
  "version": 1,
  "clients": [
    {
      "id": "c0d4f871-6ea6-42b3-9e7b-f00cc1ec354e",
      "name": "alice-laptop",
      "status": "active",
      "ipv4": {
        "mode": "static",
        "address": "10.42.0.30",
        "state": "configured"
      },
      "connection": "connected"
    }
  ]
}
```

返回字段：

| 字段 | 类型 | 必填 | 格式 / 示例 / 约束 | 用途说明 |
|---|---|---|---|---|
| `version` | `integer` | 是 | 固定值: 1 | - |
| `clients` | `object[]` | 是 | 示例: [{"id":"c0d4f871-6ea6-42b3-9e7b-f00cc1ec354e","name":"alice-laptop","status":"active","ipv4":{"mode":"static","address":"10.42.0.30","state":"configured"},"connection":"connected"}] | - |
| `clients[].id` | `string` | 是 | 格式: uuid；示例: "c0d4f871-6ea6-42b3-9e7b-f00cc1ec354e" | - |
| `clients[].name` | `string` | 是 | 格式: `^[A-Za-z0-9][A-Za-z0-9_.-]*$`；示例: "alice-laptop" | - |
| `clients[].status` | `string` | 是 | 可选值: "active", "revoked", "deleted" | - |
| `clients[].ipv4` | `object` | 是 | - | - |
| `clients[].ipv4.mode` | `string` | 是 | 可选值: "none", "static", "dynamic" | - |
| `clients[].ipv4.address` | `string \| null` | 是 | 格式: ipv4；示例: "10.42.0.30" | - |
| `clients[].ipv4.state` | `string` | 是 | 可选值: "configured", "retained", "unavailable" | - |
| `clients[].connection` | `string` | 否 | 示例: "connected" | - |

##### `401 Unauthorized`

Bearer API key is missing, malformed, unknown, or deleted.

内容类型：`application/json`

返回示例：

```json
{
  "error": {
    "kind": "unauthenticated",
    "message": "API key is missing or invalid",
    "request_id": "33ba813e-fc63-4af8-b338-7f7486f82202"
  }
}
```

返回字段：

| 字段 | 类型 | 必填 | 格式 / 示例 / 约束 | 用途说明 |
|---|---|---|---|---|
| `error` | `object` | 是 | - | - |
| `error.kind` | `string` | 是 | 示例: "unauthenticated" | - |
| `error.message` | `string` | 是 | 示例: "API key is missing or invalid" | - |
| `error.request_id` | `string` | 是 | 格式: uuid；示例: "33ba813e-fc63-4af8-b338-7f7486f82202" | - |

##### `405 Method Not Allowed`

HTTP method is not accepted; inspect the Allow header.

内容类型：`application/json`

返回示例：

```json
{
  "error": {
    "kind": "method_not_allowed",
    "message": "method is not allowed",
    "request_id": "33ba813e-fc63-4af8-b338-7f7486f82202"
  }
}
```

返回字段：

| 字段 | 类型 | 必填 | 格式 / 示例 / 约束 | 用途说明 |
|---|---|---|---|---|
| `error` | `object` | 是 | - | - |
| `error.kind` | `string` | 是 | 示例: "method_not_allowed" | - |
| `error.message` | `string` | 是 | 示例: "method is not allowed" | - |
| `error.request_id` | `string` | 是 | 格式: uuid；示例: "33ba813e-fc63-4af8-b338-7f7486f82202" | - |

##### `500 Internal Server Error`

Unclassified internal failure with no implementation details exposed.

内容类型：`application/json`

返回示例：

```json
{
  "error": {
    "kind": "internal_error",
    "message": "request could not be completed",
    "request_id": "33ba813e-fc63-4af8-b338-7f7486f82202"
  }
}
```

返回字段：

| 字段 | 类型 | 必填 | 格式 / 示例 / 约束 | 用途说明 |
|---|---|---|---|---|
| `error` | `object` | 是 | - | - |
| `error.kind` | `string` | 是 | 示例: "internal_error" | - |
| `error.message` | `string` | 是 | 示例: "request could not be completed" | - |
| `error.request_id` | `string` | 是 | 格式: uuid；示例: "33ba813e-fc63-4af8-b338-7f7486f82202" | - |

##### `503 Service Unavailable`

Authentication storage, PKI, OpenVPN, broker, supervisor, or another required dependency is unavailable.

内容类型：`application/json`

返回示例：

```json
{
  "error": {
    "kind": "runtime_unavailable",
    "message": "OpenVPN runtime is unavailable",
    "request_id": "33ba813e-fc63-4af8-b338-7f7486f82202"
  }
}
```

返回字段：

| 字段 | 类型 | 必填 | 格式 / 示例 / 约束 | 用途说明 |
|---|---|---|---|---|
| `error` | `object` | 是 | - | - |
| `error.kind` | `string` | 是 | 示例: "runtime_unavailable" | - |
| `error.message` | `string` | 是 | 示例: "OpenVPN runtime is unavailable" | - |
| `error.request_id` | `string` | 是 | 格式: uuid；示例: "33ba813e-fc63-4af8-b338-7f7486f82202" | - |

### 6. `POST /api/v1/clients`

Create client credentials and profile

#### 请求参数

##### Header 参数

| 字段 | 位置 | 类型 | 必填 | 格式 / 示例 / 约束 | 用途说明 |
|---|---|---|---|---|---|
| `Authorization` | `header` | `string` | 是 | Bearer ovpn_v1.<uuid>.<secret> | API 身份认证凭据。Bearer 后填写服务生成的 API Key。 |
| `Content-Type` | `header` | `string` | 是 | application/json | 声明请求体使用 JSON 格式。 |

##### JSON Body 字段

| 字段 | 位置 | 类型 | 必填 | 格式 / 示例 / 约束 | 用途说明 |
|---|---|---|---|---|---|
| `name` | `body` | `string` | 是 | 格式: `^[A-Za-z0-9][A-Za-z0-9_.-]*$`；示例: "alice-laptop" | Stable client name. Starts with an alphanumeric character and may contain letters, digits, underscores, dots, and hyphens. |
| `ipv4` | `body` | `string` | 是 | 示例: "auto" | IPv4 allocation intent: auto, dynamic, or a valid static IPv4 address. |

#### 请求示例

```http
POST /api/v1/clients HTTP/1.1
Authorization: Bearer ovpn_v1.<uuid>.<secret>
Content-Type: application/json

{
  "name": "alice-laptop",
  "ipv4": "auto"
}
```

#### 返回

##### `201 Created`

Client created.

内容类型：`application/json`

返回示例：

```json
{
  "version": 1,
  "operation_id": "7a21e1f8-1ce1-4694-977f-b275eadb8651",
  "client": {
    "id": "c0d4f871-6ea6-42b3-9e7b-f00cc1ec354e",
    "name": "alice-laptop",
    "status": "active",
    "ipv4": {
      "mode": "static",
      "address": "10.42.0.2",
      "state": "configured"
    }
  },
  "kick_required": false,
  "profile_redistribution_required": true
}
```

返回字段：

| 字段 | 类型 | 必填 | 格式 / 示例 / 约束 | 用途说明 |
|---|---|---|---|---|
| `version` | `integer` | 是 | 固定值: 1 | - |
| `operation_id` | `string` | 是 | 格式: uuid；示例: "7a21e1f8-1ce1-4694-977f-b275eadb8651" | - |
| `client` | `object` | 是 | - | - |
| `client.id` | `string` | 是 | 格式: uuid；示例: "c0d4f871-6ea6-42b3-9e7b-f00cc1ec354e" | - |
| `client.name` | `string` | 是 | 格式: `^[A-Za-z0-9][A-Za-z0-9_.-]*$`；示例: "alice-laptop" | - |
| `client.status` | `string` | 是 | 可选值: "active", "revoked", "deleted" | - |
| `client.ipv4` | `object` | 是 | - | - |
| `client.ipv4.mode` | `string` | 是 | 可选值: "none", "static", "dynamic" | - |
| `client.ipv4.address` | `string \| null` | 是 | 格式: ipv4；示例: "10.42.0.2" | - |
| `client.ipv4.state` | `string` | 是 | 可选值: "configured", "retained", "unavailable" | - |
| `client.connection` | `string` | 否 | 示例: "string" | - |
| `kick_required` | `boolean` | 是 | 示例: false | - |
| `profile_redistribution_required` | `boolean` | 是 | 示例: true | - |
| `runtime` | `object` | 否 | - | - |
| `runtime.client_id` | `string` | 否 | 格式: uuid；示例: "c0d4f871-6ea6-42b3-9e7b-f00cc1ec354e" | - |
| `runtime.status` | `string` | 否 | 可选值: "ok", "unavailable" | - |
| `runtime.result` | `object` | 否 | - | - |
| `runtime.result.version` | `integer` | 否 | 固定值: 1 | - |
| `runtime.result.client_id` | `string` | 否 | 格式: uuid；示例: "c0d4f871-6ea6-42b3-9e7b-f00cc1ec354e" | - |
| `runtime.result.client_name` | `string` | 否 | 示例: "string" | - |
| `runtime.result.was_connected` | `boolean` | 否 | 示例: false | - |
| `runtime.result.disconnected` | `boolean` | 否 | 示例: false | - |
| `runtime.result.connections` | `integer` | 否 | 最小值: 0 | - |

##### `400 Bad Request`

Malformed path, query, header, media type, body, or JSON.

内容类型：`application/json`

返回示例：

```json
{
  "error": {
    "kind": "invalid_json",
    "message": "request body is invalid",
    "request_id": "33ba813e-fc63-4af8-b338-7f7486f82202"
  }
}
```

返回字段：

| 字段 | 类型 | 必填 | 格式 / 示例 / 约束 | 用途说明 |
|---|---|---|---|---|
| `error` | `object` | 是 | - | - |
| `error.kind` | `string` | 是 | 示例: "invalid_json" | - |
| `error.message` | `string` | 是 | 示例: "request body is invalid" | - |
| `error.request_id` | `string` | 是 | 格式: uuid；示例: "33ba813e-fc63-4af8-b338-7f7486f82202" | - |

##### `401 Unauthorized`

Bearer API key is missing, malformed, unknown, or deleted.

内容类型：`application/json`

返回示例：

```json
{
  "error": {
    "kind": "unauthenticated",
    "message": "API key is missing or invalid",
    "request_id": "33ba813e-fc63-4af8-b338-7f7486f82202"
  }
}
```

返回字段：

| 字段 | 类型 | 必填 | 格式 / 示例 / 约束 | 用途说明 |
|---|---|---|---|---|
| `error` | `object` | 是 | - | - |
| `error.kind` | `string` | 是 | 示例: "unauthenticated" | - |
| `error.message` | `string` | 是 | 示例: "API key is missing or invalid" | - |
| `error.request_id` | `string` | 是 | 格式: uuid；示例: "33ba813e-fc63-4af8-b338-7f7486f82202" | - |

##### `409 Conflict`

Digest, revision, state, uniqueness, lock, or recovery conflict.

内容类型：`application/json`

返回示例：

```json
{
  "error": {
    "kind": "configuration_conflict",
    "message": "configuration state changed or is busy",
    "request_id": "33ba813e-fc63-4af8-b338-7f7486f82202"
  }
}
```

返回字段：

| 字段 | 类型 | 必填 | 格式 / 示例 / 约束 | 用途说明 |
|---|---|---|---|---|
| `error` | `object` | 是 | - | - |
| `error.kind` | `string` | 是 | 示例: "configuration_conflict" | - |
| `error.message` | `string` | 是 | 示例: "configuration state changed or is busy" | - |
| `error.request_id` | `string` | 是 | 格式: uuid；示例: "33ba813e-fc63-4af8-b338-7f7486f82202" | - |

##### `422 Unprocessable Entity`

JSON is valid but violates client, IPv4, or configuration semantics.

内容类型：`application/json`

返回示例：

```json
{
  "error": {
    "kind": "invalid_client",
    "message": "client request is not valid for the current state",
    "request_id": "33ba813e-fc63-4af8-b338-7f7486f82202"
  }
}
```

返回字段：

| 字段 | 类型 | 必填 | 格式 / 示例 / 约束 | 用途说明 |
|---|---|---|---|---|
| `error` | `object` | 是 | - | - |
| `error.kind` | `string` | 是 | 示例: "invalid_client" | - |
| `error.message` | `string` | 是 | 示例: "client request is not valid for the current state" | - |
| `error.request_id` | `string` | 是 | 格式: uuid；示例: "33ba813e-fc63-4af8-b338-7f7486f82202" | - |

##### `500 Internal Server Error`

Unclassified internal failure with no implementation details exposed.

内容类型：`application/json`

返回示例：

```json
{
  "error": {
    "kind": "internal_error",
    "message": "request could not be completed",
    "request_id": "33ba813e-fc63-4af8-b338-7f7486f82202"
  }
}
```

返回字段：

| 字段 | 类型 | 必填 | 格式 / 示例 / 约束 | 用途说明 |
|---|---|---|---|---|
| `error` | `object` | 是 | - | - |
| `error.kind` | `string` | 是 | 示例: "internal_error" | - |
| `error.message` | `string` | 是 | 示例: "request could not be completed" | - |
| `error.request_id` | `string` | 是 | 格式: uuid；示例: "33ba813e-fc63-4af8-b338-7f7486f82202" | - |

##### `503 Service Unavailable`

Authentication storage, PKI, OpenVPN, broker, supervisor, or another required dependency is unavailable.

内容类型：`application/json`

返回示例：

```json
{
  "error": {
    "kind": "runtime_unavailable",
    "message": "OpenVPN runtime is unavailable",
    "request_id": "33ba813e-fc63-4af8-b338-7f7486f82202"
  }
}
```

返回字段：

| 字段 | 类型 | 必填 | 格式 / 示例 / 约束 | 用途说明 |
|---|---|---|---|---|
| `error` | `object` | 是 | - | - |
| `error.kind` | `string` | 是 | 示例: "runtime_unavailable" | - |
| `error.message` | `string` | 是 | 示例: "OpenVPN runtime is unavailable" | - |
| `error.request_id` | `string` | 是 | 格式: uuid；示例: "33ba813e-fc63-4af8-b338-7f7486f82202" | - |

### 7. `GET /api/v1/clients/{client_id}`

Read one client

#### 请求参数

##### Header 参数

| 字段 | 位置 | 类型 | 必填 | 格式 / 示例 / 约束 | 用途说明 |
|---|---|---|---|---|---|
| `Authorization` | `header` | `string` | 是 | Bearer ovpn_v1.<uuid>.<secret> | API 身份认证凭据。Bearer 后填写服务生成的 API Key。 |

##### Path 参数

| 字段 | 位置 | 类型 | 必填 | 格式 / 示例 / 约束 | 用途说明 |
|---|---|---|---|---|---|
| `client_id` | `path` | `string` | 是 | 格式: uuid；示例: "c0d4f871-6ea6-42b3-9e7b-f00cc1ec354e" | Complete canonical immutable client UUID; names and UUID prefixes are rejected. |

#### 请求示例

```http
GET /api/v1/clients/c0d4f871-6ea6-42b3-9e7b-f00cc1ec354e HTTP/1.1
Authorization: Bearer ovpn_v1.<uuid>.<secret>
```

#### 返回

##### `200 OK`

Client detail.

内容类型：`application/json`

返回示例：

```json
{
  "id": "c0d4f871-6ea6-42b3-9e7b-f00cc1ec354e",
  "name": "alice-laptop",
  "status": "active",
  "ipv4": {
    "mode": "static",
    "address": "10.42.0.30",
    "state": "configured"
  }
}
```

返回字段：

| 字段 | 类型 | 必填 | 格式 / 示例 / 约束 | 用途说明 |
|---|---|---|---|---|
| `id` | `string` | 是 | 格式: uuid；示例: "c0d4f871-6ea6-42b3-9e7b-f00cc1ec354e" | - |
| `name` | `string` | 是 | 格式: `^[A-Za-z0-9][A-Za-z0-9_.-]*$`；示例: "alice-laptop" | - |
| `status` | `string` | 是 | 可选值: "active", "revoked", "deleted" | - |
| `ipv4` | `object` | 是 | - | - |
| `ipv4.mode` | `string` | 是 | 可选值: "none", "static", "dynamic" | - |
| `ipv4.address` | `string \| null` | 是 | 格式: ipv4；示例: "10.42.0.30" | - |
| `ipv4.state` | `string` | 是 | 可选值: "configured", "retained", "unavailable" | - |
| `connection` | `string` | 否 | 示例: "string" | - |

##### `400 Bad Request`

Malformed path, query, header, media type, body, or JSON.

内容类型：`application/json`

返回示例：

```json
{
  "error": {
    "kind": "invalid_json",
    "message": "request body is invalid",
    "request_id": "33ba813e-fc63-4af8-b338-7f7486f82202"
  }
}
```

返回字段：

| 字段 | 类型 | 必填 | 格式 / 示例 / 约束 | 用途说明 |
|---|---|---|---|---|
| `error` | `object` | 是 | - | - |
| `error.kind` | `string` | 是 | 示例: "invalid_json" | - |
| `error.message` | `string` | 是 | 示例: "request body is invalid" | - |
| `error.request_id` | `string` | 是 | 格式: uuid；示例: "33ba813e-fc63-4af8-b338-7f7486f82202" | - |

##### `401 Unauthorized`

Bearer API key is missing, malformed, unknown, or deleted.

内容类型：`application/json`

返回示例：

```json
{
  "error": {
    "kind": "unauthenticated",
    "message": "API key is missing or invalid",
    "request_id": "33ba813e-fc63-4af8-b338-7f7486f82202"
  }
}
```

返回字段：

| 字段 | 类型 | 必填 | 格式 / 示例 / 约束 | 用途说明 |
|---|---|---|---|---|
| `error` | `object` | 是 | - | - |
| `error.kind` | `string` | 是 | 示例: "unauthenticated" | - |
| `error.message` | `string` | 是 | 示例: "API key is missing or invalid" | - |
| `error.request_id` | `string` | 是 | 格式: uuid；示例: "33ba813e-fc63-4af8-b338-7f7486f82202" | - |

##### `404 Not Found`

Resource or client does not exist.

内容类型：`application/json`

返回示例：

```json
{
  "error": {
    "kind": "client_not_found",
    "message": "client was not found",
    "request_id": "33ba813e-fc63-4af8-b338-7f7486f82202"
  }
}
```

返回字段：

| 字段 | 类型 | 必填 | 格式 / 示例 / 约束 | 用途说明 |
|---|---|---|---|---|
| `error` | `object` | 是 | - | - |
| `error.kind` | `string` | 是 | 示例: "client_not_found" | - |
| `error.message` | `string` | 是 | 示例: "client was not found" | - |
| `error.request_id` | `string` | 是 | 格式: uuid；示例: "33ba813e-fc63-4af8-b338-7f7486f82202" | - |

##### `405 Method Not Allowed`

HTTP method is not accepted; inspect the Allow header.

内容类型：`application/json`

返回示例：

```json
{
  "error": {
    "kind": "method_not_allowed",
    "message": "method is not allowed",
    "request_id": "33ba813e-fc63-4af8-b338-7f7486f82202"
  }
}
```

返回字段：

| 字段 | 类型 | 必填 | 格式 / 示例 / 约束 | 用途说明 |
|---|---|---|---|---|
| `error` | `object` | 是 | - | - |
| `error.kind` | `string` | 是 | 示例: "method_not_allowed" | - |
| `error.message` | `string` | 是 | 示例: "method is not allowed" | - |
| `error.request_id` | `string` | 是 | 格式: uuid；示例: "33ba813e-fc63-4af8-b338-7f7486f82202" | - |

##### `500 Internal Server Error`

Unclassified internal failure with no implementation details exposed.

内容类型：`application/json`

返回示例：

```json
{
  "error": {
    "kind": "internal_error",
    "message": "request could not be completed",
    "request_id": "33ba813e-fc63-4af8-b338-7f7486f82202"
  }
}
```

返回字段：

| 字段 | 类型 | 必填 | 格式 / 示例 / 约束 | 用途说明 |
|---|---|---|---|---|
| `error` | `object` | 是 | - | - |
| `error.kind` | `string` | 是 | 示例: "internal_error" | - |
| `error.message` | `string` | 是 | 示例: "request could not be completed" | - |
| `error.request_id` | `string` | 是 | 格式: uuid；示例: "33ba813e-fc63-4af8-b338-7f7486f82202" | - |

##### `503 Service Unavailable`

Authentication storage, PKI, OpenVPN, broker, supervisor, or another required dependency is unavailable.

内容类型：`application/json`

返回示例：

```json
{
  "error": {
    "kind": "runtime_unavailable",
    "message": "OpenVPN runtime is unavailable",
    "request_id": "33ba813e-fc63-4af8-b338-7f7486f82202"
  }
}
```

返回字段：

| 字段 | 类型 | 必填 | 格式 / 示例 / 约束 | 用途说明 |
|---|---|---|---|---|
| `error` | `object` | 是 | - | - |
| `error.kind` | `string` | 是 | 示例: "runtime_unavailable" | - |
| `error.message` | `string` | 是 | 示例: "OpenVPN runtime is unavailable" | - |
| `error.request_id` | `string` | 是 | 格式: uuid；示例: "33ba813e-fc63-4af8-b338-7f7486f82202" | - |

### 8. `PATCH /api/v1/clients/{client_id}`

Rename a client

#### 请求参数

##### Header 参数

| 字段 | 位置 | 类型 | 必填 | 格式 / 示例 / 约束 | 用途说明 |
|---|---|---|---|---|---|
| `Authorization` | `header` | `string` | 是 | Bearer ovpn_v1.<uuid>.<secret> | API 身份认证凭据。Bearer 后填写服务生成的 API Key。 |
| `Content-Type` | `header` | `string` | 是 | application/json | 声明请求体使用 JSON 格式。 |

##### Path 参数

| 字段 | 位置 | 类型 | 必填 | 格式 / 示例 / 约束 | 用途说明 |
|---|---|---|---|---|---|
| `client_id` | `path` | `string` | 是 | 格式: uuid；示例: "c0d4f871-6ea6-42b3-9e7b-f00cc1ec354e" | Complete canonical immutable client UUID; names and UUID prefixes are rejected. |

##### JSON Body 字段

| 字段 | 位置 | 类型 | 必填 | 格式 / 示例 / 约束 | 用途说明 |
|---|---|---|---|---|---|
| `name` | `body` | `string` | 是 | 格式: `^[A-Za-z0-9][A-Za-z0-9_.-]*$`；示例: "alice-notebook" | New stable client name. Uses the same format as client creation. |

#### 请求示例

```http
PATCH /api/v1/clients/c0d4f871-6ea6-42b3-9e7b-f00cc1ec354e HTTP/1.1
Authorization: Bearer ovpn_v1.<uuid>.<secret>
Content-Type: application/json

{
  "name": "alice-notebook"
}
```

#### 返回

##### `200 OK`

Client renamed and profile regenerated.

内容类型：`application/json`

返回示例：

```json
{
  "version": 1,
  "operation_id": "47260b5b-47dd-4f9b-814f-4803668c5934",
  "client": {
    "id": "c0d4f871-6ea6-42b3-9e7b-f00cc1ec354e",
    "name": "alice-notebook",
    "status": "active",
    "ipv4": {
      "mode": "static",
      "address": "10.42.0.30",
      "state": "configured"
    }
  },
  "kick_required": false,
  "profile_redistribution_required": true
}
```

返回字段：

| 字段 | 类型 | 必填 | 格式 / 示例 / 约束 | 用途说明 |
|---|---|---|---|---|
| `version` | `integer` | 是 | 固定值: 1 | - |
| `operation_id` | `string` | 是 | 格式: uuid；示例: "47260b5b-47dd-4f9b-814f-4803668c5934" | - |
| `client` | `object` | 是 | - | - |
| `client.id` | `string` | 是 | 格式: uuid；示例: "c0d4f871-6ea6-42b3-9e7b-f00cc1ec354e" | - |
| `client.name` | `string` | 是 | 格式: `^[A-Za-z0-9][A-Za-z0-9_.-]*$`；示例: "alice-notebook" | - |
| `client.status` | `string` | 是 | 可选值: "active", "revoked", "deleted" | - |
| `client.ipv4` | `object` | 是 | - | - |
| `client.ipv4.mode` | `string` | 是 | 可选值: "none", "static", "dynamic" | - |
| `client.ipv4.address` | `string \| null` | 是 | 格式: ipv4；示例: "10.42.0.30" | - |
| `client.ipv4.state` | `string` | 是 | 可选值: "configured", "retained", "unavailable" | - |
| `client.connection` | `string` | 否 | 示例: "string" | - |
| `kick_required` | `boolean` | 是 | 示例: false | - |
| `profile_redistribution_required` | `boolean` | 是 | 示例: true | - |
| `runtime` | `object` | 否 | - | - |
| `runtime.client_id` | `string` | 否 | 格式: uuid；示例: "c0d4f871-6ea6-42b3-9e7b-f00cc1ec354e" | - |
| `runtime.status` | `string` | 否 | 可选值: "ok", "unavailable" | - |
| `runtime.result` | `object` | 否 | - | - |
| `runtime.result.version` | `integer` | 否 | 固定值: 1 | - |
| `runtime.result.client_id` | `string` | 否 | 格式: uuid；示例: "c0d4f871-6ea6-42b3-9e7b-f00cc1ec354e" | - |
| `runtime.result.client_name` | `string` | 否 | 示例: "string" | - |
| `runtime.result.was_connected` | `boolean` | 否 | 示例: false | - |
| `runtime.result.disconnected` | `boolean` | 否 | 示例: false | - |
| `runtime.result.connections` | `integer` | 否 | 最小值: 0 | - |

##### `400 Bad Request`

Malformed path, query, header, media type, body, or JSON.

内容类型：`application/json`

返回示例：

```json
{
  "error": {
    "kind": "invalid_json",
    "message": "request body is invalid",
    "request_id": "33ba813e-fc63-4af8-b338-7f7486f82202"
  }
}
```

返回字段：

| 字段 | 类型 | 必填 | 格式 / 示例 / 约束 | 用途说明 |
|---|---|---|---|---|
| `error` | `object` | 是 | - | - |
| `error.kind` | `string` | 是 | 示例: "invalid_json" | - |
| `error.message` | `string` | 是 | 示例: "request body is invalid" | - |
| `error.request_id` | `string` | 是 | 格式: uuid；示例: "33ba813e-fc63-4af8-b338-7f7486f82202" | - |

##### `401 Unauthorized`

Bearer API key is missing, malformed, unknown, or deleted.

内容类型：`application/json`

返回示例：

```json
{
  "error": {
    "kind": "unauthenticated",
    "message": "API key is missing or invalid",
    "request_id": "33ba813e-fc63-4af8-b338-7f7486f82202"
  }
}
```

返回字段：

| 字段 | 类型 | 必填 | 格式 / 示例 / 约束 | 用途说明 |
|---|---|---|---|---|
| `error` | `object` | 是 | - | - |
| `error.kind` | `string` | 是 | 示例: "unauthenticated" | - |
| `error.message` | `string` | 是 | 示例: "API key is missing or invalid" | - |
| `error.request_id` | `string` | 是 | 格式: uuid；示例: "33ba813e-fc63-4af8-b338-7f7486f82202" | - |

##### `404 Not Found`

Resource or client does not exist.

内容类型：`application/json`

返回示例：

```json
{
  "error": {
    "kind": "client_not_found",
    "message": "client was not found",
    "request_id": "33ba813e-fc63-4af8-b338-7f7486f82202"
  }
}
```

返回字段：

| 字段 | 类型 | 必填 | 格式 / 示例 / 约束 | 用途说明 |
|---|---|---|---|---|
| `error` | `object` | 是 | - | - |
| `error.kind` | `string` | 是 | 示例: "client_not_found" | - |
| `error.message` | `string` | 是 | 示例: "client was not found" | - |
| `error.request_id` | `string` | 是 | 格式: uuid；示例: "33ba813e-fc63-4af8-b338-7f7486f82202" | - |

##### `405 Method Not Allowed`

HTTP method is not accepted; inspect the Allow header.

内容类型：`application/json`

返回示例：

```json
{
  "error": {
    "kind": "method_not_allowed",
    "message": "method is not allowed",
    "request_id": "33ba813e-fc63-4af8-b338-7f7486f82202"
  }
}
```

返回字段：

| 字段 | 类型 | 必填 | 格式 / 示例 / 约束 | 用途说明 |
|---|---|---|---|---|
| `error` | `object` | 是 | - | - |
| `error.kind` | `string` | 是 | 示例: "method_not_allowed" | - |
| `error.message` | `string` | 是 | 示例: "method is not allowed" | - |
| `error.request_id` | `string` | 是 | 格式: uuid；示例: "33ba813e-fc63-4af8-b338-7f7486f82202" | - |

##### `409 Conflict`

Digest, revision, state, uniqueness, lock, or recovery conflict.

内容类型：`application/json`

返回示例：

```json
{
  "error": {
    "kind": "configuration_conflict",
    "message": "configuration state changed or is busy",
    "request_id": "33ba813e-fc63-4af8-b338-7f7486f82202"
  }
}
```

返回字段：

| 字段 | 类型 | 必填 | 格式 / 示例 / 约束 | 用途说明 |
|---|---|---|---|---|
| `error` | `object` | 是 | - | - |
| `error.kind` | `string` | 是 | 示例: "configuration_conflict" | - |
| `error.message` | `string` | 是 | 示例: "configuration state changed or is busy" | - |
| `error.request_id` | `string` | 是 | 格式: uuid；示例: "33ba813e-fc63-4af8-b338-7f7486f82202" | - |

##### `422 Unprocessable Entity`

JSON is valid but violates client, IPv4, or configuration semantics.

内容类型：`application/json`

返回示例：

```json
{
  "error": {
    "kind": "invalid_client",
    "message": "client request is not valid for the current state",
    "request_id": "33ba813e-fc63-4af8-b338-7f7486f82202"
  }
}
```

返回字段：

| 字段 | 类型 | 必填 | 格式 / 示例 / 约束 | 用途说明 |
|---|---|---|---|---|
| `error` | `object` | 是 | - | - |
| `error.kind` | `string` | 是 | 示例: "invalid_client" | - |
| `error.message` | `string` | 是 | 示例: "client request is not valid for the current state" | - |
| `error.request_id` | `string` | 是 | 格式: uuid；示例: "33ba813e-fc63-4af8-b338-7f7486f82202" | - |

##### `500 Internal Server Error`

Unclassified internal failure with no implementation details exposed.

内容类型：`application/json`

返回示例：

```json
{
  "error": {
    "kind": "internal_error",
    "message": "request could not be completed",
    "request_id": "33ba813e-fc63-4af8-b338-7f7486f82202"
  }
}
```

返回字段：

| 字段 | 类型 | 必填 | 格式 / 示例 / 约束 | 用途说明 |
|---|---|---|---|---|
| `error` | `object` | 是 | - | - |
| `error.kind` | `string` | 是 | 示例: "internal_error" | - |
| `error.message` | `string` | 是 | 示例: "request could not be completed" | - |
| `error.request_id` | `string` | 是 | 格式: uuid；示例: "33ba813e-fc63-4af8-b338-7f7486f82202" | - |

##### `503 Service Unavailable`

Authentication storage, PKI, OpenVPN, broker, supervisor, or another required dependency is unavailable.

内容类型：`application/json`

返回示例：

```json
{
  "error": {
    "kind": "runtime_unavailable",
    "message": "OpenVPN runtime is unavailable",
    "request_id": "33ba813e-fc63-4af8-b338-7f7486f82202"
  }
}
```

返回字段：

| 字段 | 类型 | 必填 | 格式 / 示例 / 约束 | 用途说明 |
|---|---|---|---|---|
| `error` | `object` | 是 | - | - |
| `error.kind` | `string` | 是 | 示例: "runtime_unavailable" | - |
| `error.message` | `string` | 是 | 示例: "OpenVPN runtime is unavailable" | - |
| `error.request_id` | `string` | 是 | 格式: uuid；示例: "33ba813e-fc63-4af8-b338-7f7486f82202" | - |

### 9. `DELETE /api/v1/clients/{client_id}`

Delete local client credentials. Requires an empty body. The immutable client UUID tombstone remains in authoritative state.

#### 请求参数

##### Header 参数

| 字段 | 位置 | 类型 | 必填 | 格式 / 示例 / 约束 | 用途说明 |
|---|---|---|---|---|---|
| `Authorization` | `header` | `string` | 是 | Bearer ovpn_v1.<uuid>.<secret> | API 身份认证凭据。Bearer 后填写服务生成的 API Key。 |

##### Path 参数

| 字段 | 位置 | 类型 | 必填 | 格式 / 示例 / 约束 | 用途说明 |
|---|---|---|---|---|---|
| `client_id` | `path` | `string` | 是 | 格式: uuid；示例: "c0d4f871-6ea6-42b3-9e7b-f00cc1ec354e" | Complete canonical immutable client UUID; names and UUID prefixes are rejected. |

#### 请求示例

```http
DELETE /api/v1/clients/c0d4f871-6ea6-42b3-9e7b-f00cc1ec354e HTTP/1.1
Authorization: Bearer ovpn_v1.<uuid>.<secret>
```

#### 返回

##### `200 OK`

Client credentials deleted; the UUID tombstone remains.

内容类型：`application/json`

返回示例：

```json
{
  "version": 1,
  "operation_id": "47260b5b-47dd-4f9b-814f-4803668c5934",
  "client": {
    "id": "c0d4f871-6ea6-42b3-9e7b-f00cc1ec354e",
    "name": "alice-notebook",
    "status": "deleted",
    "ipv4": {
      "mode": "none",
      "address": null,
      "state": "unavailable"
    }
  },
  "kick_required": false,
  "profile_redistribution_required": false
}
```

返回字段：

| 字段 | 类型 | 必填 | 格式 / 示例 / 约束 | 用途说明 |
|---|---|---|---|---|
| `version` | `integer` | 是 | 固定值: 1 | - |
| `operation_id` | `string` | 是 | 格式: uuid；示例: "47260b5b-47dd-4f9b-814f-4803668c5934" | - |
| `client` | `object` | 是 | - | - |
| `client.id` | `string` | 是 | 格式: uuid；示例: "c0d4f871-6ea6-42b3-9e7b-f00cc1ec354e" | - |
| `client.name` | `string` | 是 | 格式: `^[A-Za-z0-9][A-Za-z0-9_.-]*$`；示例: "alice-notebook" | - |
| `client.status` | `string` | 是 | 可选值: "active", "revoked", "deleted" | - |
| `client.ipv4` | `object` | 是 | - | - |
| `client.ipv4.mode` | `string` | 是 | 可选值: "none", "static", "dynamic" | - |
| `client.ipv4.address` | `string \| null` | 是 | 格式: ipv4；示例: "10.42.0.30" | - |
| `client.ipv4.state` | `string` | 是 | 可选值: "configured", "retained", "unavailable" | - |
| `client.connection` | `string` | 否 | 示例: "string" | - |
| `kick_required` | `boolean` | 是 | 示例: false | - |
| `profile_redistribution_required` | `boolean` | 是 | 示例: false | - |
| `runtime` | `object` | 否 | - | - |
| `runtime.client_id` | `string` | 否 | 格式: uuid；示例: "c0d4f871-6ea6-42b3-9e7b-f00cc1ec354e" | - |
| `runtime.status` | `string` | 否 | 可选值: "ok", "unavailable" | - |
| `runtime.result` | `object` | 否 | - | - |
| `runtime.result.version` | `integer` | 否 | 固定值: 1 | - |
| `runtime.result.client_id` | `string` | 否 | 格式: uuid；示例: "c0d4f871-6ea6-42b3-9e7b-f00cc1ec354e" | - |
| `runtime.result.client_name` | `string` | 否 | 示例: "string" | - |
| `runtime.result.was_connected` | `boolean` | 否 | 示例: false | - |
| `runtime.result.disconnected` | `boolean` | 否 | 示例: false | - |
| `runtime.result.connections` | `integer` | 否 | 最小值: 0 | - |

##### `400 Bad Request`

Malformed path, query, header, media type, body, or JSON.

内容类型：`application/json`

返回示例：

```json
{
  "error": {
    "kind": "invalid_json",
    "message": "request body is invalid",
    "request_id": "33ba813e-fc63-4af8-b338-7f7486f82202"
  }
}
```

返回字段：

| 字段 | 类型 | 必填 | 格式 / 示例 / 约束 | 用途说明 |
|---|---|---|---|---|
| `error` | `object` | 是 | - | - |
| `error.kind` | `string` | 是 | 示例: "invalid_json" | - |
| `error.message` | `string` | 是 | 示例: "request body is invalid" | - |
| `error.request_id` | `string` | 是 | 格式: uuid；示例: "33ba813e-fc63-4af8-b338-7f7486f82202" | - |

##### `401 Unauthorized`

Bearer API key is missing, malformed, unknown, or deleted.

内容类型：`application/json`

返回示例：

```json
{
  "error": {
    "kind": "unauthenticated",
    "message": "API key is missing or invalid",
    "request_id": "33ba813e-fc63-4af8-b338-7f7486f82202"
  }
}
```

返回字段：

| 字段 | 类型 | 必填 | 格式 / 示例 / 约束 | 用途说明 |
|---|---|---|---|---|
| `error` | `object` | 是 | - | - |
| `error.kind` | `string` | 是 | 示例: "unauthenticated" | - |
| `error.message` | `string` | 是 | 示例: "API key is missing or invalid" | - |
| `error.request_id` | `string` | 是 | 格式: uuid；示例: "33ba813e-fc63-4af8-b338-7f7486f82202" | - |

##### `404 Not Found`

Resource or client does not exist.

内容类型：`application/json`

返回示例：

```json
{
  "error": {
    "kind": "client_not_found",
    "message": "client was not found",
    "request_id": "33ba813e-fc63-4af8-b338-7f7486f82202"
  }
}
```

返回字段：

| 字段 | 类型 | 必填 | 格式 / 示例 / 约束 | 用途说明 |
|---|---|---|---|---|
| `error` | `object` | 是 | - | - |
| `error.kind` | `string` | 是 | 示例: "client_not_found" | - |
| `error.message` | `string` | 是 | 示例: "client was not found" | - |
| `error.request_id` | `string` | 是 | 格式: uuid；示例: "33ba813e-fc63-4af8-b338-7f7486f82202" | - |

##### `405 Method Not Allowed`

HTTP method is not accepted; inspect the Allow header.

内容类型：`application/json`

返回示例：

```json
{
  "error": {
    "kind": "method_not_allowed",
    "message": "method is not allowed",
    "request_id": "33ba813e-fc63-4af8-b338-7f7486f82202"
  }
}
```

返回字段：

| 字段 | 类型 | 必填 | 格式 / 示例 / 约束 | 用途说明 |
|---|---|---|---|---|
| `error` | `object` | 是 | - | - |
| `error.kind` | `string` | 是 | 示例: "method_not_allowed" | - |
| `error.message` | `string` | 是 | 示例: "method is not allowed" | - |
| `error.request_id` | `string` | 是 | 格式: uuid；示例: "33ba813e-fc63-4af8-b338-7f7486f82202" | - |

##### `409 Conflict`

Digest, revision, state, uniqueness, lock, or recovery conflict.

内容类型：`application/json`

返回示例：

```json
{
  "error": {
    "kind": "configuration_conflict",
    "message": "configuration state changed or is busy",
    "request_id": "33ba813e-fc63-4af8-b338-7f7486f82202"
  }
}
```

返回字段：

| 字段 | 类型 | 必填 | 格式 / 示例 / 约束 | 用途说明 |
|---|---|---|---|---|
| `error` | `object` | 是 | - | - |
| `error.kind` | `string` | 是 | 示例: "configuration_conflict" | - |
| `error.message` | `string` | 是 | 示例: "configuration state changed or is busy" | - |
| `error.request_id` | `string` | 是 | 格式: uuid；示例: "33ba813e-fc63-4af8-b338-7f7486f82202" | - |

##### `422 Unprocessable Entity`

JSON is valid but violates client, IPv4, or configuration semantics.

内容类型：`application/json`

返回示例：

```json
{
  "error": {
    "kind": "invalid_client",
    "message": "client request is not valid for the current state",
    "request_id": "33ba813e-fc63-4af8-b338-7f7486f82202"
  }
}
```

返回字段：

| 字段 | 类型 | 必填 | 格式 / 示例 / 约束 | 用途说明 |
|---|---|---|---|---|
| `error` | `object` | 是 | - | - |
| `error.kind` | `string` | 是 | 示例: "invalid_client" | - |
| `error.message` | `string` | 是 | 示例: "client request is not valid for the current state" | - |
| `error.request_id` | `string` | 是 | 格式: uuid；示例: "33ba813e-fc63-4af8-b338-7f7486f82202" | - |

##### `500 Internal Server Error`

Unclassified internal failure with no implementation details exposed.

内容类型：`application/json`

返回示例：

```json
{
  "error": {
    "kind": "internal_error",
    "message": "request could not be completed",
    "request_id": "33ba813e-fc63-4af8-b338-7f7486f82202"
  }
}
```

返回字段：

| 字段 | 类型 | 必填 | 格式 / 示例 / 约束 | 用途说明 |
|---|---|---|---|---|
| `error` | `object` | 是 | - | - |
| `error.kind` | `string` | 是 | 示例: "internal_error" | - |
| `error.message` | `string` | 是 | 示例: "request could not be completed" | - |
| `error.request_id` | `string` | 是 | 格式: uuid；示例: "33ba813e-fc63-4af8-b338-7f7486f82202" | - |

##### `503 Service Unavailable`

Authentication storage, PKI, OpenVPN, broker, supervisor, or another required dependency is unavailable.

内容类型：`application/json`

返回示例：

```json
{
  "error": {
    "kind": "runtime_unavailable",
    "message": "OpenVPN runtime is unavailable",
    "request_id": "33ba813e-fc63-4af8-b338-7f7486f82202"
  }
}
```

返回字段：

| 字段 | 类型 | 必填 | 格式 / 示例 / 约束 | 用途说明 |
|---|---|---|---|---|
| `error` | `object` | 是 | - | - |
| `error.kind` | `string` | 是 | 示例: "runtime_unavailable" | - |
| `error.message` | `string` | 是 | 示例: "OpenVPN runtime is unavailable" | - |
| `error.request_id` | `string` | 是 | 格式: uuid；示例: "33ba813e-fc63-4af8-b338-7f7486f82202" | - |

### 10. `GET /api/v1/clients/{client_id}/profile`

Download an OpenVPN client profile. The response contains a private key and must be handled as a credential.

#### 请求参数

##### Header 参数

| 字段 | 位置 | 类型 | 必填 | 格式 / 示例 / 约束 | 用途说明 |
|---|---|---|---|---|---|
| `Authorization` | `header` | `string` | 是 | Bearer ovpn_v1.<uuid>.<secret> | API 身份认证凭据。Bearer 后填写服务生成的 API Key。 |

##### Path 参数

| 字段 | 位置 | 类型 | 必填 | 格式 / 示例 / 约束 | 用途说明 |
|---|---|---|---|---|---|
| `client_id` | `path` | `string` | 是 | 格式: uuid；示例: "c0d4f871-6ea6-42b3-9e7b-f00cc1ec354e" | Complete canonical immutable client UUID; names and UUID prefixes are rejected. |

#### 请求示例

```http
GET /api/v1/clients/c0d4f871-6ea6-42b3-9e7b-f00cc1ec354e/profile HTTP/1.1
Authorization: Bearer ovpn_v1.<uuid>.<secret>
```

#### 返回

##### `200 OK`

OpenVPN profile containing private credentials.

内容类型：`application/x-openvpn-profile`

返回示例：

```text
string
```

返回字段：

| 字段 | 类型 | 必填 | 格式 / 示例 / 约束 | 用途说明 |
|---|---|---|---|---|
| `$body` | `string` | 是 | 格式: binary；示例: "string" | - |

##### `400 Bad Request`

Malformed path, query, header, media type, body, or JSON.

内容类型：`application/json`

返回示例：

```json
{
  "error": {
    "kind": "invalid_json",
    "message": "request body is invalid",
    "request_id": "33ba813e-fc63-4af8-b338-7f7486f82202"
  }
}
```

返回字段：

| 字段 | 类型 | 必填 | 格式 / 示例 / 约束 | 用途说明 |
|---|---|---|---|---|
| `error` | `object` | 是 | - | - |
| `error.kind` | `string` | 是 | 示例: "invalid_json" | - |
| `error.message` | `string` | 是 | 示例: "request body is invalid" | - |
| `error.request_id` | `string` | 是 | 格式: uuid；示例: "33ba813e-fc63-4af8-b338-7f7486f82202" | - |

##### `401 Unauthorized`

Bearer API key is missing, malformed, unknown, or deleted.

内容类型：`application/json`

返回示例：

```json
{
  "error": {
    "kind": "unauthenticated",
    "message": "API key is missing or invalid",
    "request_id": "33ba813e-fc63-4af8-b338-7f7486f82202"
  }
}
```

返回字段：

| 字段 | 类型 | 必填 | 格式 / 示例 / 约束 | 用途说明 |
|---|---|---|---|---|
| `error` | `object` | 是 | - | - |
| `error.kind` | `string` | 是 | 示例: "unauthenticated" | - |
| `error.message` | `string` | 是 | 示例: "API key is missing or invalid" | - |
| `error.request_id` | `string` | 是 | 格式: uuid；示例: "33ba813e-fc63-4af8-b338-7f7486f82202" | - |

##### `404 Not Found`

Resource or client does not exist.

内容类型：`application/json`

返回示例：

```json
{
  "error": {
    "kind": "client_not_found",
    "message": "client was not found",
    "request_id": "33ba813e-fc63-4af8-b338-7f7486f82202"
  }
}
```

返回字段：

| 字段 | 类型 | 必填 | 格式 / 示例 / 约束 | 用途说明 |
|---|---|---|---|---|
| `error` | `object` | 是 | - | - |
| `error.kind` | `string` | 是 | 示例: "client_not_found" | - |
| `error.message` | `string` | 是 | 示例: "client was not found" | - |
| `error.request_id` | `string` | 是 | 格式: uuid；示例: "33ba813e-fc63-4af8-b338-7f7486f82202" | - |

##### `405 Method Not Allowed`

HTTP method is not accepted; inspect the Allow header.

内容类型：`application/json`

返回示例：

```json
{
  "error": {
    "kind": "method_not_allowed",
    "message": "method is not allowed",
    "request_id": "33ba813e-fc63-4af8-b338-7f7486f82202"
  }
}
```

返回字段：

| 字段 | 类型 | 必填 | 格式 / 示例 / 约束 | 用途说明 |
|---|---|---|---|---|
| `error` | `object` | 是 | - | - |
| `error.kind` | `string` | 是 | 示例: "method_not_allowed" | - |
| `error.message` | `string` | 是 | 示例: "method is not allowed" | - |
| `error.request_id` | `string` | 是 | 格式: uuid；示例: "33ba813e-fc63-4af8-b338-7f7486f82202" | - |

##### `409 Conflict`

Digest, revision, state, uniqueness, lock, or recovery conflict.

内容类型：`application/json`

返回示例：

```json
{
  "error": {
    "kind": "configuration_conflict",
    "message": "configuration state changed or is busy",
    "request_id": "33ba813e-fc63-4af8-b338-7f7486f82202"
  }
}
```

返回字段：

| 字段 | 类型 | 必填 | 格式 / 示例 / 约束 | 用途说明 |
|---|---|---|---|---|
| `error` | `object` | 是 | - | - |
| `error.kind` | `string` | 是 | 示例: "configuration_conflict" | - |
| `error.message` | `string` | 是 | 示例: "configuration state changed or is busy" | - |
| `error.request_id` | `string` | 是 | 格式: uuid；示例: "33ba813e-fc63-4af8-b338-7f7486f82202" | - |

##### `422 Unprocessable Entity`

JSON is valid but violates client, IPv4, or configuration semantics.

内容类型：`application/json`

返回示例：

```json
{
  "error": {
    "kind": "invalid_client",
    "message": "client request is not valid for the current state",
    "request_id": "33ba813e-fc63-4af8-b338-7f7486f82202"
  }
}
```

返回字段：

| 字段 | 类型 | 必填 | 格式 / 示例 / 约束 | 用途说明 |
|---|---|---|---|---|
| `error` | `object` | 是 | - | - |
| `error.kind` | `string` | 是 | 示例: "invalid_client" | - |
| `error.message` | `string` | 是 | 示例: "client request is not valid for the current state" | - |
| `error.request_id` | `string` | 是 | 格式: uuid；示例: "33ba813e-fc63-4af8-b338-7f7486f82202" | - |

##### `500 Internal Server Error`

Unclassified internal failure with no implementation details exposed.

内容类型：`application/json`

返回示例：

```json
{
  "error": {
    "kind": "internal_error",
    "message": "request could not be completed",
    "request_id": "33ba813e-fc63-4af8-b338-7f7486f82202"
  }
}
```

返回字段：

| 字段 | 类型 | 必填 | 格式 / 示例 / 约束 | 用途说明 |
|---|---|---|---|---|
| `error` | `object` | 是 | - | - |
| `error.kind` | `string` | 是 | 示例: "internal_error" | - |
| `error.message` | `string` | 是 | 示例: "request could not be completed" | - |
| `error.request_id` | `string` | 是 | 格式: uuid；示例: "33ba813e-fc63-4af8-b338-7f7486f82202" | - |

##### `503 Service Unavailable`

Authentication storage, PKI, OpenVPN, broker, supervisor, or another required dependency is unavailable.

内容类型：`application/json`

返回示例：

```json
{
  "error": {
    "kind": "runtime_unavailable",
    "message": "OpenVPN runtime is unavailable",
    "request_id": "33ba813e-fc63-4af8-b338-7f7486f82202"
  }
}
```

返回字段：

| 字段 | 类型 | 必填 | 格式 / 示例 / 约束 | 用途说明 |
|---|---|---|---|---|
| `error` | `object` | 是 | - | - |
| `error.kind` | `string` | 是 | 示例: "runtime_unavailable" | - |
| `error.message` | `string` | 是 | 示例: "OpenVPN runtime is unavailable" | - |
| `error.request_id` | `string` | 是 | 格式: uuid；示例: "33ba813e-fc63-4af8-b338-7f7486f82202" | - |

### 11. `POST /api/v1/clients/{client_id}/revoke`

Revoke client credentials

#### 请求参数

##### Header 参数

| 字段 | 位置 | 类型 | 必填 | 格式 / 示例 / 约束 | 用途说明 |
|---|---|---|---|---|---|
| `Authorization` | `header` | `string` | 是 | Bearer ovpn_v1.<uuid>.<secret> | API 身份认证凭据。Bearer 后填写服务生成的 API Key。 |
| `Content-Type` | `header` | `string` | 是 | application/json | 声明请求体使用 JSON 格式。 |

##### Path 参数

| 字段 | 位置 | 类型 | 必填 | 格式 / 示例 / 约束 | 用途说明 |
|---|---|---|---|---|---|
| `client_id` | `path` | `string` | 是 | 格式: uuid；示例: "c0d4f871-6ea6-42b3-9e7b-f00cc1ec354e" | Complete canonical immutable client UUID; names and UUID prefixes are rejected. |

##### JSON Body 字段

| 字段 | 位置 | 类型 | 必填 | 格式 / 示例 / 约束 | 用途说明 |
|---|---|---|---|---|---|
| `release_ipv4` | `body` | `boolean` | 是 | 示例: false | true releases the assignment; false retains it for later release or reissue. |

#### 请求示例

```http
POST /api/v1/clients/c0d4f871-6ea6-42b3-9e7b-f00cc1ec354e/revoke HTTP/1.1
Authorization: Bearer ovpn_v1.<uuid>.<secret>
Content-Type: application/json

{
  "release_ipv4": false
}
```

#### 返回

##### `200 OK`

Committed client mutation. Runtime convergence is reported separately when required.

内容类型：`application/json`

返回示例：

```json
{
  "version": 1,
  "operation_id": "47260b5b-47dd-4f9b-814f-4803668c5934",
  "client": {
    "id": "c0d4f871-6ea6-42b3-9e7b-f00cc1ec354e",
    "name": "alice-laptop",
    "status": "revoked",
    "ipv4": {
      "mode": "static",
      "address": "10.42.0.30",
      "state": "retained"
    }
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

返回字段：

| 字段 | 类型 | 必填 | 格式 / 示例 / 约束 | 用途说明 |
|---|---|---|---|---|
| `version` | `integer` | 是 | 固定值: 1 | - |
| `operation_id` | `string` | 是 | 格式: uuid；示例: "47260b5b-47dd-4f9b-814f-4803668c5934" | - |
| `client` | `object` | 是 | - | - |
| `client.id` | `string` | 是 | 格式: uuid；示例: "c0d4f871-6ea6-42b3-9e7b-f00cc1ec354e" | - |
| `client.name` | `string` | 是 | 格式: `^[A-Za-z0-9][A-Za-z0-9_.-]*$`；示例: "alice-laptop" | - |
| `client.status` | `string` | 是 | 可选值: "active", "revoked", "deleted" | - |
| `client.ipv4` | `object` | 是 | - | - |
| `client.ipv4.mode` | `string` | 是 | 可选值: "none", "static", "dynamic" | - |
| `client.ipv4.address` | `string \| null` | 是 | 格式: ipv4；示例: "10.42.0.30" | - |
| `client.ipv4.state` | `string` | 是 | 可选值: "configured", "retained", "unavailable" | - |
| `client.connection` | `string` | 否 | 示例: "string" | - |
| `kick_required` | `boolean` | 是 | 示例: true | - |
| `profile_redistribution_required` | `boolean` | 是 | 示例: false | - |
| `runtime` | `object` | 否 | - | - |
| `runtime.client_id` | `string` | 否 | 格式: uuid；示例: "c0d4f871-6ea6-42b3-9e7b-f00cc1ec354e" | - |
| `runtime.status` | `string` | 否 | 可选值: "ok", "unavailable" | - |
| `runtime.result` | `object` | 否 | - | - |
| `runtime.result.version` | `integer` | 否 | 固定值: 1 | - |
| `runtime.result.client_id` | `string` | 否 | 格式: uuid；示例: "c0d4f871-6ea6-42b3-9e7b-f00cc1ec354e" | - |
| `runtime.result.client_name` | `string` | 否 | 示例: "alice-laptop" | - |
| `runtime.result.was_connected` | `boolean` | 否 | 示例: true | - |
| `runtime.result.disconnected` | `boolean` | 否 | 示例: true | - |
| `runtime.result.connections` | `integer` | 否 | 最小值: 0；示例: 1 | - |

##### `400 Bad Request`

Malformed path, query, header, media type, body, or JSON.

内容类型：`application/json`

返回示例：

```json
{
  "error": {
    "kind": "invalid_json",
    "message": "request body is invalid",
    "request_id": "33ba813e-fc63-4af8-b338-7f7486f82202"
  }
}
```

返回字段：

| 字段 | 类型 | 必填 | 格式 / 示例 / 约束 | 用途说明 |
|---|---|---|---|---|
| `error` | `object` | 是 | - | - |
| `error.kind` | `string` | 是 | 示例: "invalid_json" | - |
| `error.message` | `string` | 是 | 示例: "request body is invalid" | - |
| `error.request_id` | `string` | 是 | 格式: uuid；示例: "33ba813e-fc63-4af8-b338-7f7486f82202" | - |

##### `401 Unauthorized`

Bearer API key is missing, malformed, unknown, or deleted.

内容类型：`application/json`

返回示例：

```json
{
  "error": {
    "kind": "unauthenticated",
    "message": "API key is missing or invalid",
    "request_id": "33ba813e-fc63-4af8-b338-7f7486f82202"
  }
}
```

返回字段：

| 字段 | 类型 | 必填 | 格式 / 示例 / 约束 | 用途说明 |
|---|---|---|---|---|
| `error` | `object` | 是 | - | - |
| `error.kind` | `string` | 是 | 示例: "unauthenticated" | - |
| `error.message` | `string` | 是 | 示例: "API key is missing or invalid" | - |
| `error.request_id` | `string` | 是 | 格式: uuid；示例: "33ba813e-fc63-4af8-b338-7f7486f82202" | - |

##### `404 Not Found`

Resource or client does not exist.

内容类型：`application/json`

返回示例：

```json
{
  "error": {
    "kind": "client_not_found",
    "message": "client was not found",
    "request_id": "33ba813e-fc63-4af8-b338-7f7486f82202"
  }
}
```

返回字段：

| 字段 | 类型 | 必填 | 格式 / 示例 / 约束 | 用途说明 |
|---|---|---|---|---|
| `error` | `object` | 是 | - | - |
| `error.kind` | `string` | 是 | 示例: "client_not_found" | - |
| `error.message` | `string` | 是 | 示例: "client was not found" | - |
| `error.request_id` | `string` | 是 | 格式: uuid；示例: "33ba813e-fc63-4af8-b338-7f7486f82202" | - |

##### `405 Method Not Allowed`

HTTP method is not accepted; inspect the Allow header.

内容类型：`application/json`

返回示例：

```json
{
  "error": {
    "kind": "method_not_allowed",
    "message": "method is not allowed",
    "request_id": "33ba813e-fc63-4af8-b338-7f7486f82202"
  }
}
```

返回字段：

| 字段 | 类型 | 必填 | 格式 / 示例 / 约束 | 用途说明 |
|---|---|---|---|---|
| `error` | `object` | 是 | - | - |
| `error.kind` | `string` | 是 | 示例: "method_not_allowed" | - |
| `error.message` | `string` | 是 | 示例: "method is not allowed" | - |
| `error.request_id` | `string` | 是 | 格式: uuid；示例: "33ba813e-fc63-4af8-b338-7f7486f82202" | - |

##### `409 Conflict`

Digest, revision, state, uniqueness, lock, or recovery conflict.

内容类型：`application/json`

返回示例：

```json
{
  "error": {
    "kind": "configuration_conflict",
    "message": "configuration state changed or is busy",
    "request_id": "33ba813e-fc63-4af8-b338-7f7486f82202"
  }
}
```

返回字段：

| 字段 | 类型 | 必填 | 格式 / 示例 / 约束 | 用途说明 |
|---|---|---|---|---|
| `error` | `object` | 是 | - | - |
| `error.kind` | `string` | 是 | 示例: "configuration_conflict" | - |
| `error.message` | `string` | 是 | 示例: "configuration state changed or is busy" | - |
| `error.request_id` | `string` | 是 | 格式: uuid；示例: "33ba813e-fc63-4af8-b338-7f7486f82202" | - |

##### `422 Unprocessable Entity`

JSON is valid but violates client, IPv4, or configuration semantics.

内容类型：`application/json`

返回示例：

```json
{
  "error": {
    "kind": "invalid_client",
    "message": "client request is not valid for the current state",
    "request_id": "33ba813e-fc63-4af8-b338-7f7486f82202"
  }
}
```

返回字段：

| 字段 | 类型 | 必填 | 格式 / 示例 / 约束 | 用途说明 |
|---|---|---|---|---|
| `error` | `object` | 是 | - | - |
| `error.kind` | `string` | 是 | 示例: "invalid_client" | - |
| `error.message` | `string` | 是 | 示例: "client request is not valid for the current state" | - |
| `error.request_id` | `string` | 是 | 格式: uuid；示例: "33ba813e-fc63-4af8-b338-7f7486f82202" | - |

##### `500 Internal Server Error`

Unclassified internal failure with no implementation details exposed.

内容类型：`application/json`

返回示例：

```json
{
  "error": {
    "kind": "internal_error",
    "message": "request could not be completed",
    "request_id": "33ba813e-fc63-4af8-b338-7f7486f82202"
  }
}
```

返回字段：

| 字段 | 类型 | 必填 | 格式 / 示例 / 约束 | 用途说明 |
|---|---|---|---|---|
| `error` | `object` | 是 | - | - |
| `error.kind` | `string` | 是 | 示例: "internal_error" | - |
| `error.message` | `string` | 是 | 示例: "request could not be completed" | - |
| `error.request_id` | `string` | 是 | 格式: uuid；示例: "33ba813e-fc63-4af8-b338-7f7486f82202" | - |

##### `503 Service Unavailable`

Authentication storage, PKI, OpenVPN, broker, supervisor, or another required dependency is unavailable.

内容类型：`application/json`

返回示例：

```json
{
  "error": {
    "kind": "runtime_unavailable",
    "message": "OpenVPN runtime is unavailable",
    "request_id": "33ba813e-fc63-4af8-b338-7f7486f82202"
  }
}
```

返回字段：

| 字段 | 类型 | 必填 | 格式 / 示例 / 约束 | 用途说明 |
|---|---|---|---|---|
| `error` | `object` | 是 | - | - |
| `error.kind` | `string` | 是 | 示例: "runtime_unavailable" | - |
| `error.message` | `string` | 是 | 示例: "OpenVPN runtime is unavailable" | - |
| `error.request_id` | `string` | 是 | 格式: uuid；示例: "33ba813e-fc63-4af8-b338-7f7486f82202" | - |

### 12. `POST /api/v1/clients/{client_id}/reissue`

Reissue client credentials and profile

#### 请求参数

##### Header 参数

| 字段 | 位置 | 类型 | 必填 | 格式 / 示例 / 约束 | 用途说明 |
|---|---|---|---|---|---|
| `Authorization` | `header` | `string` | 是 | Bearer ovpn_v1.<uuid>.<secret> | API 身份认证凭据。Bearer 后填写服务生成的 API Key。 |
| `Content-Type` | `header` | `string` | 是 | application/json | 声明请求体使用 JSON 格式。 |

##### Path 参数

| 字段 | 位置 | 类型 | 必填 | 格式 / 示例 / 约束 | 用途说明 |
|---|---|---|---|---|---|
| `client_id` | `path` | `string` | 是 | 格式: uuid；示例: "c0d4f871-6ea6-42b3-9e7b-f00cc1ec354e" | Complete canonical immutable client UUID; names and UUID prefixes are rejected. |

##### JSON Body 字段

| 字段 | 位置 | 类型 | 必填 | 格式 / 示例 / 约束 | 用途说明 |
|---|---|---|---|---|---|
| `ipv4` | `body` | `string` | 是 | 示例: "dynamic" | auto, dynamic, or a valid static IPv4 address. |

#### 请求示例

```http
POST /api/v1/clients/c0d4f871-6ea6-42b3-9e7b-f00cc1ec354e/reissue HTTP/1.1
Authorization: Bearer ovpn_v1.<uuid>.<secret>
Content-Type: application/json

{
  "ipv4": "dynamic"
}
```

#### 返回

##### `200 OK`

Client credentials and profile reissued.

内容类型：`application/json`

返回示例：

```json
{
  "version": 1,
  "operation_id": "47260b5b-47dd-4f9b-814f-4803668c5934",
  "client": {
    "id": "c0d4f871-6ea6-42b3-9e7b-f00cc1ec354e",
    "name": "alice-notebook",
    "status": "active",
    "ipv4": {
      "mode": "dynamic",
      "address": null,
      "state": "configured"
    }
  },
  "kick_required": true,
  "profile_redistribution_required": true,
  "runtime": {
    "status": "unavailable"
  }
}
```

返回字段：

| 字段 | 类型 | 必填 | 格式 / 示例 / 约束 | 用途说明 |
|---|---|---|---|---|
| `version` | `integer` | 是 | 固定值: 1 | - |
| `operation_id` | `string` | 是 | 格式: uuid；示例: "47260b5b-47dd-4f9b-814f-4803668c5934" | - |
| `client` | `object` | 是 | - | - |
| `client.id` | `string` | 是 | 格式: uuid；示例: "c0d4f871-6ea6-42b3-9e7b-f00cc1ec354e" | - |
| `client.name` | `string` | 是 | 格式: `^[A-Za-z0-9][A-Za-z0-9_.-]*$`；示例: "alice-notebook" | - |
| `client.status` | `string` | 是 | 可选值: "active", "revoked", "deleted" | - |
| `client.ipv4` | `object` | 是 | - | - |
| `client.ipv4.mode` | `string` | 是 | 可选值: "none", "static", "dynamic" | - |
| `client.ipv4.address` | `string \| null` | 是 | 格式: ipv4；示例: "10.42.0.30" | - |
| `client.ipv4.state` | `string` | 是 | 可选值: "configured", "retained", "unavailable" | - |
| `client.connection` | `string` | 否 | 示例: "string" | - |
| `kick_required` | `boolean` | 是 | 示例: true | - |
| `profile_redistribution_required` | `boolean` | 是 | 示例: true | - |
| `runtime` | `object` | 否 | - | - |
| `runtime.client_id` | `string` | 否 | 格式: uuid；示例: "c0d4f871-6ea6-42b3-9e7b-f00cc1ec354e" | - |
| `runtime.status` | `string` | 否 | 可选值: "ok", "unavailable" | - |
| `runtime.result` | `object` | 否 | - | - |
| `runtime.result.version` | `integer` | 否 | 固定值: 1 | - |
| `runtime.result.client_id` | `string` | 否 | 格式: uuid；示例: "c0d4f871-6ea6-42b3-9e7b-f00cc1ec354e" | - |
| `runtime.result.client_name` | `string` | 否 | 示例: "string" | - |
| `runtime.result.was_connected` | `boolean` | 否 | 示例: false | - |
| `runtime.result.disconnected` | `boolean` | 否 | 示例: false | - |
| `runtime.result.connections` | `integer` | 否 | 最小值: 0 | - |

##### `400 Bad Request`

Malformed path, query, header, media type, body, or JSON.

内容类型：`application/json`

返回示例：

```json
{
  "error": {
    "kind": "invalid_json",
    "message": "request body is invalid",
    "request_id": "33ba813e-fc63-4af8-b338-7f7486f82202"
  }
}
```

返回字段：

| 字段 | 类型 | 必填 | 格式 / 示例 / 约束 | 用途说明 |
|---|---|---|---|---|
| `error` | `object` | 是 | - | - |
| `error.kind` | `string` | 是 | 示例: "invalid_json" | - |
| `error.message` | `string` | 是 | 示例: "request body is invalid" | - |
| `error.request_id` | `string` | 是 | 格式: uuid；示例: "33ba813e-fc63-4af8-b338-7f7486f82202" | - |

##### `401 Unauthorized`

Bearer API key is missing, malformed, unknown, or deleted.

内容类型：`application/json`

返回示例：

```json
{
  "error": {
    "kind": "unauthenticated",
    "message": "API key is missing or invalid",
    "request_id": "33ba813e-fc63-4af8-b338-7f7486f82202"
  }
}
```

返回字段：

| 字段 | 类型 | 必填 | 格式 / 示例 / 约束 | 用途说明 |
|---|---|---|---|---|
| `error` | `object` | 是 | - | - |
| `error.kind` | `string` | 是 | 示例: "unauthenticated" | - |
| `error.message` | `string` | 是 | 示例: "API key is missing or invalid" | - |
| `error.request_id` | `string` | 是 | 格式: uuid；示例: "33ba813e-fc63-4af8-b338-7f7486f82202" | - |

##### `404 Not Found`

Resource or client does not exist.

内容类型：`application/json`

返回示例：

```json
{
  "error": {
    "kind": "client_not_found",
    "message": "client was not found",
    "request_id": "33ba813e-fc63-4af8-b338-7f7486f82202"
  }
}
```

返回字段：

| 字段 | 类型 | 必填 | 格式 / 示例 / 约束 | 用途说明 |
|---|---|---|---|---|
| `error` | `object` | 是 | - | - |
| `error.kind` | `string` | 是 | 示例: "client_not_found" | - |
| `error.message` | `string` | 是 | 示例: "client was not found" | - |
| `error.request_id` | `string` | 是 | 格式: uuid；示例: "33ba813e-fc63-4af8-b338-7f7486f82202" | - |

##### `405 Method Not Allowed`

HTTP method is not accepted; inspect the Allow header.

内容类型：`application/json`

返回示例：

```json
{
  "error": {
    "kind": "method_not_allowed",
    "message": "method is not allowed",
    "request_id": "33ba813e-fc63-4af8-b338-7f7486f82202"
  }
}
```

返回字段：

| 字段 | 类型 | 必填 | 格式 / 示例 / 约束 | 用途说明 |
|---|---|---|---|---|
| `error` | `object` | 是 | - | - |
| `error.kind` | `string` | 是 | 示例: "method_not_allowed" | - |
| `error.message` | `string` | 是 | 示例: "method is not allowed" | - |
| `error.request_id` | `string` | 是 | 格式: uuid；示例: "33ba813e-fc63-4af8-b338-7f7486f82202" | - |

##### `409 Conflict`

Digest, revision, state, uniqueness, lock, or recovery conflict.

内容类型：`application/json`

返回示例：

```json
{
  "error": {
    "kind": "configuration_conflict",
    "message": "configuration state changed or is busy",
    "request_id": "33ba813e-fc63-4af8-b338-7f7486f82202"
  }
}
```

返回字段：

| 字段 | 类型 | 必填 | 格式 / 示例 / 约束 | 用途说明 |
|---|---|---|---|---|
| `error` | `object` | 是 | - | - |
| `error.kind` | `string` | 是 | 示例: "configuration_conflict" | - |
| `error.message` | `string` | 是 | 示例: "configuration state changed or is busy" | - |
| `error.request_id` | `string` | 是 | 格式: uuid；示例: "33ba813e-fc63-4af8-b338-7f7486f82202" | - |

##### `422 Unprocessable Entity`

JSON is valid but violates client, IPv4, or configuration semantics.

内容类型：`application/json`

返回示例：

```json
{
  "error": {
    "kind": "invalid_client",
    "message": "client request is not valid for the current state",
    "request_id": "33ba813e-fc63-4af8-b338-7f7486f82202"
  }
}
```

返回字段：

| 字段 | 类型 | 必填 | 格式 / 示例 / 约束 | 用途说明 |
|---|---|---|---|---|
| `error` | `object` | 是 | - | - |
| `error.kind` | `string` | 是 | 示例: "invalid_client" | - |
| `error.message` | `string` | 是 | 示例: "client request is not valid for the current state" | - |
| `error.request_id` | `string` | 是 | 格式: uuid；示例: "33ba813e-fc63-4af8-b338-7f7486f82202" | - |

##### `500 Internal Server Error`

Unclassified internal failure with no implementation details exposed.

内容类型：`application/json`

返回示例：

```json
{
  "error": {
    "kind": "internal_error",
    "message": "request could not be completed",
    "request_id": "33ba813e-fc63-4af8-b338-7f7486f82202"
  }
}
```

返回字段：

| 字段 | 类型 | 必填 | 格式 / 示例 / 约束 | 用途说明 |
|---|---|---|---|---|
| `error` | `object` | 是 | - | - |
| `error.kind` | `string` | 是 | 示例: "internal_error" | - |
| `error.message` | `string` | 是 | 示例: "request could not be completed" | - |
| `error.request_id` | `string` | 是 | 格式: uuid；示例: "33ba813e-fc63-4af8-b338-7f7486f82202" | - |

##### `503 Service Unavailable`

Authentication storage, PKI, OpenVPN, broker, supervisor, or another required dependency is unavailable.

内容类型：`application/json`

返回示例：

```json
{
  "error": {
    "kind": "runtime_unavailable",
    "message": "OpenVPN runtime is unavailable",
    "request_id": "33ba813e-fc63-4af8-b338-7f7486f82202"
  }
}
```

返回字段：

| 字段 | 类型 | 必填 | 格式 / 示例 / 约束 | 用途说明 |
|---|---|---|---|---|
| `error` | `object` | 是 | - | - |
| `error.kind` | `string` | 是 | 示例: "runtime_unavailable" | - |
| `error.message` | `string` | 是 | 示例: "OpenVPN runtime is unavailable" | - |
| `error.request_id` | `string` | 是 | 格式: uuid；示例: "33ba813e-fc63-4af8-b338-7f7486f82202" | - |

### 13. `PUT /api/v1/clients/{client_id}/ipv4`

Set client IPv4 intent. Use auto or dynamic without address. Use static with an address from the configured static region.

#### 请求参数

##### Header 参数

| 字段 | 位置 | 类型 | 必填 | 格式 / 示例 / 约束 | 用途说明 |
|---|---|---|---|---|---|
| `Authorization` | `header` | `string` | 是 | Bearer ovpn_v1.<uuid>.<secret> | API 身份认证凭据。Bearer 后填写服务生成的 API Key。 |
| `Content-Type` | `header` | `string` | 是 | application/json | 声明请求体使用 JSON 格式。 |

##### Path 参数

| 字段 | 位置 | 类型 | 必填 | 格式 / 示例 / 约束 | 用途说明 |
|---|---|---|---|---|---|
| `client_id` | `path` | `string` | 是 | 格式: uuid；示例: "c0d4f871-6ea6-42b3-9e7b-f00cc1ec354e" | Complete canonical immutable client UUID; names and UUID prefixes are rejected. |

##### JSON Body 字段

| 字段 | 位置 | 类型 | 必填 | 格式 / 示例 / 约束 | 用途说明 |
|---|---|---|---|---|---|
| `mode` | `body` | `string` | 是 | 可选值: "auto", "dynamic", "static" | Address selection mode: automatic allocation, dynamic allocation, or a specific static address. |
| `address` | `body` | `string` | 否 | 格式: ipv4；示例: "10.42.0.30" | Static IPv4 address. Required only for static mode and forbidden for auto/dynamic. |

#### 请求示例

```http
PUT /api/v1/clients/c0d4f871-6ea6-42b3-9e7b-f00cc1ec354e/ipv4 HTTP/1.1
Authorization: Bearer ovpn_v1.<uuid>.<secret>
Content-Type: application/json

{
  "mode": "auto"
}
```

#### 返回

##### `200 OK`

Committed address mutation and per-client runtime outcomes.

内容类型：`application/json`

返回示例：

```json
{
  "version": 1,
  "operation_id": "d41dbfce-b40e-4672-8a62-cb401f8c099c",
  "clients": [
    {
      "id": "c0d4f871-6ea6-42b3-9e7b-f00cc1ec354e",
      "name": "alice-laptop",
      "status": "active",
      "ipv4": {
        "mode": "static",
        "address": "10.42.0.30",
        "state": "configured"
      }
    }
  ],
  "kick_required": [
    "c0d4f871-6ea6-42b3-9e7b-f00cc1ec354e"
  ],
  "runtime": [
    {
      "client_id": "c0d4f871-6ea6-42b3-9e7b-f00cc1ec354e",
      "status": "ok",
      "result": {
        "version": 1,
        "client_id": "c0d4f871-6ea6-42b3-9e7b-f00cc1ec354e",
        "client_name": "alice-laptop",
        "was_connected": false,
        "disconnected": false,
        "connections": 0
      }
    }
  ]
}
```

返回字段：

| 字段 | 类型 | 必填 | 格式 / 示例 / 约束 | 用途说明 |
|---|---|---|---|---|
| `version` | `integer` | 是 | 固定值: 1 | - |
| `operation_id` | `string` | 是 | 格式: uuid；示例: "d41dbfce-b40e-4672-8a62-cb401f8c099c" | - |
| `clients` | `object[]` | 是 | 示例: [{"id":"c0d4f871-6ea6-42b3-9e7b-f00cc1ec354e","name":"alice-laptop","status":"active","ipv4":{"mode":"static","address":"10.42.0.30","state":"configured"}}] | - |
| `clients[].id` | `string` | 是 | 格式: uuid；示例: "c0d4f871-6ea6-42b3-9e7b-f00cc1ec354e" | - |
| `clients[].name` | `string` | 是 | 格式: `^[A-Za-z0-9][A-Za-z0-9_.-]*$`；示例: "alice-laptop" | - |
| `clients[].status` | `string` | 是 | 可选值: "active", "revoked", "deleted" | - |
| `clients[].ipv4` | `object` | 是 | - | - |
| `clients[].ipv4.mode` | `string` | 是 | 可选值: "none", "static", "dynamic" | - |
| `clients[].ipv4.address` | `string \| null` | 是 | 格式: ipv4；示例: "10.42.0.30" | - |
| `clients[].ipv4.state` | `string` | 是 | 可选值: "configured", "retained", "unavailable" | - |
| `clients[].connection` | `string` | 否 | 示例: "string" | - |
| `kick_required` | `string[]` | 是 | 示例: ["c0d4f871-6ea6-42b3-9e7b-f00cc1ec354e"] | - |
| `runtime` | `object[]` | 是 | 示例: [{"client_id":"c0d4f871-6ea6-42b3-9e7b-f00cc1ec354e","status":"ok","result":{"version":1,"client_id":"c0d4f871-6ea6-42b3-9e7b-f00cc1ec354e","client_name":"alice-laptop","was_connected":false,"disconnected":false,"connections":0}}] | - |
| `runtime[].client_id` | `string` | 否 | 格式: uuid；示例: "c0d4f871-6ea6-42b3-9e7b-f00cc1ec354e" | - |
| `runtime[].status` | `string` | 是 | 可选值: "ok", "unavailable" | - |
| `runtime[].result` | `object` | 否 | - | - |
| `runtime[].result.version` | `integer` | 否 | 固定值: 1 | - |
| `runtime[].result.client_id` | `string` | 否 | 格式: uuid；示例: "c0d4f871-6ea6-42b3-9e7b-f00cc1ec354e" | - |
| `runtime[].result.client_name` | `string` | 否 | 示例: "alice-laptop" | - |
| `runtime[].result.was_connected` | `boolean` | 否 | 示例: false | - |
| `runtime[].result.disconnected` | `boolean` | 否 | 示例: false | - |
| `runtime[].result.connections` | `integer` | 否 | 最小值: 0 | - |

##### `400 Bad Request`

Malformed path, query, header, media type, body, or JSON.

内容类型：`application/json`

返回示例：

```json
{
  "error": {
    "kind": "invalid_json",
    "message": "request body is invalid",
    "request_id": "33ba813e-fc63-4af8-b338-7f7486f82202"
  }
}
```

返回字段：

| 字段 | 类型 | 必填 | 格式 / 示例 / 约束 | 用途说明 |
|---|---|---|---|---|
| `error` | `object` | 是 | - | - |
| `error.kind` | `string` | 是 | 示例: "invalid_json" | - |
| `error.message` | `string` | 是 | 示例: "request body is invalid" | - |
| `error.request_id` | `string` | 是 | 格式: uuid；示例: "33ba813e-fc63-4af8-b338-7f7486f82202" | - |

##### `401 Unauthorized`

Bearer API key is missing, malformed, unknown, or deleted.

内容类型：`application/json`

返回示例：

```json
{
  "error": {
    "kind": "unauthenticated",
    "message": "API key is missing or invalid",
    "request_id": "33ba813e-fc63-4af8-b338-7f7486f82202"
  }
}
```

返回字段：

| 字段 | 类型 | 必填 | 格式 / 示例 / 约束 | 用途说明 |
|---|---|---|---|---|
| `error` | `object` | 是 | - | - |
| `error.kind` | `string` | 是 | 示例: "unauthenticated" | - |
| `error.message` | `string` | 是 | 示例: "API key is missing or invalid" | - |
| `error.request_id` | `string` | 是 | 格式: uuid；示例: "33ba813e-fc63-4af8-b338-7f7486f82202" | - |

##### `404 Not Found`

Resource or client does not exist.

内容类型：`application/json`

返回示例：

```json
{
  "error": {
    "kind": "client_not_found",
    "message": "client was not found",
    "request_id": "33ba813e-fc63-4af8-b338-7f7486f82202"
  }
}
```

返回字段：

| 字段 | 类型 | 必填 | 格式 / 示例 / 约束 | 用途说明 |
|---|---|---|---|---|
| `error` | `object` | 是 | - | - |
| `error.kind` | `string` | 是 | 示例: "client_not_found" | - |
| `error.message` | `string` | 是 | 示例: "client was not found" | - |
| `error.request_id` | `string` | 是 | 格式: uuid；示例: "33ba813e-fc63-4af8-b338-7f7486f82202" | - |

##### `405 Method Not Allowed`

HTTP method is not accepted; inspect the Allow header.

内容类型：`application/json`

返回示例：

```json
{
  "error": {
    "kind": "method_not_allowed",
    "message": "method is not allowed",
    "request_id": "33ba813e-fc63-4af8-b338-7f7486f82202"
  }
}
```

返回字段：

| 字段 | 类型 | 必填 | 格式 / 示例 / 约束 | 用途说明 |
|---|---|---|---|---|
| `error` | `object` | 是 | - | - |
| `error.kind` | `string` | 是 | 示例: "method_not_allowed" | - |
| `error.message` | `string` | 是 | 示例: "method is not allowed" | - |
| `error.request_id` | `string` | 是 | 格式: uuid；示例: "33ba813e-fc63-4af8-b338-7f7486f82202" | - |

##### `409 Conflict`

Digest, revision, state, uniqueness, lock, or recovery conflict.

内容类型：`application/json`

返回示例：

```json
{
  "error": {
    "kind": "configuration_conflict",
    "message": "configuration state changed or is busy",
    "request_id": "33ba813e-fc63-4af8-b338-7f7486f82202"
  }
}
```

返回字段：

| 字段 | 类型 | 必填 | 格式 / 示例 / 约束 | 用途说明 |
|---|---|---|---|---|
| `error` | `object` | 是 | - | - |
| `error.kind` | `string` | 是 | 示例: "configuration_conflict" | - |
| `error.message` | `string` | 是 | 示例: "configuration state changed or is busy" | - |
| `error.request_id` | `string` | 是 | 格式: uuid；示例: "33ba813e-fc63-4af8-b338-7f7486f82202" | - |

##### `422 Unprocessable Entity`

JSON is valid but violates client, IPv4, or configuration semantics.

内容类型：`application/json`

返回示例：

```json
{
  "error": {
    "kind": "invalid_client",
    "message": "client request is not valid for the current state",
    "request_id": "33ba813e-fc63-4af8-b338-7f7486f82202"
  }
}
```

返回字段：

| 字段 | 类型 | 必填 | 格式 / 示例 / 约束 | 用途说明 |
|---|---|---|---|---|
| `error` | `object` | 是 | - | - |
| `error.kind` | `string` | 是 | 示例: "invalid_client" | - |
| `error.message` | `string` | 是 | 示例: "client request is not valid for the current state" | - |
| `error.request_id` | `string` | 是 | 格式: uuid；示例: "33ba813e-fc63-4af8-b338-7f7486f82202" | - |

##### `500 Internal Server Error`

Unclassified internal failure with no implementation details exposed.

内容类型：`application/json`

返回示例：

```json
{
  "error": {
    "kind": "internal_error",
    "message": "request could not be completed",
    "request_id": "33ba813e-fc63-4af8-b338-7f7486f82202"
  }
}
```

返回字段：

| 字段 | 类型 | 必填 | 格式 / 示例 / 约束 | 用途说明 |
|---|---|---|---|---|
| `error` | `object` | 是 | - | - |
| `error.kind` | `string` | 是 | 示例: "internal_error" | - |
| `error.message` | `string` | 是 | 示例: "request could not be completed" | - |
| `error.request_id` | `string` | 是 | 格式: uuid；示例: "33ba813e-fc63-4af8-b338-7f7486f82202" | - |

##### `503 Service Unavailable`

Authentication storage, PKI, OpenVPN, broker, supervisor, or another required dependency is unavailable.

内容类型：`application/json`

返回示例：

```json
{
  "error": {
    "kind": "runtime_unavailable",
    "message": "OpenVPN runtime is unavailable",
    "request_id": "33ba813e-fc63-4af8-b338-7f7486f82202"
  }
}
```

返回字段：

| 字段 | 类型 | 必填 | 格式 / 示例 / 约束 | 用途说明 |
|---|---|---|---|---|
| `error` | `object` | 是 | - | - |
| `error.kind` | `string` | 是 | 示例: "runtime_unavailable" | - |
| `error.message` | `string` | 是 | 示例: "OpenVPN runtime is unavailable" | - |
| `error.request_id` | `string` | 是 | 格式: uuid；示例: "33ba813e-fc63-4af8-b338-7f7486f82202" | - |

### 14. `DELETE /api/v1/clients/{client_id}/ipv4`

Release a revoked client's retained IPv4. Requires an empty body and is valid only for a revoked client with a retained assignment.

#### 请求参数

##### Header 参数

| 字段 | 位置 | 类型 | 必填 | 格式 / 示例 / 约束 | 用途说明 |
|---|---|---|---|---|---|
| `Authorization` | `header` | `string` | 是 | Bearer ovpn_v1.<uuid>.<secret> | API 身份认证凭据。Bearer 后填写服务生成的 API Key。 |

##### Path 参数

| 字段 | 位置 | 类型 | 必填 | 格式 / 示例 / 约束 | 用途说明 |
|---|---|---|---|---|---|
| `client_id` | `path` | `string` | 是 | 格式: uuid；示例: "c0d4f871-6ea6-42b3-9e7b-f00cc1ec354e" | Complete canonical immutable client UUID; names and UUID prefixes are rejected. |

#### 请求示例

```http
DELETE /api/v1/clients/c0d4f871-6ea6-42b3-9e7b-f00cc1ec354e/ipv4 HTTP/1.1
Authorization: Bearer ovpn_v1.<uuid>.<secret>
```

#### 返回

##### `200 OK`

Revoked client's retained address released.

内容类型：`application/json`

返回示例：

```json
{
  "version": 1,
  "operation_id": "d41dbfce-b40e-4672-8a62-cb401f8c099c",
  "clients": [
    {
      "id": "c0d4f871-6ea6-42b3-9e7b-f00cc1ec354e",
      "name": "alice-notebook",
      "status": "revoked",
      "ipv4": {
        "mode": "none",
        "address": null,
        "state": "unavailable"
      }
    }
  ],
  "kick_required": [],
  "runtime": []
}
```

返回字段：

| 字段 | 类型 | 必填 | 格式 / 示例 / 约束 | 用途说明 |
|---|---|---|---|---|
| `version` | `integer` | 是 | 固定值: 1 | - |
| `operation_id` | `string` | 是 | 格式: uuid；示例: "d41dbfce-b40e-4672-8a62-cb401f8c099c" | - |
| `clients` | `object[]` | 是 | 示例: [{"id":"c0d4f871-6ea6-42b3-9e7b-f00cc1ec354e","name":"alice-notebook","status":"revoked","ipv4":{"mode":"none","address":null,"state":"unavailable"}}] | - |
| `clients[].id` | `string` | 是 | 格式: uuid；示例: "c0d4f871-6ea6-42b3-9e7b-f00cc1ec354e" | - |
| `clients[].name` | `string` | 是 | 格式: `^[A-Za-z0-9][A-Za-z0-9_.-]*$`；示例: "alice-notebook" | - |
| `clients[].status` | `string` | 是 | 可选值: "active", "revoked", "deleted" | - |
| `clients[].ipv4` | `object` | 是 | - | - |
| `clients[].ipv4.mode` | `string` | 是 | 可选值: "none", "static", "dynamic" | - |
| `clients[].ipv4.address` | `string \| null` | 是 | 格式: ipv4；示例: "10.42.0.30" | - |
| `clients[].ipv4.state` | `string` | 是 | 可选值: "configured", "retained", "unavailable" | - |
| `clients[].connection` | `string` | 否 | 示例: "string" | - |
| `kick_required` | `string[]` | 是 | 示例: [] | - |
| `runtime` | `object[]` | 是 | 示例: [] | - |
| `runtime[].client_id` | `string` | 否 | 格式: uuid；示例: "c0d4f871-6ea6-42b3-9e7b-f00cc1ec354e" | - |
| `runtime[].status` | `string` | 是 | 可选值: "ok", "unavailable" | - |
| `runtime[].result` | `object` | 否 | - | - |
| `runtime[].result.version` | `integer` | 否 | 固定值: 1 | - |
| `runtime[].result.client_id` | `string` | 否 | 格式: uuid；示例: "c0d4f871-6ea6-42b3-9e7b-f00cc1ec354e" | - |
| `runtime[].result.client_name` | `string` | 否 | 示例: "string" | - |
| `runtime[].result.was_connected` | `boolean` | 否 | 示例: false | - |
| `runtime[].result.disconnected` | `boolean` | 否 | 示例: false | - |
| `runtime[].result.connections` | `integer` | 否 | 最小值: 0 | - |

##### `400 Bad Request`

Malformed path, query, header, media type, body, or JSON.

内容类型：`application/json`

返回示例：

```json
{
  "error": {
    "kind": "invalid_json",
    "message": "request body is invalid",
    "request_id": "33ba813e-fc63-4af8-b338-7f7486f82202"
  }
}
```

返回字段：

| 字段 | 类型 | 必填 | 格式 / 示例 / 约束 | 用途说明 |
|---|---|---|---|---|
| `error` | `object` | 是 | - | - |
| `error.kind` | `string` | 是 | 示例: "invalid_json" | - |
| `error.message` | `string` | 是 | 示例: "request body is invalid" | - |
| `error.request_id` | `string` | 是 | 格式: uuid；示例: "33ba813e-fc63-4af8-b338-7f7486f82202" | - |

##### `401 Unauthorized`

Bearer API key is missing, malformed, unknown, or deleted.

内容类型：`application/json`

返回示例：

```json
{
  "error": {
    "kind": "unauthenticated",
    "message": "API key is missing or invalid",
    "request_id": "33ba813e-fc63-4af8-b338-7f7486f82202"
  }
}
```

返回字段：

| 字段 | 类型 | 必填 | 格式 / 示例 / 约束 | 用途说明 |
|---|---|---|---|---|
| `error` | `object` | 是 | - | - |
| `error.kind` | `string` | 是 | 示例: "unauthenticated" | - |
| `error.message` | `string` | 是 | 示例: "API key is missing or invalid" | - |
| `error.request_id` | `string` | 是 | 格式: uuid；示例: "33ba813e-fc63-4af8-b338-7f7486f82202" | - |

##### `404 Not Found`

Resource or client does not exist.

内容类型：`application/json`

返回示例：

```json
{
  "error": {
    "kind": "client_not_found",
    "message": "client was not found",
    "request_id": "33ba813e-fc63-4af8-b338-7f7486f82202"
  }
}
```

返回字段：

| 字段 | 类型 | 必填 | 格式 / 示例 / 约束 | 用途说明 |
|---|---|---|---|---|
| `error` | `object` | 是 | - | - |
| `error.kind` | `string` | 是 | 示例: "client_not_found" | - |
| `error.message` | `string` | 是 | 示例: "client was not found" | - |
| `error.request_id` | `string` | 是 | 格式: uuid；示例: "33ba813e-fc63-4af8-b338-7f7486f82202" | - |

##### `405 Method Not Allowed`

HTTP method is not accepted; inspect the Allow header.

内容类型：`application/json`

返回示例：

```json
{
  "error": {
    "kind": "method_not_allowed",
    "message": "method is not allowed",
    "request_id": "33ba813e-fc63-4af8-b338-7f7486f82202"
  }
}
```

返回字段：

| 字段 | 类型 | 必填 | 格式 / 示例 / 约束 | 用途说明 |
|---|---|---|---|---|
| `error` | `object` | 是 | - | - |
| `error.kind` | `string` | 是 | 示例: "method_not_allowed" | - |
| `error.message` | `string` | 是 | 示例: "method is not allowed" | - |
| `error.request_id` | `string` | 是 | 格式: uuid；示例: "33ba813e-fc63-4af8-b338-7f7486f82202" | - |

##### `409 Conflict`

Digest, revision, state, uniqueness, lock, or recovery conflict.

内容类型：`application/json`

返回示例：

```json
{
  "error": {
    "kind": "configuration_conflict",
    "message": "configuration state changed or is busy",
    "request_id": "33ba813e-fc63-4af8-b338-7f7486f82202"
  }
}
```

返回字段：

| 字段 | 类型 | 必填 | 格式 / 示例 / 约束 | 用途说明 |
|---|---|---|---|---|
| `error` | `object` | 是 | - | - |
| `error.kind` | `string` | 是 | 示例: "configuration_conflict" | - |
| `error.message` | `string` | 是 | 示例: "configuration state changed or is busy" | - |
| `error.request_id` | `string` | 是 | 格式: uuid；示例: "33ba813e-fc63-4af8-b338-7f7486f82202" | - |

##### `422 Unprocessable Entity`

JSON is valid but violates client, IPv4, or configuration semantics.

内容类型：`application/json`

返回示例：

```json
{
  "error": {
    "kind": "invalid_client",
    "message": "client request is not valid for the current state",
    "request_id": "33ba813e-fc63-4af8-b338-7f7486f82202"
  }
}
```

返回字段：

| 字段 | 类型 | 必填 | 格式 / 示例 / 约束 | 用途说明 |
|---|---|---|---|---|
| `error` | `object` | 是 | - | - |
| `error.kind` | `string` | 是 | 示例: "invalid_client" | - |
| `error.message` | `string` | 是 | 示例: "client request is not valid for the current state" | - |
| `error.request_id` | `string` | 是 | 格式: uuid；示例: "33ba813e-fc63-4af8-b338-7f7486f82202" | - |

##### `500 Internal Server Error`

Unclassified internal failure with no implementation details exposed.

内容类型：`application/json`

返回示例：

```json
{
  "error": {
    "kind": "internal_error",
    "message": "request could not be completed",
    "request_id": "33ba813e-fc63-4af8-b338-7f7486f82202"
  }
}
```

返回字段：

| 字段 | 类型 | 必填 | 格式 / 示例 / 约束 | 用途说明 |
|---|---|---|---|---|
| `error` | `object` | 是 | - | - |
| `error.kind` | `string` | 是 | 示例: "internal_error" | - |
| `error.message` | `string` | 是 | 示例: "request could not be completed" | - |
| `error.request_id` | `string` | 是 | 格式: uuid；示例: "33ba813e-fc63-4af8-b338-7f7486f82202" | - |

##### `503 Service Unavailable`

Authentication storage, PKI, OpenVPN, broker, supervisor, or another required dependency is unavailable.

内容类型：`application/json`

返回示例：

```json
{
  "error": {
    "kind": "runtime_unavailable",
    "message": "OpenVPN runtime is unavailable",
    "request_id": "33ba813e-fc63-4af8-b338-7f7486f82202"
  }
}
```

返回字段：

| 字段 | 类型 | 必填 | 格式 / 示例 / 约束 | 用途说明 |
|---|---|---|---|---|
| `error` | `object` | 是 | - | - |
| `error.kind` | `string` | 是 | 示例: "runtime_unavailable" | - |
| `error.message` | `string` | 是 | 示例: "OpenVPN runtime is unavailable" | - |
| `error.request_id` | `string` | 是 | 格式: uuid；示例: "33ba813e-fc63-4af8-b338-7f7486f82202" | - |

### 15. `POST /api/v1/clients/{client_id}/disconnect`

Disconnect current client sessions. Requires an empty body. A client with no active session returns 200 with was_connected=false.

#### 请求参数

##### Header 参数

| 字段 | 位置 | 类型 | 必填 | 格式 / 示例 / 约束 | 用途说明 |
|---|---|---|---|---|---|
| `Authorization` | `header` | `string` | 是 | Bearer ovpn_v1.<uuid>.<secret> | API 身份认证凭据。Bearer 后填写服务生成的 API Key。 |

##### Path 参数

| 字段 | 位置 | 类型 | 必填 | 格式 / 示例 / 约束 | 用途说明 |
|---|---|---|---|---|---|
| `client_id` | `path` | `string` | 是 | 格式: uuid；示例: "c0d4f871-6ea6-42b3-9e7b-f00cc1ec354e" | Complete canonical immutable client UUID; names and UUID prefixes are rejected. |

#### 请求示例

```http
POST /api/v1/clients/c0d4f871-6ea6-42b3-9e7b-f00cc1ec354e/disconnect HTTP/1.1
Authorization: Bearer ovpn_v1.<uuid>.<secret>
```

#### 返回

##### `200 OK`

Disconnect outcome, including successful no-op when no session exists.

内容类型：`application/json`

返回示例：

```json
{
  "version": 1,
  "client_id": "c0d4f871-6ea6-42b3-9e7b-f00cc1ec354e",
  "client_name": "alice-laptop",
  "was_connected": false,
  "disconnected": false,
  "connections": 0
}
```

返回字段：

| 字段 | 类型 | 必填 | 格式 / 示例 / 约束 | 用途说明 |
|---|---|---|---|---|
| `version` | `integer` | 是 | 固定值: 1 | - |
| `client_id` | `string` | 是 | 格式: uuid；示例: "c0d4f871-6ea6-42b3-9e7b-f00cc1ec354e" | - |
| `client_name` | `string` | 是 | 示例: "alice-laptop" | - |
| `was_connected` | `boolean` | 是 | 示例: false | - |
| `disconnected` | `boolean` | 是 | 示例: false | - |
| `connections` | `integer` | 是 | 最小值: 0 | - |

##### `400 Bad Request`

Malformed path, query, header, media type, body, or JSON.

内容类型：`application/json`

返回示例：

```json
{
  "error": {
    "kind": "invalid_json",
    "message": "request body is invalid",
    "request_id": "33ba813e-fc63-4af8-b338-7f7486f82202"
  }
}
```

返回字段：

| 字段 | 类型 | 必填 | 格式 / 示例 / 约束 | 用途说明 |
|---|---|---|---|---|
| `error` | `object` | 是 | - | - |
| `error.kind` | `string` | 是 | 示例: "invalid_json" | - |
| `error.message` | `string` | 是 | 示例: "request body is invalid" | - |
| `error.request_id` | `string` | 是 | 格式: uuid；示例: "33ba813e-fc63-4af8-b338-7f7486f82202" | - |

##### `401 Unauthorized`

Bearer API key is missing, malformed, unknown, or deleted.

内容类型：`application/json`

返回示例：

```json
{
  "error": {
    "kind": "unauthenticated",
    "message": "API key is missing or invalid",
    "request_id": "33ba813e-fc63-4af8-b338-7f7486f82202"
  }
}
```

返回字段：

| 字段 | 类型 | 必填 | 格式 / 示例 / 约束 | 用途说明 |
|---|---|---|---|---|
| `error` | `object` | 是 | - | - |
| `error.kind` | `string` | 是 | 示例: "unauthenticated" | - |
| `error.message` | `string` | 是 | 示例: "API key is missing or invalid" | - |
| `error.request_id` | `string` | 是 | 格式: uuid；示例: "33ba813e-fc63-4af8-b338-7f7486f82202" | - |

##### `404 Not Found`

Resource or client does not exist.

内容类型：`application/json`

返回示例：

```json
{
  "error": {
    "kind": "client_not_found",
    "message": "client was not found",
    "request_id": "33ba813e-fc63-4af8-b338-7f7486f82202"
  }
}
```

返回字段：

| 字段 | 类型 | 必填 | 格式 / 示例 / 约束 | 用途说明 |
|---|---|---|---|---|
| `error` | `object` | 是 | - | - |
| `error.kind` | `string` | 是 | 示例: "client_not_found" | - |
| `error.message` | `string` | 是 | 示例: "client was not found" | - |
| `error.request_id` | `string` | 是 | 格式: uuid；示例: "33ba813e-fc63-4af8-b338-7f7486f82202" | - |

##### `405 Method Not Allowed`

HTTP method is not accepted; inspect the Allow header.

内容类型：`application/json`

返回示例：

```json
{
  "error": {
    "kind": "method_not_allowed",
    "message": "method is not allowed",
    "request_id": "33ba813e-fc63-4af8-b338-7f7486f82202"
  }
}
```

返回字段：

| 字段 | 类型 | 必填 | 格式 / 示例 / 约束 | 用途说明 |
|---|---|---|---|---|
| `error` | `object` | 是 | - | - |
| `error.kind` | `string` | 是 | 示例: "method_not_allowed" | - |
| `error.message` | `string` | 是 | 示例: "method is not allowed" | - |
| `error.request_id` | `string` | 是 | 格式: uuid；示例: "33ba813e-fc63-4af8-b338-7f7486f82202" | - |

##### `500 Internal Server Error`

Unclassified internal failure with no implementation details exposed.

内容类型：`application/json`

返回示例：

```json
{
  "error": {
    "kind": "internal_error",
    "message": "request could not be completed",
    "request_id": "33ba813e-fc63-4af8-b338-7f7486f82202"
  }
}
```

返回字段：

| 字段 | 类型 | 必填 | 格式 / 示例 / 约束 | 用途说明 |
|---|---|---|---|---|
| `error` | `object` | 是 | - | - |
| `error.kind` | `string` | 是 | 示例: "internal_error" | - |
| `error.message` | `string` | 是 | 示例: "request could not be completed" | - |
| `error.request_id` | `string` | 是 | 格式: uuid；示例: "33ba813e-fc63-4af8-b338-7f7486f82202" | - |

##### `503 Service Unavailable`

Authentication storage, PKI, OpenVPN, broker, supervisor, or another required dependency is unavailable.

内容类型：`application/json`

返回示例：

```json
{
  "error": {
    "kind": "runtime_unavailable",
    "message": "OpenVPN runtime is unavailable",
    "request_id": "33ba813e-fc63-4af8-b338-7f7486f82202"
  }
}
```

返回字段：

| 字段 | 类型 | 必填 | 格式 / 示例 / 约束 | 用途说明 |
|---|---|---|---|---|
| `error` | `object` | 是 | - | - |
| `error.kind` | `string` | 是 | 示例: "runtime_unavailable" | - |
| `error.message` | `string` | 是 | 示例: "OpenVPN runtime is unavailable" | - |
| `error.request_id` | `string` | 是 | 格式: uuid；示例: "33ba813e-fc63-4af8-b338-7f7486f82202" | - |

### 16. `GET /api/v1/runtime`

Read OpenVPN runtime status and sessions

#### 请求参数

##### Header 参数

| 字段 | 位置 | 类型 | 必填 | 格式 / 示例 / 约束 | 用途说明 |
|---|---|---|---|---|---|
| `Authorization` | `header` | `string` | 是 | Bearer ovpn_v1.<uuid>.<secret> | API 身份认证凭据。Bearer 后填写服务生成的 API Key。 |

#### 请求示例

```http
GET /api/v1/runtime HTTP/1.1
Authorization: Bearer ovpn_v1.<uuid>.<secret>
```

#### 返回

##### `200 OK`

OpenVPN runtime status.

内容类型：`application/json`

返回示例：

```json
{
  "version": 1,
  "daemon": "running",
  "management": "connected",
  "client_count": 1,
  "clients": [
    {
      "client_id": "c0d4f871-6ea6-42b3-9e7b-f00cc1ec354e",
      "client_name": "alice-laptop",
      "remote_address": "203.0.113.10:53210",
      "virtual_address": "10.42.0.30"
    }
  ]
}
```

返回字段：

| 字段 | 类型 | 必填 | 格式 / 示例 / 约束 | 用途说明 |
|---|---|---|---|---|
| `version` | `integer` | 是 | 固定值: 1 | - |
| `daemon` | `string` | 是 | 示例: "running" | - |
| `management` | `string` | 是 | 示例: "connected" | - |
| `client_count` | `integer` | 是 | 最小值: 0；示例: 1 | - |
| `clients` | `object[]` | 是 | 示例: [{"client_id":"c0d4f871-6ea6-42b3-9e7b-f00cc1ec354e","client_name":"alice-laptop","remote_address":"203.0.113.10:53210","virtual_address":"10.42.0.30"}] | - |
| `clients[].client_id` | `string` | 是 | 格式: uuid；示例: "c0d4f871-6ea6-42b3-9e7b-f00cc1ec354e" | - |
| `clients[].client_name` | `string` | 否 | 示例: "alice-laptop" | - |
| `clients[].remote_address` | `string` | 否 | 示例: "203.0.113.10:53210" | - |
| `clients[].virtual_address` | `string` | 否 | 格式: ipv4；示例: "10.42.0.30" | - |

##### `401 Unauthorized`

Bearer API key is missing, malformed, unknown, or deleted.

内容类型：`application/json`

返回示例：

```json
{
  "error": {
    "kind": "unauthenticated",
    "message": "API key is missing or invalid",
    "request_id": "33ba813e-fc63-4af8-b338-7f7486f82202"
  }
}
```

返回字段：

| 字段 | 类型 | 必填 | 格式 / 示例 / 约束 | 用途说明 |
|---|---|---|---|---|
| `error` | `object` | 是 | - | - |
| `error.kind` | `string` | 是 | 示例: "unauthenticated" | - |
| `error.message` | `string` | 是 | 示例: "API key is missing or invalid" | - |
| `error.request_id` | `string` | 是 | 格式: uuid；示例: "33ba813e-fc63-4af8-b338-7f7486f82202" | - |

##### `405 Method Not Allowed`

HTTP method is not accepted; inspect the Allow header.

内容类型：`application/json`

返回示例：

```json
{
  "error": {
    "kind": "method_not_allowed",
    "message": "method is not allowed",
    "request_id": "33ba813e-fc63-4af8-b338-7f7486f82202"
  }
}
```

返回字段：

| 字段 | 类型 | 必填 | 格式 / 示例 / 约束 | 用途说明 |
|---|---|---|---|---|
| `error` | `object` | 是 | - | - |
| `error.kind` | `string` | 是 | 示例: "method_not_allowed" | - |
| `error.message` | `string` | 是 | 示例: "method is not allowed" | - |
| `error.request_id` | `string` | 是 | 格式: uuid；示例: "33ba813e-fc63-4af8-b338-7f7486f82202" | - |

##### `500 Internal Server Error`

Unclassified internal failure with no implementation details exposed.

内容类型：`application/json`

返回示例：

```json
{
  "error": {
    "kind": "internal_error",
    "message": "request could not be completed",
    "request_id": "33ba813e-fc63-4af8-b338-7f7486f82202"
  }
}
```

返回字段：

| 字段 | 类型 | 必填 | 格式 / 示例 / 约束 | 用途说明 |
|---|---|---|---|---|
| `error` | `object` | 是 | - | - |
| `error.kind` | `string` | 是 | 示例: "internal_error" | - |
| `error.message` | `string` | 是 | 示例: "request could not be completed" | - |
| `error.request_id` | `string` | 是 | 格式: uuid；示例: "33ba813e-fc63-4af8-b338-7f7486f82202" | - |

##### `503 Service Unavailable`

Authentication storage, PKI, OpenVPN, broker, supervisor, or another required dependency is unavailable.

内容类型：`application/json`

返回示例：

```json
{
  "error": {
    "kind": "runtime_unavailable",
    "message": "OpenVPN runtime is unavailable",
    "request_id": "33ba813e-fc63-4af8-b338-7f7486f82202"
  }
}
```

返回字段：

| 字段 | 类型 | 必填 | 格式 / 示例 / 约束 | 用途说明 |
|---|---|---|---|---|
| `error` | `object` | 是 | - | - |
| `error.kind` | `string` | 是 | 示例: "runtime_unavailable" | - |
| `error.message` | `string` | 是 | 示例: "OpenVPN runtime is unavailable" | - |
| `error.request_id` | `string` | 是 | 格式: uuid；示例: "33ba813e-fc63-4af8-b338-7f7486f82202" | - |

### 17. `GET /api/v1/runtime/events`

Read recent structured runtime events

#### 请求参数

##### Header 参数

| 字段 | 位置 | 类型 | 必填 | 格式 / 示例 / 约束 | 用途说明 |
|---|---|---|---|---|---|
| `Authorization` | `header` | `string` | 是 | Bearer ovpn_v1.<uuid>.<secret> | API 身份认证凭据。Bearer 后填写服务生成的 API Key。 |

##### Query 参数

| 字段 | 位置 | 类型 | 必填 | 格式 / 示例 / 约束 | 用途说明 |
|---|---|---|---|---|---|
| `lines` | `query` | `integer` | 否 | 最小值: 0；最大值: 1000；默认值: 100 | Number of most recent events. Defaults to 100. |

#### 请求示例

```http
GET /api/v1/runtime/events?lines=100 HTTP/1.1
Authorization: Bearer ovpn_v1.<uuid>.<secret>
```

#### 返回

##### `200 OK`

Recent structured runtime events.

内容类型：`application/json`

返回示例：

```json
{
  "version": 1,
  "events": [
    {
      "timestamp": "2026-08-19T09:55:07Z",
      "event": "client-disconnect",
      "operation": "runtime.disconnect",
      "outcome": "success",
      "client_id": "c0d4f871-6ea6-42b3-9e7b-f00cc1ec354e",
      "client_name": "alice-laptop"
    }
  ]
}
```

返回字段：

| 字段 | 类型 | 必填 | 格式 / 示例 / 约束 | 用途说明 |
|---|---|---|---|---|
| `version` | `integer` | 是 | 固定值: 1 | - |
| `events` | `object[]` | 是 | 示例: [{"timestamp":"2026-08-19T09:55:07Z","event":"client-disconnect","operation":"runtime.disconnect","outcome":"success","client_id":"c0d4f871-6ea6-42b3-9e7b-f00cc1ec354e","client_name":"alice-laptop"}] | - |
| `events[].timestamp` | `string` | 是 | 格式: date-time；示例: "2026-08-19T09:55:07Z" | - |
| `events[].event` | `string` | 是 | 示例: "client-disconnect" | - |
| `events[].operation` | `string` | 是 | 示例: "runtime.disconnect" | - |
| `events[].outcome` | `string` | 是 | 示例: "success" | - |
| `events[].client_id` | `string \| null` | 否 | 格式: uuid；示例: "c0d4f871-6ea6-42b3-9e7b-f00cc1ec354e" | - |
| `events[].client_name` | `string \| null` | 否 | 示例: "alice-laptop" | - |

##### `400 Bad Request`

Malformed path, query, header, media type, body, or JSON.

内容类型：`application/json`

返回示例：

```json
{
  "error": {
    "kind": "invalid_json",
    "message": "request body is invalid",
    "request_id": "33ba813e-fc63-4af8-b338-7f7486f82202"
  }
}
```

返回字段：

| 字段 | 类型 | 必填 | 格式 / 示例 / 约束 | 用途说明 |
|---|---|---|---|---|
| `error` | `object` | 是 | - | - |
| `error.kind` | `string` | 是 | 示例: "invalid_json" | - |
| `error.message` | `string` | 是 | 示例: "request body is invalid" | - |
| `error.request_id` | `string` | 是 | 格式: uuid；示例: "33ba813e-fc63-4af8-b338-7f7486f82202" | - |

##### `401 Unauthorized`

Bearer API key is missing, malformed, unknown, or deleted.

内容类型：`application/json`

返回示例：

```json
{
  "error": {
    "kind": "unauthenticated",
    "message": "API key is missing or invalid",
    "request_id": "33ba813e-fc63-4af8-b338-7f7486f82202"
  }
}
```

返回字段：

| 字段 | 类型 | 必填 | 格式 / 示例 / 约束 | 用途说明 |
|---|---|---|---|---|
| `error` | `object` | 是 | - | - |
| `error.kind` | `string` | 是 | 示例: "unauthenticated" | - |
| `error.message` | `string` | 是 | 示例: "API key is missing or invalid" | - |
| `error.request_id` | `string` | 是 | 格式: uuid；示例: "33ba813e-fc63-4af8-b338-7f7486f82202" | - |

##### `405 Method Not Allowed`

HTTP method is not accepted; inspect the Allow header.

内容类型：`application/json`

返回示例：

```json
{
  "error": {
    "kind": "method_not_allowed",
    "message": "method is not allowed",
    "request_id": "33ba813e-fc63-4af8-b338-7f7486f82202"
  }
}
```

返回字段：

| 字段 | 类型 | 必填 | 格式 / 示例 / 约束 | 用途说明 |
|---|---|---|---|---|
| `error` | `object` | 是 | - | - |
| `error.kind` | `string` | 是 | 示例: "method_not_allowed" | - |
| `error.message` | `string` | 是 | 示例: "method is not allowed" | - |
| `error.request_id` | `string` | 是 | 格式: uuid；示例: "33ba813e-fc63-4af8-b338-7f7486f82202" | - |

##### `500 Internal Server Error`

Unclassified internal failure with no implementation details exposed.

内容类型：`application/json`

返回示例：

```json
{
  "error": {
    "kind": "internal_error",
    "message": "request could not be completed",
    "request_id": "33ba813e-fc63-4af8-b338-7f7486f82202"
  }
}
```

返回字段：

| 字段 | 类型 | 必填 | 格式 / 示例 / 约束 | 用途说明 |
|---|---|---|---|---|
| `error` | `object` | 是 | - | - |
| `error.kind` | `string` | 是 | 示例: "internal_error" | - |
| `error.message` | `string` | 是 | 示例: "request could not be completed" | - |
| `error.request_id` | `string` | 是 | 格式: uuid；示例: "33ba813e-fc63-4af8-b338-7f7486f82202" | - |

##### `503 Service Unavailable`

Authentication storage, PKI, OpenVPN, broker, supervisor, or another required dependency is unavailable.

内容类型：`application/json`

返回示例：

```json
{
  "error": {
    "kind": "runtime_unavailable",
    "message": "OpenVPN runtime is unavailable",
    "request_id": "33ba813e-fc63-4af8-b338-7f7486f82202"
  }
}
```

返回字段：

| 字段 | 类型 | 必填 | 格式 / 示例 / 约束 | 用途说明 |
|---|---|---|---|---|
| `error` | `object` | 是 | - | - |
| `error.kind` | `string` | 是 | 示例: "runtime_unavailable" | - |
| `error.message` | `string` | 是 | 示例: "OpenVPN runtime is unavailable" | - |
| `error.request_id` | `string` | 是 | 格式: uuid；示例: "33ba813e-fc63-4af8-b338-7f7486f82202" | - |

### 18. `GET /api/v1/config/applied`

Read the applied configuration revision

#### 请求参数

##### Header 参数

| 字段 | 位置 | 类型 | 必填 | 格式 / 示例 / 约束 | 用途说明 |
|---|---|---|---|---|---|
| `Authorization` | `header` | `string` | 是 | Bearer ovpn_v1.<uuid>.<secret> | API 身份认证凭据。Bearer 后填写服务生成的 API Key。 |

#### 请求示例

```http
GET /api/v1/config/applied HTTP/1.1
Authorization: Bearer ovpn_v1.<uuid>.<secret>
```

#### 返回

##### `200 OK`

Applied revision and normalized configuration.

内容类型：`application/json`

返回示例：

```json
{
  "revision": 12,
  "digest": "be7ed82796b39a77ce949201a4d9483cf09e56648f14b8b9b192bb83d2c4d042",
  "config": {
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
    "logging": {
      "max_bytes": 10485760,
      "backups": 5
    }
  }
}
```

返回字段：

| 字段 | 类型 | 必填 | 格式 / 示例 / 约束 | 用途说明 |
|---|---|---|---|---|
| `revision` | `integer` | 是 | 最小值: 1；示例: 12 | - |
| `digest` | `string` | 是 | 格式: `^[0-9a-f]{64}$`；示例: "be7ed82796b39a77ce949201a4d9483cf09e56648f14b8b9b192bb83d2c4d042" | Lowercase SHA-256 digest identifying an exact desired configuration. |
| `config` | `object` | 是 | - | - |
| `config.version` | `integer` | 是 | 固定值: 1 | Configuration schema version. Must be 1. |
| `config.server` | `object` | 是 | - | OpenVPN listener and transport settings. |
| `config.server.endpoint` | `string` | 是 | 示例: "vpn.example.com" | Public hostname or IP address placed in generated client profiles. |
| `config.server.protocol` | `string` | 是 | 可选值: "udp", "tcp" | OpenVPN transport protocol. |
| `config.server.family` | `string` | 是 | 可选值: "auto", "ipv4", "ipv6" | Address family used for the server transport. |
| `config.server.port` | `integer` | 是 | 最小值: 1；最大值: 65535；示例: 1194 | OpenVPN listener port. |
| `config.server.client_to_client` | `boolean` | 是 | 示例: true | Whether connected VPN clients may communicate directly. |
| `config.ipv4` | `object` | 是 | - | Server IPv4 network, allocation, NAT, DNS, and route settings. |
| `config.ipv4.network` | `string` | 是 | 格式: ipv4-cidr；示例: "10.42.0.0/24" | VPN IPv4 network in CIDR notation. |
| `config.ipv4.dynamic_pool_size` | `integer` | 是 | 最小值: 0；示例: 64 | Number of addresses reserved for dynamic allocation. |
| `config.ipv4.nat_enabled` | `boolean` | 是 | 示例: false | Whether outbound traffic from the VPN network is masqueraded. |
| `config.ipv4.nat_interface` | `string` | 是 | 示例: "auto" | Outbound interface for NAT, or auto for automatic detection. |
| `config.ipv4.redirect_gateway` | `boolean` | 是 | 示例: false | Whether generated profiles redirect the default IPv4 route through VPN. |
| `config.ipv4.dns` | `string[]` | 是 | 示例: [] | IPv4 DNS servers pushed to clients. |
| `config.ipv4.routes` | `string[]` | 是 | 示例: [] | Additional IPv4 CIDR routes pushed to clients. |
| `config.logging` | `object` | 是 | - | Runtime log rotation settings. |
| `config.logging.max_bytes` | `integer` | 是 | 最小值: 1；示例: 10485760 | Maximum active log file size before rotation. |
| `config.logging.backups` | `integer` | 是 | 最小值: 0；示例: 5 | Number of rotated log files retained. |

##### `401 Unauthorized`

Bearer API key is missing, malformed, unknown, or deleted.

内容类型：`application/json`

返回示例：

```json
{
  "error": {
    "kind": "unauthenticated",
    "message": "API key is missing or invalid",
    "request_id": "33ba813e-fc63-4af8-b338-7f7486f82202"
  }
}
```

返回字段：

| 字段 | 类型 | 必填 | 格式 / 示例 / 约束 | 用途说明 |
|---|---|---|---|---|
| `error` | `object` | 是 | - | - |
| `error.kind` | `string` | 是 | 示例: "unauthenticated" | - |
| `error.message` | `string` | 是 | 示例: "API key is missing or invalid" | - |
| `error.request_id` | `string` | 是 | 格式: uuid；示例: "33ba813e-fc63-4af8-b338-7f7486f82202" | - |

##### `405 Method Not Allowed`

HTTP method is not accepted; inspect the Allow header.

内容类型：`application/json`

返回示例：

```json
{
  "error": {
    "kind": "method_not_allowed",
    "message": "method is not allowed",
    "request_id": "33ba813e-fc63-4af8-b338-7f7486f82202"
  }
}
```

返回字段：

| 字段 | 类型 | 必填 | 格式 / 示例 / 约束 | 用途说明 |
|---|---|---|---|---|
| `error` | `object` | 是 | - | - |
| `error.kind` | `string` | 是 | 示例: "method_not_allowed" | - |
| `error.message` | `string` | 是 | 示例: "method is not allowed" | - |
| `error.request_id` | `string` | 是 | 格式: uuid；示例: "33ba813e-fc63-4af8-b338-7f7486f82202" | - |

##### `409 Conflict`

Digest, revision, state, uniqueness, lock, or recovery conflict.

内容类型：`application/json`

返回示例：

```json
{
  "error": {
    "kind": "configuration_conflict",
    "message": "configuration state changed or is busy",
    "request_id": "33ba813e-fc63-4af8-b338-7f7486f82202"
  }
}
```

返回字段：

| 字段 | 类型 | 必填 | 格式 / 示例 / 约束 | 用途说明 |
|---|---|---|---|---|
| `error` | `object` | 是 | - | - |
| `error.kind` | `string` | 是 | 示例: "configuration_conflict" | - |
| `error.message` | `string` | 是 | 示例: "configuration state changed or is busy" | - |
| `error.request_id` | `string` | 是 | 格式: uuid；示例: "33ba813e-fc63-4af8-b338-7f7486f82202" | - |

##### `500 Internal Server Error`

Unclassified internal failure with no implementation details exposed.

内容类型：`application/json`

返回示例：

```json
{
  "error": {
    "kind": "internal_error",
    "message": "request could not be completed",
    "request_id": "33ba813e-fc63-4af8-b338-7f7486f82202"
  }
}
```

返回字段：

| 字段 | 类型 | 必填 | 格式 / 示例 / 约束 | 用途说明 |
|---|---|---|---|---|
| `error` | `object` | 是 | - | - |
| `error.kind` | `string` | 是 | 示例: "internal_error" | - |
| `error.message` | `string` | 是 | 示例: "request could not be completed" | - |
| `error.request_id` | `string` | 是 | 格式: uuid；示例: "33ba813e-fc63-4af8-b338-7f7486f82202" | - |

##### `503 Service Unavailable`

Authentication storage, PKI, OpenVPN, broker, supervisor, or another required dependency is unavailable.

内容类型：`application/json`

返回示例：

```json
{
  "error": {
    "kind": "runtime_unavailable",
    "message": "OpenVPN runtime is unavailable",
    "request_id": "33ba813e-fc63-4af8-b338-7f7486f82202"
  }
}
```

返回字段：

| 字段 | 类型 | 必填 | 格式 / 示例 / 约束 | 用途说明 |
|---|---|---|---|---|
| `error` | `object` | 是 | - | - |
| `error.kind` | `string` | 是 | 示例: "runtime_unavailable" | - |
| `error.message` | `string` | 是 | 示例: "OpenVPN runtime is unavailable" | - |
| `error.request_id` | `string` | 是 | 格式: uuid；示例: "33ba813e-fc63-4af8-b338-7f7486f82202" | - |

### 19. `GET /api/v1/config/desired`

Read desired configuration and CAS digest

#### 请求参数

##### Header 参数

| 字段 | 位置 | 类型 | 必填 | 格式 / 示例 / 约束 | 用途说明 |
|---|---|---|---|---|---|
| `Authorization` | `header` | `string` | 是 | Bearer ovpn_v1.<uuid>.<secret> | API 身份认证凭据。Bearer 后填写服务生成的 API Key。 |

#### 请求示例

```http
GET /api/v1/config/desired HTTP/1.1
Authorization: Bearer ovpn_v1.<uuid>.<secret>
```

#### 返回

##### `200 OK`

Desired digest and normalized configuration.

内容类型：`application/json`

返回示例：

```json
{
  "digest": "be7ed82796b39a77ce949201a4d9483cf09e56648f14b8b9b192bb83d2c4d042",
  "config": {
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
    "logging": {
      "max_bytes": 10485760,
      "backups": 5
    }
  }
}
```

返回字段：

| 字段 | 类型 | 必填 | 格式 / 示例 / 约束 | 用途说明 |
|---|---|---|---|---|
| `digest` | `string` | 是 | 格式: `^[0-9a-f]{64}$`；示例: "be7ed82796b39a77ce949201a4d9483cf09e56648f14b8b9b192bb83d2c4d042" | Lowercase SHA-256 digest identifying an exact desired configuration. |
| `config` | `object` | 是 | - | - |
| `config.version` | `integer` | 是 | 固定值: 1 | Configuration schema version. Must be 1. |
| `config.server` | `object` | 是 | - | OpenVPN listener and transport settings. |
| `config.server.endpoint` | `string` | 是 | 示例: "vpn.example.com" | Public hostname or IP address placed in generated client profiles. |
| `config.server.protocol` | `string` | 是 | 可选值: "udp", "tcp" | OpenVPN transport protocol. |
| `config.server.family` | `string` | 是 | 可选值: "auto", "ipv4", "ipv6" | Address family used for the server transport. |
| `config.server.port` | `integer` | 是 | 最小值: 1；最大值: 65535；示例: 1194 | OpenVPN listener port. |
| `config.server.client_to_client` | `boolean` | 是 | 示例: true | Whether connected VPN clients may communicate directly. |
| `config.ipv4` | `object` | 是 | - | Server IPv4 network, allocation, NAT, DNS, and route settings. |
| `config.ipv4.network` | `string` | 是 | 格式: ipv4-cidr；示例: "10.42.0.0/24" | VPN IPv4 network in CIDR notation. |
| `config.ipv4.dynamic_pool_size` | `integer` | 是 | 最小值: 0；示例: 64 | Number of addresses reserved for dynamic allocation. |
| `config.ipv4.nat_enabled` | `boolean` | 是 | 示例: false | Whether outbound traffic from the VPN network is masqueraded. |
| `config.ipv4.nat_interface` | `string` | 是 | 示例: "auto" | Outbound interface for NAT, or auto for automatic detection. |
| `config.ipv4.redirect_gateway` | `boolean` | 是 | 示例: false | Whether generated profiles redirect the default IPv4 route through VPN. |
| `config.ipv4.dns` | `string[]` | 是 | 示例: [] | IPv4 DNS servers pushed to clients. |
| `config.ipv4.routes` | `string[]` | 是 | 示例: [] | Additional IPv4 CIDR routes pushed to clients. |
| `config.logging` | `object` | 是 | - | Runtime log rotation settings. |
| `config.logging.max_bytes` | `integer` | 是 | 最小值: 1；示例: 10485760 | Maximum active log file size before rotation. |
| `config.logging.backups` | `integer` | 是 | 最小值: 0；示例: 5 | Number of rotated log files retained. |

##### `401 Unauthorized`

Bearer API key is missing, malformed, unknown, or deleted.

内容类型：`application/json`

返回示例：

```json
{
  "error": {
    "kind": "unauthenticated",
    "message": "API key is missing or invalid",
    "request_id": "33ba813e-fc63-4af8-b338-7f7486f82202"
  }
}
```

返回字段：

| 字段 | 类型 | 必填 | 格式 / 示例 / 约束 | 用途说明 |
|---|---|---|---|---|
| `error` | `object` | 是 | - | - |
| `error.kind` | `string` | 是 | 示例: "unauthenticated" | - |
| `error.message` | `string` | 是 | 示例: "API key is missing or invalid" | - |
| `error.request_id` | `string` | 是 | 格式: uuid；示例: "33ba813e-fc63-4af8-b338-7f7486f82202" | - |

##### `405 Method Not Allowed`

HTTP method is not accepted; inspect the Allow header.

内容类型：`application/json`

返回示例：

```json
{
  "error": {
    "kind": "method_not_allowed",
    "message": "method is not allowed",
    "request_id": "33ba813e-fc63-4af8-b338-7f7486f82202"
  }
}
```

返回字段：

| 字段 | 类型 | 必填 | 格式 / 示例 / 约束 | 用途说明 |
|---|---|---|---|---|
| `error` | `object` | 是 | - | - |
| `error.kind` | `string` | 是 | 示例: "method_not_allowed" | - |
| `error.message` | `string` | 是 | 示例: "method is not allowed" | - |
| `error.request_id` | `string` | 是 | 格式: uuid；示例: "33ba813e-fc63-4af8-b338-7f7486f82202" | - |

##### `409 Conflict`

Digest, revision, state, uniqueness, lock, or recovery conflict.

内容类型：`application/json`

返回示例：

```json
{
  "error": {
    "kind": "configuration_conflict",
    "message": "configuration state changed or is busy",
    "request_id": "33ba813e-fc63-4af8-b338-7f7486f82202"
  }
}
```

返回字段：

| 字段 | 类型 | 必填 | 格式 / 示例 / 约束 | 用途说明 |
|---|---|---|---|---|
| `error` | `object` | 是 | - | - |
| `error.kind` | `string` | 是 | 示例: "configuration_conflict" | - |
| `error.message` | `string` | 是 | 示例: "configuration state changed or is busy" | - |
| `error.request_id` | `string` | 是 | 格式: uuid；示例: "33ba813e-fc63-4af8-b338-7f7486f82202" | - |

##### `422 Unprocessable Entity`

JSON is valid but violates client, IPv4, or configuration semantics.

内容类型：`application/json`

返回示例：

```json
{
  "error": {
    "kind": "invalid_client",
    "message": "client request is not valid for the current state",
    "request_id": "33ba813e-fc63-4af8-b338-7f7486f82202"
  }
}
```

返回字段：

| 字段 | 类型 | 必填 | 格式 / 示例 / 约束 | 用途说明 |
|---|---|---|---|---|
| `error` | `object` | 是 | - | - |
| `error.kind` | `string` | 是 | 示例: "invalid_client" | - |
| `error.message` | `string` | 是 | 示例: "client request is not valid for the current state" | - |
| `error.request_id` | `string` | 是 | 格式: uuid；示例: "33ba813e-fc63-4af8-b338-7f7486f82202" | - |

##### `500 Internal Server Error`

Unclassified internal failure with no implementation details exposed.

内容类型：`application/json`

返回示例：

```json
{
  "error": {
    "kind": "internal_error",
    "message": "request could not be completed",
    "request_id": "33ba813e-fc63-4af8-b338-7f7486f82202"
  }
}
```

返回字段：

| 字段 | 类型 | 必填 | 格式 / 示例 / 约束 | 用途说明 |
|---|---|---|---|---|
| `error` | `object` | 是 | - | - |
| `error.kind` | `string` | 是 | 示例: "internal_error" | - |
| `error.message` | `string` | 是 | 示例: "request could not be completed" | - |
| `error.request_id` | `string` | 是 | 格式: uuid；示例: "33ba813e-fc63-4af8-b338-7f7486f82202" | - |

##### `503 Service Unavailable`

Authentication storage, PKI, OpenVPN, broker, supervisor, or another required dependency is unavailable.

内容类型：`application/json`

返回示例：

```json
{
  "error": {
    "kind": "runtime_unavailable",
    "message": "OpenVPN runtime is unavailable",
    "request_id": "33ba813e-fc63-4af8-b338-7f7486f82202"
  }
}
```

返回字段：

| 字段 | 类型 | 必填 | 格式 / 示例 / 约束 | 用途说明 |
|---|---|---|---|---|
| `error` | `object` | 是 | - | - |
| `error.kind` | `string` | 是 | 示例: "runtime_unavailable" | - |
| `error.message` | `string` | 是 | 示例: "OpenVPN runtime is unavailable" | - |
| `error.request_id` | `string` | 是 | 格式: uuid；示例: "33ba813e-fc63-4af8-b338-7f7486f82202" | - |

### 20. `PUT /api/v1/config/desired`

Replace desired configuration with CAS. Send the previous desired digest as a quoted If-Match value and the complete normalized configuration as the body.

#### 请求参数

##### Header 参数

| 字段 | 位置 | 类型 | 必填 | 格式 / 示例 / 约束 | 用途说明 |
|---|---|---|---|---|---|
| `Authorization` | `header` | `string` | 是 | Bearer ovpn_v1.<uuid>.<secret> | API 身份认证凭据。Bearer 后填写服务生成的 API Key。 |
| `Content-Type` | `header` | `string` | 是 | application/json | 声明请求体使用 JSON 格式。 |
| `If-Match` | `header` | `string` | 是 | 格式: `^"[0-9a-f]{64}"$`；示例: "\"be7ed82796b39a77ce949201a4d9483cf09e56648f14b8b9b192bb83d2c4d042\"" | Exactly one quoted lowercase SHA-256 digest from the previous desired response ETag. |

##### JSON Body 字段

| 字段 | 位置 | 类型 | 必填 | 格式 / 示例 / 约束 | 用途说明 |
|---|---|---|---|---|---|
| `version` | `body` | `integer` | 是 | 固定值: 1 | Configuration schema version. Must be 1. |
| `server` | `body` | `object` | 是 | - | OpenVPN listener and transport settings. |
| `server.endpoint` | `body` | `string` | 是 | 示例: "vpn.example.com" | Public hostname or IP address placed in generated client profiles. |
| `server.protocol` | `body` | `string` | 是 | 可选值: "udp", "tcp" | OpenVPN transport protocol. |
| `server.family` | `body` | `string` | 是 | 可选值: "auto", "ipv4", "ipv6" | Address family used for the server transport. |
| `server.port` | `body` | `integer` | 是 | 最小值: 1；最大值: 65535；示例: 1194 | OpenVPN listener port. |
| `server.client_to_client` | `body` | `boolean` | 是 | 示例: true | Whether connected VPN clients may communicate directly. |
| `ipv4` | `body` | `object` | 是 | - | Server IPv4 network, allocation, NAT, DNS, and route settings. |
| `ipv4.network` | `body` | `string` | 是 | 格式: ipv4-cidr；示例: "10.42.0.0/24" | VPN IPv4 network in CIDR notation. |
| `ipv4.dynamic_pool_size` | `body` | `integer` | 是 | 最小值: 0；示例: 64 | Number of addresses reserved for dynamic allocation. |
| `ipv4.nat_enabled` | `body` | `boolean` | 是 | 示例: false | Whether outbound traffic from the VPN network is masqueraded. |
| `ipv4.nat_interface` | `body` | `string` | 是 | 示例: "auto" | Outbound interface for NAT, or auto for automatic detection. |
| `ipv4.redirect_gateway` | `body` | `boolean` | 是 | 示例: false | Whether generated profiles redirect the default IPv4 route through VPN. |
| `ipv4.dns` | `body` | `string[]` | 是 | 示例: ["1.1.1.1"] | IPv4 DNS servers pushed to clients. |
| `ipv4.routes` | `body` | `string[]` | 是 | 示例: ["10.20.0.0/16"] | Additional IPv4 CIDR routes pushed to clients. |
| `logging` | `body` | `object` | 是 | - | Runtime log rotation settings. |
| `logging.max_bytes` | `body` | `integer` | 是 | 最小值: 1；示例: 10485760 | Maximum active log file size before rotation. |
| `logging.backups` | `body` | `integer` | 是 | 最小值: 0；示例: 5 | Number of rotated log files retained. |

#### 请求示例

```http
PUT /api/v1/config/desired HTTP/1.1
Authorization: Bearer ovpn_v1.<uuid>.<secret>
If-Match: "be7ed82796b39a77ce949201a4d9483cf09e56648f14b8b9b192bb83d2c4d042"
Content-Type: application/json

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
    "dns": [
      "1.1.1.1"
    ],
    "routes": [
      "10.20.0.0/16"
    ]
  },
  "logging": {
    "max_bytes": 10485760,
    "backups": 5
  }
}
```

#### 返回

##### `200 OK`

Desired digest and normalized configuration.

内容类型：`application/json`

返回示例：

```json
{
  "digest": "be7ed82796b39a77ce949201a4d9483cf09e56648f14b8b9b192bb83d2c4d042",
  "config": {
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
    "logging": {
      "max_bytes": 10485760,
      "backups": 5
    }
  }
}
```

返回字段：

| 字段 | 类型 | 必填 | 格式 / 示例 / 约束 | 用途说明 |
|---|---|---|---|---|
| `digest` | `string` | 是 | 格式: `^[0-9a-f]{64}$`；示例: "be7ed82796b39a77ce949201a4d9483cf09e56648f14b8b9b192bb83d2c4d042" | Lowercase SHA-256 digest identifying an exact desired configuration. |
| `config` | `object` | 是 | - | - |
| `config.version` | `integer` | 是 | 固定值: 1 | Configuration schema version. Must be 1. |
| `config.server` | `object` | 是 | - | OpenVPN listener and transport settings. |
| `config.server.endpoint` | `string` | 是 | 示例: "vpn.example.com" | Public hostname or IP address placed in generated client profiles. |
| `config.server.protocol` | `string` | 是 | 可选值: "udp", "tcp" | OpenVPN transport protocol. |
| `config.server.family` | `string` | 是 | 可选值: "auto", "ipv4", "ipv6" | Address family used for the server transport. |
| `config.server.port` | `integer` | 是 | 最小值: 1；最大值: 65535；示例: 1194 | OpenVPN listener port. |
| `config.server.client_to_client` | `boolean` | 是 | 示例: true | Whether connected VPN clients may communicate directly. |
| `config.ipv4` | `object` | 是 | - | Server IPv4 network, allocation, NAT, DNS, and route settings. |
| `config.ipv4.network` | `string` | 是 | 格式: ipv4-cidr；示例: "10.42.0.0/24" | VPN IPv4 network in CIDR notation. |
| `config.ipv4.dynamic_pool_size` | `integer` | 是 | 最小值: 0；示例: 64 | Number of addresses reserved for dynamic allocation. |
| `config.ipv4.nat_enabled` | `boolean` | 是 | 示例: false | Whether outbound traffic from the VPN network is masqueraded. |
| `config.ipv4.nat_interface` | `string` | 是 | 示例: "auto" | Outbound interface for NAT, or auto for automatic detection. |
| `config.ipv4.redirect_gateway` | `boolean` | 是 | 示例: false | Whether generated profiles redirect the default IPv4 route through VPN. |
| `config.ipv4.dns` | `string[]` | 是 | 示例: [] | IPv4 DNS servers pushed to clients. |
| `config.ipv4.routes` | `string[]` | 是 | 示例: [] | Additional IPv4 CIDR routes pushed to clients. |
| `config.logging` | `object` | 是 | - | Runtime log rotation settings. |
| `config.logging.max_bytes` | `integer` | 是 | 最小值: 1；示例: 10485760 | Maximum active log file size before rotation. |
| `config.logging.backups` | `integer` | 是 | 最小值: 0；示例: 5 | Number of rotated log files retained. |

##### `400 Bad Request`

Malformed path, query, header, media type, body, or JSON.

内容类型：`application/json`

返回示例：

```json
{
  "error": {
    "kind": "invalid_json",
    "message": "request body is invalid",
    "request_id": "33ba813e-fc63-4af8-b338-7f7486f82202"
  }
}
```

返回字段：

| 字段 | 类型 | 必填 | 格式 / 示例 / 约束 | 用途说明 |
|---|---|---|---|---|
| `error` | `object` | 是 | - | - |
| `error.kind` | `string` | 是 | 示例: "invalid_json" | - |
| `error.message` | `string` | 是 | 示例: "request body is invalid" | - |
| `error.request_id` | `string` | 是 | 格式: uuid；示例: "33ba813e-fc63-4af8-b338-7f7486f82202" | - |

##### `401 Unauthorized`

Bearer API key is missing, malformed, unknown, or deleted.

内容类型：`application/json`

返回示例：

```json
{
  "error": {
    "kind": "unauthenticated",
    "message": "API key is missing or invalid",
    "request_id": "33ba813e-fc63-4af8-b338-7f7486f82202"
  }
}
```

返回字段：

| 字段 | 类型 | 必填 | 格式 / 示例 / 约束 | 用途说明 |
|---|---|---|---|---|
| `error` | `object` | 是 | - | - |
| `error.kind` | `string` | 是 | 示例: "unauthenticated" | - |
| `error.message` | `string` | 是 | 示例: "API key is missing or invalid" | - |
| `error.request_id` | `string` | 是 | 格式: uuid；示例: "33ba813e-fc63-4af8-b338-7f7486f82202" | - |

##### `405 Method Not Allowed`

HTTP method is not accepted; inspect the Allow header.

内容类型：`application/json`

返回示例：

```json
{
  "error": {
    "kind": "method_not_allowed",
    "message": "method is not allowed",
    "request_id": "33ba813e-fc63-4af8-b338-7f7486f82202"
  }
}
```

返回字段：

| 字段 | 类型 | 必填 | 格式 / 示例 / 约束 | 用途说明 |
|---|---|---|---|---|
| `error` | `object` | 是 | - | - |
| `error.kind` | `string` | 是 | 示例: "method_not_allowed" | - |
| `error.message` | `string` | 是 | 示例: "method is not allowed" | - |
| `error.request_id` | `string` | 是 | 格式: uuid；示例: "33ba813e-fc63-4af8-b338-7f7486f82202" | - |

##### `409 Conflict`

Digest, revision, state, uniqueness, lock, or recovery conflict.

内容类型：`application/json`

返回示例：

```json
{
  "error": {
    "kind": "configuration_conflict",
    "message": "configuration state changed or is busy",
    "request_id": "33ba813e-fc63-4af8-b338-7f7486f82202"
  }
}
```

返回字段：

| 字段 | 类型 | 必填 | 格式 / 示例 / 约束 | 用途说明 |
|---|---|---|---|---|
| `error` | `object` | 是 | - | - |
| `error.kind` | `string` | 是 | 示例: "configuration_conflict" | - |
| `error.message` | `string` | 是 | 示例: "configuration state changed or is busy" | - |
| `error.request_id` | `string` | 是 | 格式: uuid；示例: "33ba813e-fc63-4af8-b338-7f7486f82202" | - |

##### `422 Unprocessable Entity`

JSON is valid but violates client, IPv4, or configuration semantics.

内容类型：`application/json`

返回示例：

```json
{
  "error": {
    "kind": "invalid_client",
    "message": "client request is not valid for the current state",
    "request_id": "33ba813e-fc63-4af8-b338-7f7486f82202"
  }
}
```

返回字段：

| 字段 | 类型 | 必填 | 格式 / 示例 / 约束 | 用途说明 |
|---|---|---|---|---|
| `error` | `object` | 是 | - | - |
| `error.kind` | `string` | 是 | 示例: "invalid_client" | - |
| `error.message` | `string` | 是 | 示例: "client request is not valid for the current state" | - |
| `error.request_id` | `string` | 是 | 格式: uuid；示例: "33ba813e-fc63-4af8-b338-7f7486f82202" | - |

##### `500 Internal Server Error`

Unclassified internal failure with no implementation details exposed.

内容类型：`application/json`

返回示例：

```json
{
  "error": {
    "kind": "internal_error",
    "message": "request could not be completed",
    "request_id": "33ba813e-fc63-4af8-b338-7f7486f82202"
  }
}
```

返回字段：

| 字段 | 类型 | 必填 | 格式 / 示例 / 约束 | 用途说明 |
|---|---|---|---|---|
| `error` | `object` | 是 | - | - |
| `error.kind` | `string` | 是 | 示例: "internal_error" | - |
| `error.message` | `string` | 是 | 示例: "request could not be completed" | - |
| `error.request_id` | `string` | 是 | 格式: uuid；示例: "33ba813e-fc63-4af8-b338-7f7486f82202" | - |

##### `503 Service Unavailable`

Authentication storage, PKI, OpenVPN, broker, supervisor, or another required dependency is unavailable.

内容类型：`application/json`

返回示例：

```json
{
  "error": {
    "kind": "runtime_unavailable",
    "message": "OpenVPN runtime is unavailable",
    "request_id": "33ba813e-fc63-4af8-b338-7f7486f82202"
  }
}
```

返回字段：

| 字段 | 类型 | 必填 | 格式 / 示例 / 约束 | 用途说明 |
|---|---|---|---|---|
| `error` | `object` | 是 | - | - |
| `error.kind` | `string` | 是 | 示例: "runtime_unavailable" | - |
| `error.message` | `string` | 是 | 示例: "OpenVPN runtime is unavailable" | - |
| `error.request_id` | `string` | 是 | 格式: uuid；示例: "33ba813e-fc63-4af8-b338-7f7486f82202" | - |

### 21. `GET /api/v1/config/plan`

Plan desired-to-applied changes

#### 请求参数

##### Header 参数

| 字段 | 位置 | 类型 | 必填 | 格式 / 示例 / 约束 | 用途说明 |
|---|---|---|---|---|---|
| `Authorization` | `header` | `string` | 是 | Bearer ovpn_v1.<uuid>.<secret> | API 身份认证凭据。Bearer 后填写服务生成的 API Key。 |

#### 请求示例

```http
GET /api/v1/config/plan HTTP/1.1
Authorization: Bearer ovpn_v1.<uuid>.<secret>
```

#### 返回

##### `200 OK`

Complete desired-to-applied plan.

内容类型：`application/json`

返回示例：

```json
{
  "version": 1,
  "instance_id": "bbffeb8c-2d11-4613-874d-b5fcc804a608",
  "configuration": {
    "initial": false,
    "current_revision": 12,
    "target_revision": 13,
    "current_digest": "be7ed82796b39a77ce949201a4d9483cf09e56648f14b8b9b192bb83d2c4d042",
    "desired_digest": "489b950c0d134b5d7e8f350b2cb4bfa4d6d163d841f840964baf6d74c65cc8ca",
    "in_sync": false,
    "changes": [
      {
        "field": "server.endpoint",
        "before": "vpn.example.com",
        "after": "vpn-new.example.com"
      }
    ],
    "impact": {
      "restart_required": true,
      "address_remap": false,
      "firewall_reconcile": false,
      "profile_redistribution": true,
      "derived_artifacts": [
        "server_config",
        "client_profiles"
      ]
    }
  },
  "address_changes": [],
  "artifacts": [],
  "profile_redistribution": [],
  "firewall": {
    "reconcile": false,
    "before": null,
    "after": null
  }
}
```

返回字段：

| 字段 | 类型 | 必填 | 格式 / 示例 / 约束 | 用途说明 |
|---|---|---|---|---|
| `version` | `integer` | 是 | 固定值: 1 | - |
| `instance_id` | `string` | 是 | 格式: uuid；示例: "bbffeb8c-2d11-4613-874d-b5fcc804a608" | - |
| `configuration` | `object` | 是 | - | - |
| `configuration.initial` | `boolean` | 是 | 示例: false | - |
| `configuration.current_revision` | `integer` | 是 | 最小值: 0；示例: 12 | - |
| `configuration.target_revision` | `integer` | 是 | 最小值: 1；示例: 13 | - |
| `configuration.current_digest` | `string` | 否 | 格式: `^[0-9a-f]{64}$`；示例: "be7ed82796b39a77ce949201a4d9483cf09e56648f14b8b9b192bb83d2c4d042" | Lowercase SHA-256 digest identifying an exact desired configuration. |
| `configuration.desired_digest` | `string` | 是 | 格式: `^[0-9a-f]{64}$`；示例: "489b950c0d134b5d7e8f350b2cb4bfa4d6d163d841f840964baf6d74c65cc8ca" | Lowercase SHA-256 digest identifying an exact desired configuration. |
| `configuration.in_sync` | `boolean` | 是 | 示例: false | - |
| `configuration.changes` | `object[]` | 是 | 示例: [{"field":"server.endpoint","before":"vpn.example.com","after":"vpn-new.example.com"}] | - |
| `configuration.changes[].field` | `string` | 是 | 示例: "server.endpoint" | - |
| `configuration.changes[].before` | `any` | 是 | 示例: "vpn.example.com" | - |
| `configuration.changes[].after` | `any` | 是 | 示例: "vpn-new.example.com" | - |
| `configuration.impact` | `object` | 是 | - | - |
| `configuration.impact.restart_required` | `boolean` | 是 | 示例: true | - |
| `configuration.impact.address_remap` | `boolean` | 是 | 示例: false | - |
| `configuration.impact.firewall_reconcile` | `boolean` | 是 | 示例: false | - |
| `configuration.impact.profile_redistribution` | `boolean` | 是 | 示例: true | - |
| `configuration.impact.derived_artifacts` | `string[]` | 是 | 示例: ["server_config","client_profiles"] | - |
| `address_changes` | `object[]` | 是 | 示例: [] | - |
| `address_changes[].client` | `object` | 是 | - | - |
| `address_changes[].client.id` | `string` | 是 | 格式: uuid；示例: "c0d4f871-6ea6-42b3-9e7b-f00cc1ec354e" | - |
| `address_changes[].client.name` | `string` | 是 | 示例: "string" | - |
| `address_changes[].before` | `object` | 是 | - | - |
| `address_changes[].before.mode` | `string` | 是 | 示例: "string" | - |
| `address_changes[].before.address` | `string \| null` | 是 | 格式: ipv4；示例: "10.42.0.30" | - |
| `address_changes[].before.state` | `string` | 是 | 示例: "string" | - |
| `address_changes[].after` | `object` | 是 | - | - |
| `address_changes[].after.mode` | `string` | 是 | 示例: "string" | - |
| `address_changes[].after.address` | `string \| null` | 是 | 格式: ipv4；示例: "10.42.0.30" | - |
| `address_changes[].after.state` | `string` | 是 | 示例: "string" | - |
| `artifacts` | `object[]` | 是 | 示例: [] | - |
| `artifacts[].owner_kind` | `string` | 是 | 示例: "string" | - |
| `artifacts[].owner_id` | `string` | 是 | 示例: "string" | - |
| `artifacts[].kind` | `string` | 是 | 示例: "string" | - |
| `artifacts[].key` | `string` | 是 | 示例: "string" | - |
| `artifacts[].action` | `string` | 是 | 可选值: "regenerate", "delete" | - |
| `profile_redistribution` | `object[]` | 是 | 示例: [] | - |
| `profile_redistribution[].id` | `string` | 是 | 格式: uuid；示例: "c0d4f871-6ea6-42b3-9e7b-f00cc1ec354e" | - |
| `profile_redistribution[].name` | `string` | 是 | 示例: "string" | - |
| `firewall` | `object` | 是 | - | - |
| `firewall.reconcile` | `boolean` | 是 | 示例: false | - |
| `firewall.before` | `object \| null` | 是 | 示例: {"network":"string","nat_enabled":false,"nat_interface":"string","routes":["string"]} | - |
| `firewall.after` | `object \| null` | 是 | 示例: {"network":"string","nat_enabled":false,"nat_interface":"string","routes":["string"]} | - |

##### `401 Unauthorized`

Bearer API key is missing, malformed, unknown, or deleted.

内容类型：`application/json`

返回示例：

```json
{
  "error": {
    "kind": "unauthenticated",
    "message": "API key is missing or invalid",
    "request_id": "33ba813e-fc63-4af8-b338-7f7486f82202"
  }
}
```

返回字段：

| 字段 | 类型 | 必填 | 格式 / 示例 / 约束 | 用途说明 |
|---|---|---|---|---|
| `error` | `object` | 是 | - | - |
| `error.kind` | `string` | 是 | 示例: "unauthenticated" | - |
| `error.message` | `string` | 是 | 示例: "API key is missing or invalid" | - |
| `error.request_id` | `string` | 是 | 格式: uuid；示例: "33ba813e-fc63-4af8-b338-7f7486f82202" | - |

##### `405 Method Not Allowed`

HTTP method is not accepted; inspect the Allow header.

内容类型：`application/json`

返回示例：

```json
{
  "error": {
    "kind": "method_not_allowed",
    "message": "method is not allowed",
    "request_id": "33ba813e-fc63-4af8-b338-7f7486f82202"
  }
}
```

返回字段：

| 字段 | 类型 | 必填 | 格式 / 示例 / 约束 | 用途说明 |
|---|---|---|---|---|
| `error` | `object` | 是 | - | - |
| `error.kind` | `string` | 是 | 示例: "method_not_allowed" | - |
| `error.message` | `string` | 是 | 示例: "method is not allowed" | - |
| `error.request_id` | `string` | 是 | 格式: uuid；示例: "33ba813e-fc63-4af8-b338-7f7486f82202" | - |

##### `409 Conflict`

Digest, revision, state, uniqueness, lock, or recovery conflict.

内容类型：`application/json`

返回示例：

```json
{
  "error": {
    "kind": "configuration_conflict",
    "message": "configuration state changed or is busy",
    "request_id": "33ba813e-fc63-4af8-b338-7f7486f82202"
  }
}
```

返回字段：

| 字段 | 类型 | 必填 | 格式 / 示例 / 约束 | 用途说明 |
|---|---|---|---|---|
| `error` | `object` | 是 | - | - |
| `error.kind` | `string` | 是 | 示例: "configuration_conflict" | - |
| `error.message` | `string` | 是 | 示例: "configuration state changed or is busy" | - |
| `error.request_id` | `string` | 是 | 格式: uuid；示例: "33ba813e-fc63-4af8-b338-7f7486f82202" | - |

##### `422 Unprocessable Entity`

JSON is valid but violates client, IPv4, or configuration semantics.

内容类型：`application/json`

返回示例：

```json
{
  "error": {
    "kind": "invalid_client",
    "message": "client request is not valid for the current state",
    "request_id": "33ba813e-fc63-4af8-b338-7f7486f82202"
  }
}
```

返回字段：

| 字段 | 类型 | 必填 | 格式 / 示例 / 约束 | 用途说明 |
|---|---|---|---|---|
| `error` | `object` | 是 | - | - |
| `error.kind` | `string` | 是 | 示例: "invalid_client" | - |
| `error.message` | `string` | 是 | 示例: "client request is not valid for the current state" | - |
| `error.request_id` | `string` | 是 | 格式: uuid；示例: "33ba813e-fc63-4af8-b338-7f7486f82202" | - |

##### `500 Internal Server Error`

Unclassified internal failure with no implementation details exposed.

内容类型：`application/json`

返回示例：

```json
{
  "error": {
    "kind": "internal_error",
    "message": "request could not be completed",
    "request_id": "33ba813e-fc63-4af8-b338-7f7486f82202"
  }
}
```

返回字段：

| 字段 | 类型 | 必填 | 格式 / 示例 / 约束 | 用途说明 |
|---|---|---|---|---|
| `error` | `object` | 是 | - | - |
| `error.kind` | `string` | 是 | 示例: "internal_error" | - |
| `error.message` | `string` | 是 | 示例: "request could not be completed" | - |
| `error.request_id` | `string` | 是 | 格式: uuid；示例: "33ba813e-fc63-4af8-b338-7f7486f82202" | - |

##### `503 Service Unavailable`

Authentication storage, PKI, OpenVPN, broker, supervisor, or another required dependency is unavailable.

内容类型：`application/json`

返回示例：

```json
{
  "error": {
    "kind": "runtime_unavailable",
    "message": "OpenVPN runtime is unavailable",
    "request_id": "33ba813e-fc63-4af8-b338-7f7486f82202"
  }
}
```

返回字段：

| 字段 | 类型 | 必填 | 格式 / 示例 / 约束 | 用途说明 |
|---|---|---|---|---|
| `error` | `object` | 是 | - | - |
| `error.kind` | `string` | 是 | 示例: "runtime_unavailable" | - |
| `error.message` | `string` | 是 | 示例: "OpenVPN runtime is unavailable" | - |
| `error.request_id` | `string` | 是 | 格式: uuid；示例: "33ba813e-fc63-4af8-b338-7f7486f82202" | - |

### 22. `POST /api/v1/config/apply`

Apply the current desired configuration online. Use desired_digest from GET/PUT desired and current_revision from GET plan. The API remains available while OpenVPN and the broker restart.

#### 请求参数

##### Header 参数

| 字段 | 位置 | 类型 | 必填 | 格式 / 示例 / 约束 | 用途说明 |
|---|---|---|---|---|---|
| `Authorization` | `header` | `string` | 是 | Bearer ovpn_v1.<uuid>.<secret> | API 身份认证凭据。Bearer 后填写服务生成的 API Key。 |
| `Content-Type` | `header` | `string` | 是 | application/json | 声明请求体使用 JSON 格式。 |

##### JSON Body 字段

| 字段 | 位置 | 类型 | 必填 | 格式 / 示例 / 约束 | 用途说明 |
|---|---|---|---|---|---|
| `desired_digest` | `body` | `string` | 是 | 格式: `^[0-9a-f]{64}$`；示例: "489b950c0d134b5d7e8f350b2cb4bfa4d6d163d841f840964baf6d74c65cc8ca" | Lowercase SHA-256 digest identifying an exact desired configuration. |
| `current_revision` | `body` | `integer` | 是 | 最小值: 1；示例: 12 | Applied revision returned by the configuration plan. |
| `force` | `body` | `boolean` | 否 | 默认值: false | Bypasses only confirmed health-preflight warnings; never bypasses schema, CAS, lock, or recovery checks. |

#### 请求示例

```http
POST /api/v1/config/apply HTTP/1.1
Authorization: Bearer ovpn_v1.<uuid>.<secret>
Content-Type: application/json

{
  "desired_digest": "489b950c0d134b5d7e8f350b2cb4bfa4d6d163d841f840964baf6d74c65cc8ca",
  "current_revision": 12,
  "force": false
}
```

#### 返回

##### `200 OK`

Apply transaction and runtime activation outcome.

内容类型：`application/json`

返回示例：

```json
{
  "version": 1,
  "applied": true,
  "operation_id": "5b781e08-3d44-41fb-81db-42edeedc61e1",
  "activation": {
    "restart_required": true,
    "runtime_restarted": true,
    "profile_redistribution": []
  },
  "plan": {
    "version": 1,
    "instance_id": "bbffeb8c-2d11-4613-874d-b5fcc804a608",
    "configuration": {
      "initial": false,
      "current_revision": 12,
      "target_revision": 13,
      "current_digest": "be7ed82796b39a77ce949201a4d9483cf09e56648f14b8b9b192bb83d2c4d042",
      "desired_digest": "489b950c0d134b5d7e8f350b2cb4bfa4d6d163d841f840964baf6d74c65cc8ca",
      "in_sync": false,
      "changes": [],
      "impact": {
        "restart_required": true,
        "address_remap": false,
        "firewall_reconcile": false,
        "profile_redistribution": false,
        "derived_artifacts": []
      }
    },
    "address_changes": [],
    "artifacts": [],
    "profile_redistribution": [],
    "firewall": {
      "reconcile": false,
      "before": null,
      "after": null
    }
  }
}
```

返回字段：

| 字段 | 类型 | 必填 | 格式 / 示例 / 约束 | 用途说明 |
|---|---|---|---|---|
| `version` | `integer` | 是 | 固定值: 1 | - |
| `applied` | `boolean` | 是 | 示例: true | - |
| `operation_id` | `string` | 否 | 格式: uuid；示例: "5b781e08-3d44-41fb-81db-42edeedc61e1" | - |
| `activation` | `object` | 是 | - | - |
| `activation.restart_required` | `boolean` | 是 | 示例: true | - |
| `activation.runtime_restarted` | `boolean` | 是 | 示例: true | - |
| `activation.profile_redistribution` | `object[]` | 是 | 示例: [] | - |
| `activation.profile_redistribution[].id` | `string` | 是 | 格式: uuid；示例: "c0d4f871-6ea6-42b3-9e7b-f00cc1ec354e" | - |
| `activation.profile_redistribution[].name` | `string` | 是 | 示例: "string" | - |
| `plan` | `object` | 是 | - | - |
| `plan.version` | `integer` | 是 | 固定值: 1 | - |
| `plan.instance_id` | `string` | 是 | 格式: uuid；示例: "bbffeb8c-2d11-4613-874d-b5fcc804a608" | - |
| `plan.configuration` | `object` | 是 | - | - |
| `plan.configuration.initial` | `boolean` | 是 | 示例: false | - |
| `plan.configuration.current_revision` | `integer` | 是 | 最小值: 0；示例: 12 | - |
| `plan.configuration.target_revision` | `integer` | 是 | 最小值: 1；示例: 13 | - |
| `plan.configuration.current_digest` | `string` | 否 | 格式: `^[0-9a-f]{64}$`；示例: "be7ed82796b39a77ce949201a4d9483cf09e56648f14b8b9b192bb83d2c4d042" | Lowercase SHA-256 digest identifying an exact desired configuration. |
| `plan.configuration.desired_digest` | `string` | 是 | 格式: `^[0-9a-f]{64}$`；示例: "489b950c0d134b5d7e8f350b2cb4bfa4d6d163d841f840964baf6d74c65cc8ca" | Lowercase SHA-256 digest identifying an exact desired configuration. |
| `plan.configuration.in_sync` | `boolean` | 是 | 示例: false | - |
| `plan.configuration.changes` | `object[]` | 是 | 示例: [] | - |
| `plan.configuration.changes[].field` | `string` | 是 | 示例: "server.endpoint" | - |
| `plan.configuration.changes[].before` | `any` | 是 | 示例: "string" | - |
| `plan.configuration.changes[].after` | `any` | 是 | 示例: "string" | - |
| `plan.configuration.impact` | `object` | 是 | - | - |
| `plan.configuration.impact.restart_required` | `boolean` | 是 | 示例: true | - |
| `plan.configuration.impact.address_remap` | `boolean` | 是 | 示例: false | - |
| `plan.configuration.impact.firewall_reconcile` | `boolean` | 是 | 示例: false | - |
| `plan.configuration.impact.profile_redistribution` | `boolean` | 是 | 示例: false | - |
| `plan.configuration.impact.derived_artifacts` | `string[]` | 是 | 示例: [] | - |
| `plan.address_changes` | `object[]` | 是 | 示例: [] | - |
| `plan.address_changes[].client` | `object` | 是 | - | - |
| `plan.address_changes[].client.id` | `string` | 是 | 格式: uuid；示例: "c0d4f871-6ea6-42b3-9e7b-f00cc1ec354e" | - |
| `plan.address_changes[].client.name` | `string` | 是 | 示例: "string" | - |
| `plan.address_changes[].before` | `object` | 是 | - | - |
| `plan.address_changes[].before.mode` | `string` | 是 | 示例: "string" | - |
| `plan.address_changes[].before.address` | `string \| null` | 是 | 格式: ipv4；示例: "10.42.0.30" | - |
| `plan.address_changes[].before.state` | `string` | 是 | 示例: "string" | - |
| `plan.address_changes[].after` | `object` | 是 | - | - |
| `plan.address_changes[].after.mode` | `string` | 是 | 示例: "string" | - |
| `plan.address_changes[].after.address` | `string \| null` | 是 | 格式: ipv4；示例: "10.42.0.30" | - |
| `plan.address_changes[].after.state` | `string` | 是 | 示例: "string" | - |
| `plan.artifacts` | `object[]` | 是 | 示例: [] | - |
| `plan.artifacts[].owner_kind` | `string` | 是 | 示例: "string" | - |
| `plan.artifacts[].owner_id` | `string` | 是 | 示例: "string" | - |
| `plan.artifacts[].kind` | `string` | 是 | 示例: "string" | - |
| `plan.artifacts[].key` | `string` | 是 | 示例: "string" | - |
| `plan.artifacts[].action` | `string` | 是 | 可选值: "regenerate", "delete" | - |
| `plan.profile_redistribution` | `object[]` | 是 | 示例: [] | - |
| `plan.profile_redistribution[].id` | `string` | 是 | 格式: uuid；示例: "c0d4f871-6ea6-42b3-9e7b-f00cc1ec354e" | - |
| `plan.profile_redistribution[].name` | `string` | 是 | 示例: "string" | - |
| `plan.firewall` | `object` | 是 | - | - |
| `plan.firewall.reconcile` | `boolean` | 是 | 示例: false | - |
| `plan.firewall.before` | `object \| null` | 是 | 示例: {"network":"string","nat_enabled":false,"nat_interface":"string","routes":["string"]} | - |
| `plan.firewall.after` | `object \| null` | 是 | 示例: {"network":"string","nat_enabled":false,"nat_interface":"string","routes":["string"]} | - |

##### `400 Bad Request`

Malformed path, query, header, media type, body, or JSON.

内容类型：`application/json`

返回示例：

```json
{
  "error": {
    "kind": "invalid_json",
    "message": "request body is invalid",
    "request_id": "33ba813e-fc63-4af8-b338-7f7486f82202"
  }
}
```

返回字段：

| 字段 | 类型 | 必填 | 格式 / 示例 / 约束 | 用途说明 |
|---|---|---|---|---|
| `error` | `object` | 是 | - | - |
| `error.kind` | `string` | 是 | 示例: "invalid_json" | - |
| `error.message` | `string` | 是 | 示例: "request body is invalid" | - |
| `error.request_id` | `string` | 是 | 格式: uuid；示例: "33ba813e-fc63-4af8-b338-7f7486f82202" | - |

##### `401 Unauthorized`

Bearer API key is missing, malformed, unknown, or deleted.

内容类型：`application/json`

返回示例：

```json
{
  "error": {
    "kind": "unauthenticated",
    "message": "API key is missing or invalid",
    "request_id": "33ba813e-fc63-4af8-b338-7f7486f82202"
  }
}
```

返回字段：

| 字段 | 类型 | 必填 | 格式 / 示例 / 约束 | 用途说明 |
|---|---|---|---|---|
| `error` | `object` | 是 | - | - |
| `error.kind` | `string` | 是 | 示例: "unauthenticated" | - |
| `error.message` | `string` | 是 | 示例: "API key is missing or invalid" | - |
| `error.request_id` | `string` | 是 | 格式: uuid；示例: "33ba813e-fc63-4af8-b338-7f7486f82202" | - |

##### `405 Method Not Allowed`

HTTP method is not accepted; inspect the Allow header.

内容类型：`application/json`

返回示例：

```json
{
  "error": {
    "kind": "method_not_allowed",
    "message": "method is not allowed",
    "request_id": "33ba813e-fc63-4af8-b338-7f7486f82202"
  }
}
```

返回字段：

| 字段 | 类型 | 必填 | 格式 / 示例 / 约束 | 用途说明 |
|---|---|---|---|---|
| `error` | `object` | 是 | - | - |
| `error.kind` | `string` | 是 | 示例: "method_not_allowed" | - |
| `error.message` | `string` | 是 | 示例: "method is not allowed" | - |
| `error.request_id` | `string` | 是 | 格式: uuid；示例: "33ba813e-fc63-4af8-b338-7f7486f82202" | - |

##### `409 Conflict`

Digest, revision, state, uniqueness, lock, or recovery conflict.

内容类型：`application/json`

返回示例：

```json
{
  "error": {
    "kind": "configuration_conflict",
    "message": "configuration state changed or is busy",
    "request_id": "33ba813e-fc63-4af8-b338-7f7486f82202"
  }
}
```

返回字段：

| 字段 | 类型 | 必填 | 格式 / 示例 / 约束 | 用途说明 |
|---|---|---|---|---|
| `error` | `object` | 是 | - | - |
| `error.kind` | `string` | 是 | 示例: "configuration_conflict" | - |
| `error.message` | `string` | 是 | 示例: "configuration state changed or is busy" | - |
| `error.request_id` | `string` | 是 | 格式: uuid；示例: "33ba813e-fc63-4af8-b338-7f7486f82202" | - |

##### `422 Unprocessable Entity`

JSON is valid but violates client, IPv4, or configuration semantics.

内容类型：`application/json`

返回示例：

```json
{
  "error": {
    "kind": "invalid_client",
    "message": "client request is not valid for the current state",
    "request_id": "33ba813e-fc63-4af8-b338-7f7486f82202"
  }
}
```

返回字段：

| 字段 | 类型 | 必填 | 格式 / 示例 / 约束 | 用途说明 |
|---|---|---|---|---|
| `error` | `object` | 是 | - | - |
| `error.kind` | `string` | 是 | 示例: "invalid_client" | - |
| `error.message` | `string` | 是 | 示例: "client request is not valid for the current state" | - |
| `error.request_id` | `string` | 是 | 格式: uuid；示例: "33ba813e-fc63-4af8-b338-7f7486f82202" | - |

##### `500 Internal Server Error`

Unclassified internal failure with no implementation details exposed.

内容类型：`application/json`

返回示例：

```json
{
  "error": {
    "kind": "internal_error",
    "message": "request could not be completed",
    "request_id": "33ba813e-fc63-4af8-b338-7f7486f82202"
  }
}
```

返回字段：

| 字段 | 类型 | 必填 | 格式 / 示例 / 约束 | 用途说明 |
|---|---|---|---|---|
| `error` | `object` | 是 | - | - |
| `error.kind` | `string` | 是 | 示例: "internal_error" | - |
| `error.message` | `string` | 是 | 示例: "request could not be completed" | - |
| `error.request_id` | `string` | 是 | 格式: uuid；示例: "33ba813e-fc63-4af8-b338-7f7486f82202" | - |

##### `503 Service Unavailable`

Authentication storage, PKI, OpenVPN, broker, supervisor, or another required dependency is unavailable.

内容类型：`application/json`

返回示例：

```json
{
  "error": {
    "kind": "runtime_unavailable",
    "message": "OpenVPN runtime is unavailable",
    "request_id": "33ba813e-fc63-4af8-b338-7f7486f82202"
  }
}
```

返回字段：

| 字段 | 类型 | 必填 | 格式 / 示例 / 约束 | 用途说明 |
|---|---|---|---|---|
| `error` | `object` | 是 | - | - |
| `error.kind` | `string` | 是 | 示例: "runtime_unavailable" | - |
| `error.message` | `string` | 是 | 示例: "OpenVPN runtime is unavailable" | - |
| `error.request_id` | `string` | 是 | 格式: uuid；示例: "33ba813e-fc63-4af8-b338-7f7486f82202" | - |

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
