# Contract: Client Configuration Generation

**Feature**: `001-hot-upstream-switch`
**Date**: 2026-11-11

## Common Rules

All three clients are configured to point at the relay's stable loopback origin. None of them
knows which upstream is active.

| Client | Base URL | Protocol | Credential source |
|--------|----------|----------|-------------------|
| Claude Code | `http://127.0.0.1:8787` | Anthropic Messages | `ANTHROPIC_API_KEY` (`x-api-key`) |
| Codex | `http://127.0.0.1:8787/v1` | OpenAI Responses | `SHUNT_API_KEY` or an existing OpenAI key variable |
| DSH (Anthropic route) | `http://127.0.0.1:8787` | Anthropic Messages | `SHUNT_TOKEN` |
| DSH (OpenAI route) | `http://127.0.0.1:8787/v1` | OpenAI Chat Completions or Responses | `SHUNT_TOKEN` |

Rules:

- The relay forwards the client's credential unchanged. The same credential that authenticates
  against the local proxy or the tailnet proxy MUST work through the relay.
- No generated file contains a literal token.
- Base URL path rules are protocol-specific. Anthropic Messages appends `/v1/messages` to the
  origin; OpenAI appends `/chat/completions` or `/responses` to a base that already ends in
  `/v1`. Getting this wrong yields a 404, not an auth error.
- `ANTHROPIC_AUTH_TOKEN` MUST NOT be set for Claude Code. The relay selects the Anthropic dialect
  from `x-api-key`; an `Authorization: Bearer` header alone would be treated as OpenAI.
- Generated model IDs come from the live catalog (`shunt models`), not from a hard-coded list.
- `--write` creates a timestamped backup before modifying any existing file, preserves unrelated
  keys, and never rewrites a file it cannot parse.

## Claude Code

Claude Code reads `ANTHROPIC_BASE_URL` from its process environment or from the `env` block of
`~/.claude/settings.json`. It sends `ANTHROPIC_API_KEY` as `x-api-key`.

### Shell environment (recommended)

PowerShell profile (`$PROFILE`):

```powershell
$env:ANTHROPIC_BASE_URL = 'http://127.0.0.1:8787'
# Keep the ANTHROPIC_API_KEY you already use for the local proxy.
# Do not set ANTHROPIC_AUTH_TOKEN.
```

POSIX shell:

```sh
export ANTHROPIC_BASE_URL="http://127.0.0.1:8787"
# ANTHROPIC_API_KEY is already exported for the local proxy.
unset ANTHROPIC_AUTH_TOKEN
```

### settings.json (non-secret keys only)

```json
{
  "env": {
    "ANTHROPIC_BASE_URL": "http://127.0.0.1:8787",
    "ANTHROPIC_MODEL": "<free-model-id>",
    "ANTHROPIC_DEFAULT_OPUS_MODEL": "<free-model-id>",
    "ANTHROPIC_DEFAULT_SONNET_MODEL": "<free-model-id>",
    "ANTHROPIC_DEFAULT_HAIKU_MODEL": "<free-model-id>",
    "ANTHROPIC_DEFAULT_FABLE_MODEL": "<free-model-id>"
  }
}
```

Notes:

- `ANTHROPIC_API_KEY` is intentionally absent from `settings.json`. Keep it in the environment.
  If a key must be supplied by a helper, use Claude Code's `apiKeyHelper` setting, not a literal
  in the file.
- If `ANTHROPIC_BASE_URL` is already set in the shell to the local proxy, update that one value
  instead of setting it in two places. A value in `settings.json` and a conflicting shell value
  is a support problem, not a feature.
- All aliases may point at the same model. The alias variables exist so Claude Code's internal
  model choices resolve to IDs the upstream actually serves.
- `ANTHROPIC_SMALL_FAST_MODEL` is legacy. Emit it only with `--legacy`.
- `API_TIMEOUT_MS` is not required. The relay imposes no body timeout, so long streams are not
  cut by the relay; the client's own timeout still applies.

### Verification

```powershell
claude --version
claude -p "reply with ok"
```

The request MUST appear in `shunt status` (`requests_total` increments) and in the relay log with
the effective profile. A 404 means the base URL has a path where the origin was expected.

## Codex

Codex reads `~/.codex/config.toml` (or `$CODEX_HOME/config.toml`). A custom provider is declared
under `[model_providers.<id>]`; the base URL includes `/v1` and Codex appends `/responses`.

```toml
model = "<free-model-id>"
model_provider = "shunt"

[model_providers.shunt]
name = "shunt"
base_url = "http://127.0.0.1:8787/v1"
env_key = "SHUNT_API_KEY"
wire_api = "responses"
```

Environment (PowerShell):

```powershell
# Reuse the token that already works against the proxies.
$env:SHUNT_API_KEY = $env:ANTHROPIC_API_KEY
```

POSIX shell:

```sh
export SHUNT_API_KEY="${SHUNT_API_KEY:-$ANTHROPIC_API_KEY}"
```

Notes:

- `wire_api = "responses"` is the only supported value in current Codex. Chat Completions is not
  an option for a new provider. The relay passes `/responses` through unchanged.
- `wire_api = "chat"` MUST NOT be emitted, even though older examples show it.
- The provider key `shunt` is arbitrary but stable. `model_provider` MUST match it exactly.
- `env_key` names an environment variable, not a file. Codex fails at startup if the variable is
  missing, which is preferable to an unauthenticated request.
- `model` MUST be a model ID returned by `shunt models --for codex` or by
  `GET http://127.0.0.1:8787/v1/models`.
- The file may contain other providers and profiles. `--write` edits only `model`,
  `model_provider`, and `[model_providers.shunt]` with a comment-preserving line editor; comments
  and unrelated tables are preserved.
- If the user already has a `[profiles.*]` block, it may override `model`/`model_provider` when
  selected. The generated notice names the profile so the operator can align it.

### Verification

```powershell
codex --version
codex exec "reply with ok"
```

A 404 usually means `base_url` lacks `/v1` or includes `/responses` twice. A 401 means
`SHUNT_API_KEY` is unset or does not match the upstream credential.

## DeepSeek Harness (DSH)

DSH stores per-client settings in `$DSH_HOME/profiles/<profile>/cordis.patch.yml`. The default
profile is `web`; the default home is `~/.dsh`. The file is a list of patch rows applied over the
installed bundle. Provider profiles live under the existing `llm-pi-ai` row's `config.providers`.

The base composition already contains:

```yaml
- id: llm-pi-ai
  name: '@deepseek-ai/dsh-llm-pi-ai'
```

A profile patch overrides that row by `id`. It MUST NOT add a second row with the same plugin
name: registering a route another adapter already owns fails plugin loading.

```yaml
- id: llm-pi-ai
  config:
    providers:
      shunt-anthropic:
        displayName: Shunt (Anthropic)
        apiKeyEnv: SHUNT_TOKEN
        api: anthropic-messages
        baseURL: http://127.0.0.1:8787
        models:
          - id: <free-model-id>
            name: <free-model-id>
            contextWindow: 200000
      shunt-openai:
        displayName: Shunt (OpenAI)
        apiKeyEnv: SHUNT_TOKEN
        api: openai-completions
        baseURL: http://127.0.0.1:8787/v1
        models:
          - id: <free-model-id>
            name: <free-model-id>
            contextWindow: 200000
```

For the Responses dialect, set `api: openai-responses` on a third provider; the base URL rule is
the same `/v1`. A provider speaks exactly one protocol, so a gateway serving both Anthropic and
OpenAI needs two provider entries, as above.

Notes:

- Provider IDs are permanent, lowercase, and hyphenated. Requests, saved sessions, and credentials
  reference them. Renaming requires adding a new provider and deleting the old one. Generated IDs
  are `shunt-anthropic` and `shunt-openai`, optionally suffixed by the profile.
- `apiKeyEnv` is a reference resolved per request. The token stays in the environment; it is not
  written to the YAML or to `$DSH_HOME/.credentials.yaml` by this tool.
- `baseURL` for `anthropic-messages` is the origin; pi-ai appends `/v1/messages`. For
  `openai-completions` and `openai-responses` it includes `/v1`; pi-ai appends
  `/chat/completions` or `/responses`.
- `models` is a list of objects, not bare strings. `id` is required; `name`, `contextWindow`,
  `maxTokens`, and `reasoningEfforts` are optional. Omitting `models` would serve the installed
  catalog for that provider ID, which does not exist for a custom route, so at least one model is
  required.
- DSH resolves profiles and credentials on the next request, so no DSH restart is needed after a
  provider edit. Whether the UI picks up the patch file immediately is version-dependent; the
  generated instructions say to reopen Settings if the provider list does not refresh.
- The Models page's own discovery only interrogates custom providers and reads listing formats it
  recognizes. `shunt models --for dsh` is authoritative here because it queries the relay directly.

### Applying the DSH fragment

v1 prints the fragment; it does not write DSH files. The operator merges it into the existing
`llm-pi-ai` row. If the row already has a `config` block, its other keys (`retryPolicy`, image
budgets, `defaultContextWindow`, and so on) MUST be preserved: a patch replaces the targeted row's
whole `config`, so replacing the block with only `providers` silently resets those values.

### Verification

```powershell
dsh --version
```

Open Settings → Models, confirm both Shunt providers and their models appear, select one, send a
message, and confirm the request appears in `shunt status`. A provider that fails to load is
usually a duplicate row, a missing `api`, or a base URL with the wrong path depth.

## Write, Backup, and Rollback

`shunt models --for <client> --write` follows this sequence:

1. Resolve the target path: `--path`, then the configuration's `clients.<id>` path, then the
   client's documented default. Refuse to write outside the user's home directory unless
   `--path` was given explicitly.
2. Read the existing file. If it exists and does not parse, stop with exit 6 and write nothing.
3. Create `backup_dir/<client>/<filename>.<UTC timestamp>.bak` by copying the exact bytes.
4. Serialize the merged document to a temporary file in the target directory.
5. Replace the target atomically.
6. Print the target path, the backup path, and the changed keys.

Rules:

- Unrelated keys, comments (for TOML), formatting where the format allows, and file permissions
  are preserved.
- Claude Code `settings.json` is merged key-by-key under `env`; existing keys not generated by
  `shunt` are untouched. The token is never merged.
- Codex `config.toml` is edited with a comment-preserving line editor, so comments survive. The
  `model` and `model_provider` top-level keys are set only if absent or if `--force` is given.
- DSH is print-only in v1 (`write_supported = false`). `--write` exits 6 with an explanation and
  the fragment is still printed.
- `shunt config restore <backup>` creates a safety backup of the current file, then restores the
  named backup byte-for-byte. The backup name is validated against the backup directory; path
  traversal is rejected.
- Backups are never deleted automatically. The CLI lists them with their timestamps.

## Generation Algorithm

For `--for <client>`:

1. Fetch the catalog for the effective profile and the dialect the client needs
   (`anthropic` for Claude Code; `openai` for Codex; both for DSH unless `--api` narrows it).
2. If `--model` is omitted, choose the first model whose `free = true`. If none is free, exit 3
   and list the candidate IDs. Unknown and paid models are never chosen automatically.
3. If `--model` is given, require it to exist in the catalog. Warn when it is paid or unknown;
   refuse only when the catalog cannot be fetched at all.
4. Fill the client template with the base URL rule, the model, and any alias overrides.
5. Print the snippet. Write only with `--write`.

A catalog fetch failure is not a reason to emit guessed model IDs. The CLI prints the error, the
profile, and the URL it tried, and exits 4. Cached, stale data may be used with a visible
`stale: true` notice.
