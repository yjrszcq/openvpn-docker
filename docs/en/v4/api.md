# REST API v1

The REST API provides authenticated remote access to the existing client, configuration, state, and runtime services. It does not replace SQLite authority, the operation journal, runtime locks, or the local `ovpn` CLI.

The API is disabled by default. It uses HTTP internally and does not manage TLS certificates. Put it behind an HTTPS reverse proxy for remote access.

When enabled, the API serves development documentation directly:

- `http://<OVPN_API_LISTEN>/docs/` is the self-contained browser reference with no CDN dependency.
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

## Resources

| Method | Path | Purpose |
|---|---|---|
| `GET` | `/healthz` | Minimal unauthenticated liveness check. |
| `GET` | `/api/v1/version` | Build, data schema, and OpenVPN compatibility. |
| `GET` | `/api/v1/state` | Instance state summary. |
| `GET` | `/api/v1/state/doctor` | Detailed SQLite, PKI, and artifact diagnostics. |
| `GET` | `/api/v1/clients` | List active and revoked clients. |
| `POST` | `/api/v1/clients` | Create a client. |
| `GET` | `/api/v1/clients/{id}` | Get one client by complete UUID. |
| `PATCH` | `/api/v1/clients/{id}` | Rename a client. |
| `DELETE` | `/api/v1/clients/{id}` | Delete local credentials and retain a UUID tombstone. |
| `GET` | `/api/v1/clients/{id}/profile` | Download an active client profile. |
| `POST` | `/api/v1/clients/{id}/revoke` | Revoke a client certificate. |
| `POST` | `/api/v1/clients/{id}/reissue` | Reissue credentials and a profile. |
| `PUT` | `/api/v1/clients/{id}/ipv4` | Set IPv4 intent. |
| `DELETE` | `/api/v1/clients/{id}/ipv4` | Release a revoked client's retained static address. |
| `POST` | `/api/v1/clients/{id}/disconnect` | Disconnect current sessions. |
| `GET` | `/api/v1/runtime` | Runtime and connected-client status. |
| `GET` | `/api/v1/runtime/events?lines=N` | Read 0 through 1000 recent structured events. |
| `GET` | `/api/v1/config/applied` | Read the applied SQLite revision. |
| `GET` | `/api/v1/config/desired` | Read normalized desired YAML and its digest. |
| `PUT` | `/api/v1/config/desired` | Validate and atomically replace desired YAML. |
| `GET` | `/api/v1/config/plan` | Plan desired-to-applied changes. |
| `POST` | `/api/v1/config/apply` | Apply the current desired configuration online. |

## Frontend contract

These tables cover all 22 operations. Except for `/healthz`, every request requires `Authorization: Bearer <API_KEY>`. An "empty body" operation rejects `{}`, `null`, and any other body content.

### System

| Request | Parameters | JSON body | Success response |
|---|---|---|---|
| `GET /healthz` | No authentication or query | None | `200 HealthResponse` |
| `GET /api/v1/version` | No query | None | `200 VersionResponse` |
| `GET /api/v1/state` | No query | None | `200 StateResponse` without `issues` |
| `GET /api/v1/state/doctor` | No query | None | `200 StateResponse`; includes `issues` when present |

### Clients

Every `{client_id}` must be a complete canonical UUID. Names and UUID prefixes are rejected.

| Request | Parameters | JSON body | Success response |
|---|---|---|---|
| `GET /api/v1/clients` | No query | None | `200 ClientListResponse` |
| `POST /api/v1/clients` | No query | `CreateClientRequest` | `201 ClientMutationResponse` plus `Location` header |
| `GET /api/v1/clients/{client_id}` | UUID path parameter | None | `200 Client` |
| `PATCH /api/v1/clients/{client_id}` | UUID path parameter | `RenameClientRequest` | `200 ClientMutationResponse` |
| `DELETE /api/v1/clients/{client_id}` | UUID path parameter | Empty body | `200 ClientMutationResponse` with `client.status="deleted"` |
| `GET /api/v1/clients/{client_id}/profile` | UUID path parameter | None | `200 application/x-openvpn-profile`, not JSON |
| `POST /api/v1/clients/{client_id}/revoke` | UUID path parameter | `RevokeClientRequest` | `200 ClientMutationResponse` |
| `POST /api/v1/clients/{client_id}/reissue` | UUID path parameter | `ReissueClientRequest` | `200 ClientMutationResponse` |
| `PUT /api/v1/clients/{client_id}/ipv4` | UUID path parameter | `IPv4Request` | `200 AddressMutationResponse` |
| `DELETE /api/v1/clients/{client_id}/ipv4` | UUID path parameter | Empty body | `200 AddressMutationResponse` |
| `POST /api/v1/clients/{client_id}/disconnect` | UUID path parameter | Empty body | `200 DisconnectResponse`; no active session is a successful no-op |

### Runtime

| Request | Parameters | JSON body | Success response |
|---|---|---|---|
| `GET /api/v1/runtime` | No query | None | `200 RuntimeResponse` |
| `GET /api/v1/runtime/events` | Optional `lines=0..1000`, default `100` | None | `200 RuntimeEventsResponse` |

### Configuration

| Request | Parameters | JSON body | Success response |
|---|---|---|---|
| `GET /api/v1/config/applied` | No query | None | `200 AppliedConfigurationResponse` |
| `GET /api/v1/config/desired` | No query | None | `200 DesiredConfigurationResponse` plus `ETag: "<digest>"` |
| `PUT /api/v1/config/desired` | Required `If-Match: "<old digest>"` | Complete bare `Configuration`, not wrapped in `config` | `200 DesiredConfigurationResponse` plus new `ETag` |
| `GET /api/v1/config/plan` | No query | None | `200 ConfigurationPlanResponse` |
| `POST /api/v1/config/apply` | No query | `ApplyConfigurationRequest` | `200 ApplyConfigurationResponse` |

### Request bodies

```ts
interface CreateClientRequest { name: string; ipv4: string }
interface RenameClientRequest { name: string }
interface RevokeClientRequest { release_ipv4: boolean }
interface ReissueClientRequest { ipv4: string }

type IPv4Request =
  | { mode: "auto" }
  | { mode: "dynamic" }
  | { mode: "static"; address: string };

interface ApplyConfigurationRequest {
  desired_digest: string;
  current_revision: number;
  force?: boolean;
}
```

`ipv4` on create/reissue accepts `auto`, `dynamic`, or a static IPv4 address. Static mode on `IPv4Request` requires `address`; auto/dynamic forbid it.

### Response models

The following TypeScript matches the actual JSON field names. `?` means the field may be omitted; `null` is distinct from omission.

```ts
type UUID = string;
type Digest = string;

interface HealthResponse { status: "ok" }
interface APIErrorResponse {
  error: { kind: string; message: string; request_id: UUID };
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
  state: "EMPTY" | "HEALTHY" | "DEGRADED_REPAIRABLE" |
    "DEGRADED_RECOVERABLE" | "DEGRADED_REISSUABLE" | "CRITICAL" | "UNRECOVERABLE";
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

interface ClientListResponse { version: 1; clients: Client[] }
interface DisconnectResponse {
  version: 1; client_id: UUID; client_name: string;
  was_connected: boolean; disconnected: boolean; connections: number;
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

interface RuntimeClient {
  client_id: UUID;
  client_name?: string;
  remote_address?: string;
  virtual_address?: string;
}
interface RuntimeResponse {
  version: 1; daemon: string; management: string;
  client_count: number; clients: RuntimeClient[];
}
interface RuntimeEvent {
  timestamp: string; event: string; operation: string; outcome: string;
  client_id?: UUID | null; client_name?: string | null;
  [key: string]: unknown;
}
interface RuntimeEventsResponse { version: 1; events: RuntimeEvent[] }
```

### Configuration models

```ts
interface Configuration {
  version: 1;
  server: {
    endpoint: string; protocol: "udp" | "tcp";
    family: "auto" | "ipv4" | "ipv6"; port: number;
    client_to_client: boolean;
  };
  ipv4: {
    network: string; dynamic_pool_size: number;
    nat_enabled: boolean; nat_interface: string;
    redirect_gateway: boolean; dns: string[]; routes: string[];
  };
  logging: { max_bytes: number; backups: number };
}

interface AppliedConfigurationResponse {
  revision: number; digest: Digest; config: Configuration;
}
interface DesiredConfigurationResponse { digest: Digest; config: Configuration }

interface ConfigurationComparison {
  initial: boolean;
  current_revision: number;
  target_revision: number;
  current_digest?: Digest;
  desired_digest: Digest;
  in_sync: boolean;
  changes: Array<{ field: string; before: unknown; after: unknown }>;
  impact: {
    restart_required: boolean; address_remap: boolean;
    firewall_reconcile: boolean; profile_redistribution: boolean;
    derived_artifacts: string[];
  };
}

interface FirewallState {
  network: string; nat_enabled: boolean; nat_interface: string; routes: string[];
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
  firewall: { reconcile: boolean; before: FirewallState | null; after: FirewallState | null };
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

The complete constraints, enums, nullable states, request examples, response examples, and per-operation error statuses are authoritative in `/docs/openapi.json`.

## Client requests

Creating a client accepts `auto`, `dynamic`, or one static IPv4 address:

```json
{"name":"laptop","ipv4":"auto"}
```

A successful create returns `201 Created` and `Location: /api/v1/clients/{id}`. Other client mutation bodies are:

| Operation | JSON body |
|---|---|
| Rename | `{"name":"new-name"}` |
| Revoke | `{"release_ipv4":false}` |
| Reissue | `{"ipv4":"dynamic"}` |
| Set automatic IPv4 | `{"mode":"auto"}` |
| Set dynamic IPv4 | `{"mode":"dynamic"}` |
| Set static IPv4 | `{"mode":"static","address":"10.42.0.20"}` |

Delete, IPv4 release, and disconnect accept no body. Reissue accepts the same `auto`, `dynamic`, or static address selection as create.

Some committed client and address changes require current sessions to be disconnected. If the broker is unavailable after the durable mutation commits, the response remains successful and reports `runtime.status` as `unavailable`. A direct disconnect request returns `503` when runtime control is unavailable.

Profiles contain private keys. Treat profile responses as credentials and never store them in browser caches, logs, analytics, or general download directories.

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
