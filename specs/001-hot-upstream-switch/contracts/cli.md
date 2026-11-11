# Contract: `shunt` Command-Line Interface

**Feature**: `001-hot-upstream-switch`
**Date**: 2026-11-11

## Global Options

| Option | Applies to | Meaning |
|--------|------------|---------|
| `--config <path>` | all commands | Configuration file. Defaults to the platform path. |
| `--json` | `status`, `doctor`, `models`, `version` | Machine-readable output. |
| `--no-color` | all human output | Disable ANSI color. |
| `--help`, `-h` | all | Usage. |
| `--version`, `-V` | all | Version and schema. |

The process reads no credential from command-line arguments. Tokens are resolved from
environment variables only.

## Commands

### `shunt init [--force]`

Create the default configuration, state, and directories. Without `--force`, refuse to overwrite
an existing configuration. Print the paths and the two default profiles.

### `shunt serve [--foreground] [--no-ui]`

Run the relay in the foreground. This is the only long-running command. `--foreground` is
accepted for clarity; there is no implicit daemon mode. `--no-ui` disables the embedded GUI for
this run without changing the config. Startup errors (invalid config, port in use, invalid bind
address) exit non-zero with an actionable message.

### `shunt ui [--open]`

Print the GUI URL for the running relay (`http://<listen>/_shunt/ui`). With `--open`, launch the
default browser. Without `--open`, respect `ui.open_browser` from the config; the default is
false. Exit 5 if the relay is not reachable. The command does not start the relay and does not
bind anything itself.

### `shunt use <profile> [--force] [--no-probe]`

Select a profile in manual mode.

- Validate that the profile exists and is enabled.
- Probe the profile unless `--no-probe` is set. If the probe fails, refuse unless `--force`.
- Write `state.json` atomically with `mode = manual`, `selected = <profile>`, and an incremented
  revision.
- If the relay is running, wait up to 1 second for `/_shunt/status` to report the new effective
  profile; if it does not, print a warning and the observed state.
- If the relay is not running, print that the state is saved and will apply on the next start.
- The GUI's switch control performs the same validation and writes the same state record; the
  two paths must not diverge.

### `shunt switch on|off [--force]`

Aliases for `shunt use <switch.on_profile>` and `shunt use <switch.off_profile>`. Defaults are
`on -> remote` and `off -> local`. The aliases set manual mode.

### `shunt mode manual|auto [--prefer <profile>]`

- `manual`: set `mode = manual`; the existing `selected` is used. If none exists, use the
  configuration default.
- `auto --prefer <profile>`: validate the profile, set `mode = auto` and `preferred = <profile>`.
- `auto` without `--prefer`: keep the current preferred profile or use the configuration default.
- A manual `shunt use` always switches back to manual mode.

### `shunt status [--json] [--live]`

- Default: query `http://<relay.listen>/_shunt/status` with a 500 ms timeout, using the configured
  entry address and port (bracketed for IPv6). A changed entry port is therefore reflected without
  editing the command. If that address is unreachable, try `runtime.json.listen` before reporting
  the relay as not running; this covers an external listen edit whose bind failed.
- If the relay is not reachable: print `relay: not running`, then the last `runtime.json`
  snapshot and the persisted intent.
- `--live`: require the live endpoint; exit 5 if it is unavailable.
- Human output includes mode, selected/preferred/effective, per-profile health, in-flight count,
  counters, and the last switch reason.
- `--json` uses the status schema from `contracts/relay-http.md`.

### `shunt doctor [--json] [--profile <id>]`

Run the checks listed in the plan and print `PASS`, `WARN`, or `FAIL` with a safe next action.
`--profile` restricts the upstream checks to one profile; the default checks all enabled
profiles. Exit codes: 0 all pass, 1 warnings, 2 failures.

### `shunt models [options]`

Retrieve and display the model catalog.

| Option | Meaning |
|--------|---------|
| `--via active` | Query the relay at the effective upstream (default). |
| `--via <profile>` | Query the named profile directly, bypassing the relay. |
| `--dialect anthropic\|openai` | Force a listing dialect. Default depends on `--for`; otherwise `anthropic`. |
| `--refresh` | Ignore a fresh cache and fetch again. |
| `--json` | Print the normalized catalog. |
| `--for claude\|codex\|dsh` | Generate client configuration instead of a table. |
| `--api anthropic\|openai\|both` | DSH only: which protocol providers to emit. Default `both`. |
| `--model <id>` | Main model. If omitted, the first free model. |
| `--opus <id>`, `--sonnet <id>`, `--haiku <id>`, `--fable <id>` | Claude Code alias overrides. |
| `--legacy` | Also emit `ANTHROPIC_SMALL_FAST_MODEL`. |
| `--write` | Write the generated configuration (Claude/Codex only). |
| `--dry-run` | Print the proposed content and do not write. |
| `--path <path>` | Override the client configuration destination. |
| `--token-env <name>` | Environment variable holding the token. Default `SHUNT_TOKEN`, with fallbacks. |

Token resolution order: `--token-env` value, `SHUNT_TOKEN`, `ANTHROPIC_API_KEY`,
`ANTHROPIC_AUTH_TOKEN`, `OPENAI_API_KEY`. The token is never printed. If no token is available,
the request is attempted without auth and a 401/403 is reported as an authentication problem.

`--for` cannot be combined with `--json`. `--write` cannot be combined with `--dry-run`.
`--write` is rejected for DSH; DSH output is print-only in v1.

### `shunt config path|show|validate|restore <backup>`

- `path`: print the configuration, state, runtime, cache, and log paths.
- `show`: print the effective configuration with secrets absent (there are none).
- `validate`: parse and validate, then exit 0/2.
- `restore <backup>`: copy a client-config backup over its target, after creating a new backup.

### `shunt install [--start-at-login] [--start-now]`

Create a per-user Task Scheduler entry that runs `shunt serve` at login. Do not request
elevation. If `schtasks` fails, print the exact command for manual execution. `--start-now` starts
the task immediately.

### `shunt uninstall`

Delete the Task Scheduler entry. Do not delete configuration, state, caches, or logs.

### `shunt version [--json]`

Print the binary version, schema version, and build target.

## Exit Codes

| Code | Meaning |
|------|---------|
| 0 | Success. |
| 1 | Generic failure. |
| 2 | Usage, configuration, or validation error. |
| 3 | No free model available; explicit `--model` required. |
| 4 | Upstream unreachable or probe failed (unless `--force`). |
| 5 | Relay not running where a live response was required. |
| 6 | Write refused (invalid target, failed backup, unsupported client write). |
| 7 | Relay already running or port in use. |

## Output Rules

- Human output is concise and stable enough to copy into a terminal.
- `--json` is a single JSON document on stdout with no log lines mixed in.
- Errors go to stderr; normal output goes to stdout.
- Log lines include a timestamp, level, event, and request id where applicable. They never
  contain credentials or bodies.
- `--dry-run` prints the proposed content with a clear `--- target ---` header and does not
  modify the file.

## Examples

```text
shunt init
shunt serve
shunt ui --open
shunt use remote
shunt switch off
shunt mode auto --prefer local
shunt status
shunt doctor
shunt models
shunt models --for claude
shunt models --for codex --model deepseek-v4-flash-free
shunt models --for dsh --api both --model deepseek-v4-flash-free
shunt models --for claude --write --dry-run
```
