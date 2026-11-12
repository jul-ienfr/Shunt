# Implementation Plan: Hot-Switching Local/Tailnet Upstream Relay

**Branch**: `001-hot-upstream-switch` | **Date**: 2026-11-11 | **Spec**: [spec.md](spec.md)

**Input**: Feature specification from `specs/001-hot-upstream-switch/spec.md`

## Summary

Build `shunt`, a single-binary local relay that gives Claude Code, Codex, and DeepSeek Harness one
stable client endpoint while the operator switches the upstream between a local proxy and a
tailnet host. The relay reads a small operator-intent file, applies a switch to the next request,
pins in-flight streams to their original upstream, and exposes a read-only status surface plus
an authenticated embedded GUI for endpoint edits. A second subsystem discovers models from the
active upstream and generates client configuration in the Claude Code, Codex, and DeepSeek Harness
formats, free-first and with no stored credentials. A health-aware automatic mode adds failover
without mid-request retries. An embedded local web GUI edits the entry IP/port and each profile's
upstream host/port, applying changes live, with loopback as the default and an explicit
confirmation before any LAN bind. Entry-address edits are client-visible reconfigurations and are
labeled as such; upstream switches never move the entry.

This plan stops at design. No production code is generated in this phase. The next phase is
`/speckit-tasks`, followed by implementation.

## Technical Context

**Language/Version**: Go 1.23 or newer, built with `CGO_ENABLED=0`. The implementation phase
must confirm `go version` on the target workstation and install Go if it is absent. No C
toolchain is required for release builds; only the optional race-detector CI job uses cgo.

**Primary Dependencies**: the Go standard library covers the whole data and control plane:
`net/http` and `net/http/httputil` (server, client, reverse proxy), `crypto/tls` (tailnet
verification with system roots), `crypto/rand` (GUI token), `embed` and `html/template` (GUI
assets and page), `encoding/json` (state, runtime, cache, model records), `flag` (CLI grammar),
`context`, `sync/atomic`, `net` (interface enumeration), `log/slog` (structured logs), and
`os`/`path/filepath`/`time`. The only external module is
`github.com/pelletier/go-toml/v2` for TOML decoding. Codex `config.toml` writes use a small
targeted line editor that preserves comments and unrelated tables; the same editor handles GUI
edits to the shunt configuration. No second TOML library is added. The GUI is plain
HTML/CSS/JavaScript embedded with `embed.FS`; no web framework, third-party template engine, or
CDN is added. `schtasks` is invoked for login startup (no package).

**Storage**: Human-edited TOML configuration; JSON operator-intent state; JSON runtime
observation; JSON model caches; append-only text/JSON logs. No database. Atomic replace for every
file the tool writes. Client configuration writes are opt-in and always backed up.

**Testing**: `go test ./...` for unit and contract tests on the target; `go test -race ./...`
for the concurrency-sensitive packages on a Linux CI runner, because the race detector requires
cgo while release builds remain `CGO_ENABLED=0`; a fake upstream built with
`net/http/httptest` for deterministic streaming, delay, disconnect, and error injection;
integration tests against the real local proxy and the real tailnet host; a mandatory hot-switch
test with a live stream; a 24-hour soak test. `go vet ./...` and `gofmt -l` are release gates.

**Target Platform**: Windows 10/11 x64 primary; Linux and macOS secondary. Client-facing leg is
HTTP/1.1 on `127.0.0.1:8787` by default. The listen address is configurable from the GUI among
the addresses actually assigned to local interfaces; a non-loopback address requires explicit
confirmation and `relay.allow_non_loopback = true`. Wildcard addresses are rejected. Upstream
local leg is HTTP; upstream tailnet leg is HTTPS with mandatory certificate verification. No
public listener.

**Project Type**: Single-binary CLI plus long-running local service with an embedded operator web
UI. The same executable provides `serve`, the GUI, operator commands, diagnostics, and client
configuration generation.

**Performance Goals**: Added median latency at most 10 ms and p95 at most 25 ms for non-streamed
requests; at most 25 ms to first streamed byte; 50 concurrent streamed requests; idle resident
memory at most 25 MB; cold start at most 200 ms; release binary at most 10 MB.

**Constraints**: No buffering of request or response bodies; no body timeout; no protocol
translation; no credential storage; no TLS verification bypass; no wildcard or public bind; no
Tailscale Funnel; no firewall changes; no cgo and no `unsafe` package in the request path; switch applies to the
next request without client restart; in-flight requests finish on their original upstream;
listener rebinding and profile endpoint changes apply without a process restart.

**Scale/Scope**: One operator, one workstation, one relay, two to eight upstream profiles, 100 to
500 models, 10 to 50 concurrent requests, streamed responses lasting up to 30 minutes. The relay
is a switch and failover mechanism, not a load balancer or an IP-pool manager.

## Constitution Check

*GATE: Must pass before Phase 0 research. Re-check after Phase 1 design.*

| Principle | Gate | Result |
|-----------|------|--------|
| I. Stable Client Surface | Clients are configured once against a fixed local address; upstream switching changes only relay-internal state. A deliberate entry-address edit is a separate client-visible reconfiguration and is labeled as such. | PASS |
| II. Stream Fidelity | Request and response bodies stream through `httputil.ReverseProxy`; no full read, re-encode, decompress, or body timeout. In-flight requests hold an immutable selection snapshot. | PASS |
| III. Explicit State, Predictable Failure | Operator intent lives in `state.json`; runtime observation lives in `runtime.json`; manual mode never silently falls back; unavailable upstreams return a structured 502/504; corrupt state falls back safely. | PASS |
| IV. Loopback by Default, Explicit LAN Opt-In | The listener defaults to loopback; a non-loopback address requires GUI confirmation plus `allow_non_loopback`, and must be a specific local-interface address (wildcards rejected). TLS verification is mandatory; credentials are read from the environment and never persisted; logs redact authorization and API-key headers; no Funnel or firewall changes are offered. | PASS |
| V. Small, Boring, Single Binary | Go + `net/http`, no runtime/database/external service; `CGO_ENABLED=0`; one external module; stripped single binary; dependency list kept intentionally small. | PASS |
| VI. Verified on the Target | Quickstart and release gates require live tests on the Windows workstation and the real `msi.tailc56568.ts.net` path, including a live SSE switch and a tailnet failure/recovery test. | PASS |

**Post-design re-check**: PASS. The design keeps the data plane stateless per request, confines
mutation to a small intent file, the CLI, and an authenticated same-origin GUI, and keeps all
secrets in the environment. The GUI adds embedded static assets, not a runtime service. No
principle violation requires a Complexity Tracking entry.

## Project Structure

### Documentation (this feature)

```text
specs/001-hot-upstream-switch/
├── plan.md              # This file
├── research.md          # Phase 0 output
├── data-model.md        # Phase 1 output
├── quickstart.md        # Phase 1 validation guide
├── contracts/           # Phase 1 interface contracts
│   ├── relay-http.md
│   ├── cli.md
│   ├── config.md
│   ├── client-config.md
│   ├── model-catalog.md
│   └── ui.md
├── checklists/
│   └── requirements.md
└── tasks.md             # Phase 2 output (/speckit-tasks - not created here)
```

### Source Code (repository root)

```text
go.mod
go.sum
README.md

cmd/
└── shunt/
    └── main.go                 # dispatch, CGO-free build

internal/
├── cli/
│   ├── cli.go                  # subcommand grammar (stdlib flag)
│   └── commands/
│       ├── serve.go
│       ├── use.go
│       ├── mode.go
│       ├── status.go
│       ├── doctor.go
│       ├── models.go
│       ├── config.go
│       ├── ui.go
│       └── install.go
├── config/
│   ├── config.go               # TOML schema, defaults
│   └── validate.go             # bind, profile, UI validation
├── state/
│   └── state.go                # operator intent, revision, atomic write
├── runtime/
│   └── runtime.go              # observation snapshot
├── relay/
│   ├── server.go               # http.Server per listener generation
│   ├── supervisor.go           # bind-first listener swap, drain
│   ├── proxy.go                # httputil.ReverseProxy, selection snapshot
│   ├── headers.go              # hop-by-hop filtering, Host rewrite
│   ├── errors.go               # structured 502/503/504 bodies
│   └── inflight.go             # in-flight counter and guard
├── upstream/
│   ├── client.go               # http.Transport pools, TLS config
│   ├── probe.go                # health probes, auto state machine
│   └── tls.go                  # system roots, minimum TLS 1.2
├── ui/
│   ├── ui.go                   # embed.FS, token, origin checks
│   ├── api.go                  # overview, entry, profile, switch, models
│   └── assets/
│       ├── index.html
│       ├── app.js
│       └── style.css
├── models/
│   ├── dialect.go              # Anthropic/OpenAI shapes
│   ├── normalize.go            # tolerant normalization
│   └── cache.go                # per-profile/dialect cache, TTL, stale
├── clients/
│   ├── claude.go               # shell snippet and settings.json merge
│   ├── codex.go                # comment-preserving TOML line editor
│   ├── dsh.go                  # merge-ready cordis.patch.yml fragment
│   └── write.go                # backup, atomic write, dry-run, rollback
├── platform/
│   ├── paths.go                # APPDATA/LOCALAPPDATA/XDG resolution
│   ├── atomic.go               # temp + os.Rename replace
│   └── service.go              # schtasks install/uninstall
├── logging/
│   └── logging.go              # slog handler, redaction, rotation
└── util/
    └── util.go                 # time, request IDs, small helpers

test/
├── contract/
│   ├── relay_headers_test.go
│   ├── relay_streaming_test.go
│   ├── relay_errors_test.go
│   ├── status_schema_test.go
│   └── gui_api_test.go
├── integration/
│   ├── hot_switch_test.go
│   ├── auto_failover_test.go
│   ├── model_discovery_test.go
│   ├── client_config_test.go
│   ├── gui_rebind_test.go
│   ├── security_test.go
│   └── soak_test.go            # skipped unless SHUNT_SOAK=1
└── support/
    ├── fake_upstream.go
    └── fixtures/
```

**Structure Decision**: Single Go module. `cmd/shunt` is a thin entry point; all logic lives under
`internal/`, so the module cannot be imported by another project by accident. The data plane
(`relay`, `upstream`) is separated from the control plane (`cli`, `state`, `runtime`, `ui`) so
the request path cannot accidentally write files. The GUI assets are compiled into the binary
with `embed.FS`; there is no asset directory to install and no web server to run separately.

## Architecture

### Process Model and Routing

`shunt serve` is a foreground process by default. It loads the TOML configuration, reads the
operator-intent state, binds the configured listener (default `127.0.0.1:8787`), and starts five
goroutines:

1. The `net/http` listener, one `http.Server` per listener generation.
2. A state watcher that polls `state.json` every 200 ms and publishes an immutable selection
   snapshot through an `atomic.Pointer[Selection]`.
3. A health-probe loop for automatic mode and status reporting.
4. A runtime snapshot writer that atomically updates `runtime.json` on change and at most every
   five seconds.
5. A configuration watcher and bind supervisor that applies profile endpoint changes, and
   rebinds the listener when the entry address changes, without restarting the process.

The router reserves `/_shunt/status`, `/_shunt/healthz`, `/_shunt/readyz`, `/_shunt/version`,
`/_shunt/ui`, and `/_shunt/api/*` for the control surface. Every other method and path is proxied.
The status endpoints remain read-only. The GUI API is the only authenticated mutation surface;
it writes the same intent file and configuration that the CLI writes, through the same
control-plane modules. The data plane (`relay`, `upstream`) never writes files.

### Switch Mechanism

`state.json` is owned by the control plane. It contains a schema version, a monotonically
increasing revision, the mode (`manual` or `auto`), the selected profile for manual mode, the
preferred profile for automatic mode, an update timestamp, and a short reason. The CLI and the
GUI's switch handler write it with an atomic replace after incrementing the revision.
`shunt use remote`, `shunt use local`, `shunt switch on`, `shunt switch off`, and the equivalent GUI
controls are the only selection mutation paths.

The relay's watcher stats the file every 200 ms. It reads the file only when the modification
time, size, or revision changes. A valid update is published as a new `Selection` value. A
missing, truncated, corrupt, or unknown-profile state is logged and ignored; the current
selection stays in place. At startup, an unusable state falls back to the configuration's
default profile and manual mode. The data plane never writes `state.json`; only the CLI and the
authenticated GUI switch handler do, through the same module.

Each incoming request loads the current `*Selection` (an immutable struct with profile id and
generation) before opening an upstream connection. That snapshot is held until the response body completes
or is dropped. A switch therefore affects only requests that start after the watcher publishes
the new selection; in-flight streams keep their original upstream and run to completion. This is
the core of the hot-switch guarantee.

`runtime.json` is written by the relay, never by the CLI. It records the effective profile,
per-profile health, in-flight count, counters, and the last switch reason. It is an observation,
not an input. `shunt status` prefers the live `/_shunt/status` endpoint and falls back to
`runtime.json` plus `state.json` when the relay is not running.

### Operator GUI and Live Reconfiguration

The GUI is a single embedded page served at `/_shunt/ui`, plus a JSON API at `/_shunt/api/*`. It
uses plain HTML, CSS, and JavaScript compiled into the binary with `embed.FS`. There is no
CDN, framework, template engine, or separate process. The page has three views:

- **Overview**: effective profile, mode, per-profile health, in-flight count, counters, last
  switch reason, and manual switch/mode controls.
- **Endpoints**: the entry IP and port, and each profile's scheme, host, and port. Local
  interface addresses are listed; wildcard addresses are not offered.
- **Models**: the normalized catalog with free/paid/unknown labels, refresh, and client snippet
  generation.

API surface:

| Method | Path | Purpose |
|--------|------|---------|
| GET | `/_shunt/api/overview` | Status plus editable configuration view |
| POST | `/_shunt/api/entry` | Change the listen IP and/or port |
| POST | `/_shunt/api/profiles/{id}/endpoint` | Change a profile's scheme/host/port |
| POST | `/_shunt/api/switch` | Select a profile (manual mode) |
| POST | `/_shunt/api/mode` | Set manual/auto and the preferred profile |
| GET | `/_shunt/api/models` | Catalog for the effective profile/dialect |
| POST | `/_shunt/api/models/refresh` | Force a catalog refresh |

Every mutation requires `X-Shunt-UI-Token`, a 256-bit value generated at process start and
embedded only in the served page. The handler also verifies that `Origin`, when present, matches
the current listener origin, and rejects cross-origin requests. Responses carry
`Content-Security-Policy: default-src 'self'; script-src 'self'; style-src 'self'`,
`X-Frame-Options: DENY`, and `Cache-Control: no-store`. There is no CORS grant.

**Entry change procedure**:

1. Parse the address and port. Reject wildcard, public, and unassigned addresses, and ports below
   1024. Non-loopback addresses must be private (RFC 1918, link-local, IPv6 ULA, or CGNAT). The
   check is a bind attempt on the requested address, plus an interface-list check for the GUI
   dropdown; a bind failure is conclusive.
2. If the address is non-loopback, require `confirm_lan: true`. The GUI shows a modal that names
   the interface and the exposure; confirmation marks the pending binding as LAN-authorized. A
   non-loopback change without confirmation is rejected with 403. The flag and listen are
   written in step 4 before the socket serves traffic. Every entry change also shows a
   client-impact notice: clients keep using the old address until their base URL is updated.
3. Bind the new `TcpListener` before touching the old one. If the bind fails (address in use,
   address disappeared), return 409 and keep the old listener.
4. Write the new address and the LAN flag to `config.toml` atomically with a
   comment-preserving TOML editor. If the write fails, close the new socket, keep the old
   listener, and return 500 `config_write_failed`.
5. Publish the new listener to the accept-loop supervisor. New connections go to the new socket;
   the old socket stops accepting and closes idle keep-alive connections. Requests already
   streaming finish without a rebind deadline; `shutdown_grace_ms` bounds process shutdown only.
6. Return the new origin. The page navigates to it; connections on the old origin receive a
   short redirect notice where practical.

**Profile endpoint change procedure**:

1. Parse `scheme://host:port`, apply default ports, and reject a path, query, or fragment.
2. Probe the new origin with the configured probe. A failure returns 422 with the probe error and
   changes nothing.
3. Write the normalized `base_url` to `config.toml` atomically.
4. Publish a new immutable profile table through an `atomic.Pointer[ProfileTable]`. Each request
   loads the table once at start, so in-flight streams keep the old endpoint and new requests use
   the new one.
5. If the edited profile is effective, `runtime.json` records the new endpoint and the change
   reason. No process restart and no client reconfiguration occur.

**External edits**: the configuration watcher stats `config.toml` every second. A valid change is
applied through the same validation and reload path. An invalid change is logged and ignored; the
last good configuration stays active. A listen-address change from an external editor follows the
same bind-first procedure, and a failed bind leaves the old listener running.

**LAN posture**: the page shows a persistent warning while the listener is non-loopback, naming
the interface and the fact that any host on that network can reach the relay. The entry listener
is plain HTTP and the page carries the token, so LAN mode assumes a trusted network: a LAN host
can open the GUI, change configuration, and send requests through the relay, consuming the
upstream's quota. The token protects against foreign web origins, not a LAN user or an on-path
eavesdropper; the confirmation modal says so. Public addresses are
rejected, so the entry listener cannot become an Internet listener by accident. The warning cannot
be dismissed permanently. The relay never enables Funnel, never binds a wildcard address, and
never changes firewall rules.

### Data Plane and Streaming

The proxy path is deliberately thin:

1. Read the request head and the current selection snapshot.
2. Build the upstream URI from the profile's base URL origin plus the original path and query.
   The base URL has no path; the relay does not add, remove, or rewrite `/v1` segments.
3. Copy end-to-end headers, remove hop-by-hop headers and any header named by `Connection`,
   replace `Host` with the upstream authority, and remove `Expect`, `TE`, `Trailer`, and
   `Upgrade`.
4. Send the request with its body streamed by `httputil.ReverseProxy`; the request body is never
   fully read into memory.
   No body is collected.
5. Apply a short connect timeout and a generous response-header timeout. There is no body
   timeout and no total-request timeout.
6. On response headers, copy the status and filtered headers and stream the response body frame
   by frame. Flush every frame. Do not decompress, re-encode, or aggregate.
7. On completion, client disconnect, or upstream disconnect, drop the body and release the
   in-flight guard.

The listener and both upstream legs use `TCP_NODELAY`. The client-facing leg is HTTP/1.1 only;
the upstream leg is HTTP/1.1 over HTTP or TLS (ALPN `http/1.1`). Connection pools are per
profile. The transport sets `DisableCompression = true`; compression is passed through, never
applied or removed by the relay.

### Failure Semantics

A connection, DNS, TLS, or response-header timeout before any response header produces a
structured JSON error with status 502 or 504. The body includes a stable error type, a
human-readable message, the selected profile, and a request id. The shape carries both the
Anthropic-style top-level `type` and the OpenAI-style `error.message`/`error.type` fields so both
client families can display it.

Once upstream response headers have been received, the relay never retries. If the upstream
disconnects mid-stream, the client connection is closed; the relay does not fabricate a
terminator or switch upstreams mid-body. Upstream 4xx and 5xx responses are passed through
unchanged. A 401, 403, or 429 is an application result, not a relay health failure, and does not
trigger failover in manual mode. Client disconnects cancel the upstream request and are logged at
debug level, not as errors.

In automatic mode, connect, DNS, and TLS failures from requests increment the same failure
counter as probes; HTTP status codes do not. A request is never retried after its body has been
forwarded. Failover is achieved by choosing the next request's upstream, not by replaying the
current one.

### Automatic Mode

Automatic mode is a small state machine per profile. The default probe is `GET {base_url}/` with
a 2-second timeout, no credentials, and a `200-399`, `401`, `403`, or `429` result counted as
reachable. A `5xx`, a connection error, a DNS error, a TLS error, or a timeout counts as a
failure. The probe verifies transport reachability, not application authorization; `shunt doctor`
performs the authenticated model-listing check separately.

The default loop probes every 10 seconds with jitter. The preferred profile is demoted after
three consecutive failures and promoted after two consecutive successes. The thresholds,
interval, and timeout are configurable. A connect, DNS, or TLS failure observed on a real request
also counts toward the failure threshold; HTTP 4xx/5xx responses do not. This makes an abrupt
upstream death visible without waiting for the next probe while avoiding failover on a client
authentication mistake.

The effective profile in automatic mode is the preferred profile while it is healthy, otherwise
the healthy fallback with the lowest numeric priority. If no profile is healthy, the effective profile remains
the last known good (or the preferred profile if there is none), and requests return the
structured unavailable error. The relay keeps probing and recovers without a restart. A manual
`shunt use <profile>` switches the mode to manual and stops automatic movement until
`shunt mode auto` is issued.

### Status and Diagnostics

`GET /_shunt/status` returns the schema described in `contracts/relay-http.md`: version, pid,
uptime, listen address, mode, selected and preferred profiles, effective profile, last known
good, in-flight count, counters, last switch, and per-profile health. `GET /_shunt/healthz`
returns liveness only. `GET /_shunt/readyz` returns 200 when at least one profile is healthy and
503 otherwise. `GET /_shunt/version` returns the binary version and schema version.

`shunt status` uses the live endpoint when it is reachable within 500 ms. If the configured entry
is unreachable, it tries `runtime.json.listen` before declaring the relay down; this covers an
external listen edit whose bind failed. Otherwise it reports that the relay is not running and
prints the last runtime snapshot and the persisted operator intent. Human output is a compact
table; `--json` emits the same schema as the endpoint.

`shunt doctor` runs independent checks and reports `PASS`, `WARN`, or `FAIL` with a safe next
action:

- Configuration file exists, parses, and passes schema and bind-address validation, including
  the local-interface and `allow_non_loopback` rules.
- State file parses or falls back safely; runtime snapshot is readable.
- The configured port is free or owned by a running `shunt` process. A non-loopback bind also
  warns that inbound traffic may be blocked by Windows Firewall; the tool does not change firewall
  rules.
- The local profile answers its probe.
- The tailnet profile resolves, completes a TLS handshake with a valid certificate, and answers
  its probe.
- If the `tailscale` CLI is available, `tailscale status` reports the local node and the MSI peer;
  `tailscale funnel status` is checked and any active Funnel is a warning, never a suggested
  fix.
- The active upstream answers an authenticated model-listing request (when a token is available).
- State, cache, and log directories are writable.
- Logs and configuration contain no obvious credential fields.

Exit codes are 0 for all pass, 1 for warnings, and 2 for failures.

### Model Discovery and Client Configuration

`shunt models` and the GUI model refresh resolve a token from `SHUNT_TOKEN`, then
`ANTHROPIC_API_KEY`, `ANTHROPIC_AUTH_TOKEN`, or `OPENAI_API_KEY`, in that order. The token is
read from the relay process environment, never from an argument, never from the GUI, and never
printed. If no token is available, refresh returns `409 credential_unavailable`; a cached
catalog is still readable. By default the command queries the relay
at `http://127.0.0.1:8787/v1/models`, which means it exercises the currently effective upstream.
`--via <profile>` bypasses the relay and queries that profile directly, which is useful when the
relay is down or when comparing local and tailnet listings.

The Anthropic dialect sends `x-api-key` and `anthropic-version: 2023-06-01`; the OpenAI dialect
sends `Authorization: Bearer`. The response parser accepts a `data` array or a `models` object,
normalizes identifiers, display names, provider, context and output limits, capabilities, input
modalities, reasoning efforts, lifecycle, and cost/tier/free metadata, and preserves the raw
record. Anthropic pagination follows `has_more` and `last_id` up to the configured page limit;
OpenAI listings are read in one response unless the gateway advertises `has_more` and `last_id`,
in which case the same pagination rule applies. A malformed or empty list is an error, not a
fabricated catalog.

Free/paid classification is conservative: explicit `free` or `tier` wins; zero cost is free;
positive cost is paid; everything else is unknown. The default model for generated configuration
is the first free model in upstream order. If no free model exists, the command exits with a
distinct code and asks for an explicit `--model`; it never defaults to paid or unknown. An
explicitly named paid model is allowed with a warning, because the operator made the choice.

The cache is keyed by profile and dialect, has a 12-hour default TTL, and stores the normalized
catalog plus the raw records. A cold fetch failure falls back to a stale cache only with a
visible stale marker; with no cache, it fails. `--refresh` bypasses the cache. `--json` emits the
normalized catalog; the human view is a table in upstream order with free/paid/unknown labels.

Client generation uses the same catalog:

- **Claude Code**: a shell snippet by default, with a stable `ANTHROPIC_BASE_URL` and the main
  model plus the opus/sonnet/haiku/fable aliases mapped to the selected free model. The existing
  `ANTHROPIC_API_KEY` is reused; no new token is derived. `ANTHROPIC_AUTH_TOKEN` is deliberately
  not set because the upstream proxy selects the Anthropic dialect from `x-api-key`. A
  settings.json merge is available with `--write`; it writes only non-secret values and leaves
  the token in the environment.
- **Codex**: a `config.toml` provider block using `base_url = "http://127.0.0.1:8787/v1"`,
  `env_key = "SHUNT_API_KEY"`, and `wire_api = "responses"` (the only value current Codex
  accepts). `--write` uses a targeted TOML line editor to preserve comments and unrelated keys.
- **DeepSeek Harness**: a merge-ready `cordis.patch.yml` fragment under an existing
  `- id: llm-pi-ai` entry. It emits one provider per protocol: `anthropic-messages` with the
  origin base URL, and/or `openai-responses` (or `openai-completions`) with the `/v1` base URL.
  Provider ids are stable, lowercase, and hyphenated; `apiKeyEnv` references `SHUNT_TOKEN`.
  DSH replaces the whole entry config, so the fragment includes a warning to merge providers and
  preserve other fields; automatic writing is refused.

All writes are opt-in, create a timestamped backup, preserve unrelated settings, and support a
`--dry-run` preview. The DSH fragment is print-only in v1.

### Security and Operations

The data plane binds to loopback by default. A non-loopback address is accepted only when it is
a specific private address assigned to a local interface and the operator confirms it in the GUI
with `allow_non_loopback = true`; wildcard and public addresses are rejected. The relay never enables Tailscale
Funnel, never changes firewall rules, and never disables upstream authentication. TLS verification is always on; there is no
`--insecure` flag. The tailnet profile uses `https://msi.tailc56568.ts.net` through Tailscale
Serve, which presents a valid certificate; a raw `http://100.x.y.z:4000` profile remains possible
because WireGuard already encrypts the tailnet path, but HTTPS is the default.

Credentials are passed through from the client request to the selected upstream. They are not
written to configuration, state, runtime, caches, or logs. The model commands and the GUI refresh
read a token from the relay process environment. Generated configuration contains environment
references (`SHUNT_TOKEN`, `SHUNT_API_KEY`, and the existing `ANTHROPIC_API_KEY` for Claude Code),
not token values. The logger redacts `Authorization`, `x-api-key`, `api-key`,
`proxy-authorization`, `cookie`, and `set-cookie`, and never logs bodies. The status endpoint
contains no secrets.

Selection mutation goes through the CLI or the authenticated GUI switch control, both writing
`state.json` through the same control-plane module. Configuration mutation goes through the CLI
or the authenticated GUI endpoint editor. The status endpoints are read-only. Without a LAN
opt-in, the attack surface is "any local process can read status"; with a LAN bind it is "any
host on that LAN can read status and reach the data plane", which the GUI states explicitly. A
client still needs its own credential to use the upstream.

`shunt install --start-at-login` uses a per-user Task Scheduler entry (`schtasks`) and does not
require elevation. If task creation fails, the command prints the exact command for manual
execution and does not attempt to elevate or disable security controls. A Windows service is out
of scope for v1.

The operational note about NordVPN is a diagnostic, not a code path: NordVPN Meshnet/NDivert can
interfere with the Tailscale `100.64.0.0/10` range. `shunt doctor` reports a tailnet timeout and
suggests checking the VPN's Meshnet/split-tunnel state; it never suggests disabling the firewall
or exposing the relay.

### Complexity Tracking

No constitution violations. The design intentionally avoids a separate control listener, a
database, a service manager, protocol translation, and automatic DSH file rewriting. Each would
add surface or risk without serving the hot-switch requirement.

| Violation | Why Needed | Simpler Alternative Rejected Because |
|-----------|------------|-------------------------------------|
| None | - | - |

## Phase 0: Research

Research is consolidated in [research.md](research.md). The decisions that shape the design are:

- A local HTTP relay is the only mechanism that changes upstreams per request without touching
  the client. Client-side environment changes, hosts-file edits, DNS changes, and `HTTPS_PROXY`
  tunnels are cold or connection-scoped and are rejected.
- Go with `net/http` is chosen over Rust, Caddy, and `netsh interface portproxy`: the standard
  library already provides streaming reverse-proxy, TLS, embedding, and HTTP server primitives,
  so the GUI and live rebinding need less custom code while the binary stays small and needs
  only one external module.
- Operator intent is a revisioned JSON file polled every 200 ms, not a separate control
  listener. The CLI and the GUI switch handler write it; this keeps intent file-based and works
  when the relay is not running.
- Request and response bodies stream through `httputil.ReverseProxy`. No buffering, no body timeout, no
  decompression, no retry after bytes are sent.
- Health probes use the unauthenticated root endpoint and treat any HTTP response below 500 as
  reachable; authenticated model listing is a separate diagnostic.
- Model discovery supports both Anthropic and OpenAI listing dialects, normalizes
  free/paid/unknown status, and defaults only to free models.
- Client configuration uses the documented Claude Code, Codex, and DSH formats; DSH is a
  merge-ready fragment because its patch replaces the whole plugin entry.

## Phase 1: Design and Contracts

The design artifacts are:

- [data-model.md](data-model.md): configuration, operator intent, runtime observation, model
  catalog, client targets, and request context, with validation rules and state transitions.
- [contracts/relay-http.md](contracts/relay-http.md): proxied request/response behavior, header
  filtering, status/health/readiness endpoints, error shapes, and timeouts.
- [contracts/cli.md](contracts/cli.md): command grammar, flags, output formats, and exit codes.
- [contracts/config.md](contracts/config.md): TOML configuration, state/runtime/cache JSON, paths,
  validation, and atomicity.
- [contracts/client-config.md](contracts/client-config.md): exact Claude Code, Codex, and DSH
  snippets, base-URL nuances, protocol selection, and write/backup rules.
- [contracts/model-catalog.md](contracts/model-catalog.md): dialect handling, normalization,
  free/paid classification, caching, pagination, and default selection.
- [contracts/ui.md](contracts/ui.md): embedded GUI routes, authentication, entry rebinding,
  profile endpoint edits, and LAN confirmation.
- [quickstart.md](quickstart.md): runnable validation scenarios, including the live hot-switch
  test and the tailnet path test.

## Implementation Strategy

Implementation is organized into milestones that each produce a testable slice. The order keeps
the data plane correct before adding convenience features.

1. **M0 - Skeleton and control state**: module, CLI grammar, config parser, paths, atomic writes,
   state/runtime schemas, logging with redaction. Exit: `shunt config validate` and
   `shunt use` work without a relay; corrupt state falls back.
2. **M1 - Data plane**: listener, router, header filtering, URI join, HTTP and HTTPS upstream
   clients, streaming request/response, in-flight guard, structured errors. Exit: contract tests
   pass against a fake upstream; SSE first byte arrives before the upstream finishes.
3. **M2 - Hot switch and status**: state watcher, `atomic.Pointer` selection snapshot,
   per-request pinning, status/health/readiness endpoints, `shunt status`, `shunt use`,
   `shunt switch on/off`. Exit: live SSE survives 20 switches; next request reaches the new
   upstream within 1 second.
4. **M3 - Automatic mode**: probe loop, hysteresis, request-failure accounting, manual override,
   runtime snapshots, `shunt mode auto`, `shunt doctor` health checks. Exit: failover and recovery
   tests pass against a controllable fake upstream and the real tailnet host.
5. **M4 - Model discovery**: dialect requests, normalization, free/paid classification,
   pagination, cache/TTL/stale fallback, table and JSON output, `--via` direct mode. Exit: 98
   models from the real local proxy in both dialects; free-first default is enforced.
6. **M5 - Client configuration**: Claude Code shell/settings, Codex TOML with a comment-preserving editor, DSH
   fragment, backups, dry-run, rollback. Exit: generated snippets validate structurally and, when
   the clients are installed, are accepted by them.
7. **M6 - Operator GUI and live reconfiguration**: embedded assets, session token and origin
   checks, overview/endpoints/models views, entry rebind supervisor, profile endpoint hot apply,
   config watcher, LAN confirmation. Exit: GUI tests rebind 8787 to a free port, change a
   profile's upstream port with a live stream in flight, and reject wildcard and unconfirmed LAN
   binds.
8. **M7 - Operations and hardening**: doctor, login startup via `schtasks`, log rotation, soak
   test, performance measurement, release build with `-trimpath -ldflags="-s -w"`. Exit: all success
   criteria measured and recorded.

No milestone includes protocol translation, a public listener, or an IP-pool manager.

## Risks and Mitigations

| Risk | Impact | Mitigation |
|------|--------|------------|
| The proxy accidentally buffers SSE | Streams stall; hot-switch test fails | `httputil.ReverseProxy` with `FlushInterval = -1`; contract test asserts first byte before upstream completion; no `io.ReadAll` in the request path. |
| Claude Code sends only `Authorization` and the upstream selects the OpenAI dialect | Wrong protocol behavior | Generated Claude Code config sets `ANTHROPIC_API_KEY` (x-api-key) and leaves `ANTHROPIC_AUTH_TOKEN` unset; document and test with the installed client. |
| Codex rejects a non-`responses` wire API | Codex cannot connect | Emit `wire_api = "responses"` only; verify the upstream serves `/v1/responses`; test with the installed Codex version. |
| DSH patch replaces the whole `llm-pi-ai` entry | Existing providers are lost | Print a merge-ready fragment with a warning; do not auto-write DSH; validate with `dsh --dump-config` when installed. |
| Tailscale path blocked by ACL, DNS, or NordVPN | Remote profile unreachable | `shunt doctor` separates DNS, TCP, TLS, and HTTP failures; report a safe next action; never suggest disabling the firewall or Funnel. |
| Windows atomic replace is not atomic | Corrupt state or cache | Use a tested atomic-write helper; revisioned state; invalid files are ignored at startup. |
| Model metadata omits free/paid | Wrong default selection | Conservative classification; unknown is never default-selected; explicit `--model` required when no free model exists. |
| Auto mode flaps | Repeated switching | Three-failure demotion, two-success promotion, 10 s interval with jitter, manual override. |
| State watcher misses a write | Switch is delayed | Atomic replace changes mtime and size; revision check; 200 ms polling; status reports the applied revision. |
| Port 8787 is occupied | Relay cannot start | Clear startup error; `shunt doctor` identifies the process and suggests a different loopback port. |
| Binary size or memory exceeds budget | Fails a constitution constraint | `CGO_ENABLED=0`, `-trimpath`, `-ldflags="-s -w"`, one external module; measure in M7 and remove non-essential code if needed. |
| Credentials leak into logs or generated files | Security defect | Environment-only token reads; header redaction; generated config uses references; security test greps logs/state/cache/config. |
| Client config format drifts | Generated snippets break | Pin to current documented formats; structural tests; `--dry-run`; backups; version notes in the contract. |
| A LAN bind is enabled casually and exposes the relay | Unwanted LAN exposure | Loopback default; non-loopback requires GUI confirmation plus `allow_non_loopback`; wildcard and public addresses rejected; persistent warning; audit log entry; doctor reports the exposure. |
| An entry-address change strands clients still pointed at the old address | Failed requests after a deliberate endpoint edit | The GUI warns before applying and names the client impact; the operation is documented as a reconfiguration, not a hot switch; clients are repointed or restarted by the operator. |
| A malicious web page posts to the GUI | Unauthorized config change or switch | Per-run 256-bit token required on every mutation; `Origin` check; no CORS; `SameSite=Strict` if a cookie is used; CSP and frame denial. |
| Listener rebind races with in-flight connections | Dropped requests during an entry change | Bind the new socket first, swap the accept loop, stop accepting on the old socket, close idle connections, and let active streams finish without a rebind deadline; old listener retained on failure. |
| Upstream port change points at a dead endpoint | All requests fail after an edit | Probe before apply; keep the previous endpoint on failure; return 422 to the GUI; in-flight requests stay on the old endpoint. |

## Validation Gates

A release is not complete until all of the following are true:

- `gofmt -l` reports nothing, `go vet ./...` passes, and `go test ./...` passes on the target.
  `go test -race ./...` passes on the Linux CI runner with cgo enabled.
- Contract tests prove header filtering, URI joining, streaming first-byte behavior, structured
  errors, status schemas, and pass-through of 401/403/429.
- The live hot-switch test passes: a streamed response survives 20 switches, and the next
  request reaches the selected upstream within 1 second.
- The tailnet test passes against `https://msi.tailc56568.ts.net` with a valid certificate, and
  the failure/recovery test passes with the tailnet path unavailable.
- Model discovery returns the real catalog in both dialects; the free-first rule is enforced;
  the cache and stale fallback behave as specified.
- Generated Claude Code, Codex, and DSH snippets match the contracts; writes create backups and
  preserve unrelated settings.
- `netstat`/`Get-NetTCPConnection` shows the default listener on `127.0.0.1` only; a LAN bind is
  possible only with confirmation and `allow_non_loopback`, and a wildcard bind is rejected.
- GUI tests prove entry-port rebinding, profile-port hot apply with a live stream, session-token
  and origin rejection, and the LAN confirmation flow. Logs, state, cache, and generated config
  contain no credentials; Tailscale Funnel is not enabled.
- The 24-hour soak and the performance measurement meet the success criteria in the spec.
