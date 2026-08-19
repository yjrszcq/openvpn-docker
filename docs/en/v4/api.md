# REST API v1

The REST API provides authenticated remote access to the existing client, configuration, state, and runtime services. It does not replace SQLite authority, the operation journal, runtime locks, or the local `ovpn` CLI.

The API is disabled by default. It uses HTTP internally and does not manage TLS certificates. Put it behind an HTTPS reverse proxy for remote access.

When enabled, the API serves development documentation directly:

- `http://<OVPN_API_LISTEN>/docs/` is the frontend reference. Each of the 22 operations independently shows its complete HTTP request, parameters, body fields, success response, error responses, and JSON examples; developers do not have to assemble a call from grouped models.
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

Each of the following 22 operations is a self-contained contract. Except for `/healthz`, every request requires `Authorization: Bearer <API_KEY>`. UUIDs, digests, and timestamps are illustrative.

Error responses use the following JSON shape. Each operation lists its own possible status codes:

```json
{"error":{"kind":"client_not_found","message":"client was not found","request_id":"33ba813e-fc63-4af8-b338-7f7486f82202"}}
```

### 1. `GET /healthz`

Request:

```http
GET /healthz HTTP/1.1
Host: vpn-admin.example.com
```

Request body: none. Authentication is not required.

Successful `200 application/json` response:

```json
{"status":"ok"}
```

Error response: `405`, using the error body shown above.

### 2. `GET /api/v1/version`

Request:

```http
GET /api/v1/version HTTP/1.1
Host: vpn-admin.example.com
Authorization: Bearer ovpn_v1.<uuid>.<secret>
```

Request body: none.

Successful `200 application/json` response:

```json
{
  "version":"4.0.2",
  "data_schema":4,
  "commit":"dd9b5213f5002a7e69f160e3fd2615e0b3d8d224",
  "build_date":"2026-08-19T09:45:40Z",
  "go_version":"go1.26.5",
  "dependencies":{"sqlite":"github.com/mattn/go-sqlite3 v1.14.48","yaml":"go.yaml.in/yaml/v3 v3.0.4"},
  "compatibility":{"contract_version":1,"adapter":"openvpn-2.7","template_family":"openvpn-2.7","supported_openvpn_versions":["2.7.6"]}
}
```

Error responses: `401`, `405`, `503`.

### 3. `GET /api/v1/state`

Request:

```http
GET /api/v1/state HTTP/1.1
Host: vpn-admin.example.com
Authorization: Bearer ovpn_v1.<uuid>.<secret>
```

Request body: none.

Successful `200 application/json` response:

```json
{"version":1,"state":"HEALTHY","data_schema":4,"instance_id":"bbffeb8c-2d11-4613-874d-b5fcc804a608","revision":12,"scanned_at":"2026-08-19T09:55:07Z","issue_count":0,"pending_operation_count":0}
```

Error responses: `401`, `405`, `500`, `503`.

### 4. `GET /api/v1/state/doctor`

Request:

```http
GET /api/v1/state/doctor HTTP/1.1
Host: vpn-admin.example.com
Authorization: Bearer ovpn_v1.<uuid>.<secret>
```

Request body: none.

Successful `200 application/json` response:

```json
{"version":1,"state":"DEGRADED_REPAIRABLE","data_schema":4,"instance_id":"bbffeb8c-2d11-4613-874d-b5fcc804a608","revision":12,"scanned_at":"2026-08-19T09:55:07Z","issue_count":1,"pending_operation_count":0,"issues":[{"id":"DECLARATIVE_CONFIG_UNAVAILABLE","severity":"repairable","action":"export-config","target":"/etc/ovpn-conf/config.yaml","detail":"declarative configuration is unavailable"}]}
```

`issues` is omitted when empty. Error responses: `401`, `405`, `500`, `503`.

### 5. `GET /api/v1/clients`

Request:

```http
GET /api/v1/clients HTTP/1.1
Host: vpn-admin.example.com
Authorization: Bearer ovpn_v1.<uuid>.<secret>
```

Request body: none.

Successful `200 application/json` response:

```json
{"version":1,"clients":[{"id":"c0d4f871-6ea6-42b3-9e7b-f00cc1ec354e","name":"alice-laptop","status":"active","ipv4":{"mode":"static","address":"10.42.0.30","state":"configured"},"connection":"connected"}]}
```

`clients` is `[]` when no clients exist. Error responses: `401`, `405`, `500`, `503`.

### 6. `POST /api/v1/clients`

Request:

```http
POST /api/v1/clients HTTP/1.1
Host: vpn-admin.example.com
Authorization: Bearer ovpn_v1.<uuid>.<secret>
Content-Type: application/json

{"name":"alice-laptop","ipv4":"auto"}
```

`ipv4` accepts `auto`, `dynamic`, or a static IPv4 address.

Successful `201 application/json` response, with `Location: /api/v1/clients/{client_id}`:

```json
{"version":1,"operation_id":"7a21e1f8-1ce1-4694-977f-b275eadb8651","client":{"id":"c0d4f871-6ea6-42b3-9e7b-f00cc1ec354e","name":"alice-laptop","status":"active","ipv4":{"mode":"static","address":"10.42.0.2","state":"configured"}},"kick_required":false,"profile_redistribution_required":true}
```

Error responses: `400`, `401`, `409`, `422`, `500`, `503`.

### 7. `GET /api/v1/clients/{client_id}`

Request:

```http
GET /api/v1/clients/c0d4f871-6ea6-42b3-9e7b-f00cc1ec354e HTTP/1.1
Host: vpn-admin.example.com
Authorization: Bearer ovpn_v1.<uuid>.<secret>
```

`client_id` must be a complete canonical UUID. Request body: none.

Successful `200 application/json` response:

```json
{"id":"c0d4f871-6ea6-42b3-9e7b-f00cc1ec354e","name":"alice-laptop","status":"active","ipv4":{"mode":"static","address":"10.42.0.30","state":"configured"}}
```

Error responses: `400`, `401`, `404`, `405`, `500`, `503`.

### 8. `PATCH /api/v1/clients/{client_id}`

Request:

```http
PATCH /api/v1/clients/c0d4f871-6ea6-42b3-9e7b-f00cc1ec354e HTTP/1.1
Host: vpn-admin.example.com
Authorization: Bearer ovpn_v1.<uuid>.<secret>
Content-Type: application/json

{"name":"alice-notebook"}
```

Successful `200 application/json` response:

```json
{"version":1,"operation_id":"47260b5b-47dd-4f9b-814f-4803668c5934","client":{"id":"c0d4f871-6ea6-42b3-9e7b-f00cc1ec354e","name":"alice-notebook","status":"active","ipv4":{"mode":"static","address":"10.42.0.30","state":"configured"}},"kick_required":false,"profile_redistribution_required":true}
```

Error responses: `400`, `401`, `404`, `405`, `409`, `422`, `500`, `503`.

### 9. `DELETE /api/v1/clients/{client_id}`

Request:

```http
DELETE /api/v1/clients/c0d4f871-6ea6-42b3-9e7b-f00cc1ec354e HTTP/1.1
Host: vpn-admin.example.com
Authorization: Bearer ovpn_v1.<uuid>.<secret>
```

The request body must be empty; `{}` and `null` are rejected.

Successful `200 application/json` response:

```json
{"version":1,"operation_id":"47260b5b-47dd-4f9b-814f-4803668c5934","client":{"id":"c0d4f871-6ea6-42b3-9e7b-f00cc1ec354e","name":"alice-notebook","status":"deleted","ipv4":{"mode":"none","address":null,"state":"unavailable"}},"kick_required":false,"profile_redistribution_required":false}
```

Error responses: `400`, `401`, `404`, `405`, `409`, `422`, `500`, `503`.

### 10. `GET /api/v1/clients/{client_id}/profile`

Request:

```http
GET /api/v1/clients/c0d4f871-6ea6-42b3-9e7b-f00cc1ec354e/profile HTTP/1.1
Host: vpn-admin.example.com
Authorization: Bearer ovpn_v1.<uuid>.<secret>
```

Request body: none.

Successful `200 application/x-openvpn-profile` response; this is not JSON:

```http
Content-Type: application/x-openvpn-profile
Content-Disposition: attachment; filename=alice-laptop.ovpn

client
# ovpn-client-id: c0d4f871-6ea6-42b3-9e7b-f00cc1ec354e
...
```

The profile contains a private key and must be treated as a credential. Error responses: `400`, `401`, `404`, `405`, `409`, `422`, `500`, `503`.

### 11. `POST /api/v1/clients/{client_id}/revoke`

Request:

```http
POST /api/v1/clients/c0d4f871-6ea6-42b3-9e7b-f00cc1ec354e/revoke HTTP/1.1
Host: vpn-admin.example.com
Authorization: Bearer ovpn_v1.<uuid>.<secret>
Content-Type: application/json

{"release_ipv4":false}
```

`false` retains the static address; `true` releases it immediately.

Successful `200 application/json` response:

```json
{"version":1,"operation_id":"47260b5b-47dd-4f9b-814f-4803668c5934","client":{"id":"c0d4f871-6ea6-42b3-9e7b-f00cc1ec354e","name":"alice-laptop","status":"revoked","ipv4":{"mode":"static","address":"10.42.0.30","state":"retained"}},"kick_required":true,"profile_redistribution_required":false,"runtime":{"status":"ok","result":{"version":1,"client_id":"c0d4f871-6ea6-42b3-9e7b-f00cc1ec354e","client_name":"alice-laptop","was_connected":true,"disconnected":true,"connections":1}}}
```

Error responses: `400`, `401`, `404`, `405`, `409`, `422`, `500`, `503`.

### 12. `POST /api/v1/clients/{client_id}/reissue`

Request:

```http
POST /api/v1/clients/c0d4f871-6ea6-42b3-9e7b-f00cc1ec354e/reissue HTTP/1.1
Host: vpn-admin.example.com
Authorization: Bearer ovpn_v1.<uuid>.<secret>
Content-Type: application/json

{"ipv4":"dynamic"}
```

`ipv4` accepts `auto`, `dynamic`, or a static IPv4 address.

Successful `200 application/json` response:

```json
{"version":1,"operation_id":"47260b5b-47dd-4f9b-814f-4803668c5934","client":{"id":"c0d4f871-6ea6-42b3-9e7b-f00cc1ec354e","name":"alice-notebook","status":"active","ipv4":{"mode":"dynamic","address":null,"state":"configured"}},"kick_required":true,"profile_redistribution_required":true,"runtime":{"status":"unavailable"}}
```

Error responses: `400`, `401`, `404`, `405`, `409`, `422`, `500`, `503`.

### 13. `PUT /api/v1/clients/{client_id}/ipv4`

Request:

```http
PUT /api/v1/clients/c0d4f871-6ea6-42b3-9e7b-f00cc1ec354e/ipv4 HTTP/1.1
Host: vpn-admin.example.com
Authorization: Bearer ovpn_v1.<uuid>.<secret>
Content-Type: application/json

{"mode":"static","address":"10.42.0.30"}
```

Other valid bodies are `{"mode":"auto"}` and `{"mode":"dynamic"}`; those modes reject `address`.

Successful `200 application/json` response:

```json
{"version":1,"operation_id":"d41dbfce-b40e-4672-8a62-cb401f8c099c","clients":[{"id":"c0d4f871-6ea6-42b3-9e7b-f00cc1ec354e","name":"alice-laptop","status":"active","ipv4":{"mode":"static","address":"10.42.0.30","state":"configured"}}],"kick_required":["c0d4f871-6ea6-42b3-9e7b-f00cc1ec354e"],"runtime":[{"client_id":"c0d4f871-6ea6-42b3-9e7b-f00cc1ec354e","status":"ok","result":{"version":1,"client_id":"c0d4f871-6ea6-42b3-9e7b-f00cc1ec354e","client_name":"alice-laptop","was_connected":false,"disconnected":false,"connections":0}}]}
```

Error responses: `400`, `401`, `404`, `405`, `409`, `422`, `500`, `503`.

### 14. `DELETE /api/v1/clients/{client_id}/ipv4`

Request:

```http
DELETE /api/v1/clients/c0d4f871-6ea6-42b3-9e7b-f00cc1ec354e/ipv4 HTTP/1.1
Host: vpn-admin.example.com
Authorization: Bearer ovpn_v1.<uuid>.<secret>
```

The request body must be empty. Only a revoked client with a retained static address is valid.

Successful `200 application/json` response:

```json
{"version":1,"operation_id":"d41dbfce-b40e-4672-8a62-cb401f8c099c","clients":[{"id":"c0d4f871-6ea6-42b3-9e7b-f00cc1ec354e","name":"alice-notebook","status":"revoked","ipv4":{"mode":"none","address":null,"state":"unavailable"}}],"kick_required":[],"runtime":[]}
```

Error responses: `400`, `401`, `404`, `405`, `409`, `422`, `500`, `503`.

### 15. `POST /api/v1/clients/{client_id}/disconnect`

Request:

```http
POST /api/v1/clients/c0d4f871-6ea6-42b3-9e7b-f00cc1ec354e/disconnect HTTP/1.1
Host: vpn-admin.example.com
Authorization: Bearer ovpn_v1.<uuid>.<secret>
```

The request body must be empty. No active session is a successful no-op.

Successful `200 application/json` response:

```json
{"version":1,"client_id":"c0d4f871-6ea6-42b3-9e7b-f00cc1ec354e","client_name":"alice-laptop","was_connected":false,"disconnected":false,"connections":0}
```

Error responses: `400`, `401`, `404`, `405`, `500`, `503`.

### 16. `GET /api/v1/runtime`

Request:

```http
GET /api/v1/runtime HTTP/1.1
Host: vpn-admin.example.com
Authorization: Bearer ovpn_v1.<uuid>.<secret>
```

Request body: none.

Successful `200 application/json` response:

```json
{"version":1,"daemon":"running","management":"connected","client_count":1,"clients":[{"client_id":"c0d4f871-6ea6-42b3-9e7b-f00cc1ec354e","client_name":"alice-laptop","remote_address":"203.0.113.10:53210","virtual_address":"10.42.0.30"}]}
```

Error responses: `401`, `405`, `500`, `503`.

### 17. `GET /api/v1/runtime/events`

Request:

```http
GET /api/v1/runtime/events?lines=100 HTTP/1.1
Host: vpn-admin.example.com
Authorization: Bearer ovpn_v1.<uuid>.<secret>
```

`lines` is optional, defaults to `100`, and accepts `0..1000`. Request body: none.

Successful `200 application/json` response:

```json
{"version":1,"events":[{"timestamp":"2026-08-19T09:55:07Z","event":"client-disconnect","operation":"runtime.disconnect","outcome":"success","client_id":"c0d4f871-6ea6-42b3-9e7b-f00cc1ec354e","client_name":"alice-laptop"}]}
```

Events may contain operation-specific extra fields. Error responses: `400`, `401`, `405`, `500`, `503`.

### 18. `GET /api/v1/config/applied`

Request:

```http
GET /api/v1/config/applied HTTP/1.1
Host: vpn-admin.example.com
Authorization: Bearer ovpn_v1.<uuid>.<secret>
```

Request body: none.

Successful `200 application/json` response:

```json
{"revision":12,"digest":"489b950c0d134b5d7e8f350b2cb4bfa4d6d163d841f840964baf6d74c65cc8ca","config":{"version":1,"server":{"endpoint":"vpn.example.com","protocol":"udp","family":"auto","port":1194,"client_to_client":true},"ipv4":{"network":"10.42.0.0/24","dynamic_pool_size":64,"nat_enabled":false,"nat_interface":"auto","redirect_gateway":false,"dns":["1.1.1.1"],"routes":["10.20.0.0/16"]},"logging":{"max_bytes":10485760,"backups":5}}}
```

Error responses: `401`, `405`, `409`, `500`, `503`.

### 19. `GET /api/v1/config/desired`

Request:

```http
GET /api/v1/config/desired HTTP/1.1
Host: vpn-admin.example.com
Authorization: Bearer ovpn_v1.<uuid>.<secret>
```

Request body: none.

Successful `200 application/json` response, plus `ETag: "<digest>"`:

```json
{"digest":"489b950c0d134b5d7e8f350b2cb4bfa4d6d163d841f840964baf6d74c65cc8ca","config":{"version":1,"server":{"endpoint":"vpn.example.com","protocol":"udp","family":"auto","port":1194,"client_to_client":true},"ipv4":{"network":"10.42.0.0/24","dynamic_pool_size":64,"nat_enabled":false,"nat_interface":"auto","redirect_gateway":false,"dns":["1.1.1.1"],"routes":["10.20.0.0/16"]},"logging":{"max_bytes":10485760,"backups":5}}}
```

Error responses: `401`, `405`, `409`, `422`, `500`, `503`.

### 20. `PUT /api/v1/config/desired`

The request body is the complete bare `Configuration`; do not wrap it in `config`:

```http
PUT /api/v1/config/desired HTTP/1.1
Host: vpn-admin.example.com
Authorization: Bearer ovpn_v1.<uuid>.<secret>
Content-Type: application/json
If-Match: "489b950c0d134b5d7e8f350b2cb4bfa4d6d163d841f840964baf6d74c65cc8ca"

{"version":1,"server":{"endpoint":"vpn.example.com","protocol":"udp","family":"auto","port":1194,"client_to_client":true},"ipv4":{"network":"10.42.0.0/24","dynamic_pool_size":64,"nat_enabled":false,"nat_interface":"auto","redirect_gateway":false,"dns":["1.1.1.1"],"routes":["10.20.0.0/16"]},"logging":{"max_bytes":10485760,"backups":5}}
```

Successful `200 application/json` response, plus the new `ETag`:

```json
{"digest":"489b950c0d134b5d7e8f350b2cb4bfa4d6d163d841f840964baf6d74c65cc8ca","config":{"version":1,"server":{"endpoint":"vpn.example.com","protocol":"udp","family":"auto","port":1194,"client_to_client":true},"ipv4":{"network":"10.42.0.0/24","dynamic_pool_size":64,"nat_enabled":false,"nat_interface":"auto","redirect_gateway":false,"dns":["1.1.1.1"],"routes":["10.20.0.0/16"]},"logging":{"max_bytes":10485760,"backups":5}}}
```

Error responses: `400`, `401`, `405`, `409`, `422`, `500`, `503`.

### 21. `GET /api/v1/config/plan`

Request:

```http
GET /api/v1/config/plan HTTP/1.1
Host: vpn-admin.example.com
Authorization: Bearer ovpn_v1.<uuid>.<secret>
```

Request body: none.

Successful `200 application/json` response:

```json
{"version":1,"instance_id":"bbffeb8c-2d11-4613-874d-b5fcc804a608","configuration":{"initial":false,"current_revision":12,"target_revision":12,"current_digest":"489b950c0d134b5d7e8f350b2cb4bfa4d6d163d841f840964baf6d74c65cc8ca","desired_digest":"489b950c0d134b5d7e8f350b2cb4bfa4d6d163d841f840964baf6d74c65cc8ca","in_sync":true,"changes":[],"impact":{"restart_required":false,"address_remap":false,"firewall_reconcile":false,"profile_redistribution":false,"derived_artifacts":[]}},"address_changes":[],"artifacts":[],"profile_redistribution":[],"firewall":{"reconcile":false,"before":null,"after":null}}
```

Error responses: `401`, `405`, `409`, `422`, `500`, `503`.

### 22. `POST /api/v1/config/apply`

The digest and revision must come from the immediately preceding desired/plan calls:

```http
POST /api/v1/config/apply HTTP/1.1
Host: vpn-admin.example.com
Authorization: Bearer ovpn_v1.<uuid>.<secret>
Content-Type: application/json

{"desired_digest":"489b950c0d134b5d7e8f350b2cb4bfa4d6d163d841f840964baf6d74c65cc8ca","current_revision":12,"force":false}
```

Successful `200 application/json` response:

```json
{"version":1,"applied":false,"activation":{"restart_required":false,"runtime_restarted":false,"profile_redistribution":[]},"plan":{"version":1,"instance_id":"bbffeb8c-2d11-4613-874d-b5fcc804a608","configuration":{"initial":false,"current_revision":12,"target_revision":12,"current_digest":"489b950c0d134b5d7e8f350b2cb4bfa4d6d163d841f840964baf6d74c65cc8ca","desired_digest":"489b950c0d134b5d7e8f350b2cb4bfa4d6d163d841f840964baf6d74c65cc8ca","in_sync":true,"changes":[],"impact":{"restart_required":false,"address_remap":false,"firewall_reconcile":false,"profile_redistribution":false,"derived_artifacts":[]}},"address_changes":[],"artifacts":[],"profile_redistribution":[],"firewall":{"reconcile":false,"before":null,"after":null}}}
```

When a change is made, `applied` is `true` and `operation_id` is present; `activation` and `plan` remain complete. Error responses: `400`, `401`, `405`, `409`, `422`, `500`, `503`.

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
