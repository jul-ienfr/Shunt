# Shunt

Shunt is a local HTTP relay with a stable client endpoint. It forwards requests to one
of several configured upstreams and lets an operator move the next request to another
upstream without restarting a client or moving the entry listener.

The first user story is deliberately small: keep Claude Code, Codex, or DSH pointed at
`http://127.0.0.1:8787`, then switch between a local proxy and a tailnet upstream with
`shunt use`, `shunt switch`, or the embedded GUI. In-flight streams stay pinned to the
upstream they started on.

## Requirements

- Windows 10/11 x64, or another platform supported by Go.
- Go 1.23 or newer. If Go is missing:

  ```powershell
  winget install GoLang.Go
  # Open a new terminal, then:
  go version
  ```

- No C toolchain is required for the release build. Builds use `CGO_ENABLED=0`.
  The optional `-race` test runs in Linux CI where cgo is available.

## Build and initialize

```powershell
$env:CGO_ENABLED = '0'
go build -trimpath -ldflags="-s -w" -o .\shunt.exe .\cmd\shunt
.\shunt.exe version
.\shunt.exe init
.\shunt.exe config path
.\shunt.exe config validate
```

`init` writes a configuration with `local` and `remote` profiles, then creates the
state, runtime, cache, log, and backup directories. It refuses to overwrite an existing
configuration without `--force`.

Upgrading from the previous `wrap` name: Shunt reads `%APPDATA%\shunt\config.toml` and
writes data under `%LOCALAPPDATA%\shunt`. Copy the old `%APPDATA%\wrap\config.toml` and
`%LOCALAPPDATA%\wrap` tree once before the first launch if you want to keep the existing
profiles, state, cache, logs, and backups. The old directories are left untouched.

For a one-click Windows launch, double-click `start-shunt.bat`. It builds `shunt.exe` when
missing, creates the default configuration on first run, starts the relay with its tray
icon, and opens the dashboard in a chromeless window. `Ctrl+C` in the console stops the
relay; the tray menu also has **Quit Shunt**.

Start the relay in one terminal and the GUI in another:

```powershell
.\shunt.exe serve
.\shunt.exe ui --open
```

The default entry is loopback-only:

```toml
[relay]
listen = "127.0.0.1:8787"
allow_non_loopback = false

[profiles.local]
base_url = "http://127.0.0.1:4000"

[profiles.remote]
base_url = "https://msi.tailc56568.ts.net"
```

IPv6 entries are written in bracketed form, for example `[::1]:8787`. Loopback, RFC 1918,
CGNAT, IPv6 ULA, and link-local addresses are accepted; wildcard and public addresses
are always refused.

## Switching and status

```powershell
.\shunt.exe use local
.\shunt.exe status
.\shunt.exe use remote --force      # allow a failed probe
.\shunt.exe switch on               # configured on_profile alias
.\shunt.exe switch off              # configured off_profile alias
.\shunt.exe mode auto --prefer local
.\shunt.exe mode manual
```

Manual mode is authoritative: an unhealthy selected profile returns a structured
`shunt_upstream_unavailable`/`shunt_upstream_timeout` error instead of silently falling
back. Auto mode probes the preferred profile, fails over after the configured failure
threshold, recovers after the success threshold, and returns
`shunt_no_healthy_upstream` when every enabled profile is known-unhealthy. Manual
selection immediately stops automatic movement.

Useful endpoints:

| Endpoint | Purpose |
|----------|---------|
| `GET /_shunt/status` | Full runtime snapshot, counters, health, last switch |
| `GET /_shunt/healthz` | Process liveness |
| `GET /_shunt/readyz` | Readiness; 503 when no enabled profile is healthy |
| `GET /_shunt/version` | Binary and schema versions |
| `GET /_shunt/ui` | Embedded GUI (token-authenticated mutations) |

## GUI

Open `/_shunt/ui` from the printed URL. On loopback the page is served directly; for a
LAN entry the relay requires `allow_non_loopback` and an explicit confirmation in the
API. The LAN warning is persistent: LAN hosts can reach the GUI, change configuration,
and consume upstream quota, and the entry listener is plain HTTP on a trusted network.

The header language button toggles the interface between English and French. The initial
language comes from `navigator.language`, remembers the last manual choice in
`localStorage` under `shunt-lang`, and is a display preference only; it is not written to
configuration, state, or cache. Static labels use `data-i18n` attributes, dynamic text uses
the shared `t()` helper in `i18n.js`, and known API error codes get a translated message.

`shunt serve` also adds a Windows tray icon (disable it with `--no-tray`). The menu opens
the dashboard, copies its URL, switches profiles in one click, and quits the relay. The
dashboard opens in a chromeless Edge or Chrome app window; `shunt ui --webview` uses the
same window, while `shunt ui --open` keeps the default-browser behavior. If neither Edge
nor Chrome is installed, the window falls back to the default browser.

The entry IP/port and each profile's scheme/host/port can be edited live. An entry change
binds the new listener first, then retires the old generation; the page navigates to the
new origin. A profile endpoint change probes the candidate before publishing an immutable
profile table, so in-flight requests finish on the previous endpoint. Wildcard and public
binds return `invalid_entry`, an unconfirmed LAN bind returns `lan_not_confirmed`, a busy
port returns `bind_failed`, and a failed configuration write returns
`config_write_failed`; in every failure the old listener keeps serving.

The GUI token is generated per process, injected only into the served page, and required
on every mutation as `X-Shunt-UI-Token`. It is never written to configuration, state,
runtime, cache, or logs. `Origin`, `Host`, and `Sec-Fetch-Site` checks reject cross-site
requests, and no CORS grant is emitted.

The **Clients** tab mirrors the CLI generator for Claude Code, Codex, and DeepSeek
Harness. Type or choose a model id, or leave the field empty to use the first free cached
model, then preview and write. Claude Code writes the non-secret `ANTHROPIC_BASE_URL` and
model aliases into `settings.json`; Codex refreshes the `[model_providers.shunt]` block while
keeping comments and unrelated keys. Both create a timestamped backup first, and each card lists
those backups with a **Restore** button; a restore first creates a safety backup of the current
file, then replaces it atomically. DSH remains preview-only in v1: copy the fragment and merge it
into the existing `llm-pi-ai` row.
Previews contain only the generated keys, never unrelated values from the existing file,
so an existing API key cannot leak into the page, and no credential is ever written.

## Model discovery and client configuration

```powershell
.\shunt.exe models
.\shunt.exe models --json
.\shunt.exe models --via remote --dialect anthropic
.\shunt.exe models --via remote --dialect openai --refresh

.\shunt.exe models --for claude --dry-run
.\shunt.exe models --for claude --write
.\shunt.exe models --for codex --write
.\shunt.exe models --for dsh --api both
```

The token is read from `SHUNT_TOKEN`, `ANTHROPIC_API_KEY`, `ANTHROPIC_AUTH_TOKEN`, or
`OPENAI_API_KEY` (or the variable named by `--token-env`). It is never accepted as a
command-line value and never printed. Discovery follows `has_more`/`last_id` pagination
up to the configured limits, normalizes both Anthropic `data` arrays and OpenAI `models`
objects, and caches per profile and dialect.

Generated client configuration is free-first: the first free model in upstream order is
selected automatically. Paid and unknown models are never selected implicitly; use
`--model` to choose one explicitly. With no free model the command exits 3. Claude Code
and Codex writes are opt-in, merge into existing settings, preserve unrelated keys, and
create a timestamped backup under the backups directory. DSH is print-only in v1 and
`--write` exits 6.

## Diagnostics

```powershell
.\shunt.exe doctor
.\shunt.exe doctor --json
.\shunt.exe doctor --profile remote
```

`doctor` checks configuration parse/validation, bind posture, state and runtime
readability, directory writability, relay reachability, the local probe, authenticated
model listing when a token is available, and the Tailscale CLI/Funnel posture. Exit 0
means all checks pass, 1 means warnings only, and 2 means at least one failure. Every
warning or failure includes a safe next action; the doctor never suggests a public bind,
a wildcard bind, firewall changes, Funnel, or TLS verification bypass.

`shunt serve` writes structured logs to stderr and to `logs/shunt.log`. The file rotates at
10 MiB and keeps five backups (`shunt.log.1` through `shunt.log.5`). Authorization,
API-key, cookie, and token attributes are redacted; request and response bodies are never
logged.

Exit codes used by the CLI:

| Code | Meaning |
|------|---------|
| 0 | Success |
| 1 | Generic failure or warnings only (`doctor`) |
| 2 | Usage/configuration failure |
| 3 | No free model; choose one with `--model` |
| 4 | Upstream/model-listing failure |
| 5 | Relay is not running |
| 6 | Client write refused (for example DSH or an unsafe path) |
| 7 | Relay is already running |

## Security posture

- TLS verification is mandatory. There is no `--insecure`, environment override, or
  `InsecureSkipVerify` path. `tls_verify = false` is rejected for HTTPS profiles.
- Wildcard (`0.0.0.0`, `::`) and public binds are rejected. A non-loopback bind requires
  `allow_non_loopback = true` in the file and `confirm_lan: true` through the GUI API.
- Credentials are forwarded to upstreams but never logged, stored in configuration/state/
  runtime/cache, or returned by `/_shunt/status`. Request bodies and headers are not logged.
- The relay does not enable Tailscale Funnel, change firewall rules, or accept `CONNECT`.
- GUI mutations require the page token and same-origin checks; the token is CSRF
  protection, not authentication against other LAN hosts.

## Tests

```powershell
gofmt -l .\cmd .\internal .\test
go vet ./...
go test ./...
go test ./test/contract -count=1
go test ./test/integration -count=1
go test ./test/integration -run HotSwitch -count=1
go test ./test/integration -run Auto -count=1
go test ./test/integration -run 'Model|ClientConfig' -count=1
go test ./test/integration -run 'GUIRebind|GUIProfile|GUIEntry' -count=1
go test ./test/integration -run 'Doctor|Security' -count=1
```

The Windows tray test is gated because it briefly creates a real icon:

```powershell
$env:SHUNT_TRAY_TEST = '1'
go test ./internal/desktop -run Tray -count=1 -v
```

The soak test is gated and skipped by default:

```powershell
$env:SHUNT_SOAK = '1'
go test ./test/integration -run Soak -count=1 -v -timeout 2h
```

It runs the configured number of requests and switches (`SHUNT_SOAK_REQUESTS`,
`SHUNT_SOAK_SWITCHES`) and includes a burst of concurrent streams. The full 24-hour soak
and the live `msi.tailc56568.ts.net` scenarios from `quickstart.md` are release checks
that require the target workstation; they are not part of the fast test suite.

Latency benchmarks compare direct requests with the same request through the relay, and report
p50/p95 plus time to first streamed byte:

```powershell
go test ./test/integration -run '^$' -bench Latency -benchmem -count=5
```

They use a loopback fake upstream. An indicative run on this host (Ryzen 5 7600X, Windows 11,
`-benchtime=300x -count=3`) added about 0.95–0.98 ms p50 and 0.64–1.17 ms p95 to non-streamed
requests, and 0.43–1.41 ms p50 (0.70–3.03 ms p95) to the first streamed byte. These numbers are
not the SC-003 acceptance measurement; that still requires the target workstation and a real
upstream.

The Linux CI job runs `CGO_ENABLED=1 go test -race ./...`. The Windows job runs
`gofmt`, `go vet`, and `go test` with `CGO_ENABLED=0`, then builds the release binary.

## Acceptance evidence

Recorded on Windows 11 with Go 1.26.1, `GOOS=windows GOARCH=amd64`, `CGO_ENABLED=0`:

| Check | Result |
|-------|--------|
| `gofmt -l .\cmd .\internal .\test` | no output |
| `go vet ./...` | pass |
| `go test ./... -count=1` | all packages pass (unit, contract, integration) |
| Hot switch | 20/20 next-request switches within 1 s; 50 switches with 10 live streams, zero truncation |
| GUI rebind | 20/20 rebinds serve within 1 s; IPv6 rebind; live stream survives a profile endpoint change |
| Auto mode | failover after 3 failures, recovery after 2 successes in under 30 s, all-unhealthy 503 |
| Model discovery | 500 entries cold within 5 s and cached within 100 ms, both dialects |
| Security | no secret in logs, state, runtime, cache, generated configs, or status; no `--insecure`; `tls_verify=false` rejected |
| Doctor | wrong port, stopped upstream, invalid certificate, missing credential, unwritable directories, and Funnel warning all classified with safe actions |
| Tray | gated `SHUNT_TRAY_TEST=1 go test ./internal/desktop -run Tray` starts and stops the Windows tray cleanly; `shunt serve` logs a warning and continues when no tray is available |
| Client config | `test/integration/client_config_test.go` and `test/contract/gui_clients_test.go`: CLI and GUI preview/write/backup-list/restore for Claude Code and Codex, DSH preview-only, backups verified, safety backup on restore, path traversal rejected, unrelated keys and comments preserved, existing credentials never appear in a preview |
| Soak smoke | `SHUNT_SOAK=1` with 200 requests, 10 switches, 5 streams: passed in 387 ms, zero truncation |
| Latency harness (local) | `test/integration/bench_test.go` with a fake upstream: added p50 0.95–0.98 ms and p95 0.64–1.17 ms non-streamed; first byte added p50 0.43–1.41 ms, p95 0.70–3.03 ms; indicative only |
| Release build | `CGO_ENABLED=0 go build -trimpath -ldflags="-s -w"`: single `shunt.exe`, 8,383,488 bytes |
| Module graph | one direct external module, `github.com/pelletier/go-toml/v2 v2.2.3` |

Deferred to the target workstation (not executable in this environment):

- Real Claude Code, Codex, and DSH sessions against `msi.tailc56568.ts.net` (T048, T111).
- Numeric median/p95 and first-byte acceptance measurements against the real target upstream
  (T108): the local fake-upstream harness is in `test/integration/bench_test.go` and described
  above. Idle RSS and cold start (SC-004) also require the target workstation; binary size is
  recorded above.
- The full 24-hour, 10,000-request, 200-switch soak (SC-010). The gated harness is ready.
- `go test -race ./...` (T107): the local host has no C compiler, so the Linux CI job is
  the authoritative race gate.

The per-criterion matrix with exact test names lives in
`specs/001-hot-upstream-switch/tasks.md`.

## More documentation

- Feature specification: `specs/001-hot-upstream-switch/spec.md`
- Implementation plan: `specs/001-hot-upstream-switch/plan.md`
- Task list: `specs/001-hot-upstream-switch/tasks.md`
- Acceptance walkthrough: `specs/001-hot-upstream-switch/quickstart.md`
- Configuration contract: `specs/001-hot-upstream-switch/contracts/config.md`
- Relay HTTP contract: `specs/001-hot-upstream-switch/contracts/relay-http.md`
- Client generation contract: `specs/001-hot-upstream-switch/contracts/client-config.md`
