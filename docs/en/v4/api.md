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
      OVPN_API_CORS_ORIGINS: https://vpn-admin.example.com
```

With the project's host-networked Compose service, `127.0.0.1:11940` exposes the API only on the host loopback interface. An empty `OVPN_API_LISTEN` disables the process. Do not add these variables to `openvpn-maintenance`.

`OVPN_API_CORS_ORIGINS` is optional. It is a comma-separated list of exact browser origins. Wildcards, paths, credentials in origins, and empty list entries are rejected. CORS is unnecessary for a same-origin frontend or non-browser client.

A minimal Caddy boundary is:

```caddyfile
vpn-admin.example.com {
    reverse_proxy 127.0.0.1:11940
}
```

The reverse proxy must provide HTTPS, preserve the `Authorization` header, and impose appropriate network access controls. The project does not recommend binding the API directly to a public `0.0.0.0` address.

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

Each of the following 22 operations is a complete, self-contained contract. Every operation separately lists Header, Path, Query, and JSON body parameters with location, type, required status, format, example, constraints, and purpose. Every response status separately lists its content type, example, and fields. All requests except `/healthz` require a Bearer API key.

### 1. `GET /healthz`

Check API process liveness. Unauthenticated liveness check. It does not report OpenVPN or storage health.

#### Request parameters

No request parameters or request body.

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
| `status` | `string` | yes | constant: "ok" | - |

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
| `error` | `object` | yes | - | - |
| `error.kind` | `string` | yes | example: "method_not_allowed" | - |
| `error.message` | `string` | yes | example: "method is not allowed" | - |
| `error.request_id` | `string` | yes | format: uuid; example: "33ba813e-fc63-4af8-b338-7f7486f82202" | - |

### 2. `GET /api/v1/version`

Read build and compatibility metadata

#### Request parameters

##### Header parameters

| Field | Location | Type | Required | Format / example / constraints | Purpose |
|---|---|---|---|---|---|
| `Authorization` | `header` | `string` | yes | Bearer ovpn_v1.<uuid>.<secret> | API authentication credential. Put the API key generated by the service after Bearer. |

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
| `version` | `string` | yes | example: "4.0.2" | - |
| `data_schema` | `integer` | yes | example: 4 | - |
| `commit` | `string` | yes | example: "dd9b5213f5002a7e69f160e3fd2615e0b3d8d224" | - |
| `build_date` | `string` | yes | example: "2026-08-19T09:45:40Z" | - |
| `go_version` | `string` | yes | example: "go1.26.5" | - |
| `dependencies` | `object` | yes | - | - |
| `dependencies.sqlite` | `string` | yes | example: "github.com/mattn/go-sqlite3 v1.14.48" | - |
| `dependencies.yaml` | `string` | yes | example: "go.yaml.in/yaml/v3 v3.0.4" | - |
| `compatibility` | `object` | yes | - | - |
| `compatibility.contract_version` | `integer` | yes | example: 1 | - |
| `compatibility.adapter` | `string` | yes | example: "openvpn-2.7" | - |
| `compatibility.template_family` | `string` | yes | example: "openvpn-2.7" | - |
| `compatibility.supported_openvpn_versions` | `string[]` | yes | example: ["2.7.6"] | - |

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
| `error` | `object` | yes | - | - |
| `error.kind` | `string` | yes | example: "unauthenticated" | - |
| `error.message` | `string` | yes | example: "API key is missing or invalid" | - |
| `error.request_id` | `string` | yes | format: uuid; example: "33ba813e-fc63-4af8-b338-7f7486f82202" | - |

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
| `error` | `object` | yes | - | - |
| `error.kind` | `string` | yes | example: "method_not_allowed" | - |
| `error.message` | `string` | yes | example: "method is not allowed" | - |
| `error.request_id` | `string` | yes | format: uuid; example: "33ba813e-fc63-4af8-b338-7f7486f82202" | - |

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
| `error` | `object` | yes | - | - |
| `error.kind` | `string` | yes | example: "runtime_unavailable" | - |
| `error.message` | `string` | yes | example: "OpenVPN runtime is unavailable" | - |
| `error.request_id` | `string` | yes | format: uuid; example: "33ba813e-fc63-4af8-b338-7f7486f82202" | - |

### 3. `GET /api/v1/state`

Read instance health summary

#### Request parameters

##### Header parameters

| Field | Location | Type | Required | Format / example / constraints | Purpose |
|---|---|---|---|---|---|
| `Authorization` | `header` | `string` | yes | Bearer ovpn_v1.<uuid>.<secret> | API authentication credential. Put the API key generated by the service after Bearer. |

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
| `version` | `integer` | yes | constant: 1 | - |
| `state` | `string` | yes | values: "EMPTY", "HEALTHY", "DEGRADED_REPAIRABLE", "DEGRADED_RECOVERABLE", "DEGRADED_REISSUABLE", "CRITICAL", "UNRECOVERABLE" | - |
| `data_schema` | `integer` | yes | example: 4 | - |
| `instance_id` | `string` | no | format: uuid; example: "bbffeb8c-2d11-4613-874d-b5fcc804a608" | - |
| `revision` | `integer` | no | minimum: 1; example: 12 | - |
| `scanned_at` | `string` | yes | format: date-time; example: "2026-08-19T09:55:07Z" | - |
| `issue_count` | `integer` | yes | minimum: 0 | - |
| `pending_operation_count` | `integer` | yes | minimum: 0 | - |
| `issues` | `object[]` | no | - | - |
| `issues[].id` | `string` | no | example: "string" | - |
| `issues[].severity` | `string` | no | values: "repairable", "recoverable", "reissuable", "critical", "unrecoverable" | - |
| `issues[].action` | `string` | no | example: "string" | - |
| `issues[].target` | `string` | no | example: "string" | - |
| `issues[].owner_id` | `string` | no | example: "string" | - |
| `issues[].artifact_kind` | `string` | no | example: "string" | - |
| `issues[].detail` | `string` | no | example: "string" | - |

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
| `error` | `object` | yes | - | - |
| `error.kind` | `string` | yes | example: "unauthenticated" | - |
| `error.message` | `string` | yes | example: "API key is missing or invalid" | - |
| `error.request_id` | `string` | yes | format: uuid; example: "33ba813e-fc63-4af8-b338-7f7486f82202" | - |

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
| `error` | `object` | yes | - | - |
| `error.kind` | `string` | yes | example: "method_not_allowed" | - |
| `error.message` | `string` | yes | example: "method is not allowed" | - |
| `error.request_id` | `string` | yes | format: uuid; example: "33ba813e-fc63-4af8-b338-7f7486f82202" | - |

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
| `error` | `object` | yes | - | - |
| `error.kind` | `string` | yes | example: "internal_error" | - |
| `error.message` | `string` | yes | example: "request could not be completed" | - |
| `error.request_id` | `string` | yes | format: uuid; example: "33ba813e-fc63-4af8-b338-7f7486f82202" | - |

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
| `error` | `object` | yes | - | - |
| `error.kind` | `string` | yes | example: "runtime_unavailable" | - |
| `error.message` | `string` | yes | example: "OpenVPN runtime is unavailable" | - |
| `error.request_id` | `string` | yes | format: uuid; example: "33ba813e-fc63-4af8-b338-7f7486f82202" | - |

### 4. `GET /api/v1/state/doctor`

Read detailed instance diagnostics. Returns the state summary plus an issues array when diagnostic issues exist.

#### Request parameters

##### Header parameters

| Field | Location | Type | Required | Format / example / constraints | Purpose |
|---|---|---|---|---|---|
| `Authorization` | `header` | `string` | yes | Bearer ovpn_v1.<uuid>.<secret> | API authentication credential. Put the API key generated by the service after Bearer. |

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
| `version` | `integer` | yes | constant: 1 | - |
| `state` | `string` | yes | values: "EMPTY", "HEALTHY", "DEGRADED_REPAIRABLE", "DEGRADED_RECOVERABLE", "DEGRADED_REISSUABLE", "CRITICAL", "UNRECOVERABLE" | - |
| `data_schema` | `integer` | yes | example: 4 | - |
| `instance_id` | `string` | no | format: uuid; example: "bbffeb8c-2d11-4613-874d-b5fcc804a608" | - |
| `revision` | `integer` | no | minimum: 1; example: 12 | - |
| `scanned_at` | `string` | yes | format: date-time; example: "2026-08-19T09:55:07Z" | - |
| `issue_count` | `integer` | yes | minimum: 0; example: 1 | - |
| `pending_operation_count` | `integer` | yes | minimum: 0 | - |
| `issues` | `object[]` | no | example: [{"id":"DECLARATIVE_CONFIG_UNAVAILABLE","severity":"repairable","action":"export-config","target":"/etc/ovpn-conf/config.yaml","detail":"declarative configuration is unavailable"}] | - |
| `issues[].id` | `string` | no | example: "DECLARATIVE_CONFIG_UNAVAILABLE" | - |
| `issues[].severity` | `string` | no | values: "repairable", "recoverable", "reissuable", "critical", "unrecoverable" | - |
| `issues[].action` | `string` | no | example: "export-config" | - |
| `issues[].target` | `string` | no | example: "/etc/ovpn-conf/config.yaml" | - |
| `issues[].owner_id` | `string` | no | example: "string" | - |
| `issues[].artifact_kind` | `string` | no | example: "string" | - |
| `issues[].detail` | `string` | no | example: "declarative configuration is unavailable" | - |

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
| `error` | `object` | yes | - | - |
| `error.kind` | `string` | yes | example: "unauthenticated" | - |
| `error.message` | `string` | yes | example: "API key is missing or invalid" | - |
| `error.request_id` | `string` | yes | format: uuid; example: "33ba813e-fc63-4af8-b338-7f7486f82202" | - |

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
| `error` | `object` | yes | - | - |
| `error.kind` | `string` | yes | example: "method_not_allowed" | - |
| `error.message` | `string` | yes | example: "method is not allowed" | - |
| `error.request_id` | `string` | yes | format: uuid; example: "33ba813e-fc63-4af8-b338-7f7486f82202" | - |

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
| `error` | `object` | yes | - | - |
| `error.kind` | `string` | yes | example: "internal_error" | - |
| `error.message` | `string` | yes | example: "request could not be completed" | - |
| `error.request_id` | `string` | yes | format: uuid; example: "33ba813e-fc63-4af8-b338-7f7486f82202" | - |

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
| `error` | `object` | yes | - | - |
| `error.kind` | `string` | yes | example: "runtime_unavailable" | - |
| `error.message` | `string` | yes | example: "OpenVPN runtime is unavailable" | - |
| `error.request_id` | `string` | yes | format: uuid; example: "33ba813e-fc63-4af8-b338-7f7486f82202" | - |

### 5. `GET /api/v1/clients`

List active, revoked, and deleted clients

#### Request parameters

##### Header parameters

| Field | Location | Type | Required | Format / example / constraints | Purpose |
|---|---|---|---|---|---|
| `Authorization` | `header` | `string` | yes | Bearer ovpn_v1.<uuid>.<secret> | API authentication credential. Put the API key generated by the service after Bearer. |

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
| `version` | `integer` | yes | constant: 1 | - |
| `clients` | `object[]` | yes | example: [{"id":"c0d4f871-6ea6-42b3-9e7b-f00cc1ec354e","name":"alice-laptop","status":"active","ipv4":{"mode":"static","address":"10.42.0.30","state":"configured"},"connection":"connected"}] | - |
| `clients[].id` | `string` | yes | format: uuid; example: "c0d4f871-6ea6-42b3-9e7b-f00cc1ec354e" | - |
| `clients[].name` | `string` | yes | pattern: `^[A-Za-z0-9][A-Za-z0-9_.-]*$`; example: "alice-laptop" | - |
| `clients[].status` | `string` | yes | values: "active", "revoked", "deleted" | - |
| `clients[].ipv4` | `object` | yes | - | - |
| `clients[].ipv4.mode` | `string` | yes | values: "none", "static", "dynamic" | - |
| `clients[].ipv4.address` | `string \| null` | yes | format: ipv4; example: "10.42.0.30" | - |
| `clients[].ipv4.state` | `string` | yes | values: "configured", "retained", "unavailable" | - |
| `clients[].connection` | `string` | no | example: "connected" | - |

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
| `error` | `object` | yes | - | - |
| `error.kind` | `string` | yes | example: "unauthenticated" | - |
| `error.message` | `string` | yes | example: "API key is missing or invalid" | - |
| `error.request_id` | `string` | yes | format: uuid; example: "33ba813e-fc63-4af8-b338-7f7486f82202" | - |

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
| `error` | `object` | yes | - | - |
| `error.kind` | `string` | yes | example: "method_not_allowed" | - |
| `error.message` | `string` | yes | example: "method is not allowed" | - |
| `error.request_id` | `string` | yes | format: uuid; example: "33ba813e-fc63-4af8-b338-7f7486f82202" | - |

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
| `error` | `object` | yes | - | - |
| `error.kind` | `string` | yes | example: "internal_error" | - |
| `error.message` | `string` | yes | example: "request could not be completed" | - |
| `error.request_id` | `string` | yes | format: uuid; example: "33ba813e-fc63-4af8-b338-7f7486f82202" | - |

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
| `error` | `object` | yes | - | - |
| `error.kind` | `string` | yes | example: "runtime_unavailable" | - |
| `error.message` | `string` | yes | example: "OpenVPN runtime is unavailable" | - |
| `error.request_id` | `string` | yes | format: uuid; example: "33ba813e-fc63-4af8-b338-7f7486f82202" | - |

### 6. `POST /api/v1/clients`

Create client credentials and profile

#### Request parameters

##### Header parameters

| Field | Location | Type | Required | Format / example / constraints | Purpose |
|---|---|---|---|---|---|
| `Authorization` | `header` | `string` | yes | Bearer ovpn_v1.<uuid>.<secret> | API authentication credential. Put the API key generated by the service after Bearer. |
| `Content-Type` | `header` | `string` | yes | application/json | Declares that the request body uses JSON. |

##### JSON body fields

| Field | Location | Type | Required | Format / example / constraints | Purpose |
|---|---|---|---|---|---|
| `name` | `body` | `string` | yes | pattern: `^[A-Za-z0-9][A-Za-z0-9_.-]*$`; example: "alice-laptop" | Stable client name. Starts with an alphanumeric character and may contain letters, digits, underscores, dots, and hyphens. |
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
| `version` | `integer` | yes | constant: 1 | - |
| `operation_id` | `string` | yes | format: uuid; example: "7a21e1f8-1ce1-4694-977f-b275eadb8651" | - |
| `client` | `object` | yes | - | - |
| `client.id` | `string` | yes | format: uuid; example: "c0d4f871-6ea6-42b3-9e7b-f00cc1ec354e" | - |
| `client.name` | `string` | yes | pattern: `^[A-Za-z0-9][A-Za-z0-9_.-]*$`; example: "alice-laptop" | - |
| `client.status` | `string` | yes | values: "active", "revoked", "deleted" | - |
| `client.ipv4` | `object` | yes | - | - |
| `client.ipv4.mode` | `string` | yes | values: "none", "static", "dynamic" | - |
| `client.ipv4.address` | `string \| null` | yes | format: ipv4; example: "10.42.0.2" | - |
| `client.ipv4.state` | `string` | yes | values: "configured", "retained", "unavailable" | - |
| `client.connection` | `string` | no | example: "string" | - |
| `kick_required` | `boolean` | yes | example: false | - |
| `profile_redistribution_required` | `boolean` | yes | example: true | - |
| `runtime` | `object` | no | - | - |
| `runtime.client_id` | `string` | no | format: uuid; example: "c0d4f871-6ea6-42b3-9e7b-f00cc1ec354e" | - |
| `runtime.status` | `string` | no | values: "ok", "unavailable" | - |
| `runtime.result` | `object` | no | - | - |
| `runtime.result.version` | `integer` | no | constant: 1 | - |
| `runtime.result.client_id` | `string` | no | format: uuid; example: "c0d4f871-6ea6-42b3-9e7b-f00cc1ec354e" | - |
| `runtime.result.client_name` | `string` | no | example: "string" | - |
| `runtime.result.was_connected` | `boolean` | no | example: false | - |
| `runtime.result.disconnected` | `boolean` | no | example: false | - |
| `runtime.result.connections` | `integer` | no | minimum: 0 | - |

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
| `error` | `object` | yes | - | - |
| `error.kind` | `string` | yes | example: "invalid_json" | - |
| `error.message` | `string` | yes | example: "request body is invalid" | - |
| `error.request_id` | `string` | yes | format: uuid; example: "33ba813e-fc63-4af8-b338-7f7486f82202" | - |

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
| `error` | `object` | yes | - | - |
| `error.kind` | `string` | yes | example: "unauthenticated" | - |
| `error.message` | `string` | yes | example: "API key is missing or invalid" | - |
| `error.request_id` | `string` | yes | format: uuid; example: "33ba813e-fc63-4af8-b338-7f7486f82202" | - |

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
| `error` | `object` | yes | - | - |
| `error.kind` | `string` | yes | example: "configuration_conflict" | - |
| `error.message` | `string` | yes | example: "configuration state changed or is busy" | - |
| `error.request_id` | `string` | yes | format: uuid; example: "33ba813e-fc63-4af8-b338-7f7486f82202" | - |

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
| `error` | `object` | yes | - | - |
| `error.kind` | `string` | yes | example: "invalid_client" | - |
| `error.message` | `string` | yes | example: "client request is not valid for the current state" | - |
| `error.request_id` | `string` | yes | format: uuid; example: "33ba813e-fc63-4af8-b338-7f7486f82202" | - |

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
| `error` | `object` | yes | - | - |
| `error.kind` | `string` | yes | example: "internal_error" | - |
| `error.message` | `string` | yes | example: "request could not be completed" | - |
| `error.request_id` | `string` | yes | format: uuid; example: "33ba813e-fc63-4af8-b338-7f7486f82202" | - |

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
| `error` | `object` | yes | - | - |
| `error.kind` | `string` | yes | example: "runtime_unavailable" | - |
| `error.message` | `string` | yes | example: "OpenVPN runtime is unavailable" | - |
| `error.request_id` | `string` | yes | format: uuid; example: "33ba813e-fc63-4af8-b338-7f7486f82202" | - |

### 7. `GET /api/v1/clients/{client_id}`

Read one client

#### Request parameters

##### Header parameters

| Field | Location | Type | Required | Format / example / constraints | Purpose |
|---|---|---|---|---|---|
| `Authorization` | `header` | `string` | yes | Bearer ovpn_v1.<uuid>.<secret> | API authentication credential. Put the API key generated by the service after Bearer. |

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
| `id` | `string` | yes | format: uuid; example: "c0d4f871-6ea6-42b3-9e7b-f00cc1ec354e" | - |
| `name` | `string` | yes | pattern: `^[A-Za-z0-9][A-Za-z0-9_.-]*$`; example: "alice-laptop" | - |
| `status` | `string` | yes | values: "active", "revoked", "deleted" | - |
| `ipv4` | `object` | yes | - | - |
| `ipv4.mode` | `string` | yes | values: "none", "static", "dynamic" | - |
| `ipv4.address` | `string \| null` | yes | format: ipv4; example: "10.42.0.30" | - |
| `ipv4.state` | `string` | yes | values: "configured", "retained", "unavailable" | - |
| `connection` | `string` | no | example: "string" | - |

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
| `error` | `object` | yes | - | - |
| `error.kind` | `string` | yes | example: "invalid_json" | - |
| `error.message` | `string` | yes | example: "request body is invalid" | - |
| `error.request_id` | `string` | yes | format: uuid; example: "33ba813e-fc63-4af8-b338-7f7486f82202" | - |

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
| `error` | `object` | yes | - | - |
| `error.kind` | `string` | yes | example: "unauthenticated" | - |
| `error.message` | `string` | yes | example: "API key is missing or invalid" | - |
| `error.request_id` | `string` | yes | format: uuid; example: "33ba813e-fc63-4af8-b338-7f7486f82202" | - |

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
| `error` | `object` | yes | - | - |
| `error.kind` | `string` | yes | example: "client_not_found" | - |
| `error.message` | `string` | yes | example: "client was not found" | - |
| `error.request_id` | `string` | yes | format: uuid; example: "33ba813e-fc63-4af8-b338-7f7486f82202" | - |

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
| `error` | `object` | yes | - | - |
| `error.kind` | `string` | yes | example: "method_not_allowed" | - |
| `error.message` | `string` | yes | example: "method is not allowed" | - |
| `error.request_id` | `string` | yes | format: uuid; example: "33ba813e-fc63-4af8-b338-7f7486f82202" | - |

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
| `error` | `object` | yes | - | - |
| `error.kind` | `string` | yes | example: "internal_error" | - |
| `error.message` | `string` | yes | example: "request could not be completed" | - |
| `error.request_id` | `string` | yes | format: uuid; example: "33ba813e-fc63-4af8-b338-7f7486f82202" | - |

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
| `error` | `object` | yes | - | - |
| `error.kind` | `string` | yes | example: "runtime_unavailable" | - |
| `error.message` | `string` | yes | example: "OpenVPN runtime is unavailable" | - |
| `error.request_id` | `string` | yes | format: uuid; example: "33ba813e-fc63-4af8-b338-7f7486f82202" | - |

### 8. `PATCH /api/v1/clients/{client_id}`

Rename a client

#### Request parameters

##### Header parameters

| Field | Location | Type | Required | Format / example / constraints | Purpose |
|---|---|---|---|---|---|
| `Authorization` | `header` | `string` | yes | Bearer ovpn_v1.<uuid>.<secret> | API authentication credential. Put the API key generated by the service after Bearer. |
| `Content-Type` | `header` | `string` | yes | application/json | Declares that the request body uses JSON. |

##### Path parameters

| Field | Location | Type | Required | Format / example / constraints | Purpose |
|---|---|---|---|---|---|
| `client_id` | `path` | `string` | yes | format: uuid; example: "c0d4f871-6ea6-42b3-9e7b-f00cc1ec354e" | Complete canonical immutable client UUID; names and UUID prefixes are rejected. |

##### JSON body fields

| Field | Location | Type | Required | Format / example / constraints | Purpose |
|---|---|---|---|---|---|
| `name` | `body` | `string` | yes | pattern: `^[A-Za-z0-9][A-Za-z0-9_.-]*$`; example: "alice-notebook" | New stable client name. Uses the same format as client creation. |

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
| `version` | `integer` | yes | constant: 1 | - |
| `operation_id` | `string` | yes | format: uuid; example: "47260b5b-47dd-4f9b-814f-4803668c5934" | - |
| `client` | `object` | yes | - | - |
| `client.id` | `string` | yes | format: uuid; example: "c0d4f871-6ea6-42b3-9e7b-f00cc1ec354e" | - |
| `client.name` | `string` | yes | pattern: `^[A-Za-z0-9][A-Za-z0-9_.-]*$`; example: "alice-notebook" | - |
| `client.status` | `string` | yes | values: "active", "revoked", "deleted" | - |
| `client.ipv4` | `object` | yes | - | - |
| `client.ipv4.mode` | `string` | yes | values: "none", "static", "dynamic" | - |
| `client.ipv4.address` | `string \| null` | yes | format: ipv4; example: "10.42.0.30" | - |
| `client.ipv4.state` | `string` | yes | values: "configured", "retained", "unavailable" | - |
| `client.connection` | `string` | no | example: "string" | - |
| `kick_required` | `boolean` | yes | example: false | - |
| `profile_redistribution_required` | `boolean` | yes | example: true | - |
| `runtime` | `object` | no | - | - |
| `runtime.client_id` | `string` | no | format: uuid; example: "c0d4f871-6ea6-42b3-9e7b-f00cc1ec354e" | - |
| `runtime.status` | `string` | no | values: "ok", "unavailable" | - |
| `runtime.result` | `object` | no | - | - |
| `runtime.result.version` | `integer` | no | constant: 1 | - |
| `runtime.result.client_id` | `string` | no | format: uuid; example: "c0d4f871-6ea6-42b3-9e7b-f00cc1ec354e" | - |
| `runtime.result.client_name` | `string` | no | example: "string" | - |
| `runtime.result.was_connected` | `boolean` | no | example: false | - |
| `runtime.result.disconnected` | `boolean` | no | example: false | - |
| `runtime.result.connections` | `integer` | no | minimum: 0 | - |

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
| `error` | `object` | yes | - | - |
| `error.kind` | `string` | yes | example: "invalid_json" | - |
| `error.message` | `string` | yes | example: "request body is invalid" | - |
| `error.request_id` | `string` | yes | format: uuid; example: "33ba813e-fc63-4af8-b338-7f7486f82202" | - |

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
| `error` | `object` | yes | - | - |
| `error.kind` | `string` | yes | example: "unauthenticated" | - |
| `error.message` | `string` | yes | example: "API key is missing or invalid" | - |
| `error.request_id` | `string` | yes | format: uuid; example: "33ba813e-fc63-4af8-b338-7f7486f82202" | - |

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
| `error` | `object` | yes | - | - |
| `error.kind` | `string` | yes | example: "client_not_found" | - |
| `error.message` | `string` | yes | example: "client was not found" | - |
| `error.request_id` | `string` | yes | format: uuid; example: "33ba813e-fc63-4af8-b338-7f7486f82202" | - |

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
| `error` | `object` | yes | - | - |
| `error.kind` | `string` | yes | example: "method_not_allowed" | - |
| `error.message` | `string` | yes | example: "method is not allowed" | - |
| `error.request_id` | `string` | yes | format: uuid; example: "33ba813e-fc63-4af8-b338-7f7486f82202" | - |

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
| `error` | `object` | yes | - | - |
| `error.kind` | `string` | yes | example: "configuration_conflict" | - |
| `error.message` | `string` | yes | example: "configuration state changed or is busy" | - |
| `error.request_id` | `string` | yes | format: uuid; example: "33ba813e-fc63-4af8-b338-7f7486f82202" | - |

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
| `error` | `object` | yes | - | - |
| `error.kind` | `string` | yes | example: "invalid_client" | - |
| `error.message` | `string` | yes | example: "client request is not valid for the current state" | - |
| `error.request_id` | `string` | yes | format: uuid; example: "33ba813e-fc63-4af8-b338-7f7486f82202" | - |

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
| `error` | `object` | yes | - | - |
| `error.kind` | `string` | yes | example: "internal_error" | - |
| `error.message` | `string` | yes | example: "request could not be completed" | - |
| `error.request_id` | `string` | yes | format: uuid; example: "33ba813e-fc63-4af8-b338-7f7486f82202" | - |

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
| `error` | `object` | yes | - | - |
| `error.kind` | `string` | yes | example: "runtime_unavailable" | - |
| `error.message` | `string` | yes | example: "OpenVPN runtime is unavailable" | - |
| `error.request_id` | `string` | yes | format: uuid; example: "33ba813e-fc63-4af8-b338-7f7486f82202" | - |

### 9. `DELETE /api/v1/clients/{client_id}`

Delete local client credentials. Requires an empty body. The immutable client UUID tombstone remains in authoritative state.

#### Request parameters

##### Header parameters

| Field | Location | Type | Required | Format / example / constraints | Purpose |
|---|---|---|---|---|---|
| `Authorization` | `header` | `string` | yes | Bearer ovpn_v1.<uuid>.<secret> | API authentication credential. Put the API key generated by the service after Bearer. |

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
| `version` | `integer` | yes | constant: 1 | - |
| `operation_id` | `string` | yes | format: uuid; example: "47260b5b-47dd-4f9b-814f-4803668c5934" | - |
| `client` | `object` | yes | - | - |
| `client.id` | `string` | yes | format: uuid; example: "c0d4f871-6ea6-42b3-9e7b-f00cc1ec354e" | - |
| `client.name` | `string` | yes | pattern: `^[A-Za-z0-9][A-Za-z0-9_.-]*$`; example: "alice-notebook" | - |
| `client.status` | `string` | yes | values: "active", "revoked", "deleted" | - |
| `client.ipv4` | `object` | yes | - | - |
| `client.ipv4.mode` | `string` | yes | values: "none", "static", "dynamic" | - |
| `client.ipv4.address` | `string \| null` | yes | format: ipv4; example: "10.42.0.30" | - |
| `client.ipv4.state` | `string` | yes | values: "configured", "retained", "unavailable" | - |
| `client.connection` | `string` | no | example: "string" | - |
| `kick_required` | `boolean` | yes | example: false | - |
| `profile_redistribution_required` | `boolean` | yes | example: false | - |
| `runtime` | `object` | no | - | - |
| `runtime.client_id` | `string` | no | format: uuid; example: "c0d4f871-6ea6-42b3-9e7b-f00cc1ec354e" | - |
| `runtime.status` | `string` | no | values: "ok", "unavailable" | - |
| `runtime.result` | `object` | no | - | - |
| `runtime.result.version` | `integer` | no | constant: 1 | - |
| `runtime.result.client_id` | `string` | no | format: uuid; example: "c0d4f871-6ea6-42b3-9e7b-f00cc1ec354e" | - |
| `runtime.result.client_name` | `string` | no | example: "string" | - |
| `runtime.result.was_connected` | `boolean` | no | example: false | - |
| `runtime.result.disconnected` | `boolean` | no | example: false | - |
| `runtime.result.connections` | `integer` | no | minimum: 0 | - |

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
| `error` | `object` | yes | - | - |
| `error.kind` | `string` | yes | example: "invalid_json" | - |
| `error.message` | `string` | yes | example: "request body is invalid" | - |
| `error.request_id` | `string` | yes | format: uuid; example: "33ba813e-fc63-4af8-b338-7f7486f82202" | - |

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
| `error` | `object` | yes | - | - |
| `error.kind` | `string` | yes | example: "unauthenticated" | - |
| `error.message` | `string` | yes | example: "API key is missing or invalid" | - |
| `error.request_id` | `string` | yes | format: uuid; example: "33ba813e-fc63-4af8-b338-7f7486f82202" | - |

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
| `error` | `object` | yes | - | - |
| `error.kind` | `string` | yes | example: "client_not_found" | - |
| `error.message` | `string` | yes | example: "client was not found" | - |
| `error.request_id` | `string` | yes | format: uuid; example: "33ba813e-fc63-4af8-b338-7f7486f82202" | - |

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
| `error` | `object` | yes | - | - |
| `error.kind` | `string` | yes | example: "method_not_allowed" | - |
| `error.message` | `string` | yes | example: "method is not allowed" | - |
| `error.request_id` | `string` | yes | format: uuid; example: "33ba813e-fc63-4af8-b338-7f7486f82202" | - |

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
| `error` | `object` | yes | - | - |
| `error.kind` | `string` | yes | example: "configuration_conflict" | - |
| `error.message` | `string` | yes | example: "configuration state changed or is busy" | - |
| `error.request_id` | `string` | yes | format: uuid; example: "33ba813e-fc63-4af8-b338-7f7486f82202" | - |

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
| `error` | `object` | yes | - | - |
| `error.kind` | `string` | yes | example: "invalid_client" | - |
| `error.message` | `string` | yes | example: "client request is not valid for the current state" | - |
| `error.request_id` | `string` | yes | format: uuid; example: "33ba813e-fc63-4af8-b338-7f7486f82202" | - |

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
| `error` | `object` | yes | - | - |
| `error.kind` | `string` | yes | example: "internal_error" | - |
| `error.message` | `string` | yes | example: "request could not be completed" | - |
| `error.request_id` | `string` | yes | format: uuid; example: "33ba813e-fc63-4af8-b338-7f7486f82202" | - |

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
| `error` | `object` | yes | - | - |
| `error.kind` | `string` | yes | example: "runtime_unavailable" | - |
| `error.message` | `string` | yes | example: "OpenVPN runtime is unavailable" | - |
| `error.request_id` | `string` | yes | format: uuid; example: "33ba813e-fc63-4af8-b338-7f7486f82202" | - |

### 10. `GET /api/v1/clients/{client_id}/profile`

Download an OpenVPN client profile. The response contains a private key and must be handled as a credential.

#### Request parameters

##### Header parameters

| Field | Location | Type | Required | Format / example / constraints | Purpose |
|---|---|---|---|---|---|
| `Authorization` | `header` | `string` | yes | Bearer ovpn_v1.<uuid>.<secret> | API authentication credential. Put the API key generated by the service after Bearer. |

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
| `$body` | `string` | yes | format: binary; example: "string" | - |

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
| `error` | `object` | yes | - | - |
| `error.kind` | `string` | yes | example: "invalid_json" | - |
| `error.message` | `string` | yes | example: "request body is invalid" | - |
| `error.request_id` | `string` | yes | format: uuid; example: "33ba813e-fc63-4af8-b338-7f7486f82202" | - |

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
| `error` | `object` | yes | - | - |
| `error.kind` | `string` | yes | example: "unauthenticated" | - |
| `error.message` | `string` | yes | example: "API key is missing or invalid" | - |
| `error.request_id` | `string` | yes | format: uuid; example: "33ba813e-fc63-4af8-b338-7f7486f82202" | - |

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
| `error` | `object` | yes | - | - |
| `error.kind` | `string` | yes | example: "client_not_found" | - |
| `error.message` | `string` | yes | example: "client was not found" | - |
| `error.request_id` | `string` | yes | format: uuid; example: "33ba813e-fc63-4af8-b338-7f7486f82202" | - |

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
| `error` | `object` | yes | - | - |
| `error.kind` | `string` | yes | example: "method_not_allowed" | - |
| `error.message` | `string` | yes | example: "method is not allowed" | - |
| `error.request_id` | `string` | yes | format: uuid; example: "33ba813e-fc63-4af8-b338-7f7486f82202" | - |

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
| `error` | `object` | yes | - | - |
| `error.kind` | `string` | yes | example: "configuration_conflict" | - |
| `error.message` | `string` | yes | example: "configuration state changed or is busy" | - |
| `error.request_id` | `string` | yes | format: uuid; example: "33ba813e-fc63-4af8-b338-7f7486f82202" | - |

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
| `error` | `object` | yes | - | - |
| `error.kind` | `string` | yes | example: "invalid_client" | - |
| `error.message` | `string` | yes | example: "client request is not valid for the current state" | - |
| `error.request_id` | `string` | yes | format: uuid; example: "33ba813e-fc63-4af8-b338-7f7486f82202" | - |

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
| `error` | `object` | yes | - | - |
| `error.kind` | `string` | yes | example: "internal_error" | - |
| `error.message` | `string` | yes | example: "request could not be completed" | - |
| `error.request_id` | `string` | yes | format: uuid; example: "33ba813e-fc63-4af8-b338-7f7486f82202" | - |

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
| `error` | `object` | yes | - | - |
| `error.kind` | `string` | yes | example: "runtime_unavailable" | - |
| `error.message` | `string` | yes | example: "OpenVPN runtime is unavailable" | - |
| `error.request_id` | `string` | yes | format: uuid; example: "33ba813e-fc63-4af8-b338-7f7486f82202" | - |

### 11. `POST /api/v1/clients/{client_id}/revoke`

Revoke client credentials

#### Request parameters

##### Header parameters

| Field | Location | Type | Required | Format / example / constraints | Purpose |
|---|---|---|---|---|---|
| `Authorization` | `header` | `string` | yes | Bearer ovpn_v1.<uuid>.<secret> | API authentication credential. Put the API key generated by the service after Bearer. |
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
| `version` | `integer` | yes | constant: 1 | - |
| `operation_id` | `string` | yes | format: uuid; example: "47260b5b-47dd-4f9b-814f-4803668c5934" | - |
| `client` | `object` | yes | - | - |
| `client.id` | `string` | yes | format: uuid; example: "c0d4f871-6ea6-42b3-9e7b-f00cc1ec354e" | - |
| `client.name` | `string` | yes | pattern: `^[A-Za-z0-9][A-Za-z0-9_.-]*$`; example: "alice-laptop" | - |
| `client.status` | `string` | yes | values: "active", "revoked", "deleted" | - |
| `client.ipv4` | `object` | yes | - | - |
| `client.ipv4.mode` | `string` | yes | values: "none", "static", "dynamic" | - |
| `client.ipv4.address` | `string \| null` | yes | format: ipv4; example: "10.42.0.30" | - |
| `client.ipv4.state` | `string` | yes | values: "configured", "retained", "unavailable" | - |
| `client.connection` | `string` | no | example: "string" | - |
| `kick_required` | `boolean` | yes | example: true | - |
| `profile_redistribution_required` | `boolean` | yes | example: false | - |
| `runtime` | `object` | no | - | - |
| `runtime.client_id` | `string` | no | format: uuid; example: "c0d4f871-6ea6-42b3-9e7b-f00cc1ec354e" | - |
| `runtime.status` | `string` | no | values: "ok", "unavailable" | - |
| `runtime.result` | `object` | no | - | - |
| `runtime.result.version` | `integer` | no | constant: 1 | - |
| `runtime.result.client_id` | `string` | no | format: uuid; example: "c0d4f871-6ea6-42b3-9e7b-f00cc1ec354e" | - |
| `runtime.result.client_name` | `string` | no | example: "alice-laptop" | - |
| `runtime.result.was_connected` | `boolean` | no | example: true | - |
| `runtime.result.disconnected` | `boolean` | no | example: true | - |
| `runtime.result.connections` | `integer` | no | minimum: 0; example: 1 | - |

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
| `error` | `object` | yes | - | - |
| `error.kind` | `string` | yes | example: "invalid_json" | - |
| `error.message` | `string` | yes | example: "request body is invalid" | - |
| `error.request_id` | `string` | yes | format: uuid; example: "33ba813e-fc63-4af8-b338-7f7486f82202" | - |

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
| `error` | `object` | yes | - | - |
| `error.kind` | `string` | yes | example: "unauthenticated" | - |
| `error.message` | `string` | yes | example: "API key is missing or invalid" | - |
| `error.request_id` | `string` | yes | format: uuid; example: "33ba813e-fc63-4af8-b338-7f7486f82202" | - |

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
| `error` | `object` | yes | - | - |
| `error.kind` | `string` | yes | example: "client_not_found" | - |
| `error.message` | `string` | yes | example: "client was not found" | - |
| `error.request_id` | `string` | yes | format: uuid; example: "33ba813e-fc63-4af8-b338-7f7486f82202" | - |

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
| `error` | `object` | yes | - | - |
| `error.kind` | `string` | yes | example: "method_not_allowed" | - |
| `error.message` | `string` | yes | example: "method is not allowed" | - |
| `error.request_id` | `string` | yes | format: uuid; example: "33ba813e-fc63-4af8-b338-7f7486f82202" | - |

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
| `error` | `object` | yes | - | - |
| `error.kind` | `string` | yes | example: "configuration_conflict" | - |
| `error.message` | `string` | yes | example: "configuration state changed or is busy" | - |
| `error.request_id` | `string` | yes | format: uuid; example: "33ba813e-fc63-4af8-b338-7f7486f82202" | - |

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
| `error` | `object` | yes | - | - |
| `error.kind` | `string` | yes | example: "invalid_client" | - |
| `error.message` | `string` | yes | example: "client request is not valid for the current state" | - |
| `error.request_id` | `string` | yes | format: uuid; example: "33ba813e-fc63-4af8-b338-7f7486f82202" | - |

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
| `error` | `object` | yes | - | - |
| `error.kind` | `string` | yes | example: "internal_error" | - |
| `error.message` | `string` | yes | example: "request could not be completed" | - |
| `error.request_id` | `string` | yes | format: uuid; example: "33ba813e-fc63-4af8-b338-7f7486f82202" | - |

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
| `error` | `object` | yes | - | - |
| `error.kind` | `string` | yes | example: "runtime_unavailable" | - |
| `error.message` | `string` | yes | example: "OpenVPN runtime is unavailable" | - |
| `error.request_id` | `string` | yes | format: uuid; example: "33ba813e-fc63-4af8-b338-7f7486f82202" | - |

### 12. `POST /api/v1/clients/{client_id}/reissue`

Reissue client credentials and profile

#### Request parameters

##### Header parameters

| Field | Location | Type | Required | Format / example / constraints | Purpose |
|---|---|---|---|---|---|
| `Authorization` | `header` | `string` | yes | Bearer ovpn_v1.<uuid>.<secret> | API authentication credential. Put the API key generated by the service after Bearer. |
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
| `version` | `integer` | yes | constant: 1 | - |
| `operation_id` | `string` | yes | format: uuid; example: "47260b5b-47dd-4f9b-814f-4803668c5934" | - |
| `client` | `object` | yes | - | - |
| `client.id` | `string` | yes | format: uuid; example: "c0d4f871-6ea6-42b3-9e7b-f00cc1ec354e" | - |
| `client.name` | `string` | yes | pattern: `^[A-Za-z0-9][A-Za-z0-9_.-]*$`; example: "alice-notebook" | - |
| `client.status` | `string` | yes | values: "active", "revoked", "deleted" | - |
| `client.ipv4` | `object` | yes | - | - |
| `client.ipv4.mode` | `string` | yes | values: "none", "static", "dynamic" | - |
| `client.ipv4.address` | `string \| null` | yes | format: ipv4; example: "10.42.0.30" | - |
| `client.ipv4.state` | `string` | yes | values: "configured", "retained", "unavailable" | - |
| `client.connection` | `string` | no | example: "string" | - |
| `kick_required` | `boolean` | yes | example: true | - |
| `profile_redistribution_required` | `boolean` | yes | example: true | - |
| `runtime` | `object` | no | - | - |
| `runtime.client_id` | `string` | no | format: uuid; example: "c0d4f871-6ea6-42b3-9e7b-f00cc1ec354e" | - |
| `runtime.status` | `string` | no | values: "ok", "unavailable" | - |
| `runtime.result` | `object` | no | - | - |
| `runtime.result.version` | `integer` | no | constant: 1 | - |
| `runtime.result.client_id` | `string` | no | format: uuid; example: "c0d4f871-6ea6-42b3-9e7b-f00cc1ec354e" | - |
| `runtime.result.client_name` | `string` | no | example: "string" | - |
| `runtime.result.was_connected` | `boolean` | no | example: false | - |
| `runtime.result.disconnected` | `boolean` | no | example: false | - |
| `runtime.result.connections` | `integer` | no | minimum: 0 | - |

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
| `error` | `object` | yes | - | - |
| `error.kind` | `string` | yes | example: "invalid_json" | - |
| `error.message` | `string` | yes | example: "request body is invalid" | - |
| `error.request_id` | `string` | yes | format: uuid; example: "33ba813e-fc63-4af8-b338-7f7486f82202" | - |

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
| `error` | `object` | yes | - | - |
| `error.kind` | `string` | yes | example: "unauthenticated" | - |
| `error.message` | `string` | yes | example: "API key is missing or invalid" | - |
| `error.request_id` | `string` | yes | format: uuid; example: "33ba813e-fc63-4af8-b338-7f7486f82202" | - |

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
| `error` | `object` | yes | - | - |
| `error.kind` | `string` | yes | example: "client_not_found" | - |
| `error.message` | `string` | yes | example: "client was not found" | - |
| `error.request_id` | `string` | yes | format: uuid; example: "33ba813e-fc63-4af8-b338-7f7486f82202" | - |

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
| `error` | `object` | yes | - | - |
| `error.kind` | `string` | yes | example: "method_not_allowed" | - |
| `error.message` | `string` | yes | example: "method is not allowed" | - |
| `error.request_id` | `string` | yes | format: uuid; example: "33ba813e-fc63-4af8-b338-7f7486f82202" | - |

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
| `error` | `object` | yes | - | - |
| `error.kind` | `string` | yes | example: "configuration_conflict" | - |
| `error.message` | `string` | yes | example: "configuration state changed or is busy" | - |
| `error.request_id` | `string` | yes | format: uuid; example: "33ba813e-fc63-4af8-b338-7f7486f82202" | - |

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
| `error` | `object` | yes | - | - |
| `error.kind` | `string` | yes | example: "invalid_client" | - |
| `error.message` | `string` | yes | example: "client request is not valid for the current state" | - |
| `error.request_id` | `string` | yes | format: uuid; example: "33ba813e-fc63-4af8-b338-7f7486f82202" | - |

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
| `error` | `object` | yes | - | - |
| `error.kind` | `string` | yes | example: "internal_error" | - |
| `error.message` | `string` | yes | example: "request could not be completed" | - |
| `error.request_id` | `string` | yes | format: uuid; example: "33ba813e-fc63-4af8-b338-7f7486f82202" | - |

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
| `error` | `object` | yes | - | - |
| `error.kind` | `string` | yes | example: "runtime_unavailable" | - |
| `error.message` | `string` | yes | example: "OpenVPN runtime is unavailable" | - |
| `error.request_id` | `string` | yes | format: uuid; example: "33ba813e-fc63-4af8-b338-7f7486f82202" | - |

### 13. `PUT /api/v1/clients/{client_id}/ipv4`

Set client IPv4 intent. Use auto or dynamic without address. Use static with an address from the configured static region.

#### Request parameters

##### Header parameters

| Field | Location | Type | Required | Format / example / constraints | Purpose |
|---|---|---|---|---|---|
| `Authorization` | `header` | `string` | yes | Bearer ovpn_v1.<uuid>.<secret> | API authentication credential. Put the API key generated by the service after Bearer. |
| `Content-Type` | `header` | `string` | yes | application/json | Declares that the request body uses JSON. |

##### Path parameters

| Field | Location | Type | Required | Format / example / constraints | Purpose |
|---|---|---|---|---|---|
| `client_id` | `path` | `string` | yes | format: uuid; example: "c0d4f871-6ea6-42b3-9e7b-f00cc1ec354e" | Complete canonical immutable client UUID; names and UUID prefixes are rejected. |

##### JSON body fields

| Field | Location | Type | Required | Format / example / constraints | Purpose |
|---|---|---|---|---|---|
| `mode` | `body` | `string` | yes | values: "auto", "dynamic", "static" | Address selection mode: automatic allocation, dynamic allocation, or a specific static address. |
| `address` | `body` | `string` | no | format: ipv4; example: "10.42.0.30" | Static IPv4 address. Required only for static mode and forbidden for auto/dynamic. |

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
| `version` | `integer` | yes | constant: 1 | - |
| `operation_id` | `string` | yes | format: uuid; example: "d41dbfce-b40e-4672-8a62-cb401f8c099c" | - |
| `clients` | `object[]` | yes | example: [{"id":"c0d4f871-6ea6-42b3-9e7b-f00cc1ec354e","name":"alice-laptop","status":"active","ipv4":{"mode":"static","address":"10.42.0.30","state":"configured"}}] | - |
| `clients[].id` | `string` | yes | format: uuid; example: "c0d4f871-6ea6-42b3-9e7b-f00cc1ec354e" | - |
| `clients[].name` | `string` | yes | pattern: `^[A-Za-z0-9][A-Za-z0-9_.-]*$`; example: "alice-laptop" | - |
| `clients[].status` | `string` | yes | values: "active", "revoked", "deleted" | - |
| `clients[].ipv4` | `object` | yes | - | - |
| `clients[].ipv4.mode` | `string` | yes | values: "none", "static", "dynamic" | - |
| `clients[].ipv4.address` | `string \| null` | yes | format: ipv4; example: "10.42.0.30" | - |
| `clients[].ipv4.state` | `string` | yes | values: "configured", "retained", "unavailable" | - |
| `clients[].connection` | `string` | no | example: "string" | - |
| `kick_required` | `string[]` | yes | example: ["c0d4f871-6ea6-42b3-9e7b-f00cc1ec354e"] | - |
| `runtime` | `object[]` | yes | example: [{"client_id":"c0d4f871-6ea6-42b3-9e7b-f00cc1ec354e","status":"ok","result":{"version":1,"client_id":"c0d4f871-6ea6-42b3-9e7b-f00cc1ec354e","client_name":"alice-laptop","was_connected":false,"disconnected":false,"connections":0}}] | - |
| `runtime[].client_id` | `string` | no | format: uuid; example: "c0d4f871-6ea6-42b3-9e7b-f00cc1ec354e" | - |
| `runtime[].status` | `string` | yes | values: "ok", "unavailable" | - |
| `runtime[].result` | `object` | no | - | - |
| `runtime[].result.version` | `integer` | no | constant: 1 | - |
| `runtime[].result.client_id` | `string` | no | format: uuid; example: "c0d4f871-6ea6-42b3-9e7b-f00cc1ec354e" | - |
| `runtime[].result.client_name` | `string` | no | example: "alice-laptop" | - |
| `runtime[].result.was_connected` | `boolean` | no | example: false | - |
| `runtime[].result.disconnected` | `boolean` | no | example: false | - |
| `runtime[].result.connections` | `integer` | no | minimum: 0 | - |

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
| `error` | `object` | yes | - | - |
| `error.kind` | `string` | yes | example: "invalid_json" | - |
| `error.message` | `string` | yes | example: "request body is invalid" | - |
| `error.request_id` | `string` | yes | format: uuid; example: "33ba813e-fc63-4af8-b338-7f7486f82202" | - |

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
| `error` | `object` | yes | - | - |
| `error.kind` | `string` | yes | example: "unauthenticated" | - |
| `error.message` | `string` | yes | example: "API key is missing or invalid" | - |
| `error.request_id` | `string` | yes | format: uuid; example: "33ba813e-fc63-4af8-b338-7f7486f82202" | - |

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
| `error` | `object` | yes | - | - |
| `error.kind` | `string` | yes | example: "client_not_found" | - |
| `error.message` | `string` | yes | example: "client was not found" | - |
| `error.request_id` | `string` | yes | format: uuid; example: "33ba813e-fc63-4af8-b338-7f7486f82202" | - |

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
| `error` | `object` | yes | - | - |
| `error.kind` | `string` | yes | example: "method_not_allowed" | - |
| `error.message` | `string` | yes | example: "method is not allowed" | - |
| `error.request_id` | `string` | yes | format: uuid; example: "33ba813e-fc63-4af8-b338-7f7486f82202" | - |

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
| `error` | `object` | yes | - | - |
| `error.kind` | `string` | yes | example: "configuration_conflict" | - |
| `error.message` | `string` | yes | example: "configuration state changed or is busy" | - |
| `error.request_id` | `string` | yes | format: uuid; example: "33ba813e-fc63-4af8-b338-7f7486f82202" | - |

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
| `error` | `object` | yes | - | - |
| `error.kind` | `string` | yes | example: "invalid_client" | - |
| `error.message` | `string` | yes | example: "client request is not valid for the current state" | - |
| `error.request_id` | `string` | yes | format: uuid; example: "33ba813e-fc63-4af8-b338-7f7486f82202" | - |

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
| `error` | `object` | yes | - | - |
| `error.kind` | `string` | yes | example: "internal_error" | - |
| `error.message` | `string` | yes | example: "request could not be completed" | - |
| `error.request_id` | `string` | yes | format: uuid; example: "33ba813e-fc63-4af8-b338-7f7486f82202" | - |

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
| `error` | `object` | yes | - | - |
| `error.kind` | `string` | yes | example: "runtime_unavailable" | - |
| `error.message` | `string` | yes | example: "OpenVPN runtime is unavailable" | - |
| `error.request_id` | `string` | yes | format: uuid; example: "33ba813e-fc63-4af8-b338-7f7486f82202" | - |

### 14. `DELETE /api/v1/clients/{client_id}/ipv4`

Release a revoked client's retained IPv4. Requires an empty body and is valid only for a revoked client with a retained assignment.

#### Request parameters

##### Header parameters

| Field | Location | Type | Required | Format / example / constraints | Purpose |
|---|---|---|---|---|---|
| `Authorization` | `header` | `string` | yes | Bearer ovpn_v1.<uuid>.<secret> | API authentication credential. Put the API key generated by the service after Bearer. |

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
| `version` | `integer` | yes | constant: 1 | - |
| `operation_id` | `string` | yes | format: uuid; example: "d41dbfce-b40e-4672-8a62-cb401f8c099c" | - |
| `clients` | `object[]` | yes | example: [{"id":"c0d4f871-6ea6-42b3-9e7b-f00cc1ec354e","name":"alice-notebook","status":"revoked","ipv4":{"mode":"none","address":null,"state":"unavailable"}}] | - |
| `clients[].id` | `string` | yes | format: uuid; example: "c0d4f871-6ea6-42b3-9e7b-f00cc1ec354e" | - |
| `clients[].name` | `string` | yes | pattern: `^[A-Za-z0-9][A-Za-z0-9_.-]*$`; example: "alice-notebook" | - |
| `clients[].status` | `string` | yes | values: "active", "revoked", "deleted" | - |
| `clients[].ipv4` | `object` | yes | - | - |
| `clients[].ipv4.mode` | `string` | yes | values: "none", "static", "dynamic" | - |
| `clients[].ipv4.address` | `string \| null` | yes | format: ipv4; example: "10.42.0.30" | - |
| `clients[].ipv4.state` | `string` | yes | values: "configured", "retained", "unavailable" | - |
| `clients[].connection` | `string` | no | example: "string" | - |
| `kick_required` | `string[]` | yes | example: [] | - |
| `runtime` | `object[]` | yes | example: [] | - |
| `runtime[].client_id` | `string` | no | format: uuid; example: "c0d4f871-6ea6-42b3-9e7b-f00cc1ec354e" | - |
| `runtime[].status` | `string` | yes | values: "ok", "unavailable" | - |
| `runtime[].result` | `object` | no | - | - |
| `runtime[].result.version` | `integer` | no | constant: 1 | - |
| `runtime[].result.client_id` | `string` | no | format: uuid; example: "c0d4f871-6ea6-42b3-9e7b-f00cc1ec354e" | - |
| `runtime[].result.client_name` | `string` | no | example: "string" | - |
| `runtime[].result.was_connected` | `boolean` | no | example: false | - |
| `runtime[].result.disconnected` | `boolean` | no | example: false | - |
| `runtime[].result.connections` | `integer` | no | minimum: 0 | - |

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
| `error` | `object` | yes | - | - |
| `error.kind` | `string` | yes | example: "invalid_json" | - |
| `error.message` | `string` | yes | example: "request body is invalid" | - |
| `error.request_id` | `string` | yes | format: uuid; example: "33ba813e-fc63-4af8-b338-7f7486f82202" | - |

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
| `error` | `object` | yes | - | - |
| `error.kind` | `string` | yes | example: "unauthenticated" | - |
| `error.message` | `string` | yes | example: "API key is missing or invalid" | - |
| `error.request_id` | `string` | yes | format: uuid; example: "33ba813e-fc63-4af8-b338-7f7486f82202" | - |

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
| `error` | `object` | yes | - | - |
| `error.kind` | `string` | yes | example: "client_not_found" | - |
| `error.message` | `string` | yes | example: "client was not found" | - |
| `error.request_id` | `string` | yes | format: uuid; example: "33ba813e-fc63-4af8-b338-7f7486f82202" | - |

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
| `error` | `object` | yes | - | - |
| `error.kind` | `string` | yes | example: "method_not_allowed" | - |
| `error.message` | `string` | yes | example: "method is not allowed" | - |
| `error.request_id` | `string` | yes | format: uuid; example: "33ba813e-fc63-4af8-b338-7f7486f82202" | - |

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
| `error` | `object` | yes | - | - |
| `error.kind` | `string` | yes | example: "configuration_conflict" | - |
| `error.message` | `string` | yes | example: "configuration state changed or is busy" | - |
| `error.request_id` | `string` | yes | format: uuid; example: "33ba813e-fc63-4af8-b338-7f7486f82202" | - |

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
| `error` | `object` | yes | - | - |
| `error.kind` | `string` | yes | example: "invalid_client" | - |
| `error.message` | `string` | yes | example: "client request is not valid for the current state" | - |
| `error.request_id` | `string` | yes | format: uuid; example: "33ba813e-fc63-4af8-b338-7f7486f82202" | - |

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
| `error` | `object` | yes | - | - |
| `error.kind` | `string` | yes | example: "internal_error" | - |
| `error.message` | `string` | yes | example: "request could not be completed" | - |
| `error.request_id` | `string` | yes | format: uuid; example: "33ba813e-fc63-4af8-b338-7f7486f82202" | - |

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
| `error` | `object` | yes | - | - |
| `error.kind` | `string` | yes | example: "runtime_unavailable" | - |
| `error.message` | `string` | yes | example: "OpenVPN runtime is unavailable" | - |
| `error.request_id` | `string` | yes | format: uuid; example: "33ba813e-fc63-4af8-b338-7f7486f82202" | - |

### 15. `POST /api/v1/clients/{client_id}/disconnect`

Disconnect current client sessions. Requires an empty body. A client with no active session returns 200 with was_connected=false.

#### Request parameters

##### Header parameters

| Field | Location | Type | Required | Format / example / constraints | Purpose |
|---|---|---|---|---|---|
| `Authorization` | `header` | `string` | yes | Bearer ovpn_v1.<uuid>.<secret> | API authentication credential. Put the API key generated by the service after Bearer. |

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
| `version` | `integer` | yes | constant: 1 | - |
| `client_id` | `string` | yes | format: uuid; example: "c0d4f871-6ea6-42b3-9e7b-f00cc1ec354e" | - |
| `client_name` | `string` | yes | example: "alice-laptop" | - |
| `was_connected` | `boolean` | yes | example: false | - |
| `disconnected` | `boolean` | yes | example: false | - |
| `connections` | `integer` | yes | minimum: 0 | - |

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
| `error` | `object` | yes | - | - |
| `error.kind` | `string` | yes | example: "invalid_json" | - |
| `error.message` | `string` | yes | example: "request body is invalid" | - |
| `error.request_id` | `string` | yes | format: uuid; example: "33ba813e-fc63-4af8-b338-7f7486f82202" | - |

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
| `error` | `object` | yes | - | - |
| `error.kind` | `string` | yes | example: "unauthenticated" | - |
| `error.message` | `string` | yes | example: "API key is missing or invalid" | - |
| `error.request_id` | `string` | yes | format: uuid; example: "33ba813e-fc63-4af8-b338-7f7486f82202" | - |

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
| `error` | `object` | yes | - | - |
| `error.kind` | `string` | yes | example: "client_not_found" | - |
| `error.message` | `string` | yes | example: "client was not found" | - |
| `error.request_id` | `string` | yes | format: uuid; example: "33ba813e-fc63-4af8-b338-7f7486f82202" | - |

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
| `error` | `object` | yes | - | - |
| `error.kind` | `string` | yes | example: "method_not_allowed" | - |
| `error.message` | `string` | yes | example: "method is not allowed" | - |
| `error.request_id` | `string` | yes | format: uuid; example: "33ba813e-fc63-4af8-b338-7f7486f82202" | - |

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
| `error` | `object` | yes | - | - |
| `error.kind` | `string` | yes | example: "internal_error" | - |
| `error.message` | `string` | yes | example: "request could not be completed" | - |
| `error.request_id` | `string` | yes | format: uuid; example: "33ba813e-fc63-4af8-b338-7f7486f82202" | - |

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
| `error` | `object` | yes | - | - |
| `error.kind` | `string` | yes | example: "runtime_unavailable" | - |
| `error.message` | `string` | yes | example: "OpenVPN runtime is unavailable" | - |
| `error.request_id` | `string` | yes | format: uuid; example: "33ba813e-fc63-4af8-b338-7f7486f82202" | - |

### 16. `GET /api/v1/runtime`

Read OpenVPN runtime status and sessions

#### Request parameters

##### Header parameters

| Field | Location | Type | Required | Format / example / constraints | Purpose |
|---|---|---|---|---|---|
| `Authorization` | `header` | `string` | yes | Bearer ovpn_v1.<uuid>.<secret> | API authentication credential. Put the API key generated by the service after Bearer. |

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
| `version` | `integer` | yes | constant: 1 | - |
| `daemon` | `string` | yes | example: "running" | - |
| `management` | `string` | yes | example: "connected" | - |
| `client_count` | `integer` | yes | minimum: 0; example: 1 | - |
| `clients` | `object[]` | yes | example: [{"client_id":"c0d4f871-6ea6-42b3-9e7b-f00cc1ec354e","client_name":"alice-laptop","remote_address":"203.0.113.10:53210","virtual_address":"10.42.0.30"}] | - |
| `clients[].client_id` | `string` | yes | format: uuid; example: "c0d4f871-6ea6-42b3-9e7b-f00cc1ec354e" | - |
| `clients[].client_name` | `string` | no | example: "alice-laptop" | - |
| `clients[].remote_address` | `string` | no | example: "203.0.113.10:53210" | - |
| `clients[].virtual_address` | `string` | no | format: ipv4; example: "10.42.0.30" | - |

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
| `error` | `object` | yes | - | - |
| `error.kind` | `string` | yes | example: "unauthenticated" | - |
| `error.message` | `string` | yes | example: "API key is missing or invalid" | - |
| `error.request_id` | `string` | yes | format: uuid; example: "33ba813e-fc63-4af8-b338-7f7486f82202" | - |

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
| `error` | `object` | yes | - | - |
| `error.kind` | `string` | yes | example: "method_not_allowed" | - |
| `error.message` | `string` | yes | example: "method is not allowed" | - |
| `error.request_id` | `string` | yes | format: uuid; example: "33ba813e-fc63-4af8-b338-7f7486f82202" | - |

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
| `error` | `object` | yes | - | - |
| `error.kind` | `string` | yes | example: "internal_error" | - |
| `error.message` | `string` | yes | example: "request could not be completed" | - |
| `error.request_id` | `string` | yes | format: uuid; example: "33ba813e-fc63-4af8-b338-7f7486f82202" | - |

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
| `error` | `object` | yes | - | - |
| `error.kind` | `string` | yes | example: "runtime_unavailable" | - |
| `error.message` | `string` | yes | example: "OpenVPN runtime is unavailable" | - |
| `error.request_id` | `string` | yes | format: uuid; example: "33ba813e-fc63-4af8-b338-7f7486f82202" | - |

### 17. `GET /api/v1/runtime/events`

Read recent structured runtime events

#### Request parameters

##### Header parameters

| Field | Location | Type | Required | Format / example / constraints | Purpose |
|---|---|---|---|---|---|
| `Authorization` | `header` | `string` | yes | Bearer ovpn_v1.<uuid>.<secret> | API authentication credential. Put the API key generated by the service after Bearer. |

##### Query parameters

| Field | Location | Type | Required | Format / example / constraints | Purpose |
|---|---|---|---|---|---|
| `lines` | `query` | `integer` | no | minimum: 0; maximum: 1000; default: 100 | Number of most recent events. Defaults to 100. |

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
| `version` | `integer` | yes | constant: 1 | - |
| `events` | `object[]` | yes | example: [{"timestamp":"2026-08-19T09:55:07Z","event":"client-disconnect","operation":"runtime.disconnect","outcome":"success","client_id":"c0d4f871-6ea6-42b3-9e7b-f00cc1ec354e","client_name":"alice-laptop"}] | - |
| `events[].timestamp` | `string` | yes | format: date-time; example: "2026-08-19T09:55:07Z" | - |
| `events[].event` | `string` | yes | example: "client-disconnect" | - |
| `events[].operation` | `string` | yes | example: "runtime.disconnect" | - |
| `events[].outcome` | `string` | yes | example: "success" | - |
| `events[].client_id` | `string \| null` | no | format: uuid; example: "c0d4f871-6ea6-42b3-9e7b-f00cc1ec354e" | - |
| `events[].client_name` | `string \| null` | no | example: "alice-laptop" | - |

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
| `error` | `object` | yes | - | - |
| `error.kind` | `string` | yes | example: "invalid_json" | - |
| `error.message` | `string` | yes | example: "request body is invalid" | - |
| `error.request_id` | `string` | yes | format: uuid; example: "33ba813e-fc63-4af8-b338-7f7486f82202" | - |

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
| `error` | `object` | yes | - | - |
| `error.kind` | `string` | yes | example: "unauthenticated" | - |
| `error.message` | `string` | yes | example: "API key is missing or invalid" | - |
| `error.request_id` | `string` | yes | format: uuid; example: "33ba813e-fc63-4af8-b338-7f7486f82202" | - |

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
| `error` | `object` | yes | - | - |
| `error.kind` | `string` | yes | example: "method_not_allowed" | - |
| `error.message` | `string` | yes | example: "method is not allowed" | - |
| `error.request_id` | `string` | yes | format: uuid; example: "33ba813e-fc63-4af8-b338-7f7486f82202" | - |

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
| `error` | `object` | yes | - | - |
| `error.kind` | `string` | yes | example: "internal_error" | - |
| `error.message` | `string` | yes | example: "request could not be completed" | - |
| `error.request_id` | `string` | yes | format: uuid; example: "33ba813e-fc63-4af8-b338-7f7486f82202" | - |

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
| `error` | `object` | yes | - | - |
| `error.kind` | `string` | yes | example: "runtime_unavailable" | - |
| `error.message` | `string` | yes | example: "OpenVPN runtime is unavailable" | - |
| `error.request_id` | `string` | yes | format: uuid; example: "33ba813e-fc63-4af8-b338-7f7486f82202" | - |

### 18. `GET /api/v1/config/applied`

Read the applied configuration revision

#### Request parameters

##### Header parameters

| Field | Location | Type | Required | Format / example / constraints | Purpose |
|---|---|---|---|---|---|
| `Authorization` | `header` | `string` | yes | Bearer ovpn_v1.<uuid>.<secret> | API authentication credential. Put the API key generated by the service after Bearer. |

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
| `revision` | `integer` | yes | minimum: 1; example: 12 | - |
| `digest` | `string` | yes | pattern: `^[0-9a-f]{64}$`; example: "be7ed82796b39a77ce949201a4d9483cf09e56648f14b8b9b192bb83d2c4d042" | Lowercase SHA-256 digest identifying an exact desired configuration. |
| `config` | `object` | yes | - | - |
| `config.version` | `integer` | yes | constant: 1 | Configuration schema version. Must be 1. |
| `config.server` | `object` | yes | - | OpenVPN listener and transport settings. |
| `config.server.endpoint` | `string` | yes | example: "vpn.example.com" | Public hostname or IP address placed in generated client profiles. |
| `config.server.protocol` | `string` | yes | values: "udp", "tcp" | OpenVPN transport protocol. |
| `config.server.family` | `string` | yes | values: "auto", "ipv4", "ipv6" | Address family used for the server transport. |
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
| `error` | `object` | yes | - | - |
| `error.kind` | `string` | yes | example: "unauthenticated" | - |
| `error.message` | `string` | yes | example: "API key is missing or invalid" | - |
| `error.request_id` | `string` | yes | format: uuid; example: "33ba813e-fc63-4af8-b338-7f7486f82202" | - |

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
| `error` | `object` | yes | - | - |
| `error.kind` | `string` | yes | example: "method_not_allowed" | - |
| `error.message` | `string` | yes | example: "method is not allowed" | - |
| `error.request_id` | `string` | yes | format: uuid; example: "33ba813e-fc63-4af8-b338-7f7486f82202" | - |

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
| `error` | `object` | yes | - | - |
| `error.kind` | `string` | yes | example: "configuration_conflict" | - |
| `error.message` | `string` | yes | example: "configuration state changed or is busy" | - |
| `error.request_id` | `string` | yes | format: uuid; example: "33ba813e-fc63-4af8-b338-7f7486f82202" | - |

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
| `error` | `object` | yes | - | - |
| `error.kind` | `string` | yes | example: "internal_error" | - |
| `error.message` | `string` | yes | example: "request could not be completed" | - |
| `error.request_id` | `string` | yes | format: uuid; example: "33ba813e-fc63-4af8-b338-7f7486f82202" | - |

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
| `error` | `object` | yes | - | - |
| `error.kind` | `string` | yes | example: "runtime_unavailable" | - |
| `error.message` | `string` | yes | example: "OpenVPN runtime is unavailable" | - |
| `error.request_id` | `string` | yes | format: uuid; example: "33ba813e-fc63-4af8-b338-7f7486f82202" | - |

### 19. `GET /api/v1/config/desired`

Read desired configuration and CAS digest

#### Request parameters

##### Header parameters

| Field | Location | Type | Required | Format / example / constraints | Purpose |
|---|---|---|---|---|---|
| `Authorization` | `header` | `string` | yes | Bearer ovpn_v1.<uuid>.<secret> | API authentication credential. Put the API key generated by the service after Bearer. |

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
| `digest` | `string` | yes | pattern: `^[0-9a-f]{64}$`; example: "be7ed82796b39a77ce949201a4d9483cf09e56648f14b8b9b192bb83d2c4d042" | Lowercase SHA-256 digest identifying an exact desired configuration. |
| `config` | `object` | yes | - | - |
| `config.version` | `integer` | yes | constant: 1 | Configuration schema version. Must be 1. |
| `config.server` | `object` | yes | - | OpenVPN listener and transport settings. |
| `config.server.endpoint` | `string` | yes | example: "vpn.example.com" | Public hostname or IP address placed in generated client profiles. |
| `config.server.protocol` | `string` | yes | values: "udp", "tcp" | OpenVPN transport protocol. |
| `config.server.family` | `string` | yes | values: "auto", "ipv4", "ipv6" | Address family used for the server transport. |
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
| `error` | `object` | yes | - | - |
| `error.kind` | `string` | yes | example: "unauthenticated" | - |
| `error.message` | `string` | yes | example: "API key is missing or invalid" | - |
| `error.request_id` | `string` | yes | format: uuid; example: "33ba813e-fc63-4af8-b338-7f7486f82202" | - |

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
| `error` | `object` | yes | - | - |
| `error.kind` | `string` | yes | example: "method_not_allowed" | - |
| `error.message` | `string` | yes | example: "method is not allowed" | - |
| `error.request_id` | `string` | yes | format: uuid; example: "33ba813e-fc63-4af8-b338-7f7486f82202" | - |

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
| `error` | `object` | yes | - | - |
| `error.kind` | `string` | yes | example: "configuration_conflict" | - |
| `error.message` | `string` | yes | example: "configuration state changed or is busy" | - |
| `error.request_id` | `string` | yes | format: uuid; example: "33ba813e-fc63-4af8-b338-7f7486f82202" | - |

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
| `error` | `object` | yes | - | - |
| `error.kind` | `string` | yes | example: "invalid_client" | - |
| `error.message` | `string` | yes | example: "client request is not valid for the current state" | - |
| `error.request_id` | `string` | yes | format: uuid; example: "33ba813e-fc63-4af8-b338-7f7486f82202" | - |

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
| `error` | `object` | yes | - | - |
| `error.kind` | `string` | yes | example: "internal_error" | - |
| `error.message` | `string` | yes | example: "request could not be completed" | - |
| `error.request_id` | `string` | yes | format: uuid; example: "33ba813e-fc63-4af8-b338-7f7486f82202" | - |

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
| `error` | `object` | yes | - | - |
| `error.kind` | `string` | yes | example: "runtime_unavailable" | - |
| `error.message` | `string` | yes | example: "OpenVPN runtime is unavailable" | - |
| `error.request_id` | `string` | yes | format: uuid; example: "33ba813e-fc63-4af8-b338-7f7486f82202" | - |

### 20. `PUT /api/v1/config/desired`

Replace desired configuration with CAS. Send the previous desired digest as a quoted If-Match value and the complete normalized configuration as the body.

#### Request parameters

##### Header parameters

| Field | Location | Type | Required | Format / example / constraints | Purpose |
|---|---|---|---|---|---|
| `Authorization` | `header` | `string` | yes | Bearer ovpn_v1.<uuid>.<secret> | API authentication credential. Put the API key generated by the service after Bearer. |
| `Content-Type` | `header` | `string` | yes | application/json | Declares that the request body uses JSON. |
| `If-Match` | `header` | `string` | yes | pattern: `^"[0-9a-f]{64}"$`; example: "\"be7ed82796b39a77ce949201a4d9483cf09e56648f14b8b9b192bb83d2c4d042\"" | Exactly one quoted lowercase SHA-256 digest from the previous desired response ETag. |

##### JSON body fields

| Field | Location | Type | Required | Format / example / constraints | Purpose |
|---|---|---|---|---|---|
| `version` | `body` | `integer` | yes | constant: 1 | Configuration schema version. Must be 1. |
| `server` | `body` | `object` | yes | - | OpenVPN listener and transport settings. |
| `server.endpoint` | `body` | `string` | yes | example: "vpn.example.com" | Public hostname or IP address placed in generated client profiles. |
| `server.protocol` | `body` | `string` | yes | values: "udp", "tcp" | OpenVPN transport protocol. |
| `server.family` | `body` | `string` | yes | values: "auto", "ipv4", "ipv6" | Address family used for the server transport. |
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
| `digest` | `string` | yes | pattern: `^[0-9a-f]{64}$`; example: "be7ed82796b39a77ce949201a4d9483cf09e56648f14b8b9b192bb83d2c4d042" | Lowercase SHA-256 digest identifying an exact desired configuration. |
| `config` | `object` | yes | - | - |
| `config.version` | `integer` | yes | constant: 1 | Configuration schema version. Must be 1. |
| `config.server` | `object` | yes | - | OpenVPN listener and transport settings. |
| `config.server.endpoint` | `string` | yes | example: "vpn.example.com" | Public hostname or IP address placed in generated client profiles. |
| `config.server.protocol` | `string` | yes | values: "udp", "tcp" | OpenVPN transport protocol. |
| `config.server.family` | `string` | yes | values: "auto", "ipv4", "ipv6" | Address family used for the server transport. |
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
| `error` | `object` | yes | - | - |
| `error.kind` | `string` | yes | example: "invalid_json" | - |
| `error.message` | `string` | yes | example: "request body is invalid" | - |
| `error.request_id` | `string` | yes | format: uuid; example: "33ba813e-fc63-4af8-b338-7f7486f82202" | - |

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
| `error` | `object` | yes | - | - |
| `error.kind` | `string` | yes | example: "unauthenticated" | - |
| `error.message` | `string` | yes | example: "API key is missing or invalid" | - |
| `error.request_id` | `string` | yes | format: uuid; example: "33ba813e-fc63-4af8-b338-7f7486f82202" | - |

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
| `error` | `object` | yes | - | - |
| `error.kind` | `string` | yes | example: "method_not_allowed" | - |
| `error.message` | `string` | yes | example: "method is not allowed" | - |
| `error.request_id` | `string` | yes | format: uuid; example: "33ba813e-fc63-4af8-b338-7f7486f82202" | - |

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
| `error` | `object` | yes | - | - |
| `error.kind` | `string` | yes | example: "configuration_conflict" | - |
| `error.message` | `string` | yes | example: "configuration state changed or is busy" | - |
| `error.request_id` | `string` | yes | format: uuid; example: "33ba813e-fc63-4af8-b338-7f7486f82202" | - |

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
| `error` | `object` | yes | - | - |
| `error.kind` | `string` | yes | example: "invalid_client" | - |
| `error.message` | `string` | yes | example: "client request is not valid for the current state" | - |
| `error.request_id` | `string` | yes | format: uuid; example: "33ba813e-fc63-4af8-b338-7f7486f82202" | - |

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
| `error` | `object` | yes | - | - |
| `error.kind` | `string` | yes | example: "internal_error" | - |
| `error.message` | `string` | yes | example: "request could not be completed" | - |
| `error.request_id` | `string` | yes | format: uuid; example: "33ba813e-fc63-4af8-b338-7f7486f82202" | - |

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
| `error` | `object` | yes | - | - |
| `error.kind` | `string` | yes | example: "runtime_unavailable" | - |
| `error.message` | `string` | yes | example: "OpenVPN runtime is unavailable" | - |
| `error.request_id` | `string` | yes | format: uuid; example: "33ba813e-fc63-4af8-b338-7f7486f82202" | - |

### 21. `GET /api/v1/config/plan`

Plan desired-to-applied changes

#### Request parameters

##### Header parameters

| Field | Location | Type | Required | Format / example / constraints | Purpose |
|---|---|---|---|---|---|
| `Authorization` | `header` | `string` | yes | Bearer ovpn_v1.<uuid>.<secret> | API authentication credential. Put the API key generated by the service after Bearer. |

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
| `version` | `integer` | yes | constant: 1 | - |
| `instance_id` | `string` | yes | format: uuid; example: "bbffeb8c-2d11-4613-874d-b5fcc804a608" | - |
| `configuration` | `object` | yes | - | - |
| `configuration.initial` | `boolean` | yes | example: false | - |
| `configuration.current_revision` | `integer` | yes | minimum: 0; example: 12 | - |
| `configuration.target_revision` | `integer` | yes | minimum: 1; example: 13 | - |
| `configuration.current_digest` | `string` | no | pattern: `^[0-9a-f]{64}$`; example: "be7ed82796b39a77ce949201a4d9483cf09e56648f14b8b9b192bb83d2c4d042" | Lowercase SHA-256 digest identifying an exact desired configuration. |
| `configuration.desired_digest` | `string` | yes | pattern: `^[0-9a-f]{64}$`; example: "489b950c0d134b5d7e8f350b2cb4bfa4d6d163d841f840964baf6d74c65cc8ca" | Lowercase SHA-256 digest identifying an exact desired configuration. |
| `configuration.in_sync` | `boolean` | yes | example: false | - |
| `configuration.changes` | `object[]` | yes | example: [{"field":"server.endpoint","before":"vpn.example.com","after":"vpn-new.example.com"}] | - |
| `configuration.changes[].field` | `string` | yes | example: "server.endpoint" | - |
| `configuration.changes[].before` | `any` | yes | example: "vpn.example.com" | - |
| `configuration.changes[].after` | `any` | yes | example: "vpn-new.example.com" | - |
| `configuration.impact` | `object` | yes | - | - |
| `configuration.impact.restart_required` | `boolean` | yes | example: true | - |
| `configuration.impact.address_remap` | `boolean` | yes | example: false | - |
| `configuration.impact.firewall_reconcile` | `boolean` | yes | example: false | - |
| `configuration.impact.profile_redistribution` | `boolean` | yes | example: true | - |
| `configuration.impact.derived_artifacts` | `string[]` | yes | example: ["server_config","client_profiles"] | - |
| `address_changes` | `object[]` | yes | example: [] | - |
| `address_changes[].client` | `object` | yes | - | - |
| `address_changes[].client.id` | `string` | yes | format: uuid; example: "c0d4f871-6ea6-42b3-9e7b-f00cc1ec354e" | - |
| `address_changes[].client.name` | `string` | yes | example: "string" | - |
| `address_changes[].before` | `object` | yes | - | - |
| `address_changes[].before.mode` | `string` | yes | example: "string" | - |
| `address_changes[].before.address` | `string \| null` | yes | format: ipv4; example: "10.42.0.30" | - |
| `address_changes[].before.state` | `string` | yes | example: "string" | - |
| `address_changes[].after` | `object` | yes | - | - |
| `address_changes[].after.mode` | `string` | yes | example: "string" | - |
| `address_changes[].after.address` | `string \| null` | yes | format: ipv4; example: "10.42.0.30" | - |
| `address_changes[].after.state` | `string` | yes | example: "string" | - |
| `artifacts` | `object[]` | yes | example: [] | - |
| `artifacts[].owner_kind` | `string` | yes | example: "string" | - |
| `artifacts[].owner_id` | `string` | yes | example: "string" | - |
| `artifacts[].kind` | `string` | yes | example: "string" | - |
| `artifacts[].key` | `string` | yes | example: "string" | - |
| `artifacts[].action` | `string` | yes | values: "regenerate", "delete" | - |
| `profile_redistribution` | `object[]` | yes | example: [] | - |
| `profile_redistribution[].id` | `string` | yes | format: uuid; example: "c0d4f871-6ea6-42b3-9e7b-f00cc1ec354e" | - |
| `profile_redistribution[].name` | `string` | yes | example: "string" | - |
| `firewall` | `object` | yes | - | - |
| `firewall.reconcile` | `boolean` | yes | example: false | - |
| `firewall.before` | `object \| null` | yes | example: {"network":"string","nat_enabled":false,"nat_interface":"string","routes":["string"]} | - |
| `firewall.after` | `object \| null` | yes | example: {"network":"string","nat_enabled":false,"nat_interface":"string","routes":["string"]} | - |

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
| `error` | `object` | yes | - | - |
| `error.kind` | `string` | yes | example: "unauthenticated" | - |
| `error.message` | `string` | yes | example: "API key is missing or invalid" | - |
| `error.request_id` | `string` | yes | format: uuid; example: "33ba813e-fc63-4af8-b338-7f7486f82202" | - |

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
| `error` | `object` | yes | - | - |
| `error.kind` | `string` | yes | example: "method_not_allowed" | - |
| `error.message` | `string` | yes | example: "method is not allowed" | - |
| `error.request_id` | `string` | yes | format: uuid; example: "33ba813e-fc63-4af8-b338-7f7486f82202" | - |

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
| `error` | `object` | yes | - | - |
| `error.kind` | `string` | yes | example: "configuration_conflict" | - |
| `error.message` | `string` | yes | example: "configuration state changed or is busy" | - |
| `error.request_id` | `string` | yes | format: uuid; example: "33ba813e-fc63-4af8-b338-7f7486f82202" | - |

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
| `error` | `object` | yes | - | - |
| `error.kind` | `string` | yes | example: "invalid_client" | - |
| `error.message` | `string` | yes | example: "client request is not valid for the current state" | - |
| `error.request_id` | `string` | yes | format: uuid; example: "33ba813e-fc63-4af8-b338-7f7486f82202" | - |

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
| `error` | `object` | yes | - | - |
| `error.kind` | `string` | yes | example: "internal_error" | - |
| `error.message` | `string` | yes | example: "request could not be completed" | - |
| `error.request_id` | `string` | yes | format: uuid; example: "33ba813e-fc63-4af8-b338-7f7486f82202" | - |

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
| `error` | `object` | yes | - | - |
| `error.kind` | `string` | yes | example: "runtime_unavailable" | - |
| `error.message` | `string` | yes | example: "OpenVPN runtime is unavailable" | - |
| `error.request_id` | `string` | yes | format: uuid; example: "33ba813e-fc63-4af8-b338-7f7486f82202" | - |

### 22. `POST /api/v1/config/apply`

Apply the current desired configuration online. Use desired_digest from GET/PUT desired and current_revision from GET plan. The API remains available while OpenVPN and the broker restart.

#### Request parameters

##### Header parameters

| Field | Location | Type | Required | Format / example / constraints | Purpose |
|---|---|---|---|---|---|
| `Authorization` | `header` | `string` | yes | Bearer ovpn_v1.<uuid>.<secret> | API authentication credential. Put the API key generated by the service after Bearer. |
| `Content-Type` | `header` | `string` | yes | application/json | Declares that the request body uses JSON. |

##### JSON body fields

| Field | Location | Type | Required | Format / example / constraints | Purpose |
|---|---|---|---|---|---|
| `desired_digest` | `body` | `string` | yes | pattern: `^[0-9a-f]{64}$`; example: "489b950c0d134b5d7e8f350b2cb4bfa4d6d163d841f840964baf6d74c65cc8ca" | Lowercase SHA-256 digest identifying an exact desired configuration. |
| `current_revision` | `body` | `integer` | yes | minimum: 1; example: 12 | Applied revision returned by the configuration plan. |
| `force` | `body` | `boolean` | no | default: false | Bypasses only confirmed health-preflight warnings; never bypasses schema, CAS, lock, or recovery checks. |

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
| `version` | `integer` | yes | constant: 1 | - |
| `applied` | `boolean` | yes | example: true | - |
| `operation_id` | `string` | no | format: uuid; example: "5b781e08-3d44-41fb-81db-42edeedc61e1" | - |
| `activation` | `object` | yes | - | - |
| `activation.restart_required` | `boolean` | yes | example: true | - |
| `activation.runtime_restarted` | `boolean` | yes | example: true | - |
| `activation.profile_redistribution` | `object[]` | yes | example: [] | - |
| `activation.profile_redistribution[].id` | `string` | yes | format: uuid; example: "c0d4f871-6ea6-42b3-9e7b-f00cc1ec354e" | - |
| `activation.profile_redistribution[].name` | `string` | yes | example: "string" | - |
| `plan` | `object` | yes | - | - |
| `plan.version` | `integer` | yes | constant: 1 | - |
| `plan.instance_id` | `string` | yes | format: uuid; example: "bbffeb8c-2d11-4613-874d-b5fcc804a608" | - |
| `plan.configuration` | `object` | yes | - | - |
| `plan.configuration.initial` | `boolean` | yes | example: false | - |
| `plan.configuration.current_revision` | `integer` | yes | minimum: 0; example: 12 | - |
| `plan.configuration.target_revision` | `integer` | yes | minimum: 1; example: 13 | - |
| `plan.configuration.current_digest` | `string` | no | pattern: `^[0-9a-f]{64}$`; example: "be7ed82796b39a77ce949201a4d9483cf09e56648f14b8b9b192bb83d2c4d042" | Lowercase SHA-256 digest identifying an exact desired configuration. |
| `plan.configuration.desired_digest` | `string` | yes | pattern: `^[0-9a-f]{64}$`; example: "489b950c0d134b5d7e8f350b2cb4bfa4d6d163d841f840964baf6d74c65cc8ca" | Lowercase SHA-256 digest identifying an exact desired configuration. |
| `plan.configuration.in_sync` | `boolean` | yes | example: false | - |
| `plan.configuration.changes` | `object[]` | yes | example: [] | - |
| `plan.configuration.changes[].field` | `string` | yes | example: "server.endpoint" | - |
| `plan.configuration.changes[].before` | `any` | yes | example: "string" | - |
| `plan.configuration.changes[].after` | `any` | yes | example: "string" | - |
| `plan.configuration.impact` | `object` | yes | - | - |
| `plan.configuration.impact.restart_required` | `boolean` | yes | example: true | - |
| `plan.configuration.impact.address_remap` | `boolean` | yes | example: false | - |
| `plan.configuration.impact.firewall_reconcile` | `boolean` | yes | example: false | - |
| `plan.configuration.impact.profile_redistribution` | `boolean` | yes | example: false | - |
| `plan.configuration.impact.derived_artifacts` | `string[]` | yes | example: [] | - |
| `plan.address_changes` | `object[]` | yes | example: [] | - |
| `plan.address_changes[].client` | `object` | yes | - | - |
| `plan.address_changes[].client.id` | `string` | yes | format: uuid; example: "c0d4f871-6ea6-42b3-9e7b-f00cc1ec354e" | - |
| `plan.address_changes[].client.name` | `string` | yes | example: "string" | - |
| `plan.address_changes[].before` | `object` | yes | - | - |
| `plan.address_changes[].before.mode` | `string` | yes | example: "string" | - |
| `plan.address_changes[].before.address` | `string \| null` | yes | format: ipv4; example: "10.42.0.30" | - |
| `plan.address_changes[].before.state` | `string` | yes | example: "string" | - |
| `plan.address_changes[].after` | `object` | yes | - | - |
| `plan.address_changes[].after.mode` | `string` | yes | example: "string" | - |
| `plan.address_changes[].after.address` | `string \| null` | yes | format: ipv4; example: "10.42.0.30" | - |
| `plan.address_changes[].after.state` | `string` | yes | example: "string" | - |
| `plan.artifacts` | `object[]` | yes | example: [] | - |
| `plan.artifacts[].owner_kind` | `string` | yes | example: "string" | - |
| `plan.artifacts[].owner_id` | `string` | yes | example: "string" | - |
| `plan.artifacts[].kind` | `string` | yes | example: "string" | - |
| `plan.artifacts[].key` | `string` | yes | example: "string" | - |
| `plan.artifacts[].action` | `string` | yes | values: "regenerate", "delete" | - |
| `plan.profile_redistribution` | `object[]` | yes | example: [] | - |
| `plan.profile_redistribution[].id` | `string` | yes | format: uuid; example: "c0d4f871-6ea6-42b3-9e7b-f00cc1ec354e" | - |
| `plan.profile_redistribution[].name` | `string` | yes | example: "string" | - |
| `plan.firewall` | `object` | yes | - | - |
| `plan.firewall.reconcile` | `boolean` | yes | example: false | - |
| `plan.firewall.before` | `object \| null` | yes | example: {"network":"string","nat_enabled":false,"nat_interface":"string","routes":["string"]} | - |
| `plan.firewall.after` | `object \| null` | yes | example: {"network":"string","nat_enabled":false,"nat_interface":"string","routes":["string"]} | - |

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
| `error` | `object` | yes | - | - |
| `error.kind` | `string` | yes | example: "invalid_json" | - |
| `error.message` | `string` | yes | example: "request body is invalid" | - |
| `error.request_id` | `string` | yes | format: uuid; example: "33ba813e-fc63-4af8-b338-7f7486f82202" | - |

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
| `error` | `object` | yes | - | - |
| `error.kind` | `string` | yes | example: "unauthenticated" | - |
| `error.message` | `string` | yes | example: "API key is missing or invalid" | - |
| `error.request_id` | `string` | yes | format: uuid; example: "33ba813e-fc63-4af8-b338-7f7486f82202" | - |

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
| `error` | `object` | yes | - | - |
| `error.kind` | `string` | yes | example: "method_not_allowed" | - |
| `error.message` | `string` | yes | example: "method is not allowed" | - |
| `error.request_id` | `string` | yes | format: uuid; example: "33ba813e-fc63-4af8-b338-7f7486f82202" | - |

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
| `error` | `object` | yes | - | - |
| `error.kind` | `string` | yes | example: "configuration_conflict" | - |
| `error.message` | `string` | yes | example: "configuration state changed or is busy" | - |
| `error.request_id` | `string` | yes | format: uuid; example: "33ba813e-fc63-4af8-b338-7f7486f82202" | - |

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
| `error` | `object` | yes | - | - |
| `error.kind` | `string` | yes | example: "invalid_client" | - |
| `error.message` | `string` | yes | example: "client request is not valid for the current state" | - |
| `error.request_id` | `string` | yes | format: uuid; example: "33ba813e-fc63-4af8-b338-7f7486f82202" | - |

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
| `error` | `object` | yes | - | - |
| `error.kind` | `string` | yes | example: "internal_error" | - |
| `error.message` | `string` | yes | example: "request could not be completed" | - |
| `error.request_id` | `string` | yes | format: uuid; example: "33ba813e-fc63-4af8-b338-7f7486f82202" | - |

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
| `error` | `object` | yes | - | - |
| `error.kind` | `string` | yes | example: "runtime_unavailable" | - |
| `error.message` | `string` | yes | example: "OpenVPN runtime is unavailable" | - |
| `error.request_id` | `string` | yes | format: uuid; example: "33ba813e-fc63-4af8-b338-7f7486f82202" | - |

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
