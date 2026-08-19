# REST API v1

The REST API provides authenticated remote access to the existing client, configuration, state, and runtime services. It does not replace SQLite authority, the operation journal, runtime locks, or the local `ovpn` CLI.

The API is disabled by default. It uses HTTP internally and does not manage TLS certificates. Put it behind an HTTPS reverse proxy for remote access.

When enabled, the API serves development documentation directly:

- `http://<OVPN_API_LISTEN>/docs/` is the frontend reference. Each of the 22 operations independently shows its complete HTTP request, parameters, body fields, success response, error responses, and JSON examples; developers do not have to assemble a call from grouped models. It follows the browser language by default (Chinese for Chinese browser locales and English otherwise), with a persistent manual switch in the top bar.
- `http://<OVPN_API_LISTEN>/docs/openapi.json` is the OpenAPI 3.1 contract for Orval, OpenAPI Generator, NSwag, and API clients.

Documentation is public because it contains only the API contract. Actual `/api/v1/*` resources still require an API key.

## Enable the API

Set a non-empty listen address on the live `openvpn` service:

```yaml
services:
  openvpn:
    environment:
      OVPN_API_LISTEN: 127.0.0.1:11940
      OVPN_API_CORS_ORIGINS: vpn-admin.example.com
```

`OVPN_API_LISTEN` uses `address:port` format. The port may be any unused host port from `1` through `65535`:

| Example | Exposure |
|---|---|
| `127.0.0.1:11940` | Listens only on the host IPv4 loopback interface and is recommended with a local HTTPS reverse proxy. Replace `11940` with any other unused port. |
| `0.0.0.0:11940` | Listens on every host IPv4 interface. Replace `11940` with any other unused port. LAN or Internet reachability also depends on the host firewall, cloud security groups, and routing. |

The project Compose file uses `network_mode: host`, so this directly occupies a host port; a Compose `ports` mapping is neither required nor a way to resolve a conflict. An empty `OVPN_API_LISTEN` disables the API process. Do not include an `http://` or `https://` scheme, and do not add these variables to `openvpn-maintenance`.

`OVPN_API_CORS_ORIGINS` is optional and allows cross-origin browser frontends to access the API. Values omit the `http://` or `https://` scheme and support these forms:

| Value | Meaning |
|---|---|
| empty | Disables CORS. A same-origin frontend or non-browser client needs no CORS configuration. |
| `vpn-admin.example.com` | Allows the domain with either an HTTP or HTTPS browser origin. |
| `vpn-admin.example.com:3000` | Allows the domain on the specified port. |
| `192.0.2.10` | Allows the IP address. |
| `192.0.2.10:3000` | Allows the IP address on the specified port. |
| `*` | Allows any valid HTTP or HTTPS origin. `*` must be used alone. |
| `vpn-admin.example.com,192.0.2.10:3000` | Uses commas to separate multiple domains, IP addresses, or values with ports. |

Host matching is case-insensitive and ports must match exactly. Do not include a scheme, path, query, credentials, or an empty list entry such as a trailing comma. `*` broadens browser access and should be enabled only when explicitly required.

A minimal Caddy boundary is:

```caddyfile
vpn-admin.example.com {
    reverse_proxy 127.0.0.1:11940
}
```

The reverse proxy must provide HTTPS, preserve the `Authorization` header, and impose appropriate network access controls. The project does not recommend exposing a `0.0.0.0` binding directly to the Internet.

## API keys

API keys can be created, listed, and deleted only through the local CLI:

```bash
docker exec openvpn ovpn api key create frontend-production
docker exec openvpn ovpn api key list
docker exec openvpn ovpn api key delete frontend-production --yes
```

Create prints the complete key once. To avoid terminal history or captured output, write it directly to a new mode-`0600` file:

```bash
docker exec openvpn \
  ovpn api key create frontend-production --output /etc/openvpn/frontend.key
```

The key format is `ovpn_v1.<uuid>.<secret>`. SQLite stores only its SHA-256 digest. Deleting a key invalidates the next request immediately; historical audit records retain the key UUID but no credential material.

Send the key as a Bearer credential:

```bash
curl --fail --silent --show-error \
  -H "Authorization: Bearer $OVPN_API_KEY" \
  https://vpn-admin.example.com/api/v1/state
```

Do not send keys in URLs, query strings, cookies, or request bodies.

## HTTP contract

- Base path: `/api/v1`.
- JSON fields use `snake_case` and UTF-8.
- JSON mutation bodies require `Content-Type: application/json`.
- Unknown fields, duplicate fields, `null`, trailing documents, invalid types, and bodies over 1 MiB are rejected.
- Paths use complete client UUIDs, never mutable client names or UUID prefixes.
- Query parameters are rejected except `lines` on runtime event history.
- Every response includes `X-Request-ID`, `Cache-Control: no-store`, and `X-Content-Type-Options: nosniff`.
- `/healthz` is unauthenticated and reports only API process liveness.
- Profile downloads use `application/x-openvpn-profile` and an attachment filename.

## Per-operation requests and responses

All 22 operations are documented as independent contracts. Every operation lists its Header, Path, Query, and JSON Body parameters with location, type, required status, format, example, constraints, and purpose. Every response status includes its content type, example, and fields. All requests except `/healthz` require a Bearer API key.

### 1. `GET /healthz`

Check API process liveness. Unauthenticated liveness check. It does not report OpenVPN or storage health.

#### Request parameters

This operation has no request parameters or request body.

#### Request example

```http
GET /healthz HTTP/1.1
```

#### Responses

##### `200 OK`

API process is alive.

Content type: `application/json`

Response example:

```json
{
  "status": "ok"
}
```

Response fields:

| Field | Type | Required | Format / example / constraints | Purpose |
|---|---|---|---|---|
| `status` | `string` | yes | constant: "ok" | Liveness state; always ok for a successful response. |

##### `405 Method Not Allowed`

HTTP method is not accepted; inspect the Allow header.

Content type: `application/json`

Response example:

```json
{
  "error": {
    "kind": "method_not_allowed",
    "message": "method is not allowed",
    "request_id": "33ba813e-fc63-4af8-b338-7f7486f82202"
  }
}
```

Response fields:

| Field | Type | Required | Format / example / constraints | Purpose |
|---|---|---|---|---|
| `error` | `object` | yes | - | Machine-readable API error details. |
| `error.kind` | `string` | yes | example: "method_not_allowed" | Stable machine-readable error kind. |
| `error.message` | `string` | yes | example: "method is not allowed" | Safe human-readable error message. |
| `error.request_id` | `string` | yes | format: uuid; example: "33ba813e-fc63-4af8-b338-7f7486f82202" | UUID used to correlate the request with server logs. |

### 2. `GET /api/v1/version`

Read build and compatibility metadata

#### Request parameters

##### Header parameters

| Field | Location | Type | Required | Format / example / constraints | Purpose |
|---|---|---|---|---|---|
| `Authorization` | `header` | `string` | yes | Bearer ovpn_v1.<uuid>.<secret> | API authentication credential. Put the service-generated API key after Bearer. |

#### Request example

```http
GET /api/v1/version HTTP/1.1
Authorization: Bearer ovpn_v1.<uuid>.<secret>
```

#### Responses

##### `200 OK`

Build and compatibility metadata.

Content type: `application/json`

Response example:

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

Response fields:

| Field | Type | Required | Format / example / constraints | Purpose |
|---|---|---|---|---|
| `version` | `string` | yes | example: "4.0.2" | OpenVPN Docker release version. |
| `data_schema` | `integer` | yes | example: 4 | Authoritative data schema version. |
| `commit` | `string` | yes | example: "dd9b5213f5002a7e69f160e3fd2615e0b3d8d224" | Source control revision used to build the binary. |
| `build_date` | `string` | yes | example: "2026-08-19T09:45:40Z" | UTC timestamp when the binary was built. |
| `go_version` | `string` | yes | example: "go1.26.5" | Go toolchain version used to build the binary. |
| `dependencies` | `object` | yes | - | Build-time library dependency versions. |
| `dependencies.sqlite` | `string` | yes | example: "github.com/mattn/go-sqlite3 v1.14.48" | SQLite driver module and version. |
| `dependencies.yaml` | `string` | yes | example: "go.yaml.in/yaml/v3 v3.0.4" | YAML parser module and version. |
| `compatibility` | `object` | yes | - | Runtime compatibility contract and supported versions. |
| `compatibility.contract_version` | `integer` | yes | example: 1 | Compatibility contract schema version. |
| `compatibility.adapter` | `string` | yes | example: "openvpn-2.7" | Compatibility adapter selected for this runtime. |
| `compatibility.template_family` | `string` | yes | example: "openvpn-2.7" | Template family selected for generated OpenVPN files. |
| `compatibility.supported_openvpn_versions` | `string[]` | yes | example: ["2.7.6"] | OpenVPN versions accepted by this compatibility contract. |

##### `401 Unauthorized`

Bearer API key is missing, malformed, unknown, or deleted.

Content type: `application/json`

Response example:

```json
{
  "error": {
    "kind": "unauthenticated",
    "message": "API key is missing or invalid",
    "request_id": "33ba813e-fc63-4af8-b338-7f7486f82202"
  }
}
```

Response fields:

| Field | Type | Required | Format / example / constraints | Purpose |
|---|---|---|---|---|
| `error` | `object` | yes | - | Machine-readable API error details. |
| `error.kind` | `string` | yes | example: "unauthenticated" | Stable machine-readable error kind. |
| `error.message` | `string` | yes | example: "API key is missing or invalid" | Safe human-readable error message. |
| `error.request_id` | `string` | yes | format: uuid; example: "33ba813e-fc63-4af8-b338-7f7486f82202" | UUID used to correlate the request with server logs. |

##### `405 Method Not Allowed`

HTTP method is not accepted; inspect the Allow header.

Content type: `application/json`

Response example:

```json
{
  "error": {
    "kind": "method_not_allowed",
    "message": "method is not allowed",
    "request_id": "33ba813e-fc63-4af8-b338-7f7486f82202"
  }
}
```

Response fields:

| Field | Type | Required | Format / example / constraints | Purpose |
|---|---|---|---|---|
| `error` | `object` | yes | - | Machine-readable API error details. |
| `error.kind` | `string` | yes | example: "method_not_allowed" | Stable machine-readable error kind. |
| `error.message` | `string` | yes | example: "method is not allowed" | Safe human-readable error message. |
| `error.request_id` | `string` | yes | format: uuid; example: "33ba813e-fc63-4af8-b338-7f7486f82202" | UUID used to correlate the request with server logs. |

##### `503 Service Unavailable`

Authentication storage, PKI, OpenVPN, broker, supervisor, or another required dependency is unavailable.

Content type: `application/json`

Response example:

```json
{
  "error": {
    "kind": "runtime_unavailable",
    "message": "OpenVPN runtime is unavailable",
    "request_id": "33ba813e-fc63-4af8-b338-7f7486f82202"
  }
}
```

Response fields:

| Field | Type | Required | Format / example / constraints | Purpose |
|---|---|---|---|---|
| `error` | `object` | yes | - | Machine-readable API error details. |
| `error.kind` | `string` | yes | example: "runtime_unavailable" | Stable machine-readable error kind. |
| `error.message` | `string` | yes | example: "OpenVPN runtime is unavailable" | Safe human-readable error message. |
| `error.request_id` | `string` | yes | format: uuid; example: "33ba813e-fc63-4af8-b338-7f7486f82202" | UUID used to correlate the request with server logs. |

### 3. `GET /api/v1/state`

Read instance health summary

#### Request parameters

##### Header parameters

| Field | Location | Type | Required | Format / example / constraints | Purpose |
|---|---|---|---|---|---|
| `Authorization` | `header` | `string` | yes | Bearer ovpn_v1.<uuid>.<secret> | API authentication credential. Put the service-generated API key after Bearer. |

#### Request example

```http
GET /api/v1/state HTTP/1.1
Authorization: Bearer ovpn_v1.<uuid>.<secret>
```

#### Responses

##### `200 OK`

Instance state summary.

Content type: `application/json`

Response example:

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

Response fields:

| Field | Type | Required | Format / example / constraints | Purpose |
|---|---|---|---|---|
| `version` | `integer` | yes | constant: 1 | Response contract schema version. |
| `state` | `"EMPTY" \| "HEALTHY" \| "DEGRADED_REPAIRABLE" \| "DEGRADED_RECOVERABLE" \| "DEGRADED_REISSUABLE" \| "CRITICAL" \| "UNRECOVERABLE"` | yes | values: "EMPTY" \| "HEALTHY" \| "DEGRADED_REPAIRABLE" \| "DEGRADED_RECOVERABLE" \| "DEGRADED_REISSUABLE" \| "CRITICAL" \| "UNRECOVERABLE" | Overall authoritative instance health state. |
| `data_schema` | `integer` | yes | example: 4 | Authoritative data schema version. |
| `instance_id` | `string` | no | format: uuid; example: "bbffeb8c-2d11-4613-874d-b5fcc804a608" | Immutable UUID of the initialized OpenVPN instance. |
| `revision` | `integer` | no | minimum: 1; example: 12 | Monotonic applied configuration revision. |
| `scanned_at` | `string` | yes | format: date-time; example: "2026-08-19T09:55:07Z" | UTC timestamp of the state scan. |
| `issue_count` | `integer` | yes | minimum: 0; example: 0 | Number of diagnostic issues currently detected. |
| `pending_operation_count` | `integer` | yes | minimum: 0; example: 0 | Number of incomplete journal operations. |
| `issues` | `object[]` | no | - | Detailed diagnostic issues; omitted when empty. |
| `issues[].id` | `string` | no | - | Stable diagnostic issue identifier. |
| `issues[].severity` | `"repairable" \| "recoverable" \| "reissuable" \| "critical" \| "unrecoverable"` | no | values: "repairable" \| "recoverable" \| "reissuable" \| "critical" \| "unrecoverable" | Diagnostic severity and recovery class. |
| `issues[].action` | `string` | no | - | Recommended operator action for this issue. |
| `issues[].target` | `string` | no | - | Affected path or resource when applicable. |
| `issues[].owner_id` | `string` | no | - | Stable identifier of the owning object. |
| `issues[].artifact_kind` | `string` | no | - | Kind of affected derived artifact. |
| `issues[].detail` | `string` | no | - | Human-readable diagnostic detail. |

##### `401 Unauthorized`

Bearer API key is missing, malformed, unknown, or deleted.

Content type: `application/json`

Response example:

```json
{
  "error": {
    "kind": "unauthenticated",
    "message": "API key is missing or invalid",
    "request_id": "33ba813e-fc63-4af8-b338-7f7486f82202"
  }
}
```

Response fields:

| Field | Type | Required | Format / example / constraints | Purpose |
|---|---|---|---|---|
| `error` | `object` | yes | - | Machine-readable API error details. |
| `error.kind` | `string` | yes | example: "unauthenticated" | Stable machine-readable error kind. |
| `error.message` | `string` | yes | example: "API key is missing or invalid" | Safe human-readable error message. |
| `error.request_id` | `string` | yes | format: uuid; example: "33ba813e-fc63-4af8-b338-7f7486f82202" | UUID used to correlate the request with server logs. |

##### `405 Method Not Allowed`

HTTP method is not accepted; inspect the Allow header.

Content type: `application/json`

Response example:

```json
{
  "error": {
    "kind": "method_not_allowed",
    "message": "method is not allowed",
    "request_id": "33ba813e-fc63-4af8-b338-7f7486f82202"
  }
}
```

Response fields:

| Field | Type | Required | Format / example / constraints | Purpose |
|---|---|---|---|---|
| `error` | `object` | yes | - | Machine-readable API error details. |
| `error.kind` | `string` | yes | example: "method_not_allowed" | Stable machine-readable error kind. |
| `error.message` | `string` | yes | example: "method is not allowed" | Safe human-readable error message. |
| `error.request_id` | `string` | yes | format: uuid; example: "33ba813e-fc63-4af8-b338-7f7486f82202" | UUID used to correlate the request with server logs. |

##### `500 Internal Server Error`

Unclassified internal failure with no implementation details exposed.

Content type: `application/json`

Response example:

```json
{
  "error": {
    "kind": "internal_error",
    "message": "request could not be completed",
    "request_id": "33ba813e-fc63-4af8-b338-7f7486f82202"
  }
}
```

Response fields:

| Field | Type | Required | Format / example / constraints | Purpose |
|---|---|---|---|---|
| `error` | `object` | yes | - | Machine-readable API error details. |
| `error.kind` | `string` | yes | example: "internal_error" | Stable machine-readable error kind. |
| `error.message` | `string` | yes | example: "request could not be completed" | Safe human-readable error message. |
| `error.request_id` | `string` | yes | format: uuid; example: "33ba813e-fc63-4af8-b338-7f7486f82202" | UUID used to correlate the request with server logs. |

##### `503 Service Unavailable`

Authentication storage, PKI, OpenVPN, broker, supervisor, or another required dependency is unavailable.

Content type: `application/json`

Response example:

```json
{
  "error": {
    "kind": "runtime_unavailable",
    "message": "OpenVPN runtime is unavailable",
    "request_id": "33ba813e-fc63-4af8-b338-7f7486f82202"
  }
}
```

Response fields:

| Field | Type | Required | Format / example / constraints | Purpose |
|---|---|---|---|---|
| `error` | `object` | yes | - | Machine-readable API error details. |
| `error.kind` | `string` | yes | example: "runtime_unavailable" | Stable machine-readable error kind. |
| `error.message` | `string` | yes | example: "OpenVPN runtime is unavailable" | Safe human-readable error message. |
| `error.request_id` | `string` | yes | format: uuid; example: "33ba813e-fc63-4af8-b338-7f7486f82202" | UUID used to correlate the request with server logs. |

### 4. `GET /api/v1/state/doctor`

Read detailed instance diagnostics. Returns the state summary plus an issues array when diagnostic issues exist.

#### Request parameters

##### Header parameters

| Field | Location | Type | Required | Format / example / constraints | Purpose |
|---|---|---|---|---|---|
| `Authorization` | `header` | `string` | yes | Bearer ovpn_v1.<uuid>.<secret> | API authentication credential. Put the service-generated API key after Bearer. |

#### Request example

```http
GET /api/v1/state/doctor HTTP/1.1
Authorization: Bearer ovpn_v1.<uuid>.<secret>
```

#### Responses

##### `200 OK`

Detailed instance diagnostics. `issues` is omitted when empty.

Content type: `application/json`

Response example:

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

Response fields:

| Field | Type | Required | Format / example / constraints | Purpose |
|---|---|---|---|---|
| `version` | `integer` | yes | constant: 1 | Response contract schema version. |
| `state` | `"EMPTY" \| "HEALTHY" \| "DEGRADED_REPAIRABLE" \| "DEGRADED_RECOVERABLE" \| "DEGRADED_REISSUABLE" \| "CRITICAL" \| "UNRECOVERABLE"` | yes | values: "EMPTY" \| "HEALTHY" \| "DEGRADED_REPAIRABLE" \| "DEGRADED_RECOVERABLE" \| "DEGRADED_REISSUABLE" \| "CRITICAL" \| "UNRECOVERABLE" | Overall authoritative instance health state. |
| `data_schema` | `integer` | yes | example: 4 | Authoritative data schema version. |
| `instance_id` | `string` | no | format: uuid; example: "bbffeb8c-2d11-4613-874d-b5fcc804a608" | Immutable UUID of the initialized OpenVPN instance. |
| `revision` | `integer` | no | minimum: 1; example: 12 | Monotonic applied configuration revision. |
| `scanned_at` | `string` | yes | format: date-time; example: "2026-08-19T09:55:07Z" | UTC timestamp of the state scan. |
| `issue_count` | `integer` | yes | minimum: 0; example: 1 | Number of diagnostic issues currently detected. |
| `pending_operation_count` | `integer` | yes | minimum: 0; example: 0 | Number of incomplete journal operations. |
| `issues` | `object[]` | no | example: [{"id":"DECLARATIVE_CONFIG_UNAVAILABLE","severity":"repairable","action":"export-config","target":"/etc/ovpn-conf/config.yaml","detail":"declarative configuration is unavailable"}] | Detailed diagnostic issues; omitted when empty. |
| `issues[].id` | `string` | no | example: "DECLARATIVE_CONFIG_UNAVAILABLE" | Stable diagnostic issue identifier. |
| `issues[].severity` | `"repairable" \| "recoverable" \| "reissuable" \| "critical" \| "unrecoverable"` | no | values: "repairable" \| "recoverable" \| "reissuable" \| "critical" \| "unrecoverable" | Diagnostic severity and recovery class. |
| `issues[].action` | `string` | no | example: "export-config" | Recommended operator action for this issue. |
| `issues[].target` | `string` | no | example: "/etc/ovpn-conf/config.yaml" | Affected path or resource when applicable. |
| `issues[].owner_id` | `string` | no | - | Stable identifier of the owning object. |
| `issues[].artifact_kind` | `string` | no | - | Kind of affected derived artifact. |
| `issues[].detail` | `string` | no | example: "declarative configuration is unavailable" | Human-readable diagnostic detail. |

##### `401 Unauthorized`

Bearer API key is missing, malformed, unknown, or deleted.

Content type: `application/json`

Response example:

```json
{
  "error": {
    "kind": "unauthenticated",
    "message": "API key is missing or invalid",
    "request_id": "33ba813e-fc63-4af8-b338-7f7486f82202"
  }
}
```

Response fields:

| Field | Type | Required | Format / example / constraints | Purpose |
|---|---|---|---|---|
| `error` | `object` | yes | - | Machine-readable API error details. |
| `error.kind` | `string` | yes | example: "unauthenticated" | Stable machine-readable error kind. |
| `error.message` | `string` | yes | example: "API key is missing or invalid" | Safe human-readable error message. |
| `error.request_id` | `string` | yes | format: uuid; example: "33ba813e-fc63-4af8-b338-7f7486f82202" | UUID used to correlate the request with server logs. |

##### `405 Method Not Allowed`

HTTP method is not accepted; inspect the Allow header.

Content type: `application/json`

Response example:

```json
{
  "error": {
    "kind": "method_not_allowed",
    "message": "method is not allowed",
    "request_id": "33ba813e-fc63-4af8-b338-7f7486f82202"
  }
}
```

Response fields:

| Field | Type | Required | Format / example / constraints | Purpose |
|---|---|---|---|---|
| `error` | `object` | yes | - | Machine-readable API error details. |
| `error.kind` | `string` | yes | example: "method_not_allowed" | Stable machine-readable error kind. |
| `error.message` | `string` | yes | example: "method is not allowed" | Safe human-readable error message. |
| `error.request_id` | `string` | yes | format: uuid; example: "33ba813e-fc63-4af8-b338-7f7486f82202" | UUID used to correlate the request with server logs. |

##### `500 Internal Server Error`

Unclassified internal failure with no implementation details exposed.

Content type: `application/json`

Response example:

```json
{
  "error": {
    "kind": "internal_error",
    "message": "request could not be completed",
    "request_id": "33ba813e-fc63-4af8-b338-7f7486f82202"
  }
}
```

Response fields:

| Field | Type | Required | Format / example / constraints | Purpose |
|---|---|---|---|---|
| `error` | `object` | yes | - | Machine-readable API error details. |
| `error.kind` | `string` | yes | example: "internal_error" | Stable machine-readable error kind. |
| `error.message` | `string` | yes | example: "request could not be completed" | Safe human-readable error message. |
| `error.request_id` | `string` | yes | format: uuid; example: "33ba813e-fc63-4af8-b338-7f7486f82202" | UUID used to correlate the request with server logs. |

##### `503 Service Unavailable`

Authentication storage, PKI, OpenVPN, broker, supervisor, or another required dependency is unavailable.

Content type: `application/json`

Response example:

```json
{
  "error": {
    "kind": "runtime_unavailable",
    "message": "OpenVPN runtime is unavailable",
    "request_id": "33ba813e-fc63-4af8-b338-7f7486f82202"
  }
}
```

Response fields:

| Field | Type | Required | Format / example / constraints | Purpose |
|---|---|---|---|---|
| `error` | `object` | yes | - | Machine-readable API error details. |
| `error.kind` | `string` | yes | example: "runtime_unavailable" | Stable machine-readable error kind. |
| `error.message` | `string` | yes | example: "OpenVPN runtime is unavailable" | Safe human-readable error message. |
| `error.request_id` | `string` | yes | format: uuid; example: "33ba813e-fc63-4af8-b338-7f7486f82202" | UUID used to correlate the request with server logs. |

### 5. `GET /api/v1/clients`

List active, revoked, and deleted clients

#### Request parameters

##### Header parameters

| Field | Location | Type | Required | Format / example / constraints | Purpose |
|---|---|---|---|---|---|
| `Authorization` | `header` | `string` | yes | Bearer ovpn_v1.<uuid>.<secret> | API authentication credential. Put the service-generated API key after Bearer. |

#### Request example

```http
GET /api/v1/clients HTTP/1.1
Authorization: Bearer ovpn_v1.<uuid>.<secret>
```

#### Responses

##### `200 OK`

Client list.

Content type: `application/json`

Response example:

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

Response fields:

| Field | Type | Required | Format / example / constraints | Purpose |
|---|---|---|---|---|
| `version` | `integer` | yes | constant: 1 | Response contract schema version. |
| `clients` | `object[]` | yes | example: [{"id":"c0d4f871-6ea6-42b3-9e7b-f00cc1ec354e","name":"alice-laptop","status":"active","ipv4":{"mode":"static","address":"10.42.0.30","state":"configured"},"connection":"connected"}] | Clients included in this response. |
| `clients[].id` | `string` | yes | format: uuid; example: "c0d4f871-6ea6-42b3-9e7b-f00cc1ec354e" | Stable identifier of this object. |
| `clients[].name` | `string` | yes | format: ^[A-Za-z0-9][A-Za-z0-9_.-]*$; example: "alice-laptop" | Stable human-readable name. |
| `clients[].status` | `"active" \| "revoked" \| "deleted"` | yes | values: "active" \| "revoked" \| "deleted" | Client credential lifecycle status. |
| `clients[].ipv4` | `object` | yes | - | Current client IPv4 assignment view. |
| `clients[].ipv4.mode` | `"none" \| "static" \| "dynamic"` | yes | values: "none" \| "static" \| "dynamic" | IPv4 allocation mode. |
| `clients[].ipv4.address` | `string \| null` | yes | format: ipv4; example: "10.42.0.30" | IPv4 address, or null when no address is assigned. |
| `clients[].ipv4.state` | `"configured" \| "retained" \| "unavailable"` | yes | values: "configured" \| "retained" \| "unavailable" | Availability state of the client address assignment. |
| `clients[].connection` | `string` | no | example: "connected" | Current connection state when runtime data is available. |

##### `401 Unauthorized`

Bearer API key is missing, malformed, unknown, or deleted.

Content type: `application/json`

Response example:

```json
{
  "error": {
    "kind": "unauthenticated",
    "message": "API key is missing or invalid",
    "request_id": "33ba813e-fc63-4af8-b338-7f7486f82202"
  }
}
```

Response fields:

| Field | Type | Required | Format / example / constraints | Purpose |
|---|---|---|---|---|
| `error` | `object` | yes | - | Machine-readable API error details. |
| `error.kind` | `string` | yes | example: "unauthenticated" | Stable machine-readable error kind. |
| `error.message` | `string` | yes | example: "API key is missing or invalid" | Safe human-readable error message. |
| `error.request_id` | `string` | yes | format: uuid; example: "33ba813e-fc63-4af8-b338-7f7486f82202" | UUID used to correlate the request with server logs. |

##### `405 Method Not Allowed`

HTTP method is not accepted; inspect the Allow header.

Content type: `application/json`

Response example:

```json
{
  "error": {
    "kind": "method_not_allowed",
    "message": "method is not allowed",
    "request_id": "33ba813e-fc63-4af8-b338-7f7486f82202"
  }
}
```

Response fields:

| Field | Type | Required | Format / example / constraints | Purpose |
|---|---|---|---|---|
| `error` | `object` | yes | - | Machine-readable API error details. |
| `error.kind` | `string` | yes | example: "method_not_allowed" | Stable machine-readable error kind. |
| `error.message` | `string` | yes | example: "method is not allowed" | Safe human-readable error message. |
| `error.request_id` | `string` | yes | format: uuid; example: "33ba813e-fc63-4af8-b338-7f7486f82202" | UUID used to correlate the request with server logs. |

##### `500 Internal Server Error`

Unclassified internal failure with no implementation details exposed.

Content type: `application/json`

Response example:

```json
{
  "error": {
    "kind": "internal_error",
    "message": "request could not be completed",
    "request_id": "33ba813e-fc63-4af8-b338-7f7486f82202"
  }
}
```

Response fields:

| Field | Type | Required | Format / example / constraints | Purpose |
|---|---|---|---|---|
| `error` | `object` | yes | - | Machine-readable API error details. |
| `error.kind` | `string` | yes | example: "internal_error" | Stable machine-readable error kind. |
| `error.message` | `string` | yes | example: "request could not be completed" | Safe human-readable error message. |
| `error.request_id` | `string` | yes | format: uuid; example: "33ba813e-fc63-4af8-b338-7f7486f82202" | UUID used to correlate the request with server logs. |

##### `503 Service Unavailable`

Authentication storage, PKI, OpenVPN, broker, supervisor, or another required dependency is unavailable.

Content type: `application/json`

Response example:

```json
{
  "error": {
    "kind": "runtime_unavailable",
    "message": "OpenVPN runtime is unavailable",
    "request_id": "33ba813e-fc63-4af8-b338-7f7486f82202"
  }
}
```

Response fields:

| Field | Type | Required | Format / example / constraints | Purpose |
|---|---|---|---|---|
| `error` | `object` | yes | - | Machine-readable API error details. |
| `error.kind` | `string` | yes | example: "runtime_unavailable" | Stable machine-readable error kind. |
| `error.message` | `string` | yes | example: "OpenVPN runtime is unavailable" | Safe human-readable error message. |
| `error.request_id` | `string` | yes | format: uuid; example: "33ba813e-fc63-4af8-b338-7f7486f82202" | UUID used to correlate the request with server logs. |

### 6. `POST /api/v1/clients`

Create client credentials and profile

#### Request parameters

##### Header parameters

| Field | Location | Type | Required | Format / example / constraints | Purpose |
|---|---|---|---|---|---|
| `Authorization` | `header` | `string` | yes | Bearer ovpn_v1.<uuid>.<secret> | API authentication credential. Put the service-generated API key after Bearer. |
| `Content-Type` | `header` | `string` | yes | application/json | Declares that the request body uses JSON. |

##### JSON body fields

| Field | Location | Type | Required | Format / example / constraints | Purpose |
|---|---|---|---|---|---|
| `name` | `body` | `string` | yes | format: ^[A-Za-z0-9][A-Za-z0-9_.-]*$; example: "alice-laptop" | Stable client name. Starts with an alphanumeric character and may contain letters, digits, underscores, dots, and hyphens. |
| `ipv4` | `body` | `string` | yes | example: "auto" | IPv4 allocation intent: auto, dynamic, or a valid static IPv4 address. |

#### Request example

```http
POST /api/v1/clients HTTP/1.1
Authorization: Bearer ovpn_v1.<uuid>.<secret>
Content-Type: application/json

{
  "name": "alice-laptop",
  "ipv4": "auto"
}
```

#### Responses

##### `201 Created`

Client created.

Content type: `application/json`

Response example:

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

Response fields:

| Field | Type | Required | Format / example / constraints | Purpose |
|---|---|---|---|---|
| `version` | `integer` | yes | constant: 1 | Response contract schema version. |
| `operation_id` | `string` | yes | format: uuid; example: "7a21e1f8-1ce1-4694-977f-b275eadb8651" | UUID of the committed journal operation. |
| `client` | `object` | yes | - | Authoritative client identity and lifecycle state. |
| `client.id` | `string` | yes | format: uuid; example: "c0d4f871-6ea6-42b3-9e7b-f00cc1ec354e" | Stable identifier of this object. |
| `client.name` | `string` | yes | format: ^[A-Za-z0-9][A-Za-z0-9_.-]*$; example: "alice-laptop" | Stable human-readable name. |
| `client.status` | `"active" \| "revoked" \| "deleted"` | yes | values: "active" \| "revoked" \| "deleted" | Client credential lifecycle status. |
| `client.ipv4` | `object` | yes | - | Current client IPv4 assignment view. |
| `client.ipv4.mode` | `"none" \| "static" \| "dynamic"` | yes | values: "none" \| "static" \| "dynamic" | IPv4 allocation mode. |
| `client.ipv4.address` | `string \| null` | yes | format: ipv4; example: "10.42.0.2" | IPv4 address, or null when no address is assigned. |
| `client.ipv4.state` | `"configured" \| "retained" \| "unavailable"` | yes | values: "configured" \| "retained" \| "unavailable" | Availability state of the client address assignment. |
| `client.connection` | `string` | no | - | Current connection state when runtime data is available. |
| `kick_required` | `boolean` | yes | example: false | Whether the mutated client's active session must be disconnected. |
| `profile_redistribution_required` | `boolean` | yes | example: true | Whether the client's generated profile must be redistributed. |
| `runtime` | `object` | no | - | Per-client runtime convergence result after a mutation. |
| `runtime.client_id` | `string` | no | format: uuid | Immutable client UUID. |
| `runtime.status` | `"ok" \| "unavailable"` | no | values: "ok" \| "unavailable" | Whether the requested runtime action completed or was unavailable. |
| `runtime.result` | `object` | no | - | Result of requesting client session disconnection. |
| `runtime.result.version` | `integer` | no | constant: 1 | Response contract schema version. |
| `runtime.result.client_id` | `string` | no | format: uuid | Immutable client UUID. |
| `runtime.result.client_name` | `string` | no | - | Human-readable client name associated with the session. |
| `runtime.result.was_connected` | `boolean` | no | - | Whether a matching session existed before the request. |
| `runtime.result.disconnected` | `boolean` | no | - | Whether at least one matching session was disconnected. |
| `runtime.result.connections` | `integer` | no | minimum: 0 | Number of matching sessions processed by the disconnect request. |

##### `400 Bad Request`

Malformed path, query, header, media type, body, or JSON.

Content type: `application/json`

Response example:

```json
{
  "error": {
    "kind": "invalid_json",
    "message": "request body is invalid",
    "request_id": "33ba813e-fc63-4af8-b338-7f7486f82202"
  }
}
```

Response fields:

| Field | Type | Required | Format / example / constraints | Purpose |
|---|---|---|---|---|
| `error` | `object` | yes | - | Machine-readable API error details. |
| `error.kind` | `string` | yes | example: "invalid_json" | Stable machine-readable error kind. |
| `error.message` | `string` | yes | example: "request body is invalid" | Safe human-readable error message. |
| `error.request_id` | `string` | yes | format: uuid; example: "33ba813e-fc63-4af8-b338-7f7486f82202" | UUID used to correlate the request with server logs. |

##### `401 Unauthorized`

Bearer API key is missing, malformed, unknown, or deleted.

Content type: `application/json`

Response example:

```json
{
  "error": {
    "kind": "unauthenticated",
    "message": "API key is missing or invalid",
    "request_id": "33ba813e-fc63-4af8-b338-7f7486f82202"
  }
}
```

Response fields:

| Field | Type | Required | Format / example / constraints | Purpose |
|---|---|---|---|---|
| `error` | `object` | yes | - | Machine-readable API error details. |
| `error.kind` | `string` | yes | example: "unauthenticated" | Stable machine-readable error kind. |
| `error.message` | `string` | yes | example: "API key is missing or invalid" | Safe human-readable error message. |
| `error.request_id` | `string` | yes | format: uuid; example: "33ba813e-fc63-4af8-b338-7f7486f82202" | UUID used to correlate the request with server logs. |

##### `409 Conflict`

Digest, revision, state, uniqueness, lock, or recovery conflict.

Content type: `application/json`

Response example:

```json
{
  "error": {
    "kind": "configuration_conflict",
    "message": "configuration state changed or is busy",
    "request_id": "33ba813e-fc63-4af8-b338-7f7486f82202"
  }
}
```

Response fields:

| Field | Type | Required | Format / example / constraints | Purpose |
|---|---|---|---|---|
| `error` | `object` | yes | - | Machine-readable API error details. |
| `error.kind` | `string` | yes | example: "configuration_conflict" | Stable machine-readable error kind. |
| `error.message` | `string` | yes | example: "configuration state changed or is busy" | Safe human-readable error message. |
| `error.request_id` | `string` | yes | format: uuid; example: "33ba813e-fc63-4af8-b338-7f7486f82202" | UUID used to correlate the request with server logs. |

##### `422 Unprocessable Entity`

JSON is valid but violates client, IPv4, or configuration semantics.

Content type: `application/json`

Response example:

```json
{
  "error": {
    "kind": "invalid_client",
    "message": "client request is not valid for the current state",
    "request_id": "33ba813e-fc63-4af8-b338-7f7486f82202"
  }
}
```

Response fields:

| Field | Type | Required | Format / example / constraints | Purpose |
|---|---|---|---|---|
| `error` | `object` | yes | - | Machine-readable API error details. |
| `error.kind` | `string` | yes | example: "invalid_client" | Stable machine-readable error kind. |
| `error.message` | `string` | yes | example: "client request is not valid for the current state" | Safe human-readable error message. |
| `error.request_id` | `string` | yes | format: uuid; example: "33ba813e-fc63-4af8-b338-7f7486f82202" | UUID used to correlate the request with server logs. |

##### `500 Internal Server Error`

Unclassified internal failure with no implementation details exposed.

Content type: `application/json`

Response example:

```json
{
  "error": {
    "kind": "internal_error",
    "message": "request could not be completed",
    "request_id": "33ba813e-fc63-4af8-b338-7f7486f82202"
  }
}
```

Response fields:

| Field | Type | Required | Format / example / constraints | Purpose |
|---|---|---|---|---|
| `error` | `object` | yes | - | Machine-readable API error details. |
| `error.kind` | `string` | yes | example: "internal_error" | Stable machine-readable error kind. |
| `error.message` | `string` | yes | example: "request could not be completed" | Safe human-readable error message. |
| `error.request_id` | `string` | yes | format: uuid; example: "33ba813e-fc63-4af8-b338-7f7486f82202" | UUID used to correlate the request with server logs. |

##### `503 Service Unavailable`

Authentication storage, PKI, OpenVPN, broker, supervisor, or another required dependency is unavailable.

Content type: `application/json`

Response example:

```json
{
  "error": {
    "kind": "runtime_unavailable",
    "message": "OpenVPN runtime is unavailable",
    "request_id": "33ba813e-fc63-4af8-b338-7f7486f82202"
  }
}
```

Response fields:

| Field | Type | Required | Format / example / constraints | Purpose |
|---|---|---|---|---|
| `error` | `object` | yes | - | Machine-readable API error details. |
| `error.kind` | `string` | yes | example: "runtime_unavailable" | Stable machine-readable error kind. |
| `error.message` | `string` | yes | example: "OpenVPN runtime is unavailable" | Safe human-readable error message. |
| `error.request_id` | `string` | yes | format: uuid; example: "33ba813e-fc63-4af8-b338-7f7486f82202" | UUID used to correlate the request with server logs. |

### 7. `GET /api/v1/clients/{client_id}`

Read one client

#### Request parameters

##### Header parameters

| Field | Location | Type | Required | Format / example / constraints | Purpose |
|---|---|---|---|---|---|
| `Authorization` | `header` | `string` | yes | Bearer ovpn_v1.<uuid>.<secret> | API authentication credential. Put the service-generated API key after Bearer. |

##### Path parameters

| Field | Location | Type | Required | Format / example / constraints | Purpose |
|---|---|---|---|---|---|
| `client_id` | `path` | `string` | yes | format: uuid; example: "c0d4f871-6ea6-42b3-9e7b-f00cc1ec354e" | Complete canonical immutable client UUID; names and UUID prefixes are rejected. |

#### Request example

```http
GET /api/v1/clients/c0d4f871-6ea6-42b3-9e7b-f00cc1ec354e HTTP/1.1
Authorization: Bearer ovpn_v1.<uuid>.<secret>
```

#### Responses

##### `200 OK`

Client detail.

Content type: `application/json`

Response example:

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

Response fields:

| Field | Type | Required | Format / example / constraints | Purpose |
|---|---|---|---|---|
| `id` | `string` | yes | format: uuid; example: "c0d4f871-6ea6-42b3-9e7b-f00cc1ec354e" | Stable identifier of this object. |
| `name` | `string` | yes | format: ^[A-Za-z0-9][A-Za-z0-9_.-]*$; example: "alice-laptop" | Stable human-readable name. |
| `status` | `"active" \| "revoked" \| "deleted"` | yes | values: "active" \| "revoked" \| "deleted" | Client credential lifecycle status. |
| `ipv4` | `object` | yes | - | Current client IPv4 assignment view. |
| `ipv4.mode` | `"none" \| "static" \| "dynamic"` | yes | values: "none" \| "static" \| "dynamic" | IPv4 allocation mode. |
| `ipv4.address` | `string \| null` | yes | format: ipv4; example: "10.42.0.30" | IPv4 address, or null when no address is assigned. |
| `ipv4.state` | `"configured" \| "retained" \| "unavailable"` | yes | values: "configured" \| "retained" \| "unavailable" | Availability state of the client address assignment. |
| `connection` | `string` | no | - | Current connection state when runtime data is available. |

##### `400 Bad Request`

Malformed path, query, header, media type, body, or JSON.

Content type: `application/json`

Response example:

```json
{
  "error": {
    "kind": "invalid_json",
    "message": "request body is invalid",
    "request_id": "33ba813e-fc63-4af8-b338-7f7486f82202"
  }
}
```

Response fields:

| Field | Type | Required | Format / example / constraints | Purpose |
|---|---|---|---|---|
| `error` | `object` | yes | - | Machine-readable API error details. |
| `error.kind` | `string` | yes | example: "invalid_json" | Stable machine-readable error kind. |
| `error.message` | `string` | yes | example: "request body is invalid" | Safe human-readable error message. |
| `error.request_id` | `string` | yes | format: uuid; example: "33ba813e-fc63-4af8-b338-7f7486f82202" | UUID used to correlate the request with server logs. |

##### `401 Unauthorized`

Bearer API key is missing, malformed, unknown, or deleted.

Content type: `application/json`

Response example:

```json
{
  "error": {
    "kind": "unauthenticated",
    "message": "API key is missing or invalid",
    "request_id": "33ba813e-fc63-4af8-b338-7f7486f82202"
  }
}
```

Response fields:

| Field | Type | Required | Format / example / constraints | Purpose |
|---|---|---|---|---|
| `error` | `object` | yes | - | Machine-readable API error details. |
| `error.kind` | `string` | yes | example: "unauthenticated" | Stable machine-readable error kind. |
| `error.message` | `string` | yes | example: "API key is missing or invalid" | Safe human-readable error message. |
| `error.request_id` | `string` | yes | format: uuid; example: "33ba813e-fc63-4af8-b338-7f7486f82202" | UUID used to correlate the request with server logs. |

##### `404 Not Found`

Resource or client does not exist.

Content type: `application/json`

Response example:

```json
{
  "error": {
    "kind": "client_not_found",
    "message": "client was not found",
    "request_id": "33ba813e-fc63-4af8-b338-7f7486f82202"
  }
}
```

Response fields:

| Field | Type | Required | Format / example / constraints | Purpose |
|---|---|---|---|---|
| `error` | `object` | yes | - | Machine-readable API error details. |
| `error.kind` | `string` | yes | example: "client_not_found" | Stable machine-readable error kind. |
| `error.message` | `string` | yes | example: "client was not found" | Safe human-readable error message. |
| `error.request_id` | `string` | yes | format: uuid; example: "33ba813e-fc63-4af8-b338-7f7486f82202" | UUID used to correlate the request with server logs. |

##### `405 Method Not Allowed`

HTTP method is not accepted; inspect the Allow header.

Content type: `application/json`

Response example:

```json
{
  "error": {
    "kind": "method_not_allowed",
    "message": "method is not allowed",
    "request_id": "33ba813e-fc63-4af8-b338-7f7486f82202"
  }
}
```

Response fields:

| Field | Type | Required | Format / example / constraints | Purpose |
|---|---|---|---|---|
| `error` | `object` | yes | - | Machine-readable API error details. |
| `error.kind` | `string` | yes | example: "method_not_allowed" | Stable machine-readable error kind. |
| `error.message` | `string` | yes | example: "method is not allowed" | Safe human-readable error message. |
| `error.request_id` | `string` | yes | format: uuid; example: "33ba813e-fc63-4af8-b338-7f7486f82202" | UUID used to correlate the request with server logs. |

##### `500 Internal Server Error`

Unclassified internal failure with no implementation details exposed.

Content type: `application/json`

Response example:

```json
{
  "error": {
    "kind": "internal_error",
    "message": "request could not be completed",
    "request_id": "33ba813e-fc63-4af8-b338-7f7486f82202"
  }
}
```

Response fields:

| Field | Type | Required | Format / example / constraints | Purpose |
|---|---|---|---|---|
| `error` | `object` | yes | - | Machine-readable API error details. |
| `error.kind` | `string` | yes | example: "internal_error" | Stable machine-readable error kind. |
| `error.message` | `string` | yes | example: "request could not be completed" | Safe human-readable error message. |
| `error.request_id` | `string` | yes | format: uuid; example: "33ba813e-fc63-4af8-b338-7f7486f82202" | UUID used to correlate the request with server logs. |

##### `503 Service Unavailable`

Authentication storage, PKI, OpenVPN, broker, supervisor, or another required dependency is unavailable.

Content type: `application/json`

Response example:

```json
{
  "error": {
    "kind": "runtime_unavailable",
    "message": "OpenVPN runtime is unavailable",
    "request_id": "33ba813e-fc63-4af8-b338-7f7486f82202"
  }
}
```

Response fields:

| Field | Type | Required | Format / example / constraints | Purpose |
|---|---|---|---|---|
| `error` | `object` | yes | - | Machine-readable API error details. |
| `error.kind` | `string` | yes | example: "runtime_unavailable" | Stable machine-readable error kind. |
| `error.message` | `string` | yes | example: "OpenVPN runtime is unavailable" | Safe human-readable error message. |
| `error.request_id` | `string` | yes | format: uuid; example: "33ba813e-fc63-4af8-b338-7f7486f82202" | UUID used to correlate the request with server logs. |

### 8. `PATCH /api/v1/clients/{client_id}`

Rename a client

#### Request parameters

##### Header parameters

| Field | Location | Type | Required | Format / example / constraints | Purpose |
|---|---|---|---|---|---|
| `Authorization` | `header` | `string` | yes | Bearer ovpn_v1.<uuid>.<secret> | API authentication credential. Put the service-generated API key after Bearer. |
| `Content-Type` | `header` | `string` | yes | application/json | Declares that the request body uses JSON. |

##### Path parameters

| Field | Location | Type | Required | Format / example / constraints | Purpose |
|---|---|---|---|---|---|
| `client_id` | `path` | `string` | yes | format: uuid; example: "c0d4f871-6ea6-42b3-9e7b-f00cc1ec354e" | Complete canonical immutable client UUID; names and UUID prefixes are rejected. |

##### JSON body fields

| Field | Location | Type | Required | Format / example / constraints | Purpose |
|---|---|---|---|---|---|
| `name` | `body` | `string` | yes | format: ^[A-Za-z0-9][A-Za-z0-9_.-]*$; example: "alice-notebook" | New stable client name. Uses the same format as client creation. |

#### Request example

```http
PATCH /api/v1/clients/c0d4f871-6ea6-42b3-9e7b-f00cc1ec354e HTTP/1.1
Authorization: Bearer ovpn_v1.<uuid>.<secret>
Content-Type: application/json

{
  "name": "alice-notebook"
}
```

#### Responses

##### `200 OK`

Client renamed and profile regenerated.

Content type: `application/json`

Response example:

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

Response fields:

| Field | Type | Required | Format / example / constraints | Purpose |
|---|---|---|---|---|
| `version` | `integer` | yes | constant: 1 | Response contract schema version. |
| `operation_id` | `string` | yes | format: uuid; example: "47260b5b-47dd-4f9b-814f-4803668c5934" | UUID of the committed journal operation. |
| `client` | `object` | yes | - | Authoritative client identity and lifecycle state. |
| `client.id` | `string` | yes | format: uuid; example: "c0d4f871-6ea6-42b3-9e7b-f00cc1ec354e" | Stable identifier of this object. |
| `client.name` | `string` | yes | format: ^[A-Za-z0-9][A-Za-z0-9_.-]*$; example: "alice-notebook" | Stable human-readable name. |
| `client.status` | `"active" \| "revoked" \| "deleted"` | yes | values: "active" \| "revoked" \| "deleted" | Client credential lifecycle status. |
| `client.ipv4` | `object` | yes | - | Current client IPv4 assignment view. |
| `client.ipv4.mode` | `"none" \| "static" \| "dynamic"` | yes | values: "none" \| "static" \| "dynamic" | IPv4 allocation mode. |
| `client.ipv4.address` | `string \| null` | yes | format: ipv4; example: "10.42.0.30" | IPv4 address, or null when no address is assigned. |
| `client.ipv4.state` | `"configured" \| "retained" \| "unavailable"` | yes | values: "configured" \| "retained" \| "unavailable" | Availability state of the client address assignment. |
| `client.connection` | `string` | no | - | Current connection state when runtime data is available. |
| `kick_required` | `boolean` | yes | example: false | Whether the mutated client's active session must be disconnected. |
| `profile_redistribution_required` | `boolean` | yes | example: true | Whether the client's generated profile must be redistributed. |
| `runtime` | `object` | no | - | Per-client runtime convergence result after a mutation. |
| `runtime.client_id` | `string` | no | format: uuid | Immutable client UUID. |
| `runtime.status` | `"ok" \| "unavailable"` | no | values: "ok" \| "unavailable" | Whether the requested runtime action completed or was unavailable. |
| `runtime.result` | `object` | no | - | Result of requesting client session disconnection. |
| `runtime.result.version` | `integer` | no | constant: 1 | Response contract schema version. |
| `runtime.result.client_id` | `string` | no | format: uuid | Immutable client UUID. |
| `runtime.result.client_name` | `string` | no | - | Human-readable client name associated with the session. |
| `runtime.result.was_connected` | `boolean` | no | - | Whether a matching session existed before the request. |
| `runtime.result.disconnected` | `boolean` | no | - | Whether at least one matching session was disconnected. |
| `runtime.result.connections` | `integer` | no | minimum: 0 | Number of matching sessions processed by the disconnect request. |

##### `400 Bad Request`

Malformed path, query, header, media type, body, or JSON.

Content type: `application/json`

Response example:

```json
{
  "error": {
    "kind": "invalid_json",
    "message": "request body is invalid",
    "request_id": "33ba813e-fc63-4af8-b338-7f7486f82202"
  }
}
```

Response fields:

| Field | Type | Required | Format / example / constraints | Purpose |
|---|---|---|---|---|
| `error` | `object` | yes | - | Machine-readable API error details. |
| `error.kind` | `string` | yes | example: "invalid_json" | Stable machine-readable error kind. |
| `error.message` | `string` | yes | example: "request body is invalid" | Safe human-readable error message. |
| `error.request_id` | `string` | yes | format: uuid; example: "33ba813e-fc63-4af8-b338-7f7486f82202" | UUID used to correlate the request with server logs. |

##### `401 Unauthorized`

Bearer API key is missing, malformed, unknown, or deleted.

Content type: `application/json`

Response example:

```json
{
  "error": {
    "kind": "unauthenticated",
    "message": "API key is missing or invalid",
    "request_id": "33ba813e-fc63-4af8-b338-7f7486f82202"
  }
}
```

Response fields:

| Field | Type | Required | Format / example / constraints | Purpose |
|---|---|---|---|---|
| `error` | `object` | yes | - | Machine-readable API error details. |
| `error.kind` | `string` | yes | example: "unauthenticated" | Stable machine-readable error kind. |
| `error.message` | `string` | yes | example: "API key is missing or invalid" | Safe human-readable error message. |
| `error.request_id` | `string` | yes | format: uuid; example: "33ba813e-fc63-4af8-b338-7f7486f82202" | UUID used to correlate the request with server logs. |

##### `404 Not Found`

Resource or client does not exist.

Content type: `application/json`

Response example:

```json
{
  "error": {
    "kind": "client_not_found",
    "message": "client was not found",
    "request_id": "33ba813e-fc63-4af8-b338-7f7486f82202"
  }
}
```

Response fields:

| Field | Type | Required | Format / example / constraints | Purpose |
|---|---|---|---|---|
| `error` | `object` | yes | - | Machine-readable API error details. |
| `error.kind` | `string` | yes | example: "client_not_found" | Stable machine-readable error kind. |
| `error.message` | `string` | yes | example: "client was not found" | Safe human-readable error message. |
| `error.request_id` | `string` | yes | format: uuid; example: "33ba813e-fc63-4af8-b338-7f7486f82202" | UUID used to correlate the request with server logs. |

##### `405 Method Not Allowed`

HTTP method is not accepted; inspect the Allow header.

Content type: `application/json`

Response example:

```json
{
  "error": {
    "kind": "method_not_allowed",
    "message": "method is not allowed",
    "request_id": "33ba813e-fc63-4af8-b338-7f7486f82202"
  }
}
```

Response fields:

| Field | Type | Required | Format / example / constraints | Purpose |
|---|---|---|---|---|
| `error` | `object` | yes | - | Machine-readable API error details. |
| `error.kind` | `string` | yes | example: "method_not_allowed" | Stable machine-readable error kind. |
| `error.message` | `string` | yes | example: "method is not allowed" | Safe human-readable error message. |
| `error.request_id` | `string` | yes | format: uuid; example: "33ba813e-fc63-4af8-b338-7f7486f82202" | UUID used to correlate the request with server logs. |

##### `409 Conflict`

Digest, revision, state, uniqueness, lock, or recovery conflict.

Content type: `application/json`

Response example:

```json
{
  "error": {
    "kind": "configuration_conflict",
    "message": "configuration state changed or is busy",
    "request_id": "33ba813e-fc63-4af8-b338-7f7486f82202"
  }
}
```

Response fields:

| Field | Type | Required | Format / example / constraints | Purpose |
|---|---|---|---|---|
| `error` | `object` | yes | - | Machine-readable API error details. |
| `error.kind` | `string` | yes | example: "configuration_conflict" | Stable machine-readable error kind. |
| `error.message` | `string` | yes | example: "configuration state changed or is busy" | Safe human-readable error message. |
| `error.request_id` | `string` | yes | format: uuid; example: "33ba813e-fc63-4af8-b338-7f7486f82202" | UUID used to correlate the request with server logs. |

##### `422 Unprocessable Entity`

JSON is valid but violates client, IPv4, or configuration semantics.

Content type: `application/json`

Response example:

```json
{
  "error": {
    "kind": "invalid_client",
    "message": "client request is not valid for the current state",
    "request_id": "33ba813e-fc63-4af8-b338-7f7486f82202"
  }
}
```

Response fields:

| Field | Type | Required | Format / example / constraints | Purpose |
|---|---|---|---|---|
| `error` | `object` | yes | - | Machine-readable API error details. |
| `error.kind` | `string` | yes | example: "invalid_client" | Stable machine-readable error kind. |
| `error.message` | `string` | yes | example: "client request is not valid for the current state" | Safe human-readable error message. |
| `error.request_id` | `string` | yes | format: uuid; example: "33ba813e-fc63-4af8-b338-7f7486f82202" | UUID used to correlate the request with server logs. |

##### `500 Internal Server Error`

Unclassified internal failure with no implementation details exposed.

Content type: `application/json`

Response example:

```json
{
  "error": {
    "kind": "internal_error",
    "message": "request could not be completed",
    "request_id": "33ba813e-fc63-4af8-b338-7f7486f82202"
  }
}
```

Response fields:

| Field | Type | Required | Format / example / constraints | Purpose |
|---|---|---|---|---|
| `error` | `object` | yes | - | Machine-readable API error details. |
| `error.kind` | `string` | yes | example: "internal_error" | Stable machine-readable error kind. |
| `error.message` | `string` | yes | example: "request could not be completed" | Safe human-readable error message. |
| `error.request_id` | `string` | yes | format: uuid; example: "33ba813e-fc63-4af8-b338-7f7486f82202" | UUID used to correlate the request with server logs. |

##### `503 Service Unavailable`

Authentication storage, PKI, OpenVPN, broker, supervisor, or another required dependency is unavailable.

Content type: `application/json`

Response example:

```json
{
  "error": {
    "kind": "runtime_unavailable",
    "message": "OpenVPN runtime is unavailable",
    "request_id": "33ba813e-fc63-4af8-b338-7f7486f82202"
  }
}
```

Response fields:

| Field | Type | Required | Format / example / constraints | Purpose |
|---|---|---|---|---|
| `error` | `object` | yes | - | Machine-readable API error details. |
| `error.kind` | `string` | yes | example: "runtime_unavailable" | Stable machine-readable error kind. |
| `error.message` | `string` | yes | example: "OpenVPN runtime is unavailable" | Safe human-readable error message. |
| `error.request_id` | `string` | yes | format: uuid; example: "33ba813e-fc63-4af8-b338-7f7486f82202" | UUID used to correlate the request with server logs. |

### 9. `DELETE /api/v1/clients/{client_id}`

Delete local client credentials. Requires an empty body. The immutable client UUID tombstone remains in authoritative state.

#### Request parameters

##### Header parameters

| Field | Location | Type | Required | Format / example / constraints | Purpose |
|---|---|---|---|---|---|
| `Authorization` | `header` | `string` | yes | Bearer ovpn_v1.<uuid>.<secret> | API authentication credential. Put the service-generated API key after Bearer. |

##### Path parameters

| Field | Location | Type | Required | Format / example / constraints | Purpose |
|---|---|---|---|---|---|
| `client_id` | `path` | `string` | yes | format: uuid; example: "c0d4f871-6ea6-42b3-9e7b-f00cc1ec354e" | Complete canonical immutable client UUID; names and UUID prefixes are rejected. |

#### Request example

```http
DELETE /api/v1/clients/c0d4f871-6ea6-42b3-9e7b-f00cc1ec354e HTTP/1.1
Authorization: Bearer ovpn_v1.<uuid>.<secret>
```

#### Responses

##### `200 OK`

Client credentials deleted; the UUID tombstone remains.

Content type: `application/json`

Response example:

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

Response fields:

| Field | Type | Required | Format / example / constraints | Purpose |
|---|---|---|---|---|
| `version` | `integer` | yes | constant: 1 | Response contract schema version. |
| `operation_id` | `string` | yes | format: uuid; example: "47260b5b-47dd-4f9b-814f-4803668c5934" | UUID of the committed journal operation. |
| `client` | `object` | yes | - | Authoritative client identity and lifecycle state. |
| `client.id` | `string` | yes | format: uuid; example: "c0d4f871-6ea6-42b3-9e7b-f00cc1ec354e" | Stable identifier of this object. |
| `client.name` | `string` | yes | format: ^[A-Za-z0-9][A-Za-z0-9_.-]*$; example: "alice-notebook" | Stable human-readable name. |
| `client.status` | `"active" \| "revoked" \| "deleted"` | yes | values: "active" \| "revoked" \| "deleted" | Client credential lifecycle status. |
| `client.ipv4` | `object` | yes | - | Current client IPv4 assignment view. |
| `client.ipv4.mode` | `"none" \| "static" \| "dynamic"` | yes | values: "none" \| "static" \| "dynamic" | IPv4 allocation mode. |
| `client.ipv4.address` | `string \| null` | yes | format: ipv4 | IPv4 address, or null when no address is assigned. |
| `client.ipv4.state` | `"configured" \| "retained" \| "unavailable"` | yes | values: "configured" \| "retained" \| "unavailable" | Availability state of the client address assignment. |
| `client.connection` | `string` | no | - | Current connection state when runtime data is available. |
| `kick_required` | `boolean` | yes | example: false | Whether the mutated client's active session must be disconnected. |
| `profile_redistribution_required` | `boolean` | yes | example: false | Whether the client's generated profile must be redistributed. |
| `runtime` | `object` | no | - | Per-client runtime convergence result after a mutation. |
| `runtime.client_id` | `string` | no | format: uuid | Immutable client UUID. |
| `runtime.status` | `"ok" \| "unavailable"` | no | values: "ok" \| "unavailable" | Whether the requested runtime action completed or was unavailable. |
| `runtime.result` | `object` | no | - | Result of requesting client session disconnection. |
| `runtime.result.version` | `integer` | no | constant: 1 | Response contract schema version. |
| `runtime.result.client_id` | `string` | no | format: uuid | Immutable client UUID. |
| `runtime.result.client_name` | `string` | no | - | Human-readable client name associated with the session. |
| `runtime.result.was_connected` | `boolean` | no | - | Whether a matching session existed before the request. |
| `runtime.result.disconnected` | `boolean` | no | - | Whether at least one matching session was disconnected. |
| `runtime.result.connections` | `integer` | no | minimum: 0 | Number of matching sessions processed by the disconnect request. |

##### `400 Bad Request`

Malformed path, query, header, media type, body, or JSON.

Content type: `application/json`

Response example:

```json
{
  "error": {
    "kind": "invalid_json",
    "message": "request body is invalid",
    "request_id": "33ba813e-fc63-4af8-b338-7f7486f82202"
  }
}
```

Response fields:

| Field | Type | Required | Format / example / constraints | Purpose |
|---|---|---|---|---|
| `error` | `object` | yes | - | Machine-readable API error details. |
| `error.kind` | `string` | yes | example: "invalid_json" | Stable machine-readable error kind. |
| `error.message` | `string` | yes | example: "request body is invalid" | Safe human-readable error message. |
| `error.request_id` | `string` | yes | format: uuid; example: "33ba813e-fc63-4af8-b338-7f7486f82202" | UUID used to correlate the request with server logs. |

##### `401 Unauthorized`

Bearer API key is missing, malformed, unknown, or deleted.

Content type: `application/json`

Response example:

```json
{
  "error": {
    "kind": "unauthenticated",
    "message": "API key is missing or invalid",
    "request_id": "33ba813e-fc63-4af8-b338-7f7486f82202"
  }
}
```

Response fields:

| Field | Type | Required | Format / example / constraints | Purpose |
|---|---|---|---|---|
| `error` | `object` | yes | - | Machine-readable API error details. |
| `error.kind` | `string` | yes | example: "unauthenticated" | Stable machine-readable error kind. |
| `error.message` | `string` | yes | example: "API key is missing or invalid" | Safe human-readable error message. |
| `error.request_id` | `string` | yes | format: uuid; example: "33ba813e-fc63-4af8-b338-7f7486f82202" | UUID used to correlate the request with server logs. |

##### `404 Not Found`

Resource or client does not exist.

Content type: `application/json`

Response example:

```json
{
  "error": {
    "kind": "client_not_found",
    "message": "client was not found",
    "request_id": "33ba813e-fc63-4af8-b338-7f7486f82202"
  }
}
```

Response fields:

| Field | Type | Required | Format / example / constraints | Purpose |
|---|---|---|---|---|
| `error` | `object` | yes | - | Machine-readable API error details. |
| `error.kind` | `string` | yes | example: "client_not_found" | Stable machine-readable error kind. |
| `error.message` | `string` | yes | example: "client was not found" | Safe human-readable error message. |
| `error.request_id` | `string` | yes | format: uuid; example: "33ba813e-fc63-4af8-b338-7f7486f82202" | UUID used to correlate the request with server logs. |

##### `405 Method Not Allowed`

HTTP method is not accepted; inspect the Allow header.

Content type: `application/json`

Response example:

```json
{
  "error": {
    "kind": "method_not_allowed",
    "message": "method is not allowed",
    "request_id": "33ba813e-fc63-4af8-b338-7f7486f82202"
  }
}
```

Response fields:

| Field | Type | Required | Format / example / constraints | Purpose |
|---|---|---|---|---|
| `error` | `object` | yes | - | Machine-readable API error details. |
| `error.kind` | `string` | yes | example: "method_not_allowed" | Stable machine-readable error kind. |
| `error.message` | `string` | yes | example: "method is not allowed" | Safe human-readable error message. |
| `error.request_id` | `string` | yes | format: uuid; example: "33ba813e-fc63-4af8-b338-7f7486f82202" | UUID used to correlate the request with server logs. |

##### `409 Conflict`

Digest, revision, state, uniqueness, lock, or recovery conflict.

Content type: `application/json`

Response example:

```json
{
  "error": {
    "kind": "configuration_conflict",
    "message": "configuration state changed or is busy",
    "request_id": "33ba813e-fc63-4af8-b338-7f7486f82202"
  }
}
```

Response fields:

| Field | Type | Required | Format / example / constraints | Purpose |
|---|---|---|---|---|
| `error` | `object` | yes | - | Machine-readable API error details. |
| `error.kind` | `string` | yes | example: "configuration_conflict" | Stable machine-readable error kind. |
| `error.message` | `string` | yes | example: "configuration state changed or is busy" | Safe human-readable error message. |
| `error.request_id` | `string` | yes | format: uuid; example: "33ba813e-fc63-4af8-b338-7f7486f82202" | UUID used to correlate the request with server logs. |

##### `422 Unprocessable Entity`

JSON is valid but violates client, IPv4, or configuration semantics.

Content type: `application/json`

Response example:

```json
{
  "error": {
    "kind": "invalid_client",
    "message": "client request is not valid for the current state",
    "request_id": "33ba813e-fc63-4af8-b338-7f7486f82202"
  }
}
```

Response fields:

| Field | Type | Required | Format / example / constraints | Purpose |
|---|---|---|---|---|
| `error` | `object` | yes | - | Machine-readable API error details. |
| `error.kind` | `string` | yes | example: "invalid_client" | Stable machine-readable error kind. |
| `error.message` | `string` | yes | example: "client request is not valid for the current state" | Safe human-readable error message. |
| `error.request_id` | `string` | yes | format: uuid; example: "33ba813e-fc63-4af8-b338-7f7486f82202" | UUID used to correlate the request with server logs. |

##### `500 Internal Server Error`

Unclassified internal failure with no implementation details exposed.

Content type: `application/json`

Response example:

```json
{
  "error": {
    "kind": "internal_error",
    "message": "request could not be completed",
    "request_id": "33ba813e-fc63-4af8-b338-7f7486f82202"
  }
}
```

Response fields:

| Field | Type | Required | Format / example / constraints | Purpose |
|---|---|---|---|---|
| `error` | `object` | yes | - | Machine-readable API error details. |
| `error.kind` | `string` | yes | example: "internal_error" | Stable machine-readable error kind. |
| `error.message` | `string` | yes | example: "request could not be completed" | Safe human-readable error message. |
| `error.request_id` | `string` | yes | format: uuid; example: "33ba813e-fc63-4af8-b338-7f7486f82202" | UUID used to correlate the request with server logs. |

##### `503 Service Unavailable`

Authentication storage, PKI, OpenVPN, broker, supervisor, or another required dependency is unavailable.

Content type: `application/json`

Response example:

```json
{
  "error": {
    "kind": "runtime_unavailable",
    "message": "OpenVPN runtime is unavailable",
    "request_id": "33ba813e-fc63-4af8-b338-7f7486f82202"
  }
}
```

Response fields:

| Field | Type | Required | Format / example / constraints | Purpose |
|---|---|---|---|---|
| `error` | `object` | yes | - | Machine-readable API error details. |
| `error.kind` | `string` | yes | example: "runtime_unavailable" | Stable machine-readable error kind. |
| `error.message` | `string` | yes | example: "OpenVPN runtime is unavailable" | Safe human-readable error message. |
| `error.request_id` | `string` | yes | format: uuid; example: "33ba813e-fc63-4af8-b338-7f7486f82202" | UUID used to correlate the request with server logs. |

### 10. `GET /api/v1/clients/{client_id}/profile`

Download an OpenVPN client profile. The response contains a private key and must be handled as a credential.

#### Request parameters

##### Header parameters

| Field | Location | Type | Required | Format / example / constraints | Purpose |
|---|---|---|---|---|---|
| `Authorization` | `header` | `string` | yes | Bearer ovpn_v1.<uuid>.<secret> | API authentication credential. Put the service-generated API key after Bearer. |

##### Path parameters

| Field | Location | Type | Required | Format / example / constraints | Purpose |
|---|---|---|---|---|---|
| `client_id` | `path` | `string` | yes | format: uuid; example: "c0d4f871-6ea6-42b3-9e7b-f00cc1ec354e" | Complete canonical immutable client UUID; names and UUID prefixes are rejected. |

#### Request example

```http
GET /api/v1/clients/c0d4f871-6ea6-42b3-9e7b-f00cc1ec354e/profile HTTP/1.1
Authorization: Bearer ovpn_v1.<uuid>.<secret>
```

#### Responses

##### `200 OK`

OpenVPN profile containing private credentials.

Content type: `application/x-openvpn-profile`

Response example:

```text
string
```

Response fields:

| Field | Type | Required | Format / example / constraints | Purpose |
|---|---|---|---|---|
| `response` | `string` | yes | format: binary; example: "string" | OpenVPN profile containing private credentials. |

##### `400 Bad Request`

Malformed path, query, header, media type, body, or JSON.

Content type: `application/json`

Response example:

```json
{
  "error": {
    "kind": "invalid_json",
    "message": "request body is invalid",
    "request_id": "33ba813e-fc63-4af8-b338-7f7486f82202"
  }
}
```

Response fields:

| Field | Type | Required | Format / example / constraints | Purpose |
|---|---|---|---|---|
| `error` | `object` | yes | - | Machine-readable API error details. |
| `error.kind` | `string` | yes | example: "invalid_json" | Stable machine-readable error kind. |
| `error.message` | `string` | yes | example: "request body is invalid" | Safe human-readable error message. |
| `error.request_id` | `string` | yes | format: uuid; example: "33ba813e-fc63-4af8-b338-7f7486f82202" | UUID used to correlate the request with server logs. |

##### `401 Unauthorized`

Bearer API key is missing, malformed, unknown, or deleted.

Content type: `application/json`

Response example:

```json
{
  "error": {
    "kind": "unauthenticated",
    "message": "API key is missing or invalid",
    "request_id": "33ba813e-fc63-4af8-b338-7f7486f82202"
  }
}
```

Response fields:

| Field | Type | Required | Format / example / constraints | Purpose |
|---|---|---|---|---|
| `error` | `object` | yes | - | Machine-readable API error details. |
| `error.kind` | `string` | yes | example: "unauthenticated" | Stable machine-readable error kind. |
| `error.message` | `string` | yes | example: "API key is missing or invalid" | Safe human-readable error message. |
| `error.request_id` | `string` | yes | format: uuid; example: "33ba813e-fc63-4af8-b338-7f7486f82202" | UUID used to correlate the request with server logs. |

##### `404 Not Found`

Resource or client does not exist.

Content type: `application/json`

Response example:

```json
{
  "error": {
    "kind": "client_not_found",
    "message": "client was not found",
    "request_id": "33ba813e-fc63-4af8-b338-7f7486f82202"
  }
}
```

Response fields:

| Field | Type | Required | Format / example / constraints | Purpose |
|---|---|---|---|---|
| `error` | `object` | yes | - | Machine-readable API error details. |
| `error.kind` | `string` | yes | example: "client_not_found" | Stable machine-readable error kind. |
| `error.message` | `string` | yes | example: "client was not found" | Safe human-readable error message. |
| `error.request_id` | `string` | yes | format: uuid; example: "33ba813e-fc63-4af8-b338-7f7486f82202" | UUID used to correlate the request with server logs. |

##### `405 Method Not Allowed`

HTTP method is not accepted; inspect the Allow header.

Content type: `application/json`

Response example:

```json
{
  "error": {
    "kind": "method_not_allowed",
    "message": "method is not allowed",
    "request_id": "33ba813e-fc63-4af8-b338-7f7486f82202"
  }
}
```

Response fields:

| Field | Type | Required | Format / example / constraints | Purpose |
|---|---|---|---|---|
| `error` | `object` | yes | - | Machine-readable API error details. |
| `error.kind` | `string` | yes | example: "method_not_allowed" | Stable machine-readable error kind. |
| `error.message` | `string` | yes | example: "method is not allowed" | Safe human-readable error message. |
| `error.request_id` | `string` | yes | format: uuid; example: "33ba813e-fc63-4af8-b338-7f7486f82202" | UUID used to correlate the request with server logs. |

##### `409 Conflict`

Digest, revision, state, uniqueness, lock, or recovery conflict.

Content type: `application/json`

Response example:

```json
{
  "error": {
    "kind": "configuration_conflict",
    "message": "configuration state changed or is busy",
    "request_id": "33ba813e-fc63-4af8-b338-7f7486f82202"
  }
}
```

Response fields:

| Field | Type | Required | Format / example / constraints | Purpose |
|---|---|---|---|---|
| `error` | `object` | yes | - | Machine-readable API error details. |
| `error.kind` | `string` | yes | example: "configuration_conflict" | Stable machine-readable error kind. |
| `error.message` | `string` | yes | example: "configuration state changed or is busy" | Safe human-readable error message. |
| `error.request_id` | `string` | yes | format: uuid; example: "33ba813e-fc63-4af8-b338-7f7486f82202" | UUID used to correlate the request with server logs. |

##### `422 Unprocessable Entity`

JSON is valid but violates client, IPv4, or configuration semantics.

Content type: `application/json`

Response example:

```json
{
  "error": {
    "kind": "invalid_client",
    "message": "client request is not valid for the current state",
    "request_id": "33ba813e-fc63-4af8-b338-7f7486f82202"
  }
}
```

Response fields:

| Field | Type | Required | Format / example / constraints | Purpose |
|---|---|---|---|---|
| `error` | `object` | yes | - | Machine-readable API error details. |
| `error.kind` | `string` | yes | example: "invalid_client" | Stable machine-readable error kind. |
| `error.message` | `string` | yes | example: "client request is not valid for the current state" | Safe human-readable error message. |
| `error.request_id` | `string` | yes | format: uuid; example: "33ba813e-fc63-4af8-b338-7f7486f82202" | UUID used to correlate the request with server logs. |

##### `500 Internal Server Error`

Unclassified internal failure with no implementation details exposed.

Content type: `application/json`

Response example:

```json
{
  "error": {
    "kind": "internal_error",
    "message": "request could not be completed",
    "request_id": "33ba813e-fc63-4af8-b338-7f7486f82202"
  }
}
```

Response fields:

| Field | Type | Required | Format / example / constraints | Purpose |
|---|---|---|---|---|
| `error` | `object` | yes | - | Machine-readable API error details. |
| `error.kind` | `string` | yes | example: "internal_error" | Stable machine-readable error kind. |
| `error.message` | `string` | yes | example: "request could not be completed" | Safe human-readable error message. |
| `error.request_id` | `string` | yes | format: uuid; example: "33ba813e-fc63-4af8-b338-7f7486f82202" | UUID used to correlate the request with server logs. |

##### `503 Service Unavailable`

Authentication storage, PKI, OpenVPN, broker, supervisor, or another required dependency is unavailable.

Content type: `application/json`

Response example:

```json
{
  "error": {
    "kind": "runtime_unavailable",
    "message": "OpenVPN runtime is unavailable",
    "request_id": "33ba813e-fc63-4af8-b338-7f7486f82202"
  }
}
```

Response fields:

| Field | Type | Required | Format / example / constraints | Purpose |
|---|---|---|---|---|
| `error` | `object` | yes | - | Machine-readable API error details. |
| `error.kind` | `string` | yes | example: "runtime_unavailable" | Stable machine-readable error kind. |
| `error.message` | `string` | yes | example: "OpenVPN runtime is unavailable" | Safe human-readable error message. |
| `error.request_id` | `string` | yes | format: uuid; example: "33ba813e-fc63-4af8-b338-7f7486f82202" | UUID used to correlate the request with server logs. |

### 11. `POST /api/v1/clients/{client_id}/revoke`

Revoke client credentials

#### Request parameters

##### Header parameters

| Field | Location | Type | Required | Format / example / constraints | Purpose |
|---|---|---|---|---|---|
| `Authorization` | `header` | `string` | yes | Bearer ovpn_v1.<uuid>.<secret> | API authentication credential. Put the service-generated API key after Bearer. |
| `Content-Type` | `header` | `string` | yes | application/json | Declares that the request body uses JSON. |

##### Path parameters

| Field | Location | Type | Required | Format / example / constraints | Purpose |
|---|---|---|---|---|---|
| `client_id` | `path` | `string` | yes | format: uuid; example: "c0d4f871-6ea6-42b3-9e7b-f00cc1ec354e" | Complete canonical immutable client UUID; names and UUID prefixes are rejected. |

##### JSON body fields

| Field | Location | Type | Required | Format / example / constraints | Purpose |
|---|---|---|---|---|---|
| `release_ipv4` | `body` | `boolean` | yes | example: false | true releases the assignment; false retains it for later release or reissue. |

#### Request example

```http
POST /api/v1/clients/c0d4f871-6ea6-42b3-9e7b-f00cc1ec354e/revoke HTTP/1.1
Authorization: Bearer ovpn_v1.<uuid>.<secret>
Content-Type: application/json

{
  "release_ipv4": false
}
```

#### Responses

##### `200 OK`

Committed client mutation. Runtime convergence is reported separately when required.

Content type: `application/json`

Response example:

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

Response fields:

| Field | Type | Required | Format / example / constraints | Purpose |
|---|---|---|---|---|
| `version` | `integer` | yes | constant: 1 | Response contract schema version. |
| `operation_id` | `string` | yes | format: uuid; example: "47260b5b-47dd-4f9b-814f-4803668c5934" | UUID of the committed journal operation. |
| `client` | `object` | yes | - | Authoritative client identity and lifecycle state. |
| `client.id` | `string` | yes | format: uuid; example: "c0d4f871-6ea6-42b3-9e7b-f00cc1ec354e" | Stable identifier of this object. |
| `client.name` | `string` | yes | format: ^[A-Za-z0-9][A-Za-z0-9_.-]*$; example: "alice-laptop" | Stable human-readable name. |
| `client.status` | `"active" \| "revoked" \| "deleted"` | yes | values: "active" \| "revoked" \| "deleted" | Client credential lifecycle status. |
| `client.ipv4` | `object` | yes | - | Current client IPv4 assignment view. |
| `client.ipv4.mode` | `"none" \| "static" \| "dynamic"` | yes | values: "none" \| "static" \| "dynamic" | IPv4 allocation mode. |
| `client.ipv4.address` | `string \| null` | yes | format: ipv4; example: "10.42.0.30" | IPv4 address, or null when no address is assigned. |
| `client.ipv4.state` | `"configured" \| "retained" \| "unavailable"` | yes | values: "configured" \| "retained" \| "unavailable" | Availability state of the client address assignment. |
| `client.connection` | `string` | no | - | Current connection state when runtime data is available. |
| `kick_required` | `boolean` | yes | example: true | Whether the mutated client's active session must be disconnected. |
| `profile_redistribution_required` | `boolean` | yes | example: false | Whether the client's generated profile must be redistributed. |
| `runtime` | `object` | no | - | Per-client runtime convergence result after a mutation. |
| `runtime.client_id` | `string` | no | format: uuid | Immutable client UUID. |
| `runtime.status` | `"ok" \| "unavailable"` | no | values: "ok" \| "unavailable" | Whether the requested runtime action completed or was unavailable. |
| `runtime.result` | `object` | no | - | Result of requesting client session disconnection. |
| `runtime.result.version` | `integer` | no | constant: 1 | Response contract schema version. |
| `runtime.result.client_id` | `string` | no | format: uuid; example: "c0d4f871-6ea6-42b3-9e7b-f00cc1ec354e" | Immutable client UUID. |
| `runtime.result.client_name` | `string` | no | example: "alice-laptop" | Human-readable client name associated with the session. |
| `runtime.result.was_connected` | `boolean` | no | example: true | Whether a matching session existed before the request. |
| `runtime.result.disconnected` | `boolean` | no | example: true | Whether at least one matching session was disconnected. |
| `runtime.result.connections` | `integer` | no | minimum: 0; example: 1 | Number of matching sessions processed by the disconnect request. |

##### `400 Bad Request`

Malformed path, query, header, media type, body, or JSON.

Content type: `application/json`

Response example:

```json
{
  "error": {
    "kind": "invalid_json",
    "message": "request body is invalid",
    "request_id": "33ba813e-fc63-4af8-b338-7f7486f82202"
  }
}
```

Response fields:

| Field | Type | Required | Format / example / constraints | Purpose |
|---|---|---|---|---|
| `error` | `object` | yes | - | Machine-readable API error details. |
| `error.kind` | `string` | yes | example: "invalid_json" | Stable machine-readable error kind. |
| `error.message` | `string` | yes | example: "request body is invalid" | Safe human-readable error message. |
| `error.request_id` | `string` | yes | format: uuid; example: "33ba813e-fc63-4af8-b338-7f7486f82202" | UUID used to correlate the request with server logs. |

##### `401 Unauthorized`

Bearer API key is missing, malformed, unknown, or deleted.

Content type: `application/json`

Response example:

```json
{
  "error": {
    "kind": "unauthenticated",
    "message": "API key is missing or invalid",
    "request_id": "33ba813e-fc63-4af8-b338-7f7486f82202"
  }
}
```

Response fields:

| Field | Type | Required | Format / example / constraints | Purpose |
|---|---|---|---|---|
| `error` | `object` | yes | - | Machine-readable API error details. |
| `error.kind` | `string` | yes | example: "unauthenticated" | Stable machine-readable error kind. |
| `error.message` | `string` | yes | example: "API key is missing or invalid" | Safe human-readable error message. |
| `error.request_id` | `string` | yes | format: uuid; example: "33ba813e-fc63-4af8-b338-7f7486f82202" | UUID used to correlate the request with server logs. |

##### `404 Not Found`

Resource or client does not exist.

Content type: `application/json`

Response example:

```json
{
  "error": {
    "kind": "client_not_found",
    "message": "client was not found",
    "request_id": "33ba813e-fc63-4af8-b338-7f7486f82202"
  }
}
```

Response fields:

| Field | Type | Required | Format / example / constraints | Purpose |
|---|---|---|---|---|
| `error` | `object` | yes | - | Machine-readable API error details. |
| `error.kind` | `string` | yes | example: "client_not_found" | Stable machine-readable error kind. |
| `error.message` | `string` | yes | example: "client was not found" | Safe human-readable error message. |
| `error.request_id` | `string` | yes | format: uuid; example: "33ba813e-fc63-4af8-b338-7f7486f82202" | UUID used to correlate the request with server logs. |

##### `405 Method Not Allowed`

HTTP method is not accepted; inspect the Allow header.

Content type: `application/json`

Response example:

```json
{
  "error": {
    "kind": "method_not_allowed",
    "message": "method is not allowed",
    "request_id": "33ba813e-fc63-4af8-b338-7f7486f82202"
  }
}
```

Response fields:

| Field | Type | Required | Format / example / constraints | Purpose |
|---|---|---|---|---|
| `error` | `object` | yes | - | Machine-readable API error details. |
| `error.kind` | `string` | yes | example: "method_not_allowed" | Stable machine-readable error kind. |
| `error.message` | `string` | yes | example: "method is not allowed" | Safe human-readable error message. |
| `error.request_id` | `string` | yes | format: uuid; example: "33ba813e-fc63-4af8-b338-7f7486f82202" | UUID used to correlate the request with server logs. |

##### `409 Conflict`

Digest, revision, state, uniqueness, lock, or recovery conflict.

Content type: `application/json`

Response example:

```json
{
  "error": {
    "kind": "configuration_conflict",
    "message": "configuration state changed or is busy",
    "request_id": "33ba813e-fc63-4af8-b338-7f7486f82202"
  }
}
```

Response fields:

| Field | Type | Required | Format / example / constraints | Purpose |
|---|---|---|---|---|
| `error` | `object` | yes | - | Machine-readable API error details. |
| `error.kind` | `string` | yes | example: "configuration_conflict" | Stable machine-readable error kind. |
| `error.message` | `string` | yes | example: "configuration state changed or is busy" | Safe human-readable error message. |
| `error.request_id` | `string` | yes | format: uuid; example: "33ba813e-fc63-4af8-b338-7f7486f82202" | UUID used to correlate the request with server logs. |

##### `422 Unprocessable Entity`

JSON is valid but violates client, IPv4, or configuration semantics.

Content type: `application/json`

Response example:

```json
{
  "error": {
    "kind": "invalid_client",
    "message": "client request is not valid for the current state",
    "request_id": "33ba813e-fc63-4af8-b338-7f7486f82202"
  }
}
```

Response fields:

| Field | Type | Required | Format / example / constraints | Purpose |
|---|---|---|---|---|
| `error` | `object` | yes | - | Machine-readable API error details. |
| `error.kind` | `string` | yes | example: "invalid_client" | Stable machine-readable error kind. |
| `error.message` | `string` | yes | example: "client request is not valid for the current state" | Safe human-readable error message. |
| `error.request_id` | `string` | yes | format: uuid; example: "33ba813e-fc63-4af8-b338-7f7486f82202" | UUID used to correlate the request with server logs. |

##### `500 Internal Server Error`

Unclassified internal failure with no implementation details exposed.

Content type: `application/json`

Response example:

```json
{
  "error": {
    "kind": "internal_error",
    "message": "request could not be completed",
    "request_id": "33ba813e-fc63-4af8-b338-7f7486f82202"
  }
}
```

Response fields:

| Field | Type | Required | Format / example / constraints | Purpose |
|---|---|---|---|---|
| `error` | `object` | yes | - | Machine-readable API error details. |
| `error.kind` | `string` | yes | example: "internal_error" | Stable machine-readable error kind. |
| `error.message` | `string` | yes | example: "request could not be completed" | Safe human-readable error message. |
| `error.request_id` | `string` | yes | format: uuid; example: "33ba813e-fc63-4af8-b338-7f7486f82202" | UUID used to correlate the request with server logs. |

##### `503 Service Unavailable`

Authentication storage, PKI, OpenVPN, broker, supervisor, or another required dependency is unavailable.

Content type: `application/json`

Response example:

```json
{
  "error": {
    "kind": "runtime_unavailable",
    "message": "OpenVPN runtime is unavailable",
    "request_id": "33ba813e-fc63-4af8-b338-7f7486f82202"
  }
}
```

Response fields:

| Field | Type | Required | Format / example / constraints | Purpose |
|---|---|---|---|---|
| `error` | `object` | yes | - | Machine-readable API error details. |
| `error.kind` | `string` | yes | example: "runtime_unavailable" | Stable machine-readable error kind. |
| `error.message` | `string` | yes | example: "OpenVPN runtime is unavailable" | Safe human-readable error message. |
| `error.request_id` | `string` | yes | format: uuid; example: "33ba813e-fc63-4af8-b338-7f7486f82202" | UUID used to correlate the request with server logs. |

### 12. `POST /api/v1/clients/{client_id}/reissue`

Reissue client credentials and profile

#### Request parameters

##### Header parameters

| Field | Location | Type | Required | Format / example / constraints | Purpose |
|---|---|---|---|---|---|
| `Authorization` | `header` | `string` | yes | Bearer ovpn_v1.<uuid>.<secret> | API authentication credential. Put the service-generated API key after Bearer. |
| `Content-Type` | `header` | `string` | yes | application/json | Declares that the request body uses JSON. |

##### Path parameters

| Field | Location | Type | Required | Format / example / constraints | Purpose |
|---|---|---|---|---|---|
| `client_id` | `path` | `string` | yes | format: uuid; example: "c0d4f871-6ea6-42b3-9e7b-f00cc1ec354e" | Complete canonical immutable client UUID; names and UUID prefixes are rejected. |

##### JSON body fields

| Field | Location | Type | Required | Format / example / constraints | Purpose |
|---|---|---|---|---|---|
| `ipv4` | `body` | `string` | yes | example: "dynamic" | auto, dynamic, or a valid static IPv4 address. |

#### Request example

```http
POST /api/v1/clients/c0d4f871-6ea6-42b3-9e7b-f00cc1ec354e/reissue HTTP/1.1
Authorization: Bearer ovpn_v1.<uuid>.<secret>
Content-Type: application/json

{
  "ipv4": "dynamic"
}
```

#### Responses

##### `200 OK`

Client credentials and profile reissued.

Content type: `application/json`

Response example:

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

Response fields:

| Field | Type | Required | Format / example / constraints | Purpose |
|---|---|---|---|---|
| `version` | `integer` | yes | constant: 1 | Response contract schema version. |
| `operation_id` | `string` | yes | format: uuid; example: "47260b5b-47dd-4f9b-814f-4803668c5934" | UUID of the committed journal operation. |
| `client` | `object` | yes | - | Authoritative client identity and lifecycle state. |
| `client.id` | `string` | yes | format: uuid; example: "c0d4f871-6ea6-42b3-9e7b-f00cc1ec354e" | Stable identifier of this object. |
| `client.name` | `string` | yes | format: ^[A-Za-z0-9][A-Za-z0-9_.-]*$; example: "alice-notebook" | Stable human-readable name. |
| `client.status` | `"active" \| "revoked" \| "deleted"` | yes | values: "active" \| "revoked" \| "deleted" | Client credential lifecycle status. |
| `client.ipv4` | `object` | yes | - | Current client IPv4 assignment view. |
| `client.ipv4.mode` | `"none" \| "static" \| "dynamic"` | yes | values: "none" \| "static" \| "dynamic" | IPv4 allocation mode. |
| `client.ipv4.address` | `string \| null` | yes | format: ipv4 | IPv4 address, or null when no address is assigned. |
| `client.ipv4.state` | `"configured" \| "retained" \| "unavailable"` | yes | values: "configured" \| "retained" \| "unavailable" | Availability state of the client address assignment. |
| `client.connection` | `string` | no | - | Current connection state when runtime data is available. |
| `kick_required` | `boolean` | yes | example: true | Whether the mutated client's active session must be disconnected. |
| `profile_redistribution_required` | `boolean` | yes | example: true | Whether the client's generated profile must be redistributed. |
| `runtime` | `object` | no | - | Per-client runtime convergence result after a mutation. |
| `runtime.client_id` | `string` | no | format: uuid | Immutable client UUID. |
| `runtime.status` | `"ok" \| "unavailable"` | no | values: "ok" \| "unavailable" | Whether the requested runtime action completed or was unavailable. |
| `runtime.result` | `object` | no | - | Result of requesting client session disconnection. |
| `runtime.result.version` | `integer` | no | constant: 1 | Response contract schema version. |
| `runtime.result.client_id` | `string` | no | format: uuid | Immutable client UUID. |
| `runtime.result.client_name` | `string` | no | - | Human-readable client name associated with the session. |
| `runtime.result.was_connected` | `boolean` | no | - | Whether a matching session existed before the request. |
| `runtime.result.disconnected` | `boolean` | no | - | Whether at least one matching session was disconnected. |
| `runtime.result.connections` | `integer` | no | minimum: 0 | Number of matching sessions processed by the disconnect request. |

##### `400 Bad Request`

Malformed path, query, header, media type, body, or JSON.

Content type: `application/json`

Response example:

```json
{
  "error": {
    "kind": "invalid_json",
    "message": "request body is invalid",
    "request_id": "33ba813e-fc63-4af8-b338-7f7486f82202"
  }
}
```

Response fields:

| Field | Type | Required | Format / example / constraints | Purpose |
|---|---|---|---|---|
| `error` | `object` | yes | - | Machine-readable API error details. |
| `error.kind` | `string` | yes | example: "invalid_json" | Stable machine-readable error kind. |
| `error.message` | `string` | yes | example: "request body is invalid" | Safe human-readable error message. |
| `error.request_id` | `string` | yes | format: uuid; example: "33ba813e-fc63-4af8-b338-7f7486f82202" | UUID used to correlate the request with server logs. |

##### `401 Unauthorized`

Bearer API key is missing, malformed, unknown, or deleted.

Content type: `application/json`

Response example:

```json
{
  "error": {
    "kind": "unauthenticated",
    "message": "API key is missing or invalid",
    "request_id": "33ba813e-fc63-4af8-b338-7f7486f82202"
  }
}
```

Response fields:

| Field | Type | Required | Format / example / constraints | Purpose |
|---|---|---|---|---|
| `error` | `object` | yes | - | Machine-readable API error details. |
| `error.kind` | `string` | yes | example: "unauthenticated" | Stable machine-readable error kind. |
| `error.message` | `string` | yes | example: "API key is missing or invalid" | Safe human-readable error message. |
| `error.request_id` | `string` | yes | format: uuid; example: "33ba813e-fc63-4af8-b338-7f7486f82202" | UUID used to correlate the request with server logs. |

##### `404 Not Found`

Resource or client does not exist.

Content type: `application/json`

Response example:

```json
{
  "error": {
    "kind": "client_not_found",
    "message": "client was not found",
    "request_id": "33ba813e-fc63-4af8-b338-7f7486f82202"
  }
}
```

Response fields:

| Field | Type | Required | Format / example / constraints | Purpose |
|---|---|---|---|---|
| `error` | `object` | yes | - | Machine-readable API error details. |
| `error.kind` | `string` | yes | example: "client_not_found" | Stable machine-readable error kind. |
| `error.message` | `string` | yes | example: "client was not found" | Safe human-readable error message. |
| `error.request_id` | `string` | yes | format: uuid; example: "33ba813e-fc63-4af8-b338-7f7486f82202" | UUID used to correlate the request with server logs. |

##### `405 Method Not Allowed`

HTTP method is not accepted; inspect the Allow header.

Content type: `application/json`

Response example:

```json
{
  "error": {
    "kind": "method_not_allowed",
    "message": "method is not allowed",
    "request_id": "33ba813e-fc63-4af8-b338-7f7486f82202"
  }
}
```

Response fields:

| Field | Type | Required | Format / example / constraints | Purpose |
|---|---|---|---|---|
| `error` | `object` | yes | - | Machine-readable API error details. |
| `error.kind` | `string` | yes | example: "method_not_allowed" | Stable machine-readable error kind. |
| `error.message` | `string` | yes | example: "method is not allowed" | Safe human-readable error message. |
| `error.request_id` | `string` | yes | format: uuid; example: "33ba813e-fc63-4af8-b338-7f7486f82202" | UUID used to correlate the request with server logs. |

##### `409 Conflict`

Digest, revision, state, uniqueness, lock, or recovery conflict.

Content type: `application/json`

Response example:

```json
{
  "error": {
    "kind": "configuration_conflict",
    "message": "configuration state changed or is busy",
    "request_id": "33ba813e-fc63-4af8-b338-7f7486f82202"
  }
}
```

Response fields:

| Field | Type | Required | Format / example / constraints | Purpose |
|---|---|---|---|---|
| `error` | `object` | yes | - | Machine-readable API error details. |
| `error.kind` | `string` | yes | example: "configuration_conflict" | Stable machine-readable error kind. |
| `error.message` | `string` | yes | example: "configuration state changed or is busy" | Safe human-readable error message. |
| `error.request_id` | `string` | yes | format: uuid; example: "33ba813e-fc63-4af8-b338-7f7486f82202" | UUID used to correlate the request with server logs. |

##### `422 Unprocessable Entity`

JSON is valid but violates client, IPv4, or configuration semantics.

Content type: `application/json`

Response example:

```json
{
  "error": {
    "kind": "invalid_client",
    "message": "client request is not valid for the current state",
    "request_id": "33ba813e-fc63-4af8-b338-7f7486f82202"
  }
}
```

Response fields:

| Field | Type | Required | Format / example / constraints | Purpose |
|---|---|---|---|---|
| `error` | `object` | yes | - | Machine-readable API error details. |
| `error.kind` | `string` | yes | example: "invalid_client" | Stable machine-readable error kind. |
| `error.message` | `string` | yes | example: "client request is not valid for the current state" | Safe human-readable error message. |
| `error.request_id` | `string` | yes | format: uuid; example: "33ba813e-fc63-4af8-b338-7f7486f82202" | UUID used to correlate the request with server logs. |

##### `500 Internal Server Error`

Unclassified internal failure with no implementation details exposed.

Content type: `application/json`

Response example:

```json
{
  "error": {
    "kind": "internal_error",
    "message": "request could not be completed",
    "request_id": "33ba813e-fc63-4af8-b338-7f7486f82202"
  }
}
```

Response fields:

| Field | Type | Required | Format / example / constraints | Purpose |
|---|---|---|---|---|
| `error` | `object` | yes | - | Machine-readable API error details. |
| `error.kind` | `string` | yes | example: "internal_error" | Stable machine-readable error kind. |
| `error.message` | `string` | yes | example: "request could not be completed" | Safe human-readable error message. |
| `error.request_id` | `string` | yes | format: uuid; example: "33ba813e-fc63-4af8-b338-7f7486f82202" | UUID used to correlate the request with server logs. |

##### `503 Service Unavailable`

Authentication storage, PKI, OpenVPN, broker, supervisor, or another required dependency is unavailable.

Content type: `application/json`

Response example:

```json
{
  "error": {
    "kind": "runtime_unavailable",
    "message": "OpenVPN runtime is unavailable",
    "request_id": "33ba813e-fc63-4af8-b338-7f7486f82202"
  }
}
```

Response fields:

| Field | Type | Required | Format / example / constraints | Purpose |
|---|---|---|---|---|
| `error` | `object` | yes | - | Machine-readable API error details. |
| `error.kind` | `string` | yes | example: "runtime_unavailable" | Stable machine-readable error kind. |
| `error.message` | `string` | yes | example: "OpenVPN runtime is unavailable" | Safe human-readable error message. |
| `error.request_id` | `string` | yes | format: uuid; example: "33ba813e-fc63-4af8-b338-7f7486f82202" | UUID used to correlate the request with server logs. |

### 13. `PUT /api/v1/clients/{client_id}/ipv4`

Set client IPv4 intent. Use auto or dynamic without address. Use static with an address from the configured static region.

#### Request parameters

##### Header parameters

| Field | Location | Type | Required | Format / example / constraints | Purpose |
|---|---|---|---|---|---|
| `Authorization` | `header` | `string` | yes | Bearer ovpn_v1.<uuid>.<secret> | API authentication credential. Put the service-generated API key after Bearer. |
| `Content-Type` | `header` | `string` | yes | application/json | Declares that the request body uses JSON. |

##### Path parameters

| Field | Location | Type | Required | Format / example / constraints | Purpose |
|---|---|---|---|---|---|
| `client_id` | `path` | `string` | yes | format: uuid; example: "c0d4f871-6ea6-42b3-9e7b-f00cc1ec354e" | Complete canonical immutable client UUID; names and UUID prefixes are rejected. |

##### JSON body fields

| Field | Location | Type | Required | Format / example / constraints | Purpose |
|---|---|---|---|---|---|
| `mode` | `body` | `"auto" \| "dynamic" \| "static"` | yes | values: "auto" \| "dynamic" \| "static" | Address selection mode: automatic allocation, dynamic allocation, or a specific static address. |
| `address` | `body` | `string` | no | format: ipv4 | Static IPv4 address. Required only for static mode and forbidden for auto/dynamic. |

#### Request example

```http
PUT /api/v1/clients/c0d4f871-6ea6-42b3-9e7b-f00cc1ec354e/ipv4 HTTP/1.1
Authorization: Bearer ovpn_v1.<uuid>.<secret>
Content-Type: application/json

{
  "mode": "auto"
}
```

#### Responses

##### `200 OK`

Committed address mutation and per-client runtime outcomes.

Content type: `application/json`

Response example:

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

Response fields:

| Field | Type | Required | Format / example / constraints | Purpose |
|---|---|---|---|---|
| `version` | `integer` | yes | constant: 1 | Response contract schema version. |
| `operation_id` | `string` | yes | format: uuid; example: "d41dbfce-b40e-4672-8a62-cb401f8c099c" | UUID of the committed journal operation. |
| `clients` | `object[]` | yes | example: [{"id":"c0d4f871-6ea6-42b3-9e7b-f00cc1ec354e","name":"alice-laptop","status":"active","ipv4":{"mode":"static","address":"10.42.0.30","state":"configured"}}] | Clients included in this response. |
| `clients[].id` | `string` | yes | format: uuid; example: "c0d4f871-6ea6-42b3-9e7b-f00cc1ec354e" | Stable identifier of this object. |
| `clients[].name` | `string` | yes | format: ^[A-Za-z0-9][A-Za-z0-9_.-]*$; example: "alice-laptop" | Stable human-readable name. |
| `clients[].status` | `"active" \| "revoked" \| "deleted"` | yes | values: "active" \| "revoked" \| "deleted" | Client credential lifecycle status. |
| `clients[].ipv4` | `object` | yes | - | Current client IPv4 assignment view. |
| `clients[].ipv4.mode` | `"none" \| "static" \| "dynamic"` | yes | values: "none" \| "static" \| "dynamic" | IPv4 allocation mode. |
| `clients[].ipv4.address` | `string \| null` | yes | format: ipv4; example: "10.42.0.30" | IPv4 address, or null when no address is assigned. |
| `clients[].ipv4.state` | `"configured" \| "retained" \| "unavailable"` | yes | values: "configured" \| "retained" \| "unavailable" | Availability state of the client address assignment. |
| `clients[].connection` | `string` | no | - | Current connection state when runtime data is available. |
| `kick_required` | `string[]` | yes | example: ["c0d4f871-6ea6-42b3-9e7b-f00cc1ec354e"] | Clients whose active sessions must be disconnected. |
| `runtime` | `object[]` | yes | example: [{"client_id":"c0d4f871-6ea6-42b3-9e7b-f00cc1ec354e","status":"ok","result":{"version":1,"client_id":"c0d4f871-6ea6-42b3-9e7b-f00cc1ec354e","client_name":"alice-laptop","was_connected":false,"disconnected":false,"connections":0}}] | Runtime convergence result when a live action was attempted. |
| `runtime[].client_id` | `string` | no | format: uuid; example: "c0d4f871-6ea6-42b3-9e7b-f00cc1ec354e" | Immutable client UUID. |
| `runtime[].status` | `"ok" \| "unavailable"` | yes | values: "ok" \| "unavailable" | Whether the requested runtime action completed or was unavailable. |
| `runtime[].result` | `object` | no | - | Result of requesting client session disconnection. |
| `runtime[].result.version` | `integer` | no | constant: 1 | Response contract schema version. |
| `runtime[].result.client_id` | `string` | no | format: uuid; example: "c0d4f871-6ea6-42b3-9e7b-f00cc1ec354e" | Immutable client UUID. |
| `runtime[].result.client_name` | `string` | no | example: "alice-laptop" | Human-readable client name associated with the session. |
| `runtime[].result.was_connected` | `boolean` | no | example: false | Whether a matching session existed before the request. |
| `runtime[].result.disconnected` | `boolean` | no | example: false | Whether at least one matching session was disconnected. |
| `runtime[].result.connections` | `integer` | no | minimum: 0; example: 0 | Number of matching sessions processed by the disconnect request. |

##### `400 Bad Request`

Malformed path, query, header, media type, body, or JSON.

Content type: `application/json`

Response example:

```json
{
  "error": {
    "kind": "invalid_json",
    "message": "request body is invalid",
    "request_id": "33ba813e-fc63-4af8-b338-7f7486f82202"
  }
}
```

Response fields:

| Field | Type | Required | Format / example / constraints | Purpose |
|---|---|---|---|---|
| `error` | `object` | yes | - | Machine-readable API error details. |
| `error.kind` | `string` | yes | example: "invalid_json" | Stable machine-readable error kind. |
| `error.message` | `string` | yes | example: "request body is invalid" | Safe human-readable error message. |
| `error.request_id` | `string` | yes | format: uuid; example: "33ba813e-fc63-4af8-b338-7f7486f82202" | UUID used to correlate the request with server logs. |

##### `401 Unauthorized`

Bearer API key is missing, malformed, unknown, or deleted.

Content type: `application/json`

Response example:

```json
{
  "error": {
    "kind": "unauthenticated",
    "message": "API key is missing or invalid",
    "request_id": "33ba813e-fc63-4af8-b338-7f7486f82202"
  }
}
```

Response fields:

| Field | Type | Required | Format / example / constraints | Purpose |
|---|---|---|---|---|
| `error` | `object` | yes | - | Machine-readable API error details. |
| `error.kind` | `string` | yes | example: "unauthenticated" | Stable machine-readable error kind. |
| `error.message` | `string` | yes | example: "API key is missing or invalid" | Safe human-readable error message. |
| `error.request_id` | `string` | yes | format: uuid; example: "33ba813e-fc63-4af8-b338-7f7486f82202" | UUID used to correlate the request with server logs. |

##### `404 Not Found`

Resource or client does not exist.

Content type: `application/json`

Response example:

```json
{
  "error": {
    "kind": "client_not_found",
    "message": "client was not found",
    "request_id": "33ba813e-fc63-4af8-b338-7f7486f82202"
  }
}
```

Response fields:

| Field | Type | Required | Format / example / constraints | Purpose |
|---|---|---|---|---|
| `error` | `object` | yes | - | Machine-readable API error details. |
| `error.kind` | `string` | yes | example: "client_not_found" | Stable machine-readable error kind. |
| `error.message` | `string` | yes | example: "client was not found" | Safe human-readable error message. |
| `error.request_id` | `string` | yes | format: uuid; example: "33ba813e-fc63-4af8-b338-7f7486f82202" | UUID used to correlate the request with server logs. |

##### `405 Method Not Allowed`

HTTP method is not accepted; inspect the Allow header.

Content type: `application/json`

Response example:

```json
{
  "error": {
    "kind": "method_not_allowed",
    "message": "method is not allowed",
    "request_id": "33ba813e-fc63-4af8-b338-7f7486f82202"
  }
}
```

Response fields:

| Field | Type | Required | Format / example / constraints | Purpose |
|---|---|---|---|---|
| `error` | `object` | yes | - | Machine-readable API error details. |
| `error.kind` | `string` | yes | example: "method_not_allowed" | Stable machine-readable error kind. |
| `error.message` | `string` | yes | example: "method is not allowed" | Safe human-readable error message. |
| `error.request_id` | `string` | yes | format: uuid; example: "33ba813e-fc63-4af8-b338-7f7486f82202" | UUID used to correlate the request with server logs. |

##### `409 Conflict`

Digest, revision, state, uniqueness, lock, or recovery conflict.

Content type: `application/json`

Response example:

```json
{
  "error": {
    "kind": "configuration_conflict",
    "message": "configuration state changed or is busy",
    "request_id": "33ba813e-fc63-4af8-b338-7f7486f82202"
  }
}
```

Response fields:

| Field | Type | Required | Format / example / constraints | Purpose |
|---|---|---|---|---|
| `error` | `object` | yes | - | Machine-readable API error details. |
| `error.kind` | `string` | yes | example: "configuration_conflict" | Stable machine-readable error kind. |
| `error.message` | `string` | yes | example: "configuration state changed or is busy" | Safe human-readable error message. |
| `error.request_id` | `string` | yes | format: uuid; example: "33ba813e-fc63-4af8-b338-7f7486f82202" | UUID used to correlate the request with server logs. |

##### `422 Unprocessable Entity`

JSON is valid but violates client, IPv4, or configuration semantics.

Content type: `application/json`

Response example:

```json
{
  "error": {
    "kind": "invalid_client",
    "message": "client request is not valid for the current state",
    "request_id": "33ba813e-fc63-4af8-b338-7f7486f82202"
  }
}
```

Response fields:

| Field | Type | Required | Format / example / constraints | Purpose |
|---|---|---|---|---|
| `error` | `object` | yes | - | Machine-readable API error details. |
| `error.kind` | `string` | yes | example: "invalid_client" | Stable machine-readable error kind. |
| `error.message` | `string` | yes | example: "client request is not valid for the current state" | Safe human-readable error message. |
| `error.request_id` | `string` | yes | format: uuid; example: "33ba813e-fc63-4af8-b338-7f7486f82202" | UUID used to correlate the request with server logs. |

##### `500 Internal Server Error`

Unclassified internal failure with no implementation details exposed.

Content type: `application/json`

Response example:

```json
{
  "error": {
    "kind": "internal_error",
    "message": "request could not be completed",
    "request_id": "33ba813e-fc63-4af8-b338-7f7486f82202"
  }
}
```

Response fields:

| Field | Type | Required | Format / example / constraints | Purpose |
|---|---|---|---|---|
| `error` | `object` | yes | - | Machine-readable API error details. |
| `error.kind` | `string` | yes | example: "internal_error" | Stable machine-readable error kind. |
| `error.message` | `string` | yes | example: "request could not be completed" | Safe human-readable error message. |
| `error.request_id` | `string` | yes | format: uuid; example: "33ba813e-fc63-4af8-b338-7f7486f82202" | UUID used to correlate the request with server logs. |

##### `503 Service Unavailable`

Authentication storage, PKI, OpenVPN, broker, supervisor, or another required dependency is unavailable.

Content type: `application/json`

Response example:

```json
{
  "error": {
    "kind": "runtime_unavailable",
    "message": "OpenVPN runtime is unavailable",
    "request_id": "33ba813e-fc63-4af8-b338-7f7486f82202"
  }
}
```

Response fields:

| Field | Type | Required | Format / example / constraints | Purpose |
|---|---|---|---|---|
| `error` | `object` | yes | - | Machine-readable API error details. |
| `error.kind` | `string` | yes | example: "runtime_unavailable" | Stable machine-readable error kind. |
| `error.message` | `string` | yes | example: "OpenVPN runtime is unavailable" | Safe human-readable error message. |
| `error.request_id` | `string` | yes | format: uuid; example: "33ba813e-fc63-4af8-b338-7f7486f82202" | UUID used to correlate the request with server logs. |

### 14. `DELETE /api/v1/clients/{client_id}/ipv4`

Release a revoked client's retained IPv4. Requires an empty body and is valid only for a revoked client with a retained assignment.

#### Request parameters

##### Header parameters

| Field | Location | Type | Required | Format / example / constraints | Purpose |
|---|---|---|---|---|---|
| `Authorization` | `header` | `string` | yes | Bearer ovpn_v1.<uuid>.<secret> | API authentication credential. Put the service-generated API key after Bearer. |

##### Path parameters

| Field | Location | Type | Required | Format / example / constraints | Purpose |
|---|---|---|---|---|---|
| `client_id` | `path` | `string` | yes | format: uuid; example: "c0d4f871-6ea6-42b3-9e7b-f00cc1ec354e" | Complete canonical immutable client UUID; names and UUID prefixes are rejected. |

#### Request example

```http
DELETE /api/v1/clients/c0d4f871-6ea6-42b3-9e7b-f00cc1ec354e/ipv4 HTTP/1.1
Authorization: Bearer ovpn_v1.<uuid>.<secret>
```

#### Responses

##### `200 OK`

Revoked client's retained address released.

Content type: `application/json`

Response example:

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

Response fields:

| Field | Type | Required | Format / example / constraints | Purpose |
|---|---|---|---|---|
| `version` | `integer` | yes | constant: 1 | Response contract schema version. |
| `operation_id` | `string` | yes | format: uuid; example: "d41dbfce-b40e-4672-8a62-cb401f8c099c" | UUID of the committed journal operation. |
| `clients` | `object[]` | yes | example: [{"id":"c0d4f871-6ea6-42b3-9e7b-f00cc1ec354e","name":"alice-notebook","status":"revoked","ipv4":{"mode":"none","address":null,"state":"unavailable"}}] | Clients included in this response. |
| `clients[].id` | `string` | yes | format: uuid; example: "c0d4f871-6ea6-42b3-9e7b-f00cc1ec354e" | Stable identifier of this object. |
| `clients[].name` | `string` | yes | format: ^[A-Za-z0-9][A-Za-z0-9_.-]*$; example: "alice-notebook" | Stable human-readable name. |
| `clients[].status` | `"active" \| "revoked" \| "deleted"` | yes | values: "active" \| "revoked" \| "deleted" | Client credential lifecycle status. |
| `clients[].ipv4` | `object` | yes | - | Current client IPv4 assignment view. |
| `clients[].ipv4.mode` | `"none" \| "static" \| "dynamic"` | yes | values: "none" \| "static" \| "dynamic" | IPv4 allocation mode. |
| `clients[].ipv4.address` | `string \| null` | yes | format: ipv4 | IPv4 address, or null when no address is assigned. |
| `clients[].ipv4.state` | `"configured" \| "retained" \| "unavailable"` | yes | values: "configured" \| "retained" \| "unavailable" | Availability state of the client address assignment. |
| `clients[].connection` | `string` | no | - | Current connection state when runtime data is available. |
| `kick_required` | `string[]` | yes | example: [] | Clients whose active sessions must be disconnected. |
| `runtime` | `object[]` | yes | example: [] | Runtime convergence result when a live action was attempted. |
| `runtime[].client_id` | `string` | no | format: uuid | Immutable client UUID. |
| `runtime[].status` | `"ok" \| "unavailable"` | yes | values: "ok" \| "unavailable" | Whether the requested runtime action completed or was unavailable. |
| `runtime[].result` | `object` | no | - | Result of requesting client session disconnection. |
| `runtime[].result.version` | `integer` | no | constant: 1 | Response contract schema version. |
| `runtime[].result.client_id` | `string` | no | format: uuid | Immutable client UUID. |
| `runtime[].result.client_name` | `string` | no | - | Human-readable client name associated with the session. |
| `runtime[].result.was_connected` | `boolean` | no | - | Whether a matching session existed before the request. |
| `runtime[].result.disconnected` | `boolean` | no | - | Whether at least one matching session was disconnected. |
| `runtime[].result.connections` | `integer` | no | minimum: 0 | Number of matching sessions processed by the disconnect request. |

##### `400 Bad Request`

Malformed path, query, header, media type, body, or JSON.

Content type: `application/json`

Response example:

```json
{
  "error": {
    "kind": "invalid_json",
    "message": "request body is invalid",
    "request_id": "33ba813e-fc63-4af8-b338-7f7486f82202"
  }
}
```

Response fields:

| Field | Type | Required | Format / example / constraints | Purpose |
|---|---|---|---|---|
| `error` | `object` | yes | - | Machine-readable API error details. |
| `error.kind` | `string` | yes | example: "invalid_json" | Stable machine-readable error kind. |
| `error.message` | `string` | yes | example: "request body is invalid" | Safe human-readable error message. |
| `error.request_id` | `string` | yes | format: uuid; example: "33ba813e-fc63-4af8-b338-7f7486f82202" | UUID used to correlate the request with server logs. |

##### `401 Unauthorized`

Bearer API key is missing, malformed, unknown, or deleted.

Content type: `application/json`

Response example:

```json
{
  "error": {
    "kind": "unauthenticated",
    "message": "API key is missing or invalid",
    "request_id": "33ba813e-fc63-4af8-b338-7f7486f82202"
  }
}
```

Response fields:

| Field | Type | Required | Format / example / constraints | Purpose |
|---|---|---|---|---|
| `error` | `object` | yes | - | Machine-readable API error details. |
| `error.kind` | `string` | yes | example: "unauthenticated" | Stable machine-readable error kind. |
| `error.message` | `string` | yes | example: "API key is missing or invalid" | Safe human-readable error message. |
| `error.request_id` | `string` | yes | format: uuid; example: "33ba813e-fc63-4af8-b338-7f7486f82202" | UUID used to correlate the request with server logs. |

##### `404 Not Found`

Resource or client does not exist.

Content type: `application/json`

Response example:

```json
{
  "error": {
    "kind": "client_not_found",
    "message": "client was not found",
    "request_id": "33ba813e-fc63-4af8-b338-7f7486f82202"
  }
}
```

Response fields:

| Field | Type | Required | Format / example / constraints | Purpose |
|---|---|---|---|---|
| `error` | `object` | yes | - | Machine-readable API error details. |
| `error.kind` | `string` | yes | example: "client_not_found" | Stable machine-readable error kind. |
| `error.message` | `string` | yes | example: "client was not found" | Safe human-readable error message. |
| `error.request_id` | `string` | yes | format: uuid; example: "33ba813e-fc63-4af8-b338-7f7486f82202" | UUID used to correlate the request with server logs. |

##### `405 Method Not Allowed`

HTTP method is not accepted; inspect the Allow header.

Content type: `application/json`

Response example:

```json
{
  "error": {
    "kind": "method_not_allowed",
    "message": "method is not allowed",
    "request_id": "33ba813e-fc63-4af8-b338-7f7486f82202"
  }
}
```

Response fields:

| Field | Type | Required | Format / example / constraints | Purpose |
|---|---|---|---|---|
| `error` | `object` | yes | - | Machine-readable API error details. |
| `error.kind` | `string` | yes | example: "method_not_allowed" | Stable machine-readable error kind. |
| `error.message` | `string` | yes | example: "method is not allowed" | Safe human-readable error message. |
| `error.request_id` | `string` | yes | format: uuid; example: "33ba813e-fc63-4af8-b338-7f7486f82202" | UUID used to correlate the request with server logs. |

##### `409 Conflict`

Digest, revision, state, uniqueness, lock, or recovery conflict.

Content type: `application/json`

Response example:

```json
{
  "error": {
    "kind": "configuration_conflict",
    "message": "configuration state changed or is busy",
    "request_id": "33ba813e-fc63-4af8-b338-7f7486f82202"
  }
}
```

Response fields:

| Field | Type | Required | Format / example / constraints | Purpose |
|---|---|---|---|---|
| `error` | `object` | yes | - | Machine-readable API error details. |
| `error.kind` | `string` | yes | example: "configuration_conflict" | Stable machine-readable error kind. |
| `error.message` | `string` | yes | example: "configuration state changed or is busy" | Safe human-readable error message. |
| `error.request_id` | `string` | yes | format: uuid; example: "33ba813e-fc63-4af8-b338-7f7486f82202" | UUID used to correlate the request with server logs. |

##### `422 Unprocessable Entity`

JSON is valid but violates client, IPv4, or configuration semantics.

Content type: `application/json`

Response example:

```json
{
  "error": {
    "kind": "invalid_client",
    "message": "client request is not valid for the current state",
    "request_id": "33ba813e-fc63-4af8-b338-7f7486f82202"
  }
}
```

Response fields:

| Field | Type | Required | Format / example / constraints | Purpose |
|---|---|---|---|---|
| `error` | `object` | yes | - | Machine-readable API error details. |
| `error.kind` | `string` | yes | example: "invalid_client" | Stable machine-readable error kind. |
| `error.message` | `string` | yes | example: "client request is not valid for the current state" | Safe human-readable error message. |
| `error.request_id` | `string` | yes | format: uuid; example: "33ba813e-fc63-4af8-b338-7f7486f82202" | UUID used to correlate the request with server logs. |

##### `500 Internal Server Error`

Unclassified internal failure with no implementation details exposed.

Content type: `application/json`

Response example:

```json
{
  "error": {
    "kind": "internal_error",
    "message": "request could not be completed",
    "request_id": "33ba813e-fc63-4af8-b338-7f7486f82202"
  }
}
```

Response fields:

| Field | Type | Required | Format / example / constraints | Purpose |
|---|---|---|---|---|
| `error` | `object` | yes | - | Machine-readable API error details. |
| `error.kind` | `string` | yes | example: "internal_error" | Stable machine-readable error kind. |
| `error.message` | `string` | yes | example: "request could not be completed" | Safe human-readable error message. |
| `error.request_id` | `string` | yes | format: uuid; example: "33ba813e-fc63-4af8-b338-7f7486f82202" | UUID used to correlate the request with server logs. |

##### `503 Service Unavailable`

Authentication storage, PKI, OpenVPN, broker, supervisor, or another required dependency is unavailable.

Content type: `application/json`

Response example:

```json
{
  "error": {
    "kind": "runtime_unavailable",
    "message": "OpenVPN runtime is unavailable",
    "request_id": "33ba813e-fc63-4af8-b338-7f7486f82202"
  }
}
```

Response fields:

| Field | Type | Required | Format / example / constraints | Purpose |
|---|---|---|---|---|
| `error` | `object` | yes | - | Machine-readable API error details. |
| `error.kind` | `string` | yes | example: "runtime_unavailable" | Stable machine-readable error kind. |
| `error.message` | `string` | yes | example: "OpenVPN runtime is unavailable" | Safe human-readable error message. |
| `error.request_id` | `string` | yes | format: uuid; example: "33ba813e-fc63-4af8-b338-7f7486f82202" | UUID used to correlate the request with server logs. |

### 15. `POST /api/v1/clients/{client_id}/disconnect`

Disconnect current client sessions. Requires an empty body. A client with no active session returns 200 with was_connected=false.

#### Request parameters

##### Header parameters

| Field | Location | Type | Required | Format / example / constraints | Purpose |
|---|---|---|---|---|---|
| `Authorization` | `header` | `string` | yes | Bearer ovpn_v1.<uuid>.<secret> | API authentication credential. Put the service-generated API key after Bearer. |

##### Path parameters

| Field | Location | Type | Required | Format / example / constraints | Purpose |
|---|---|---|---|---|---|
| `client_id` | `path` | `string` | yes | format: uuid; example: "c0d4f871-6ea6-42b3-9e7b-f00cc1ec354e" | Complete canonical immutable client UUID; names and UUID prefixes are rejected. |

#### Request example

```http
POST /api/v1/clients/c0d4f871-6ea6-42b3-9e7b-f00cc1ec354e/disconnect HTTP/1.1
Authorization: Bearer ovpn_v1.<uuid>.<secret>
```

#### Responses

##### `200 OK`

Disconnect outcome, including successful no-op when no session exists.

Content type: `application/json`

Response example:

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

Response fields:

| Field | Type | Required | Format / example / constraints | Purpose |
|---|---|---|---|---|
| `version` | `integer` | yes | constant: 1 | Response contract schema version. |
| `client_id` | `string` | yes | format: uuid; example: "c0d4f871-6ea6-42b3-9e7b-f00cc1ec354e" | Immutable client UUID. |
| `client_name` | `string` | yes | example: "alice-laptop" | Human-readable client name associated with the session. |
| `was_connected` | `boolean` | yes | example: false | Whether a matching session existed before the request. |
| `disconnected` | `boolean` | yes | example: false | Whether at least one matching session was disconnected. |
| `connections` | `integer` | yes | minimum: 0; example: 0 | Number of matching sessions processed by the disconnect request. |

##### `400 Bad Request`

Malformed path, query, header, media type, body, or JSON.

Content type: `application/json`

Response example:

```json
{
  "error": {
    "kind": "invalid_json",
    "message": "request body is invalid",
    "request_id": "33ba813e-fc63-4af8-b338-7f7486f82202"
  }
}
```

Response fields:

| Field | Type | Required | Format / example / constraints | Purpose |
|---|---|---|---|---|
| `error` | `object` | yes | - | Machine-readable API error details. |
| `error.kind` | `string` | yes | example: "invalid_json" | Stable machine-readable error kind. |
| `error.message` | `string` | yes | example: "request body is invalid" | Safe human-readable error message. |
| `error.request_id` | `string` | yes | format: uuid; example: "33ba813e-fc63-4af8-b338-7f7486f82202" | UUID used to correlate the request with server logs. |

##### `401 Unauthorized`

Bearer API key is missing, malformed, unknown, or deleted.

Content type: `application/json`

Response example:

```json
{
  "error": {
    "kind": "unauthenticated",
    "message": "API key is missing or invalid",
    "request_id": "33ba813e-fc63-4af8-b338-7f7486f82202"
  }
}
```

Response fields:

| Field | Type | Required | Format / example / constraints | Purpose |
|---|---|---|---|---|
| `error` | `object` | yes | - | Machine-readable API error details. |
| `error.kind` | `string` | yes | example: "unauthenticated" | Stable machine-readable error kind. |
| `error.message` | `string` | yes | example: "API key is missing or invalid" | Safe human-readable error message. |
| `error.request_id` | `string` | yes | format: uuid; example: "33ba813e-fc63-4af8-b338-7f7486f82202" | UUID used to correlate the request with server logs. |

##### `404 Not Found`

Resource or client does not exist.

Content type: `application/json`

Response example:

```json
{
  "error": {
    "kind": "client_not_found",
    "message": "client was not found",
    "request_id": "33ba813e-fc63-4af8-b338-7f7486f82202"
  }
}
```

Response fields:

| Field | Type | Required | Format / example / constraints | Purpose |
|---|---|---|---|---|
| `error` | `object` | yes | - | Machine-readable API error details. |
| `error.kind` | `string` | yes | example: "client_not_found" | Stable machine-readable error kind. |
| `error.message` | `string` | yes | example: "client was not found" | Safe human-readable error message. |
| `error.request_id` | `string` | yes | format: uuid; example: "33ba813e-fc63-4af8-b338-7f7486f82202" | UUID used to correlate the request with server logs. |

##### `405 Method Not Allowed`

HTTP method is not accepted; inspect the Allow header.

Content type: `application/json`

Response example:

```json
{
  "error": {
    "kind": "method_not_allowed",
    "message": "method is not allowed",
    "request_id": "33ba813e-fc63-4af8-b338-7f7486f82202"
  }
}
```

Response fields:

| Field | Type | Required | Format / example / constraints | Purpose |
|---|---|---|---|---|
| `error` | `object` | yes | - | Machine-readable API error details. |
| `error.kind` | `string` | yes | example: "method_not_allowed" | Stable machine-readable error kind. |
| `error.message` | `string` | yes | example: "method is not allowed" | Safe human-readable error message. |
| `error.request_id` | `string` | yes | format: uuid; example: "33ba813e-fc63-4af8-b338-7f7486f82202" | UUID used to correlate the request with server logs. |

##### `500 Internal Server Error`

Unclassified internal failure with no implementation details exposed.

Content type: `application/json`

Response example:

```json
{
  "error": {
    "kind": "internal_error",
    "message": "request could not be completed",
    "request_id": "33ba813e-fc63-4af8-b338-7f7486f82202"
  }
}
```

Response fields:

| Field | Type | Required | Format / example / constraints | Purpose |
|---|---|---|---|---|
| `error` | `object` | yes | - | Machine-readable API error details. |
| `error.kind` | `string` | yes | example: "internal_error" | Stable machine-readable error kind. |
| `error.message` | `string` | yes | example: "request could not be completed" | Safe human-readable error message. |
| `error.request_id` | `string` | yes | format: uuid; example: "33ba813e-fc63-4af8-b338-7f7486f82202" | UUID used to correlate the request with server logs. |

##### `503 Service Unavailable`

Authentication storage, PKI, OpenVPN, broker, supervisor, or another required dependency is unavailable.

Content type: `application/json`

Response example:

```json
{
  "error": {
    "kind": "runtime_unavailable",
    "message": "OpenVPN runtime is unavailable",
    "request_id": "33ba813e-fc63-4af8-b338-7f7486f82202"
  }
}
```

Response fields:

| Field | Type | Required | Format / example / constraints | Purpose |
|---|---|---|---|---|
| `error` | `object` | yes | - | Machine-readable API error details. |
| `error.kind` | `string` | yes | example: "runtime_unavailable" | Stable machine-readable error kind. |
| `error.message` | `string` | yes | example: "OpenVPN runtime is unavailable" | Safe human-readable error message. |
| `error.request_id` | `string` | yes | format: uuid; example: "33ba813e-fc63-4af8-b338-7f7486f82202" | UUID used to correlate the request with server logs. |

### 16. `GET /api/v1/runtime`

Read OpenVPN runtime status and sessions

#### Request parameters

##### Header parameters

| Field | Location | Type | Required | Format / example / constraints | Purpose |
|---|---|---|---|---|---|
| `Authorization` | `header` | `string` | yes | Bearer ovpn_v1.<uuid>.<secret> | API authentication credential. Put the service-generated API key after Bearer. |

#### Request example

```http
GET /api/v1/runtime HTTP/1.1
Authorization: Bearer ovpn_v1.<uuid>.<secret>
```

#### Responses

##### `200 OK`

OpenVPN runtime status.

Content type: `application/json`

Response example:

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

Response fields:

| Field | Type | Required | Format / example / constraints | Purpose |
|---|---|---|---|---|
| `version` | `integer` | yes | constant: 1 | Response contract schema version. |
| `daemon` | `string` | yes | example: "running" | OpenVPN daemon process state. |
| `management` | `string` | yes | example: "connected" | OpenVPN management interface connection state. |
| `client_count` | `integer` | yes | minimum: 0; example: 1 | Number of client sessions reported by the runtime. |
| `clients` | `object[]` | yes | example: [{"client_id":"c0d4f871-6ea6-42b3-9e7b-f00cc1ec354e","client_name":"alice-laptop","remote_address":"203.0.113.10:53210","virtual_address":"10.42.0.30"}] | Clients included in this response. |
| `clients[].client_id` | `string` | yes | format: uuid; example: "c0d4f871-6ea6-42b3-9e7b-f00cc1ec354e" | Immutable client UUID. |
| `clients[].client_name` | `string` | no | example: "alice-laptop" | Human-readable client name associated with the session. |
| `clients[].remote_address` | `string` | no | example: "203.0.113.10:53210" | Remote network address of the connected client. |
| `clients[].virtual_address` | `string` | no | format: ipv4; example: "10.42.0.30" | VPN virtual IPv4 address assigned to the session. |

##### `401 Unauthorized`

Bearer API key is missing, malformed, unknown, or deleted.

Content type: `application/json`

Response example:

```json
{
  "error": {
    "kind": "unauthenticated",
    "message": "API key is missing or invalid",
    "request_id": "33ba813e-fc63-4af8-b338-7f7486f82202"
  }
}
```

Response fields:

| Field | Type | Required | Format / example / constraints | Purpose |
|---|---|---|---|---|
| `error` | `object` | yes | - | Machine-readable API error details. |
| `error.kind` | `string` | yes | example: "unauthenticated" | Stable machine-readable error kind. |
| `error.message` | `string` | yes | example: "API key is missing or invalid" | Safe human-readable error message. |
| `error.request_id` | `string` | yes | format: uuid; example: "33ba813e-fc63-4af8-b338-7f7486f82202" | UUID used to correlate the request with server logs. |

##### `405 Method Not Allowed`

HTTP method is not accepted; inspect the Allow header.

Content type: `application/json`

Response example:

```json
{
  "error": {
    "kind": "method_not_allowed",
    "message": "method is not allowed",
    "request_id": "33ba813e-fc63-4af8-b338-7f7486f82202"
  }
}
```

Response fields:

| Field | Type | Required | Format / example / constraints | Purpose |
|---|---|---|---|---|
| `error` | `object` | yes | - | Machine-readable API error details. |
| `error.kind` | `string` | yes | example: "method_not_allowed" | Stable machine-readable error kind. |
| `error.message` | `string` | yes | example: "method is not allowed" | Safe human-readable error message. |
| `error.request_id` | `string` | yes | format: uuid; example: "33ba813e-fc63-4af8-b338-7f7486f82202" | UUID used to correlate the request with server logs. |

##### `500 Internal Server Error`

Unclassified internal failure with no implementation details exposed.

Content type: `application/json`

Response example:

```json
{
  "error": {
    "kind": "internal_error",
    "message": "request could not be completed",
    "request_id": "33ba813e-fc63-4af8-b338-7f7486f82202"
  }
}
```

Response fields:

| Field | Type | Required | Format / example / constraints | Purpose |
|---|---|---|---|---|
| `error` | `object` | yes | - | Machine-readable API error details. |
| `error.kind` | `string` | yes | example: "internal_error" | Stable machine-readable error kind. |
| `error.message` | `string` | yes | example: "request could not be completed" | Safe human-readable error message. |
| `error.request_id` | `string` | yes | format: uuid; example: "33ba813e-fc63-4af8-b338-7f7486f82202" | UUID used to correlate the request with server logs. |

##### `503 Service Unavailable`

Authentication storage, PKI, OpenVPN, broker, supervisor, or another required dependency is unavailable.

Content type: `application/json`

Response example:

```json
{
  "error": {
    "kind": "runtime_unavailable",
    "message": "OpenVPN runtime is unavailable",
    "request_id": "33ba813e-fc63-4af8-b338-7f7486f82202"
  }
}
```

Response fields:

| Field | Type | Required | Format / example / constraints | Purpose |
|---|---|---|---|---|
| `error` | `object` | yes | - | Machine-readable API error details. |
| `error.kind` | `string` | yes | example: "runtime_unavailable" | Stable machine-readable error kind. |
| `error.message` | `string` | yes | example: "OpenVPN runtime is unavailable" | Safe human-readable error message. |
| `error.request_id` | `string` | yes | format: uuid; example: "33ba813e-fc63-4af8-b338-7f7486f82202" | UUID used to correlate the request with server logs. |

### 17. `GET /api/v1/runtime/events`

Read recent structured runtime events

#### Request parameters

##### Header parameters

| Field | Location | Type | Required | Format / example / constraints | Purpose |
|---|---|---|---|---|---|
| `Authorization` | `header` | `string` | yes | Bearer ovpn_v1.<uuid>.<secret> | API authentication credential. Put the service-generated API key after Bearer. |

##### Query parameters

| Field | Location | Type | Required | Format / example / constraints | Purpose |
|---|---|---|---|---|---|
| `lines` | `query` | `integer` | no | minimum: 0; maximum: 1000; default: 100; example: 100 | Number of most recent events. Defaults to 100. |

#### Request example

```http
GET /api/v1/runtime/events?lines=100 HTTP/1.1
Authorization: Bearer ovpn_v1.<uuid>.<secret>
```

#### Responses

##### `200 OK`

Recent structured runtime events.

Content type: `application/json`

Response example:

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

Response fields:

| Field | Type | Required | Format / example / constraints | Purpose |
|---|---|---|---|---|
| `version` | `integer` | yes | constant: 1 | Response contract schema version. |
| `events` | `object[]` | yes | example: [{"timestamp":"2026-08-19T09:55:07Z","event":"client-disconnect","operation":"runtime.disconnect","outcome":"success","client_id":"c0d4f871-6ea6-42b3-9e7b-f00cc1ec354e","client_name":"alice-laptop"}] | Recent structured runtime events in chronological order. |
| `events[].timestamp` | `string` | yes | format: date-time; example: "2026-08-19T09:55:07Z" | UTC timestamp when the runtime event occurred. |
| `events[].event` | `string` | yes | example: "client-disconnect" | Stable runtime event name. |
| `events[].operation` | `string` | yes | example: "runtime.disconnect" | Stable operation name that emitted the event. |
| `events[].outcome` | `string` | yes | example: "success" | Stable event outcome value. |
| `events[].client_id` | `string \| null` | no | format: uuid; example: "c0d4f871-6ea6-42b3-9e7b-f00cc1ec354e" | Client UUID associated with the event, or null for non-client events. |
| `events[].client_name` | `string \| null` | no | example: "alice-laptop" | Client name associated with the event, or null when unavailable. |

##### `400 Bad Request`

Malformed path, query, header, media type, body, or JSON.

Content type: `application/json`

Response example:

```json
{
  "error": {
    "kind": "invalid_json",
    "message": "request body is invalid",
    "request_id": "33ba813e-fc63-4af8-b338-7f7486f82202"
  }
}
```

Response fields:

| Field | Type | Required | Format / example / constraints | Purpose |
|---|---|---|---|---|
| `error` | `object` | yes | - | Machine-readable API error details. |
| `error.kind` | `string` | yes | example: "invalid_json" | Stable machine-readable error kind. |
| `error.message` | `string` | yes | example: "request body is invalid" | Safe human-readable error message. |
| `error.request_id` | `string` | yes | format: uuid; example: "33ba813e-fc63-4af8-b338-7f7486f82202" | UUID used to correlate the request with server logs. |

##### `401 Unauthorized`

Bearer API key is missing, malformed, unknown, or deleted.

Content type: `application/json`

Response example:

```json
{
  "error": {
    "kind": "unauthenticated",
    "message": "API key is missing or invalid",
    "request_id": "33ba813e-fc63-4af8-b338-7f7486f82202"
  }
}
```

Response fields:

| Field | Type | Required | Format / example / constraints | Purpose |
|---|---|---|---|---|
| `error` | `object` | yes | - | Machine-readable API error details. |
| `error.kind` | `string` | yes | example: "unauthenticated" | Stable machine-readable error kind. |
| `error.message` | `string` | yes | example: "API key is missing or invalid" | Safe human-readable error message. |
| `error.request_id` | `string` | yes | format: uuid; example: "33ba813e-fc63-4af8-b338-7f7486f82202" | UUID used to correlate the request with server logs. |

##### `405 Method Not Allowed`

HTTP method is not accepted; inspect the Allow header.

Content type: `application/json`

Response example:

```json
{
  "error": {
    "kind": "method_not_allowed",
    "message": "method is not allowed",
    "request_id": "33ba813e-fc63-4af8-b338-7f7486f82202"
  }
}
```

Response fields:

| Field | Type | Required | Format / example / constraints | Purpose |
|---|---|---|---|---|
| `error` | `object` | yes | - | Machine-readable API error details. |
| `error.kind` | `string` | yes | example: "method_not_allowed" | Stable machine-readable error kind. |
| `error.message` | `string` | yes | example: "method is not allowed" | Safe human-readable error message. |
| `error.request_id` | `string` | yes | format: uuid; example: "33ba813e-fc63-4af8-b338-7f7486f82202" | UUID used to correlate the request with server logs. |

##### `500 Internal Server Error`

Unclassified internal failure with no implementation details exposed.

Content type: `application/json`

Response example:

```json
{
  "error": {
    "kind": "internal_error",
    "message": "request could not be completed",
    "request_id": "33ba813e-fc63-4af8-b338-7f7486f82202"
  }
}
```

Response fields:

| Field | Type | Required | Format / example / constraints | Purpose |
|---|---|---|---|---|
| `error` | `object` | yes | - | Machine-readable API error details. |
| `error.kind` | `string` | yes | example: "internal_error" | Stable machine-readable error kind. |
| `error.message` | `string` | yes | example: "request could not be completed" | Safe human-readable error message. |
| `error.request_id` | `string` | yes | format: uuid; example: "33ba813e-fc63-4af8-b338-7f7486f82202" | UUID used to correlate the request with server logs. |

##### `503 Service Unavailable`

Authentication storage, PKI, OpenVPN, broker, supervisor, or another required dependency is unavailable.

Content type: `application/json`

Response example:

```json
{
  "error": {
    "kind": "runtime_unavailable",
    "message": "OpenVPN runtime is unavailable",
    "request_id": "33ba813e-fc63-4af8-b338-7f7486f82202"
  }
}
```

Response fields:

| Field | Type | Required | Format / example / constraints | Purpose |
|---|---|---|---|---|
| `error` | `object` | yes | - | Machine-readable API error details. |
| `error.kind` | `string` | yes | example: "runtime_unavailable" | Stable machine-readable error kind. |
| `error.message` | `string` | yes | example: "OpenVPN runtime is unavailable" | Safe human-readable error message. |
| `error.request_id` | `string` | yes | format: uuid; example: "33ba813e-fc63-4af8-b338-7f7486f82202" | UUID used to correlate the request with server logs. |

### 18. `GET /api/v1/config/applied`

Read the applied configuration revision

#### Request parameters

##### Header parameters

| Field | Location | Type | Required | Format / example / constraints | Purpose |
|---|---|---|---|---|---|
| `Authorization` | `header` | `string` | yes | Bearer ovpn_v1.<uuid>.<secret> | API authentication credential. Put the service-generated API key after Bearer. |

#### Request example

```http
GET /api/v1/config/applied HTTP/1.1
Authorization: Bearer ovpn_v1.<uuid>.<secret>
```

#### Responses

##### `200 OK`

Applied revision and normalized configuration.

Content type: `application/json`

Response example:

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

Response fields:

| Field | Type | Required | Format / example / constraints | Purpose |
|---|---|---|---|---|
| `revision` | `integer` | yes | minimum: 1; example: 12 | Monotonic applied configuration revision. |
| `digest` | `string` | yes | format: ^[0-9a-f]{64}$; example: "be7ed82796b39a77ce949201a4d9483cf09e56648f14b8b9b192bb83d2c4d042" | Lowercase SHA-256 digest identifying an exact desired configuration. |
| `config` | `object` | yes | - | Complete normalized OpenVPN server configuration. |
| `config.version` | `integer` | yes | constant: 1 | Configuration schema version. Must be 1. |
| `config.server` | `object` | yes | - | OpenVPN listener and transport settings. |
| `config.server.endpoint` | `string` | yes | example: "vpn.example.com" | Public hostname or IP address placed in generated client profiles. |
| `config.server.protocol` | `"udp" \| "tcp"` | yes | values: "udp" \| "tcp" | OpenVPN transport protocol. |
| `config.server.family` | `"auto" \| "ipv4" \| "ipv6"` | yes | values: "auto" \| "ipv4" \| "ipv6" | Address family used for the server transport. |
| `config.server.port` | `integer` | yes | minimum: 1; maximum: 65535; example: 1194 | OpenVPN listener port. |
| `config.server.client_to_client` | `boolean` | yes | example: true | Whether connected VPN clients may communicate directly. |
| `config.ipv4` | `object` | yes | - | Server IPv4 network, allocation, NAT, DNS, and route settings. |
| `config.ipv4.network` | `string` | yes | format: ipv4-cidr; example: "10.42.0.0/24" | VPN IPv4 network in CIDR notation. |
| `config.ipv4.dynamic_pool_size` | `integer` | yes | minimum: 0; example: 64 | Number of addresses reserved for dynamic allocation. |
| `config.ipv4.nat_enabled` | `boolean` | yes | example: false | Whether outbound traffic from the VPN network is masqueraded. |
| `config.ipv4.nat_interface` | `string` | yes | example: "auto" | Outbound interface for NAT, or auto for automatic detection. |
| `config.ipv4.redirect_gateway` | `boolean` | yes | example: false | Whether generated profiles redirect the default IPv4 route through VPN. |
| `config.ipv4.dns` | `string[]` | yes | example: [] | IPv4 DNS servers pushed to clients. |
| `config.ipv4.routes` | `string[]` | yes | example: [] | Additional IPv4 CIDR routes pushed to clients. |
| `config.logging` | `object` | yes | - | Runtime log rotation settings. |
| `config.logging.max_bytes` | `integer` | yes | minimum: 1; example: 10485760 | Maximum active log file size before rotation. |
| `config.logging.backups` | `integer` | yes | minimum: 0; example: 5 | Number of rotated log files retained. |

##### `401 Unauthorized`

Bearer API key is missing, malformed, unknown, or deleted.

Content type: `application/json`

Response example:

```json
{
  "error": {
    "kind": "unauthenticated",
    "message": "API key is missing or invalid",
    "request_id": "33ba813e-fc63-4af8-b338-7f7486f82202"
  }
}
```

Response fields:

| Field | Type | Required | Format / example / constraints | Purpose |
|---|---|---|---|---|
| `error` | `object` | yes | - | Machine-readable API error details. |
| `error.kind` | `string` | yes | example: "unauthenticated" | Stable machine-readable error kind. |
| `error.message` | `string` | yes | example: "API key is missing or invalid" | Safe human-readable error message. |
| `error.request_id` | `string` | yes | format: uuid; example: "33ba813e-fc63-4af8-b338-7f7486f82202" | UUID used to correlate the request with server logs. |

##### `405 Method Not Allowed`

HTTP method is not accepted; inspect the Allow header.

Content type: `application/json`

Response example:

```json
{
  "error": {
    "kind": "method_not_allowed",
    "message": "method is not allowed",
    "request_id": "33ba813e-fc63-4af8-b338-7f7486f82202"
  }
}
```

Response fields:

| Field | Type | Required | Format / example / constraints | Purpose |
|---|---|---|---|---|
| `error` | `object` | yes | - | Machine-readable API error details. |
| `error.kind` | `string` | yes | example: "method_not_allowed" | Stable machine-readable error kind. |
| `error.message` | `string` | yes | example: "method is not allowed" | Safe human-readable error message. |
| `error.request_id` | `string` | yes | format: uuid; example: "33ba813e-fc63-4af8-b338-7f7486f82202" | UUID used to correlate the request with server logs. |

##### `409 Conflict`

Digest, revision, state, uniqueness, lock, or recovery conflict.

Content type: `application/json`

Response example:

```json
{
  "error": {
    "kind": "configuration_conflict",
    "message": "configuration state changed or is busy",
    "request_id": "33ba813e-fc63-4af8-b338-7f7486f82202"
  }
}
```

Response fields:

| Field | Type | Required | Format / example / constraints | Purpose |
|---|---|---|---|---|
| `error` | `object` | yes | - | Machine-readable API error details. |
| `error.kind` | `string` | yes | example: "configuration_conflict" | Stable machine-readable error kind. |
| `error.message` | `string` | yes | example: "configuration state changed or is busy" | Safe human-readable error message. |
| `error.request_id` | `string` | yes | format: uuid; example: "33ba813e-fc63-4af8-b338-7f7486f82202" | UUID used to correlate the request with server logs. |

##### `500 Internal Server Error`

Unclassified internal failure with no implementation details exposed.

Content type: `application/json`

Response example:

```json
{
  "error": {
    "kind": "internal_error",
    "message": "request could not be completed",
    "request_id": "33ba813e-fc63-4af8-b338-7f7486f82202"
  }
}
```

Response fields:

| Field | Type | Required | Format / example / constraints | Purpose |
|---|---|---|---|---|
| `error` | `object` | yes | - | Machine-readable API error details. |
| `error.kind` | `string` | yes | example: "internal_error" | Stable machine-readable error kind. |
| `error.message` | `string` | yes | example: "request could not be completed" | Safe human-readable error message. |
| `error.request_id` | `string` | yes | format: uuid; example: "33ba813e-fc63-4af8-b338-7f7486f82202" | UUID used to correlate the request with server logs. |

##### `503 Service Unavailable`

Authentication storage, PKI, OpenVPN, broker, supervisor, or another required dependency is unavailable.

Content type: `application/json`

Response example:

```json
{
  "error": {
    "kind": "runtime_unavailable",
    "message": "OpenVPN runtime is unavailable",
    "request_id": "33ba813e-fc63-4af8-b338-7f7486f82202"
  }
}
```

Response fields:

| Field | Type | Required | Format / example / constraints | Purpose |
|---|---|---|---|---|
| `error` | `object` | yes | - | Machine-readable API error details. |
| `error.kind` | `string` | yes | example: "runtime_unavailable" | Stable machine-readable error kind. |
| `error.message` | `string` | yes | example: "OpenVPN runtime is unavailable" | Safe human-readable error message. |
| `error.request_id` | `string` | yes | format: uuid; example: "33ba813e-fc63-4af8-b338-7f7486f82202" | UUID used to correlate the request with server logs. |

### 19. `GET /api/v1/config/desired`

Read desired configuration and CAS digest

#### Request parameters

##### Header parameters

| Field | Location | Type | Required | Format / example / constraints | Purpose |
|---|---|---|---|---|---|
| `Authorization` | `header` | `string` | yes | Bearer ovpn_v1.<uuid>.<secret> | API authentication credential. Put the service-generated API key after Bearer. |

#### Request example

```http
GET /api/v1/config/desired HTTP/1.1
Authorization: Bearer ovpn_v1.<uuid>.<secret>
```

#### Responses

##### `200 OK`

Desired digest and normalized configuration.

Content type: `application/json`

Response example:

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

Response fields:

| Field | Type | Required | Format / example / constraints | Purpose |
|---|---|---|---|---|
| `digest` | `string` | yes | format: ^[0-9a-f]{64}$; example: "be7ed82796b39a77ce949201a4d9483cf09e56648f14b8b9b192bb83d2c4d042" | Lowercase SHA-256 digest identifying an exact desired configuration. |
| `config` | `object` | yes | - | Complete normalized OpenVPN server configuration. |
| `config.version` | `integer` | yes | constant: 1 | Configuration schema version. Must be 1. |
| `config.server` | `object` | yes | - | OpenVPN listener and transport settings. |
| `config.server.endpoint` | `string` | yes | example: "vpn.example.com" | Public hostname or IP address placed in generated client profiles. |
| `config.server.protocol` | `"udp" \| "tcp"` | yes | values: "udp" \| "tcp" | OpenVPN transport protocol. |
| `config.server.family` | `"auto" \| "ipv4" \| "ipv6"` | yes | values: "auto" \| "ipv4" \| "ipv6" | Address family used for the server transport. |
| `config.server.port` | `integer` | yes | minimum: 1; maximum: 65535; example: 1194 | OpenVPN listener port. |
| `config.server.client_to_client` | `boolean` | yes | example: true | Whether connected VPN clients may communicate directly. |
| `config.ipv4` | `object` | yes | - | Server IPv4 network, allocation, NAT, DNS, and route settings. |
| `config.ipv4.network` | `string` | yes | format: ipv4-cidr; example: "10.42.0.0/24" | VPN IPv4 network in CIDR notation. |
| `config.ipv4.dynamic_pool_size` | `integer` | yes | minimum: 0; example: 64 | Number of addresses reserved for dynamic allocation. |
| `config.ipv4.nat_enabled` | `boolean` | yes | example: false | Whether outbound traffic from the VPN network is masqueraded. |
| `config.ipv4.nat_interface` | `string` | yes | example: "auto" | Outbound interface for NAT, or auto for automatic detection. |
| `config.ipv4.redirect_gateway` | `boolean` | yes | example: false | Whether generated profiles redirect the default IPv4 route through VPN. |
| `config.ipv4.dns` | `string[]` | yes | example: [] | IPv4 DNS servers pushed to clients. |
| `config.ipv4.routes` | `string[]` | yes | example: [] | Additional IPv4 CIDR routes pushed to clients. |
| `config.logging` | `object` | yes | - | Runtime log rotation settings. |
| `config.logging.max_bytes` | `integer` | yes | minimum: 1; example: 10485760 | Maximum active log file size before rotation. |
| `config.logging.backups` | `integer` | yes | minimum: 0; example: 5 | Number of rotated log files retained. |

##### `401 Unauthorized`

Bearer API key is missing, malformed, unknown, or deleted.

Content type: `application/json`

Response example:

```json
{
  "error": {
    "kind": "unauthenticated",
    "message": "API key is missing or invalid",
    "request_id": "33ba813e-fc63-4af8-b338-7f7486f82202"
  }
}
```

Response fields:

| Field | Type | Required | Format / example / constraints | Purpose |
|---|---|---|---|---|
| `error` | `object` | yes | - | Machine-readable API error details. |
| `error.kind` | `string` | yes | example: "unauthenticated" | Stable machine-readable error kind. |
| `error.message` | `string` | yes | example: "API key is missing or invalid" | Safe human-readable error message. |
| `error.request_id` | `string` | yes | format: uuid; example: "33ba813e-fc63-4af8-b338-7f7486f82202" | UUID used to correlate the request with server logs. |

##### `405 Method Not Allowed`

HTTP method is not accepted; inspect the Allow header.

Content type: `application/json`

Response example:

```json
{
  "error": {
    "kind": "method_not_allowed",
    "message": "method is not allowed",
    "request_id": "33ba813e-fc63-4af8-b338-7f7486f82202"
  }
}
```

Response fields:

| Field | Type | Required | Format / example / constraints | Purpose |
|---|---|---|---|---|
| `error` | `object` | yes | - | Machine-readable API error details. |
| `error.kind` | `string` | yes | example: "method_not_allowed" | Stable machine-readable error kind. |
| `error.message` | `string` | yes | example: "method is not allowed" | Safe human-readable error message. |
| `error.request_id` | `string` | yes | format: uuid; example: "33ba813e-fc63-4af8-b338-7f7486f82202" | UUID used to correlate the request with server logs. |

##### `409 Conflict`

Digest, revision, state, uniqueness, lock, or recovery conflict.

Content type: `application/json`

Response example:

```json
{
  "error": {
    "kind": "configuration_conflict",
    "message": "configuration state changed or is busy",
    "request_id": "33ba813e-fc63-4af8-b338-7f7486f82202"
  }
}
```

Response fields:

| Field | Type | Required | Format / example / constraints | Purpose |
|---|---|---|---|---|
| `error` | `object` | yes | - | Machine-readable API error details. |
| `error.kind` | `string` | yes | example: "configuration_conflict" | Stable machine-readable error kind. |
| `error.message` | `string` | yes | example: "configuration state changed or is busy" | Safe human-readable error message. |
| `error.request_id` | `string` | yes | format: uuid; example: "33ba813e-fc63-4af8-b338-7f7486f82202" | UUID used to correlate the request with server logs. |

##### `422 Unprocessable Entity`

JSON is valid but violates client, IPv4, or configuration semantics.

Content type: `application/json`

Response example:

```json
{
  "error": {
    "kind": "invalid_client",
    "message": "client request is not valid for the current state",
    "request_id": "33ba813e-fc63-4af8-b338-7f7486f82202"
  }
}
```

Response fields:

| Field | Type | Required | Format / example / constraints | Purpose |
|---|---|---|---|---|
| `error` | `object` | yes | - | Machine-readable API error details. |
| `error.kind` | `string` | yes | example: "invalid_client" | Stable machine-readable error kind. |
| `error.message` | `string` | yes | example: "client request is not valid for the current state" | Safe human-readable error message. |
| `error.request_id` | `string` | yes | format: uuid; example: "33ba813e-fc63-4af8-b338-7f7486f82202" | UUID used to correlate the request with server logs. |

##### `500 Internal Server Error`

Unclassified internal failure with no implementation details exposed.

Content type: `application/json`

Response example:

```json
{
  "error": {
    "kind": "internal_error",
    "message": "request could not be completed",
    "request_id": "33ba813e-fc63-4af8-b338-7f7486f82202"
  }
}
```

Response fields:

| Field | Type | Required | Format / example / constraints | Purpose |
|---|---|---|---|---|
| `error` | `object` | yes | - | Machine-readable API error details. |
| `error.kind` | `string` | yes | example: "internal_error" | Stable machine-readable error kind. |
| `error.message` | `string` | yes | example: "request could not be completed" | Safe human-readable error message. |
| `error.request_id` | `string` | yes | format: uuid; example: "33ba813e-fc63-4af8-b338-7f7486f82202" | UUID used to correlate the request with server logs. |

##### `503 Service Unavailable`

Authentication storage, PKI, OpenVPN, broker, supervisor, or another required dependency is unavailable.

Content type: `application/json`

Response example:

```json
{
  "error": {
    "kind": "runtime_unavailable",
    "message": "OpenVPN runtime is unavailable",
    "request_id": "33ba813e-fc63-4af8-b338-7f7486f82202"
  }
}
```

Response fields:

| Field | Type | Required | Format / example / constraints | Purpose |
|---|---|---|---|---|
| `error` | `object` | yes | - | Machine-readable API error details. |
| `error.kind` | `string` | yes | example: "runtime_unavailable" | Stable machine-readable error kind. |
| `error.message` | `string` | yes | example: "OpenVPN runtime is unavailable" | Safe human-readable error message. |
| `error.request_id` | `string` | yes | format: uuid; example: "33ba813e-fc63-4af8-b338-7f7486f82202" | UUID used to correlate the request with server logs. |

### 20. `PUT /api/v1/config/desired`

Replace desired configuration with CAS. Send the previous desired digest as a quoted If-Match value and the complete normalized configuration as the body.

#### Request parameters

##### Header parameters

| Field | Location | Type | Required | Format / example / constraints | Purpose |
|---|---|---|---|---|---|
| `Authorization` | `header` | `string` | yes | Bearer ovpn_v1.<uuid>.<secret> | API authentication credential. Put the service-generated API key after Bearer. |
| `Content-Type` | `header` | `string` | yes | application/json | Declares that the request body uses JSON. |
| `If-Match` | `header` | `string` | yes | format: ^"[0-9a-f]{64}"$; example: "\"be7ed82796b39a77ce949201a4d9483cf09e56648f14b8b9b192bb83d2c4d042\"" | Exactly one quoted lowercase SHA-256 digest from the previous desired response ETag. |

##### JSON body fields

| Field | Location | Type | Required | Format / example / constraints | Purpose |
|---|---|---|---|---|---|
| `version` | `body` | `integer` | yes | constant: 1 | Configuration schema version. Must be 1. |
| `server` | `body` | `object` | yes | - | OpenVPN listener and transport settings. |
| `server.endpoint` | `body` | `string` | yes | example: "vpn.example.com" | Public hostname or IP address placed in generated client profiles. |
| `server.protocol` | `body` | `"udp" \| "tcp"` | yes | values: "udp" \| "tcp" | OpenVPN transport protocol. |
| `server.family` | `body` | `"auto" \| "ipv4" \| "ipv6"` | yes | values: "auto" \| "ipv4" \| "ipv6" | Address family used for the server transport. |
| `server.port` | `body` | `integer` | yes | minimum: 1; maximum: 65535; example: 1194 | OpenVPN listener port. |
| `server.client_to_client` | `body` | `boolean` | yes | example: true | Whether connected VPN clients may communicate directly. |
| `ipv4` | `body` | `object` | yes | - | Server IPv4 network, allocation, NAT, DNS, and route settings. |
| `ipv4.network` | `body` | `string` | yes | format: ipv4-cidr; example: "10.42.0.0/24" | VPN IPv4 network in CIDR notation. |
| `ipv4.dynamic_pool_size` | `body` | `integer` | yes | minimum: 0; example: 64 | Number of addresses reserved for dynamic allocation. |
| `ipv4.nat_enabled` | `body` | `boolean` | yes | example: false | Whether outbound traffic from the VPN network is masqueraded. |
| `ipv4.nat_interface` | `body` | `string` | yes | example: "auto" | Outbound interface for NAT, or auto for automatic detection. |
| `ipv4.redirect_gateway` | `body` | `boolean` | yes | example: false | Whether generated profiles redirect the default IPv4 route through VPN. |
| `ipv4.dns` | `body` | `string[]` | yes | example: ["1.1.1.1"] | IPv4 DNS servers pushed to clients. |
| `ipv4.routes` | `body` | `string[]` | yes | example: ["10.20.0.0/16"] | Additional IPv4 CIDR routes pushed to clients. |
| `logging` | `body` | `object` | yes | - | Runtime log rotation settings. |
| `logging.max_bytes` | `body` | `integer` | yes | minimum: 1; example: 10485760 | Maximum active log file size before rotation. |
| `logging.backups` | `body` | `integer` | yes | minimum: 0; example: 5 | Number of rotated log files retained. |

#### Request example

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

#### Responses

##### `200 OK`

Desired digest and normalized configuration.

Content type: `application/json`

Response example:

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

Response fields:

| Field | Type | Required | Format / example / constraints | Purpose |
|---|---|---|---|---|
| `digest` | `string` | yes | format: ^[0-9a-f]{64}$; example: "be7ed82796b39a77ce949201a4d9483cf09e56648f14b8b9b192bb83d2c4d042" | Lowercase SHA-256 digest identifying an exact desired configuration. |
| `config` | `object` | yes | - | Complete normalized OpenVPN server configuration. |
| `config.version` | `integer` | yes | constant: 1 | Configuration schema version. Must be 1. |
| `config.server` | `object` | yes | - | OpenVPN listener and transport settings. |
| `config.server.endpoint` | `string` | yes | example: "vpn.example.com" | Public hostname or IP address placed in generated client profiles. |
| `config.server.protocol` | `"udp" \| "tcp"` | yes | values: "udp" \| "tcp" | OpenVPN transport protocol. |
| `config.server.family` | `"auto" \| "ipv4" \| "ipv6"` | yes | values: "auto" \| "ipv4" \| "ipv6" | Address family used for the server transport. |
| `config.server.port` | `integer` | yes | minimum: 1; maximum: 65535; example: 1194 | OpenVPN listener port. |
| `config.server.client_to_client` | `boolean` | yes | example: true | Whether connected VPN clients may communicate directly. |
| `config.ipv4` | `object` | yes | - | Server IPv4 network, allocation, NAT, DNS, and route settings. |
| `config.ipv4.network` | `string` | yes | format: ipv4-cidr; example: "10.42.0.0/24" | VPN IPv4 network in CIDR notation. |
| `config.ipv4.dynamic_pool_size` | `integer` | yes | minimum: 0; example: 64 | Number of addresses reserved for dynamic allocation. |
| `config.ipv4.nat_enabled` | `boolean` | yes | example: false | Whether outbound traffic from the VPN network is masqueraded. |
| `config.ipv4.nat_interface` | `string` | yes | example: "auto" | Outbound interface for NAT, or auto for automatic detection. |
| `config.ipv4.redirect_gateway` | `boolean` | yes | example: false | Whether generated profiles redirect the default IPv4 route through VPN. |
| `config.ipv4.dns` | `string[]` | yes | example: [] | IPv4 DNS servers pushed to clients. |
| `config.ipv4.routes` | `string[]` | yes | example: [] | Additional IPv4 CIDR routes pushed to clients. |
| `config.logging` | `object` | yes | - | Runtime log rotation settings. |
| `config.logging.max_bytes` | `integer` | yes | minimum: 1; example: 10485760 | Maximum active log file size before rotation. |
| `config.logging.backups` | `integer` | yes | minimum: 0; example: 5 | Number of rotated log files retained. |

##### `400 Bad Request`

Malformed path, query, header, media type, body, or JSON.

Content type: `application/json`

Response example:

```json
{
  "error": {
    "kind": "invalid_json",
    "message": "request body is invalid",
    "request_id": "33ba813e-fc63-4af8-b338-7f7486f82202"
  }
}
```

Response fields:

| Field | Type | Required | Format / example / constraints | Purpose |
|---|---|---|---|---|
| `error` | `object` | yes | - | Machine-readable API error details. |
| `error.kind` | `string` | yes | example: "invalid_json" | Stable machine-readable error kind. |
| `error.message` | `string` | yes | example: "request body is invalid" | Safe human-readable error message. |
| `error.request_id` | `string` | yes | format: uuid; example: "33ba813e-fc63-4af8-b338-7f7486f82202" | UUID used to correlate the request with server logs. |

##### `401 Unauthorized`

Bearer API key is missing, malformed, unknown, or deleted.

Content type: `application/json`

Response example:

```json
{
  "error": {
    "kind": "unauthenticated",
    "message": "API key is missing or invalid",
    "request_id": "33ba813e-fc63-4af8-b338-7f7486f82202"
  }
}
```

Response fields:

| Field | Type | Required | Format / example / constraints | Purpose |
|---|---|---|---|---|
| `error` | `object` | yes | - | Machine-readable API error details. |
| `error.kind` | `string` | yes | example: "unauthenticated" | Stable machine-readable error kind. |
| `error.message` | `string` | yes | example: "API key is missing or invalid" | Safe human-readable error message. |
| `error.request_id` | `string` | yes | format: uuid; example: "33ba813e-fc63-4af8-b338-7f7486f82202" | UUID used to correlate the request with server logs. |

##### `405 Method Not Allowed`

HTTP method is not accepted; inspect the Allow header.

Content type: `application/json`

Response example:

```json
{
  "error": {
    "kind": "method_not_allowed",
    "message": "method is not allowed",
    "request_id": "33ba813e-fc63-4af8-b338-7f7486f82202"
  }
}
```

Response fields:

| Field | Type | Required | Format / example / constraints | Purpose |
|---|---|---|---|---|
| `error` | `object` | yes | - | Machine-readable API error details. |
| `error.kind` | `string` | yes | example: "method_not_allowed" | Stable machine-readable error kind. |
| `error.message` | `string` | yes | example: "method is not allowed" | Safe human-readable error message. |
| `error.request_id` | `string` | yes | format: uuid; example: "33ba813e-fc63-4af8-b338-7f7486f82202" | UUID used to correlate the request with server logs. |

##### `409 Conflict`

Digest, revision, state, uniqueness, lock, or recovery conflict.

Content type: `application/json`

Response example:

```json
{
  "error": {
    "kind": "configuration_conflict",
    "message": "configuration state changed or is busy",
    "request_id": "33ba813e-fc63-4af8-b338-7f7486f82202"
  }
}
```

Response fields:

| Field | Type | Required | Format / example / constraints | Purpose |
|---|---|---|---|---|
| `error` | `object` | yes | - | Machine-readable API error details. |
| `error.kind` | `string` | yes | example: "configuration_conflict" | Stable machine-readable error kind. |
| `error.message` | `string` | yes | example: "configuration state changed or is busy" | Safe human-readable error message. |
| `error.request_id` | `string` | yes | format: uuid; example: "33ba813e-fc63-4af8-b338-7f7486f82202" | UUID used to correlate the request with server logs. |

##### `422 Unprocessable Entity`

JSON is valid but violates client, IPv4, or configuration semantics.

Content type: `application/json`

Response example:

```json
{
  "error": {
    "kind": "invalid_client",
    "message": "client request is not valid for the current state",
    "request_id": "33ba813e-fc63-4af8-b338-7f7486f82202"
  }
}
```

Response fields:

| Field | Type | Required | Format / example / constraints | Purpose |
|---|---|---|---|---|
| `error` | `object` | yes | - | Machine-readable API error details. |
| `error.kind` | `string` | yes | example: "invalid_client" | Stable machine-readable error kind. |
| `error.message` | `string` | yes | example: "client request is not valid for the current state" | Safe human-readable error message. |
| `error.request_id` | `string` | yes | format: uuid; example: "33ba813e-fc63-4af8-b338-7f7486f82202" | UUID used to correlate the request with server logs. |

##### `500 Internal Server Error`

Unclassified internal failure with no implementation details exposed.

Content type: `application/json`

Response example:

```json
{
  "error": {
    "kind": "internal_error",
    "message": "request could not be completed",
    "request_id": "33ba813e-fc63-4af8-b338-7f7486f82202"
  }
}
```

Response fields:

| Field | Type | Required | Format / example / constraints | Purpose |
|---|---|---|---|---|
| `error` | `object` | yes | - | Machine-readable API error details. |
| `error.kind` | `string` | yes | example: "internal_error" | Stable machine-readable error kind. |
| `error.message` | `string` | yes | example: "request could not be completed" | Safe human-readable error message. |
| `error.request_id` | `string` | yes | format: uuid; example: "33ba813e-fc63-4af8-b338-7f7486f82202" | UUID used to correlate the request with server logs. |

##### `503 Service Unavailable`

Authentication storage, PKI, OpenVPN, broker, supervisor, or another required dependency is unavailable.

Content type: `application/json`

Response example:

```json
{
  "error": {
    "kind": "runtime_unavailable",
    "message": "OpenVPN runtime is unavailable",
    "request_id": "33ba813e-fc63-4af8-b338-7f7486f82202"
  }
}
```

Response fields:

| Field | Type | Required | Format / example / constraints | Purpose |
|---|---|---|---|---|
| `error` | `object` | yes | - | Machine-readable API error details. |
| `error.kind` | `string` | yes | example: "runtime_unavailable" | Stable machine-readable error kind. |
| `error.message` | `string` | yes | example: "OpenVPN runtime is unavailable" | Safe human-readable error message. |
| `error.request_id` | `string` | yes | format: uuid; example: "33ba813e-fc63-4af8-b338-7f7486f82202" | UUID used to correlate the request with server logs. |

### 21. `GET /api/v1/config/plan`

Plan desired-to-applied changes

#### Request parameters

##### Header parameters

| Field | Location | Type | Required | Format / example / constraints | Purpose |
|---|---|---|---|---|---|
| `Authorization` | `header` | `string` | yes | Bearer ovpn_v1.<uuid>.<secret> | API authentication credential. Put the service-generated API key after Bearer. |

#### Request example

```http
GET /api/v1/config/plan HTTP/1.1
Authorization: Bearer ovpn_v1.<uuid>.<secret>
```

#### Responses

##### `200 OK`

Complete desired-to-applied plan.

Content type: `application/json`

Response example:

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

Response fields:

| Field | Type | Required | Format / example / constraints | Purpose |
|---|---|---|---|---|
| `version` | `integer` | yes | constant: 1 | Response contract schema version. |
| `instance_id` | `string` | yes | format: uuid; example: "bbffeb8c-2d11-4613-874d-b5fcc804a608" | Immutable UUID of the initialized OpenVPN instance. |
| `configuration` | `object` | yes | - | Revision, digest, changes, and impact comparison. |
| `configuration.initial` | `boolean` | yes | example: false | Whether the plan creates the first applied revision. |
| `configuration.current_revision` | `integer` | yes | minimum: 0; example: 12 | Currently applied configuration revision used for comparison. |
| `configuration.target_revision` | `integer` | yes | minimum: 1; example: 13 | Applied revision that would result from this plan. |
| `configuration.current_digest` | `string` | no | format: ^[0-9a-f]{64}$; example: "be7ed82796b39a77ce949201a4d9483cf09e56648f14b8b9b192bb83d2c4d042" | Lowercase SHA-256 digest identifying an exact desired configuration. |
| `configuration.desired_digest` | `string` | yes | format: ^[0-9a-f]{64}$; example: "489b950c0d134b5d7e8f350b2cb4bfa4d6d163d841f840964baf6d74c65cc8ca" | Lowercase SHA-256 digest identifying an exact desired configuration. |
| `configuration.in_sync` | `boolean` | yes | example: false | Whether desired and applied configuration are identical. |
| `configuration.changes` | `object[]` | yes | example: [{"field":"server.endpoint","before":"vpn.example.com","after":"vpn-new.example.com"}] | Field-level configuration changes in this plan. |
| `configuration.changes[].field` | `string` | yes | example: "server.endpoint" | Canonical dotted path of the changed configuration field. |
| `configuration.changes[].before` | `any` | yes | example: "vpn.example.com" | Applied value before the proposed configuration change. |
| `configuration.changes[].after` | `any` | yes | example: "vpn-new.example.com" | Desired value after the proposed configuration change. |
| `configuration.impact` | `object` | yes | - | Runtime and artifact impact of configuration changes. |
| `configuration.impact.restart_required` | `boolean` | yes | example: true | Whether applying the plan requires an OpenVPN restart. |
| `configuration.impact.address_remap` | `boolean` | yes | example: false | Whether client address assignments must be recalculated. |
| `configuration.impact.firewall_reconcile` | `boolean` | yes | example: false | Whether firewall rules must be reconciled. |
| `configuration.impact.profile_redistribution` | `boolean` | yes | example: true | Clients whose generated profiles must be redistributed. |
| `configuration.impact.derived_artifacts` | `string[]` | yes | example: ["server_config","client_profiles"] | Derived artifacts that must be regenerated or removed. |
| `address_changes` | `object[]` | yes | example: [] | Client address changes required by this plan. |
| `address_changes[].client` | `object` | yes | - | Stable client identity used in plan reports. |
| `address_changes[].client.id` | `string` | yes | format: uuid | Stable identifier of this object. |
| `address_changes[].client.name` | `string` | yes | - | Stable human-readable name. |
| `address_changes[].before` | `object` | yes | - | Client IPv4 allocation intent at one point in time. |
| `address_changes[].before.mode` | `string` | yes | - | IPv4 allocation mode. |
| `address_changes[].before.address` | `string \| null` | yes | format: ipv4 | IPv4 address, or null when no address is assigned. |
| `address_changes[].before.state` | `string` | yes | - | Lifecycle state of this address intent. |
| `address_changes[].after` | `object` | yes | - | Client IPv4 allocation intent at one point in time. |
| `address_changes[].after.mode` | `string` | yes | - | IPv4 allocation mode. |
| `address_changes[].after.address` | `string \| null` | yes | format: ipv4 | IPv4 address, or null when no address is assigned. |
| `address_changes[].after.state` | `string` | yes | - | Lifecycle state of this address intent. |
| `artifacts` | `object[]` | yes | example: [] | Derived artifact operations required by this plan. |
| `artifacts[].owner_kind` | `string` | yes | - | Kind of object that owns the artifact. |
| `artifacts[].owner_id` | `string` | yes | - | Stable identifier of the owning object. |
| `artifacts[].kind` | `string` | yes | - | Derived artifact type. |
| `artifacts[].key` | `string` | yes | - | Stable key identifying the derived artifact. |
| `artifacts[].action` | `"regenerate" \| "delete"` | yes | values: "regenerate" \| "delete" | Whether to regenerate or delete the artifact. |
| `profile_redistribution` | `object[]` | yes | example: [] | Clients whose generated profiles must be redistributed. |
| `profile_redistribution[].id` | `string` | yes | format: uuid | Stable identifier of this object. |
| `profile_redistribution[].name` | `string` | yes | - | Stable human-readable name. |
| `firewall` | `object` | yes | - | Before-and-after firewall state and reconciliation requirement. |
| `firewall.reconcile` | `boolean` | yes | example: false | Whether firewall state must be reconciled during apply. |
| `firewall.before` | `object \| null` | yes | - | Firewall state before apply, or null for initial configuration. |
| `firewall.after` | `object \| null` | yes | - | Firewall state required by the desired configuration, or null when absent. |

##### `401 Unauthorized`

Bearer API key is missing, malformed, unknown, or deleted.

Content type: `application/json`

Response example:

```json
{
  "error": {
    "kind": "unauthenticated",
    "message": "API key is missing or invalid",
    "request_id": "33ba813e-fc63-4af8-b338-7f7486f82202"
  }
}
```

Response fields:

| Field | Type | Required | Format / example / constraints | Purpose |
|---|---|---|---|---|
| `error` | `object` | yes | - | Machine-readable API error details. |
| `error.kind` | `string` | yes | example: "unauthenticated" | Stable machine-readable error kind. |
| `error.message` | `string` | yes | example: "API key is missing or invalid" | Safe human-readable error message. |
| `error.request_id` | `string` | yes | format: uuid; example: "33ba813e-fc63-4af8-b338-7f7486f82202" | UUID used to correlate the request with server logs. |

##### `405 Method Not Allowed`

HTTP method is not accepted; inspect the Allow header.

Content type: `application/json`

Response example:

```json
{
  "error": {
    "kind": "method_not_allowed",
    "message": "method is not allowed",
    "request_id": "33ba813e-fc63-4af8-b338-7f7486f82202"
  }
}
```

Response fields:

| Field | Type | Required | Format / example / constraints | Purpose |
|---|---|---|---|---|
| `error` | `object` | yes | - | Machine-readable API error details. |
| `error.kind` | `string` | yes | example: "method_not_allowed" | Stable machine-readable error kind. |
| `error.message` | `string` | yes | example: "method is not allowed" | Safe human-readable error message. |
| `error.request_id` | `string` | yes | format: uuid; example: "33ba813e-fc63-4af8-b338-7f7486f82202" | UUID used to correlate the request with server logs. |

##### `409 Conflict`

Digest, revision, state, uniqueness, lock, or recovery conflict.

Content type: `application/json`

Response example:

```json
{
  "error": {
    "kind": "configuration_conflict",
    "message": "configuration state changed or is busy",
    "request_id": "33ba813e-fc63-4af8-b338-7f7486f82202"
  }
}
```

Response fields:

| Field | Type | Required | Format / example / constraints | Purpose |
|---|---|---|---|---|
| `error` | `object` | yes | - | Machine-readable API error details. |
| `error.kind` | `string` | yes | example: "configuration_conflict" | Stable machine-readable error kind. |
| `error.message` | `string` | yes | example: "configuration state changed or is busy" | Safe human-readable error message. |
| `error.request_id` | `string` | yes | format: uuid; example: "33ba813e-fc63-4af8-b338-7f7486f82202" | UUID used to correlate the request with server logs. |

##### `422 Unprocessable Entity`

JSON is valid but violates client, IPv4, or configuration semantics.

Content type: `application/json`

Response example:

```json
{
  "error": {
    "kind": "invalid_client",
    "message": "client request is not valid for the current state",
    "request_id": "33ba813e-fc63-4af8-b338-7f7486f82202"
  }
}
```

Response fields:

| Field | Type | Required | Format / example / constraints | Purpose |
|---|---|---|---|---|
| `error` | `object` | yes | - | Machine-readable API error details. |
| `error.kind` | `string` | yes | example: "invalid_client" | Stable machine-readable error kind. |
| `error.message` | `string` | yes | example: "client request is not valid for the current state" | Safe human-readable error message. |
| `error.request_id` | `string` | yes | format: uuid; example: "33ba813e-fc63-4af8-b338-7f7486f82202" | UUID used to correlate the request with server logs. |

##### `500 Internal Server Error`

Unclassified internal failure with no implementation details exposed.

Content type: `application/json`

Response example:

```json
{
  "error": {
    "kind": "internal_error",
    "message": "request could not be completed",
    "request_id": "33ba813e-fc63-4af8-b338-7f7486f82202"
  }
}
```

Response fields:

| Field | Type | Required | Format / example / constraints | Purpose |
|---|---|---|---|---|
| `error` | `object` | yes | - | Machine-readable API error details. |
| `error.kind` | `string` | yes | example: "internal_error" | Stable machine-readable error kind. |
| `error.message` | `string` | yes | example: "request could not be completed" | Safe human-readable error message. |
| `error.request_id` | `string` | yes | format: uuid; example: "33ba813e-fc63-4af8-b338-7f7486f82202" | UUID used to correlate the request with server logs. |

##### `503 Service Unavailable`

Authentication storage, PKI, OpenVPN, broker, supervisor, or another required dependency is unavailable.

Content type: `application/json`

Response example:

```json
{
  "error": {
    "kind": "runtime_unavailable",
    "message": "OpenVPN runtime is unavailable",
    "request_id": "33ba813e-fc63-4af8-b338-7f7486f82202"
  }
}
```

Response fields:

| Field | Type | Required | Format / example / constraints | Purpose |
|---|---|---|---|---|
| `error` | `object` | yes | - | Machine-readable API error details. |
| `error.kind` | `string` | yes | example: "runtime_unavailable" | Stable machine-readable error kind. |
| `error.message` | `string` | yes | example: "OpenVPN runtime is unavailable" | Safe human-readable error message. |
| `error.request_id` | `string` | yes | format: uuid; example: "33ba813e-fc63-4af8-b338-7f7486f82202" | UUID used to correlate the request with server logs. |

### 22. `POST /api/v1/config/apply`

Apply the current desired configuration online. Use desired_digest from GET/PUT desired and current_revision from GET plan. The API remains available while OpenVPN and the broker restart.

#### Request parameters

##### Header parameters

| Field | Location | Type | Required | Format / example / constraints | Purpose |
|---|---|---|---|---|---|
| `Authorization` | `header` | `string` | yes | Bearer ovpn_v1.<uuid>.<secret> | API authentication credential. Put the service-generated API key after Bearer. |
| `Content-Type` | `header` | `string` | yes | application/json | Declares that the request body uses JSON. |

##### JSON body fields

| Field | Location | Type | Required | Format / example / constraints | Purpose |
|---|---|---|---|---|---|
| `desired_digest` | `body` | `string` | yes | format: ^[0-9a-f]{64}$; example: "489b950c0d134b5d7e8f350b2cb4bfa4d6d163d841f840964baf6d74c65cc8ca" | Lowercase SHA-256 digest identifying an exact desired configuration. |
| `current_revision` | `body` | `integer` | yes | minimum: 1; example: 12 | Applied revision returned by the configuration plan. |
| `force` | `body` | `boolean` | no | default: false; example: false | Bypasses only confirmed health-preflight warnings; never bypasses schema, CAS, lock, or recovery checks. |

#### Request example

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

#### Responses

##### `200 OK`

Apply transaction and runtime activation outcome.

Content type: `application/json`

Response example:

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

Response fields:

| Field | Type | Required | Format / example / constraints | Purpose |
|---|---|---|---|---|
| `version` | `integer` | yes | constant: 1 | Response contract schema version. |
| `applied` | `boolean` | yes | example: true | Whether this request committed a new applied revision. |
| `operation_id` | `string` | no | format: uuid; example: "5b781e08-3d44-41fb-81db-42edeedc61e1" | UUID of the committed journal operation. |
| `activation` | `object` | yes | - | Runtime activation actions performed after configuration commit. |
| `activation.restart_required` | `boolean` | yes | example: true | Whether applying the plan requires an OpenVPN restart. |
| `activation.runtime_restarted` | `boolean` | yes | example: true | Whether the managed OpenVPN runtime was restarted. |
| `activation.profile_redistribution` | `object[]` | yes | example: [] | Clients whose generated profiles must be redistributed. |
| `activation.profile_redistribution[].id` | `string` | yes | format: uuid | Stable identifier of this object. |
| `activation.profile_redistribution[].name` | `string` | yes | - | Stable human-readable name. |
| `plan` | `object` | yes | - | Complete plan for applying the desired configuration. |
| `plan.version` | `integer` | yes | constant: 1 | Response contract schema version. |
| `plan.instance_id` | `string` | yes | format: uuid; example: "bbffeb8c-2d11-4613-874d-b5fcc804a608" | Immutable UUID of the initialized OpenVPN instance. |
| `plan.configuration` | `object` | yes | - | Revision, digest, changes, and impact comparison. |
| `plan.configuration.initial` | `boolean` | yes | example: false | Whether the plan creates the first applied revision. |
| `plan.configuration.current_revision` | `integer` | yes | minimum: 0; example: 12 | Currently applied configuration revision used for comparison. |
| `plan.configuration.target_revision` | `integer` | yes | minimum: 1; example: 13 | Applied revision that would result from this plan. |
| `plan.configuration.current_digest` | `string` | no | format: ^[0-9a-f]{64}$; example: "be7ed82796b39a77ce949201a4d9483cf09e56648f14b8b9b192bb83d2c4d042" | Lowercase SHA-256 digest identifying an exact desired configuration. |
| `plan.configuration.desired_digest` | `string` | yes | format: ^[0-9a-f]{64}$; example: "489b950c0d134b5d7e8f350b2cb4bfa4d6d163d841f840964baf6d74c65cc8ca" | Lowercase SHA-256 digest identifying an exact desired configuration. |
| `plan.configuration.in_sync` | `boolean` | yes | example: false | Whether desired and applied configuration are identical. |
| `plan.configuration.changes` | `object[]` | yes | example: [] | Field-level configuration changes in this plan. |
| `plan.configuration.changes[].field` | `string` | yes | - | Canonical dotted path of the changed configuration field. |
| `plan.configuration.changes[].before` | `any` | yes | - | Applied value before the proposed configuration change. |
| `plan.configuration.changes[].after` | `any` | yes | - | Desired value after the proposed configuration change. |
| `plan.configuration.impact` | `object` | yes | - | Runtime and artifact impact of configuration changes. |
| `plan.configuration.impact.restart_required` | `boolean` | yes | example: true | Whether applying the plan requires an OpenVPN restart. |
| `plan.configuration.impact.address_remap` | `boolean` | yes | example: false | Whether client address assignments must be recalculated. |
| `plan.configuration.impact.firewall_reconcile` | `boolean` | yes | example: false | Whether firewall rules must be reconciled. |
| `plan.configuration.impact.profile_redistribution` | `boolean` | yes | example: false | Clients whose generated profiles must be redistributed. |
| `plan.configuration.impact.derived_artifacts` | `string[]` | yes | example: [] | Derived artifacts that must be regenerated or removed. |
| `plan.address_changes` | `object[]` | yes | example: [] | Client address changes required by this plan. |
| `plan.address_changes[].client` | `object` | yes | - | Stable client identity used in plan reports. |
| `plan.address_changes[].client.id` | `string` | yes | format: uuid | Stable identifier of this object. |
| `plan.address_changes[].client.name` | `string` | yes | - | Stable human-readable name. |
| `plan.address_changes[].before` | `object` | yes | - | Client IPv4 allocation intent at one point in time. |
| `plan.address_changes[].before.mode` | `string` | yes | - | IPv4 allocation mode. |
| `plan.address_changes[].before.address` | `string \| null` | yes | format: ipv4 | IPv4 address, or null when no address is assigned. |
| `plan.address_changes[].before.state` | `string` | yes | - | Lifecycle state of this address intent. |
| `plan.address_changes[].after` | `object` | yes | - | Client IPv4 allocation intent at one point in time. |
| `plan.address_changes[].after.mode` | `string` | yes | - | IPv4 allocation mode. |
| `plan.address_changes[].after.address` | `string \| null` | yes | format: ipv4 | IPv4 address, or null when no address is assigned. |
| `plan.address_changes[].after.state` | `string` | yes | - | Lifecycle state of this address intent. |
| `plan.artifacts` | `object[]` | yes | example: [] | Derived artifact operations required by this plan. |
| `plan.artifacts[].owner_kind` | `string` | yes | - | Kind of object that owns the artifact. |
| `plan.artifacts[].owner_id` | `string` | yes | - | Stable identifier of the owning object. |
| `plan.artifacts[].kind` | `string` | yes | - | Derived artifact type. |
| `plan.artifacts[].key` | `string` | yes | - | Stable key identifying the derived artifact. |
| `plan.artifacts[].action` | `"regenerate" \| "delete"` | yes | values: "regenerate" \| "delete" | Whether to regenerate or delete the artifact. |
| `plan.profile_redistribution` | `object[]` | yes | example: [] | Clients whose generated profiles must be redistributed. |
| `plan.profile_redistribution[].id` | `string` | yes | format: uuid | Stable identifier of this object. |
| `plan.profile_redistribution[].name` | `string` | yes | - | Stable human-readable name. |
| `plan.firewall` | `object` | yes | - | Before-and-after firewall state and reconciliation requirement. |
| `plan.firewall.reconcile` | `boolean` | yes | example: false | Whether firewall state must be reconciled during apply. |
| `plan.firewall.before` | `object \| null` | yes | - | Firewall state before apply, or null for initial configuration. |
| `plan.firewall.after` | `object \| null` | yes | - | Firewall state required by the desired configuration, or null when absent. |

##### `400 Bad Request`

Malformed path, query, header, media type, body, or JSON.

Content type: `application/json`

Response example:

```json
{
  "error": {
    "kind": "invalid_json",
    "message": "request body is invalid",
    "request_id": "33ba813e-fc63-4af8-b338-7f7486f82202"
  }
}
```

Response fields:

| Field | Type | Required | Format / example / constraints | Purpose |
|---|---|---|---|---|
| `error` | `object` | yes | - | Machine-readable API error details. |
| `error.kind` | `string` | yes | example: "invalid_json" | Stable machine-readable error kind. |
| `error.message` | `string` | yes | example: "request body is invalid" | Safe human-readable error message. |
| `error.request_id` | `string` | yes | format: uuid; example: "33ba813e-fc63-4af8-b338-7f7486f82202" | UUID used to correlate the request with server logs. |

##### `401 Unauthorized`

Bearer API key is missing, malformed, unknown, or deleted.

Content type: `application/json`

Response example:

```json
{
  "error": {
    "kind": "unauthenticated",
    "message": "API key is missing or invalid",
    "request_id": "33ba813e-fc63-4af8-b338-7f7486f82202"
  }
}
```

Response fields:

| Field | Type | Required | Format / example / constraints | Purpose |
|---|---|---|---|---|
| `error` | `object` | yes | - | Machine-readable API error details. |
| `error.kind` | `string` | yes | example: "unauthenticated" | Stable machine-readable error kind. |
| `error.message` | `string` | yes | example: "API key is missing or invalid" | Safe human-readable error message. |
| `error.request_id` | `string` | yes | format: uuid; example: "33ba813e-fc63-4af8-b338-7f7486f82202" | UUID used to correlate the request with server logs. |

##### `405 Method Not Allowed`

HTTP method is not accepted; inspect the Allow header.

Content type: `application/json`

Response example:

```json
{
  "error": {
    "kind": "method_not_allowed",
    "message": "method is not allowed",
    "request_id": "33ba813e-fc63-4af8-b338-7f7486f82202"
  }
}
```

Response fields:

| Field | Type | Required | Format / example / constraints | Purpose |
|---|---|---|---|---|
| `error` | `object` | yes | - | Machine-readable API error details. |
| `error.kind` | `string` | yes | example: "method_not_allowed" | Stable machine-readable error kind. |
| `error.message` | `string` | yes | example: "method is not allowed" | Safe human-readable error message. |
| `error.request_id` | `string` | yes | format: uuid; example: "33ba813e-fc63-4af8-b338-7f7486f82202" | UUID used to correlate the request with server logs. |

##### `409 Conflict`

Digest, revision, state, uniqueness, lock, or recovery conflict.

Content type: `application/json`

Response example:

```json
{
  "error": {
    "kind": "configuration_conflict",
    "message": "configuration state changed or is busy",
    "request_id": "33ba813e-fc63-4af8-b338-7f7486f82202"
  }
}
```

Response fields:

| Field | Type | Required | Format / example / constraints | Purpose |
|---|---|---|---|---|
| `error` | `object` | yes | - | Machine-readable API error details. |
| `error.kind` | `string` | yes | example: "configuration_conflict" | Stable machine-readable error kind. |
| `error.message` | `string` | yes | example: "configuration state changed or is busy" | Safe human-readable error message. |
| `error.request_id` | `string` | yes | format: uuid; example: "33ba813e-fc63-4af8-b338-7f7486f82202" | UUID used to correlate the request with server logs. |

##### `422 Unprocessable Entity`

JSON is valid but violates client, IPv4, or configuration semantics.

Content type: `application/json`

Response example:

```json
{
  "error": {
    "kind": "invalid_client",
    "message": "client request is not valid for the current state",
    "request_id": "33ba813e-fc63-4af8-b338-7f7486f82202"
  }
}
```

Response fields:

| Field | Type | Required | Format / example / constraints | Purpose |
|---|---|---|---|---|
| `error` | `object` | yes | - | Machine-readable API error details. |
| `error.kind` | `string` | yes | example: "invalid_client" | Stable machine-readable error kind. |
| `error.message` | `string` | yes | example: "client request is not valid for the current state" | Safe human-readable error message. |
| `error.request_id` | `string` | yes | format: uuid; example: "33ba813e-fc63-4af8-b338-7f7486f82202" | UUID used to correlate the request with server logs. |

##### `500 Internal Server Error`

Unclassified internal failure with no implementation details exposed.

Content type: `application/json`

Response example:

```json
{
  "error": {
    "kind": "internal_error",
    "message": "request could not be completed",
    "request_id": "33ba813e-fc63-4af8-b338-7f7486f82202"
  }
}
```

Response fields:

| Field | Type | Required | Format / example / constraints | Purpose |
|---|---|---|---|---|
| `error` | `object` | yes | - | Machine-readable API error details. |
| `error.kind` | `string` | yes | example: "internal_error" | Stable machine-readable error kind. |
| `error.message` | `string` | yes | example: "request could not be completed" | Safe human-readable error message. |
| `error.request_id` | `string` | yes | format: uuid; example: "33ba813e-fc63-4af8-b338-7f7486f82202" | UUID used to correlate the request with server logs. |

##### `503 Service Unavailable`

Authentication storage, PKI, OpenVPN, broker, supervisor, or another required dependency is unavailable.

Content type: `application/json`

Response example:

```json
{
  "error": {
    "kind": "runtime_unavailable",
    "message": "OpenVPN runtime is unavailable",
    "request_id": "33ba813e-fc63-4af8-b338-7f7486f82202"
  }
}
```

Response fields:

| Field | Type | Required | Format / example / constraints | Purpose |
|---|---|---|---|---|
| `error` | `object` | yes | - | Machine-readable API error details. |
| `error.kind` | `string` | yes | example: "runtime_unavailable" | Stable machine-readable error kind. |
| `error.message` | `string` | yes | example: "OpenVPN runtime is unavailable" | Safe human-readable error message. |
| `error.request_id` | `string` | yes | format: uuid; example: "33ba813e-fc63-4af8-b338-7f7486f82202" | UUID used to correlate the request with server logs. |

## Configuration workflow

Configuration uses an optimistic three-step workflow:

```text
PUT desired -> GET plan -> POST apply
```

First read desired configuration. The response body contains `digest` and `config`; the same digest is returned as a quoted `ETag`:

```bash
curl -H "Authorization: Bearer $OVPN_API_KEY" \
  https://vpn-admin.example.com/api/v1/config/desired
```

To replace desired YAML, send the complete normalized `config` object as the request body and the previously observed digest in `If-Match`:

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

The API validates the same domain rules as YAML, writes canonical mode-`0600` YAML through an atomic rename, and returns the new digest and `ETag`. A stale digest returns `409` without replacing the file.

Review `/api/v1/config/plan`, then apply exactly that desired digest and the plan's `configuration.current_revision`:

```json
{
  "desired_digest": "<64-character-lowercase-digest>",
  "current_revision": 12,
  "force": false
}
```

Apply performs the normal health preflight, asks the supervisor to pause OpenVPN and the broker, rechecks digest and revision, runs the existing journaled transaction under exclusive locks, and restarts the managed runtime. The API process remains available during this operation. `force: true` bypasses only a reviewed health-preflight false negative; it does not bypass schema, path, lock, concurrency, or recovery checks.

Digest changes, applied revision changes, held mutation locks, and stale plan state return `409`. An unavailable supervisor returns `503`.

## Errors

Errors use one stable envelope and never include internal paths, SQL, request bodies, credentials, or Go causes:

```json
{
  "error": {
    "kind": "client_not_found",
    "message": "client was not found",
    "request_id": "33ba813e-fc63-4af8-b338-7f7486f82202"
  }
}
```

| Status | Meaning |
|---|---|
| `400` | Invalid path, query, JSON, media type, body, or concurrency header. |
| `401` | Missing, malformed, unknown, or deleted API key. |
| `404` | Resource or client not found. |
| `405` | Method not allowed; inspect the `Allow` header. |
| `409` | Digest, revision, state, constraint, lock, or recovery conflict. |
| `422` | Valid JSON with invalid client, IPv4, or configuration semantics. |
| `503` | Authentication storage, OpenVPN, broker, supervisor, or another required dependency is unavailable. |
| `500` | Unclassified internal failure. |

Use `request_id` to correlate a failed call without recording its Authorization header or body.

## Security boundary

- Keep `OVPN_API_LISTEN` empty when remote management is not required.
- Bind to loopback or a private management network and terminate TLS at a trusted reverse proxy.
- Restrict reverse-proxy and host firewall access independently of API authentication.
- Use a distinct API key per integration and delete unused keys promptly.
- Never expose API keys or profiles to logs, browser storage, telemetry, source control, or chat systems.
- Back up SQLite, YAML, PKI, artifacts, and keys as one coordinated operational unit; the REST API is not a backup interface.
- API v1 has no users, sessions, RBAC, key scopes, key expiry, asynchronous jobs, or built-in TLS.
