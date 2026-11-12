# Contract: Embedded Operator GUI

**Feature**: `001-hot-upstream-switch`
**Date**: 2026-11-11

## Routes

| Method | Path | Auth | Purpose |
|--------|------|------|---------|
| GET | `/_shunt/ui` | none | The single-page GUI. |
| GET | `/_shunt/ui/` | none | Same page. |
| GET | `/_shunt/ui/app.js` | none | Embedded script. |
| GET | `/_shunt/ui/style.css` | none | Embedded stylesheet. |
| GET | `/_shunt/ui/favicon.svg` | none | Embedded icon. |
| GET | `/_shunt/api/overview` | none (read) | Status plus editable config view. |
| GET | `/_shunt/api/models` | none (read) | Cached or fetched catalog. |
| GET | `/_shunt/api/clients` | none (read) | Client targets, dialects, and write capability. |
| POST | `/_shunt/api/clients/preview` | token | Generate a non-secret client configuration. |
| POST | `/_shunt/api/clients/write` | token | Write Claude Code or Codex configuration with a backup. |
| GET | `/_shunt/api/clients/backups` | none (read) | List timestamped backups for one client. |
| POST | `/_shunt/api/clients/restore` | token | Restore a listed backup after creating a safety backup. |
| POST | `/_shunt/api/entry` | token | Change the listen IP/port. |
| POST | `/_shunt/api/profiles/{id}/endpoint` | token | Change a profile's scheme/host/port. |
| POST | `/_shunt/api/switch` | token | Select a profile in manual mode. |
| POST | `/_shunt/api/mode` | token | Set manual/auto and preferred profile. |
| POST | `/_shunt/api/models/refresh` | token | Force a catalog refresh. |

The page is served on the same listener as the data plane. There is no separate UI port and no
separate process. If the listener moves, the page is reachable at the new origin only.

## Authentication

At startup the relay generates a 256-bit random token, hex-encoded. It is injected into the
served `index.html` as a `<meta name="shunt-ui-token" content="...">` value and kept in memory.
Every mutation MUST send it as `X-Shunt-UI-Token`. A missing or wrong token is `403` with
`error.code = "ui_token_invalid"`.

The token is per process run. It is never written to disk, logs, `runtime.json`, or `state.json`.
The GUI has no login form and no cookie-based session. A page reload fetches a fresh copy of the
token from the relay.

Origin and host checks:

- If `Origin` is present, it MUST equal `http://<current-listen>` (no trailing slash; or the
  `https://` equivalent if a future TLS listener is added). Otherwise `403 ui_origin_invalid`.
- `Host` MUST match the current listen address or `localhost` for loopback. This blocks DNS
  rebinding.
- `Sec-Fetch-Site`, when present, MUST be `same-origin` or `none`.
- No `Access-Control-Allow-Origin` header is ever sent.
- Mutation bodies are limited to `ui.max_request_bytes` (64 KiB default). Larger bodies get 413.

Response headers:

```text
Content-Security-Policy: default-src 'self'; script-src 'self'; style-src 'self'; img-src 'self' data:; connect-src 'self'; frame-ancestors 'none'
X-Frame-Options: DENY
X-Content-Type-Options: nosniff
Referrer-Policy: no-referrer
Cache-Control: no-store
```

## `GET /_shunt/api/overview`

```json
{
  "schema": 1,
  "relay": {
    "version": "0.1.0",
    "pid": 12345,
    "uptime_s": 3600,
    "listen": "127.0.0.1:8787",
    "is_loopback": true,
    "allow_non_loopback": false,
    "interfaces": [
      { "name": "Loopback", "address": "127.0.0.1", "loopback": true },
      { "name": "Ethernet", "address": "192.168.1.20", "loopback": false }
    ]
  },
  "mode": "manual",
  "selected": "remote",
  "preferred": null,
  "effective": "remote",
  "inflight": 3,
  "last_switch": {
    "at": "2026-11-11T12:30:00Z",
    "from": "local",
    "to": "remote",
    "reason": "operator",
    "by": "gui"
  },
  "profiles": [
    {
      "id": "local",
      "label": "Local proxy",
      "scheme": "http",
      "host": "127.0.0.1",
      "port": 4000,
      "base_url": "http://127.0.0.1:4000",
      "enabled": true,
      "health": "healthy",
      "latency_ms": 2,
      "last_error": null
    },
    {
      "id": "remote",
      "label": "Tailnet MSI",
      "scheme": "https",
      "host": "msi.tailc56568.ts.net",
      "port": 443,
      "base_url": "https://msi.tailc56568.ts.net",
      "enabled": true,
      "health": "healthy",
      "latency_ms": 84,
      "last_error": null
    }
  ]
}
```

The interface list contains only addresses assigned to local interfaces. Wildcards are omitted.
`profiles[].port` is the effective port after defaulting (80 for `http`, 443 for `https`).

## `POST /_shunt/api/entry`

Request:

```json
{
  "ip": "127.0.0.1",
  "port": 8787,
  "confirm_lan": false
}
```

Rules:

- The page MUST warn that changing the entry address requires updating the base URL of every
  configured client; upstream switches do not move the entry and need no client change.
- `ip` MUST be a loopback address or a private address in the interface list (RFC 1918,
  link-local, IPv6 ULA, or CGNAT).
- `port` MUST be 1024-65535.
- `0.0.0.0`, `::`, a public address, an address not assigned locally, and a port below 1024 are
  `422 invalid_entry`.
- A non-loopback `ip` requires `confirm_lan: true`. The confirmation modal names the interface
  and the exposure; confirmation authorizes the handler to write `relay.allow_non_loopback =
  true` with the new `listen` in one atomic configuration update after the bind succeeds and
  before the socket serves traffic. Without confirmation the request is `403 lan_not_confirmed`.
  A loopback change does not clear an existing flag; the operator clears it by editing the
  configuration.
- Apply order is bind, write configuration, swap the accept loop. A bind failure is `409
  bind_failed` with the OS error and the old listen address, which remains active. A
  configuration-write failure closes the new socket, keeps the old listener, and returns `500
  config_write_failed`.
- On success the config is written atomically, the accept loop moves to the new generation, and
  the response is `200`:

```json
{
  "schema": 1,
  "listen": "127.0.0.1:8788",
  "url": "http://127.0.0.1:8788/_shunt/ui",
  "generation": 2,
  "rebound": true
}
```

The page navigates to `url` after a short delay. The old listener stops accepting immediately and
its idle keep-alive connections are closed; requests already streaming are allowed to finish
without a rebind deadline. `shutdown_grace_ms` bounds process shutdown only, not a rebind.

## `POST /_shunt/api/profiles/{id}/endpoint`

Request:

```json
{
  "scheme": "https",
  "host": "msi.tailc56568.ts.net",
  "port": 443
}
```

Rules:

- `id` MUST name an existing profile.
- `scheme` is `http` or `https`. `port` is 1-65535; omitted means 80/443 by scheme.
- `host` is a DNS name, IPv4 literal, or bracketed IPv6 literal. No userinfo, path, query, or
  fragment.
- `https` requires `tls_verify = true` (always enforced).
- The new origin is probed before acceptance. The probe is an unauthenticated `GET {base_url}/`
  with the profile probe timeout; any HTTP status below 500 counts as reachable, so an
  authenticated upstream that returns 401 or 403 is not rejected. A transport, DNS, TLS, timeout,
  or 5xx failure is `422 probe_failed` with the cause; the old endpoint remains active.
- On success the normalized `base_url` is written to `config.toml`, the profile table is
  published to new requests, and the response is `200`. A configuration-write failure returns
  `500 config_write_failed` and keeps the old endpoint:

```json
{
  "schema": 1,
  "profile": "remote",
  "base_url": "https://msi.tailc56568.ts.net",
  "probe": { "ok": true, "latency_ms": 84, "status": 200 },
  "applied": true
}
```

In-flight requests keep the old endpoint; only requests that start after the publish use the new
one. If the edited profile is effective, `runtime.json` records the new endpoint and the change
reason.

## `POST /_shunt/api/switch`

```json
{ "profile": "local", "reason": "gui" }
```

Validates the profile, optionally probes it (the GUI offers a "probe first" toggle, default on),
writes `state.json` with `mode = manual`, and returns the overview fragment. A failed probe is
`422 probe_failed` unless `force: true` is sent. Switching never restarts clients and never
affects in-flight requests.

## `POST /_shunt/api/mode`

```json
{ "mode": "auto", "preferred": "local" }
```

`mode` is `manual` or `auto`. In `auto`, `preferred` is required and must name an enabled
profile. The response is the updated overview fragment.

## `GET /_shunt/api/models`

Query parameters: `dialect=anthropic|openai`, `refresh=true|false`, `via=active|<profile>`.

Returns the normalized catalog from `contracts/model-catalog.md`. The default is the effective
profile and its preferred dialect. The relay discovers with a token resolved from its own
environment (`SHUNT_TOKEN`, then the client key variables); it never asks the GUI for a secret and
never borrows the credential of an unrelated in-flight request. If no token is available the
response is `409 credential_unavailable` and the message names the environment variable to set.
An upstream 401/403 is `502 upstream_auth_failed`; a stale cache is still returned with
`"stale": true` when one exists.

## `GET /_shunt/api/clients`

Returns the three supported clients and their configured destinations. The relay origin is the
stable base URL every generated configuration receives.

```json
{
  "schema": 1,
  "relay": "http://127.0.0.1:8787",
  "backup_dir": "C:\\Users\\julie\\AppData\\Local\\shunt\\backups",
  "clients": [
    {
      "id": "claude",
      "label": "Claude Code",
      "target": "C:\\Users\\julie\\.claude\\settings.json",
      "base_url_mode": "origin",
      "write_supported": true,
      "dialects": ["anthropic"]
    },
    {
      "id": "codex",
      "label": "Codex",
      "target": "C:\\Users\\julie\\.codex\\config.toml",
      "base_url_mode": "v1",
      "write_supported": true,
      "dialects": ["openai"]
    },
    {
      "id": "dsh",
      "label": "DeepSeek Harness",
      "target": "C:\\Users\\julie\\.dsh",
      "base_url_mode": "origin",
      "write_supported": false,
      "dialects": ["anthropic", "openai"]
    }
  ]
}
```

The paths come from `[clients.*]` in `config.toml`; they are never accepted from the request.
DSH reports `write_supported: false` because v1 produces a merge-ready fragment only.

## `POST /_shunt/api/clients/preview`

Request:

```json
{
  "client": "claude",
  "model": "free-a",
  "opus": "",
  "sonnet": "",
  "haiku": "",
  "fable": "",
  "legacy": false
}
```

`client` is `claude`, `codex`, or `dsh`. `codex` accepts `force`; `dsh` accepts
`api` (`anthropic`, `openai`, or `both`) plus `anthropic_model` and `openai_model`. An empty
model field selects the first free model from the cached catalog for the effective profile. If
no catalog is cached the response is `409 model_catalog_missing`; if the catalog has no free
model it is `422 no_free_model` and the message lists the available ids. An explicit paid model
is accepted with a warning, and an explicit id that is absent from the cached catalog is
accepted with a warning so operators can use a model the upstream added before the cache
refreshes.

The reply contains the non-secret generated fragment, the Claude Code shell snippet when
applicable, the resolved target, and the keys a write would change:

```json
{
  "schema": 1,
  "client": "claude",
  "label": "Claude Code",
  "target": "C:\\Users\\julie\\.claude\\settings.json",
  "content": "{\n  \"env\": {\n    \"ANTHROPIC_BASE_URL\": \"http://127.0.0.1:8787\",\n    \"ANTHROPIC_MODEL\": \"free-a\"\n  }\n}\n",
  "snippet": "$env:ANTHROPIC_BASE_URL = 'http://127.0.0.1:8787'\n...",
  "changed": ["ANTHROPIC_BASE_URL", "ANTHROPIC_MODEL"],
  "warnings": [],
  "write_supported": true,
  "backup_dir": "C:\\Users\\julie\\AppData\\Local\\shunt\\backups",
  "note": "Keep your existing ANTHROPIC_API_KEY..."
}
```

`content` contains only generated keys. Unrelated values from an existing client file, including
an existing API key, are never returned to the page.

## `POST /_shunt/api/clients/write`

The body is the same as preview. Claude Code merges into the existing `settings.json`; Codex
merges into `config.toml` with the comment-preserving editor. Both preserve unrelated keys and
comments, create a timestamped backup under `backup_dir`, and replace the file atomically. The
response is:

```json
{
  "schema": 1,
  "client": "claude",
  "target": "C:\\Users\\julie\\.claude\\settings.json",
  "backup": "C:\\Users\\julie\\AppData\\Local\\shunt\\backups\\claude\\settings.json.20261111T183000.000000000Z.bak",
  "changed": ["ANTHROPIC_BASE_URL", "ANTHROPIC_MODEL"],
  "notice": "Configuration written atomically. Restart the client to load the new endpoint."
}
```

DSH writes return `501 client_write_unsupported` with the preview guidance. A merge failure is
`409 client_merge_failed`; an unreadable target is `409 client_target_unreadable`; a write
failure is `409 client_write_failed`. No credential value is accepted, generated, or written.

## `GET /_shunt/api/clients/backups?client=claude`

Lists the regular files in `<backup_dir>/<client>`, newest first. The target again comes from
`config.toml`. DSH returns `501 client_restore_unsupported` because it is preview only.

```json
{
  "schema": 1,
  "client": "claude",
  "label": "Claude Code",
  "target": "C:\\Users\\julie\\.claude\\settings.json",
  "backup_dir": "C:\\Users\\julie\\AppData\\Local\\shunt\\backups",
  "write_supported": true,
  "backups": [
    {
      "name": "settings.json.20261111T183000.000000000Z.bak",
      "size": 412,
      "modified": "2026-11-11T18:30:00Z"
    }
  ]
}
```

Only file names are returned; the page never receives a path it can submit outside the client's
backup directory. A missing backup directory is an empty list.

## `POST /_shunt/api/clients/restore`

Request:

```json
{
  "client": "claude",
  "backup": "settings.json.20261111T183000.000000000Z.bak"
}
```

`backup` must be one of the bare names returned by the listing. Any separator, absolute path,
`.` or `..` is rejected with `400 backup_invalid`; so is a missing, non-regular, or symbolic-link
entry. The handler calls `clients.Restore`, which validates the name again, copies the backup
bytes over the configured target atomically, and first creates a timestamped safety backup of
the current target.

```json
{
  "schema": 1,
  "client": "claude",
  "target": "C:\\Users\\julie\\.claude\\settings.json",
  "restored": "settings.json.20261111T183000.000000000Z.bak",
  "backup": "C:\\Users\\julie\\AppData\\Local\\shunt\\backups\\claude\\settings.json.20261111T190000.000000000Z.bak",
  "notice": "Configuration restored atomically. Restart the client to load the previous endpoint."
}
```

DSH returns `501 client_restore_unsupported`. The restore route requires `X-Shunt-UI-Token`; the
listing is read-only like `GET /_shunt/api/clients`.

## Error Shape

```json
{
  "schema": 1,
  "error": {
    "code": "bind_failed",
    "message": "address already in use",
    "field": "port",
    "current": "127.0.0.1:8787"
  }
}
```

Stable codes: `ui_token_invalid`, `ui_origin_invalid`, `invalid_entry`, `lan_not_confirmed`,
`bind_failed`, `config_write_failed`, `invalid_endpoint`, `probe_failed`, `profile_not_found`,
`profile_disabled`, `invalid_mode`, `payload_too_large`, `credential_unavailable`,
`upstream_auth_failed`, `upstream_unreachable`, `upstream_timeout`, `model_catalog_missing`,
`no_free_model`, `invalid_client`, `invalid_api`, `client_merge_failed`,
`client_target_unreadable`, `client_write_failed`, `client_write_unsupported`.

Errors never include credentials or full request headers.

## Static Assets

`index.html`, `app.js`, `i18n.js`, `theme.js`, `style.css`, and `favicon.svg` are embedded with
`embed.FS`. The page uses no external fonts, scripts, images, or analytics. It renders without
network access other than the relay itself. The JavaScript is vanilla ES2020, uses `fetch`, and
stores the UI token only in a module-scoped variable.

## Localization

The GUI is available in English and French. The initial language comes from
`navigator.language` (French when it starts with `fr`, English otherwise). A manual choice is
remembered in `localStorage` under `shunt-lang` and never written to configuration, state, or
cache. Static labels are marked with `data-i18n` attributes; dynamic strings go through the
`t(key, vars)` helper exported by `i18n.js`. Known API error codes (`ui_token_invalid`,
`invalid_payload`, `no_free_model`, `backup_invalid`, and so on) are mapped to translated
messages; unknown errors fall back to the server message. Switching the language re-renders
the overview, models, client cards, and backup panels in place without reloading the page.

## Security Invariants

- The GUI is reachable only where the listener is reachable. Loopback is the default; LAN
  requires explicit confirmation and the config flag. The entry listener is plain HTTP and the
  token is embedded in the served page, so LAN mode assumes a trusted network: any host that can
  reach the listener can open the GUI, obtain the token, and change configuration. The token is
  CSRF protection against foreign web origins, not user authentication. The confirmation modal
  MUST state that LAN hosts can send requests through the relay, consuming the upstream's quota,
  and can change configuration. It also notes that Windows Firewall may still block inbound
  traffic; the tool does not change firewall rules. No upstream credential is ever sent to the
  GUI.
- The GUI never displays, requests, or stores API keys, `Authorization` headers, or request
  bodies.
- The GUI never offers Funnel, wildcard binds, TLS-verification bypass, or firewall changes.
- Mutations are same-origin, token-authenticated, size-limited, and audited in the log with the
  action, old value, new value, and result. Credentials are never part of an audit entry.
