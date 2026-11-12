# Contract: Model Discovery and Normalization

**Feature**: `001-hot-upstream-switch`
**Date**: 2026-11-11

## Endpoint

Both dialects use `GET /v1/models` on the profile origin. The difference is the request headers
and the response shape, not the path.

| Dialect | URL | Headers |
|---------|-----|---------|
| `anthropic` | `{base_url}/v1/models?limit=1000` | `x-api-key: <token>`, `anthropic-version: 2023-06-01` |
| `openai` | `{base_url}/v1/models` | `Authorization: Bearer <token>` |

`shunt models` sends the request through the relay by default (so the active profile is used), or
directly to `--via <profile>`. It never sends the request to both profiles at once.

If no token is available, the request is sent without an auth header. A 401/403 is reported as
`authentication failed for profile <id>; set SHUNT_TOKEN or the client's key variable`, with the
HTTP status and without echoing headers.

## Anthropic Response

```json
{
  "data": [
    {
      "type": "model",
      "id": "claude-sonnet-4-5",
      "display_name": "Claude Sonnet 4.5",
      "created_at": "2025-09-29T00:00:00Z",
      "max_input_tokens": 200000,
      "max_tokens": 64000,
      "capabilities": {
        "image_input": { "supported": true },
        "pdf_input": { "supported": true },
        "thinking": { "supported": true }
      }
    }
  ],
  "has_more": false,
  "first_id": "claude-sonnet-4-5",
  "last_id": "claude-sonnet-4-5"
}
```

Pagination: while `has_more` is true, request
`{base_url}/v1/models?limit=1000&after_id={last_id}`. Stop when `has_more` is false, when no
`last_id` is returned, after 20 pages, or after 5000 models. A repeated `last_id` is treated as the
end to avoid an infinite loop.

## OpenAI Response

Canonical:

```json
{
  "object": "list",
  "data": [
    {
      "id": "deepseek-v4-flash-free",
      "object": "model",
      "created": 1762300000,
      "owned_by": "deepseek"
    }
  ]
}
```

Accepted variants, in order:

- `data` is an array (canonical OpenAI, OpenRouter, most gateways).
- `models` is an array (Ollama-style).
- `models` is an object keyed by model ID; each value may carry `name`, `context_length`,
  `pricing`, and capability fields.

Enriched fields, when present, are read but not required:

- `name` / `display_name`
- `context_length` / `context_window` / `max_context_length`
- `max_completion_tokens` / `max_output_tokens` / `top_provider.max_completion_tokens`
- `pricing.prompt`, `pricing.completion`, `pricing.request`, `pricing.image`
- `architecture.input_modalities`
- `supported_parameters` (used to infer reasoning support)
- `created` (Unix seconds)

OpenAI-compatible listings have no standard pagination. If a response contains `has_more: true`
and a `last_id`, follow the same `after_id` rule as Anthropic; otherwise stop after one page.

## Normalized Record

```json
{
  "id": "deepseek-v4-flash-free",
  "display_name": "DeepSeek V4 Flash (free)",
  "provider": "deepseek",
  "created_at": "2026-11-04T00:00:00Z",
  "context_window": 128000,
  "max_output_tokens": 8192,
  "capabilities": ["text", "tools", "reasoning"],
  "input_modalities": ["text"],
  "reasoning_efforts": ["off", "low", "medium", "high"],
  "lifecycle": "active",
  "cost": {
    "input_per_million": 0,
    "output_per_million": 0,
    "currency": "USD"
  },
  "free": "free",
  "raw": {}
}
```

Field rules:

- `id` is required. A record without a non-empty string ID is dropped and counted as a warning.
- Duplicate IDs keep the first occurrence and increment a duplicate counter.
- `provider` comes from `owned_by`, the listing's provider field, or the profile ID, in that order.
- `created_at` is RFC 3339 UTC; a missing or invalid `created` becomes `null`.
- `context_window` and `max_output_tokens` are integers or `null`. Values are not guessed.
- `capabilities` is derived from explicit capability flags and known fields: `text` always,
  `image` when image input is supported, `pdf` when PDF input is supported, `tools` when tool use
  is advertised, `reasoning` when thinking/reasoning is advertised.
- `input_modalities` defaults to `["text"]` only when the upstream says nothing.
- `lifecycle` is `active`, `deprecated`, or `preview` when advertised, else `active`.
- `raw` retains the upstream object for diagnostics. It is omitted from default table output and
  included in `--json`.

## Free / Paid / Unknown Classification

Evaluated in order; the first rule that applies wins.

1. An explicit boolean `free` field: `true` -> `free`, `false` -> `paid`.
2. An explicit `tier` of `free`/`trial` -> `free`; `paid`/`premium` -> `paid`.
3. Cost data with every component equal to zero -> `free`.
4. Cost data with any component greater than zero -> `paid`.
5. No cost data and no explicit marker -> `unknown`.

Details:

- Cost strings are parsed as decimal USD per token. `"0"`, `"0.0"`, and `"0.000000"` are free.
- A model whose ID ends in `:free` is `free` only when no cost data contradicts it.
- A model with zero prompt cost but a positive completion cost is `paid`.
- A model with an explicit `free: true` and positive pricing is `free`; the explicit marker wins,
  and the conflict is logged as a warning.
- Unknown is not free. It is never auto-selected.

`shunt models` displays a `FREE`, `PAID`, or `?` column. `--json` includes `free` as one of
`"free"`, `"paid"`, `"unknown"`.

## Cache

- Key: `(profile_id, dialect)`. File: `cache/models.<profile>.<dialect>.json`.
- Fresh for `models.cache_ttl_hours` (default 12 h).
- Stale but usable until `models.stale_ttl_hours` (default 168 h).
- A cache entry is invalid if its `source_url` no longer matches the profile's current base URL.
  This prevents showing models from an old endpoint after a configuration change.
- The cache stores the normalized records plus the raw upstream objects. It stores no headers,
  tokens, or request bodies.
- `--refresh` bypasses a fresh entry and fetches again. If that fetch fails and a usable stale
  entry exists, the stale entry is used with `stale: true`, a warning is printed, and the exit
  code is 0. If no usable entry exists, exit 4.
- A failed fetch never overwrites a good cache.
- A corrupt cache file is deleted and treated as a miss. Deletion failure is a warning, not a
  fatal error.
- Concurrent CLI invocations serialize cache writes with a lock file; a lock older than 30
  seconds is considered abandoned.

## Default Model Selection

| Target | Rule |
|--------|------|
| Claude Code | First `free` model in upstream order. All aliases use it unless overridden. |
| Codex | First `free` model in upstream order. |
| DSH, one API | First `free` model in that dialect. |
| DSH, both APIs | First `free` model present in both dialects. If none is common, the first free per dialect is used and the difference is called out. |

- If no free model exists in the required dialect, exit 3, print the profile, the dialect, and the
  available model IDs, and do not emit a snippet.
- `--model <id>` overrides selection. The ID must exist in the catalog for the required dialect.
  A paid or unknown model is allowed only with an explicit `--model`; a warning is printed.
- The upstream order is preserved. No sorting by name, context size, or price.
- Aliases are not guessed from model names. `--opus`, `--sonnet`, `--haiku`, and `--fable` are
  explicit overrides; without them, every alias points at the selected model.

## Output

### Table (default)

```text
PROFILE  remote        DIALECT  anthropic        FETCHED  2026-11-11T12:00:00Z
MODEL                          FREE  CONTEXT   MAX OUT  CAPS
deepseek-v4-flash-free         FREE  128000    8192     text,tools,reasoning
deepseek-v4-pro                PAID  128000    16384    text,tools,reasoning
some-gateway-model             ?     -         -        text
98 models: 12 free, 3 paid, 83 unknown
```

Unknown values print as `-`, never as `0`.

### JSON

```json
{
  "schema": 1,
  "profile": "remote",
  "dialect": "anthropic",
  "source_url": "https://msi.tailc56568.ts.net/v1/models",
  "fetched_at": "2026-11-11T12:00:00Z",
  "stale": false,
  "counts": { "total": 98, "free": 12, "paid": 3, "unknown": 83 },
  "models": []
}
```

`--json` writes one document and nothing else to stdout.

## Failure Reporting

| Condition | Message | Exit |
|-----------|---------|------|
| Relay not running on the default path | `relay not running at 127.0.0.1:8787; start shunt serve or use --via <profile>` | 5 |
| DNS/TLS/connect failure | `cannot reach <profile> at <url>: <cause>` | 4 |
| HTTP 401/403 | `authentication failed for <profile> (HTTP <status>); set the token variable` | 4 |
| HTTP 404 | `model listing not found at <url>; the upstream may not implement /v1/models` | 4 |
| HTTP 5xx | `upstream returned HTTP <status> for <url>` | 4 |
| Invalid JSON or unrecognized shape | `unrecognized model listing from <profile>; expected data[] or models[]` | 4 |
| Zero parseable models | `no models returned by <profile>` | 4 |
| No free model | `no free model in <dialect> for <profile>; choose one with --model` | 3 |

Errors name the profile and URL. They never include headers, tokens, or response bodies beyond a
short parse error.

## Examples

```text
shunt models --json
shunt models --refresh
shunt models --via local --dialect openai
shunt models --for claude
shunt models --for codex --model deepseek-v4-flash-free
shunt models --for dsh --api both
shunt models --for claude --opus deepseek-v4-pro --model deepseek-v4-flash-free
```
