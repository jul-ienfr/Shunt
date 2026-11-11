# Contract: Configuration, State, Runtime, and Cache Files

**Feature**: `001-hot-upstream-switch`
**Date**: 2026-11-11

## Paths

| Item | Windows default | Override |
|------|-----------------|----------|
| Config | `%APPDATA%\shunt\config.toml` | `--config`, `SHUNT_CONFIG` |
| Intent | `%LOCALAPPDATA%\shunt\state.json` | `state_path` in config |
| Runtime | `%LOCALAPPDATA%\shunt\runtime.json` | `runtime_path` in config |
| Cache | `%LOCALAPPDATA%\shunt\cache\` | `cache_dir` in config |
| Logs | `%LOCALAPPDATA%\shunt\logs\` | `log_dir` in config |
| Backups | `%LOCALAPPDATA%\shunt\backups\` | `backup_dir` in config |

On Linux and macOS the equivalents are `$XDG_CONFIG_HOME/shunt/config.toml` and
`$XDG_STATE_HOME/shunt/`. Paths support `~` expansion. Relative paths are resolved against the
directory containing the configuration file.

The configuration file is read at startup and watched for external edits every
`relay.config_poll_ms`. Profile and UI changes are applied live; a listen change is applied by
binding the new socket before closing the old one. A failed external listen change is logged, the
old listener stays active, and the watcher does not retry until the file changes again. State is
re-read every `state_poll_ms`.
Runtime and cache are written by the relay/CLI, never hand-edited.

## Configuration Schema (TOML)

```toml
schema = 1
default_profile = "local"

[relay]
listen = "127.0.0.1:8787"          # loopback by default; a specific local IP needs opt-in
allow_non_loopback = false         # required for any non-loopback listener
state_poll_ms = 200                # 50..2000
config_poll_ms = 1000              # 200..10000
max_inflight = 64                  # 1..1024
connect_timeout_ms = 2000          # per-profile override allowed
response_header_timeout_ms = 120000
body_idle_timeout_ms = 0           # 0 = disabled
shutdown_grace_ms = 10000          # bounds process shutdown only, not a listener rebind
tcp_keepalive_s = 30
trace_headers = false
log_level = "info"                 # error|warn|info|debug|trace
log_format = "text"                # text|json
request_id_header = "X-Shunt-Request-Id"

[switch]
on_profile = "remote"
off_profile = "local"

[auto]
enabled = false
preferred = "local"
probe_interval_ms = 10000
probe_timeout_ms = 2000
failure_threshold = 3
recovery_threshold = 2

[profiles.local]
label = "Local proxy"
base_url = "http://127.0.0.1:4000"
probe_url = "http://127.0.0.1:4000/"
probe_method = "GET"
connect_timeout_ms = 500
priority = 10
enabled = true
tags = ["local"]

[profiles.remote]
label = "Tailnet MSI"
base_url = "https://msi.tailc56568.ts.net"
probe_url = "https://msi.tailc56568.ts.net/"
probe_method = "GET"
connect_timeout_ms = 2000
tls_verify = true
priority = 20
enabled = true
tags = ["tailnet"]

[ui]
enabled = true
open_browser = false
max_request_bytes = 65536

[paths]
# Optional overrides. Empty values mean the platform defaults above.
state = ""
runtime = ""
cache_dir = ""
log_dir = ""
backup_dir = ""

[models]
cache_ttl_hours = 12
stale_ttl_hours = 168
page_size = 1000
max_pages = 10
default_dialect = "anthropic"

[clients.claude]
settings_path = "~/.claude/settings.json"
base_url_mode = "origin"

[clients.codex]
config_path = "~/.codex/config.toml"
base_url_mode = "v1"

[clients.dsh]
home = "~/.dsh"
profile = "web"
write_supported = false
```

### Validation

- Unknown top-level keys, unknown profile keys, and unknown client keys are rejected with a
  message naming the key and its location. This catches typos such as `base_uri`.
- `schema` MUST be `1`.
- `relay.listen` MUST resolve to a loopback address, or to a specific address assigned to a
  local interface when `relay.allow_non_loopback = true`. Wildcard addresses (`0.0.0.0`, `::`)
  are always a fatal configuration error. A non-loopback address that is not assigned to a local
  interface, or that is public, is a fatal configuration error; allowed non-loopback ranges are
  RFC 1918, link-local, IPv6 ULA, and CGNAT. The entry port MUST be 1024-65535; a lower port is
  rejected so the default configuration never needs elevation.
- `default_profile` MUST name an enabled profile. It is used only when no valid
  `state.json` exists.
- Profile IDs match `^[a-z][a-z0-9-]{0,31}$`, are permanent, and are case-sensitive.
- At least one enabled profile is required. `switch.on_profile` and `switch.off_profile` MUST
  name enabled profiles. `auto.preferred` MUST name an enabled profile.
- `base_url` MUST be absolute, use `http` or `https`, and contain no path, query, or fragment.
  Trailing slash is stripped.
- `probe_url`, when present, MUST be absolute and on the same origin as `base_url`.
- `tls_verify` MUST be `true`; setting it to `false` is a fatal configuration error. There is no
  bypass flag.
- `connect_timeout_ms`, `probe_timeout_ms`, and `probe_interval_ms` MUST be greater than zero.
  `body_idle_timeout_ms` MUST be `0`.
- `max_inflight` MUST be at least 1.
- `models.cache_ttl_hours` and `models.stale_ttl_hours` MUST be positive, and
  `stale_ttl_hours` MUST be greater than `cache_ttl_hours`. `models.page_size` MUST be 1-1000;
  `models.max_pages` MUST be 1-100. `models.default_dialect` MUST be `anthropic` or `openai`.
- `tcp_keepalive_s` MUST be 0-3600; 0 disables keepalive.
- `trace_headers` is a boolean. When true, diagnostic headers are added to upstream requests.
- `log_level` MUST be one of `error`, `warn`, `info`, `debug`, `trace`; `log_format` MUST be
  `text` or `json`.
- `paths.*` values, when non-empty, are absolute or `~`-expanded paths. Relative paths are
  resolved against the configuration directory. The process must be able to create and write
  each parent directory.
- Duplicate priorities are allowed; ties are broken by profile ID.
- A profile named in `[switch]` or `[auto]` that is missing or disabled is a fatal error at
  startup, not a silent fallback.

Profile endpoint changes and UI settings are applied without a restart. An external profile
endpoint change is probed before the new profile table is published; a failed probe leaves the old
endpoint active and is logged. A listen address change is applied by binding the new socket first;
a failed bind leaves the old listener running and is reported. Selection, mode, and preference
still live in `state.json`, not in the configuration. The GUI writes configuration changes
atomically and preserves comments.

## Intent State (`state.json`)

```json
{
  "schema": 1,
  "revision": 42,
  "mode": "manual",
  "selected": "remote",
  "preferred": "local",
  "updated_at": "2026-11-11T12:34:56Z",
  "updated_by": "shunt use remote",
  "reason": "operator"
}
```

| Field | Type | Rules |
|-------|------|-------|
| `schema` | integer | `1`. |
| `revision` | integer | Strictly increasing on every write; monotonic from the previous readable value. |
| `mode` | string | `manual` or `auto`. |
| `selected` | string or null | Profile ID used in manual mode. Required when `mode = manual`. |
| `preferred` | string or null | Profile ID preferred in auto mode. |
| `updated_at` | string | RFC 3339 UTC. |
| `updated_by` | string | Command or actor, at most 128 characters. |
| `reason` | string | Short human reason, at most 256 characters. |

The data plane never writes this file. The CLI and the authenticated GUI switch handler write it
atomically. The relay treats an unreadable,
invalid, or unknown-schema file as "no intent" and keeps its last-known-good selection; it logs a
warning and continues serving. It never deletes or rewrites the file. A stale `revision` that
does not advance is ignored, so a malformed file cannot cause a switch loop.

Valid transitions:

- `manual -> manual`: `selected` changes.
- `manual -> auto`: `preferred` is set.
- `auto -> manual`: `selected` is set.
- In auto mode, `selected` is informational only and is not used for routing.
- A profile referenced by the intent that no longer exists falls back to the configuration
  default with a warning. This is the only fallback, and it is reported in `status`.

## Runtime Observation (`runtime.json`)

```json
{
  "schema": 1,
  "version": "0.1.0",
  "pid": 12345,
  "started_at": "2026-11-11T12:00:00Z",
  "uptime_s": 3600,
  "listen": "127.0.0.1:8787",
  "mode": "manual",
  "selected": "remote",
  "preferred": "local",
  "effective": "remote",
  "last_good": "remote",
  "inflight": 3,
  "requests_total": 91234,
  "errors_total": 12,
  "switches_total": 7,
  "bytes_in": 123456789,
  "bytes_out": 987654321,
  "last_switch": {
    "at": "2026-11-11T12:30:00Z",
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
      "last_probe_at": "2026-11-11T12:59:50Z",
      "last_probe_ms": 2,
      "last_status": 200,
      "consecutive_failures": 0,
      "consecutive_successes": 12,
      "last_error": null
    },
    "remote": {
      "base_url": "https://msi.tailc56568.ts.net",
      "enabled": true,
      "healthy": true,
      "last_probe_at": "2026-11-11T12:59:51Z",
      "last_probe_ms": 84,
      "last_status": 200,
      "consecutive_failures": 0,
      "consecutive_successes": 5,
      "last_error": null
    }
  }
}
```

Written by the relay every 5 seconds and on every switch, atomically. It is advisory: `status`
prefers the live endpoint and shows this file only when the relay is down. Stale values carry
their timestamp so the operator can judge them.

## Model Cache

One file per `(profile, dialect)`, e.g. `cache/models.remote.anthropic.json`:

```json
{
  "schema": 1,
  "profile": "remote",
  "dialect": "anthropic",
  "source_url": "https://msi.tailc56568.ts.net/v1/models",
  "fetched_at": "2026-11-11T12:00:00Z",
  "stale_after": "2026-11-12T00:00:00Z",
  "etag": null,
  "models": []
}
```

The cache contains no credentials, headers, or request bodies. A corrupt cache is deleted and
treated as a miss. Cache files are written atomically after a successful, parseable response.

## Atomicity

Every write follows the same procedure:

1. Serialize the complete document to memory.
2. Write to a temporary file in the destination directory with a unique suffix.
3. Flush and sync the file.
4. Replace the destination with a single rename, or `ReplaceFileW` on Windows.
5. Best-effort sync of the directory on Unix.

Readers never observe a partially written file. A failed write leaves the previous file intact and
returns a non-zero exit code. Client configuration writes use the same procedure plus a timestamped
backup created before replacement.
