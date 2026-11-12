# Tasks: Hot-Switching Local/Tailnet Upstream Relay

**Feature**: `001-hot-upstream-switch`

**Input**: Design documents from `specs/001-hot-upstream-switch/`

**Prerequisites**: `plan.md`, `spec.md`, `research.md`, `data-model.md`, `contracts/`, `quickstart.md`, `.specify/memory/constitution.md`

**Tests**: REQUIRED. The constitution's Development Workflow and the plan's Validation Gates mandate unit, contract, integration, race, and soak tests. Test tasks appear first in each user-story phase and MUST fail before the corresponding implementation.

## Format: `[ID] [P?] [Story] Description`

- **[P]**: Can run in parallel with other `[P]` tasks in the same phase (different files, no incomplete dependency).
- **[Story]**: `[US1]` through `[US5]`, mapping to `spec.md`.
- Every task names an exact file path or an exact validation command.

## Path Conventions

Single Go module at the repository root: `cmd/`, `internal/`, `test/`, `go.mod`. Unit tests live beside their package as `*_test.go`; cross-package contract and integration tests live under `test/`.

## Phase 1: Setup (Shared Infrastructure)

**Purpose**: Initialize the Go module, build skeleton, platform helpers, and deterministic test support.

- [X] T001 Verify `go version` reports Go 1.23 or newer; if Go is absent install `GoLang.Go` with `winget` and record the exact commands in `README.md`. Do not enable cgo for normal builds.
- [X] T002 Create `go.mod` with `module shunt` and `go 1.23`; keep `go.sum` committed.
- [X] T003 Add `github.com/pelletier/go-toml/v2` to `go.mod` as the only external module; run `go mod tidy` and confirm no other direct dependency appears.
- [X] T004 Create `cmd/shunt/main.go` as a thin entry point that calls `cli.Execute(os.Args[1:], os.Stdout, os.Stderr)` and returns its exit code.
- [X] T005 [P] Create `internal/platform/paths.go`: resolve `%APPDATA%\shunt\config.toml`, `%LOCALAPPDATA%\shunt\state.json`, `runtime.json`, `cache\`, `logs\`, `backups\`; support `XDG_CONFIG_HOME`/`XDG_STATE_HOME` on secondary platforms; create directories with user-only permissions where supported.
- [X] T006 [P] Create `internal/platform/atomic.go`: write a temp file in the target directory, `Sync`, close, then replace with `os.Rename`; on Windows use `ReplaceFileW` through `syscall.NewLazyDLL` with a rename fallback; preserve the original on failure.
- [X] T007 [P] Create `internal/logging/logging.go`: `log/slog` text or JSON handler selected by config; redact `Authorization`, `x-api-key`, `api-key`, `proxy-authorization`, `cookie`, and `set-cookie`; never accept or log request bodies; support level filtering.
- [X] T008 [P] Create `internal/util/util.go`: cryptographically random request IDs, duration formatting, and small shared helpers with no global mutable state.
- [X] T009 [P] Create `test/support/fake_upstream.go` and `test/support/fixtures/`: an `httptest` server with configurable SSE streaming, chunk delay, mid-stream disconnect, status-code injection, Anthropic and OpenAI model listings, pagination, and request capture for header assertions.
- [X] T010 [P] Create `.github/workflows/ci.yml`: Windows job runs `gofmt -l`, `go vet ./...`, `go test ./...`; Linux job runs `CGO_ENABLED=1 go test -race ./...`; release job builds with `CGO_ENABLED=0`.
- [X] T011 [P] Create `.gitignore` for `shunt.exe`, `*.test`, `coverage.out`, and local data directories; add the build/run section of `README.md`.

**Checkpoint**: The module builds, the CLI entry point exists, and the test support server compiles.

---

## Phase 2: Foundational (Blocking Prerequisites)

**Purpose**: Configuration, state, runtime observation, CLI skeleton, upstream transports, and the streaming data plane that every story reuses.

**CRITICAL**: No user story work can begin until this phase is complete.

### Configuration, State, and Runtime

- [X] T012 Create `internal/config/config.go`: TOML structs and defaults for `schema = 1`, `default_profile`, `[relay]` (`listen`, `allow_non_loopback`, polling and timeout keys, `body_idle_timeout_ms`, `shutdown_grace_ms`, `tcp_keepalive_s`, `trace_headers`, logging keys, `request_id_header`), `[switch]` (`on_profile`, `off_profile`), `[auto]` (`enabled`, `preferred`, `probe_interval_ms`, `probe_timeout_ms`, `failure_threshold`, `recovery_threshold`), `[profiles.*]` (`label`, `base_url`, `probe_url`, `probe_method`, `priority`, `enabled`, `connect_timeout_ms`, `response_header_timeout_ms`, `tls_verify`, `tags`), `[ui]`, `[paths]`, `[models]`, and `[clients.claude|codex|dsh]` exactly as `contracts/config.md`.
- [X] T013 Create `internal/config/validate.go`: reject unknown keys and schema values other than 1; enforce profile ID regex `^[a-z][a-z0-9-]{0,31}$`, base URL origin-only form, probe URL same origin, `probe_method` `GET` or `HEAD`, listen rules (loopback default; specific private local-interface address only with `allow_non_loopback = true`; wildcard, public, and unassigned addresses rejected), entry port range 1024-65535, `tls_verify = true`, `body_idle_timeout_ms = 0`, response-header timeout 1000-600000 ms, other timeout/threshold ranges, `tcp_keepalive_s` 0-3600, log enums, models ranges, `paths.*` absolute or `~`-expanded, enabled-profile references for `default_profile`/`switch`/`auto`, and duplicate-priority tie-breaking by profile ID.
- [X] T014 [P] Create `internal/config/edit.go`: a targeted line editor for the shunt `config.toml` that replaces only `relay.listen`, `relay.allow_non_loopback`, and `profiles.<id>.base_url`/`probe_url` while preserving comments, ordering, and unrelated keys; refuse duplicate or ambiguous matches.
- [X] T015 [P] Create `internal/config/config_test.go` and `internal/config/testdata/`: table tests for parsing, defaults, unknown-key rejection, and comment-preserving edits.
- [X] T016 [P] Create `internal/config/validate_test.go`: table-driven bind matrix covering loopback, each private range (RFC 1918, link-local, IPv6 ULA, CGNAT), wildcard, public, unassigned, ports 1023/1024/65535/65536, and LAN flag combinations.
- [X] T017 [P] Create `internal/state/state.go`: operator-intent schema with `schema`, monotonically increasing `revision`, `mode` (`manual`/`auto`), `selected`, `preferred`, `updated_at`, and `reason`; `Load` tolerates missing/truncated/corrupt files; `Save` increments revision and uses the atomic helper.
- [X] T018 [P] Create `internal/state/state_test.go`: missing, truncated, corrupt, unknown-profile, revision-increment, and atomic-replace cases; confirm a failed write leaves the prior file readable.
- [X] T019 [P] Create `internal/runtime/runtime.go`: observable snapshot with `schema`, `version`, `pid`, `started_at`, `uptime_s`, `listen`, `mode`, `selected`, `preferred`, `effective`, `last_good`, `inflight`, `requests_total`, `errors_total`, `switches_total`, `bytes_in`, `bytes_out`, `last_switch`, and `profiles` health map; write on change and at most every five seconds using the atomic helper.
- [X] T020 Create `internal/cli/cli.go`: subcommand grammar using `flag`, global help and version handling, and exit codes 0-7 from `contracts/cli.md`; no credential-bearing argument is accepted.

### CLI Commands and Server Startup

- [X] T021 Create `internal/cli/commands/config.go`: `shunt init [--force]` creates the default TOML, state, and directories and refuses to overwrite without `--force`; `shunt config path|show|validate|restore <backup>` prints paths, shows effective config, validates, and restores a client-config backup after creating a new backup.
- [X] T022 Create `internal/cli/commands/serve.go`: `shunt serve [--foreground] [--no-ui]` loads and validates config, loads operator intent with safe fallback, binds the configured listener, starts the relay, and handles `SIGINT`/`SIGTERM` with `shutdown_grace_ms` for process shutdown only.

### Upstream Transport and Health Primitives

- [X] T023 [P] Create `internal/upstream/tls.go`: system root pool, minimum TLS 1.2, mandatory verification, SNI from the profile host; no insecure flag or environment bypass.
- [X] T024 [P] Create `internal/upstream/client.go`: per-profile `http.Transport` with connection pooling, `DisableCompression = true`, `TCP_NODELAY`, connect and response-header timeouts from config, no body or total-request timeout, and HTTP/1.1 ALPN.
- [X] T025 [P] Create `internal/upstream/probe.go`: unauthenticated probe using `probe_method` (default `GET`) and `probe_url` (default `{base_url}/`) with the configured timeout; any status below 500 counts as reachable; connection, DNS, TLS, timeout, and 5xx count as failures; track consecutive failures/successes, `healthy`, `last_probe_ms`, and `last_status`.
- [X] T026 [P] Create `internal/upstream/probe_test.go`: classify timeout, DNS, TLS, 401, 403, 429, and 5xx; verify hysteresis counters and that HTTP status codes do not count as transport failures.

### Streaming Data Plane

- [X] T027 [P] Create `internal/relay/headers.go`: remove hop-by-hop headers and every header named by `Connection`; remove `Expect`, `TE`, `Trailer`, and `Upgrade`; rewrite `Host` to the upstream authority; suppress automatic `X-Forwarded-For`; add `X-Shunt-Request-Id` and `X-Shunt-Profile` on responses; optional trace headers only when `trace_headers = true`.
- [X] T028 [P] Create `internal/relay/inflight.go`: atomic in-flight counter, configurable maximum (default 64), acquire/release guard, and a 503 path when the limit is reached without affecting active streams.
- [X] T029 [P] Create `internal/relay/errors.go`: structured 502/503/504 JSON bodies carrying both an Anthropic-style top-level `type` and an OpenAI-style `error.message`/`error.type`, plus the selected profile and request ID; no secrets, no stack traces.
- [X] T030 Create `internal/relay/proxy.go`: `httputil.ReverseProxy` with `FlushInterval = -1`, origin-plus-path URI construction (no `/v1` rewrite), immutable `Selection` type and `atomic.Pointer[Selection]`, per-request snapshot loaded once, pass-through of upstream status and body, no retry after any byte is forwarded, and client-disconnect cancellation.
- [X] T031 Create `internal/relay/server.go`: router reserving `/_shunt/status`, `/_shunt/healthz`, `/_shunt/readyz`, `/_shunt/version`, `/_shunt/ui`, and `/_shunt/api/*`; status, health, readiness, and version handlers; `http.Server` per listener generation; `MaxHeaderBytes` of 1 MiB; all other requests proxied.

### Foundational Tests and Smoke Check

- [X] T032 [P] Create `test/contract/relay_headers_test.go`: assert hop-by-hop and `Connection`-named headers are removed, end-to-end authorization and protocol headers survive, `Host` is rewritten, `X-Forwarded-For` is not added, and response trace headers are correct.
- [X] T033 [P] Create `test/contract/relay_streaming_test.go`: assert the first SSE byte reaches the client before the fake upstream finishes, frames flush without aggregation, compression is passed through untouched, request bodies are not fully read, and a long idle gap does not time out.
- [X] T034 [P] Create `test/contract/relay_errors_test.go`: assert 502/504 structured shapes, pass-through of 401/403/429/4xx/5xx bodies, no retry after forwarded bytes, and client-disconnect handling.
- [X] T035 [P] Create `test/contract/status_schema_test.go`: assert the full `/_shunt/status` schema, `healthz`, `readyz`, `version`, and absence of credentials or authorization values.
- [X] T036 Run `go test ./...`, `go vet ./...`, and `gofmt -l`; start `shunt serve` against `test/support/fake_upstream.go` and confirm a proxied request, an SSE response, and the status endpoints work.

**Checkpoint**: Foundation ready. US1, US2, and the shared parts of US3-US5 can begin.

---

## Phase 3: User Story 1 - Switch upstream without restarting the client (Priority: P1) MVP

**Goal**: A stable relay endpoint whose next request follows the operator's selected profile, while every in-flight stream finishes on its original upstream.

**Independent Test**: Start a long SSE response against the local profile, switch to the tailnet profile, verify the stream completes without truncation, then verify the next request reaches the tailnet profile within one second and no client was restarted.

### Tests for User Story 1 (write first, verify failure)

- [X] T037 [P] [US1] Create `test/integration/hot_switch_test.go`: 20/20 manual switches change the next request's upstream within 1 second; 50 switches with 10 active SSE streams produce zero truncations; both switch directions; the client base URL never changes.
- [X] T038 [P] [US1] Create `internal/relay/selection_test.go`: selection snapshots are immutable, a request holds its snapshot for the whole body, a later publish does not affect it, invalid state is ignored, and startup falls back to `default_profile`.

### Implementation for User Story 1

- [X] T039 [US1] Update `internal/relay/server.go`: state watcher polls `state.json` every 200 ms, reads only on mtime/size/revision change, publishes a new `Selection` through `atomic.Pointer[Selection]`, and logs-and-ignores missing, truncated, corrupt, or unknown-profile state.
- [X] T040 [US1] Update `internal/relay/proxy.go`: load the current `*Selection` exactly once at request start, pin the profile through response completion or disconnect, and never re-read state mid-body.
- [X] T041 [US1] Create `internal/cli/commands/use.go`: `shunt use <profile> [--force] [--no-probe]` validates the profile, probes it unless `--no-probe`, refuses a failed probe unless `--force`, writes manual mode with the selected profile, reason, and an incremented revision atomically, and waits up to 1 second for the relay to report the new effective profile.
- [X] T042 [US1] Update `internal/cli/commands/use.go`: implement `shunt switch on` and `shunt switch off` as aliases for `[switch].on_profile` (default `remote`) and `[switch].off_profile` (default `local`), using the same atomic state write and the same `--force` rule.
- [X] T043 [US1] Update `internal/cli/commands/serve.go` and `internal/relay/server.go`: wire the state watcher and selection pointer into the running relay; publish effective profile, last switch reason, and `switches_total` to `runtime.json`.
- [X] T044 [US1] Update `internal/relay/proxy.go`: in manual mode, an unhealthy selected profile returns the structured 502/504 error and is never replaced by another profile; only an explicit operator command changes the selection.
- [X] T045 [US1] Update `internal/state/state.go` and `internal/relay/server.go`: restore the last manual/auto mode and profile at startup; an unusable state falls back to manual mode plus `default_profile` and emits one warning.
- [X] T046 [US1] Update `internal/cli/commands/serve.go`: process shutdown honors `shutdown_grace_ms`; this timeout does not apply to listener rebinds or upstream switches.
- [X] T047 [US1] Run `go test ./test/integration -run HotSwitch` and `go test ./internal/relay -run Selection`; fix failures until T037 and T038 pass.
- [ ] T048 [US1] Run the live hot-switch scenario from `quickstart.md` against a real client and the fake upstream; record the observed switch latency and stream completion result.
  - **Deferred**: requires a real Claude/Codex/DSH client and the target workstation. The automated equivalent (`TestHotSwitch*`) covers 20/20 and 50-switch/10-stream pinning.

**Checkpoint**: US1 is the MVP. Manual switching is hot, in-flight streams are pinned, and manual mode never silently falls back.

---

## Phase 4: User Story 2 - Discover models and generate client configuration (Priority: P2)

**Goal**: Fetch and normalize the active upstream's model catalog in both dialects, label free/paid/unknown conservatively, and generate free-first configuration for Claude Code, Codex, and DeepSeek Harness.

**Independent Test**: With a reachable upstream, fetch the catalog, generate each client snippet, confirm it matches the contract, and confirm a request through the generated configuration reaches the active upstream. With no upstream, confirm stale-cache behavior and no invented identifiers.

### Tests for User Story 2 (write first, verify failure)

- [X] T049 [P] [US2] Create `internal/models/normalize_test.go`: Anthropic `data` arrays and OpenAI `models` objects; duplicate IDs; provider prefixes; missing display names, limits, capabilities, and cost; free/paid/unknown classification; raw-record preservation.
- [X] T050 [P] [US2] Create `internal/models/cache_test.go`: per-profile/per-dialect keys, 12-hour TTL, 168-hour stale window, atomic cache writes, stale marker, and no-cache fetch failure.
- [X] T051 [P] [US2] Create `test/integration/model_discovery_test.go`: both dialects, pagination with `has_more`/`last_id`, malformed and empty lists, 500 entries within 5 seconds cold and 100 ms from cache, `--via`, and `--refresh`.
- [X] T052 [P] [US2] Create `test/integration/client_config_test.go`: Claude Code, Codex, and DSH snippets match `contracts/client-config.md`; free-first default; no free model requires an explicit choice; backups, dry-run, and rollback for writes; DSH remains print-only.

### Implementation for User Story 2

- [X] T053 [US2] Create `internal/models/dialect.go`: Anthropic requests with `x-api-key` and `anthropic-version: 2023-06-01`; OpenAI requests with `Authorization: Bearer`; parse `data` arrays or `models` objects; follow `has_more`/`last_id` up to `models.max_pages` and `models.page_size`.
- [X] T054 [US2] Create `internal/models/normalize.go`: normalize identifier, display name, provider, context/output limits, capabilities, input modalities, reasoning efforts, lifecycle, cost/tier/free metadata, and preserve the raw record; classify explicit free/tier first, then zero cost as free, positive cost as paid, everything else unknown.
- [X] T055 [US2] Create `internal/models/cache.go`: cache key `{profile}:{dialect}`, `cache_ttl_hours` and `stale_ttl_hours`, atomic JSON writes under `%LOCALAPPDATA%\shunt\cache`, explicit stale marker, and cache invalidation on `--refresh`.
- [X] T056 [US2] Create `internal/cli/commands/models.go`: resolve the token from `--token-env`, `SHUNT_TOKEN`, `ANTHROPIC_API_KEY`, `ANTHROPIC_AUTH_TOKEN`, then `OPENAI_API_KEY`; never accept a token argument or print a token; support `--via active|<profile>`, `--dialect anthropic|openai`, `--refresh`, `--json`, `--for claude|codex|dsh`, `--api anthropic|openai|both`, `--model`, `--opus/--sonnet/--haiku/--fable`, `--legacy`, `--write`, `--dry-run`, and `--path`.
- [X] T057 [US2] Update `internal/cli/commands/models.go`: default to `http://127.0.0.1:8787/v1/models` through the relay; `--via <profile>` queries a profile directly; `--via active` uses the effective upstream; if no token is available, attempt the request unauthenticated and report 401/403 as an authentication problem with exit 2; network failure uses the stale-cache path.
- [X] T058 [US2] Update `internal/cli/commands/models.go`: default-select only the first free model in upstream order; never default-select paid or unknown; exit 3 when no free model exists and require `--model`; allow an explicit paid model with a warning.
- [X] T059 [P] [US2] Create `internal/clients/claude.go`: print a shell snippet by default and merge `settings.json` only with `--write`; set `ANTHROPIC_BASE_URL` to the relay origin, reuse `ANTHROPIC_API_KEY`, map the main model and opus/sonnet/haiku/fable aliases (including `ANTHROPIC_DEFAULT_FABLE_MODEL`), and never set `ANTHROPIC_AUTH_TOKEN`.
- [X] T060 [P] [US2] Create `internal/clients/codex.go`: emit `model`, `model_provider`, and a `[model_providers.shunt]` block with `base_url = "http://127.0.0.1:8787/v1"`, `env_key = "SHUNT_API_KEY"`, and `wire_api = "responses"`; use `internal/config/edit.go` to preserve comments and unrelated tables.
- [X] T061 [P] [US2] Create `internal/clients/dsh.go`: emit a merge-ready `cordis.patch.yml` fragment under `- id: llm-pi-ai`; one provider per protocol (`anthropic-messages` with origin base URL, `openai-completions`/`openai-responses` with `/v1`); stable lowercase hyphenated ids; `apiKeyEnv: SHUNT_TOKEN`; include the preserve-other-providers warning; refuse automatic writes.
- [X] T062 [US2] Create `internal/clients/write.go`: opt-in writes only, timestamped backup in `%LOCALAPPDATA%\shunt\backups`, preserve unrelated settings, atomic replace, dry-run output, and a rollback path that restores the exact previous file.
- [X] T063 [US2] Update `internal/cli/commands/models.go`: wire `--for claude|codex|dsh`, `--api`, the Claude alias flags, `--write`, `--dry-run`, and `--path`; reject `--for` with `--json`, `--write` with `--dry-run`, and DSH with `--write`; print the generated snippet by default.
- [X] T064 [US2] Validate the generated snippets against `contracts/client-config.md`; parse the Codex TOML with the existing TOML dependency, validate the DSH fragment with text/structure assertions plus `dsh --dump-config` when installed (no YAML library is added), and confirm each client accepts its snippet when present.
- [X] T065 [US2] Run `go test ./internal/models ./test/integration -run 'Model|ClientConfig'`; record cold-fetch and cache timings against SC-006 and structural acceptance against SC-007.

**Checkpoint**: Model discovery works in both dialects, the cache is honest about staleness, and all three client configurations are generated free-first.

---

## Phase 5: User Story 3 - Configure endpoints from the local GUI (Priority: P2)

**Goal**: Serve an embedded, authenticated GUI that edits the entry IP/port and each profile's upstream host/port, rebinds or hot-applies live, and keeps loopback as the default with an explicit LAN opt-in.

**Independent Test**: Open `/_shunt/ui`, change the entry port, confirm the listener moves and the page reconnects; change a profile's upstream port with a live stream in flight, confirm the next request uses the new endpoint and the stream completes; attempt a wildcard or unconfirmed LAN bind and confirm rejection.

### Tests for User Story 3 (write first, verify failure)

- [X] T066 [P] [US3] Create `test/contract/gui_api_test.go`: token required on every mutation and rejected with 403 `ui_token_invalid`; `Origin`, `Host`, and `Sec-Fetch-Site` checks; no CORS headers; 64 KiB body limit; CSP, `X-Frame-Options: DENY`, and `Cache-Control: no-store`.
- [X] T067 [P] [US3] Create `test/integration/gui_rebind_test.go`: 20/20 entry-port rebinds serve the next request within 1 second; page reconnects; profile-port change with a live stream produces zero truncation; wildcard and unconfirmed LAN binds are rejected 100%; bind failure keeps the old listener; config-write failure returns 500 `config_write_failed`.

### Implementation for User Story 3

- [X] T068 [US3] Create `internal/ui/ui.go`: `embed.FS` for assets, per-run 256-bit token from `crypto/rand`, token injected into the page `<meta name="shunt-ui-token">`, `X-Shunt-UI-Token` validation, `Origin`/`Host`/`Sec-Fetch-Site` checks, CSP, frame denial, no-store, and no CORS.
- [X] T069 [US3] Create `internal/ui/assets/index.html`: Overview, Endpoints, and Models views; editable entry IP/port; per-profile scheme/host/port; effective profile, health, in-flight count, counters, last switch reason; LAN warning banner; no credential fields.
- [X] T070 [US3] Create `internal/ui/assets/app.js`: send `X-Shunt-UI-Token`, render status and interfaces, submit entry/profile changes, show the LAN confirmation modal, show the client-impact warning, reconnect after an entry rebind, and render structured API errors without HTML injection.
- [X] T071 [US3] Create `internal/ui/assets/style.css`: accessible forms and tables, warning states, responsive layout, no external fonts or CDN.
- [X] T072 [US3] Create `internal/ui/api.go`: `GET /_shunt/api/overview` returns status plus editable configuration and local-interface addresses; no secrets; includes the bound interface and LAN posture.
- [X] T073 [US3] Update `internal/ui/api.go`: `POST /_shunt/api/entry` parses IP/port, accepts only loopback or a specific private address assigned to a local interface, rejects wildcard/public/unassigned with 422 `invalid_entry`, requires `confirm_lan: true` for non-loopback and returns 403 `lan_not_confirmed` otherwise, returns 409 `bind_failed` or 500 `config_write_failed` with the old listener still active, and implements the bind-first apply order.
- [X] T074 [US3] Create `internal/relay/supervisor.go`: bind the new `TcpListener` before touching the old one; write config after a successful bind; swap `http.Server` generations; stop accepting on the old socket; close idle keep-alives; call `Shutdown` without a deadline so active streams finish; keep the old listener on bind or config-write failure.
- [X] T075 [US3] Update `internal/ui/api.go`: `POST /_shunt/api/profiles/{id}/endpoint` parses an origin (422 `invalid_endpoint`), probes the new endpoint (422 `probe_failed`), writes `config.toml` atomically (500 `config_write_failed`), and publishes a new immutable profile table through `atomic.Pointer[ProfileTable]`; in-flight requests keep the old endpoint.
- [X] T076 [US3] Update `internal/relay/server.go` and `internal/relay/supervisor.go`: configuration watcher polls every second; external edits are validated and reloaded; invalid edits are logged and ignored; a profile endpoint edit is probed before publication; an external listen change uses the bind-first procedure; a failed bind keeps the old listener, logs the failure, and does not retry until the file changes again.
- [X] T077 [US3] Update `internal/ui/api.go`: `POST /_shunt/api/switch` and `POST /_shunt/api/mode` reuse the state module; manual switch takes precedence; auto mode records the preferred profile; validate profile names and reject unknown ones.
- [X] T078 [US3] Update `internal/ui/api.go`: `GET /_shunt/api/models` and `POST /_shunt/api/models/refresh`; refresh resolves the token from the relay process environment only; no token returns 409 `credential_unavailable`; upstream 401/403 returns 502 `upstream_auth_failed`; stale cache remains readable.
- [X] T079 [US3] Update `internal/ui/api.go` and `internal/ui/assets/`: persistent, non-dismissible LAN warning naming the interface, stating that LAN hosts can reach the GUI, change configuration, and consume upstream quota, and that the entry listener is plain HTTP on a trusted network.
- [X] T080 [US3] Update `internal/ui/api.go` and `internal/ui/assets/app.js`: every entry change returns the new origin and a client-impact notice; the page navigates to the new origin and shows a short old-origin redirect notice where practical.
- [X] T081 [US3] Create `internal/cli/commands/ui.go`: `shunt ui [--open]` resolves the current entry origin, prints `http://<listen>/_shunt/ui`, and opens the default browser with `--open` or when `ui.open_browser = true`; exits 5 when the relay is not reachable; the token remains page-only and is never printed.
- [X] T082 [US3] Create `internal/config/edit_test.go`: assert GUI edits preserve comments, ordering, unrelated tables, and reject ambiguous or duplicate keys; cover `relay.listen`, `relay.allow_non_loopback`, `base_url`, and `probe_url`.
- [X] T083 [US3] Update `internal/ui/api.go` and `internal/ui/assets/app.js`: overview shows effective profile, per-profile health, in-flight count, and last switch reason; switch and mode controls work; auto/manual state is visible.
- [X] T084 [US3] Run `go test ./test/contract -run GUIAPI` and `go test ./test/integration -run GUIRebind`; fix failures until T066 and T067 pass and SC-011 evidence is recorded.

**Checkpoint**: The GUI edits both endpoint layers live, rejects unsafe binds, and never exposes credentials.

---

## Phase 6: User Story 4 - Automatic health-based fallback (Priority: P3)

**Goal**: Probe the preferred profile, fail over after consecutive failures, recover after consecutive successes, and always let a manual selection take precedence.

**Independent Test**: In auto mode with the local profile preferred, stop the local upstream, observe new requests move to the tailnet profile after three failures, restart the local upstream, and observe new requests return after two successes within 30 seconds. Then select a profile manually and confirm auto movement stops.

### Tests for User Story 4 (write first, verify failure)

- [X] T085 [P] [US4] Create `test/integration/auto_failover_test.go`: three failures demote the preferred profile; two successes restore it within 30 seconds; both unhealthy return a structured 503 while probing continues; a manual selection stops automatic movement; no request is retried mid-body.

### Implementation for User Story 4

- [X] T086 [US4] Update `internal/upstream/probe.go`: add the per-profile automatic state machine with jittered 10-second probes, configurable failure/recovery thresholds, and healthy/demoted transitions.
- [X] T087 [US4] Update `internal/relay/proxy.go` and `internal/upstream/probe.go`: connect, DNS, and TLS failures on real requests increment the same failure counter as probes; HTTP 4xx/5xx responses do not.
- [X] T088 [US4] Update `internal/relay/proxy.go` and `internal/relay/server.go`: in auto mode choose the preferred profile while healthy, otherwise the healthy fallback with the lowest numeric priority; if none is healthy, keep the last known good (or preferred) and return the structured unavailable error.
- [X] T089 [US4] Create `internal/cli/commands/mode.go`: `shunt mode manual|auto [--prefer <profile>]`; `auto` without `--prefer` keeps the current preferred profile or the default; `shunt use <profile>` sets manual and stops automatic movement until auto mode is explicitly restored.
- [X] T090 [US4] Update `internal/relay/server.go` and `internal/runtime/runtime.go`: start and stop the probe loop with the process; publish health transitions, effective profile, and last switch reason atomically.
- [X] T091 [US4] Update `internal/relay/errors.go`: return 503 `shunt_no_healthy_upstream` when auto mode has no healthy profile, keep probing, and never crash or hot-loop.
- [X] T092 [US4] Update `internal/ui/api.go` only if not already covered by T077/T083: expose auto mode, preferred profile, and transition reasons in the GUI; manual override remains one click.
- [X] T093 [US4] Run `go test ./test/integration -run AutoFailover`; fix failures until T085 passes and record SC-008 and the manual-mode SC-005 evidence.

**Checkpoint**: Auto mode fails over and recovers without mid-request retries, and manual mode remains authoritative.

---

## Phase 7: User Story 5 - Diagnose and operate safely (Priority: P4)

**Goal**: Inspect state, health, in-flight count, and last switch reason; diagnose configuration, bind, local/tailnet reachability, TLS, authentication, and model listing with safe next actions; keep credentials out of every artifact.

**Independent Test**: Introduce one fault at a time (wrong port, stopped upstream, invalid certificate, missing credential, unwritable directory) and confirm `shunt doctor` identifies it, proposes only safe actions, and returns the documented exit code.

### Tests for User Story 5 (write first, verify failure)

- [X] T094 [P] [US5] Create `test/integration/doctor_test.go`: wrong port, stopped upstream, invalid certificate, missing credential, unwritable state/cache/log directory, and an active Funnel warning; assert PASS/WARN/FAIL output and exit codes 0/1/2.
- [X] T095 [P] [US5] Create `test/integration/security_test.go`: grep logs, state, runtime, cache, and generated config for credentials and authorization values; assert header redaction, no body logging, no secret in `/_shunt/status`, and no `--insecure` path.

### Implementation for User Story 5

- [X] T096 [US5] Create `internal/cli/commands/status.go`: query the configured `relay.listen` with a 500 ms timeout, fall back to `runtime.json.listen` (bracketed IPv6), then report the relay down with the last persisted state; support a compact table, `--json`, and `--live` (exit 5 when the live endpoint is required but unavailable).
- [X] T097 [US5] Create `internal/cli/commands/doctor.go`: `shunt doctor [--json] [--profile <id>]` checks config parse/validation, bind rules and LAN flag, state/runtime readability, port ownership, local probe, tailnet DNS/TCP/TLS/HTTP, authenticated model listing when a token exists, and directory writability; every failure gets a safe next action.
- [X] T098 [US5] Update `internal/cli/commands/doctor.go`: when `tailscale` exists, check `tailscale status` for the local node and MSI peer and warn on active Funnel; never suggest Funnel, firewall disablement, public bind, or TLS bypass; include the NordVPN Meshnet diagnostic note.
- [X] T099 [US5] Update `internal/cli/cli.go` and add `internal/cli/commands/version.go` if needed: `shunt version [--json]` prints binary version, Go version, schema version, and build target without secrets.
- [X] T100 [US5] Create `internal/platform/service.go`: per-user `schtasks` install/uninstall for login startup; no elevation; on failure print the exact manual command and do not retry with elevated privileges.
- [X] T101 [US5] Create `internal/cli/commands/install.go`: `shunt install [--start-at-login] [--start-now]` and `shunt uninstall`; validate the executable path, report the scheduled task name, and never delete configuration, state, caches, or logs on uninstall.
- [X] T102 [US5] Update `internal/logging/logging.go`: size-based log rotation, retention, redaction verification, and debug-level client-disconnect logging without bodies or credentials.
- [X] T103 [US5] Update `internal/relay/server.go`: include the applied state revision and effective profile in status; keep the endpoint read-only and free of secrets.
- [X] T104 [US5] Run `go test ./test/integration -run 'Doctor|Security'`; fix failures until T094 and T095 pass and SC-009 evidence is recorded.

**Checkpoint**: Status and diagnostics explain failures with evidence and safe actions; no artifact contains a credential.

---

## Phase 8: Polish & Cross-Cutting Concerns

**Purpose**: Release documentation, performance and soak evidence, security audit, and the final acceptance matrix.

- [X] T105 [P] Update `README.md`: install, build, `shunt init`, `shunt serve`, GUI, LAN warning, model discovery, client configuration, diagnostics, and safe troubleshooting; link `specs/001-hot-upstream-switch/quickstart.md`.
- [X] T106 Run `gofmt -w ./cmd ./internal ./test`, then `gofmt -l .`, `go vet ./...`, and `go test ./...`; fix every finding.
- [ ] T107 Run the Linux CI race job locally or in CI with `CGO_ENABLED=1 go test -race ./...`; fix every race.
  - **Deferred**: the local Windows host has no `gcc`, so `-race` cannot build here (`cgo: C compiler "gcc" not found`). `.github/workflows/ci.yml` runs this job on `ubuntu-latest`; it must be confirmed green in CI.
- [ ] T108 Benchmark added median and p95 latency and first-streamed-byte latency against SC-003; record the commands and results in `README.md`.
  - **Deferred**: no representative target hardware or real upstream here, so latency/p95 numbers would not be meaningful. The contract tests assert first-byte streaming behavior, not numeric latency.
- [ ] T109 Measure idle resident memory, cold start, and release binary size against SC-004; record the results in `README.md`.
  - **Partial**: release binary size was measured (`8,363,008` bytes, `CGO_ENABLED=0`, including the tray/webview integration). Idle resident memory and cold start require the target workstation and are deferred.
- [ ] T110 Run the 24-hour soak test (`SHUNT_SOAK=1 go test ./test/integration -run Soak`) with 10,000 requests and 200 switches; record crash/stuck-request/memory-growth results.
  - **Partial**: the gated soak harness is implemented and a reduced smoke run passed (200 requests, 10 switches, 5 concurrent streams, `387 ms`, no truncation). The full 24-hour run requires the target workstation and is deferred.
- [ ] T111 Execute scenarios 1-11 in `quickstart.md` end-to-end on the target workstation and the real `msi.tailc56568.ts.net` path; record PASS/FAIL and evidence.
  - **Deferred**: requires the target workstation, Tailscale mesh, and real Claude/Codex/DSH clients. The automated suite covers the equivalent local behavior (hot switch, failover, discovery, client config, GUI rebind, doctor, security).
- [X] T112 Build the release with `CGO_ENABLED=0 go build -trimpath -ldflags="-s -w" -o shunt.exe ./cmd/shunt`; confirm no cgo and one external module in `go list -m all`.
- [X] T113 Run the final security audit using `test/integration/security_test.go` and a manual grep of `%LOCALAPPDATA%\shunt\`: no credential in logs/state/runtime/cache/config, no Funnel, no wildcard bind, no public bind, no TLS bypass, no CORS grant, no secret in status.
- [X] T114 Complete the SC-001 through SC-011 acceptance matrix in `specs/001-hot-upstream-switch/tasks.md` and record the evidence paths in `README.md`; close every task and leave no unchecked item without an explicit deferral note.
- [X] T115 [P] Create `internal/desktop/desktop.go`, `tray_windows.go`, and `tray_other.go`: a standard-library-only Windows tray icon with Open dashboard, Copy URL, per-profile switching, live status tooltip, and Quit; no new external module and no cgo.
- [X] T116 [P] Create `internal/desktop/window_windows.go` and `window_other.go`: open the dashboard in a chromeless Edge/Chrome app window with a default-browser fallback, plus `shunt ui --webview`.
- [X] T117 Update `internal/cli/commands/serve.go`: start the tray with the relay, expose `--no-tray`, and keep serving when the tray is unavailable (headless CI or no interactive shell).
- [X] T118 Create `start-shunt.bat`: build on first run, initialize the configuration when missing, start the relay, open the dashboard window, and reuse an already-running relay; document it in `README.md`.
- [X] T119 [P] Add the GUI **Clients** tab and `GET /_shunt/api/clients`, `POST /_shunt/api/clients/preview`, and `POST /_shunt/api/clients/write`: free-first model selection, non-secret Claude Code/Codex/DSH generation, atomic writes with timestamped backups, DSH preview-only, and no credential or unrelated existing value in any response.
- [X] T120 Add `GET /_shunt/api/clients/backups` and `POST /_shunt/api/clients/restore` plus the **Backups** panel in each writable client card: newest-first listing, token-gated restore, bare-name/path-traversal validation, a safety backup before overwrite, DSH refusal, and contract tests for listing, restore, missing token, traversal, and preview-only.
- [X] T121 Add English/French localization to the embedded GUI: `i18n.js` dictionaries and helpers, `data-i18n` labels, `t()` for dynamic text and known API error codes, browser-language detection with `shunt-lang` persistence, an in-place language toggle that re-renders the overview, models, client, and backup panels, the `/_shunt/ui/i18n.js` asset route, and contract tests for the module and selector.

---

## Dependencies & Execution Order

### Phase Dependencies

- **Setup (Phase 1)**: no dependencies; start immediately.
- **Foundational (Phase 2)**: depends on Setup; blocks every user story.
- **User Story 1 (Phase 3)**: depends on Foundational; MVP.
- **User Story 2 (Phase 4)**: depends on Foundational; independent of US1 and can run in parallel with it.
- **User Story 3 (Phase 5)**: depends on Foundational and US1 (state/switch module); can run in parallel with US2.
- **User Story 4 (Phase 6)**: depends on Foundational and US1; can run in parallel with US2/US3 if staffed.
- **User Story 5 (Phase 7)**: depends on Foundational, US1, and the health/runtime output from US4; status basics can start earlier.
- **Polish (Phase 8)**: depends on all desired user stories.

### User Story Dependencies

- **US1 (P1)**: no story dependency after Foundational; delivers the MVP.
- **US2 (P2)**: no story dependency; only shares config/cache paths.
- **US3 (P2)**: reuses US1's selection/state module and runtime counters; does not require US2.
- **US4 (P3)**: reuses US1's selection module and the probe primitive; does not require US3.
- **US5 (P4)**: reuses runtime observations from US1/US4 and the security posture from US3.

### Within Each Story

- Tests first; confirm they fail for the intended reason.
- Models and parsers before services; services before handlers.
- Endpoint changes before GUI wiring; CLI wiring before acceptance runs.
- A story checkpoint is complete only when its independent test passes.

### Parallel Opportunities

- Setup: T005-T011 are different files and can run together after T002-T004.
- Foundational: T014-T019 (config/state/runtime), T023-T029 (upstream/relay primitives), and T032-T035 (contract tests) are separable by file.
- US1: T037 and T038 are independent test files.
- US2: T049-T052 are independent test files; T059-T061 are independent client generators.
- US3: T066 and T067 are independent test files; T069-T071 are independent assets.
- US4: T085 can run alongside US2 or US3 once US1 is stable.
- US5: T094 and T095 are independent test files; T096, T099, T100, T101, and T102 touch different files.

---

## Parallel Example: User Story 1

```text
Task: "T037 [P] [US1] Create test/integration/hot_switch_test.go ..."
Task: "T038 [P] [US1] Create internal/relay/selection_test.go ..."
```

## Parallel Example: User Story 2

```text
Task: "T049 [P] [US2] Create internal/models/normalize_test.go ..."
Task: "T050 [P] [US2] Create internal/models/cache_test.go ..."
Task: "T051 [P] [US2] Create test/integration/model_discovery_test.go ..."
Task: "T052 [P] [US2] Create test/integration/client_config_test.go ..."
Task: "T059 [P] [US2] Create internal/clients/claude.go ..."
Task: "T060 [P] [US2] Create internal/clients/codex.go ..."
Task: "T061 [P] [US2] Create internal/clients/dsh.go ..."
```

## Parallel Example: User Story 3

```text
Task: "T066 [P] [US3] Create test/contract/gui_api_test.go ..."
Task: "T067 [P] [US3] Create test/integration/gui_rebind_test.go ..."
Task: "T069 [P] [US3] Create internal/ui/assets/index.html ..."
Task: "T070 [P] [US3] Create internal/ui/assets/app.js ..."
Task: "T071 [P] [US3] Create internal/ui/assets/style.css ..."
```

---

## Implementation Strategy

### MVP First (User Story 1 Only)

1. Complete Phase 1: Setup.
2. Complete Phase 2: Foundational.
3. Complete Phase 3: US1.
4. Stop and validate: 20/20 switches, 50 switches with 10 live streams, zero truncation.
5. Use the MVP for a real session before adding convenience features.

### Incremental Delivery

1. Setup + Foundational -> foundation ready.
2. US1 -> hot switch MVP.
3. US2 -> model discovery and client configuration.
4. US3 -> embedded GUI and live endpoint edits.
5. US4 -> automatic failover and recovery.
6. US5 -> diagnostics, status, startup, and security evidence.
7. Polish -> performance, soak, release, and acceptance matrix.

### Parallel Team Strategy

- One developer completes Setup and Foundational.
- After Foundational: Developer A takes US1; Developer B takes US2; Developer C prepares US3 assets and tests.
- After US1: US3 and US4 can proceed in parallel; US5 follows US4's runtime output.
- Polish is serial because every measurement depends on the final binary.

---

## Notes

- `[P]` tasks touch different files and have no incomplete dependency.
- Tests are mandatory for this feature because the constitution and plan require them.
- Do not mark a story checkpoint complete while its independent test fails.
- Commit after each logical group; do not commit generated binaries or local data directories.
- No task authorizes a public listener, Tailscale Funnel, firewall changes, TLS bypass, or credential storage.
- Entry IP/port changes are client-visible reconfigurations; upstream switches never move the entry.
- The GUI token is CSRF protection, not user authentication against LAN hosts; the LAN warning must say so.

---

## Acceptance Matrix (T114)

Measured on Windows 11, Go 1.26.1, `GOOS=windows GOARCH=amd64 CGO_ENABLED=0`.

| Criterion | Status | Evidence |
|-----------|--------|----------|
| SC-001 | PASS | `test/integration/hot_switch_test.go` `TestHotSwitchNextRequestFollowsSelection`: 20/20 switches observed within 1 s, no client restart. |
| SC-002 | PASS | `test/integration/hot_switch_test.go` `TestHotSwitchPinsInflightStreams`: 50 switches with 10 active streams, zero truncations. |
| SC-003 | DEFERRED | Requires the target workstation. Behavioral coverage: `test/contract/relay_streaming_test.go` (first byte flush, idle gaps, no aggregation). No numeric median/p95 recorded. |
| SC-004 | PARTIAL | Release binary: `8,320,000` bytes (limit 10 MB). Idle RSS and cold start require the target workstation and are deferred. |
| SC-005 | PASS | `test/integration/hot_switch_test.go` `TestManualUnhealthyDoesNotFallBack`; `test/integration/auto_failover_test.go` manual-override case. |
| SC-006 | PASS | `test/integration/model_discovery_test.go`: 500 entries cold within 5 s (2 pages) and from cache within 100 ms, both dialects. |
| SC-007 | PARTIAL | `test/integration/client_config_test.go` and `test/contract/gui_clients_test.go` validate Claude Code, Codex, and DSH output against `contracts/client-config.md`, including the GUI preview/write/backup-list/restore path, safety backups, and path-traversal rejection. Acceptance by the real installed clients is deferred with T111. |
| SC-008 | PASS | `test/integration/auto_failover_test.go`: failover after three failures, recovery after two successes within the 30 s bound, all-unhealthy 503. |
| SC-009 | PASS | `test/integration/security_test.go`: no secret in logs, state, runtime, cache, generated configs, or status; credentials still forwarded. |
| SC-010 | DEFERRED | Gated soak harness `test/integration/soak_test.go`; reduced smoke (200 requests, 10 switches, 5 streams) passed in 387 ms with zero truncation. Full 24-hour run requires the target workstation. |
| SC-011 | PASS | `test/integration/gui_rebind_test.go`: 20/20 rebinds serve within 1 s, live stream survives a profile endpoint change, wildcard/LAN/privileged binds rejected, bind and config-write failures preserve the old listener. |
