# Quickstart: Validate the Hot-Switching Relay

**Feature**: `001-hot-upstream-switch`
**Date**: 2026-11-11

This is the acceptance walkthrough for the design. It assumes the binary has been implemented per
`plan.md`. It is also the manual test script used before release.

## Prerequisites

- Windows 10/11 x64 with Go 1.23 or newer. If Go is not installed:
  `winget install GoLang.Go`, then open a new terminal and check `go version`. No C toolchain is
  needed for the release build because it sets `CGO_ENABLED=0`; the optional `-race` test runs in
  Linux CI where cgo is available.
- The local proxy reachable at `http://127.0.0.1:4000`.
- Tailscale up, with `msi.tailc56568.ts.net` reachable over HTTPS on 443.
- A credential that works against both proxies.
- A current browser on the workstation for the GUI.
- Clients under test: Claude Code, Codex, and DSH, each already installed and working against the
  local proxy directly.

Do not run `tailscale funnel`, open a firewall port, or disable TLS verification at any point.

## Build and Initialize

```powershell
$env:CGO_ENABLED = '0'
go build -trimpath -ldflags="-s -w" -o .\shunt.exe .\cmd\shunt
.\shunt.exe version
.\shunt.exe init
.\shunt.exe config path
.\shunt.exe config validate
```

Expected: `version` prints the binary and schema versions; `init` creates `config.toml` with the
`local` and `remote` profiles and state/runtime/cache directories; `validate` exits 0.

The default configuration is:

```toml
[profiles.local]
base_url = "http://127.0.0.1:4000"

[profiles.remote]
base_url = "https://msi.tailc56568.ts.net"
```

Start the relay in one terminal:

```powershell
.\shunt.exe serve
.\shunt.exe ui --open
```

In another terminal:

```powershell
.\shunt.exe status
Invoke-RestMethod http://127.0.0.1:8787/_shunt/healthz
```

Expected: `status` shows `relay: running`, mode `manual`, effective profile `local` (or the
configured default). `healthz` returns 200 with `{"status":"ok"}`.

Confirm loopback-only binding:

```powershell
Get-NetTCPConnection -LocalPort 8787 -State Listen |
  Select-Object LocalAddress, LocalPort, OwningProcess
```

Expected: `LocalAddress` is `127.0.0.1` (or `::1`), never `0.0.0.0` or a LAN address.

## Desktop Launcher, Tray, and Theme

For a one-click Windows start, double-click `start-shunt.bat`. It builds `shunt.exe` when missing,
initializes the configuration on first run, starts `shunt serve` with its tray icon, and opens the
dashboard in a chromeless Edge or Chrome app window. `Ctrl+C` in its console stops the relay;
the tray menu also has **Quit Shunt** and **Open dashboard**.

```powershell
.\shunt.exe serve --no-tray
.\shunt.exe ui --webview
.\shunt.exe ui --open
```

Expected: `serve` adds a tray icon unless `--no-tray` is passed; the menu copies the dashboard URL,
switches profiles, shows the current profile/mode in its tooltip, and quits. `ui --webview` uses a
chromeless app window when Edge or Chrome exists and falls back to the default browser otherwise.

The **Theme** button in the header cycles `Auto`, `Dark`, and `Light`. The choice is stored in
`localStorage` under `shunt-theme` and applies before the first paint. It is a display preference
only; it is not written to configuration, state, or cache.

The **language** button in the header toggles English and French. The page starts in the browser
language (`navigator.language`) or the last manual choice, stored in `localStorage` under
`shunt-lang`. Like the theme, it is a display preference only and is never written to
configuration, state, or cache.

## Scenario 1: Manual Hot Switch

With the relay running and a client configured against it:

```powershell
.\shunt.exe use local
.\shunt.exe status
curl.exe -s http://127.0.0.1:8787/v1/models -H "Authorization: Bearer $env:SHUNT_TOKEN" |
  Select-Object -First 1

.\shunt.exe use remote
.\shunt.exe status
curl.exe -s http://127.0.0.1:8787/v1/models -H "Authorization: Bearer $env:SHUNT_TOKEN" |
  Select-Object -First 1
```

Expected:

- `status` reports the new effective profile within 1 second of each command.
- The second listing comes from the tailnet profile; the response's `X-Shunt-Profile` header is
  `remote`.
- No client process was restarted and no client environment variable changed.
- `state.json` contains `mode = manual`, the selected profile, and an incremented revision.

`shunt switch on` and `shunt switch off` are aliases for `use remote` and `use local`.

## Scenario 2: Switch With a Stream In Flight

This is the mandatory release test. Use the local proxy's streaming endpoint (or the fake upstream
from `test/support/` for a deterministic stream).

Terminal A:

```powershell
curl.exe -N -s http://127.0.0.1:8787/v1/messages `
  -H "x-api-key: $env:ANTHROPIC_API_KEY" `
  -H "anthropic-version: 2023-06-01" `
  -H "content-type: application/json" `
  -d '{\"model\":\"<free-model-id>\",\"max_tokens\":512,\"stream\":true,\"messages\":[{\"role\":\"user\",\"content\":\"Count from 1 to 200 slowly.\"}]}'
```

While tokens are arriving, Terminal B:

```powershell
.\shunt.exe use remote
.\shunt.exe status
```

Expected:

- The stream in Terminal A completes without truncation, with the same `X-Shunt-Profile` it started
  with.
- `status` reports in-flight count greater than zero during the stream and the new effective
  profile after the switch.
- The next request goes to `remote`.
- No `shunt_upstream_*` error appears for the in-flight request.

Repeat 20 times with a short stream and 50 switches; record zero truncated streams (SC-002).

## Scenario 3: Model Discovery

```powershell
.\shunt.exe models
.\shunt.exe models --json
.\shunt.exe models --via remote --dialect anthropic
.\shunt.exe models --via remote --dialect openai
```

Expected:

- Both dialects return the upstream's model list through the same `/v1/models` path.
- Each row is labeled `FREE`, `PAID`, or `?`.
- `--json` is a single document; counts add up to the total.
- `--refresh` bypasses a fresh cache; a second call within the TTL is served from cache and is
  visibly fast.
- The GUI Models view can refresh the catalog only when the relay process itself has a token in
  its environment. With no token, refresh reports `credential_unavailable`; it never borrows the
  credential of an in-flight client request. A loaded catalog also feeds the model suggestions in
  the Clients tab.
- Stopping the relay and running `shunt models` exits 5 with the `--via` hint.
- Pointing a profile at an unreachable host exits 4 and does not emit a client snippet.

If no free model is found, the command exits 3 and prints the available IDs. Do not substitute a
paid or unknown model automatically.

## Scenario 4: Claude Code

Generate and inspect first:

```powershell
.\shunt.exe models --for claude --dry-run
```

Expected: a shell snippet and a `settings.json` block with `ANTHROPIC_BASE_URL` set to
`http://127.0.0.1:8787`, a free model ID in every alias, and no token.

Apply the environment form in a new terminal, then:

```powershell
$env:ANTHROPIC_BASE_URL = 'http://127.0.0.1:8787'
claude -p "reply with ok"
```

Expected: the request appears in `shunt status`, `X-Shunt-Profile` matches the effective profile,
and the answer streams normally. Switch profiles and repeat without restarting the shell.

Check the failure mode: with `use remote` active and the tailnet path deliberately unavailable
(for example, the test fake returns a connection refusal), Claude Code receives a structured
`shunt_upstream_unavailable` error, not a silent answer from `local`.

## Scenario 5: Codex

```powershell
.\shunt.exe models --for codex --dry-run
.\shunt.exe models --for codex --write
codex exec "reply with ok"
```

Expected: `config.toml` gains `model`, `model_provider = "shunt"`, and
`[model_providers.shunt]` with `base_url = "http://127.0.0.1:8787/v1"`,
`env_key = "SHUNT_API_KEY"`, and `wire_api = "responses"`. Existing comments and unrelated tables
survive. A backup exists under the backups directory.

`SHUNT_API_KEY` must be set in the environment before Codex starts. If it is missing, Codex fails
before sending a request.

Switch profiles and run `codex exec` again in the same session. The new request uses the new
profile with no Codex restart.

## Scenario 6: DSH

```powershell
.\shunt.exe models --for dsh --api both
```

Expected: one YAML fragment overriding `- id: llm-pi-ai` with two providers,
`shunt-anthropic` (`anthropic-messages`, origin URL) and `shunt-openai` (`openai-completions`,
`/v1` URL), each with `apiKeyEnv: SHUNT_TOKEN` and at least one model object.

Merge the `providers` map into the existing `llm-pi-ai` row of
`$DSH_HOME\profiles\web\cordis.patch.yml`, preserving any other keys in that row's `config`.
Set `$env:SHUNT_TOKEN`, start DSH, open Settings → Models, and confirm both providers appear.

Select a model from each provider and send a message. Expected:

- Both requests reach the relay and appear in `shunt status`.
- The Anthropic request uses `/v1/messages`; the OpenAI request uses
  `/v1/chat/completions` (or `/v1/responses` for the responses provider).
- Switching the relay profile does not require restarting DSH; the next message uses the new
  upstream.

`shunt models --for dsh --write` exits 6 and prints the fragment. DSH writing is out of scope in v1.

## Scenario 7: Automatic Failover and Recovery

Use the deterministic fake upstream in `test/support/` so the test does not depend on the live
tailnet being down.

```powershell
.\shunt.exe mode auto --prefer local
.\shunt.exe status
# Stop the fake local upstream.
.\shunt.exe status
```

Expected:

- After 3 consecutive failed probes (about 30 seconds at the default 10-second interval), the
  effective profile moves to the next healthy profile and `last_switch.reason` names the probe
  failure.
- Requests that arrive during the failure window get a structured error or are routed to the
  healthy profile, never a silent hang.
- Restarting the fake upstream produces 2 consecutive successes and promotes back to `local`.
- `shunt status` shows the health counters and the last error for each profile.
- `shunt use <profile>` during auto mode switches to manual immediately.

Do not simulate failure by disabling the firewall, Tailscale, or TLS verification.

## Scenario 8: Doctor

```powershell
.\shunt.exe doctor
.\shunt.exe doctor --json
.\shunt.exe doctor --profile remote
```

Expected checks and outcomes:

| Check | Pass condition |
|-------|----------------|
| Config parse and schema | No unknown keys; loopback listen; TLS verification on |
| State file | Readable, valid schema, selected profile exists |
| Listen port | Free, or owned by this relay |
| Local probe | Reachable within the configured timeout |
| Tailnet DNS | `msi.tailc56568.ts.net` resolves |
| Tailnet TLS | Certificate valid for the hostname |
| Tailnet probe | Reachable within the timeout |
| Tailscale CLI | Installed; reports the node online |
| Funnel | Off; a warning if on, never a suggestion to enable it |
| Authenticated listing | 200 from `/v1/models` with the resolved token |
| Directories | Config/state/cache/log/backup writable |
| Secrets | No credential-shaped field in config, state, runtime, or cache |

Exit 0 when all pass, 1 when there are warnings only, 2 on any failure. A NordVPN/Meshnet
interface on `100.64.0.0/10` produces a warning, not an automatic change.

## Scenario 9: Security Checks

1. Start the relay, send one request, then search every durable file for the token:

   ```powershell
   $sentinel = 'SHUNT_SENTINEL_DO_NOT_LEAK'
   $env:SHUNT_TOKEN = $sentinel
   # send a request through the relay
   Get-ChildItem "$env:LOCALAPPDATA\shunt" -Recurse -File |
     Select-String -SimpleMatch $sentinel
   ```

   Expected: no matches in config, state, runtime, cache, or logs.

2. Set `relay.listen = "0.0.0.0:8787"` and start. Expected: exit 2, explicit refusal, no listener.
3. Set `tls_verify = false` on `remote`. Expected: exit 2, explicit refusal.
4. Send `CONNECT` to the relay. Expected: 405.
5. Request `/_shunt/status` from another machine while bound to loopback. Expected: connection
   refused. With a confirmed LAN bind, expect a response and the persistent LAN warning in the GUI.
6. Check logs for request bodies and headers. Expected: method, path, status, profile, request ID,
   byte counts, and duration only.

## Scenario 10: Soak

Run for 24 hours with at least 10,000 requests and 200 switches, including at least 50 concurrent
streams at peak. A PowerShell sketch:

```powershell
$deadline = (Get-Date).AddHours(24)
$i = 0
while ((Get-Date) -lt $deadline) {
  $i++
  if ($i % 50 -eq 0) {
    $p = if ((Get-Random -Maximum 2) -eq 0) { 'local' } else { 'remote' }
    .\shunt.exe use $p --no-probe | Out-Null
  }
  curl.exe -s -o NUL -w "%{http_code}`n" http://127.0.0.1:8787/v1/models `
    -H "Authorization: Bearer $env:SHUNT_TOKEN"
  Start-Sleep -Milliseconds 200
}
```

Expected: no crash, no unbounded memory growth (idle RSS returns to <= 25 MB), no truncated
streams, and `runtime.json` counters consistent with the workload. Force one relay restart during
the soak and confirm clients reconnect on their next request without reconfiguration.

## Scenario 11: GUI Entry and Profile Ports

Open the GUI:

```powershell
.\shunt.exe ui --open
```

Expected: the Overview view shows the entry address (`127.0.0.1:8787` by default), the effective
profile, per-profile health, and the in-flight count. The Endpoints view lists the loopback
addresses and the workstation's LAN addresses, never `0.0.0.0` or `::`.

**Entry port change**: with a streamed request in flight on 8787, change the entry port to 8788
and apply. Expected: the GUI first warns that clients configured for 8787 must be updated, then the
new listener binds first, the page reconnects to `http://127.0.0.1:8788/_shunt/ui`, the in-flight
stream completes, and a new request to 8788 succeeds. A new connection to 8787 is refused after
the old listener stops accepting. No process restart occurs. This is a client-visible endpoint
change, not an upstream switch; Claude Code and Codex keep using 8787 until their base URL is
updated.

**Profile port change**: point the local profile at a test listener on port 4001 (the fake
upstream), apply, and confirm the probe succeeds. Expected: the next request uses 4001, an
in-flight stream on 4000 finishes, and `runtime.json` records the new endpoint. Then point it at
an unused port, apply, and confirm a 422 probe error leaves 4001 active.

**LAN bind**: choose a LAN address, apply, and accept the confirmation modal. Expected: the page
is reachable at `http://<lan-ip>:8788/_shunt/ui`, the warning banner names the interface and states
that LAN hosts can change configuration and send requests through the relay, and
`allow_non_loopback = true` is in the config. If another device cannot connect, check Windows
Firewall yourself; the relay never changes firewall rules. Attempt `0.0.0.0`: rejected with
`invalid_entry`. Attempt a LAN address with `confirm_lan: false`: rejected with
`lan_not_confirmed`.

**GUI security**: send a mutation without `X-Shunt-UI-Token`: 403 `ui_token_invalid`. Send one
with a foreign `Origin`: 403 `ui_origin_invalid`. Search the logs, `state.json`, `runtime.json`,
and the cache for the token: no match. The token appears only in the served page.

**External edit**: with the GUI open, change a profile's port in `config.toml` by hand. Expected:
the watcher applies it within about 1 second, without a restart. Write an invalid value: the
change is ignored, the old value stays active, and the error is logged.

## Scenario 12: GUI Client Configuration

Open the **Clients** tab. It lists Claude Code, Codex, and DeepSeek Harness with the configured
target path and a `writes with backup` or `preview only` badge.

1. Click **Load models** on a card, then type a model id or accept a suggestion. Leaving the field
   empty uses the first free cached model. A model absent from the catalog, or a paid model, is
   accepted with a warning; it is never chosen implicitly.
2. Click **Preview**. Expected: the generated fragment contains the relay origin
   (`http://127.0.0.1:8787`) and the selected model, and no token. Claude Code also shows an
   environment-variable snippet.
3. For Claude Code, add an unrelated key and an `ANTHROPIC_API_KEY` to the existing
   `settings.json`, then preview again. Expected: the unrelated key and the credential do not
   appear in the page.
4. Click **Write configuration** and accept the confirmation. Expected: a timestamped backup is
   created under the backups directory, unrelated keys and comments survive, and the response
   names the target and backup. Repeat for Codex and confirm `#` comments survive.
5. In the card's **Backups** panel, click **Refresh**. Expected: the new backup is listed newest
   first with its size and timestamp. Click **Restore**, accept the confirmation, and check the
   file: the previous content is back and a new safety backup of the overwritten file now heads
   the list. Submitting `{"client":"claude","backup":"../escape.bak"}` returns `400
   backup_invalid`.
6. DSH has no write button and no backups panel. Copy the fragment and merge it into the existing
   `llm-pi-ai` row; `POST /_shunt/api/clients/write` for DSH returns `501
   client_write_unsupported`, and `GET /_shunt/api/clients/backups?client=dsh` returns `501
   client_restore_unsupported`.
7. Send a preview without `X-Shunt-UI-Token`: 403 `ui_token_invalid`. Send a restore without the
   token: same 403. Search the logs and response bodies for a credential: no match.

## Acceptance Mapping

| Criterion | Scenario |
|-----------|----------|
| SC-001 switch latency <= 1 s | 1 |
| SC-002 zero truncation across 50 switches / 10 streams | 2 |
| SC-003 added latency p50 <= 10 ms, p95 <= 25 ms | benchmark + 1 |
| SC-004 <= 25 MB idle, <= 200 ms start, <= 10 MB binary | build + 10 |
| SC-005 structured failure, no silent fallback | 4, 7 |
| SC-006 500 models <= 5 s cold, <= 100 ms cached | 3 |
| SC-007 generated config accepted first try | 4, 5, 6, 12 |
| SC-008 auto recovery <= 30 s | 7 |
| SC-009 no credentials in logs/state/cache | 9 |
| SC-010 24 h soak | 10 |
| SC-011 GUI entry/profile port changes | 11 |
