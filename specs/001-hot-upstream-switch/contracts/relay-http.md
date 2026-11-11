# Contract: Relay HTTP Interface

**Feature**: `001-hot-upstream-switch`
**Date**: 2026-11-11

## Listener

- Address: `127.0.0.1:8787` by default. Configurable to another loopback address or a specific
  local-interface address, the latter only with `allow_non_loopback = true` and an explicit GUI
  confirmation.
- Protocol: HTTP/1.1. Keep-alive enabled. `TCP_NODELAY` enabled.
- No TLS on the client-facing leg. The default client is on the same machine; LAN mode is plain
  HTTP over a trusted network and is not a substitute for transport security.
- The listener MUST reject wildcard addresses and any non-loopback address that is not assigned
  to a local interface. A non-loopback bind requires the config flag and a recorded confirmation.
- The relay is not a forward proxy: `CONNECT` returns `405 Method Not Allowed`.

## Control Endpoints

These endpoints are reserved and are never proxied. The status endpoints are read-only; the GUI
and its API are described in `contracts/ui.md`. The UI consumes the same status data.

### `GET /_shunt/status`

Returns 200 with the current runtime snapshot. The schema is:

```json
{
  "schema": 1,
  "version": "0.1.0",
  "pid": 12345,
  "started_at": "2026-11-11T09:00:00Z",
  "uptime_s": 321,
  "listen": "127.0.0.1:8787",
  "mode": "manual",
  "selected": "local",
  "preferred": "local",
  "effective": "local",
  "last_good": "local",
  "inflight": 1,
  "requests_total": 345,
  "errors_total": 2,
  "switches_total": 12,
  "last_switch": {
    "at": "2026-11-11T09:04:00Z",
    "from": "local",
    "to": "remote",
    "reason": "operator",
    "by": "cli"
  },
  "profiles": {
    "local": {
      "base_url": "http://127.0.0.1:4000",
      "enabled": true,
      "healthy": true,
      "last_probe_at": "2026-11-11T09:05:00Z",
      "last_probe_ms": 3,
      "last_status": 200,
      "consecutive_failures": 0,
      "consecutive_successes": 7,
      "last_error": null
    },
    "remote": {
      "base_url": "https://msi.tailc56568.ts.net",
      "enabled": true,
      "healthy": true,
      "last_probe_at": "2026-11-11T09:05:00Z",
      "last_probe_ms": 45,
      "last_status": 200,
      "consecutive_failures": 0,
      "consecutive_successes": 7,
      "last_error": null
    }
  }
}
```

`last_switch` may be `null`. `selected` and `preferred` may be `null` when not applicable.
`healthy` may be `null` before the first probe. No header, credential, or body content appears
in this document.

### `GET /_shunt/healthz`

Returns 200 with `{"ok": true}` when the process is alive. It does not check upstreams.

### `GET /_shunt/readyz`

Returns 200 with `{"ready": true}` when at least one enabled profile is healthy. Returns 503
with `{"ready": false, "reason": "no_healthy_upstream"}` otherwise.

### `GET /_shunt/version`

Returns 200 with:

```json
{"version": "0.1.0", "schema": 1}
```

## Proxied Requests

Every method and path other than the reserved control endpoints is proxied.

### Request line

- Method: preserved exactly.
- Path: preserved exactly, including trailing slash and percent-encoding.
- Query: preserved exactly, including order and encoding.
- HTTP version: HTTP/1.1.
- Absolute-form request targets are normalized to origin-form; the authority in the target is
  not used for routing.

### URI construction

The upstream URI is the profile `base_url` origin plus the request path and query:

- `base_url` is validated as an origin with no path, query, or fragment.
- The request path is appended as-is, including its leading `/`.
- The relay does not add, remove, or rewrite `/v1`. A client that is configured with the wrong
  base URL gets the resulting path; the relay is transparent.

Examples:

| Profile base URL | Request path | Upstream URL |
|------------------|--------------|--------------|
| `http://127.0.0.1:4000` | `/v1/messages` | `http://127.0.0.1:4000/v1/messages` |
| `https://msi.tailc56568.ts.net` | `/v1/models` | `https://msi.tailc56568.ts.net/v1/models` |

### Request headers

Preserved end-to-end, including `Authorization`, `x-api-key`, `anthropic-version`,
`anthropic-beta`, `OpenAI-*`, `Content-Type`, `Accept`, `Accept-Encoding`, `User-Agent`, and
`Content-Length` when present.

Removed (hop-by-hop or transport-specific):

- `Connection`, and every header named by its comma-separated value.
- `Keep-Alive`
- `Proxy-Authenticate`, `Proxy-Authorization`
- `TE`
- `Trailer`
- `Transfer-Encoding`
- `Upgrade`
- `Expect`
- `Host` (replaced with the upstream authority)

No header is added to the upstream request by default. `httputil.ReverseProxy` normally adds
`X-Forwarded-For`; the relay suppresses it by setting that header map entry to nil. With
`trace_headers = true`, the relay adds `X-Shunt-Request-Id` and `X-Shunt-Profile` to the upstream
request as well.

### Request body

Forwarded as a stream. The relay does not collect, inspect, decompress, or rewrite it. There is
no body size limit imposed by the relay. The upstream's limits apply.

### Response headers

Preserved end-to-end, except hop-by-hop headers. The relay removes `Connection`, `Keep-Alive`,
`Proxy-Authenticate`, `Proxy-Authorization`, `TE`, `Trailer`, `Transfer-Encoding`, and `Upgrade`.
It preserves `Content-Type`, `Content-Length`, `Cache-Control`, `Retry-After`, `x-request-id`,
`anthropic-*`, and `OpenAI-*`.

The relay adds:

- `X-Shunt-Request-Id: <request id>`
- `X-Shunt-Profile: <profile id>`

### Response body

Forwarded as a stream, frame by frame, with a flush after each frame. SSE `data:` chunks are not
re-framed, reordered, or delayed. The relay does not append a terminator. If the upstream closes
mid-stream, the relay closes the client connection without fabricating an end event.

### Status codes

- Upstream status codes pass through unchanged, including 400, 401, 403, 404, 409, 422, 429, and
  5xx.
- Relay-generated errors are 502 (upstream unavailable), 503 (overloaded or no healthy
  upstream), and 504 (upstream timeout).

## Relay Error Shape

When the relay cannot obtain response headers, it returns JSON:

```json
{
  "type": "error",
  "error": {
    "type": "shunt_upstream_unavailable",
    "message": "Upstream 'remote' is unavailable: TLS handshake failed",
    "code": "shunt_upstream_unavailable",
    "profile": "remote"
  },
  "request_id": "shunt-1762850000000-42"
}
```

Rules:

- `type` is always `error` at the top level.
- `error.type` and `error.code` are stable identifiers: `shunt_upstream_unavailable`,
  `shunt_upstream_timeout`, `shunt_overloaded`, `shunt_no_healthy_upstream`.
- `error.message` is a short, sanitized description; it never contains headers, bodies, or
  credentials.
- `error.profile` is the selected profile id.
- `request_id` matches the `X-Shunt-Request-Id` response header.
- `Content-Type: application/json; charset=utf-8`.
- The shape is readable by both Anthropic-style (`error.type`, `error.message`) and OpenAI-style
  (`error.message`, `error.type`) clients.

## Timeouts and Limits

| Setting | Default | Behavior |
|---------|---------|----------|
| Connect timeout (local) | 500 ms | Returns 502 on expiry. |
| Connect timeout (tailnet) | 2000 ms | Returns 502 on expiry. |
| Response-header timeout | 120000 ms | Returns 504 on expiry. |
| Body idle timeout | 0 (disabled) | Streams may be idle indefinitely. |
| Total request timeout | 0 (disabled) | Long reasoning is allowed. |
| Max in-flight | 64 | Returns 503 `shunt_overloaded` when exceeded. |
| Max header size | 1 MiB (`http.DefaultMaxHeaderBytes`) | Malformed/oversized headers are rejected by `net/http`. |

No timeout is applied to individual body frames. A client that disconnects is detected by the
server body/connection and cancels the upstream request.

## Retry and Failover Rules

- No retry after any request body byte has been forwarded.
- No retry after any response header has been received.
- No retry of a request that received an upstream 4xx/5xx response.
- Manual mode never routes a request to a profile other than the selected one.
- Automatic mode chooses the effective profile before the request starts. If that profile fails
  before response headers, the request returns 502/504; the failure counts toward the health
  threshold, and the next request may use the fallback after the threshold is reached.
- GET/HEAD requests are not retried either, for simplicity and predictability. The operator can
  retry manually.

## Client Base-URL Rules

The relay is path-transparent, so each client must use the base URL its API expects:

| Client | Base URL | Client appends |
|--------|----------|----------------|
| Claude Code (`ANTHROPIC_BASE_URL`) | `http://127.0.0.1:8787` | `/v1/messages` |
| Anthropic SDK | `http://127.0.0.1:8787` | `/v1/messages` |
| Codex (`base_url`) | `http://127.0.0.1:8787/v1` | `/responses` |
| OpenAI SDK (`base_url`) | `http://127.0.0.1:8787/v1` | `/chat/completions` or `/responses` |
| DSH `anthropic-messages` (`baseURL`) | `http://127.0.0.1:8787` | `/v1/messages` |
| DSH `openai-responses` (`baseURL`) | `http://127.0.0.1:8787/v1` | `/responses` |
| DSH `openai-completions` (`baseURL`) | `http://127.0.0.1:8787/v1` | `/chat/completions` |

A base URL with an extra `/v1` produces a doubled path; the relay does not correct it.

## Examples

### List models through the relay

```http
GET /v1/models HTTP/1.1
Host: 127.0.0.1:8787
x-api-key: <token>
anthropic-version: 2023-06-01
```

The relay forwards the same path and headers to the effective profile and streams the response.

### Streaming messages

```http
POST /v1/messages HTTP/1.1
Host: 127.0.0.1:8787
x-api-key: <token>
content-type: application/json
accept: text/event-stream
```

The response is streamed. A switch performed while this request is active does not change its
upstream.

### Status

```http
GET /_shunt/status HTTP/1.1
Host: 127.0.0.1:8787
```

Returns the runtime snapshot. No credentials are required because the listener is loopback-only
and the document contains no secrets.

## Security Requirements

- The listener MUST default to loopback. A non-loopback bind requires a specific local-interface
  address, `allow_non_loopback = true`, and an explicit confirmation.
- The relay MUST NOT accept a public bind even if the configuration requests one.
- The relay MUST NOT enable Tailscale Funnel, modify firewall rules, or disable upstream auth.
- The relay MUST NOT store or log credentials, authorization headers, or bodies.
- TLS verification for HTTPS profiles MUST be enabled; there is no insecure mode.
- The control endpoints MUST be read-only.
- `CONNECT` and `Upgrade` are not supported; the relay returns 405 or removes the upgrade.
