# REST API v1

REST API v1 为现有客户端、配置、状态和 runtime service 提供经过认证的远程入口。它不会替代 SQLite 权威状态、operation journal、runtime 锁或本地 `ovpn` CLI。

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
      OVPN_API_CORS_ORIGINS: vpn-admin.example.com
```

`OVPN_API_LISTEN` 使用 `地址:端口` 格式，端口可以是宿主机上任意未占用的 `1-65535` 端口：

| 示例 | 访问范围 |
|---|---|
| `127.0.0.1:11940` | 仅监听宿主机 IPv4 loopback，推荐配合本机 HTTPS 反向代理使用。地址后的 `11940` 可替换为其他空闲端口。 |
| `0.0.0.0:11940` | 监听宿主机所有 IPv4 网卡；地址后的 `11940` 可替换为其他空闲端口。是否能从局域网或公网访问还取决于宿主机防火墙、云安全组和路由。 |

项目 Compose 使用 `network_mode: host`，因此端口直接占用宿主机端口，不需要也不能依靠 Compose `ports` 映射解决冲突。`OVPN_API_LISTEN` 为空时不会启动 API 进程。不要包含 `http://` 或 `https://` 协议前缀，也不要把这些变量加入 `openvpn-maintenance`。

`OVPN_API_CORS_ORIGINS` 可选，用于允许跨域浏览器前端访问 API。配置值不带 `http://` 或 `https://`，支持以下格式：

| 配置值 | 含义 |
|---|---|
| 空 | 关闭 CORS；同源前端或非浏览器客户端不需要配置。 |
| `vpn-admin.example.com` | 允许该域名，不限制浏览器 origin 使用 HTTP 或 HTTPS。 |
| `vpn-admin.example.com:3000` | 允许该域名的指定端口。 |
| `192.0.2.10` | 允许该 IP。 |
| `192.0.2.10:3000` | 允许该 IP 的指定端口。 |
| `*` | 允许任意合法的 HTTP/HTTPS origin；`*` 必须单独填写。 |
| `vpn-admin.example.com,192.0.2.10:3000` | 使用英文逗号分隔多个域名、IP 或带端口的值。 |

匹配时主机名不区分大小写，端口必须精确匹配。不要填写协议、路径、query 或凭据；也不要产生空列表项，例如结尾多写逗号。使用 `*` 会扩大浏览器访问范围，只应在明确需要时启用。

最小 Caddy 边界如下：

```caddyfile
vpn-admin.example.com {
    reverse_proxy 127.0.0.1:11940
}
```

反向代理必须提供 HTTPS、保留 `Authorization` header，并设置适当的网络访问控制。不建议将 API 通过 `0.0.0.0` 直接暴露到公网。

## API key

API key 只能通过容器内本地 CLI 创建、列出和删除。从宿主机执行时，命令格式约定为 `docker exec openvpn ovpn <command>`；以下示例仅列出 `ovpn ...` 简写：

```bash
ovpn api key create frontend-production
ovpn api key list
ovpn api key delete frontend-production --yes
```

完整 key 只在创建时输出一次。为避免终端历史或输出采集，可直接写入新的 mode-`0600` 文件：

```bash
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

检查 API 进程存活状态。无需认证的存活检查，不报告 OpenVPN 或存储健康状态。

#### 请求参数

无请求参数，也不接受请求体。

#### 请求示例

```http
GET /healthz HTTP/1.1
```

#### 返回

##### `200 成功`

API 进程正常运行。

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
| `status` | `string` | 是 | 固定值: "ok" | 存活状态；成功响应时始终为 ok。 |

##### `405 方法不允许`

不接受该 HTTP 方法；请检查 Allow header。

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
| `error` | `object` | 是 | - | 可供程序读取的 API 错误详情。 |
| `error.kind` | `string` | 是 | 示例: "method_not_allowed" | 稳定且可供程序读取的错误类型。 |
| `error.message` | `string` | 是 | 示例: "method is not allowed" | 可安全展示的错误消息。 |
| `error.request_id` | `string` | 是 | 格式: uuid；示例: "33ba813e-fc63-4af8-b338-7f7486f82202" | 用于关联请求与服务器日志的 UUID。 |

### 2. `GET /api/v1/version`

读取构建与兼容性元数据

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

##### `200 成功`

构建与兼容性元数据。

内容类型：`application/json`

返回示例：

```json
{
  "version": "4.1.0",
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
| `version` | `string` | 是 | 示例: "4.1.0" | OpenVPN Docker 发布版本。 |
| `data_schema` | `integer` | 是 | 示例: 4 | 权威数据 schema 版本。 |
| `commit` | `string` | 是 | 示例: "dd9b5213f5002a7e69f160e3fd2615e0b3d8d224" | 构建二进制时使用的源码版本。 |
| `build_date` | `string` | 是 | 示例: "2026-08-19T09:45:40Z" | 二进制构建时的 UTC 时间。 |
| `go_version` | `string` | 是 | 示例: "go1.26.5" | 构建二进制时使用的 Go 工具链版本。 |
| `dependencies` | `object` | 是 | - | 构建时使用的库依赖版本。 |
| `dependencies.sqlite` | `string` | 是 | 示例: "github.com/mattn/go-sqlite3 v1.14.48" | SQLite 驱动模块及版本。 |
| `dependencies.yaml` | `string` | 是 | 示例: "go.yaml.in/yaml/v3 v3.0.4" | YAML 解析模块及版本。 |
| `compatibility` | `object` | 是 | - | 运行时兼容性契约及支持的版本。 |
| `compatibility.contract_version` | `integer` | 是 | 示例: 1 | 兼容性契约 schema 版本。 |
| `compatibility.adapter` | `string` | 是 | 示例: "openvpn-2.7" | 为当前运行时选择的兼容性适配器。 |
| `compatibility.template_family` | `string` | 是 | 示例: "openvpn-2.7" | 生成 OpenVPN 文件时选择的模板系列。 |
| `compatibility.supported_openvpn_versions` | `string[]` | 是 | 示例: ["2.7.6"] | 该兼容性契约接受的 OpenVPN 版本。 |

##### `401 未认证`

Bearer API key 缺失、格式错误、未知或已删除。

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
| `error` | `object` | 是 | - | 可供程序读取的 API 错误详情。 |
| `error.kind` | `string` | 是 | 示例: "unauthenticated" | 稳定且可供程序读取的错误类型。 |
| `error.message` | `string` | 是 | 示例: "API key is missing or invalid" | 可安全展示的错误消息。 |
| `error.request_id` | `string` | 是 | 格式: uuid；示例: "33ba813e-fc63-4af8-b338-7f7486f82202" | 用于关联请求与服务器日志的 UUID。 |

##### `405 方法不允许`

不接受该 HTTP 方法；请检查 Allow header。

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
| `error` | `object` | 是 | - | 可供程序读取的 API 错误详情。 |
| `error.kind` | `string` | 是 | 示例: "method_not_allowed" | 稳定且可供程序读取的错误类型。 |
| `error.message` | `string` | 是 | 示例: "method is not allowed" | 可安全展示的错误消息。 |
| `error.request_id` | `string` | 是 | 格式: uuid；示例: "33ba813e-fc63-4af8-b338-7f7486f82202" | 用于关联请求与服务器日志的 UUID。 |

##### `503 服务不可用`

认证存储、PKI、OpenVPN、broker、supervisor 或其他必要依赖不可用。

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
| `error` | `object` | 是 | - | 可供程序读取的 API 错误详情。 |
| `error.kind` | `string` | 是 | 示例: "runtime_unavailable" | 稳定且可供程序读取的错误类型。 |
| `error.message` | `string` | 是 | 示例: "OpenVPN runtime is unavailable" | 可安全展示的错误消息。 |
| `error.request_id` | `string` | 是 | 格式: uuid；示例: "33ba813e-fc63-4af8-b338-7f7486f82202" | 用于关联请求与服务器日志的 UUID。 |

### 3. `GET /api/v1/state`

读取实例健康摘要

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

##### `200 成功`

实例状态摘要。

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
| `version` | `integer` | 是 | 固定值: 1 | 响应契约 schema 版本。 |
| `state` | `"EMPTY" \| "HEALTHY" \| "DEGRADED_REPAIRABLE" \| "DEGRADED_RECOVERABLE" \| "DEGRADED_REISSUABLE" \| "CRITICAL" \| "UNRECOVERABLE"` | 是 | 可选值: "EMPTY" \| "HEALTHY" \| "DEGRADED_REPAIRABLE" \| "DEGRADED_RECOVERABLE" \| "DEGRADED_REISSUABLE" \| "CRITICAL" \| "UNRECOVERABLE" | 权威实例整体健康状态。 |
| `data_schema` | `integer` | 是 | 示例: 4 | 权威数据 schema 版本。 |
| `instance_id` | `string` | 否 | 格式: uuid；示例: "bbffeb8c-2d11-4613-874d-b5fcc804a608" | 已初始化 OpenVPN 实例的不可变 UUID。 |
| `revision` | `integer` | 否 | 最小值: 1；示例: 12 | 单调递增的已应用配置版本。 |
| `scanned_at` | `string` | 是 | 格式: date-time；示例: "2026-08-19T09:55:07Z" | 状态扫描的 UTC 时间。 |
| `issue_count` | `integer` | 是 | 最小值: 0；示例: 0 | 当前检测到的诊断问题数量。 |
| `pending_operation_count` | `integer` | 是 | 最小值: 0；示例: 0 | 未完成 journal 操作的数量。 |
| `issues` | `object[]` | 否 | - | 详细诊断问题；为空时省略。 |
| `issues[].id` | `string` | 否 | - | 稳定的诊断问题标识符。 |
| `issues[].severity` | `"repairable" \| "recoverable" \| "reissuable" \| "critical" \| "unrecoverable"` | 否 | 可选值: "repairable" \| "recoverable" \| "reissuable" \| "critical" \| "unrecoverable" | 诊断严重程度和恢复类别。 |
| `issues[].action` | `string` | 否 | - | 针对该问题建议执行的运维动作。 |
| `issues[].target` | `string` | 否 | - | 适用时受影响的路径或资源。 |
| `issues[].owner_id` | `string` | 否 | - | 所属对象的稳定标识符。 |
| `issues[].artifact_kind` | `string` | 否 | - | 受影响派生制品的类型。 |
| `issues[].detail` | `string` | 否 | - | 可读的诊断详情。 |

##### `401 未认证`

Bearer API key 缺失、格式错误、未知或已删除。

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
| `error` | `object` | 是 | - | 可供程序读取的 API 错误详情。 |
| `error.kind` | `string` | 是 | 示例: "unauthenticated" | 稳定且可供程序读取的错误类型。 |
| `error.message` | `string` | 是 | 示例: "API key is missing or invalid" | 可安全展示的错误消息。 |
| `error.request_id` | `string` | 是 | 格式: uuid；示例: "33ba813e-fc63-4af8-b338-7f7486f82202" | 用于关联请求与服务器日志的 UUID。 |

##### `405 方法不允许`

不接受该 HTTP 方法；请检查 Allow header。

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
| `error` | `object` | 是 | - | 可供程序读取的 API 错误详情。 |
| `error.kind` | `string` | 是 | 示例: "method_not_allowed" | 稳定且可供程序读取的错误类型。 |
| `error.message` | `string` | 是 | 示例: "method is not allowed" | 可安全展示的错误消息。 |
| `error.request_id` | `string` | 是 | 格式: uuid；示例: "33ba813e-fc63-4af8-b338-7f7486f82202" | 用于关联请求与服务器日志的 UUID。 |

##### `500 服务器内部错误`

未分类的内部错误，不会暴露实现细节。

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
| `error` | `object` | 是 | - | 可供程序读取的 API 错误详情。 |
| `error.kind` | `string` | 是 | 示例: "internal_error" | 稳定且可供程序读取的错误类型。 |
| `error.message` | `string` | 是 | 示例: "request could not be completed" | 可安全展示的错误消息。 |
| `error.request_id` | `string` | 是 | 格式: uuid；示例: "33ba813e-fc63-4af8-b338-7f7486f82202" | 用于关联请求与服务器日志的 UUID。 |

##### `503 服务不可用`

认证存储、PKI、OpenVPN、broker、supervisor 或其他必要依赖不可用。

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
| `error` | `object` | 是 | - | 可供程序读取的 API 错误详情。 |
| `error.kind` | `string` | 是 | 示例: "runtime_unavailable" | 稳定且可供程序读取的错误类型。 |
| `error.message` | `string` | 是 | 示例: "OpenVPN runtime is unavailable" | 可安全展示的错误消息。 |
| `error.request_id` | `string` | 是 | 格式: uuid；示例: "33ba813e-fc63-4af8-b338-7f7486f82202" | 用于关联请求与服务器日志的 UUID。 |

### 4. `GET /api/v1/state/doctor`

读取实例详细诊断。返回状态摘要；存在诊断问题时还会返回 issues 数组。

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

##### `200 成功`

实例详细诊断；没有问题时省略 `issues`。

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
| `version` | `integer` | 是 | 固定值: 1 | 响应契约 schema 版本。 |
| `state` | `"EMPTY" \| "HEALTHY" \| "DEGRADED_REPAIRABLE" \| "DEGRADED_RECOVERABLE" \| "DEGRADED_REISSUABLE" \| "CRITICAL" \| "UNRECOVERABLE"` | 是 | 可选值: "EMPTY" \| "HEALTHY" \| "DEGRADED_REPAIRABLE" \| "DEGRADED_RECOVERABLE" \| "DEGRADED_REISSUABLE" \| "CRITICAL" \| "UNRECOVERABLE" | 权威实例整体健康状态。 |
| `data_schema` | `integer` | 是 | 示例: 4 | 权威数据 schema 版本。 |
| `instance_id` | `string` | 否 | 格式: uuid；示例: "bbffeb8c-2d11-4613-874d-b5fcc804a608" | 已初始化 OpenVPN 实例的不可变 UUID。 |
| `revision` | `integer` | 否 | 最小值: 1；示例: 12 | 单调递增的已应用配置版本。 |
| `scanned_at` | `string` | 是 | 格式: date-time；示例: "2026-08-19T09:55:07Z" | 状态扫描的 UTC 时间。 |
| `issue_count` | `integer` | 是 | 最小值: 0；示例: 1 | 当前检测到的诊断问题数量。 |
| `pending_operation_count` | `integer` | 是 | 最小值: 0；示例: 0 | 未完成 journal 操作的数量。 |
| `issues` | `object[]` | 否 | 示例: [{"id":"DECLARATIVE_CONFIG_UNAVAILABLE","severity":"repairable","action":"export-config","target":"/etc/ovpn-conf/config.yaml","detail":"declarative configuration is unavailable"}] | 详细诊断问题；为空时省略。 |
| `issues[].id` | `string` | 否 | 示例: "DECLARATIVE_CONFIG_UNAVAILABLE" | 稳定的诊断问题标识符。 |
| `issues[].severity` | `"repairable" \| "recoverable" \| "reissuable" \| "critical" \| "unrecoverable"` | 否 | 可选值: "repairable" \| "recoverable" \| "reissuable" \| "critical" \| "unrecoverable" | 诊断严重程度和恢复类别。 |
| `issues[].action` | `string` | 否 | 示例: "export-config" | 针对该问题建议执行的运维动作。 |
| `issues[].target` | `string` | 否 | 示例: "/etc/ovpn-conf/config.yaml" | 适用时受影响的路径或资源。 |
| `issues[].owner_id` | `string` | 否 | - | 所属对象的稳定标识符。 |
| `issues[].artifact_kind` | `string` | 否 | - | 受影响派生制品的类型。 |
| `issues[].detail` | `string` | 否 | 示例: "declarative configuration is unavailable" | 可读的诊断详情。 |

##### `401 未认证`

Bearer API key 缺失、格式错误、未知或已删除。

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
| `error` | `object` | 是 | - | 可供程序读取的 API 错误详情。 |
| `error.kind` | `string` | 是 | 示例: "unauthenticated" | 稳定且可供程序读取的错误类型。 |
| `error.message` | `string` | 是 | 示例: "API key is missing or invalid" | 可安全展示的错误消息。 |
| `error.request_id` | `string` | 是 | 格式: uuid；示例: "33ba813e-fc63-4af8-b338-7f7486f82202" | 用于关联请求与服务器日志的 UUID。 |

##### `405 方法不允许`

不接受该 HTTP 方法；请检查 Allow header。

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
| `error` | `object` | 是 | - | 可供程序读取的 API 错误详情。 |
| `error.kind` | `string` | 是 | 示例: "method_not_allowed" | 稳定且可供程序读取的错误类型。 |
| `error.message` | `string` | 是 | 示例: "method is not allowed" | 可安全展示的错误消息。 |
| `error.request_id` | `string` | 是 | 格式: uuid；示例: "33ba813e-fc63-4af8-b338-7f7486f82202" | 用于关联请求与服务器日志的 UUID。 |

##### `500 服务器内部错误`

未分类的内部错误，不会暴露实现细节。

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
| `error` | `object` | 是 | - | 可供程序读取的 API 错误详情。 |
| `error.kind` | `string` | 是 | 示例: "internal_error" | 稳定且可供程序读取的错误类型。 |
| `error.message` | `string` | 是 | 示例: "request could not be completed" | 可安全展示的错误消息。 |
| `error.request_id` | `string` | 是 | 格式: uuid；示例: "33ba813e-fc63-4af8-b338-7f7486f82202" | 用于关联请求与服务器日志的 UUID。 |

##### `503 服务不可用`

认证存储、PKI、OpenVPN、broker、supervisor 或其他必要依赖不可用。

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
| `error` | `object` | 是 | - | 可供程序读取的 API 错误详情。 |
| `error.kind` | `string` | 是 | 示例: "runtime_unavailable" | 稳定且可供程序读取的错误类型。 |
| `error.message` | `string` | 是 | 示例: "OpenVPN runtime is unavailable" | 可安全展示的错误消息。 |
| `error.request_id` | `string` | 是 | 格式: uuid；示例: "33ba813e-fc63-4af8-b338-7f7486f82202" | 用于关联请求与服务器日志的 UUID。 |

### 5. `GET /api/v1/clients`

列出有效、已吊销和已删除的客户端

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

##### `200 成功`

客户端列表。

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
| `version` | `integer` | 是 | 固定值: 1 | 响应契约 schema 版本。 |
| `clients` | `object[]` | 是 | 示例: [{"id":"c0d4f871-6ea6-42b3-9e7b-f00cc1ec354e","name":"alice-laptop","status":"active","ipv4":{"mode":"static","address":"10.42.0.30","state":"configured"},"connection":"connected"}] | 该响应包含的客户端。 |
| `clients[].id` | `string` | 是 | 格式: uuid；示例: "c0d4f871-6ea6-42b3-9e7b-f00cc1ec354e" | 该对象的稳定标识符。 |
| `clients[].name` | `string` | 是 | 格式: ^[A-Za-z0-9][A-Za-z0-9_.-]*$；示例: "alice-laptop" | 稳定且可读的名称。 |
| `clients[].status` | `"active" \| "revoked" \| "deleted"` | 是 | 可选值: "active" \| "revoked" \| "deleted" | 客户端凭据生命周期状态。 |
| `clients[].ipv4` | `object` | 是 | - | 当前客户端 IPv4 分配视图。 |
| `clients[].ipv4.mode` | `"none" \| "static" \| "dynamic"` | 是 | 可选值: "none" \| "static" \| "dynamic" | IPv4 分配模式。 |
| `clients[].ipv4.address` | `string \| null` | 是 | 格式: ipv4；示例: "10.42.0.30" | IPv4 地址；未分配地址时为 null。 |
| `clients[].ipv4.state` | `"configured" \| "retained" \| "unavailable"` | 是 | 可选值: "configured" \| "retained" \| "unavailable" | 客户端地址分配的可用状态。 |
| `clients[].connection` | `string` | 否 | 示例: "connected" | 运行时数据可用时的当前连接状态。 |

##### `401 未认证`

Bearer API key 缺失、格式错误、未知或已删除。

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
| `error` | `object` | 是 | - | 可供程序读取的 API 错误详情。 |
| `error.kind` | `string` | 是 | 示例: "unauthenticated" | 稳定且可供程序读取的错误类型。 |
| `error.message` | `string` | 是 | 示例: "API key is missing or invalid" | 可安全展示的错误消息。 |
| `error.request_id` | `string` | 是 | 格式: uuid；示例: "33ba813e-fc63-4af8-b338-7f7486f82202" | 用于关联请求与服务器日志的 UUID。 |

##### `405 方法不允许`

不接受该 HTTP 方法；请检查 Allow header。

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
| `error` | `object` | 是 | - | 可供程序读取的 API 错误详情。 |
| `error.kind` | `string` | 是 | 示例: "method_not_allowed" | 稳定且可供程序读取的错误类型。 |
| `error.message` | `string` | 是 | 示例: "method is not allowed" | 可安全展示的错误消息。 |
| `error.request_id` | `string` | 是 | 格式: uuid；示例: "33ba813e-fc63-4af8-b338-7f7486f82202" | 用于关联请求与服务器日志的 UUID。 |

##### `500 服务器内部错误`

未分类的内部错误，不会暴露实现细节。

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
| `error` | `object` | 是 | - | 可供程序读取的 API 错误详情。 |
| `error.kind` | `string` | 是 | 示例: "internal_error" | 稳定且可供程序读取的错误类型。 |
| `error.message` | `string` | 是 | 示例: "request could not be completed" | 可安全展示的错误消息。 |
| `error.request_id` | `string` | 是 | 格式: uuid；示例: "33ba813e-fc63-4af8-b338-7f7486f82202" | 用于关联请求与服务器日志的 UUID。 |

##### `503 服务不可用`

认证存储、PKI、OpenVPN、broker、supervisor 或其他必要依赖不可用。

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
| `error` | `object` | 是 | - | 可供程序读取的 API 错误详情。 |
| `error.kind` | `string` | 是 | 示例: "runtime_unavailable" | 稳定且可供程序读取的错误类型。 |
| `error.message` | `string` | 是 | 示例: "OpenVPN runtime is unavailable" | 可安全展示的错误消息。 |
| `error.request_id` | `string` | 是 | 格式: uuid；示例: "33ba813e-fc63-4af8-b338-7f7486f82202" | 用于关联请求与服务器日志的 UUID。 |

### 6. `POST /api/v1/clients`

创建客户端凭据和配置文件

#### 请求参数

##### Header 参数

| 字段 | 位置 | 类型 | 必填 | 格式 / 示例 / 约束 | 用途说明 |
|---|---|---|---|---|---|
| `Authorization` | `header` | `string` | 是 | Bearer ovpn_v1.<uuid>.<secret> | API 身份认证凭据。Bearer 后填写服务生成的 API Key。 |
| `Content-Type` | `header` | `string` | 是 | application/json | 声明请求体使用 JSON 格式。 |

##### JSON Body 字段

| 字段 | 位置 | 类型 | 必填 | 格式 / 示例 / 约束 | 用途说明 |
|---|---|---|---|---|---|
| `name` | `body` | `string` | 是 | 格式: ^[A-Za-z0-9][A-Za-z0-9_.-]*$；示例: "alice-laptop" | 稳定的客户端名称。必须以字母或数字开头，可包含字母、数字、下划线、点和连字符。 |
| `ipv4` | `body` | `string` | 是 | 示例: "auto" | IPv4 分配方式：auto、dynamic 或有效的静态 IPv4 地址。 |

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

##### `201 已创建`

客户端已创建。

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
| `version` | `integer` | 是 | 固定值: 1 | 响应契约 schema 版本。 |
| `operation_id` | `string` | 是 | 格式: uuid；示例: "7a21e1f8-1ce1-4694-977f-b275eadb8651" | 已提交 journal 操作的 UUID。 |
| `client` | `object` | 是 | - | 权威客户端身份与生命周期状态。 |
| `client.id` | `string` | 是 | 格式: uuid；示例: "c0d4f871-6ea6-42b3-9e7b-f00cc1ec354e" | 该对象的稳定标识符。 |
| `client.name` | `string` | 是 | 格式: ^[A-Za-z0-9][A-Za-z0-9_.-]*$；示例: "alice-laptop" | 稳定且可读的名称。 |
| `client.status` | `"active" \| "revoked" \| "deleted"` | 是 | 可选值: "active" \| "revoked" \| "deleted" | 客户端凭据生命周期状态。 |
| `client.ipv4` | `object` | 是 | - | 当前客户端 IPv4 分配视图。 |
| `client.ipv4.mode` | `"none" \| "static" \| "dynamic"` | 是 | 可选值: "none" \| "static" \| "dynamic" | IPv4 分配模式。 |
| `client.ipv4.address` | `string \| null` | 是 | 格式: ipv4；示例: "10.42.0.2" | IPv4 地址；未分配地址时为 null。 |
| `client.ipv4.state` | `"configured" \| "retained" \| "unavailable"` | 是 | 可选值: "configured" \| "retained" \| "unavailable" | 客户端地址分配的可用状态。 |
| `client.connection` | `string` | 否 | - | 运行时数据可用时的当前连接状态。 |
| `kick_required` | `boolean` | 是 | 示例: false | 是否必须断开已变更客户端的活动会话。 |
| `profile_redistribution_required` | `boolean` | 是 | 示例: true | 是否必须重新分发该客户端生成的配置文件。 |
| `runtime` | `object` | 否 | - | 变更后单个客户端的运行时收敛结果。 |
| `runtime.client_id` | `string` | 否 | 格式: uuid | 不可变的客户端 UUID。 |
| `runtime.status` | `"ok" \| "unavailable"` | 否 | 可选值: "ok" \| "unavailable" | 请求的运行时操作是否完成或不可用。 |
| `runtime.result` | `object` | 否 | - | 请求断开客户端会话的结果。 |
| `runtime.result.version` | `integer` | 否 | 固定值: 1 | 响应契约 schema 版本。 |
| `runtime.result.client_id` | `string` | 否 | 格式: uuid | 不可变的客户端 UUID。 |
| `runtime.result.client_name` | `string` | 否 | - | 与会话关联的可读客户端名称。 |
| `runtime.result.was_connected` | `boolean` | 否 | - | 请求前是否存在匹配会话。 |
| `runtime.result.disconnected` | `boolean` | 否 | - | 是否至少断开了一个匹配会话。 |
| `runtime.result.connections` | `integer` | 否 | 最小值: 0 | 断开请求处理的匹配会话数量。 |

##### `400 错误请求`

path、query、header、媒体类型、请求体或 JSON 格式错误。

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
| `error` | `object` | 是 | - | 可供程序读取的 API 错误详情。 |
| `error.kind` | `string` | 是 | 示例: "invalid_json" | 稳定且可供程序读取的错误类型。 |
| `error.message` | `string` | 是 | 示例: "request body is invalid" | 可安全展示的错误消息。 |
| `error.request_id` | `string` | 是 | 格式: uuid；示例: "33ba813e-fc63-4af8-b338-7f7486f82202" | 用于关联请求与服务器日志的 UUID。 |

##### `401 未认证`

Bearer API key 缺失、格式错误、未知或已删除。

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
| `error` | `object` | 是 | - | 可供程序读取的 API 错误详情。 |
| `error.kind` | `string` | 是 | 示例: "unauthenticated" | 稳定且可供程序读取的错误类型。 |
| `error.message` | `string` | 是 | 示例: "API key is missing or invalid" | 可安全展示的错误消息。 |
| `error.request_id` | `string` | 是 | 格式: uuid；示例: "33ba813e-fc63-4af8-b338-7f7486f82202" | 用于关联请求与服务器日志的 UUID。 |

##### `409 冲突`

摘要、版本、状态、唯一性、锁或恢复流程发生冲突。

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
| `error` | `object` | 是 | - | 可供程序读取的 API 错误详情。 |
| `error.kind` | `string` | 是 | 示例: "configuration_conflict" | 稳定且可供程序读取的错误类型。 |
| `error.message` | `string` | 是 | 示例: "configuration state changed or is busy" | 可安全展示的错误消息。 |
| `error.request_id` | `string` | 是 | 格式: uuid；示例: "33ba813e-fc63-4af8-b338-7f7486f82202" | 用于关联请求与服务器日志的 UUID。 |

##### `422 无法处理`

JSON 格式有效，但违反客户端、IPv4 或配置语义规则。

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
| `error` | `object` | 是 | - | 可供程序读取的 API 错误详情。 |
| `error.kind` | `string` | 是 | 示例: "invalid_client" | 稳定且可供程序读取的错误类型。 |
| `error.message` | `string` | 是 | 示例: "client request is not valid for the current state" | 可安全展示的错误消息。 |
| `error.request_id` | `string` | 是 | 格式: uuid；示例: "33ba813e-fc63-4af8-b338-7f7486f82202" | 用于关联请求与服务器日志的 UUID。 |

##### `500 服务器内部错误`

未分类的内部错误，不会暴露实现细节。

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
| `error` | `object` | 是 | - | 可供程序读取的 API 错误详情。 |
| `error.kind` | `string` | 是 | 示例: "internal_error" | 稳定且可供程序读取的错误类型。 |
| `error.message` | `string` | 是 | 示例: "request could not be completed" | 可安全展示的错误消息。 |
| `error.request_id` | `string` | 是 | 格式: uuid；示例: "33ba813e-fc63-4af8-b338-7f7486f82202" | 用于关联请求与服务器日志的 UUID。 |

##### `503 服务不可用`

认证存储、PKI、OpenVPN、broker、supervisor 或其他必要依赖不可用。

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
| `error` | `object` | 是 | - | 可供程序读取的 API 错误详情。 |
| `error.kind` | `string` | 是 | 示例: "runtime_unavailable" | 稳定且可供程序读取的错误类型。 |
| `error.message` | `string` | 是 | 示例: "OpenVPN runtime is unavailable" | 可安全展示的错误消息。 |
| `error.request_id` | `string` | 是 | 格式: uuid；示例: "33ba813e-fc63-4af8-b338-7f7486f82202" | 用于关联请求与服务器日志的 UUID。 |

### 7. `GET /api/v1/clients/{client_id}`

读取单个客户端

#### 请求参数

##### Header 参数

| 字段 | 位置 | 类型 | 必填 | 格式 / 示例 / 约束 | 用途说明 |
|---|---|---|---|---|---|
| `Authorization` | `header` | `string` | 是 | Bearer ovpn_v1.<uuid>.<secret> | API 身份认证凭据。Bearer 后填写服务生成的 API Key。 |

##### Path 参数

| 字段 | 位置 | 类型 | 必填 | 格式 / 示例 / 约束 | 用途说明 |
|---|---|---|---|---|---|
| `client_id` | `path` | `string` | 是 | 格式: uuid；示例: "c0d4f871-6ea6-42b3-9e7b-f00cc1ec354e" | 完整、规范且不可变的客户端 UUID；不接受名称或 UUID 前缀。 |

#### 请求示例

```http
GET /api/v1/clients/c0d4f871-6ea6-42b3-9e7b-f00cc1ec354e HTTP/1.1
Authorization: Bearer ovpn_v1.<uuid>.<secret>
```

#### 返回

##### `200 成功`

客户端详情。

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
| `id` | `string` | 是 | 格式: uuid；示例: "c0d4f871-6ea6-42b3-9e7b-f00cc1ec354e" | 该对象的稳定标识符。 |
| `name` | `string` | 是 | 格式: ^[A-Za-z0-9][A-Za-z0-9_.-]*$；示例: "alice-laptop" | 稳定且可读的名称。 |
| `status` | `"active" \| "revoked" \| "deleted"` | 是 | 可选值: "active" \| "revoked" \| "deleted" | 客户端凭据生命周期状态。 |
| `ipv4` | `object` | 是 | - | 当前客户端 IPv4 分配视图。 |
| `ipv4.mode` | `"none" \| "static" \| "dynamic"` | 是 | 可选值: "none" \| "static" \| "dynamic" | IPv4 分配模式。 |
| `ipv4.address` | `string \| null` | 是 | 格式: ipv4；示例: "10.42.0.30" | IPv4 地址；未分配地址时为 null。 |
| `ipv4.state` | `"configured" \| "retained" \| "unavailable"` | 是 | 可选值: "configured" \| "retained" \| "unavailable" | 客户端地址分配的可用状态。 |
| `connection` | `string` | 否 | - | 运行时数据可用时的当前连接状态。 |

##### `400 错误请求`

path、query、header、媒体类型、请求体或 JSON 格式错误。

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
| `error` | `object` | 是 | - | 可供程序读取的 API 错误详情。 |
| `error.kind` | `string` | 是 | 示例: "invalid_json" | 稳定且可供程序读取的错误类型。 |
| `error.message` | `string` | 是 | 示例: "request body is invalid" | 可安全展示的错误消息。 |
| `error.request_id` | `string` | 是 | 格式: uuid；示例: "33ba813e-fc63-4af8-b338-7f7486f82202" | 用于关联请求与服务器日志的 UUID。 |

##### `401 未认证`

Bearer API key 缺失、格式错误、未知或已删除。

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
| `error` | `object` | 是 | - | 可供程序读取的 API 错误详情。 |
| `error.kind` | `string` | 是 | 示例: "unauthenticated" | 稳定且可供程序读取的错误类型。 |
| `error.message` | `string` | 是 | 示例: "API key is missing or invalid" | 可安全展示的错误消息。 |
| `error.request_id` | `string` | 是 | 格式: uuid；示例: "33ba813e-fc63-4af8-b338-7f7486f82202" | 用于关联请求与服务器日志的 UUID。 |

##### `404 未找到`

资源或客户端不存在。

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
| `error` | `object` | 是 | - | 可供程序读取的 API 错误详情。 |
| `error.kind` | `string` | 是 | 示例: "client_not_found" | 稳定且可供程序读取的错误类型。 |
| `error.message` | `string` | 是 | 示例: "client was not found" | 可安全展示的错误消息。 |
| `error.request_id` | `string` | 是 | 格式: uuid；示例: "33ba813e-fc63-4af8-b338-7f7486f82202" | 用于关联请求与服务器日志的 UUID。 |

##### `405 方法不允许`

不接受该 HTTP 方法；请检查 Allow header。

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
| `error` | `object` | 是 | - | 可供程序读取的 API 错误详情。 |
| `error.kind` | `string` | 是 | 示例: "method_not_allowed" | 稳定且可供程序读取的错误类型。 |
| `error.message` | `string` | 是 | 示例: "method is not allowed" | 可安全展示的错误消息。 |
| `error.request_id` | `string` | 是 | 格式: uuid；示例: "33ba813e-fc63-4af8-b338-7f7486f82202" | 用于关联请求与服务器日志的 UUID。 |

##### `500 服务器内部错误`

未分类的内部错误，不会暴露实现细节。

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
| `error` | `object` | 是 | - | 可供程序读取的 API 错误详情。 |
| `error.kind` | `string` | 是 | 示例: "internal_error" | 稳定且可供程序读取的错误类型。 |
| `error.message` | `string` | 是 | 示例: "request could not be completed" | 可安全展示的错误消息。 |
| `error.request_id` | `string` | 是 | 格式: uuid；示例: "33ba813e-fc63-4af8-b338-7f7486f82202" | 用于关联请求与服务器日志的 UUID。 |

##### `503 服务不可用`

认证存储、PKI、OpenVPN、broker、supervisor 或其他必要依赖不可用。

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
| `error` | `object` | 是 | - | 可供程序读取的 API 错误详情。 |
| `error.kind` | `string` | 是 | 示例: "runtime_unavailable" | 稳定且可供程序读取的错误类型。 |
| `error.message` | `string` | 是 | 示例: "OpenVPN runtime is unavailable" | 可安全展示的错误消息。 |
| `error.request_id` | `string` | 是 | 格式: uuid；示例: "33ba813e-fc63-4af8-b338-7f7486f82202" | 用于关联请求与服务器日志的 UUID。 |

### 8. `PATCH /api/v1/clients/{client_id}`

重命名客户端

#### 请求参数

##### Header 参数

| 字段 | 位置 | 类型 | 必填 | 格式 / 示例 / 约束 | 用途说明 |
|---|---|---|---|---|---|
| `Authorization` | `header` | `string` | 是 | Bearer ovpn_v1.<uuid>.<secret> | API 身份认证凭据。Bearer 后填写服务生成的 API Key。 |
| `Content-Type` | `header` | `string` | 是 | application/json | 声明请求体使用 JSON 格式。 |

##### Path 参数

| 字段 | 位置 | 类型 | 必填 | 格式 / 示例 / 约束 | 用途说明 |
|---|---|---|---|---|---|
| `client_id` | `path` | `string` | 是 | 格式: uuid；示例: "c0d4f871-6ea6-42b3-9e7b-f00cc1ec354e" | 完整、规范且不可变的客户端 UUID；不接受名称或 UUID 前缀。 |

##### JSON Body 字段

| 字段 | 位置 | 类型 | 必填 | 格式 / 示例 / 约束 | 用途说明 |
|---|---|---|---|---|---|
| `name` | `body` | `string` | 是 | 格式: ^[A-Za-z0-9][A-Za-z0-9_.-]*$；示例: "alice-notebook" | 新的稳定客户端名称，格式与创建客户端时相同。 |

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

##### `200 成功`

客户端已重命名并重新生成配置文件。

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
| `version` | `integer` | 是 | 固定值: 1 | 响应契约 schema 版本。 |
| `operation_id` | `string` | 是 | 格式: uuid；示例: "47260b5b-47dd-4f9b-814f-4803668c5934" | 已提交 journal 操作的 UUID。 |
| `client` | `object` | 是 | - | 权威客户端身份与生命周期状态。 |
| `client.id` | `string` | 是 | 格式: uuid；示例: "c0d4f871-6ea6-42b3-9e7b-f00cc1ec354e" | 该对象的稳定标识符。 |
| `client.name` | `string` | 是 | 格式: ^[A-Za-z0-9][A-Za-z0-9_.-]*$；示例: "alice-notebook" | 稳定且可读的名称。 |
| `client.status` | `"active" \| "revoked" \| "deleted"` | 是 | 可选值: "active" \| "revoked" \| "deleted" | 客户端凭据生命周期状态。 |
| `client.ipv4` | `object` | 是 | - | 当前客户端 IPv4 分配视图。 |
| `client.ipv4.mode` | `"none" \| "static" \| "dynamic"` | 是 | 可选值: "none" \| "static" \| "dynamic" | IPv4 分配模式。 |
| `client.ipv4.address` | `string \| null` | 是 | 格式: ipv4；示例: "10.42.0.30" | IPv4 地址；未分配地址时为 null。 |
| `client.ipv4.state` | `"configured" \| "retained" \| "unavailable"` | 是 | 可选值: "configured" \| "retained" \| "unavailable" | 客户端地址分配的可用状态。 |
| `client.connection` | `string` | 否 | - | 运行时数据可用时的当前连接状态。 |
| `kick_required` | `boolean` | 是 | 示例: false | 是否必须断开已变更客户端的活动会话。 |
| `profile_redistribution_required` | `boolean` | 是 | 示例: true | 是否必须重新分发该客户端生成的配置文件。 |
| `runtime` | `object` | 否 | - | 变更后单个客户端的运行时收敛结果。 |
| `runtime.client_id` | `string` | 否 | 格式: uuid | 不可变的客户端 UUID。 |
| `runtime.status` | `"ok" \| "unavailable"` | 否 | 可选值: "ok" \| "unavailable" | 请求的运行时操作是否完成或不可用。 |
| `runtime.result` | `object` | 否 | - | 请求断开客户端会话的结果。 |
| `runtime.result.version` | `integer` | 否 | 固定值: 1 | 响应契约 schema 版本。 |
| `runtime.result.client_id` | `string` | 否 | 格式: uuid | 不可变的客户端 UUID。 |
| `runtime.result.client_name` | `string` | 否 | - | 与会话关联的可读客户端名称。 |
| `runtime.result.was_connected` | `boolean` | 否 | - | 请求前是否存在匹配会话。 |
| `runtime.result.disconnected` | `boolean` | 否 | - | 是否至少断开了一个匹配会话。 |
| `runtime.result.connections` | `integer` | 否 | 最小值: 0 | 断开请求处理的匹配会话数量。 |

##### `400 错误请求`

path、query、header、媒体类型、请求体或 JSON 格式错误。

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
| `error` | `object` | 是 | - | 可供程序读取的 API 错误详情。 |
| `error.kind` | `string` | 是 | 示例: "invalid_json" | 稳定且可供程序读取的错误类型。 |
| `error.message` | `string` | 是 | 示例: "request body is invalid" | 可安全展示的错误消息。 |
| `error.request_id` | `string` | 是 | 格式: uuid；示例: "33ba813e-fc63-4af8-b338-7f7486f82202" | 用于关联请求与服务器日志的 UUID。 |

##### `401 未认证`

Bearer API key 缺失、格式错误、未知或已删除。

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
| `error` | `object` | 是 | - | 可供程序读取的 API 错误详情。 |
| `error.kind` | `string` | 是 | 示例: "unauthenticated" | 稳定且可供程序读取的错误类型。 |
| `error.message` | `string` | 是 | 示例: "API key is missing or invalid" | 可安全展示的错误消息。 |
| `error.request_id` | `string` | 是 | 格式: uuid；示例: "33ba813e-fc63-4af8-b338-7f7486f82202" | 用于关联请求与服务器日志的 UUID。 |

##### `404 未找到`

资源或客户端不存在。

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
| `error` | `object` | 是 | - | 可供程序读取的 API 错误详情。 |
| `error.kind` | `string` | 是 | 示例: "client_not_found" | 稳定且可供程序读取的错误类型。 |
| `error.message` | `string` | 是 | 示例: "client was not found" | 可安全展示的错误消息。 |
| `error.request_id` | `string` | 是 | 格式: uuid；示例: "33ba813e-fc63-4af8-b338-7f7486f82202" | 用于关联请求与服务器日志的 UUID。 |

##### `405 方法不允许`

不接受该 HTTP 方法；请检查 Allow header。

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
| `error` | `object` | 是 | - | 可供程序读取的 API 错误详情。 |
| `error.kind` | `string` | 是 | 示例: "method_not_allowed" | 稳定且可供程序读取的错误类型。 |
| `error.message` | `string` | 是 | 示例: "method is not allowed" | 可安全展示的错误消息。 |
| `error.request_id` | `string` | 是 | 格式: uuid；示例: "33ba813e-fc63-4af8-b338-7f7486f82202" | 用于关联请求与服务器日志的 UUID。 |

##### `409 冲突`

摘要、版本、状态、唯一性、锁或恢复流程发生冲突。

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
| `error` | `object` | 是 | - | 可供程序读取的 API 错误详情。 |
| `error.kind` | `string` | 是 | 示例: "configuration_conflict" | 稳定且可供程序读取的错误类型。 |
| `error.message` | `string` | 是 | 示例: "configuration state changed or is busy" | 可安全展示的错误消息。 |
| `error.request_id` | `string` | 是 | 格式: uuid；示例: "33ba813e-fc63-4af8-b338-7f7486f82202" | 用于关联请求与服务器日志的 UUID。 |

##### `422 无法处理`

JSON 格式有效，但违反客户端、IPv4 或配置语义规则。

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
| `error` | `object` | 是 | - | 可供程序读取的 API 错误详情。 |
| `error.kind` | `string` | 是 | 示例: "invalid_client" | 稳定且可供程序读取的错误类型。 |
| `error.message` | `string` | 是 | 示例: "client request is not valid for the current state" | 可安全展示的错误消息。 |
| `error.request_id` | `string` | 是 | 格式: uuid；示例: "33ba813e-fc63-4af8-b338-7f7486f82202" | 用于关联请求与服务器日志的 UUID。 |

##### `500 服务器内部错误`

未分类的内部错误，不会暴露实现细节。

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
| `error` | `object` | 是 | - | 可供程序读取的 API 错误详情。 |
| `error.kind` | `string` | 是 | 示例: "internal_error" | 稳定且可供程序读取的错误类型。 |
| `error.message` | `string` | 是 | 示例: "request could not be completed" | 可安全展示的错误消息。 |
| `error.request_id` | `string` | 是 | 格式: uuid；示例: "33ba813e-fc63-4af8-b338-7f7486f82202" | 用于关联请求与服务器日志的 UUID。 |

##### `503 服务不可用`

认证存储、PKI、OpenVPN、broker、supervisor 或其他必要依赖不可用。

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
| `error` | `object` | 是 | - | 可供程序读取的 API 错误详情。 |
| `error.kind` | `string` | 是 | 示例: "runtime_unavailable" | 稳定且可供程序读取的错误类型。 |
| `error.message` | `string` | 是 | 示例: "OpenVPN runtime is unavailable" | 可安全展示的错误消息。 |
| `error.request_id` | `string` | 是 | 格式: uuid；示例: "33ba813e-fc63-4af8-b338-7f7486f82202" | 用于关联请求与服务器日志的 UUID。 |

### 9. `DELETE /api/v1/clients/{client_id}`

删除本地客户端凭据。请求体必须为空。权威状态中仍会保留不可变的客户端 UUID 墓碑记录。

#### 请求参数

##### Header 参数

| 字段 | 位置 | 类型 | 必填 | 格式 / 示例 / 约束 | 用途说明 |
|---|---|---|---|---|---|
| `Authorization` | `header` | `string` | 是 | Bearer ovpn_v1.<uuid>.<secret> | API 身份认证凭据。Bearer 后填写服务生成的 API Key。 |

##### Path 参数

| 字段 | 位置 | 类型 | 必填 | 格式 / 示例 / 约束 | 用途说明 |
|---|---|---|---|---|---|
| `client_id` | `path` | `string` | 是 | 格式: uuid；示例: "c0d4f871-6ea6-42b3-9e7b-f00cc1ec354e" | 完整、规范且不可变的客户端 UUID；不接受名称或 UUID 前缀。 |

#### 请求示例

```http
DELETE /api/v1/clients/c0d4f871-6ea6-42b3-9e7b-f00cc1ec354e HTTP/1.1
Authorization: Bearer ovpn_v1.<uuid>.<secret>
```

#### 返回

##### `200 成功`

客户端凭据已删除；UUID 墓碑记录仍保留。

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
| `version` | `integer` | 是 | 固定值: 1 | 响应契约 schema 版本。 |
| `operation_id` | `string` | 是 | 格式: uuid；示例: "47260b5b-47dd-4f9b-814f-4803668c5934" | 已提交 journal 操作的 UUID。 |
| `client` | `object` | 是 | - | 权威客户端身份与生命周期状态。 |
| `client.id` | `string` | 是 | 格式: uuid；示例: "c0d4f871-6ea6-42b3-9e7b-f00cc1ec354e" | 该对象的稳定标识符。 |
| `client.name` | `string` | 是 | 格式: ^[A-Za-z0-9][A-Za-z0-9_.-]*$；示例: "alice-notebook" | 稳定且可读的名称。 |
| `client.status` | `"active" \| "revoked" \| "deleted"` | 是 | 可选值: "active" \| "revoked" \| "deleted" | 客户端凭据生命周期状态。 |
| `client.ipv4` | `object` | 是 | - | 当前客户端 IPv4 分配视图。 |
| `client.ipv4.mode` | `"none" \| "static" \| "dynamic"` | 是 | 可选值: "none" \| "static" \| "dynamic" | IPv4 分配模式。 |
| `client.ipv4.address` | `string \| null` | 是 | 格式: ipv4 | IPv4 地址；未分配地址时为 null。 |
| `client.ipv4.state` | `"configured" \| "retained" \| "unavailable"` | 是 | 可选值: "configured" \| "retained" \| "unavailable" | 客户端地址分配的可用状态。 |
| `client.connection` | `string` | 否 | - | 运行时数据可用时的当前连接状态。 |
| `kick_required` | `boolean` | 是 | 示例: false | 是否必须断开已变更客户端的活动会话。 |
| `profile_redistribution_required` | `boolean` | 是 | 示例: false | 是否必须重新分发该客户端生成的配置文件。 |
| `runtime` | `object` | 否 | - | 变更后单个客户端的运行时收敛结果。 |
| `runtime.client_id` | `string` | 否 | 格式: uuid | 不可变的客户端 UUID。 |
| `runtime.status` | `"ok" \| "unavailable"` | 否 | 可选值: "ok" \| "unavailable" | 请求的运行时操作是否完成或不可用。 |
| `runtime.result` | `object` | 否 | - | 请求断开客户端会话的结果。 |
| `runtime.result.version` | `integer` | 否 | 固定值: 1 | 响应契约 schema 版本。 |
| `runtime.result.client_id` | `string` | 否 | 格式: uuid | 不可变的客户端 UUID。 |
| `runtime.result.client_name` | `string` | 否 | - | 与会话关联的可读客户端名称。 |
| `runtime.result.was_connected` | `boolean` | 否 | - | 请求前是否存在匹配会话。 |
| `runtime.result.disconnected` | `boolean` | 否 | - | 是否至少断开了一个匹配会话。 |
| `runtime.result.connections` | `integer` | 否 | 最小值: 0 | 断开请求处理的匹配会话数量。 |

##### `400 错误请求`

path、query、header、媒体类型、请求体或 JSON 格式错误。

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
| `error` | `object` | 是 | - | 可供程序读取的 API 错误详情。 |
| `error.kind` | `string` | 是 | 示例: "invalid_json" | 稳定且可供程序读取的错误类型。 |
| `error.message` | `string` | 是 | 示例: "request body is invalid" | 可安全展示的错误消息。 |
| `error.request_id` | `string` | 是 | 格式: uuid；示例: "33ba813e-fc63-4af8-b338-7f7486f82202" | 用于关联请求与服务器日志的 UUID。 |

##### `401 未认证`

Bearer API key 缺失、格式错误、未知或已删除。

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
| `error` | `object` | 是 | - | 可供程序读取的 API 错误详情。 |
| `error.kind` | `string` | 是 | 示例: "unauthenticated" | 稳定且可供程序读取的错误类型。 |
| `error.message` | `string` | 是 | 示例: "API key is missing or invalid" | 可安全展示的错误消息。 |
| `error.request_id` | `string` | 是 | 格式: uuid；示例: "33ba813e-fc63-4af8-b338-7f7486f82202" | 用于关联请求与服务器日志的 UUID。 |

##### `404 未找到`

资源或客户端不存在。

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
| `error` | `object` | 是 | - | 可供程序读取的 API 错误详情。 |
| `error.kind` | `string` | 是 | 示例: "client_not_found" | 稳定且可供程序读取的错误类型。 |
| `error.message` | `string` | 是 | 示例: "client was not found" | 可安全展示的错误消息。 |
| `error.request_id` | `string` | 是 | 格式: uuid；示例: "33ba813e-fc63-4af8-b338-7f7486f82202" | 用于关联请求与服务器日志的 UUID。 |

##### `405 方法不允许`

不接受该 HTTP 方法；请检查 Allow header。

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
| `error` | `object` | 是 | - | 可供程序读取的 API 错误详情。 |
| `error.kind` | `string` | 是 | 示例: "method_not_allowed" | 稳定且可供程序读取的错误类型。 |
| `error.message` | `string` | 是 | 示例: "method is not allowed" | 可安全展示的错误消息。 |
| `error.request_id` | `string` | 是 | 格式: uuid；示例: "33ba813e-fc63-4af8-b338-7f7486f82202" | 用于关联请求与服务器日志的 UUID。 |

##### `409 冲突`

摘要、版本、状态、唯一性、锁或恢复流程发生冲突。

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
| `error` | `object` | 是 | - | 可供程序读取的 API 错误详情。 |
| `error.kind` | `string` | 是 | 示例: "configuration_conflict" | 稳定且可供程序读取的错误类型。 |
| `error.message` | `string` | 是 | 示例: "configuration state changed or is busy" | 可安全展示的错误消息。 |
| `error.request_id` | `string` | 是 | 格式: uuid；示例: "33ba813e-fc63-4af8-b338-7f7486f82202" | 用于关联请求与服务器日志的 UUID。 |

##### `422 无法处理`

JSON 格式有效，但违反客户端、IPv4 或配置语义规则。

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
| `error` | `object` | 是 | - | 可供程序读取的 API 错误详情。 |
| `error.kind` | `string` | 是 | 示例: "invalid_client" | 稳定且可供程序读取的错误类型。 |
| `error.message` | `string` | 是 | 示例: "client request is not valid for the current state" | 可安全展示的错误消息。 |
| `error.request_id` | `string` | 是 | 格式: uuid；示例: "33ba813e-fc63-4af8-b338-7f7486f82202" | 用于关联请求与服务器日志的 UUID。 |

##### `500 服务器内部错误`

未分类的内部错误，不会暴露实现细节。

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
| `error` | `object` | 是 | - | 可供程序读取的 API 错误详情。 |
| `error.kind` | `string` | 是 | 示例: "internal_error" | 稳定且可供程序读取的错误类型。 |
| `error.message` | `string` | 是 | 示例: "request could not be completed" | 可安全展示的错误消息。 |
| `error.request_id` | `string` | 是 | 格式: uuid；示例: "33ba813e-fc63-4af8-b338-7f7486f82202" | 用于关联请求与服务器日志的 UUID。 |

##### `503 服务不可用`

认证存储、PKI、OpenVPN、broker、supervisor 或其他必要依赖不可用。

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
| `error` | `object` | 是 | - | 可供程序读取的 API 错误详情。 |
| `error.kind` | `string` | 是 | 示例: "runtime_unavailable" | 稳定且可供程序读取的错误类型。 |
| `error.message` | `string` | 是 | 示例: "OpenVPN runtime is unavailable" | 可安全展示的错误消息。 |
| `error.request_id` | `string` | 是 | 格式: uuid；示例: "33ba813e-fc63-4af8-b338-7f7486f82202" | 用于关联请求与服务器日志的 UUID。 |

### 10. `GET /api/v1/clients/{client_id}/profile`

下载 OpenVPN 客户端配置文件。响应包含私钥，必须按凭据进行安全处理。

#### 请求参数

##### Header 参数

| 字段 | 位置 | 类型 | 必填 | 格式 / 示例 / 约束 | 用途说明 |
|---|---|---|---|---|---|
| `Authorization` | `header` | `string` | 是 | Bearer ovpn_v1.<uuid>.<secret> | API 身份认证凭据。Bearer 后填写服务生成的 API Key。 |

##### Path 参数

| 字段 | 位置 | 类型 | 必填 | 格式 / 示例 / 约束 | 用途说明 |
|---|---|---|---|---|---|
| `client_id` | `path` | `string` | 是 | 格式: uuid；示例: "c0d4f871-6ea6-42b3-9e7b-f00cc1ec354e" | 完整、规范且不可变的客户端 UUID；不接受名称或 UUID 前缀。 |

#### 请求示例

```http
GET /api/v1/clients/c0d4f871-6ea6-42b3-9e7b-f00cc1ec354e/profile HTTP/1.1
Authorization: Bearer ovpn_v1.<uuid>.<secret>
```

#### 返回

##### `200 成功`

包含私密凭据的 OpenVPN 配置文件。

内容类型：`application/x-openvpn-profile`

返回示例：

```text
string
```

返回字段：

| 字段 | 类型 | 必填 | 格式 / 示例 / 约束 | 用途说明 |
|---|---|---|---|---|
| `response` | `string` | 是 | 格式: binary；示例: "string" | 包含私密凭据的 OpenVPN 配置文件。 |

##### `400 错误请求`

path、query、header、媒体类型、请求体或 JSON 格式错误。

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
| `error` | `object` | 是 | - | 可供程序读取的 API 错误详情。 |
| `error.kind` | `string` | 是 | 示例: "invalid_json" | 稳定且可供程序读取的错误类型。 |
| `error.message` | `string` | 是 | 示例: "request body is invalid" | 可安全展示的错误消息。 |
| `error.request_id` | `string` | 是 | 格式: uuid；示例: "33ba813e-fc63-4af8-b338-7f7486f82202" | 用于关联请求与服务器日志的 UUID。 |

##### `401 未认证`

Bearer API key 缺失、格式错误、未知或已删除。

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
| `error` | `object` | 是 | - | 可供程序读取的 API 错误详情。 |
| `error.kind` | `string` | 是 | 示例: "unauthenticated" | 稳定且可供程序读取的错误类型。 |
| `error.message` | `string` | 是 | 示例: "API key is missing or invalid" | 可安全展示的错误消息。 |
| `error.request_id` | `string` | 是 | 格式: uuid；示例: "33ba813e-fc63-4af8-b338-7f7486f82202" | 用于关联请求与服务器日志的 UUID。 |

##### `404 未找到`

资源或客户端不存在。

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
| `error` | `object` | 是 | - | 可供程序读取的 API 错误详情。 |
| `error.kind` | `string` | 是 | 示例: "client_not_found" | 稳定且可供程序读取的错误类型。 |
| `error.message` | `string` | 是 | 示例: "client was not found" | 可安全展示的错误消息。 |
| `error.request_id` | `string` | 是 | 格式: uuid；示例: "33ba813e-fc63-4af8-b338-7f7486f82202" | 用于关联请求与服务器日志的 UUID。 |

##### `405 方法不允许`

不接受该 HTTP 方法；请检查 Allow header。

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
| `error` | `object` | 是 | - | 可供程序读取的 API 错误详情。 |
| `error.kind` | `string` | 是 | 示例: "method_not_allowed" | 稳定且可供程序读取的错误类型。 |
| `error.message` | `string` | 是 | 示例: "method is not allowed" | 可安全展示的错误消息。 |
| `error.request_id` | `string` | 是 | 格式: uuid；示例: "33ba813e-fc63-4af8-b338-7f7486f82202" | 用于关联请求与服务器日志的 UUID。 |

##### `409 冲突`

摘要、版本、状态、唯一性、锁或恢复流程发生冲突。

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
| `error` | `object` | 是 | - | 可供程序读取的 API 错误详情。 |
| `error.kind` | `string` | 是 | 示例: "configuration_conflict" | 稳定且可供程序读取的错误类型。 |
| `error.message` | `string` | 是 | 示例: "configuration state changed or is busy" | 可安全展示的错误消息。 |
| `error.request_id` | `string` | 是 | 格式: uuid；示例: "33ba813e-fc63-4af8-b338-7f7486f82202" | 用于关联请求与服务器日志的 UUID。 |

##### `422 无法处理`

JSON 格式有效，但违反客户端、IPv4 或配置语义规则。

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
| `error` | `object` | 是 | - | 可供程序读取的 API 错误详情。 |
| `error.kind` | `string` | 是 | 示例: "invalid_client" | 稳定且可供程序读取的错误类型。 |
| `error.message` | `string` | 是 | 示例: "client request is not valid for the current state" | 可安全展示的错误消息。 |
| `error.request_id` | `string` | 是 | 格式: uuid；示例: "33ba813e-fc63-4af8-b338-7f7486f82202" | 用于关联请求与服务器日志的 UUID。 |

##### `500 服务器内部错误`

未分类的内部错误，不会暴露实现细节。

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
| `error` | `object` | 是 | - | 可供程序读取的 API 错误详情。 |
| `error.kind` | `string` | 是 | 示例: "internal_error" | 稳定且可供程序读取的错误类型。 |
| `error.message` | `string` | 是 | 示例: "request could not be completed" | 可安全展示的错误消息。 |
| `error.request_id` | `string` | 是 | 格式: uuid；示例: "33ba813e-fc63-4af8-b338-7f7486f82202" | 用于关联请求与服务器日志的 UUID。 |

##### `503 服务不可用`

认证存储、PKI、OpenVPN、broker、supervisor 或其他必要依赖不可用。

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
| `error` | `object` | 是 | - | 可供程序读取的 API 错误详情。 |
| `error.kind` | `string` | 是 | 示例: "runtime_unavailable" | 稳定且可供程序读取的错误类型。 |
| `error.message` | `string` | 是 | 示例: "OpenVPN runtime is unavailable" | 可安全展示的错误消息。 |
| `error.request_id` | `string` | 是 | 格式: uuid；示例: "33ba813e-fc63-4af8-b338-7f7486f82202" | 用于关联请求与服务器日志的 UUID。 |

### 11. `POST /api/v1/clients/{client_id}/revoke`

吊销客户端凭据

#### 请求参数

##### Header 参数

| 字段 | 位置 | 类型 | 必填 | 格式 / 示例 / 约束 | 用途说明 |
|---|---|---|---|---|---|
| `Authorization` | `header` | `string` | 是 | Bearer ovpn_v1.<uuid>.<secret> | API 身份认证凭据。Bearer 后填写服务生成的 API Key。 |
| `Content-Type` | `header` | `string` | 是 | application/json | 声明请求体使用 JSON 格式。 |

##### Path 参数

| 字段 | 位置 | 类型 | 必填 | 格式 / 示例 / 约束 | 用途说明 |
|---|---|---|---|---|---|
| `client_id` | `path` | `string` | 是 | 格式: uuid；示例: "c0d4f871-6ea6-42b3-9e7b-f00cc1ec354e" | 完整、规范且不可变的客户端 UUID；不接受名称或 UUID 前缀。 |

##### JSON Body 字段

| 字段 | 位置 | 类型 | 必填 | 格式 / 示例 / 约束 | 用途说明 |
|---|---|---|---|---|---|
| `release_ipv4` | `body` | `boolean` | 是 | 示例: false | true 表示释放地址分配；false 表示保留，以便之后释放或重新签发。 |

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

##### `200 成功`

已提交客户端变更；需要时会单独报告运行时收敛结果。

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
| `version` | `integer` | 是 | 固定值: 1 | 响应契约 schema 版本。 |
| `operation_id` | `string` | 是 | 格式: uuid；示例: "47260b5b-47dd-4f9b-814f-4803668c5934" | 已提交 journal 操作的 UUID。 |
| `client` | `object` | 是 | - | 权威客户端身份与生命周期状态。 |
| `client.id` | `string` | 是 | 格式: uuid；示例: "c0d4f871-6ea6-42b3-9e7b-f00cc1ec354e" | 该对象的稳定标识符。 |
| `client.name` | `string` | 是 | 格式: ^[A-Za-z0-9][A-Za-z0-9_.-]*$；示例: "alice-laptop" | 稳定且可读的名称。 |
| `client.status` | `"active" \| "revoked" \| "deleted"` | 是 | 可选值: "active" \| "revoked" \| "deleted" | 客户端凭据生命周期状态。 |
| `client.ipv4` | `object` | 是 | - | 当前客户端 IPv4 分配视图。 |
| `client.ipv4.mode` | `"none" \| "static" \| "dynamic"` | 是 | 可选值: "none" \| "static" \| "dynamic" | IPv4 分配模式。 |
| `client.ipv4.address` | `string \| null` | 是 | 格式: ipv4；示例: "10.42.0.30" | IPv4 地址；未分配地址时为 null。 |
| `client.ipv4.state` | `"configured" \| "retained" \| "unavailable"` | 是 | 可选值: "configured" \| "retained" \| "unavailable" | 客户端地址分配的可用状态。 |
| `client.connection` | `string` | 否 | - | 运行时数据可用时的当前连接状态。 |
| `kick_required` | `boolean` | 是 | 示例: true | 是否必须断开已变更客户端的活动会话。 |
| `profile_redistribution_required` | `boolean` | 是 | 示例: false | 是否必须重新分发该客户端生成的配置文件。 |
| `runtime` | `object` | 否 | - | 变更后单个客户端的运行时收敛结果。 |
| `runtime.client_id` | `string` | 否 | 格式: uuid | 不可变的客户端 UUID。 |
| `runtime.status` | `"ok" \| "unavailable"` | 否 | 可选值: "ok" \| "unavailable" | 请求的运行时操作是否完成或不可用。 |
| `runtime.result` | `object` | 否 | - | 请求断开客户端会话的结果。 |
| `runtime.result.version` | `integer` | 否 | 固定值: 1 | 响应契约 schema 版本。 |
| `runtime.result.client_id` | `string` | 否 | 格式: uuid；示例: "c0d4f871-6ea6-42b3-9e7b-f00cc1ec354e" | 不可变的客户端 UUID。 |
| `runtime.result.client_name` | `string` | 否 | 示例: "alice-laptop" | 与会话关联的可读客户端名称。 |
| `runtime.result.was_connected` | `boolean` | 否 | 示例: true | 请求前是否存在匹配会话。 |
| `runtime.result.disconnected` | `boolean` | 否 | 示例: true | 是否至少断开了一个匹配会话。 |
| `runtime.result.connections` | `integer` | 否 | 最小值: 0；示例: 1 | 断开请求处理的匹配会话数量。 |

##### `400 错误请求`

path、query、header、媒体类型、请求体或 JSON 格式错误。

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
| `error` | `object` | 是 | - | 可供程序读取的 API 错误详情。 |
| `error.kind` | `string` | 是 | 示例: "invalid_json" | 稳定且可供程序读取的错误类型。 |
| `error.message` | `string` | 是 | 示例: "request body is invalid" | 可安全展示的错误消息。 |
| `error.request_id` | `string` | 是 | 格式: uuid；示例: "33ba813e-fc63-4af8-b338-7f7486f82202" | 用于关联请求与服务器日志的 UUID。 |

##### `401 未认证`

Bearer API key 缺失、格式错误、未知或已删除。

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
| `error` | `object` | 是 | - | 可供程序读取的 API 错误详情。 |
| `error.kind` | `string` | 是 | 示例: "unauthenticated" | 稳定且可供程序读取的错误类型。 |
| `error.message` | `string` | 是 | 示例: "API key is missing or invalid" | 可安全展示的错误消息。 |
| `error.request_id` | `string` | 是 | 格式: uuid；示例: "33ba813e-fc63-4af8-b338-7f7486f82202" | 用于关联请求与服务器日志的 UUID。 |

##### `404 未找到`

资源或客户端不存在。

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
| `error` | `object` | 是 | - | 可供程序读取的 API 错误详情。 |
| `error.kind` | `string` | 是 | 示例: "client_not_found" | 稳定且可供程序读取的错误类型。 |
| `error.message` | `string` | 是 | 示例: "client was not found" | 可安全展示的错误消息。 |
| `error.request_id` | `string` | 是 | 格式: uuid；示例: "33ba813e-fc63-4af8-b338-7f7486f82202" | 用于关联请求与服务器日志的 UUID。 |

##### `405 方法不允许`

不接受该 HTTP 方法；请检查 Allow header。

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
| `error` | `object` | 是 | - | 可供程序读取的 API 错误详情。 |
| `error.kind` | `string` | 是 | 示例: "method_not_allowed" | 稳定且可供程序读取的错误类型。 |
| `error.message` | `string` | 是 | 示例: "method is not allowed" | 可安全展示的错误消息。 |
| `error.request_id` | `string` | 是 | 格式: uuid；示例: "33ba813e-fc63-4af8-b338-7f7486f82202" | 用于关联请求与服务器日志的 UUID。 |

##### `409 冲突`

摘要、版本、状态、唯一性、锁或恢复流程发生冲突。

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
| `error` | `object` | 是 | - | 可供程序读取的 API 错误详情。 |
| `error.kind` | `string` | 是 | 示例: "configuration_conflict" | 稳定且可供程序读取的错误类型。 |
| `error.message` | `string` | 是 | 示例: "configuration state changed or is busy" | 可安全展示的错误消息。 |
| `error.request_id` | `string` | 是 | 格式: uuid；示例: "33ba813e-fc63-4af8-b338-7f7486f82202" | 用于关联请求与服务器日志的 UUID。 |

##### `422 无法处理`

JSON 格式有效，但违反客户端、IPv4 或配置语义规则。

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
| `error` | `object` | 是 | - | 可供程序读取的 API 错误详情。 |
| `error.kind` | `string` | 是 | 示例: "invalid_client" | 稳定且可供程序读取的错误类型。 |
| `error.message` | `string` | 是 | 示例: "client request is not valid for the current state" | 可安全展示的错误消息。 |
| `error.request_id` | `string` | 是 | 格式: uuid；示例: "33ba813e-fc63-4af8-b338-7f7486f82202" | 用于关联请求与服务器日志的 UUID。 |

##### `500 服务器内部错误`

未分类的内部错误，不会暴露实现细节。

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
| `error` | `object` | 是 | - | 可供程序读取的 API 错误详情。 |
| `error.kind` | `string` | 是 | 示例: "internal_error" | 稳定且可供程序读取的错误类型。 |
| `error.message` | `string` | 是 | 示例: "request could not be completed" | 可安全展示的错误消息。 |
| `error.request_id` | `string` | 是 | 格式: uuid；示例: "33ba813e-fc63-4af8-b338-7f7486f82202" | 用于关联请求与服务器日志的 UUID。 |

##### `503 服务不可用`

认证存储、PKI、OpenVPN、broker、supervisor 或其他必要依赖不可用。

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
| `error` | `object` | 是 | - | 可供程序读取的 API 错误详情。 |
| `error.kind` | `string` | 是 | 示例: "runtime_unavailable" | 稳定且可供程序读取的错误类型。 |
| `error.message` | `string` | 是 | 示例: "OpenVPN runtime is unavailable" | 可安全展示的错误消息。 |
| `error.request_id` | `string` | 是 | 格式: uuid；示例: "33ba813e-fc63-4af8-b338-7f7486f82202" | 用于关联请求与服务器日志的 UUID。 |

### 12. `POST /api/v1/clients/{client_id}/reissue`

重新签发客户端凭据和配置文件

#### 请求参数

##### Header 参数

| 字段 | 位置 | 类型 | 必填 | 格式 / 示例 / 约束 | 用途说明 |
|---|---|---|---|---|---|
| `Authorization` | `header` | `string` | 是 | Bearer ovpn_v1.<uuid>.<secret> | API 身份认证凭据。Bearer 后填写服务生成的 API Key。 |
| `Content-Type` | `header` | `string` | 是 | application/json | 声明请求体使用 JSON 格式。 |

##### Path 参数

| 字段 | 位置 | 类型 | 必填 | 格式 / 示例 / 约束 | 用途说明 |
|---|---|---|---|---|---|
| `client_id` | `path` | `string` | 是 | 格式: uuid；示例: "c0d4f871-6ea6-42b3-9e7b-f00cc1ec354e" | 完整、规范且不可变的客户端 UUID；不接受名称或 UUID 前缀。 |

##### JSON Body 字段

| 字段 | 位置 | 类型 | 必填 | 格式 / 示例 / 约束 | 用途说明 |
|---|---|---|---|---|---|
| `ipv4` | `body` | `string` | 是 | 示例: "dynamic" | auto、dynamic 或有效的静态 IPv4 地址。 |

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

##### `200 成功`

客户端凭据和配置文件已重新签发。

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
| `version` | `integer` | 是 | 固定值: 1 | 响应契约 schema 版本。 |
| `operation_id` | `string` | 是 | 格式: uuid；示例: "47260b5b-47dd-4f9b-814f-4803668c5934" | 已提交 journal 操作的 UUID。 |
| `client` | `object` | 是 | - | 权威客户端身份与生命周期状态。 |
| `client.id` | `string` | 是 | 格式: uuid；示例: "c0d4f871-6ea6-42b3-9e7b-f00cc1ec354e" | 该对象的稳定标识符。 |
| `client.name` | `string` | 是 | 格式: ^[A-Za-z0-9][A-Za-z0-9_.-]*$；示例: "alice-notebook" | 稳定且可读的名称。 |
| `client.status` | `"active" \| "revoked" \| "deleted"` | 是 | 可选值: "active" \| "revoked" \| "deleted" | 客户端凭据生命周期状态。 |
| `client.ipv4` | `object` | 是 | - | 当前客户端 IPv4 分配视图。 |
| `client.ipv4.mode` | `"none" \| "static" \| "dynamic"` | 是 | 可选值: "none" \| "static" \| "dynamic" | IPv4 分配模式。 |
| `client.ipv4.address` | `string \| null` | 是 | 格式: ipv4 | IPv4 地址；未分配地址时为 null。 |
| `client.ipv4.state` | `"configured" \| "retained" \| "unavailable"` | 是 | 可选值: "configured" \| "retained" \| "unavailable" | 客户端地址分配的可用状态。 |
| `client.connection` | `string` | 否 | - | 运行时数据可用时的当前连接状态。 |
| `kick_required` | `boolean` | 是 | 示例: true | 是否必须断开已变更客户端的活动会话。 |
| `profile_redistribution_required` | `boolean` | 是 | 示例: true | 是否必须重新分发该客户端生成的配置文件。 |
| `runtime` | `object` | 否 | - | 变更后单个客户端的运行时收敛结果。 |
| `runtime.client_id` | `string` | 否 | 格式: uuid | 不可变的客户端 UUID。 |
| `runtime.status` | `"ok" \| "unavailable"` | 否 | 可选值: "ok" \| "unavailable" | 请求的运行时操作是否完成或不可用。 |
| `runtime.result` | `object` | 否 | - | 请求断开客户端会话的结果。 |
| `runtime.result.version` | `integer` | 否 | 固定值: 1 | 响应契约 schema 版本。 |
| `runtime.result.client_id` | `string` | 否 | 格式: uuid | 不可变的客户端 UUID。 |
| `runtime.result.client_name` | `string` | 否 | - | 与会话关联的可读客户端名称。 |
| `runtime.result.was_connected` | `boolean` | 否 | - | 请求前是否存在匹配会话。 |
| `runtime.result.disconnected` | `boolean` | 否 | - | 是否至少断开了一个匹配会话。 |
| `runtime.result.connections` | `integer` | 否 | 最小值: 0 | 断开请求处理的匹配会话数量。 |

##### `400 错误请求`

path、query、header、媒体类型、请求体或 JSON 格式错误。

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
| `error` | `object` | 是 | - | 可供程序读取的 API 错误详情。 |
| `error.kind` | `string` | 是 | 示例: "invalid_json" | 稳定且可供程序读取的错误类型。 |
| `error.message` | `string` | 是 | 示例: "request body is invalid" | 可安全展示的错误消息。 |
| `error.request_id` | `string` | 是 | 格式: uuid；示例: "33ba813e-fc63-4af8-b338-7f7486f82202" | 用于关联请求与服务器日志的 UUID。 |

##### `401 未认证`

Bearer API key 缺失、格式错误、未知或已删除。

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
| `error` | `object` | 是 | - | 可供程序读取的 API 错误详情。 |
| `error.kind` | `string` | 是 | 示例: "unauthenticated" | 稳定且可供程序读取的错误类型。 |
| `error.message` | `string` | 是 | 示例: "API key is missing or invalid" | 可安全展示的错误消息。 |
| `error.request_id` | `string` | 是 | 格式: uuid；示例: "33ba813e-fc63-4af8-b338-7f7486f82202" | 用于关联请求与服务器日志的 UUID。 |

##### `404 未找到`

资源或客户端不存在。

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
| `error` | `object` | 是 | - | 可供程序读取的 API 错误详情。 |
| `error.kind` | `string` | 是 | 示例: "client_not_found" | 稳定且可供程序读取的错误类型。 |
| `error.message` | `string` | 是 | 示例: "client was not found" | 可安全展示的错误消息。 |
| `error.request_id` | `string` | 是 | 格式: uuid；示例: "33ba813e-fc63-4af8-b338-7f7486f82202" | 用于关联请求与服务器日志的 UUID。 |

##### `405 方法不允许`

不接受该 HTTP 方法；请检查 Allow header。

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
| `error` | `object` | 是 | - | 可供程序读取的 API 错误详情。 |
| `error.kind` | `string` | 是 | 示例: "method_not_allowed" | 稳定且可供程序读取的错误类型。 |
| `error.message` | `string` | 是 | 示例: "method is not allowed" | 可安全展示的错误消息。 |
| `error.request_id` | `string` | 是 | 格式: uuid；示例: "33ba813e-fc63-4af8-b338-7f7486f82202" | 用于关联请求与服务器日志的 UUID。 |

##### `409 冲突`

摘要、版本、状态、唯一性、锁或恢复流程发生冲突。

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
| `error` | `object` | 是 | - | 可供程序读取的 API 错误详情。 |
| `error.kind` | `string` | 是 | 示例: "configuration_conflict" | 稳定且可供程序读取的错误类型。 |
| `error.message` | `string` | 是 | 示例: "configuration state changed or is busy" | 可安全展示的错误消息。 |
| `error.request_id` | `string` | 是 | 格式: uuid；示例: "33ba813e-fc63-4af8-b338-7f7486f82202" | 用于关联请求与服务器日志的 UUID。 |

##### `422 无法处理`

JSON 格式有效，但违反客户端、IPv4 或配置语义规则。

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
| `error` | `object` | 是 | - | 可供程序读取的 API 错误详情。 |
| `error.kind` | `string` | 是 | 示例: "invalid_client" | 稳定且可供程序读取的错误类型。 |
| `error.message` | `string` | 是 | 示例: "client request is not valid for the current state" | 可安全展示的错误消息。 |
| `error.request_id` | `string` | 是 | 格式: uuid；示例: "33ba813e-fc63-4af8-b338-7f7486f82202" | 用于关联请求与服务器日志的 UUID。 |

##### `500 服务器内部错误`

未分类的内部错误，不会暴露实现细节。

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
| `error` | `object` | 是 | - | 可供程序读取的 API 错误详情。 |
| `error.kind` | `string` | 是 | 示例: "internal_error" | 稳定且可供程序读取的错误类型。 |
| `error.message` | `string` | 是 | 示例: "request could not be completed" | 可安全展示的错误消息。 |
| `error.request_id` | `string` | 是 | 格式: uuid；示例: "33ba813e-fc63-4af8-b338-7f7486f82202" | 用于关联请求与服务器日志的 UUID。 |

##### `503 服务不可用`

认证存储、PKI、OpenVPN、broker、supervisor 或其他必要依赖不可用。

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
| `error` | `object` | 是 | - | 可供程序读取的 API 错误详情。 |
| `error.kind` | `string` | 是 | 示例: "runtime_unavailable" | 稳定且可供程序读取的错误类型。 |
| `error.message` | `string` | 是 | 示例: "OpenVPN runtime is unavailable" | 可安全展示的错误消息。 |
| `error.request_id` | `string` | 是 | 格式: uuid；示例: "33ba813e-fc63-4af8-b338-7f7486f82202" | 用于关联请求与服务器日志的 UUID。 |

### 13. `PUT /api/v1/clients/{client_id}/ipv4`

设置客户端 IPv4 分配方式。auto 或 dynamic 模式不能提供 address；static 模式必须提供已配置静态区域内的地址。

#### 请求参数

##### Header 参数

| 字段 | 位置 | 类型 | 必填 | 格式 / 示例 / 约束 | 用途说明 |
|---|---|---|---|---|---|
| `Authorization` | `header` | `string` | 是 | Bearer ovpn_v1.<uuid>.<secret> | API 身份认证凭据。Bearer 后填写服务生成的 API Key。 |
| `Content-Type` | `header` | `string` | 是 | application/json | 声明请求体使用 JSON 格式。 |

##### Path 参数

| 字段 | 位置 | 类型 | 必填 | 格式 / 示例 / 约束 | 用途说明 |
|---|---|---|---|---|---|
| `client_id` | `path` | `string` | 是 | 格式: uuid；示例: "c0d4f871-6ea6-42b3-9e7b-f00cc1ec354e" | 完整、规范且不可变的客户端 UUID；不接受名称或 UUID 前缀。 |

##### JSON Body 字段

| 字段 | 位置 | 类型 | 必填 | 格式 / 示例 / 约束 | 用途说明 |
|---|---|---|---|---|---|
| `mode` | `body` | `"auto" \| "dynamic" \| "static"` | 是 | 可选值: "auto" \| "dynamic" \| "static" | 地址选择模式：自动分配、动态分配或指定静态地址。 |
| `address` | `body` | `string` | 否 | 格式: ipv4 | 静态 IPv4 地址。仅 static 模式必填，auto/dynamic 模式禁止提供。 |

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

##### `200 成功`

已提交地址变更以及各客户端的运行时结果。

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
| `version` | `integer` | 是 | 固定值: 1 | 响应契约 schema 版本。 |
| `operation_id` | `string` | 是 | 格式: uuid；示例: "d41dbfce-b40e-4672-8a62-cb401f8c099c" | 已提交 journal 操作的 UUID。 |
| `clients` | `object[]` | 是 | 示例: [{"id":"c0d4f871-6ea6-42b3-9e7b-f00cc1ec354e","name":"alice-laptop","status":"active","ipv4":{"mode":"static","address":"10.42.0.30","state":"configured"}}] | 该响应包含的客户端。 |
| `clients[].id` | `string` | 是 | 格式: uuid；示例: "c0d4f871-6ea6-42b3-9e7b-f00cc1ec354e" | 该对象的稳定标识符。 |
| `clients[].name` | `string` | 是 | 格式: ^[A-Za-z0-9][A-Za-z0-9_.-]*$；示例: "alice-laptop" | 稳定且可读的名称。 |
| `clients[].status` | `"active" \| "revoked" \| "deleted"` | 是 | 可选值: "active" \| "revoked" \| "deleted" | 客户端凭据生命周期状态。 |
| `clients[].ipv4` | `object` | 是 | - | 当前客户端 IPv4 分配视图。 |
| `clients[].ipv4.mode` | `"none" \| "static" \| "dynamic"` | 是 | 可选值: "none" \| "static" \| "dynamic" | IPv4 分配模式。 |
| `clients[].ipv4.address` | `string \| null` | 是 | 格式: ipv4；示例: "10.42.0.30" | IPv4 地址；未分配地址时为 null。 |
| `clients[].ipv4.state` | `"configured" \| "retained" \| "unavailable"` | 是 | 可选值: "configured" \| "retained" \| "unavailable" | 客户端地址分配的可用状态。 |
| `clients[].connection` | `string` | 否 | - | 运行时数据可用时的当前连接状态。 |
| `kick_required` | `string[]` | 是 | 示例: ["c0d4f871-6ea6-42b3-9e7b-f00cc1ec354e"] | 必须断开活动会话的客户端。 |
| `runtime` | `object[]` | 是 | 示例: [{"client_id":"c0d4f871-6ea6-42b3-9e7b-f00cc1ec354e","status":"ok","result":{"version":1,"client_id":"c0d4f871-6ea6-42b3-9e7b-f00cc1ec354e","client_name":"alice-laptop","was_connected":false,"disconnected":false,"connections":0}}] | 尝试实时操作时的运行时收敛结果。 |
| `runtime[].client_id` | `string` | 否 | 格式: uuid；示例: "c0d4f871-6ea6-42b3-9e7b-f00cc1ec354e" | 不可变的客户端 UUID。 |
| `runtime[].status` | `"ok" \| "unavailable"` | 是 | 可选值: "ok" \| "unavailable" | 请求的运行时操作是否完成或不可用。 |
| `runtime[].result` | `object` | 否 | - | 请求断开客户端会话的结果。 |
| `runtime[].result.version` | `integer` | 否 | 固定值: 1 | 响应契约 schema 版本。 |
| `runtime[].result.client_id` | `string` | 否 | 格式: uuid；示例: "c0d4f871-6ea6-42b3-9e7b-f00cc1ec354e" | 不可变的客户端 UUID。 |
| `runtime[].result.client_name` | `string` | 否 | 示例: "alice-laptop" | 与会话关联的可读客户端名称。 |
| `runtime[].result.was_connected` | `boolean` | 否 | 示例: false | 请求前是否存在匹配会话。 |
| `runtime[].result.disconnected` | `boolean` | 否 | 示例: false | 是否至少断开了一个匹配会话。 |
| `runtime[].result.connections` | `integer` | 否 | 最小值: 0；示例: 0 | 断开请求处理的匹配会话数量。 |

##### `400 错误请求`

path、query、header、媒体类型、请求体或 JSON 格式错误。

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
| `error` | `object` | 是 | - | 可供程序读取的 API 错误详情。 |
| `error.kind` | `string` | 是 | 示例: "invalid_json" | 稳定且可供程序读取的错误类型。 |
| `error.message` | `string` | 是 | 示例: "request body is invalid" | 可安全展示的错误消息。 |
| `error.request_id` | `string` | 是 | 格式: uuid；示例: "33ba813e-fc63-4af8-b338-7f7486f82202" | 用于关联请求与服务器日志的 UUID。 |

##### `401 未认证`

Bearer API key 缺失、格式错误、未知或已删除。

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
| `error` | `object` | 是 | - | 可供程序读取的 API 错误详情。 |
| `error.kind` | `string` | 是 | 示例: "unauthenticated" | 稳定且可供程序读取的错误类型。 |
| `error.message` | `string` | 是 | 示例: "API key is missing or invalid" | 可安全展示的错误消息。 |
| `error.request_id` | `string` | 是 | 格式: uuid；示例: "33ba813e-fc63-4af8-b338-7f7486f82202" | 用于关联请求与服务器日志的 UUID。 |

##### `404 未找到`

资源或客户端不存在。

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
| `error` | `object` | 是 | - | 可供程序读取的 API 错误详情。 |
| `error.kind` | `string` | 是 | 示例: "client_not_found" | 稳定且可供程序读取的错误类型。 |
| `error.message` | `string` | 是 | 示例: "client was not found" | 可安全展示的错误消息。 |
| `error.request_id` | `string` | 是 | 格式: uuid；示例: "33ba813e-fc63-4af8-b338-7f7486f82202" | 用于关联请求与服务器日志的 UUID。 |

##### `405 方法不允许`

不接受该 HTTP 方法；请检查 Allow header。

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
| `error` | `object` | 是 | - | 可供程序读取的 API 错误详情。 |
| `error.kind` | `string` | 是 | 示例: "method_not_allowed" | 稳定且可供程序读取的错误类型。 |
| `error.message` | `string` | 是 | 示例: "method is not allowed" | 可安全展示的错误消息。 |
| `error.request_id` | `string` | 是 | 格式: uuid；示例: "33ba813e-fc63-4af8-b338-7f7486f82202" | 用于关联请求与服务器日志的 UUID。 |

##### `409 冲突`

摘要、版本、状态、唯一性、锁或恢复流程发生冲突。

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
| `error` | `object` | 是 | - | 可供程序读取的 API 错误详情。 |
| `error.kind` | `string` | 是 | 示例: "configuration_conflict" | 稳定且可供程序读取的错误类型。 |
| `error.message` | `string` | 是 | 示例: "configuration state changed or is busy" | 可安全展示的错误消息。 |
| `error.request_id` | `string` | 是 | 格式: uuid；示例: "33ba813e-fc63-4af8-b338-7f7486f82202" | 用于关联请求与服务器日志的 UUID。 |

##### `422 无法处理`

JSON 格式有效，但违反客户端、IPv4 或配置语义规则。

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
| `error` | `object` | 是 | - | 可供程序读取的 API 错误详情。 |
| `error.kind` | `string` | 是 | 示例: "invalid_client" | 稳定且可供程序读取的错误类型。 |
| `error.message` | `string` | 是 | 示例: "client request is not valid for the current state" | 可安全展示的错误消息。 |
| `error.request_id` | `string` | 是 | 格式: uuid；示例: "33ba813e-fc63-4af8-b338-7f7486f82202" | 用于关联请求与服务器日志的 UUID。 |

##### `500 服务器内部错误`

未分类的内部错误，不会暴露实现细节。

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
| `error` | `object` | 是 | - | 可供程序读取的 API 错误详情。 |
| `error.kind` | `string` | 是 | 示例: "internal_error" | 稳定且可供程序读取的错误类型。 |
| `error.message` | `string` | 是 | 示例: "request could not be completed" | 可安全展示的错误消息。 |
| `error.request_id` | `string` | 是 | 格式: uuid；示例: "33ba813e-fc63-4af8-b338-7f7486f82202" | 用于关联请求与服务器日志的 UUID。 |

##### `503 服务不可用`

认证存储、PKI、OpenVPN、broker、supervisor 或其他必要依赖不可用。

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
| `error` | `object` | 是 | - | 可供程序读取的 API 错误详情。 |
| `error.kind` | `string` | 是 | 示例: "runtime_unavailable" | 稳定且可供程序读取的错误类型。 |
| `error.message` | `string` | 是 | 示例: "OpenVPN runtime is unavailable" | 可安全展示的错误消息。 |
| `error.request_id` | `string` | 是 | 格式: uuid；示例: "33ba813e-fc63-4af8-b338-7f7486f82202" | 用于关联请求与服务器日志的 UUID。 |

### 14. `DELETE /api/v1/clients/{client_id}/ipv4`

释放已吊销客户端保留的 IPv4 地址。请求体必须为空，且仅适用于仍保留地址分配的已吊销客户端。

#### 请求参数

##### Header 参数

| 字段 | 位置 | 类型 | 必填 | 格式 / 示例 / 约束 | 用途说明 |
|---|---|---|---|---|---|
| `Authorization` | `header` | `string` | 是 | Bearer ovpn_v1.<uuid>.<secret> | API 身份认证凭据。Bearer 后填写服务生成的 API Key。 |

##### Path 参数

| 字段 | 位置 | 类型 | 必填 | 格式 / 示例 / 约束 | 用途说明 |
|---|---|---|---|---|---|
| `client_id` | `path` | `string` | 是 | 格式: uuid；示例: "c0d4f871-6ea6-42b3-9e7b-f00cc1ec354e" | 完整、规范且不可变的客户端 UUID；不接受名称或 UUID 前缀。 |

#### 请求示例

```http
DELETE /api/v1/clients/c0d4f871-6ea6-42b3-9e7b-f00cc1ec354e/ipv4 HTTP/1.1
Authorization: Bearer ovpn_v1.<uuid>.<secret>
```

#### 返回

##### `200 成功`

已释放被吊销客户端保留的地址。

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
| `version` | `integer` | 是 | 固定值: 1 | 响应契约 schema 版本。 |
| `operation_id` | `string` | 是 | 格式: uuid；示例: "d41dbfce-b40e-4672-8a62-cb401f8c099c" | 已提交 journal 操作的 UUID。 |
| `clients` | `object[]` | 是 | 示例: [{"id":"c0d4f871-6ea6-42b3-9e7b-f00cc1ec354e","name":"alice-notebook","status":"revoked","ipv4":{"mode":"none","address":null,"state":"unavailable"}}] | 该响应包含的客户端。 |
| `clients[].id` | `string` | 是 | 格式: uuid；示例: "c0d4f871-6ea6-42b3-9e7b-f00cc1ec354e" | 该对象的稳定标识符。 |
| `clients[].name` | `string` | 是 | 格式: ^[A-Za-z0-9][A-Za-z0-9_.-]*$；示例: "alice-notebook" | 稳定且可读的名称。 |
| `clients[].status` | `"active" \| "revoked" \| "deleted"` | 是 | 可选值: "active" \| "revoked" \| "deleted" | 客户端凭据生命周期状态。 |
| `clients[].ipv4` | `object` | 是 | - | 当前客户端 IPv4 分配视图。 |
| `clients[].ipv4.mode` | `"none" \| "static" \| "dynamic"` | 是 | 可选值: "none" \| "static" \| "dynamic" | IPv4 分配模式。 |
| `clients[].ipv4.address` | `string \| null` | 是 | 格式: ipv4 | IPv4 地址；未分配地址时为 null。 |
| `clients[].ipv4.state` | `"configured" \| "retained" \| "unavailable"` | 是 | 可选值: "configured" \| "retained" \| "unavailable" | 客户端地址分配的可用状态。 |
| `clients[].connection` | `string` | 否 | - | 运行时数据可用时的当前连接状态。 |
| `kick_required` | `string[]` | 是 | 示例: [] | 必须断开活动会话的客户端。 |
| `runtime` | `object[]` | 是 | 示例: [] | 尝试实时操作时的运行时收敛结果。 |
| `runtime[].client_id` | `string` | 否 | 格式: uuid | 不可变的客户端 UUID。 |
| `runtime[].status` | `"ok" \| "unavailable"` | 是 | 可选值: "ok" \| "unavailable" | 请求的运行时操作是否完成或不可用。 |
| `runtime[].result` | `object` | 否 | - | 请求断开客户端会话的结果。 |
| `runtime[].result.version` | `integer` | 否 | 固定值: 1 | 响应契约 schema 版本。 |
| `runtime[].result.client_id` | `string` | 否 | 格式: uuid | 不可变的客户端 UUID。 |
| `runtime[].result.client_name` | `string` | 否 | - | 与会话关联的可读客户端名称。 |
| `runtime[].result.was_connected` | `boolean` | 否 | - | 请求前是否存在匹配会话。 |
| `runtime[].result.disconnected` | `boolean` | 否 | - | 是否至少断开了一个匹配会话。 |
| `runtime[].result.connections` | `integer` | 否 | 最小值: 0 | 断开请求处理的匹配会话数量。 |

##### `400 错误请求`

path、query、header、媒体类型、请求体或 JSON 格式错误。

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
| `error` | `object` | 是 | - | 可供程序读取的 API 错误详情。 |
| `error.kind` | `string` | 是 | 示例: "invalid_json" | 稳定且可供程序读取的错误类型。 |
| `error.message` | `string` | 是 | 示例: "request body is invalid" | 可安全展示的错误消息。 |
| `error.request_id` | `string` | 是 | 格式: uuid；示例: "33ba813e-fc63-4af8-b338-7f7486f82202" | 用于关联请求与服务器日志的 UUID。 |

##### `401 未认证`

Bearer API key 缺失、格式错误、未知或已删除。

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
| `error` | `object` | 是 | - | 可供程序读取的 API 错误详情。 |
| `error.kind` | `string` | 是 | 示例: "unauthenticated" | 稳定且可供程序读取的错误类型。 |
| `error.message` | `string` | 是 | 示例: "API key is missing or invalid" | 可安全展示的错误消息。 |
| `error.request_id` | `string` | 是 | 格式: uuid；示例: "33ba813e-fc63-4af8-b338-7f7486f82202" | 用于关联请求与服务器日志的 UUID。 |

##### `404 未找到`

资源或客户端不存在。

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
| `error` | `object` | 是 | - | 可供程序读取的 API 错误详情。 |
| `error.kind` | `string` | 是 | 示例: "client_not_found" | 稳定且可供程序读取的错误类型。 |
| `error.message` | `string` | 是 | 示例: "client was not found" | 可安全展示的错误消息。 |
| `error.request_id` | `string` | 是 | 格式: uuid；示例: "33ba813e-fc63-4af8-b338-7f7486f82202" | 用于关联请求与服务器日志的 UUID。 |

##### `405 方法不允许`

不接受该 HTTP 方法；请检查 Allow header。

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
| `error` | `object` | 是 | - | 可供程序读取的 API 错误详情。 |
| `error.kind` | `string` | 是 | 示例: "method_not_allowed" | 稳定且可供程序读取的错误类型。 |
| `error.message` | `string` | 是 | 示例: "method is not allowed" | 可安全展示的错误消息。 |
| `error.request_id` | `string` | 是 | 格式: uuid；示例: "33ba813e-fc63-4af8-b338-7f7486f82202" | 用于关联请求与服务器日志的 UUID。 |

##### `409 冲突`

摘要、版本、状态、唯一性、锁或恢复流程发生冲突。

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
| `error` | `object` | 是 | - | 可供程序读取的 API 错误详情。 |
| `error.kind` | `string` | 是 | 示例: "configuration_conflict" | 稳定且可供程序读取的错误类型。 |
| `error.message` | `string` | 是 | 示例: "configuration state changed or is busy" | 可安全展示的错误消息。 |
| `error.request_id` | `string` | 是 | 格式: uuid；示例: "33ba813e-fc63-4af8-b338-7f7486f82202" | 用于关联请求与服务器日志的 UUID。 |

##### `422 无法处理`

JSON 格式有效，但违反客户端、IPv4 或配置语义规则。

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
| `error` | `object` | 是 | - | 可供程序读取的 API 错误详情。 |
| `error.kind` | `string` | 是 | 示例: "invalid_client" | 稳定且可供程序读取的错误类型。 |
| `error.message` | `string` | 是 | 示例: "client request is not valid for the current state" | 可安全展示的错误消息。 |
| `error.request_id` | `string` | 是 | 格式: uuid；示例: "33ba813e-fc63-4af8-b338-7f7486f82202" | 用于关联请求与服务器日志的 UUID。 |

##### `500 服务器内部错误`

未分类的内部错误，不会暴露实现细节。

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
| `error` | `object` | 是 | - | 可供程序读取的 API 错误详情。 |
| `error.kind` | `string` | 是 | 示例: "internal_error" | 稳定且可供程序读取的错误类型。 |
| `error.message` | `string` | 是 | 示例: "request could not be completed" | 可安全展示的错误消息。 |
| `error.request_id` | `string` | 是 | 格式: uuid；示例: "33ba813e-fc63-4af8-b338-7f7486f82202" | 用于关联请求与服务器日志的 UUID。 |

##### `503 服务不可用`

认证存储、PKI、OpenVPN、broker、supervisor 或其他必要依赖不可用。

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
| `error` | `object` | 是 | - | 可供程序读取的 API 错误详情。 |
| `error.kind` | `string` | 是 | 示例: "runtime_unavailable" | 稳定且可供程序读取的错误类型。 |
| `error.message` | `string` | 是 | 示例: "OpenVPN runtime is unavailable" | 可安全展示的错误消息。 |
| `error.request_id` | `string` | 是 | 格式: uuid；示例: "33ba813e-fc63-4af8-b338-7f7486f82202" | 用于关联请求与服务器日志的 UUID。 |

### 15. `POST /api/v1/clients/{client_id}/disconnect`

断开客户端当前会话。请求体必须为空。客户端没有活动会话时返回 200，且 `was_connected=false`。

#### 请求参数

##### Header 参数

| 字段 | 位置 | 类型 | 必填 | 格式 / 示例 / 约束 | 用途说明 |
|---|---|---|---|---|---|
| `Authorization` | `header` | `string` | 是 | Bearer ovpn_v1.<uuid>.<secret> | API 身份认证凭据。Bearer 后填写服务生成的 API Key。 |

##### Path 参数

| 字段 | 位置 | 类型 | 必填 | 格式 / 示例 / 约束 | 用途说明 |
|---|---|---|---|---|---|
| `client_id` | `path` | `string` | 是 | 格式: uuid；示例: "c0d4f871-6ea6-42b3-9e7b-f00cc1ec354e" | 完整、规范且不可变的客户端 UUID；不接受名称或 UUID 前缀。 |

#### 请求示例

```http
POST /api/v1/clients/c0d4f871-6ea6-42b3-9e7b-f00cc1ec354e/disconnect HTTP/1.1
Authorization: Bearer ovpn_v1.<uuid>.<secret>
```

#### 返回

##### `200 成功`

断开结果；没有会话时也会成功返回且不执行操作。

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
| `version` | `integer` | 是 | 固定值: 1 | 响应契约 schema 版本。 |
| `client_id` | `string` | 是 | 格式: uuid；示例: "c0d4f871-6ea6-42b3-9e7b-f00cc1ec354e" | 不可变的客户端 UUID。 |
| `client_name` | `string` | 是 | 示例: "alice-laptop" | 与会话关联的可读客户端名称。 |
| `was_connected` | `boolean` | 是 | 示例: false | 请求前是否存在匹配会话。 |
| `disconnected` | `boolean` | 是 | 示例: false | 是否至少断开了一个匹配会话。 |
| `connections` | `integer` | 是 | 最小值: 0；示例: 0 | 断开请求处理的匹配会话数量。 |

##### `400 错误请求`

path、query、header、媒体类型、请求体或 JSON 格式错误。

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
| `error` | `object` | 是 | - | 可供程序读取的 API 错误详情。 |
| `error.kind` | `string` | 是 | 示例: "invalid_json" | 稳定且可供程序读取的错误类型。 |
| `error.message` | `string` | 是 | 示例: "request body is invalid" | 可安全展示的错误消息。 |
| `error.request_id` | `string` | 是 | 格式: uuid；示例: "33ba813e-fc63-4af8-b338-7f7486f82202" | 用于关联请求与服务器日志的 UUID。 |

##### `401 未认证`

Bearer API key 缺失、格式错误、未知或已删除。

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
| `error` | `object` | 是 | - | 可供程序读取的 API 错误详情。 |
| `error.kind` | `string` | 是 | 示例: "unauthenticated" | 稳定且可供程序读取的错误类型。 |
| `error.message` | `string` | 是 | 示例: "API key is missing or invalid" | 可安全展示的错误消息。 |
| `error.request_id` | `string` | 是 | 格式: uuid；示例: "33ba813e-fc63-4af8-b338-7f7486f82202" | 用于关联请求与服务器日志的 UUID。 |

##### `404 未找到`

资源或客户端不存在。

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
| `error` | `object` | 是 | - | 可供程序读取的 API 错误详情。 |
| `error.kind` | `string` | 是 | 示例: "client_not_found" | 稳定且可供程序读取的错误类型。 |
| `error.message` | `string` | 是 | 示例: "client was not found" | 可安全展示的错误消息。 |
| `error.request_id` | `string` | 是 | 格式: uuid；示例: "33ba813e-fc63-4af8-b338-7f7486f82202" | 用于关联请求与服务器日志的 UUID。 |

##### `405 方法不允许`

不接受该 HTTP 方法；请检查 Allow header。

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
| `error` | `object` | 是 | - | 可供程序读取的 API 错误详情。 |
| `error.kind` | `string` | 是 | 示例: "method_not_allowed" | 稳定且可供程序读取的错误类型。 |
| `error.message` | `string` | 是 | 示例: "method is not allowed" | 可安全展示的错误消息。 |
| `error.request_id` | `string` | 是 | 格式: uuid；示例: "33ba813e-fc63-4af8-b338-7f7486f82202" | 用于关联请求与服务器日志的 UUID。 |

##### `500 服务器内部错误`

未分类的内部错误，不会暴露实现细节。

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
| `error` | `object` | 是 | - | 可供程序读取的 API 错误详情。 |
| `error.kind` | `string` | 是 | 示例: "internal_error" | 稳定且可供程序读取的错误类型。 |
| `error.message` | `string` | 是 | 示例: "request could not be completed" | 可安全展示的错误消息。 |
| `error.request_id` | `string` | 是 | 格式: uuid；示例: "33ba813e-fc63-4af8-b338-7f7486f82202" | 用于关联请求与服务器日志的 UUID。 |

##### `503 服务不可用`

认证存储、PKI、OpenVPN、broker、supervisor 或其他必要依赖不可用。

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
| `error` | `object` | 是 | - | 可供程序读取的 API 错误详情。 |
| `error.kind` | `string` | 是 | 示例: "runtime_unavailable" | 稳定且可供程序读取的错误类型。 |
| `error.message` | `string` | 是 | 示例: "OpenVPN runtime is unavailable" | 可安全展示的错误消息。 |
| `error.request_id` | `string` | 是 | 格式: uuid；示例: "33ba813e-fc63-4af8-b338-7f7486f82202" | 用于关联请求与服务器日志的 UUID。 |

### 16. `GET /api/v1/runtime`

读取 OpenVPN 运行状态和会话

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

##### `200 成功`

OpenVPN 运行状态。

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
| `version` | `integer` | 是 | 固定值: 1 | 响应契约 schema 版本。 |
| `daemon` | `string` | 是 | 示例: "running" | OpenVPN daemon 进程状态。 |
| `management` | `string` | 是 | 示例: "connected" | OpenVPN 管理接口连接状态。 |
| `client_count` | `integer` | 是 | 最小值: 0；示例: 1 | 运行时报告的客户端会话数量。 |
| `clients` | `object[]` | 是 | 示例: [{"client_id":"c0d4f871-6ea6-42b3-9e7b-f00cc1ec354e","client_name":"alice-laptop","remote_address":"203.0.113.10:53210","virtual_address":"10.42.0.30"}] | 该响应包含的客户端。 |
| `clients[].client_id` | `string` | 是 | 格式: uuid；示例: "c0d4f871-6ea6-42b3-9e7b-f00cc1ec354e" | 不可变的客户端 UUID。 |
| `clients[].client_name` | `string` | 否 | 示例: "alice-laptop" | 与会话关联的可读客户端名称。 |
| `clients[].remote_address` | `string` | 否 | 示例: "203.0.113.10:53210" | 已连接客户端的远端网络地址。 |
| `clients[].virtual_address` | `string` | 否 | 格式: ipv4；示例: "10.42.0.30" | 分配给会话的 VPN 虚拟 IPv4 地址。 |

##### `401 未认证`

Bearer API key 缺失、格式错误、未知或已删除。

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
| `error` | `object` | 是 | - | 可供程序读取的 API 错误详情。 |
| `error.kind` | `string` | 是 | 示例: "unauthenticated" | 稳定且可供程序读取的错误类型。 |
| `error.message` | `string` | 是 | 示例: "API key is missing or invalid" | 可安全展示的错误消息。 |
| `error.request_id` | `string` | 是 | 格式: uuid；示例: "33ba813e-fc63-4af8-b338-7f7486f82202" | 用于关联请求与服务器日志的 UUID。 |

##### `405 方法不允许`

不接受该 HTTP 方法；请检查 Allow header。

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
| `error` | `object` | 是 | - | 可供程序读取的 API 错误详情。 |
| `error.kind` | `string` | 是 | 示例: "method_not_allowed" | 稳定且可供程序读取的错误类型。 |
| `error.message` | `string` | 是 | 示例: "method is not allowed" | 可安全展示的错误消息。 |
| `error.request_id` | `string` | 是 | 格式: uuid；示例: "33ba813e-fc63-4af8-b338-7f7486f82202" | 用于关联请求与服务器日志的 UUID。 |

##### `500 服务器内部错误`

未分类的内部错误，不会暴露实现细节。

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
| `error` | `object` | 是 | - | 可供程序读取的 API 错误详情。 |
| `error.kind` | `string` | 是 | 示例: "internal_error" | 稳定且可供程序读取的错误类型。 |
| `error.message` | `string` | 是 | 示例: "request could not be completed" | 可安全展示的错误消息。 |
| `error.request_id` | `string` | 是 | 格式: uuid；示例: "33ba813e-fc63-4af8-b338-7f7486f82202" | 用于关联请求与服务器日志的 UUID。 |

##### `503 服务不可用`

认证存储、PKI、OpenVPN、broker、supervisor 或其他必要依赖不可用。

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
| `error` | `object` | 是 | - | 可供程序读取的 API 错误详情。 |
| `error.kind` | `string` | 是 | 示例: "runtime_unavailable" | 稳定且可供程序读取的错误类型。 |
| `error.message` | `string` | 是 | 示例: "OpenVPN runtime is unavailable" | 可安全展示的错误消息。 |
| `error.request_id` | `string` | 是 | 格式: uuid；示例: "33ba813e-fc63-4af8-b338-7f7486f82202" | 用于关联请求与服务器日志的 UUID。 |

### 17. `GET /api/v1/runtime/events`

读取最近的结构化运行事件

#### 请求参数

##### Header 参数

| 字段 | 位置 | 类型 | 必填 | 格式 / 示例 / 约束 | 用途说明 |
|---|---|---|---|---|---|
| `Authorization` | `header` | `string` | 是 | Bearer ovpn_v1.<uuid>.<secret> | API 身份认证凭据。Bearer 后填写服务生成的 API Key。 |

##### Query 参数

| 字段 | 位置 | 类型 | 必填 | 格式 / 示例 / 约束 | 用途说明 |
|---|---|---|---|---|---|
| `lines` | `query` | `integer` | 否 | 最小值: 0；最大值: 1000；默认值: 100；示例: 100 | 返回最近事件的数量，默认为 100。 |

#### 请求示例

```http
GET /api/v1/runtime/events?lines=100 HTTP/1.1
Authorization: Bearer ovpn_v1.<uuid>.<secret>
```

#### 返回

##### `200 成功`

最近的结构化运行事件。

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
| `version` | `integer` | 是 | 固定值: 1 | 响应契约 schema 版本。 |
| `events` | `object[]` | 是 | 示例: [{"timestamp":"2026-08-19T09:55:07Z","event":"client-disconnect","operation":"runtime.disconnect","outcome":"success","client_id":"c0d4f871-6ea6-42b3-9e7b-f00cc1ec354e","client_name":"alice-laptop"}] | 按时间顺序排列的最近结构化运行事件。 |
| `events[].timestamp` | `string` | 是 | 格式: date-time；示例: "2026-08-19T09:55:07Z" | 运行事件发生时的 UTC 时间。 |
| `events[].event` | `string` | 是 | 示例: "client-disconnect" | 稳定的运行事件名称。 |
| `events[].operation` | `string` | 是 | 示例: "runtime.disconnect" | 产生该事件的稳定操作名称。 |
| `events[].outcome` | `string` | 是 | 示例: "success" | 稳定的事件结果值。 |
| `events[].client_id` | `string \| null` | 否 | 格式: uuid；示例: "c0d4f871-6ea6-42b3-9e7b-f00cc1ec354e" | 与事件关联的客户端 UUID；非客户端事件时为 null。 |
| `events[].client_name` | `string \| null` | 否 | 示例: "alice-laptop" | 与事件关联的客户端名称；不可用时为 null。 |

##### `400 错误请求`

path、query、header、媒体类型、请求体或 JSON 格式错误。

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
| `error` | `object` | 是 | - | 可供程序读取的 API 错误详情。 |
| `error.kind` | `string` | 是 | 示例: "invalid_json" | 稳定且可供程序读取的错误类型。 |
| `error.message` | `string` | 是 | 示例: "request body is invalid" | 可安全展示的错误消息。 |
| `error.request_id` | `string` | 是 | 格式: uuid；示例: "33ba813e-fc63-4af8-b338-7f7486f82202" | 用于关联请求与服务器日志的 UUID。 |

##### `401 未认证`

Bearer API key 缺失、格式错误、未知或已删除。

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
| `error` | `object` | 是 | - | 可供程序读取的 API 错误详情。 |
| `error.kind` | `string` | 是 | 示例: "unauthenticated" | 稳定且可供程序读取的错误类型。 |
| `error.message` | `string` | 是 | 示例: "API key is missing or invalid" | 可安全展示的错误消息。 |
| `error.request_id` | `string` | 是 | 格式: uuid；示例: "33ba813e-fc63-4af8-b338-7f7486f82202" | 用于关联请求与服务器日志的 UUID。 |

##### `405 方法不允许`

不接受该 HTTP 方法；请检查 Allow header。

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
| `error` | `object` | 是 | - | 可供程序读取的 API 错误详情。 |
| `error.kind` | `string` | 是 | 示例: "method_not_allowed" | 稳定且可供程序读取的错误类型。 |
| `error.message` | `string` | 是 | 示例: "method is not allowed" | 可安全展示的错误消息。 |
| `error.request_id` | `string` | 是 | 格式: uuid；示例: "33ba813e-fc63-4af8-b338-7f7486f82202" | 用于关联请求与服务器日志的 UUID。 |

##### `500 服务器内部错误`

未分类的内部错误，不会暴露实现细节。

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
| `error` | `object` | 是 | - | 可供程序读取的 API 错误详情。 |
| `error.kind` | `string` | 是 | 示例: "internal_error" | 稳定且可供程序读取的错误类型。 |
| `error.message` | `string` | 是 | 示例: "request could not be completed" | 可安全展示的错误消息。 |
| `error.request_id` | `string` | 是 | 格式: uuid；示例: "33ba813e-fc63-4af8-b338-7f7486f82202" | 用于关联请求与服务器日志的 UUID。 |

##### `503 服务不可用`

认证存储、PKI、OpenVPN、broker、supervisor 或其他必要依赖不可用。

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
| `error` | `object` | 是 | - | 可供程序读取的 API 错误详情。 |
| `error.kind` | `string` | 是 | 示例: "runtime_unavailable" | 稳定且可供程序读取的错误类型。 |
| `error.message` | `string` | 是 | 示例: "OpenVPN runtime is unavailable" | 可安全展示的错误消息。 |
| `error.request_id` | `string` | 是 | 格式: uuid；示例: "33ba813e-fc63-4af8-b338-7f7486f82202" | 用于关联请求与服务器日志的 UUID。 |

### 18. `GET /api/v1/config/applied`

读取已应用的配置版本

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

##### `200 成功`

已应用的版本号和规范化配置。

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
| `revision` | `integer` | 是 | 最小值: 1；示例: 12 | 单调递增的已应用配置版本。 |
| `digest` | `string` | 是 | 格式: ^[0-9a-f]{64}$；示例: "be7ed82796b39a77ce949201a4d9483cf09e56648f14b8b9b192bb83d2c4d042" | 用于精确标识期望配置的小写 SHA-256 摘要。 |
| `config` | `object` | 是 | - | 完整且规范化的 OpenVPN 服务端配置。 |
| `config.version` | `integer` | 是 | 固定值: 1 | 配置 schema 版本，必须为 1。 |
| `config.server` | `object` | 是 | - | OpenVPN 监听和传输设置。 |
| `config.server.endpoint` | `string` | 是 | 示例: "vpn.example.com" | 写入生成客户端配置文件的公网主机名或 IP 地址。 |
| `config.server.protocol` | `"udp" \| "tcp"` | 是 | 可选值: "udp" \| "tcp" | OpenVPN 传输协议。 |
| `config.server.family` | `"auto" \| "ipv4" \| "ipv6"` | 是 | 可选值: "auto" \| "ipv4" \| "ipv6" | 服务端传输使用的地址族。 |
| `config.server.port` | `integer` | 是 | 最小值: 1；最大值: 65535；示例: 1194 | OpenVPN 监听端口。 |
| `config.server.client_to_client` | `boolean` | 是 | 示例: true | 是否允许已连接的 VPN 客户端直接通信。 |
| `config.ipv4` | `object` | 是 | - | 服务端 IPv4 网络、地址分配、NAT、DNS 和路由设置。 |
| `config.ipv4.network` | `string` | 是 | 格式: ipv4-cidr；示例: "10.42.0.0/24" | CIDR 格式的 VPN IPv4 网络。 |
| `config.ipv4.dynamic_pool_size` | `integer` | 是 | 最小值: 0；示例: 64 | 为动态分配保留的地址数量。 |
| `config.ipv4.nat_enabled` | `boolean` | 是 | 示例: false | 是否对 VPN 网络的出站流量执行地址伪装。 |
| `config.ipv4.nat_interface` | `string` | 是 | 示例: "auto" | NAT 使用的出口接口；使用 auto 时自动检测。 |
| `config.ipv4.redirect_gateway` | `boolean` | 是 | 示例: false | 生成的配置文件是否将默认 IPv4 路由重定向到 VPN。 |
| `config.ipv4.dns` | `string[]` | 是 | 示例: [] | 推送给客户端的 IPv4 DNS 服务器。 |
| `config.ipv4.routes` | `string[]` | 是 | 示例: [] | 推送给客户端的其他 IPv4 CIDR 路由。 |
| `config.logging` | `object` | 是 | - | 运行时日志轮转设置。 |
| `config.logging.max_bytes` | `integer` | 是 | 最小值: 1；示例: 10485760 | 触发轮转前活动日志文件的最大字节数。 |
| `config.logging.backups` | `integer` | 是 | 最小值: 0；示例: 5 | 保留的轮转日志文件数量。 |

##### `401 未认证`

Bearer API key 缺失、格式错误、未知或已删除。

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
| `error` | `object` | 是 | - | 可供程序读取的 API 错误详情。 |
| `error.kind` | `string` | 是 | 示例: "unauthenticated" | 稳定且可供程序读取的错误类型。 |
| `error.message` | `string` | 是 | 示例: "API key is missing or invalid" | 可安全展示的错误消息。 |
| `error.request_id` | `string` | 是 | 格式: uuid；示例: "33ba813e-fc63-4af8-b338-7f7486f82202" | 用于关联请求与服务器日志的 UUID。 |

##### `405 方法不允许`

不接受该 HTTP 方法；请检查 Allow header。

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
| `error` | `object` | 是 | - | 可供程序读取的 API 错误详情。 |
| `error.kind` | `string` | 是 | 示例: "method_not_allowed" | 稳定且可供程序读取的错误类型。 |
| `error.message` | `string` | 是 | 示例: "method is not allowed" | 可安全展示的错误消息。 |
| `error.request_id` | `string` | 是 | 格式: uuid；示例: "33ba813e-fc63-4af8-b338-7f7486f82202" | 用于关联请求与服务器日志的 UUID。 |

##### `409 冲突`

摘要、版本、状态、唯一性、锁或恢复流程发生冲突。

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
| `error` | `object` | 是 | - | 可供程序读取的 API 错误详情。 |
| `error.kind` | `string` | 是 | 示例: "configuration_conflict" | 稳定且可供程序读取的错误类型。 |
| `error.message` | `string` | 是 | 示例: "configuration state changed or is busy" | 可安全展示的错误消息。 |
| `error.request_id` | `string` | 是 | 格式: uuid；示例: "33ba813e-fc63-4af8-b338-7f7486f82202" | 用于关联请求与服务器日志的 UUID。 |

##### `500 服务器内部错误`

未分类的内部错误，不会暴露实现细节。

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
| `error` | `object` | 是 | - | 可供程序读取的 API 错误详情。 |
| `error.kind` | `string` | 是 | 示例: "internal_error" | 稳定且可供程序读取的错误类型。 |
| `error.message` | `string` | 是 | 示例: "request could not be completed" | 可安全展示的错误消息。 |
| `error.request_id` | `string` | 是 | 格式: uuid；示例: "33ba813e-fc63-4af8-b338-7f7486f82202" | 用于关联请求与服务器日志的 UUID。 |

##### `503 服务不可用`

认证存储、PKI、OpenVPN、broker、supervisor 或其他必要依赖不可用。

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
| `error` | `object` | 是 | - | 可供程序读取的 API 错误详情。 |
| `error.kind` | `string` | 是 | 示例: "runtime_unavailable" | 稳定且可供程序读取的错误类型。 |
| `error.message` | `string` | 是 | 示例: "OpenVPN runtime is unavailable" | 可安全展示的错误消息。 |
| `error.request_id` | `string` | 是 | 格式: uuid；示例: "33ba813e-fc63-4af8-b338-7f7486f82202" | 用于关联请求与服务器日志的 UUID。 |

### 19. `GET /api/v1/config/desired`

读取期望配置和 CAS 摘要

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

##### `200 成功`

期望配置摘要和规范化配置。

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
| `digest` | `string` | 是 | 格式: ^[0-9a-f]{64}$；示例: "be7ed82796b39a77ce949201a4d9483cf09e56648f14b8b9b192bb83d2c4d042" | 用于精确标识期望配置的小写 SHA-256 摘要。 |
| `config` | `object` | 是 | - | 完整且规范化的 OpenVPN 服务端配置。 |
| `config.version` | `integer` | 是 | 固定值: 1 | 配置 schema 版本，必须为 1。 |
| `config.server` | `object` | 是 | - | OpenVPN 监听和传输设置。 |
| `config.server.endpoint` | `string` | 是 | 示例: "vpn.example.com" | 写入生成客户端配置文件的公网主机名或 IP 地址。 |
| `config.server.protocol` | `"udp" \| "tcp"` | 是 | 可选值: "udp" \| "tcp" | OpenVPN 传输协议。 |
| `config.server.family` | `"auto" \| "ipv4" \| "ipv6"` | 是 | 可选值: "auto" \| "ipv4" \| "ipv6" | 服务端传输使用的地址族。 |
| `config.server.port` | `integer` | 是 | 最小值: 1；最大值: 65535；示例: 1194 | OpenVPN 监听端口。 |
| `config.server.client_to_client` | `boolean` | 是 | 示例: true | 是否允许已连接的 VPN 客户端直接通信。 |
| `config.ipv4` | `object` | 是 | - | 服务端 IPv4 网络、地址分配、NAT、DNS 和路由设置。 |
| `config.ipv4.network` | `string` | 是 | 格式: ipv4-cidr；示例: "10.42.0.0/24" | CIDR 格式的 VPN IPv4 网络。 |
| `config.ipv4.dynamic_pool_size` | `integer` | 是 | 最小值: 0；示例: 64 | 为动态分配保留的地址数量。 |
| `config.ipv4.nat_enabled` | `boolean` | 是 | 示例: false | 是否对 VPN 网络的出站流量执行地址伪装。 |
| `config.ipv4.nat_interface` | `string` | 是 | 示例: "auto" | NAT 使用的出口接口；使用 auto 时自动检测。 |
| `config.ipv4.redirect_gateway` | `boolean` | 是 | 示例: false | 生成的配置文件是否将默认 IPv4 路由重定向到 VPN。 |
| `config.ipv4.dns` | `string[]` | 是 | 示例: [] | 推送给客户端的 IPv4 DNS 服务器。 |
| `config.ipv4.routes` | `string[]` | 是 | 示例: [] | 推送给客户端的其他 IPv4 CIDR 路由。 |
| `config.logging` | `object` | 是 | - | 运行时日志轮转设置。 |
| `config.logging.max_bytes` | `integer` | 是 | 最小值: 1；示例: 10485760 | 触发轮转前活动日志文件的最大字节数。 |
| `config.logging.backups` | `integer` | 是 | 最小值: 0；示例: 5 | 保留的轮转日志文件数量。 |

##### `401 未认证`

Bearer API key 缺失、格式错误、未知或已删除。

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
| `error` | `object` | 是 | - | 可供程序读取的 API 错误详情。 |
| `error.kind` | `string` | 是 | 示例: "unauthenticated" | 稳定且可供程序读取的错误类型。 |
| `error.message` | `string` | 是 | 示例: "API key is missing or invalid" | 可安全展示的错误消息。 |
| `error.request_id` | `string` | 是 | 格式: uuid；示例: "33ba813e-fc63-4af8-b338-7f7486f82202" | 用于关联请求与服务器日志的 UUID。 |

##### `405 方法不允许`

不接受该 HTTP 方法；请检查 Allow header。

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
| `error` | `object` | 是 | - | 可供程序读取的 API 错误详情。 |
| `error.kind` | `string` | 是 | 示例: "method_not_allowed" | 稳定且可供程序读取的错误类型。 |
| `error.message` | `string` | 是 | 示例: "method is not allowed" | 可安全展示的错误消息。 |
| `error.request_id` | `string` | 是 | 格式: uuid；示例: "33ba813e-fc63-4af8-b338-7f7486f82202" | 用于关联请求与服务器日志的 UUID。 |

##### `409 冲突`

摘要、版本、状态、唯一性、锁或恢复流程发生冲突。

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
| `error` | `object` | 是 | - | 可供程序读取的 API 错误详情。 |
| `error.kind` | `string` | 是 | 示例: "configuration_conflict" | 稳定且可供程序读取的错误类型。 |
| `error.message` | `string` | 是 | 示例: "configuration state changed or is busy" | 可安全展示的错误消息。 |
| `error.request_id` | `string` | 是 | 格式: uuid；示例: "33ba813e-fc63-4af8-b338-7f7486f82202" | 用于关联请求与服务器日志的 UUID。 |

##### `422 无法处理`

JSON 格式有效，但违反客户端、IPv4 或配置语义规则。

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
| `error` | `object` | 是 | - | 可供程序读取的 API 错误详情。 |
| `error.kind` | `string` | 是 | 示例: "invalid_client" | 稳定且可供程序读取的错误类型。 |
| `error.message` | `string` | 是 | 示例: "client request is not valid for the current state" | 可安全展示的错误消息。 |
| `error.request_id` | `string` | 是 | 格式: uuid；示例: "33ba813e-fc63-4af8-b338-7f7486f82202" | 用于关联请求与服务器日志的 UUID。 |

##### `500 服务器内部错误`

未分类的内部错误，不会暴露实现细节。

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
| `error` | `object` | 是 | - | 可供程序读取的 API 错误详情。 |
| `error.kind` | `string` | 是 | 示例: "internal_error" | 稳定且可供程序读取的错误类型。 |
| `error.message` | `string` | 是 | 示例: "request could not be completed" | 可安全展示的错误消息。 |
| `error.request_id` | `string` | 是 | 格式: uuid；示例: "33ba813e-fc63-4af8-b338-7f7486f82202" | 用于关联请求与服务器日志的 UUID。 |

##### `503 服务不可用`

认证存储、PKI、OpenVPN、broker、supervisor 或其他必要依赖不可用。

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
| `error` | `object` | 是 | - | 可供程序读取的 API 错误详情。 |
| `error.kind` | `string` | 是 | 示例: "runtime_unavailable" | 稳定且可供程序读取的错误类型。 |
| `error.message` | `string` | 是 | 示例: "OpenVPN runtime is unavailable" | 可安全展示的错误消息。 |
| `error.request_id` | `string` | 是 | 格式: uuid；示例: "33ba813e-fc63-4af8-b338-7f7486f82202" | 用于关联请求与服务器日志的 UUID。 |

### 20. `PUT /api/v1/config/desired`

使用 CAS 替换期望配置。将上一次期望配置摘要作为带双引号的 If-Match 值，并在请求体中发送完整的规范化配置。

#### 请求参数

##### Header 参数

| 字段 | 位置 | 类型 | 必填 | 格式 / 示例 / 约束 | 用途说明 |
|---|---|---|---|---|---|
| `Authorization` | `header` | `string` | 是 | Bearer ovpn_v1.<uuid>.<secret> | API 身份认证凭据。Bearer 后填写服务生成的 API Key。 |
| `Content-Type` | `header` | `string` | 是 | application/json | 声明请求体使用 JSON 格式。 |
| `If-Match` | `header` | `string` | 是 | 格式: ^"[0-9a-f]{64}"$；示例: "\"be7ed82796b39a77ce949201a4d9483cf09e56648f14b8b9b192bb83d2c4d042\"" | 上一次期望配置响应 ETag 中带双引号的小写 SHA-256 摘要，只能提供一个。 |

##### JSON Body 字段

| 字段 | 位置 | 类型 | 必填 | 格式 / 示例 / 约束 | 用途说明 |
|---|---|---|---|---|---|
| `version` | `body` | `integer` | 是 | 固定值: 1 | 配置 schema 版本，必须为 1。 |
| `server` | `body` | `object` | 是 | - | OpenVPN 监听和传输设置。 |
| `server.endpoint` | `body` | `string` | 是 | 示例: "vpn.example.com" | 写入生成客户端配置文件的公网主机名或 IP 地址。 |
| `server.protocol` | `body` | `"udp" \| "tcp"` | 是 | 可选值: "udp" \| "tcp" | OpenVPN 传输协议。 |
| `server.family` | `body` | `"auto" \| "ipv4" \| "ipv6"` | 是 | 可选值: "auto" \| "ipv4" \| "ipv6" | 服务端传输使用的地址族。 |
| `server.port` | `body` | `integer` | 是 | 最小值: 1；最大值: 65535；示例: 1194 | OpenVPN 监听端口。 |
| `server.client_to_client` | `body` | `boolean` | 是 | 示例: true | 是否允许已连接的 VPN 客户端直接通信。 |
| `ipv4` | `body` | `object` | 是 | - | 服务端 IPv4 网络、地址分配、NAT、DNS 和路由设置。 |
| `ipv4.network` | `body` | `string` | 是 | 格式: ipv4-cidr；示例: "10.42.0.0/24" | CIDR 格式的 VPN IPv4 网络。 |
| `ipv4.dynamic_pool_size` | `body` | `integer` | 是 | 最小值: 0；示例: 64 | 为动态分配保留的地址数量。 |
| `ipv4.nat_enabled` | `body` | `boolean` | 是 | 示例: false | 是否对 VPN 网络的出站流量执行地址伪装。 |
| `ipv4.nat_interface` | `body` | `string` | 是 | 示例: "auto" | NAT 使用的出口接口；使用 auto 时自动检测。 |
| `ipv4.redirect_gateway` | `body` | `boolean` | 是 | 示例: false | 生成的配置文件是否将默认 IPv4 路由重定向到 VPN。 |
| `ipv4.dns` | `body` | `string[]` | 是 | 示例: ["1.1.1.1"] | 推送给客户端的 IPv4 DNS 服务器。 |
| `ipv4.routes` | `body` | `string[]` | 是 | 示例: ["10.20.0.0/16"] | 推送给客户端的其他 IPv4 CIDR 路由。 |
| `logging` | `body` | `object` | 是 | - | 运行时日志轮转设置。 |
| `logging.max_bytes` | `body` | `integer` | 是 | 最小值: 1；示例: 10485760 | 触发轮转前活动日志文件的最大字节数。 |
| `logging.backups` | `body` | `integer` | 是 | 最小值: 0；示例: 5 | 保留的轮转日志文件数量。 |

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

##### `200 成功`

期望配置摘要和规范化配置。

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
| `digest` | `string` | 是 | 格式: ^[0-9a-f]{64}$；示例: "be7ed82796b39a77ce949201a4d9483cf09e56648f14b8b9b192bb83d2c4d042" | 用于精确标识期望配置的小写 SHA-256 摘要。 |
| `config` | `object` | 是 | - | 完整且规范化的 OpenVPN 服务端配置。 |
| `config.version` | `integer` | 是 | 固定值: 1 | 配置 schema 版本，必须为 1。 |
| `config.server` | `object` | 是 | - | OpenVPN 监听和传输设置。 |
| `config.server.endpoint` | `string` | 是 | 示例: "vpn.example.com" | 写入生成客户端配置文件的公网主机名或 IP 地址。 |
| `config.server.protocol` | `"udp" \| "tcp"` | 是 | 可选值: "udp" \| "tcp" | OpenVPN 传输协议。 |
| `config.server.family` | `"auto" \| "ipv4" \| "ipv6"` | 是 | 可选值: "auto" \| "ipv4" \| "ipv6" | 服务端传输使用的地址族。 |
| `config.server.port` | `integer` | 是 | 最小值: 1；最大值: 65535；示例: 1194 | OpenVPN 监听端口。 |
| `config.server.client_to_client` | `boolean` | 是 | 示例: true | 是否允许已连接的 VPN 客户端直接通信。 |
| `config.ipv4` | `object` | 是 | - | 服务端 IPv4 网络、地址分配、NAT、DNS 和路由设置。 |
| `config.ipv4.network` | `string` | 是 | 格式: ipv4-cidr；示例: "10.42.0.0/24" | CIDR 格式的 VPN IPv4 网络。 |
| `config.ipv4.dynamic_pool_size` | `integer` | 是 | 最小值: 0；示例: 64 | 为动态分配保留的地址数量。 |
| `config.ipv4.nat_enabled` | `boolean` | 是 | 示例: false | 是否对 VPN 网络的出站流量执行地址伪装。 |
| `config.ipv4.nat_interface` | `string` | 是 | 示例: "auto" | NAT 使用的出口接口；使用 auto 时自动检测。 |
| `config.ipv4.redirect_gateway` | `boolean` | 是 | 示例: false | 生成的配置文件是否将默认 IPv4 路由重定向到 VPN。 |
| `config.ipv4.dns` | `string[]` | 是 | 示例: [] | 推送给客户端的 IPv4 DNS 服务器。 |
| `config.ipv4.routes` | `string[]` | 是 | 示例: [] | 推送给客户端的其他 IPv4 CIDR 路由。 |
| `config.logging` | `object` | 是 | - | 运行时日志轮转设置。 |
| `config.logging.max_bytes` | `integer` | 是 | 最小值: 1；示例: 10485760 | 触发轮转前活动日志文件的最大字节数。 |
| `config.logging.backups` | `integer` | 是 | 最小值: 0；示例: 5 | 保留的轮转日志文件数量。 |

##### `400 错误请求`

path、query、header、媒体类型、请求体或 JSON 格式错误。

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
| `error` | `object` | 是 | - | 可供程序读取的 API 错误详情。 |
| `error.kind` | `string` | 是 | 示例: "invalid_json" | 稳定且可供程序读取的错误类型。 |
| `error.message` | `string` | 是 | 示例: "request body is invalid" | 可安全展示的错误消息。 |
| `error.request_id` | `string` | 是 | 格式: uuid；示例: "33ba813e-fc63-4af8-b338-7f7486f82202" | 用于关联请求与服务器日志的 UUID。 |

##### `401 未认证`

Bearer API key 缺失、格式错误、未知或已删除。

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
| `error` | `object` | 是 | - | 可供程序读取的 API 错误详情。 |
| `error.kind` | `string` | 是 | 示例: "unauthenticated" | 稳定且可供程序读取的错误类型。 |
| `error.message` | `string` | 是 | 示例: "API key is missing or invalid" | 可安全展示的错误消息。 |
| `error.request_id` | `string` | 是 | 格式: uuid；示例: "33ba813e-fc63-4af8-b338-7f7486f82202" | 用于关联请求与服务器日志的 UUID。 |

##### `405 方法不允许`

不接受该 HTTP 方法；请检查 Allow header。

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
| `error` | `object` | 是 | - | 可供程序读取的 API 错误详情。 |
| `error.kind` | `string` | 是 | 示例: "method_not_allowed" | 稳定且可供程序读取的错误类型。 |
| `error.message` | `string` | 是 | 示例: "method is not allowed" | 可安全展示的错误消息。 |
| `error.request_id` | `string` | 是 | 格式: uuid；示例: "33ba813e-fc63-4af8-b338-7f7486f82202" | 用于关联请求与服务器日志的 UUID。 |

##### `409 冲突`

摘要、版本、状态、唯一性、锁或恢复流程发生冲突。

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
| `error` | `object` | 是 | - | 可供程序读取的 API 错误详情。 |
| `error.kind` | `string` | 是 | 示例: "configuration_conflict" | 稳定且可供程序读取的错误类型。 |
| `error.message` | `string` | 是 | 示例: "configuration state changed or is busy" | 可安全展示的错误消息。 |
| `error.request_id` | `string` | 是 | 格式: uuid；示例: "33ba813e-fc63-4af8-b338-7f7486f82202" | 用于关联请求与服务器日志的 UUID。 |

##### `422 无法处理`

JSON 格式有效，但违反客户端、IPv4 或配置语义规则。

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
| `error` | `object` | 是 | - | 可供程序读取的 API 错误详情。 |
| `error.kind` | `string` | 是 | 示例: "invalid_client" | 稳定且可供程序读取的错误类型。 |
| `error.message` | `string` | 是 | 示例: "client request is not valid for the current state" | 可安全展示的错误消息。 |
| `error.request_id` | `string` | 是 | 格式: uuid；示例: "33ba813e-fc63-4af8-b338-7f7486f82202" | 用于关联请求与服务器日志的 UUID。 |

##### `500 服务器内部错误`

未分类的内部错误，不会暴露实现细节。

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
| `error` | `object` | 是 | - | 可供程序读取的 API 错误详情。 |
| `error.kind` | `string` | 是 | 示例: "internal_error" | 稳定且可供程序读取的错误类型。 |
| `error.message` | `string` | 是 | 示例: "request could not be completed" | 可安全展示的错误消息。 |
| `error.request_id` | `string` | 是 | 格式: uuid；示例: "33ba813e-fc63-4af8-b338-7f7486f82202" | 用于关联请求与服务器日志的 UUID。 |

##### `503 服务不可用`

认证存储、PKI、OpenVPN、broker、supervisor 或其他必要依赖不可用。

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
| `error` | `object` | 是 | - | 可供程序读取的 API 错误详情。 |
| `error.kind` | `string` | 是 | 示例: "runtime_unavailable" | 稳定且可供程序读取的错误类型。 |
| `error.message` | `string` | 是 | 示例: "OpenVPN runtime is unavailable" | 可安全展示的错误消息。 |
| `error.request_id` | `string` | 是 | 格式: uuid；示例: "33ba813e-fc63-4af8-b338-7f7486f82202" | 用于关联请求与服务器日志的 UUID。 |

### 21. `GET /api/v1/config/plan`

规划从期望配置到已应用配置的变更

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

##### `200 成功`

从期望配置到已应用配置的完整计划。

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
| `version` | `integer` | 是 | 固定值: 1 | 响应契约 schema 版本。 |
| `instance_id` | `string` | 是 | 格式: uuid；示例: "bbffeb8c-2d11-4613-874d-b5fcc804a608" | 已初始化 OpenVPN 实例的不可变 UUID。 |
| `configuration` | `object` | 是 | - | 版本、摘要、变更和影响的对比结果。 |
| `configuration.initial` | `boolean` | 是 | 示例: false | 该计划是否创建首个已应用版本。 |
| `configuration.current_revision` | `integer` | 是 | 最小值: 0；示例: 12 | 用于比较的当前已应用配置版本。 |
| `configuration.target_revision` | `integer` | 是 | 最小值: 1；示例: 13 | 执行该计划后将产生的已应用版本。 |
| `configuration.current_digest` | `string` | 否 | 格式: ^[0-9a-f]{64}$；示例: "be7ed82796b39a77ce949201a4d9483cf09e56648f14b8b9b192bb83d2c4d042" | 用于精确标识期望配置的小写 SHA-256 摘要。 |
| `configuration.desired_digest` | `string` | 是 | 格式: ^[0-9a-f]{64}$；示例: "489b950c0d134b5d7e8f350b2cb4bfa4d6d163d841f840964baf6d74c65cc8ca" | 用于精确标识期望配置的小写 SHA-256 摘要。 |
| `configuration.in_sync` | `boolean` | 是 | 示例: false | 期望配置与已应用配置是否一致。 |
| `configuration.changes` | `object[]` | 是 | 示例: [{"field":"server.endpoint","before":"vpn.example.com","after":"vpn-new.example.com"}] | 该计划中的字段级配置变更。 |
| `configuration.changes[].field` | `string` | 是 | 示例: "server.endpoint" | 发生变更的配置字段规范点分路径。 |
| `configuration.changes[].before` | `any` | 是 | 示例: "vpn.example.com" | 建议配置变更前的已应用值。 |
| `configuration.changes[].after` | `any` | 是 | 示例: "vpn-new.example.com" | 建议配置变更后的期望值。 |
| `configuration.impact` | `object` | 是 | - | 配置变更对运行时和制品的影响。 |
| `configuration.impact.restart_required` | `boolean` | 是 | 示例: true | 应用该计划是否需要重启 OpenVPN。 |
| `configuration.impact.address_remap` | `boolean` | 是 | 示例: false | 是否必须重新计算客户端地址分配。 |
| `configuration.impact.firewall_reconcile` | `boolean` | 是 | 示例: false | 是否必须协调防火墙规则。 |
| `configuration.impact.profile_redistribution` | `boolean` | 是 | 示例: true | 必须重新分发生成配置文件的客户端。 |
| `configuration.impact.derived_artifacts` | `string[]` | 是 | 示例: ["server_config","client_profiles"] | 必须重新生成或删除的派生制品。 |
| `address_changes` | `object[]` | 是 | 示例: [] | 该计划要求执行的客户端地址变更。 |
| `address_changes[].client` | `object` | 是 | - | 计划报告中使用的稳定客户端身份。 |
| `address_changes[].client.id` | `string` | 是 | 格式: uuid | 该对象的稳定标识符。 |
| `address_changes[].client.name` | `string` | 是 | - | 稳定且可读的名称。 |
| `address_changes[].before` | `object` | 是 | - | 某一时刻的客户端 IPv4 分配意图。 |
| `address_changes[].before.mode` | `string` | 是 | - | IPv4 分配模式。 |
| `address_changes[].before.address` | `string \| null` | 是 | 格式: ipv4 | IPv4 地址；未分配地址时为 null。 |
| `address_changes[].before.state` | `string` | 是 | - | 该地址意图的生命周期状态。 |
| `address_changes[].after` | `object` | 是 | - | 某一时刻的客户端 IPv4 分配意图。 |
| `address_changes[].after.mode` | `string` | 是 | - | IPv4 分配模式。 |
| `address_changes[].after.address` | `string \| null` | 是 | 格式: ipv4 | IPv4 地址；未分配地址时为 null。 |
| `address_changes[].after.state` | `string` | 是 | - | 该地址意图的生命周期状态。 |
| `artifacts` | `object[]` | 是 | 示例: [] | 该计划要求执行的派生制品操作。 |
| `artifacts[].owner_kind` | `string` | 是 | - | 拥有该制品的对象类型。 |
| `artifacts[].owner_id` | `string` | 是 | - | 所属对象的稳定标识符。 |
| `artifacts[].kind` | `string` | 是 | - | 派生制品类型。 |
| `artifacts[].key` | `string` | 是 | - | 用于标识派生制品的稳定 key。 |
| `artifacts[].action` | `"regenerate" \| "delete"` | 是 | 可选值: "regenerate" \| "delete" | 重新生成还是删除该制品。 |
| `profile_redistribution` | `object[]` | 是 | 示例: [] | 必须重新分发生成配置文件的客户端。 |
| `profile_redistribution[].id` | `string` | 是 | 格式: uuid | 该对象的稳定标识符。 |
| `profile_redistribution[].name` | `string` | 是 | - | 稳定且可读的名称。 |
| `firewall` | `object` | 是 | - | 防火墙变更前后状态及协调要求。 |
| `firewall.reconcile` | `boolean` | 是 | 示例: false | 应用期间是否必须协调防火墙状态。 |
| `firewall.before` | `object \| null` | 是 | - | 应用前的防火墙状态；初始配置时为 null。 |
| `firewall.after` | `object \| null` | 是 | - | 期望配置要求的防火墙状态；不存在时为 null。 |

##### `401 未认证`

Bearer API key 缺失、格式错误、未知或已删除。

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
| `error` | `object` | 是 | - | 可供程序读取的 API 错误详情。 |
| `error.kind` | `string` | 是 | 示例: "unauthenticated" | 稳定且可供程序读取的错误类型。 |
| `error.message` | `string` | 是 | 示例: "API key is missing or invalid" | 可安全展示的错误消息。 |
| `error.request_id` | `string` | 是 | 格式: uuid；示例: "33ba813e-fc63-4af8-b338-7f7486f82202" | 用于关联请求与服务器日志的 UUID。 |

##### `405 方法不允许`

不接受该 HTTP 方法；请检查 Allow header。

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
| `error` | `object` | 是 | - | 可供程序读取的 API 错误详情。 |
| `error.kind` | `string` | 是 | 示例: "method_not_allowed" | 稳定且可供程序读取的错误类型。 |
| `error.message` | `string` | 是 | 示例: "method is not allowed" | 可安全展示的错误消息。 |
| `error.request_id` | `string` | 是 | 格式: uuid；示例: "33ba813e-fc63-4af8-b338-7f7486f82202" | 用于关联请求与服务器日志的 UUID。 |

##### `409 冲突`

摘要、版本、状态、唯一性、锁或恢复流程发生冲突。

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
| `error` | `object` | 是 | - | 可供程序读取的 API 错误详情。 |
| `error.kind` | `string` | 是 | 示例: "configuration_conflict" | 稳定且可供程序读取的错误类型。 |
| `error.message` | `string` | 是 | 示例: "configuration state changed or is busy" | 可安全展示的错误消息。 |
| `error.request_id` | `string` | 是 | 格式: uuid；示例: "33ba813e-fc63-4af8-b338-7f7486f82202" | 用于关联请求与服务器日志的 UUID。 |

##### `422 无法处理`

JSON 格式有效，但违反客户端、IPv4 或配置语义规则。

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
| `error` | `object` | 是 | - | 可供程序读取的 API 错误详情。 |
| `error.kind` | `string` | 是 | 示例: "invalid_client" | 稳定且可供程序读取的错误类型。 |
| `error.message` | `string` | 是 | 示例: "client request is not valid for the current state" | 可安全展示的错误消息。 |
| `error.request_id` | `string` | 是 | 格式: uuid；示例: "33ba813e-fc63-4af8-b338-7f7486f82202" | 用于关联请求与服务器日志的 UUID。 |

##### `500 服务器内部错误`

未分类的内部错误，不会暴露实现细节。

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
| `error` | `object` | 是 | - | 可供程序读取的 API 错误详情。 |
| `error.kind` | `string` | 是 | 示例: "internal_error" | 稳定且可供程序读取的错误类型。 |
| `error.message` | `string` | 是 | 示例: "request could not be completed" | 可安全展示的错误消息。 |
| `error.request_id` | `string` | 是 | 格式: uuid；示例: "33ba813e-fc63-4af8-b338-7f7486f82202" | 用于关联请求与服务器日志的 UUID。 |

##### `503 服务不可用`

认证存储、PKI、OpenVPN、broker、supervisor 或其他必要依赖不可用。

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
| `error` | `object` | 是 | - | 可供程序读取的 API 错误详情。 |
| `error.kind` | `string` | 是 | 示例: "runtime_unavailable" | 稳定且可供程序读取的错误类型。 |
| `error.message` | `string` | 是 | 示例: "OpenVPN runtime is unavailable" | 可安全展示的错误消息。 |
| `error.request_id` | `string` | 是 | 格式: uuid；示例: "33ba813e-fc63-4af8-b338-7f7486f82202" | 用于关联请求与服务器日志的 UUID。 |

### 22. `POST /api/v1/config/apply`

在线应用当前期望配置。使用 GET/PUT desired 返回的 desired_digest 和 GET plan 返回的 current_revision。OpenVPN 与 broker 重启期间 API 仍保持可用。

#### 请求参数

##### Header 参数

| 字段 | 位置 | 类型 | 必填 | 格式 / 示例 / 约束 | 用途说明 |
|---|---|---|---|---|---|
| `Authorization` | `header` | `string` | 是 | Bearer ovpn_v1.<uuid>.<secret> | API 身份认证凭据。Bearer 后填写服务生成的 API Key。 |
| `Content-Type` | `header` | `string` | 是 | application/json | 声明请求体使用 JSON 格式。 |

##### JSON Body 字段

| 字段 | 位置 | 类型 | 必填 | 格式 / 示例 / 约束 | 用途说明 |
|---|---|---|---|---|---|
| `desired_digest` | `body` | `string` | 是 | 格式: ^[0-9a-f]{64}$；示例: "489b950c0d134b5d7e8f350b2cb4bfa4d6d163d841f840964baf6d74c65cc8ca" | 用于精确标识期望配置的小写 SHA-256 摘要。 |
| `current_revision` | `body` | `integer` | 是 | 最小值: 1；示例: 12 | 配置计划返回的已应用版本号。 |
| `force` | `body` | `boolean` | 否 | 默认值: false；示例: false | 仅绕过已确认的健康预检警告；不会绕过 schema、CAS、锁或恢复检查。 |

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

##### `200 成功`

应用事务及运行时激活结果。

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
| `version` | `integer` | 是 | 固定值: 1 | 响应契约 schema 版本。 |
| `applied` | `boolean` | 是 | 示例: true | 本次请求是否提交了新的已应用版本。 |
| `operation_id` | `string` | 否 | 格式: uuid；示例: "5b781e08-3d44-41fb-81db-42edeedc61e1" | 已提交 journal 操作的 UUID。 |
| `activation` | `object` | 是 | - | 配置提交后执行的运行时激活动作。 |
| `activation.restart_required` | `boolean` | 是 | 示例: true | 应用该计划是否需要重启 OpenVPN。 |
| `activation.runtime_restarted` | `boolean` | 是 | 示例: true | 是否已重启受管 OpenVPN 运行时。 |
| `activation.profile_redistribution` | `object[]` | 是 | 示例: [] | 必须重新分发生成配置文件的客户端。 |
| `activation.profile_redistribution[].id` | `string` | 是 | 格式: uuid | 该对象的稳定标识符。 |
| `activation.profile_redistribution[].name` | `string` | 是 | - | 稳定且可读的名称。 |
| `plan` | `object` | 是 | - | 应用期望配置的完整计划。 |
| `plan.version` | `integer` | 是 | 固定值: 1 | 响应契约 schema 版本。 |
| `plan.instance_id` | `string` | 是 | 格式: uuid；示例: "bbffeb8c-2d11-4613-874d-b5fcc804a608" | 已初始化 OpenVPN 实例的不可变 UUID。 |
| `plan.configuration` | `object` | 是 | - | 版本、摘要、变更和影响的对比结果。 |
| `plan.configuration.initial` | `boolean` | 是 | 示例: false | 该计划是否创建首个已应用版本。 |
| `plan.configuration.current_revision` | `integer` | 是 | 最小值: 0；示例: 12 | 用于比较的当前已应用配置版本。 |
| `plan.configuration.target_revision` | `integer` | 是 | 最小值: 1；示例: 13 | 执行该计划后将产生的已应用版本。 |
| `plan.configuration.current_digest` | `string` | 否 | 格式: ^[0-9a-f]{64}$；示例: "be7ed82796b39a77ce949201a4d9483cf09e56648f14b8b9b192bb83d2c4d042" | 用于精确标识期望配置的小写 SHA-256 摘要。 |
| `plan.configuration.desired_digest` | `string` | 是 | 格式: ^[0-9a-f]{64}$；示例: "489b950c0d134b5d7e8f350b2cb4bfa4d6d163d841f840964baf6d74c65cc8ca" | 用于精确标识期望配置的小写 SHA-256 摘要。 |
| `plan.configuration.in_sync` | `boolean` | 是 | 示例: false | 期望配置与已应用配置是否一致。 |
| `plan.configuration.changes` | `object[]` | 是 | 示例: [] | 该计划中的字段级配置变更。 |
| `plan.configuration.changes[].field` | `string` | 是 | - | 发生变更的配置字段规范点分路径。 |
| `plan.configuration.changes[].before` | `any` | 是 | - | 建议配置变更前的已应用值。 |
| `plan.configuration.changes[].after` | `any` | 是 | - | 建议配置变更后的期望值。 |
| `plan.configuration.impact` | `object` | 是 | - | 配置变更对运行时和制品的影响。 |
| `plan.configuration.impact.restart_required` | `boolean` | 是 | 示例: true | 应用该计划是否需要重启 OpenVPN。 |
| `plan.configuration.impact.address_remap` | `boolean` | 是 | 示例: false | 是否必须重新计算客户端地址分配。 |
| `plan.configuration.impact.firewall_reconcile` | `boolean` | 是 | 示例: false | 是否必须协调防火墙规则。 |
| `plan.configuration.impact.profile_redistribution` | `boolean` | 是 | 示例: false | 必须重新分发生成配置文件的客户端。 |
| `plan.configuration.impact.derived_artifacts` | `string[]` | 是 | 示例: [] | 必须重新生成或删除的派生制品。 |
| `plan.address_changes` | `object[]` | 是 | 示例: [] | 该计划要求执行的客户端地址变更。 |
| `plan.address_changes[].client` | `object` | 是 | - | 计划报告中使用的稳定客户端身份。 |
| `plan.address_changes[].client.id` | `string` | 是 | 格式: uuid | 该对象的稳定标识符。 |
| `plan.address_changes[].client.name` | `string` | 是 | - | 稳定且可读的名称。 |
| `plan.address_changes[].before` | `object` | 是 | - | 某一时刻的客户端 IPv4 分配意图。 |
| `plan.address_changes[].before.mode` | `string` | 是 | - | IPv4 分配模式。 |
| `plan.address_changes[].before.address` | `string \| null` | 是 | 格式: ipv4 | IPv4 地址；未分配地址时为 null。 |
| `plan.address_changes[].before.state` | `string` | 是 | - | 该地址意图的生命周期状态。 |
| `plan.address_changes[].after` | `object` | 是 | - | 某一时刻的客户端 IPv4 分配意图。 |
| `plan.address_changes[].after.mode` | `string` | 是 | - | IPv4 分配模式。 |
| `plan.address_changes[].after.address` | `string \| null` | 是 | 格式: ipv4 | IPv4 地址；未分配地址时为 null。 |
| `plan.address_changes[].after.state` | `string` | 是 | - | 该地址意图的生命周期状态。 |
| `plan.artifacts` | `object[]` | 是 | 示例: [] | 该计划要求执行的派生制品操作。 |
| `plan.artifacts[].owner_kind` | `string` | 是 | - | 拥有该制品的对象类型。 |
| `plan.artifacts[].owner_id` | `string` | 是 | - | 所属对象的稳定标识符。 |
| `plan.artifacts[].kind` | `string` | 是 | - | 派生制品类型。 |
| `plan.artifacts[].key` | `string` | 是 | - | 用于标识派生制品的稳定 key。 |
| `plan.artifacts[].action` | `"regenerate" \| "delete"` | 是 | 可选值: "regenerate" \| "delete" | 重新生成还是删除该制品。 |
| `plan.profile_redistribution` | `object[]` | 是 | 示例: [] | 必须重新分发生成配置文件的客户端。 |
| `plan.profile_redistribution[].id` | `string` | 是 | 格式: uuid | 该对象的稳定标识符。 |
| `plan.profile_redistribution[].name` | `string` | 是 | - | 稳定且可读的名称。 |
| `plan.firewall` | `object` | 是 | - | 防火墙变更前后状态及协调要求。 |
| `plan.firewall.reconcile` | `boolean` | 是 | 示例: false | 应用期间是否必须协调防火墙状态。 |
| `plan.firewall.before` | `object \| null` | 是 | - | 应用前的防火墙状态；初始配置时为 null。 |
| `plan.firewall.after` | `object \| null` | 是 | - | 期望配置要求的防火墙状态；不存在时为 null。 |

##### `400 错误请求`

path、query、header、媒体类型、请求体或 JSON 格式错误。

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
| `error` | `object` | 是 | - | 可供程序读取的 API 错误详情。 |
| `error.kind` | `string` | 是 | 示例: "invalid_json" | 稳定且可供程序读取的错误类型。 |
| `error.message` | `string` | 是 | 示例: "request body is invalid" | 可安全展示的错误消息。 |
| `error.request_id` | `string` | 是 | 格式: uuid；示例: "33ba813e-fc63-4af8-b338-7f7486f82202" | 用于关联请求与服务器日志的 UUID。 |

##### `401 未认证`

Bearer API key 缺失、格式错误、未知或已删除。

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
| `error` | `object` | 是 | - | 可供程序读取的 API 错误详情。 |
| `error.kind` | `string` | 是 | 示例: "unauthenticated" | 稳定且可供程序读取的错误类型。 |
| `error.message` | `string` | 是 | 示例: "API key is missing or invalid" | 可安全展示的错误消息。 |
| `error.request_id` | `string` | 是 | 格式: uuid；示例: "33ba813e-fc63-4af8-b338-7f7486f82202" | 用于关联请求与服务器日志的 UUID。 |

##### `405 方法不允许`

不接受该 HTTP 方法；请检查 Allow header。

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
| `error` | `object` | 是 | - | 可供程序读取的 API 错误详情。 |
| `error.kind` | `string` | 是 | 示例: "method_not_allowed" | 稳定且可供程序读取的错误类型。 |
| `error.message` | `string` | 是 | 示例: "method is not allowed" | 可安全展示的错误消息。 |
| `error.request_id` | `string` | 是 | 格式: uuid；示例: "33ba813e-fc63-4af8-b338-7f7486f82202" | 用于关联请求与服务器日志的 UUID。 |

##### `409 冲突`

摘要、版本、状态、唯一性、锁或恢复流程发生冲突。

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
| `error` | `object` | 是 | - | 可供程序读取的 API 错误详情。 |
| `error.kind` | `string` | 是 | 示例: "configuration_conflict" | 稳定且可供程序读取的错误类型。 |
| `error.message` | `string` | 是 | 示例: "configuration state changed or is busy" | 可安全展示的错误消息。 |
| `error.request_id` | `string` | 是 | 格式: uuid；示例: "33ba813e-fc63-4af8-b338-7f7486f82202" | 用于关联请求与服务器日志的 UUID。 |

##### `422 无法处理`

JSON 格式有效，但违反客户端、IPv4 或配置语义规则。

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
| `error` | `object` | 是 | - | 可供程序读取的 API 错误详情。 |
| `error.kind` | `string` | 是 | 示例: "invalid_client" | 稳定且可供程序读取的错误类型。 |
| `error.message` | `string` | 是 | 示例: "client request is not valid for the current state" | 可安全展示的错误消息。 |
| `error.request_id` | `string` | 是 | 格式: uuid；示例: "33ba813e-fc63-4af8-b338-7f7486f82202" | 用于关联请求与服务器日志的 UUID。 |

##### `500 服务器内部错误`

未分类的内部错误，不会暴露实现细节。

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
| `error` | `object` | 是 | - | 可供程序读取的 API 错误详情。 |
| `error.kind` | `string` | 是 | 示例: "internal_error" | 稳定且可供程序读取的错误类型。 |
| `error.message` | `string` | 是 | 示例: "request could not be completed" | 可安全展示的错误消息。 |
| `error.request_id` | `string` | 是 | 格式: uuid；示例: "33ba813e-fc63-4af8-b338-7f7486f82202" | 用于关联请求与服务器日志的 UUID。 |

##### `503 服务不可用`

认证存储、PKI、OpenVPN、broker、supervisor 或其他必要依赖不可用。

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
| `error` | `object` | 是 | - | 可供程序读取的 API 错误详情。 |
| `error.kind` | `string` | 是 | 示例: "runtime_unavailable" | 稳定且可供程序读取的错误类型。 |
| `error.message` | `string` | 是 | 示例: "OpenVPN runtime is unavailable" | 可安全展示的错误消息。 |
| `error.request_id` | `string` | 是 | 格式: uuid；示例: "33ba813e-fc63-4af8-b338-7f7486f82202" | 用于关联请求与服务器日志的 UUID。 |

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
