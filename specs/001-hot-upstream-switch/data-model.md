# Data Model: Hot-Switching Local/Tailnet Upstream Relay

**Feature**: `001-hot-upstream-switch`
**Date**: 2026-11-11

## Overview

The model separates **operator intent** (what the human asked for) from **runtime observation**
(what the relay is actually doing). The intent is durable and written only by the control plane
(CLI or authenticated GUI). The
observation is ephemeral, written by the relay, and safe to discard. This separation prevents a
feedback loop in automatic mode and keeps manual switches predictable.

```text
RelayConfig (TOML) ──> profiles
        │
OperatorIntent (state.json) ──> mode + selected/preferred
        │
        ├── manual: effective = selected
        └── auto:   effective = preferred if healthy, else healthy fallback
                              │
RuntimeObservation (runtime.json) <── relay
        │
        ├── ModelCatalog (cache JSON) ──> ClientTarget snippets
        └── status endpoint
```

## UpstreamProfile

A named destination the relay can use.

| Field | Type | Required | Default | Rules |
|-------|------|----------|---------|-------|
| `id` | string | yes | - | Lowercase, hyphenated, unique, 1-64 chars, `[a-z0-9][a-z0-9-]*`. |
| `label` | string | no | `id` | Human label, 1-80 chars. |
| `base_url` | URL | yes | - | `http` or `https`; no path, query, or fragment. The initial profiles are loopback and tailnet; additional HTTP(S) origins are allowed. |
| `probe_url` | URL | no | `base_url + "/"` | Must resolve to the same origin (scheme, host, and port) as `base_url`. |
| `probe_method` | enum | no | `GET` | `GET` or `HEAD`. |
| `priority` | integer | no | 100 | Lower is preferred as a fallback; 0-1000. |
| `enabled` | boolean | no | `true` | Disabled profiles cannot be selected or used as fallbacks. |
| `connect_timeout_ms` | integer | no | 2000 | 50-30000. Local profiles may use 500. |
| `response_header_timeout_ms` | integer | no | 120000 | 1000-600000. Applies before response headers only. |
| `tls_verify` | boolean | no | `true` | Must be `true`; `false` is a configuration error. |
| `tags` | string array | no | `[]` | Free-form labels for status/doctor. |

**Invariants**:

- At least one enabled profile exists.
- `base_url` scheme `https` requires `tls_verify = true`.
- A profile may point at a loopback address or a tailnet address; it may not point at a public
  wildcard or an unspecified address.
- The local and remote default profiles are always present in the generated configuration; the
  operator may add more.

## RelayConfig

The human-edited configuration, stored as TOML.

| Field | Type | Required | Default | Rules |
|-------|------|----------|---------|-------|
| `schema` | integer | yes | `1` | Only schema 1 is accepted in v1. |
| `relay.listen` | socket address | yes | `127.0.0.1:8787` | Loopback by default; a specific local-interface address requires `allow_non_loopback = true`; wildcard addresses rejected; port 1024-65535. |
| `relay.allow_non_loopback` | boolean | no | `false` | Must be `true` for any non-loopback listen address. Set only by an explicit GUI confirmation or a deliberate hand edit. |
| `default_profile` | profile id | yes | `local` | Must exist and be enabled; used only when no valid state exists. |
| `profiles` | table | yes | local + remote | At least one enabled profile. |
| `auto.enabled` | boolean | no | `false` | Startup mode when no valid state exists; `true` selects automatic mode. |
| `auto.probe_interval_ms` | integer | no | 10000 | 2000-300000. |
| `auto.probe_timeout_ms` | integer | no | 2000 | 250-30000. |
| `auto.failure_threshold` | integer | no | 3 | 1-20. |
| `auto.recovery_threshold` | integer | no | 2 | 1-20. |
| `relay.max_inflight` | integer | no | 64 | 1-1024. |
| `relay.body_idle_timeout_ms` | integer | no | 0 | Must be 0; a non-zero body timeout is rejected. |
| `relay.tcp_keepalive_s` | integer | no | 30 | 0-3600. |
| `relay.trace_headers` | boolean | no | `false` | Adds diagnostic headers to proxied traffic. |
| `relay.log_level` | enum | no | `info` | `error`, `warn`, `info`, `debug`, `trace`. |
| `relay.log_format` | enum | no | `text` | `text` or `json`. |
| `relay.config_poll_ms` | integer | no | 1000 | 200-10000. Watcher interval for external config edits. |
| `ui.enabled` | boolean | no | `true` | Serve `/_shunt/ui` and `/_shunt/api/*`. |
| `ui.open_browser` | boolean | no | `false` | Open the default browser on `shunt ui`. |
| `ui.max_request_bytes` | integer | no | 65536 | 1024-1048576. Bound on GUI JSON request bodies. |
| `paths.state` | path | no | platform default | Empty means default; relative paths resolve against the config directory. |
| `paths.runtime` | path | no | platform default | Same rules. |
| `paths.cache_dir` | path | no | platform default | Same rules. |
| `paths.log_dir` | path | no | platform default | Same rules. |
| `paths.backup_dir` | path | no | platform default | Same rules. |
| `models.cache_ttl_hours` | integer | no | 12 | 1-168. |
| `models.stale_ttl_hours` | integer | no | 168 | Greater than `cache_ttl_hours`. |
| `models.page_size` | integer | no | 1000 | 1-1000. |
| `models.max_pages` | integer | no | 10 | 1-100. |
| `models.default_dialect` | enum | no | `anthropic` | `anthropic` or `openai`. |
| `switch.on_profile` | profile id | no | `remote` | Must exist and be enabled. |
| `switch.off_profile` | profile id | no | `local` | Must exist and be enabled. |

**Validation**:

- The file must parse as TOML; unknown keys are rejected to catch typos.
- All profile references resolve to enabled profiles.
- `listen` parses as `SocketAddr`. Loopback is accepted by default. Any other address must be
  assigned to a local interface and `allow_non_loopback` must be `true`; wildcard addresses are
  always rejected.
- `body_idle_timeout_ms` must be 0; the relay does not support body timeouts.
- `tls_verify` must be true for every HTTPS profile.
- The config file is read at startup and watched for external edits. Profile and UI changes are
  applied live; an external profile endpoint change is probed before publication, and a failed
  probe leaves the old endpoint active. A listen change is applied by binding the new socket
  before closing the old one. An invalid edit is ignored and the last good configuration remains
  active. Switching does not require a config reload.

## ListenerBinding

The live entry endpoint, derived from `relay.listen` and the bind supervisor.

| Field | Type | Notes |
|-------|------|-------|
| `ip` | IP address | A loopback or specific private local-interface address (RFC 1918, link-local, ULA, or CGNAT). Never `0.0.0.0`, `::`, or a public address. An IPv6 link-local bind uses the selected interface as its zone. |
| `port` | integer | 1024-65535. |
| `is_loopback` | boolean | Derived from `ip`. |
| `interface` | string or null | Interface name when the address belongs to a NIC. |
| `allow_non_loopback` | boolean | Mirrors the config flag at the time of bind. |
| `confirmed_at` | timestamp or null | When the operator confirmed a LAN bind in the GUI. |
| `generation` | integer | Increments on every successful rebind. |

**Rules**:

- A new binding is attempted before the old listener is closed. A failure leaves the old binding
  active and returns the bind error.
- Wildcard and public addresses are rejected before the bind attempt. Non-loopback addresses
  must be private (RFC 1918, link-local, IPv6 ULA, or CGNAT).
- A non-loopback bind requires a recorded confirmation and a durable
  `allow_non_loopback = true`. The socket is attempted after confirmation; the flag and the new
  `listen` are written atomically before it serves traffic, and a socket that fails that write is
  closed.
- The old listener stops accepting immediately and closes idle keep-alive connections; active
  requests finish without a rebind deadline. New connections use the new generation.
  `shutdown_grace_ms` bounds process shutdown only.

## OperatorIntent (`state.json`)

The durable record of what the operator asked for. Written by the control plane (CLI or the
authenticated GUI) with an atomic replace. The data plane only reads it.

| Field | Type | Required | Rules |
|-------|------|----------|-------|
| `schema` | integer | yes | `1`. |
| `revision` | integer | yes | Monotonically increasing; starts at 1; used to detect changes. |
| `mode` | enum | yes | `manual` or `auto`. |
| `selected` | profile id | conditional | Required in manual mode; must exist and be enabled. |
| `preferred` | profile id | conditional | Required in auto mode; must exist and be enabled. |
| `updated_at` | RFC 3339 string | yes | UTC. |
| `updated_by` | string | yes | `cli`, `gui`, `init`, or `migration`; 1-32 chars. |
| `reason` | string | no | 0-200 chars; sanitized to one line. |

**Invariants**:

- `revision` increases on every write. A same-revision write with a different body is ignored.
- The data plane never writes this file; the CLI and the authenticated GUI switch handler do.
- A missing, corrupt, or unknown-profile state is ignored; the relay keeps its current selection
  and logs a warning. At startup it falls back to the config default and manual mode.
- A manual `use` sets `mode = manual` and `selected = <profile>`; `preferred` may be retained for
  later.
- `mode auto --prefer <profile>` sets `mode = auto` and `preferred = <profile>`; `selected` is
  retained as the last manual choice but is not used while auto is active.

**State transitions**:

```text
                 shunt use <p>                 shunt mode auto --prefer <p>
   [manual: p] ─────────────────> [manual: p] ────────────────────────> [auto: prefer p]
        ^                              ^                                      │
        │                              │                                      │ probe failures
        │ shunt use <p>                 │ shunt use <p>                         v
        └──────────────────────────────┴──────────────────────────── [auto: fallback q]
                                                                              │
                                                          probe recovery      │
                                                                              v
                                                                     [auto: prefer p]
```

A manual `use` always wins and switches the mode back to manual. Automatic movement never
rewrites the intent file; it changes the effective profile in runtime observation only.

## RuntimeObservation (`runtime.json`)

The relay's current view. Written by the relay at most every five seconds and on every change;
read by `shunt status` when the relay is not running. It is not an input to the relay.

| Field | Type | Rules |
|-------|------|-------|
| `schema` | integer | `1`. |
| `version` | string | Binary version. |
| `pid` | integer | Relay process id. |
| `started_at` | RFC 3339 string | UTC. |
| `uptime_s` | integer | Seconds since start. |
| `listen` | socket address | Actual bound address. |
| `mode` | enum | `manual` or `auto`. |
| `selected` | profile id or null | Last manual selection. |
| `preferred` | profile id or null | Auto preference. |
| `effective` | profile id | Current routing target. |
| `last_good` | profile id or null | Last profile that served a request or passed a probe. |
| `inflight` | integer | Current in-flight requests. |
| `requests_total` | integer | Monotonic. |
| `errors_total` | integer | Monotonic; relay-generated errors only. |
| `switches_total` | integer | Monotonic; effective-profile changes. |
| `bytes_in` | integer | Monotonic; request body bytes forwarded. |
| `bytes_out` | integer | Monotonic; response body bytes forwarded. |
| `last_switch` | object or null | `at`, `from`, `to`, `reason`, `by`. |
| `profiles` | map | Per-profile health, see below. |

Per-profile health:

| Field | Type | Meaning |
|-------|------|---------|
| `base_url` | string | Configured URL. |
| `enabled` | boolean | Config flag. |
| `healthy` | boolean or null | `null` until the first probe. |
| `last_probe_at` | RFC 3339 string or null | Last probe time. |
| `last_probe_ms` | integer or null | Probe duration. |
| `last_status` | integer or null | HTTP status, if any. |
| `consecutive_failures` | integer | Reset on success. |
| `consecutive_successes` | integer | Reset on failure. |
| `last_error` | string or null | Sanitized error class, no secrets. |

**Invariants**:

- `effective` is always an enabled profile if any enabled profile exists.
- Counters never decrease during a process lifetime.
- `last_error` contains a class and a short message, never headers or bodies.
- The file is atomic; a reader sees either the previous or the new snapshot.

## ModelRecord

A normalized model entry from either dialect.

| Field | Type | Required | Notes |
|-------|------|----------|-------|
| `id` | string | yes | Exact identifier used by clients; 1-256 chars. |
| `display_name` | string | no | Falls back to `id`. |
| `provider` | string | no | Upstream/provider label if supplied. |
| `created_at` | RFC 3339 string or integer | no | Normalized to RFC 3339 when possible. |
| `context_window` | integer | no | From `max_input_tokens`, `context_window`, `context_length`, or `input_tokens`. |
| `max_output_tokens` | integer | no | From `max_tokens`, `max_output_tokens`, or `output_tokens`. |
| `capabilities` | object | no | Tools, vision, reasoning, etc.; keys preserved. |
| `input_modalities` | string array | no | `text`, `image`, `audio`, `video`; default empty/unknown. |
| `reasoning_efforts` | string array | no | Ordered levels if supplied. |
| `lifecycle` | string | no | `stable`, `preview`, `deprecated`, etc. |
| `cost` | object | no | Input/output/cache prices if supplied. |
| `free` | enum | yes | `free`, `paid`, or `unknown`. |
| `raw` | object | yes | Original record for traceability. |

**Free classification**:

1. Explicit `free: true` or a `tier` containing `free` -> `free`.
2. Explicit `free: false` or a `tier` containing `paid`, `pro`, or `premium` -> `paid`.
3. A cost object with all zero input/output prices -> `free`; any positive price -> `paid`.
4. Otherwise -> `unknown`.

The default model is the first `free` record in upstream order. `unknown` and `paid` are never
default-selected. An explicit `--model` may select any record; a paid choice prints a warning.

## ModelCatalog

A cached snapshot for one profile and dialect.

| Field | Type | Rules |
|-------|------|-------|
| `schema` | integer | `1`. |
| `profile` | profile id | Matches the profile used. |
| `dialect` | enum | `anthropic` or `openai`. |
| `source` | enum | `relay` or `direct`. |
| `fetched_at` | RFC 3339 string | UTC. |
| `stale_after` | RFC 3339 string | `fetched_at + ttl`. |
| `stale` | boolean | True when the cache is past `stale_after`. |
| `stale_reason` | string or null | Why a stale cache was used. |
| `models` | ModelRecord array | At least one; upstream order, each labeled free/paid/unknown. |

**Invariants**:

- Cache key is `(profile, dialect)`.
- A cache with `schema != 1` is discarded.
- A cache with zero models is not written.
- Stale data is never presented as fresh; the output includes the fetch time and the reason.
- The raw records are retained; normalized fields are derived, not destructive.

## ClientTarget

A supported client and its generated configuration.

| Field | Type | Notes |
|-------|------|-------|
| `id` | enum | `claude`, `codex`, `dsh`. |
| `label` | string | Human name. |
| `path` | path | Default destination; overridable. |
| `format` | enum | `shell`, `json`, `toml`, `yaml-fragment`. |
| `protocol` | enum | `anthropic-messages`, `openai-responses`, `openai-completions`, or `both`. |
| `base_url_rule` | string | Claude/DSH Anthropic use the origin; Codex/DSH OpenAI use `/v1`. |
| `aliases` | map | Claude Code model aliases; empty for others. |
| `write_supported` | boolean | False for DSH in v1. |

## RequestContext

A transient record for one proxied request. It is not persisted.

| Field | Type | Notes |
|-------|------|-------|
| `request_id` | string | `shunt-<epoch_ms>-<counter>`; 1-64 chars. |
| `profile` | profile id | Snapshot at request start. |
| `method` | string | HTTP method. |
| `path` | string | Path and query, no body. |
| `started_at` | instant | Monotonic. |
| `streaming` | boolean | True when the response is chunked/SSE. |
| `status` | integer or null | Upstream status if headers arrived. |
| `bytes_in` | integer | Request body bytes forwarded. |
| `bytes_out` | integer | Response body bytes forwarded. |
| `outcome` | enum | `ok`, `upstream_error`, `client_error`, `client_disconnect`, `timeout`, `overloaded`. |

## Invariants and Validation Summary

- The listener is loopback by default; a non-loopback bind requires a specific local-interface
  address, `allow_non_loopback = true`, and an operator confirmation. Wildcards are rejected.
- The GUI session token is per process run and is never written to disk or logs.
- A request is bound to exactly one profile snapshot; no mid-request switch.
- Manual mode never changes the effective profile without a new intent revision.
- Auto mode changes the effective profile only through the documented thresholds.
- No credential value is stored in any durable file.
- Every durable write is atomic and schema-versioned.
- `state.json` and `runtime.json` are separate files with separate writers.
- A malformed cache or runtime file never blocks startup or serving.
