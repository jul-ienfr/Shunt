# Shunt

Shunt is a local HTTP relay with a stable client endpoint. It forwards requests to one
of several configured upstreams and lets an operator move the next request to another
upstream without restarting a client or moving the entry listener.

Clients keep pointing at `http://127.0.0.1:8787`; `shunt use`, `shunt switch`, or the
embedded GUI change where the next request goes. In-flight streams stay pinned to the
upstream they started on. Several entry ports can run at once, each either following the
global switch or pinned to one upstream.

The same source builds and runs on Windows, macOS, Linux, and other Unix systems. No
C toolchain is required: release builds use `CGO_ENABLED=0`.

## Requirements

- Windows 10/11, macOS 12+, or a Linux/BSD distribution supported by Go, on amd64 or
  arm64.
- Go 1.23 or newer to build from source:
  - Windows: `winget install GoLang.Go`
  - macOS: `brew install go`
  - Debian/Ubuntu: `sudo apt install golang-go`
- No credential is ever passed on the command line.

## Build and initialize

POSIX shells:

```sh
CGO_ENABLED=0 go build -trimpath -ldflags="-s -w" -o shunt ./cmd/shunt
./shunt version
./shunt init
./shunt config path
./shunt config validate
./shunt serve
```

PowerShell:

```powershell
$env:CGO_ENABLED = '0'
go build -trimpath -ldflags="-s -w" -o .\shunt.exe .\cmd\shunt
.\shunt.exe version
.\shunt.exe init
.\shunt.exe config path
.\shunt.exe config validate
.\shunt.exe serve
```

`init` writes a configuration with `local` and `remote` profiles, then creates the
state, runtime, cache, log, and backup directories. It refuses to overwrite an existing
configuration without `--force`.

Set the upstreams at init time so no machine-specific host is baked into the source:

```sh
./shunt init --local-url http://127.0.0.1:4000 --remote-url https://upstream.example
# or
SHUNT_LOCAL_URL=http://127.0.0.1:4000 \
SHUNT_REMOTE_URL=https://upstream.example \
./shunt init --force
```

The default remote is the reserved placeholder `https://remote.invalid`; point it at a
real upstream from the GUI, `shunt profile set-url`, or `init --force`.

### Platform paths

| Data | Windows | macOS | Linux/BSD |
|------|---------|-------|-----------|
| Configuration | `%APPDATA%\shunt\config.toml` | `~/Library/Application Support/shunt/config.toml` | `$XDG_CONFIG_HOME/shunt/config.toml` (`~/.config/shunt`) |
| State and runtime | `%LOCALAPPDATA%\shunt` | `~/Library/Application Support/shunt` | `$XDG_STATE_HOME/shunt` (`~/.local/state/shunt`) |
| Logs | `%LOCALAPPDATA%\shunt` | `~/Library/Logs/shunt` | `$XDG_STATE_HOME/shunt/logs` |
| Cache | `%LOCALAPPDATA%\shunt\cache` | `~/Library/Caches/shunt` | `$XDG_CACHE_HOME/shunt` |
| Backups | `%LOCALAPPDATA%\shunt\backups` | `~/Library/Application Support/shunt` | `$XDG_STATE_HOME/shunt/backups` |

`SHUNT_CONFIG` or `--config <path>` overrides the configuration file. `shunt config path`
always prints the resolved location.

Upgrading from the previous `wrap` name: copy the old `wrap` configuration and data tree
once into the `shunt` locations above, or pass the old file with `--config`.

### One-click launchers

- Windows: double-click `start-shunt.bat`. It rebuilds `shunt.exe` when Go is available,
  keeps the existing binary if a build fails, creates the configuration on first run,
  starts the relay with its tray icon, and opens the dashboard.
- macOS/Linux/BSD: run `./start-shunt.sh` (after `chmod +x start-shunt.sh`). It builds
  when Go is present, initializes on first run, and leaves the relay in the foreground.

`shunt serve` runs in the foreground. `Ctrl+C` stops it; the tray menu also has **Quit
Shunt**. Start the GUI separately when needed:

```sh
./shunt ui --open
```

### Run at login

```sh
./shunt install              # per-user startup entry
./shunt install --start-now  # also start it immediately
./shunt uninstall            # remove the entry; data is kept
```

The mechanism is native to the platform and never requests elevation:

| Platform | Startup mechanism |
|----------|-------------------|
| Windows | Scheduled task `shunt-relay` at logon (`schtasks`) |
| Linux | `~/.config/systemd/user/shunt-relay.service` (`systemctl --user`) |
| macOS | `~/Library/LaunchAgents/com.shunt.relay.plist` (`launchctl`) |

On a headless Linux host, make sure user lingering is enabled if the relay must run
without an active login session (`loginctl enable-linger "$USER"`).

## Multiple entry ports

The legacy single entry is still supported:

```toml
[relay]
listen = "127.0.0.1:8787"
allow_non_loopback = false
```

Declare `[[listeners]]` to run several ports at once:

```toml
# Follows the global manual/auto selection.
[[listeners]]
name = "switch"
listen = "127.0.0.1:8787"

# Always sent to the remote upstream, whatever the global selection is.
[[listeners]]
name = "remote"
listen = "127.0.0.1:8788"
profile = "remote"

# Pinned to the local proxy.
[[listeners]]
name = "local"
listen = "127.0.0.1:8789"
profile = "local"
enabled = true
```

Rules:

- The first enabled listener is the **primary** entry used by `status`, `doctor`, the GUI
  entry editor, and the tray dashboard link.
- A listener without `profile` follows the global switch. A pinned listener ignores
  global switches but still respects manual health gating.
- Names match `^[a-z][a-z0-9-]{0,31}$`, are unique, and canonical addresses must be
  unique. A pinned profile must be enabled, and at least one listener must be enabled.
- Public and wildcard binds are refused, exactly like the single entry.
- Hot reload binds new ports before retiring old ones. If any new bind fails, the
  previous listener set keeps serving.
- When `[[listeners]]` is present, `[relay] listen` is ignored. Without it, the legacy
  entry applies.

Manage the topology without hand-editing TOML:

```sh
./shunt listener list
./shunt listener add edge --listen 127.0.0.1:8788 --profile remote
./shunt listener set-listen edge 127.0.0.1:8790
./shunt listener remove edge

./shunt profile list
./shunt profile add third --url https://third.example --label "Third upstream"
./shunt profile set-url third https://new.example
./shunt profile remove third
```

Every edit is parsed and validated as a whole before it is written atomically, so a
mistyped address or a profile still pinned by a listener is rejected without touching
the running file. The dashboard additionally retargets `default_profile`,
`switch.on_profile`, `switch.off_profile`, `auto.preferred`, and the durable selection
to another enabled profile when you delete one, so an existing profile can be replaced
without hand-editing TOML. A running relay applies the change within
`relay.config_poll_ms`. `listener add` materializes the existing legacy entry as
`default` first, so adding a port never silently removes the current one.

## Switching and status

```sh
./shunt use local
./shunt status
./shunt use remote --force      # allow a failed probe
./shunt switch on               # configured on_profile alias
./shunt switch off              # configured off_profile alias
./shunt mode auto --prefer local
./shunt mode manual
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
| `GET /_shunt/status` | Full runtime snapshot, counters, health, listeners, last switch |
| `GET /_shunt/healthz` | Process liveness |
| `GET /_shunt/readyz` | Readiness; 503 when no enabled profile is healthy |
| `GET /_shunt/version` | Binary and schema versions |
| `GET /_shunt/ui` | Embedded GUI (token-authenticated mutations) |

`/_shunt/status` and the GUI overview include a `listeners` array with each name,
address, pinned profile, and effective profile.

## Upstream proxy policy

Relay traffic is **direct by default**. A machine-wide `HTTP_PROXY`/`HTTPS_PROXY` must
not silently capture relay traffic, because that is exactly how a configuration that
works on one machine fails on another. Opt in per profile:

```toml
[profiles.remote]
proxy_url = "http://proxy.example:3128"  # explicit proxy for this profile
# or
proxy_from_env = true                     # honor HTTP_PROXY/HTTPS_PROXY for this profile
```

`proxy_url` takes precedence when both are set, and must be an absolute `http` or
`https` URL. This setting is validated before the configuration is accepted.

## GUI

Open `/_shunt/ui` from the printed URL. On loopback the page is served directly; for a
LAN entry the relay requires `allow_non_loopback` and an explicit confirmation in the
API. The LAN warning is persistent: LAN hosts can reach the GUI, change configuration,
and consume upstream quota, and the entry listener is plain HTTP on a trusted network.

The header language button toggles the interface between English and French. The initial
language comes from `navigator.language`, remembers the last manual choice in
`localStorage` under `shunt-lang`, and is a display preference only. Static labels use
`data-i18n` attributes, dynamic text uses the shared `t()` helper in `i18n.js`, and known
API error codes get a translated message.

`shunt serve` also adds a Windows tray icon (disable it with `--no-tray`). On other
platforms the tray is skipped and the relay keeps running. The menu opens the dashboard,
copies its URL, switches profiles in one click, and quits the relay. `shunt ui --webview`
opens a chromeless Edge/Chrome window when available and falls back to the default
browser.

The entry IP/port and each profile's scheme/host/port can be edited live. The
**Endpoints** tab also adds and removes profiles and additional entry ports, including
moving an existing port to another address. Deleting a profile that is still pinned by a
port returns `profile_in_use`; deleting one used as the default, a switch alias, or the
auto preference retargets those references to another enabled profile. The last enabled
profile and the last enabled port cannot be removed. An entry change binds the new
listener first, then retires the old generation; the page navigates to the new origin. A
profile endpoint change probes the candidate before publishing an immutable profile
table, so in-flight requests finish on the previous endpoint. Wildcard and public binds
return `invalid_entry`, an unconfirmed LAN bind returns `lan_not_confirmed`, a busy port
returns `bind_failed`, and a failed configuration write returns `config_write_failed`; in
every failure the old listener keeps serving.

The GUI token is generated per process, injected only into the served page, and required
on every mutation as `X-Shunt-UI-Token`. It is never written to configuration, state,
runtime, cache, or logs. `Origin`, `Host`, and `Sec-Fetch-Site` checks reject cross-site
requests, and no CORS grant is emitted.

The **Clients** tab mirrors the CLI generator for Claude Code, Codex, and DeepSeek
Harness. Type or choose a model id, or leave the field empty to use the first free cached
model, then preview and write. Claude Code writes the non-secret `ANTHROPIC_BASE_URL` and
model aliases into `settings.json`; Codex refreshes the `[model_providers.shunt]` block
while keeping comments and unrelated keys. Both create a timestamped backup first, and
each card lists those backups with a **Restore** button; a restore first creates a safety
backup of the current file, then replaces it atomically. DSH remains preview-only in v1:
copy the fragment and merge it into the existing `llm-pi-ai` row.

## Model discovery and client configuration

```sh
./shunt models
./shunt models --json
./shunt models --via remote --dialect anthropic
./shunt models --via remote --dialect openai --refresh

./shunt models --for claude --dry-run
./shunt models --for claude --write
./shunt models --for codex --write
./shunt models --for dsh --api both
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

```sh
./shunt doctor
./shunt doctor --json
./shunt doctor --profile remote
```

`doctor` checks configuration parse/validation, every listener bind, state and runtime
readability, directory writability, relay reachability, the local probe, authenticated
model listing when a token is available, and the Tailscale CLI/Funnel posture. Exit 0
means all checks pass, 1 means warnings only, and 2 means at least one failure. Every
warning or failure includes a safe next action; the doctor never suggests a public bind,
a wildcard bind, firewall changes, Funnel, or TLS verification bypass.

`shunt serve` writes structured logs to stderr and to the log directory. The file rotates
at 10 MiB and keeps five backups. Authorization, API-key, cookie, and token attributes
are redacted; request and response bodies are never logged.

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

## Development

```sh
make build       # CGO_ENABLED=0 build of ./cmd/shunt
make test        # go test ./... -count=1
make race        # go test -race ./... (needs a C toolchain)
make vet         # go vet ./...
make cross       # linux/darwin/windows, amd64 and arm64, into dist/
```

Without `make`:

```sh
gofmt -l ./cmd ./internal ./test
go vet ./...
CGO_ENABLED=0 go test ./... -count=1
```

Targeted suites:

```sh
go test ./test/contract -count=1
go test ./test/integration -count=1
go test ./test/integration -run 'HotSwitch|MultiListener|ListenerSet' -count=1
go test ./test/integration -run Auto -count=1
go test ./test/integration -run 'Model|ClientConfig' -count=1
go test ./test/integration -run 'GUIRebind|GUIProfile|GUIEntry|GUITopology|GUIRemovesReferencedProfile' -count=1
go test ./test/integration -run 'Doctor|Security' -count=1
```

The tray test is gated because it briefly creates a real Windows icon:

```powershell
$env:SHUNT_TRAY_TEST = '1'
go test ./internal/desktop -run Tray -count=1 -v
```

The soak test is gated and skipped by default:

```sh
SHUNT_SOAK=1 go test ./test/integration -run Soak -count=1 -v -timeout 2h
```

It runs the configured number of requests and switches (`SHUNT_SOAK_REQUESTS`,
`SHUNT_SOAK_SWITCHES`) and includes a burst of concurrent streams.

### Continuous integration

`.github/workflows/ci.yml` runs on every push and pull request:

- `gofmt`, `go vet`, and `go test ./...` on Ubuntu, Windows, and macOS with
  `CGO_ENABLED=0`.
- `go test -race ./...` on Linux.
- Cross-compilation of linux/amd64, linux/arm64, darwin/amd64, darwin/arm64,
  freebsd/amd64, windows/amd64, and windows/arm64, uploaded as a build artifact.

Live upstream checks and the full 24-hour soak still require the operator's own
environment; they are not part of the fast suite.

## More documentation

- Feature specification: `specs/001-hot-upstream-switch/spec.md`
- Implementation plan: `specs/001-hot-upstream-switch/plan.md`
- Task list: `specs/001-hot-upstream-switch/tasks.md`
- Acceptance walkthrough: `specs/001-hot-upstream-switch/quickstart.md`
- Configuration contract: `specs/001-hot-upstream-switch/contracts/config.md`
- Relay HTTP contract: `specs/001-hot-upstream-switch/contracts/relay-http.md`
- Client generation contract: `specs/001-hot-upstream-switch/contracts/client-config.md`
