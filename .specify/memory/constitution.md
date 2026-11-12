<!--
Sync Impact Report
Version change: 1.1.0 -> 1.2.1 (1.2.0 added the Go/LAN constraints; 1.2.1 clarifies Principle I)
Modified principles: I clarified that a deliberate entry-endpoint change is a client-visible
  reconfiguration, not a hot upstream switch; IV renamed and amended (Loopback by Default,
  Explicit LAN Opt-In); implementation constraints add the embedded operator GUI and
  local-interface binding rules, and switch the implementation language from Rust to Go.
Added sections: none
Removed sections: none
Follow-up TODOs: none
-->

# Shunt Constitution

## Core Principles

### I. Stable Client Surface (NON-NEGOTIABLE)

The address a client application is configured to use MUST remain fixed for the lifetime of
that client session. Changing the active upstream MUST happen behind that address, on the next
request, without editing client environment variables, the hosts file, DNS, or restarting the
client. A design that requires a client restart to change upstreams is rejected. Deliberately
changing the listener endpoint (entry IP or port) is a separate configuration operation: it MAY
require updating client configuration, and the GUI MUST say so. It is never presented as a hot
upstream switch.

**Rationale**: Claude Code and comparable clients capture their base address at process start.
Any switch that depends on the client re-reading configuration is not a hot switch.

### II. Stream Fidelity

Request and response bodies MUST be forwarded as streams, byte-for-byte, except for
transport-required header changes. The relay MUST NOT buffer, aggregate, re-encode, decompress,
or delay streamed bodies. A request that has started on an upstream MUST remain pinned to that
upstream and MUST be allowed to finish, even while a switch is in progress.

**Rationale**: Long-lived model responses are the normal case, not an edge case. Buffering
breaks token streaming and turns a cheap relay into a latency and memory problem.

### III. Explicit State, Predictable Failure

At any moment there MUST be one authoritative operator intent and one observable effective
upstream. A manual switch MUST be atomic and MUST NOT silently fall back to another upstream.
When the effective upstream is unavailable, the relay MUST return a structured error, preserve
the last-known-good state, and keep serving other requests. A corrupt or missing state file MUST
never prevent startup.

**Rationale**: Silent fallback hides the very failure the operator is trying to control. Clear
state makes both normal operation and incident diagnosis deterministic.

### IV. Loopback by Default, Explicit LAN Opt-In

The data plane MUST bind to loopback by default. Binding to a non-loopback address MUST require
two explicit acts: an operator confirmation in the GUI, and `relay.allow_non_loopback = true` in
the configuration. The address MUST be an address actually assigned to a local interface; the
wildcard addresses `0.0.0.0` and `::` are rejected. The relay MUST NOT expose itself publicly,
enable Tailscale Funnel, disable upstream authentication, or weaken TLS verification. Credentials
MUST be passed through per request and MUST NOT be stored in configuration, state, caches, or
logs. The control surface MUST be authenticated locally, and status output MUST NOT contain
secrets.

**Rationale**: The relay carries model credentials. Its useful scope is one workstation, or the
operator's own LAN when deliberately chosen, not the public Internet. A confirmation plus a
non-wildcard address keeps a casual click from turning the relay into an open LAN service. A LAN
bind is a trust decision that extends to GUI configuration changes, and the confirmation says so;
the GUI token is CSRF protection, not user authentication against LAN hosts.

### V. Small, Boring, Single Binary

The product MUST ship as one self-contained executable with no runtime, database, or external
service. Idle resource use and startup time MUST remain small enough to be invisible on a
developer workstation. Every dependency MUST justify its size and risk. The request path MUST
NOT contain unsafe code.

**Rationale**: A proxy that is heavier than the workload it forwards is the wrong proxy. A small
binary is easier to audit, deploy, and keep running.

### VI. Verified on the Target

Claims about switching, streaming, latency, and recovery MUST be verified on the actual Windows
workstation and the actual tailnet path, with a live stream in flight. A test on another machine
MUST NOT be generalized to this machine, and a single failed attempt MUST NOT be generalized to
an entire pool of addresses or servers.

**Rationale**: This feature sits on top of machine-specific networking, DNS, TLS, and VPN
behavior. Only a test of the actual path is evidence.

## Implementation Constraints

- **Platform**: Windows 10/11 x64 is the primary target; Linux and macOS are secondary. The
  implementation language is Go (current stable, 1.23 or newer), built with `CGO_ENABLED=0` and
  the standard library. TOML parsing is the only external module. No cgo, no web framework, no
  reflection-heavy runtime.
- **Protocols**: The client-facing leg is HTTP/1.1. The relay passes through Anthropic Messages,
  OpenAI Chat Completions, OpenAI Responses, and model-listing requests without protocol
  translation. HTTPS is used for tailnet upstreams with mandatory certificate verification.
- **Storage**: Human-edited configuration is TOML. State, runtime observations, and model caches
  are JSON. There is no database.
- **Resource budgets**: idle resident memory at or below 25 MB; release binary at or below 10 MB;
  cold start at or below 200 ms; added median request latency at or below 10 ms and p95 at or
  below 25 ms; at least 50 concurrent streamed requests.
- **Client compatibility**: Generated configuration MUST be valid for Claude Code, Codex, and
  DeepSeek Harness using their current documented formats. A snippet that the client rejects is
  a defect, not a documentation issue.
- **Free-first model selection**: Model discovery MUST label each model as free, paid, or unknown
  when the upstream provides enough information. Automated configuration MUST NOT default-select
  a paid model. If no free model is available, the tool MUST say so and require an explicit
  model choice.
- **Operator GUI**: The product includes an embedded web UI served by the same binary on the
  relay listener at `/_shunt/ui`. Assets are embedded in the executable; no external CDN,
  framework runtime, or separate service is allowed. Mutating requests require a per-run session
  token and an origin check. The GUI MUST NOT display or store credentials.
- **Bind addresses**: Loopback is the default. A specific local-interface address is allowed only
  after explicit confirmation and only with `relay.allow_non_loopback = true`. Wildcard binds,
  public addresses not assigned to a local interface, and automatic LAN exposure are rejected.
- **Live reconfiguration**: Changing the listen address or port rebinds the listener without
  restarting the process. Changing a profile's upstream host or port is applied to new requests
  after validation and a successful probe; in-flight requests stay on their original upstream.
- **No public exposure**: Tailscale Funnel, public bind addresses, firewall disablement, and
  authentication bypass are out of scope and MUST NOT be offered as troubleshooting steps.

## Development Workflow

- Work proceeds specification, plan, tasks, implementation. No production code is written before
  the plan is accepted.
- Unit tests cover state transitions, atomic writes, header filtering, and model normalization.
- Contract tests cover the client-facing HTTP behavior, error shapes, and CLI output.
- Integration tests use a fake upstream for deterministic streaming and a real tailnet upstream
  for path validation.
- A hot-switch test with a live stream in flight is mandatory before release.
- Every write to a client configuration file requires a backup and a tested rollback path.
- Complexity that violates a principle MUST be recorded in the plan's Complexity Tracking table
  with the simpler alternative that was rejected.

## Governance

This constitution supersedes conflicting local practices. Amendments require a documented
change, a semantic version bump, and a review of affected plans and tests. Versioning follows
MAJOR for incompatible principle changes, MINOR for new or materially expanded guidance, and
PATCH for clarifications. Every plan and task list MUST include a constitution check. Runtime
operating guidance remains in the project's agent instructions; this document governs the
product.

**Version**: 1.2.1 | **Ratified**: 2026-11-11 | **Last Amended**: 2026-11-11
