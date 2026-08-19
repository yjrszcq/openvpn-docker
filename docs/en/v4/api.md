# REST API v1

The REST API provides authenticated remote access to the existing client, configuration, state, and runtime services. It does not replace SQLite authority, the operation journal, runtime locks, or the local `ovpn` CLI.

The API is disabled by default. It uses HTTP internally and does not manage TLS certificates. Put it behind an HTTPS reverse proxy for remote access.

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
