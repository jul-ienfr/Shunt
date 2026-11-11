# Research: Hot-Switching Local/Tailnet Upstream Relay

**Date**: 2026-11-11
**Feature**: `001-hot-upstream-switch`
**Status**: Complete; no unresolved `NEEDS CLARIFICATION`

## Context and Constraints

Claude Code, Codex, and DeepSeek Harness each read their API endpoint from configuration at
process start. Claude Code uses `ANTHROPIC_BASE_URL`; Codex uses a `model_providers.<id>.base_url`
entry; DSH uses a provider `baseURL` in a profile patch. None of them safely re-reads an endpoint
change mid-session. The operator wants the local proxy by default, an on-demand switch to the
tailnet host, and the switch to work while a client is running. The client-side endpoint must
therefore never change.

The environment observed during discovery:

- The local proxy runs as a FastAPI/uvicorn process bound to `0.0.0.0:4000`.
- It serves `/v1/models`, `/v1/messages`, `/v1/chat/completions`, `/v1/responses`, and a dashboard
  at `/`.
- It selects the Anthropic dialect when the request carries `x-api-key`, otherwise the OpenAI
  dialect. The local token is the placeholder `proxypal-local`.
- The `/v1/models` response is compatible with both the OpenAI list shape and the Anthropic
  Models API shape and currently exposes 98 models.
- The MSI host is reachable at `https://msi.tailc56568.ts.net` through Tailscale Serve on 443
  with a valid certificate. Raw port 4000 is not exposed.
- A local Windows `WSAEACCES` condition was observed while a VPN component was active and
  disappeared after the VPN was disconnected. This is an operational failure mode for the
  tailnet path, not a reason to weaken security.

## Decision 1: Local HTTP Relay, Not Client-Side Switching

**Decision**: Put a stable HTTP relay at `http://127.0.0.1:8787` and switch the relay's upstream
per request.

**Rationale**: The relay is the only layer that can change the destination after the client has
started. The client keeps one base URL for its whole life, and the switch is a local state
change. The relay can also stream responses without buffering and can present a single status
surface.

**Alternatives considered**:

- **Change environment variables / restart the client**: rejected because it is a cold switch
  and loses the running session.
- **Edit the hosts file**: rejected because hosts maps names to IPs, not ports, is cached by the
  OS and clients, and cannot express HTTP-level routing.
- **`netsh interface portproxy`**: rejected because it is a kernel TCP forwarder with no
  HTTP-level selection, no TLS origination, no header control, and no per-request switch.
- **`HTTPS_PROXY` CONNECT tunnel**: rejected because a CONNECT tunnel is established per
  connection and a per-request switch would require MITM and certificate substitution. It also
  does not carry the Anthropic base-URL semantics cleanly.
- **Caddy with admin API reload**: technically possible, but a larger binary, a separate config
  model, and reload semantics that are connection-oriented rather than request-oriented. It is
  more moving parts than the problem requires.
- **Direct connection to the tailnet host**: rejected because it is not a switch and does not
  solve the local-default requirement.

## Decision 2: Go + net/http, Single Binary

**Decision**: Go current stable (1.23 or newer), standard library `net/http`,
`net/http/httputil`, `crypto/tls`, `embed`, and `encoding/json`, with
`github.com/pelletier/go-toml/v2` as the only external module for parsing human-edited TOML.
Built with `CGO_ENABLED=0`.

**Rationale**: The request path needs precise streaming control, a small memory footprint, a
fast start, and a single executable with no runtime. `httputil.ReverseProxy` streams request and
response bodies without buffering and handles hop-by-hop headers; `FlushInterval = -1` flushes
each write immediately for SSE. `net/http` supplies the server, client transport, TLS
verification, and graceful shutdown. `embed.FS` carries the GUI assets inside the binary. Go's
runtime and garbage collector are small enough for this workload, and the toolchain produces a
static single binary on Windows without a C toolchain when `CGO_ENABLED=0`.

**Alternatives considered**:

- **Rust + Hyper**: maximum control and no garbage collector, but more custom async code for
  listener swaps, draining, and the embedded GUI. Still viable; Go reduces implementation and
  maintenance cost for the same observable behavior.
- **Caddy**: excellent reverse proxy, but much larger than needed and its dynamic upstream
  configuration is not designed for per-request switching with an in-flight drain.
- **Node/TypeScript**: requires a runtime and has a larger memory footprint; the local proxy is
  already Python, and adding another runtime is unnecessary.
- **A Windows service in C#**: heavier, platform-bound, and harder to keep portable.

**Build choices**: `go build -trimpath -ldflags="-s -w"` with `CGO_ENABLED=0`; module mode with
a pinned `go.sum`. No cgo, no reflection-heavy framework, and no code generation in the request
path. The release binary is measured against the 10 MB budget; if it exceeds the budget,
dependencies are removed before the budget is relaxed.

## Decision 3: Revisioned Intent File, Not a Separate Control Listener

**Decision**: The control plane (CLI or authenticated GUI switch handler) writes `state.json`
atomically; the relay polls it every 200 ms and publishes a new selection through an
`atomic.Pointer[Selection]` read once per request. The data plane never writes the intent file.
Runtime observations go to a separate `runtime.json`.

**Rationale**: The mutation surface is a local file protected by the user's profile permissions.
There is no second control port to authenticate, and the CLI still works when the relay is
stopped. The GUI token authorizes its same-origin API but is never needed to read or write
intent. Atomic replace plus a monotonic revision makes a torn read detectable. A 200 ms poll is
invisible to a human and costs one `stat` per interval.

**Alternatives considered**:

- **Separate HTTP control listener**: convenient, but adds a second port, authentication and
  CSRF surface, and cannot update intent when the relay is down. The embedded GUI therefore
  calls the same file-backed switch handler instead of owning state.
- **Named pipe / Unix socket**: lower latency, but platform-specific code and lifecycle
  handling. Not justified for a sub-second switch.
- **Filesystem watcher**: immediate, but needs platform-specific event handling and dependency
  surface. A 200 ms poll is simpler and deterministic.
- **Read the file on every request**: simple, but adds a filesystem read to the hot path. The
  atomic pointer keeps the request path memory-only.

**Semantics**: Manual mode selects one profile. Automatic mode stores a preferred profile and
lets the relay choose the effective profile. A manual `use` changes the mode to manual. Corrupt
or unknown state is ignored; startup falls back to the configured default.

## Decision 4: Stream Everything, Never Buffer

**Decision**: Forward request and response bodies as `net/http` streams through
`httputil.ReverseProxy`, chunk by chunk. No `io.ReadAll`, no decompression, no re-encoding, no
body timeout, and no retry after any request bytes have been sent.

**Rationale**: Model responses are long-lived SSE streams. Buffering the first byte until the
upstream finishes would destroy the user experience and add memory pressure. A body timeout
would truncate long reasoning. Re-encoding could change chunk boundaries, compression, and
timing. The relay's job is transport, not transformation.

**Implementation notes**:

- `ReverseProxy` with `FlushInterval = -1` writes and flushes each response chunk as soon as it
  arrives. The first response byte reaches the client before the upstream completes.
- The client-facing leg is HTTP/1.1 only. The upstream leg is HTTP/1.1 over HTTP or TLS with ALPN
  pinned to `http/1.1`. This avoids h2 flow-control and stream-reset behavior that would add
  complexity without a benefit for these clients.
- `TCP_NODELAY` is enabled on accepted listeners and upstream dials.
- Connect timeout is short (500 ms local, 2 s tailnet by default) through a custom
  `net.Dialer`. Response-header timeout is generous (120 s default) through
  `http.Transport.ResponseHeaderTimeout`. There is no body timeout and no total-request timeout
  (`http.Client.Timeout` is not set).
- `Content-Length` is preserved when present; `Transfer-Encoding` is removed and re-framed by
  `net/http`. This changes HTTP framing, not body bytes.
- `Accept-Encoding` is passed through. The transport sets `DisableCompression = true`, so Go
  neither adds `Accept-Encoding` nor transparently decompresses a response; if the upstream
  compresses, the relay forwards the compressed bytes unchanged.

**Header handling**: Remove the hop-by-hop headers from RFC 7230 section 6.1 and any header named
by `Connection`: `Connection`, `Keep-Alive`, `Proxy-Authenticate`, `Proxy-Authorization`, `TE`,
`Trailer`, `Transfer-Encoding`, and `Upgrade`. Remove `Expect` and `Host`, then set `Host` to
the upstream authority. Preserve `Authorization`, `x-api-key`, `anthropic-version`,
`anthropic-beta`, `OpenAI-*`, `Content-Type`, `Accept`, and other end-to-end headers. Add
`X-Shunt-Request-Id` and `X-Shunt-Profile` to the response for diagnosis; the request path does not
add headers unless `trace_headers` is enabled.

**Failure semantics**: A connection, DNS, TLS, or header timeout before response headers returns
a structured 502/504. Once response headers are received, no retry. A mid-stream upstream
disconnect closes the client connection; the relay does not fabricate a terminator. Upstream
4xx/5xx responses pass through unchanged. Client disconnects cancel the upstream request.

## Decision 5: Probe Transport Health, Not Authorization

**Decision**: The default health probe is `GET {base_url}/` with a 2 s timeout and no
credentials. Any HTTP response below 500 counts as reachable; 5xx, connection errors, DNS
errors, TLS errors, and timeouts count as failures. Authenticated model listing is a separate
doctor check.

**Rationale**: The relay does not own credentials and must not store them for probes. The root
endpoint is an unauthenticated liveness surface on the local proxy and on Tailscale Serve. A 401
or 403 still proves TCP, DNS, TLS, and HTTP are working; the client's own credential determines
whether the request succeeds. Treating 401 as unhealthy would cause unnecessary failover and
hide the real authentication problem.

**Automatic mode**: Three consecutive failures demote the preferred profile; two consecutive
successes promote it. Probe every 10 s with jitter. Connect/DNS/TLS failures observed on real
requests also count; HTTP 4xx/5xx responses do not. The state machine has hysteresis to prevent
flapping. If no profile is healthy, the effective profile stays at the last known good and
requests return a structured error; probing continues.

**Alternatives considered**:

- **Probe `/v1/models` with a token**: more representative, but requires credential storage and
  can be expensive on a large catalog. Rejected for the periodic probe; used in doctor.
- **Probe a model request**: expensive and could consume quota. Rejected.
- **TCP-only probe**: does not detect TLS or HTTP failures. Rejected.
- **Failover on 429**: a rate limit is profile-specific and may be a valid reason to switch, but
  retrying a POST after body bytes are sent is unsafe. The relay does not buffer or replay. The
  operator can switch manually or use automatic health if the upstream is actually down.

## Decision 6: Two Model-Listing Dialects, One Normalized Catalog

**Decision**: `shunt models` can query either the Anthropic or the OpenAI listing dialect and
normalizes both into one catalog. Anthropic discovery uses `x-api-key` and
`anthropic-version: 2023-06-01`; OpenAI discovery uses `Authorization: Bearer`. The command
accepts a `data` array or a `models` object, normalizes identifiers, display names, provider,
context/output limits, capabilities, input modalities, reasoning efforts, lifecycle, and
cost/tier/free metadata, and keeps the raw record.

**Rationale**: The upstream proxy serves both shapes and selects the dialect from the auth
header. Claude Code needs Anthropic-style model identifiers and aliases; Codex and DSH OpenAI
providers need OpenAI-style discovery. A single normalized catalog avoids three parsers and
three cache formats.

**Free-first rule**: Explicit `free` or `tier` metadata wins. Zero cost is free; positive cost is
paid; everything else is unknown. The default model is the first free model in upstream order.
If no free model exists, the command exits with a distinct code and requires `--model`; it never
defaults to paid or unknown. An explicit paid choice is allowed with a warning, because the
operator made it. This follows the project rule that the free leg is the objective and that a
paid leg must not be proposed as the answer.

**Cache**: Per profile and dialect, 12-hour TTL, with `fetched_at`, source, and a stale marker.
A cold fetch failure uses a stale cache only with a visible warning; no cache means an error.
`--refresh` bypasses the cache. `--via <profile>` bypasses the relay and queries the profile
directly; the default goes through the relay so it tests the effective path.

**Pagination**: Anthropic listings follow `has_more`/`last_id` up to a configurable page limit
(default 10). OpenAI listings are read in one response. A malformed or empty list is an error,
not a fabricated catalog.

**Sources**: The local proxy's `/v1/models` route and its dual-shape docstring were inspected in
`opencode.py`. DSH's discovery behavior is documented in the `dsh-llm-pi-ai` README: OpenAI
dialects use `GET {baseURL}/models` with bearer auth; Anthropic uses native `GET /v1/models`
semantics with `x-api-key` and `anthropic-version`, accepting a trailing `/v1` or not.

## Decision 7: Claude Code Configuration Uses `ANTHROPIC_API_KEY`

**Decision**: Generate a stable `ANTHROPIC_BASE_URL` (origin, no `/v1`), an
`ANTHROPIC_API_KEY` populated from `SHUNT_TOKEN`, `ANTHROPIC_MODEL`, and the
`ANTHROPIC_DEFAULT_OPUS_MODEL`, `ANTHROPIC_DEFAULT_SONNET_MODEL`, `ANTHROPIC_DEFAULT_HAIKU_MODEL`,
and `ANTHROPIC_DEFAULT_FABLE_MODEL` aliases. Do not set `ANTHROPIC_AUTH_TOKEN`. The legacy
`ANTHROPIC_SMALL_FAST_MODEL` is emitted only with an explicit `--legacy` flag.

**Rationale**: Claude Code's documented environment variables distinguish the API key (sent as
`x-api-key`) from the auth token (sent as `Authorization: Bearer`). The upstream proxy selects
the Anthropic dialect from `x-api-key`. Configuring only `ANTHROPIC_AUTH_TOKEN` would make the
proxy treat Claude Code as an OpenAI client. Setting `ANTHROPIC_API_KEY` is the reliable way to
keep the Anthropic path.

**Aliases**: Claude Code resolves `opus`, `sonnet`, `haiku`, and `fable` through default-model
variables. If those variables are unset, the client may request Anthropic model names that the
gateway does not serve. The generator maps all aliases to the selected free model unless the
operator provides explicit `--opus`, `--sonnet`, `--haiku`, or `--fable` values. This is safe and
keeps the client usable with a single free model.

**Base URL**: `http://127.0.0.1:8787` without `/v1`, because Claude Code appends `/v1/messages`.

**Token handling**: The client keeps the `ANTHROPIC_API_KEY` it already uses against the local
proxy; the relay forwards it unchanged. The generated snippet changes only `ANTHROPIC_BASE_URL`
and the model aliases, and explicitly leaves `ANTHROPIC_AUTH_TOKEN` unset. A settings.json merge
writes only non-secret values and leaves the key to the environment. This respects the
no-stored-credentials principle and avoids inventing a second credential.

**Sources**: Claude Code settings and model configuration documentation:
`https://docs.anthropic.com/en/docs/claude-code/settings`,
`https://docs.anthropic.com/en/docs/claude-code/model-config`.

## Decision 8: Codex Uses a Custom Responses Provider

**Decision**: Generate:

```toml
model = "<id>"
model_provider = "shunt"

[model_providers.shunt]
name = "Shunt relay"
base_url = "http://127.0.0.1:8787/v1"
env_key = "SHUNT_API_KEY"
wire_api = "responses"
```

**Rationale**: Codex's current configuration reference accepts a custom provider with `name`,
`base_url`, `env_key`, and `wire_api`; `wire_api` is `responses` only and is the default when
omitted. The upstream proxy serves `/v1/responses`, so this is the supported path. The base URL
includes `/v1` because Codex appends `/responses`. The token is referenced through
`SHUNT_API_KEY`, not written into the TOML.

**Write behavior**: `--write` uses the comment-preserving line editor to preserve comments and
unrelated keys, creates a
timestamped backup, and refuses to write if the existing file is not valid TOML. `--dry-run`
prints the proposed content. The generated block is also printed to stdout by default.

**Sources**: `https://developers.openai.com/codex/config-reference`,
`https://developers.openai.com/codex/config-advanced`.

## Decision 9: DeepSeek Harness Gets a Merge-Ready Fragment

**Decision**: Generate a `cordis.patch.yml` fragment under an existing `- id: llm-pi-ai` entry,
with one provider per protocol:

```yaml
- id: llm-pi-ai
  config:
    providers:
      shunt-anthropic:
        displayName: Shunt (Anthropic)
        api: anthropic-messages
        baseURL: http://127.0.0.1:8787
        apiKeyEnv: SHUNT_TOKEN
        models:
          - id: <model-id>
      shunt-openai-responses:
        displayName: Shunt (OpenAI Responses)
        api: openai-responses
        baseURL: http://127.0.0.1:8787/v1
        apiKeyEnv: SHUNT_TOKEN
        models:
          - id: <model-id>
```

**Rationale**: DSH's pi-ai adapter uses one protocol per provider. A gateway that serves
Anthropic and OpenAI protocols therefore needs two providers. The profile patch path is
`$DSH_HOME/profiles/<profile>/cordis.patch.yml` (default profile `web`, default home `~/.dsh`).
Provider IDs are permanent, lowercase, and hyphenated; requests, saved sessions, and credential
references use them. `apiKeyEnv` is a credential reference resolved per request, so no secret
enters the file. DSH re-reads the patch on the next request; no restart is needed.

**The merge hazard**: A Cordis config override replaces the complete entry config. Appending a
second `- id: llm-pi-ai` entry is invalid/ambiguous, and replacing the entry can drop other
providers. The generator therefore prints a fragment and a warning: merge the new providers into
the existing `config.providers` map, preserving every other provider and field. Automatic writing
of the DSH file is refused in v1. `dsh --dump-config` is the validation step when DSH is
installed.

**Base URLs**: For `anthropic-messages`, use the origin `http://127.0.0.1:8787`; pi-ai appends
`/v1/messages`. For OpenAI protocols, use `http://127.0.0.1:8787/v1`; pi-ai appends
`/chat/completions` or `/responses`. DSH's Anthropic discovery accepts a base URL with or without
a trailing `/v1`, but model requests use the configured `baseURL` unchanged, so the origin form
is the safe choice for Anthropic.

**Discovery**: DSH's pi-ai adapter discovers OpenAI routes with `GET {baseURL}/models` and bearer
auth, and Anthropic routes with native `GET /v1/models?limit=1000` semantics plus `x-api-key` and
`anthropic-version`. The relay supports both paths, so DSH's own "Fetch available models" can
query the relay as well.

**Sources**: `https://github.com/deepseek-ai/deepseek-harness/blob/master/docs/user/guide/providers.md`
and
`https://github.com/deepseek-ai/deepseek-harness/blob/master/packages/llm/llm-pi-ai/README.md`.

## Decision 10: Security Posture

**Decision**: Loopback bind by default; a specific local-interface address is allowed only with
`allow_non_loopback = true` and an explicit GUI confirmation; wildcard addresses are rejected.
TLS verification always on; credentials read from the environment and passed through; no
persistence of credentials; redacted logs; no public exposure; no Funnel; no firewall changes.
The GUI uses a per-run token and same-origin checks for mutations.

**Rationale**: The relay carries model credentials and can reach a tailnet host. Binding it
publicly or weakening TLS would turn a local convenience into an exposure. The credentials are
already held by the clients; the relay should not become a second secret store. A LAN bind is
useful when another device on the operator's own network needs the relay, but it must be a
deliberate, visible act rather than a side effect of editing a port. A read-only status endpoint
plus an authenticated same-origin GUI is enough for diagnosis and configuration.

**Operational notes**:

- The relay's default listener is `127.0.0.1:8787`; `Get-NetTCPConnection -LocalPort 8787`
  should show a loopback local address. A LAN bind shows the chosen interface address and a
  persistent warning in the GUI.
- Tailscale Serve on the MSI publishes HTTPS on 443; raw port 4000 is not exposed. The remote
  profile uses the FQDN so the certificate matches.
- A raw `http://100.x.y.z:4000` profile is possible because WireGuard encrypts the tailnet path,
  but HTTPS is the default.
- NordVPN Meshnet/NDivert can interfere with `100.64.0.0/10`. `shunt doctor` reports a tailnet
  timeout and suggests checking Meshnet/split-tunnel state; it does not suggest disabling the
  firewall or exposing the relay.
- `shunt install --start-at-login` uses a per-user `schtasks` entry and does not require admin. A
  Windows service is out of scope.

## Decision 11: File and Build Mechanics

**Decision**: Use a small `internal/platform/atomic` helper for atomic replace,
`github.com/pelletier/go-toml/v2` for config reads, a comment-preserving targeted line editor for
Codex writes, `encoding/json` for state/runtime/cache, `flag` for the CLI, and `time` for UTC
timestamps. Build with `CGO_ENABLED=0`.

**Rationale**: The helper writes a temporary file in the destination directory, flushes and
syncs it, then renames it over the target. Go's `os.Rename` maps to `MoveFileEx` with replace
semantics on Windows; where a locked destination or sharing violation defeats it, the helper
falls back to `ReplaceFileW` through `syscall.NewLazyDLL("kernel32.dll")`, so no extra module is
needed. The targeted line editor rewrites only the managed keys and sections of a Codex config or of the
shunt config, preserving comments and unrelated settings that a full TOML round-trip would lose.
`encoding/json` is sufficient for machine-written JSON. The subcommand grammar is small enough
for `flag` plus a hand-written dispatcher; a CLI framework would add size without value.
`net/http` recovers panics per handler, and the few long-lived supervisor goroutines recover
explicitly so one bad update does not abort every in-flight stream.

**Alternatives considered**:

- **Bare `os.Rename` everywhere**: rejected because destination locking and replacement behavior
  differ across platforms and filesystems; the helper concentrates and tests that behavior.
- **`golang.org/x/sys/windows`**: unnecessary; `syscall.NewLazyDLL` covers the single
  `ReplaceFileW` call while keeping the one-module rule.
- **A YAML library for DSH**: rejected because DSH writing is print-only in v1; adding a YAML
  parser would not fix the entry-replacement hazard.
- **`cobra` or `urfave/cli`**: ergonomic, but larger than needed for this command set.
- **A Windows service package**: requires elevation and a service lifecycle; a per-user scheduled
  task is enough for a workstation relay.

## Decision 12: Embedded Web GUI, Not a Desktop Toolkit

**Decision**: Serve a single-page GUI from the relay listener at `/_shunt/ui` using HTML, CSS, and
vanilla JavaScript embedded with `embed.FS`. Provide a JSON API under `/_shunt/api/*` for the
overview, entry address, profile endpoints, switch/mode, and model catalog. Authenticate every
mutation with a per-run 256-bit token and an origin check. List local interface addresses with
`net.Interfaces` and `net.InterfaceAddrs`; validate a requested bind by attempting it.

**Rationale**: A web UI satisfies the requirement to edit the entry IP/port and each profile's
upstream port without adding a GUI toolkit, a WebView2 dependency, or a second process. It keeps
the single-binary and size budgets plausible: the assets are tens of kilobytes, and the HTTP
server already exists. The browser is the operator's on the same workstation; a web page is the
smallest thing that works. A native toolkit (egui) would add several megabytes and a rendering
loop; Tauri would add WebView2 and a build toolchain. The embedded page has neither.

**Why not a control API on a separate port**: A second listener doubles the bind surface and
creates a second address to protect. Serving the GUI on the relay listener means the same
loopback/LAN policy applies to both, and changing the entry address moves both together.

**Live rebinding**: The bind supervisor holds the current `net.Listener` and `http.Server`
behind a mutex-guarded generation slot. A change binds the new `net.Listener` first, starts the
new server, swaps the slot, and calls `Shutdown` on the old server without a deadline: it stops
accepting, closes idle connections, and lets active streams finish. The configured shutdown grace
applies to process exit, not to a rebind. A failed bind never disturbs the old listener. This is
the only way to change the entry port without restarting the process and dropping streams.

**Live profile changes**: The profile table is an immutable snapshot published through an
`atomic.Pointer[ProfileTable]`. Each request loads it once at start, so a profile endpoint change
affects only new requests. The new endpoint is probed before the table is published; a failure
keeps the old table.

**Security**: Binding a LAN address exposes the relay to that LAN. The confirmation modal names
the interface and the consequence, the config flag makes the choice durable, and the page keeps a
warning visible. Wildcards are rejected because they silently widen the exposure to every
interface, including ones added later. The token prevents a random web page from posting to
`127.0.0.1`; the origin check prevents DNS rebinding; the CSP prevents injected third-party
script.

**Alternatives considered**:

- **egui native window**: no browser, but several megabytes of GUI code and a new event loop in
  a process whose job is proxying. Rejected for size and complexity.
- **Tauri**: WebView2 dependency, a second build system, and a larger binary. Rejected.
- **CLI-only configuration**: smallest, but the user explicitly asked for a GUI. Rejected.
- **Separate admin port**: doubles the listener and its exposure. Rejected.
- **Cookie session**: requires CSRF handling and a login surface; a per-run token in the page is
  simpler and adequate for a single operator.

**Sources**: Go `net/http` server, `httputil.ReverseProxy`, and `embed` documentation; OWASP
guidance on DNS rebinding and same-origin policy; Go `net` package for local interface
enumeration.

## Unresolved Questions

None. The feature is fully specified for planning. Implementation must verify the generated
client snippets against the installed Claude Code, Codex, and DSH versions; if a client's current
format differs, the contract and generator are updated before release, not worked around with a
silent fallback.
