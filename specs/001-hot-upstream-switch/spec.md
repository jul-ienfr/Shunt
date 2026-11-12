# Feature Specification: Hot-Switching Local/Tailnet Upstream Relay

**Feature Branch**: `001-hot-upstream-switch`

**Created**: 2026-11-11

**Status**: Draft

**Input**: User description: "Concevoir un petit logiciel avec SpecKit pour que Claude Code passe par un proxy paramétrable, local par défaut, avec reroutage à la demande vers une IP Tailscale, activable et désactivable à chaud, et récupération de la liste des modèles pour configurer Claude Code, Codex et DeepSeek Harness, et une interface graphique locale pour modifier l'IP et le port d'entrée ainsi que le port de sortie de chaque profil."

## User Scenarios & Testing *(mandatory)*

### User Story 1 - Switch upstream without restarting the client (Priority: P1)

The operator configures Claude Code once to use a stable local address. During a working
session, the operator switches the upstream from the local proxy to the tailnet host, or back,
with a single command. The next request uses the newly selected upstream. The client keeps
running, the session keeps its context, and any response already streaming continues to
completion.

**Why this priority**: This is the reason the product exists. Claude Code captures its endpoint
at launch, so a switch that requires a restart is not a hot switch.

**Independent Test**: Configure a client against the stable address, start a long streamed
response, switch upstream while the stream is active, and verify both that the stream completes
and that the next request reaches the newly selected upstream.

**Acceptance Scenarios**:

1. **Given** the relay is running with the local upstream active, **When** the operator selects
   the tailnet upstream, **Then** the next client request is sent to the tailnet host without
   restarting or reconfiguring the client.
2. **Given** the tailnet upstream is active, **When** the operator selects the local upstream,
   **Then** the next client request is sent to the local proxy without restarting the client.
3. **Given** a streamed response is in progress, **When** the operator switches upstream,
   **Then** the in-progress response finishes on its original upstream and only later requests
   use the new upstream.
4. **Given** the selected upstream is unreachable, **When** the operator attempts a switch,
   **Then** the switch is refused with an actionable message unless the operator explicitly
   forces it.
5. **Given** the relay process restarts, **When** it comes back, **Then** it restores the last
   operator-selected mode and upstream, or a safe default if the stored state is unusable.

---

### User Story 2 - Discover models and generate client configuration (Priority: P2)

The operator asks the relay which models the active upstream currently offers. The result shows
the model identifiers and useful metadata, clearly marking free, paid, and unknown entries. The
operator then generates ready-to-use configuration for Claude Code, Codex, or DeepSeek Harness,
all pointing at the stable local address rather than at a particular upstream.

**Why this priority**: A working relay is not usable until each client is configured with a real
model identifier. Manual copying across three different configuration formats is slow and
error-prone.

**Independent Test**: With a reachable upstream, request the model list, generate configuration
for each supported client, and validate that each generated snippet is accepted by that client
and that a test request reaches the active upstream.

**Acceptance Scenarios**:

1. **Given** the relay is running and the active upstream answers model-listing requests,
   **When** the operator requests the model list, **Then** the operator sees identifiers,
   display names, context limits, capabilities, and free/paid/unknown status where available.
2. **Given** a model list is available, **When** the operator requests Claude Code
   configuration, **Then** the output contains a stable local base address, a credential
   reference, and model identifiers, with no paid model selected by default.
3. **Given** a model list is available, **When** the operator requests Codex configuration,
   **Then** the output is a valid provider block that points at the stable local address and uses
   the protocol that client expects.
4. **Given** a model list is available, **When** the operator requests DeepSeek Harness
   configuration, **Then** the output is a merge-ready provider fragment that preserves existing
   providers and explains that one provider speaks one protocol.
5. **Given** the upstream cannot be reached, **When** the operator requests the model list,
   **Then** the tool reports the failure, uses a cached list only if one exists and is clearly
   marked stale, and never invents model identifiers.
6. **Given** the operator asks to write a configuration file, **When** the target file already
   exists, **Then** a timestamped backup is created and the previous content can be restored.

---

### User Story 3 - Configure endpoints from the local GUI (Priority: P2)

The operator opens a web page served by the relay on its own listener. The page shows the
current entry address and port, the upstream host and port of every profile, the active profile,
and the health of each upstream. The operator changes the entry IP or port, or an upstream port,
applies the change, and the relay reconfigures itself without a process restart. A confirmation
step guards any non-loopback entry address.

**Why this priority**: Editing TOML by hand for an address or port is the most common operational
change. Doing it in the GUI removes a relay restart and a class of typos. Changing the entry
address is a deliberate client-visible reconfiguration; unlike an upstream switch, it may require
updating the client base URL, and the GUI says so.

**Independent Test**: Open the GUI, change the entry port, confirm the listener moves and the
page reconnects; change a profile's upstream port, confirm the next request uses it while an
in-flight stream completes; attempt a non-loopback bind and confirm the explicit confirmation is
required.

**Acceptance Scenarios**:

1. **Given** the relay is running, **When** the operator opens the local GUI, **Then** the page
   shows the entry IP and port, each profile's upstream host and port, the effective profile,
   and per-profile health.
2. **Given** the relay is listening on its current entry port, **When** the operator changes it
   to another free port and applies, **Then** the relay binds the new port, the old listener
   closes, the page reconnects, a request to the new port succeeds without restarting the process,
   and the GUI warns that clients still configured for the old address must be updated.
3. **Given** the local profile points at one upstream port, **When** the operator changes it to
   another port and the probe succeeds, **Then** new requests use the new port and an in-flight
   stream on the old port finishes.
4. **Given** the operator chooses a LAN interface address, **When** the operator applies it,
   **Then** the GUI requires an explicit confirmation, records a durable LAN opt-in, and keeps a
   visible warning that LAN hosts can reach the relay, use its GUI to change configuration, and
   send requests through it.
5. **Given** the operator enters a wildcard address, **When** the operator applies, **Then** the
   change is rejected with an explanation; wildcard binds are never allowed.
6. **Given** a new upstream port is unreachable, **When** the operator applies it, **Then** the
   previous endpoint remains active and the page shows the probe failure.
7. **Given** the operator uses the GUI, **When** a request is made from another web origin,
   **Then** the mutation is rejected unless it carries the per-run session token.

---

### User Story 4 - Automatic health-based fallback (Priority: P3)

The operator enables automatic mode. The relay watches the health of the preferred upstream and
moves traffic to the other configured upstream when the preferred one stops answering. When the
preferred upstream recovers, the relay moves traffic back after a stable recovery period. The
operator can always override the choice manually.

**Why this priority**: Manual switching covers planned work. Automatic fallback covers the
common case where the local proxy stops or the tailnet path degrades while the operator is in
the middle of a session.

**Independent Test**: Run the relay in automatic mode with the local upstream preferred, stop
the local upstream, observe traffic move to the tailnet upstream, restart the local upstream,
and observe traffic return after the configured recovery period.

**Acceptance Scenarios**:

1. **Given** automatic mode with the local upstream preferred and healthy, **When** the local
   upstream fails a configured number of consecutive health checks, **Then** new requests use
   the tailnet upstream and status shows the reason.
2. **Given** automatic mode has failed over to the tailnet upstream, **When** the local upstream
   passes the configured recovery checks, **Then** new requests return to the local upstream.
3. **Given** both upstreams are unhealthy, **When** a request arrives, **Then** the relay returns
   a structured unavailable error and continues probing; it does not crash or loop.
4. **Given** the operator selects a specific upstream manually, **When** automatic mode is
   active, **Then** the manual selection takes precedence and automatic movement stops until the
   operator returns to automatic mode.

---

### User Story 5 - Diagnose and operate safely (Priority: P4)

The operator can inspect the relay's current state, the health of each upstream, the number of
requests in flight, and the last switch reason. A diagnostic command checks configuration,
address binding, local reachability, tailnet reachability, certificate validity, authentication
behavior, and model-listing availability, and it explains failures without suggesting unsafe
workarounds.

**Why this priority**: When a switch does not behave as expected, the operator needs evidence
rather than guesswork. Good diagnostics also prevent unsafe fixes such as disabling the firewall
or exposing the relay publicly.

**Independent Test**: Introduce one fault at a time (wrong port, stopped upstream, invalid
certificate, missing credential) and verify that the diagnostic command identifies the fault and
proposes a safe next step.

**Acceptance Scenarios**:

1. **Given** the relay is running, **When** the operator requests status, **Then** the output
   shows the operator's selected mode, the effective upstream, per-upstream health, in-flight
   requests, and the last switch reason.
2. **Given** the relay is not running, **When** the operator requests status, **Then** the output
   says so plainly and reports the last persisted state instead of failing opaquely.
3. **Given** a configuration or network fault, **When** the operator runs diagnostics, **Then**
   the failing check is identified with a safe corrective action.
4. **Given** the operator inspects logs, **When** requests have been served, **Then** no
   authorization header, API key, or request body appears in the logs.

---

### Edge Cases

- A switch is requested while multiple streamed responses are in flight.
- The client disconnects in the middle of a response.
- The upstream closes the connection after sending response headers but before the stream ends.
- The upstream returns an authentication error, a rate-limit error, or a server error.
- The upstream is reachable but its model-listing endpoint is not.
- The model list is empty, malformed, paginated, or uses the other API dialect.
- The model list contains duplicate identifiers or identifiers with provider prefixes.
- The model list contains no free model, or does not say whether a model is free.
- The state file is missing, truncated, corrupt, or names an upstream that no longer exists.
- The configuration file is edited while the relay is running.
- A profile's upstream port is changed while a stream from the old endpoint is in flight.
- The entry address changes while multiple clients are connected.
- The GUI is open in a browser when the listen address changes.
- A LAN interface used by the listener disappears or changes address.
- A web page from another origin attempts a GUI mutation.
- The listen port is already in use by another process.
- The tailnet name resolves but the tailnet access rules block the port.
- The tailnet certificate is expired, self-signed, or issued for a different name.
- The local proxy is stopped and the operator has selected it manually.
- A generated client configuration targets a client version that does not support one of the
  requested variables.
- The relay is started by a user who cannot write to the configuration or cache directory.
- The system wakes from sleep and the tailnet path has changed.

## Requirements *(mandatory)*

### Functional Requirements

- **FR-001**: The system MUST expose one stable local address that clients use for every request.
  The address MUST NOT change when the active upstream changes.
- **FR-002**: The system MUST support named upstream profiles, including a local profile and a
  tailnet profile, and MUST allow the operator to add or edit profiles without changing client
  configuration.
- **FR-003**: The system MUST apply a manual upstream change to the next request without
  restarting or reconfiguring any client.
- **FR-004**: The system MUST keep every in-progress request on the upstream where it started and
  MUST allow it to finish during and after a switch.
- **FR-005**: The system MUST support a manual mode and an automatic mode.
- **FR-006**: In manual mode, the system MUST NOT switch upstreams without an explicit operator
  command. If the selected upstream is unhealthy, the system MUST report the failure rather than
  silently using another upstream.
- **FR-007**: In automatic mode, the system MUST probe the preferred upstream, move new requests
  to a fallback after a configurable number of consecutive failures, and return to the preferred
  upstream after a configurable number of consecutive successes.
- **FR-008**: A manual selection MUST take precedence over automatic movement until the operator
  returns to automatic mode.
- **FR-009**: The system MUST return a structured, client-readable error when no upstream can
  serve a request, including the selected upstream, the reason, and a request identifier.
- **FR-010**: The system MUST stream request and response bodies incrementally. It MUST NOT
  collect, re-encode, decompress, or delay streamed bodies.
- **FR-011**: The system MUST preserve end-to-end headers that carry authentication, protocol
  version, content type, or model behavior, and MUST remove only transport-specific headers.
- **FR-012**: The system MUST pass through upstream status codes and error bodies unchanged for
  authentication, authorization, rate-limit, validation, and server errors.
- **FR-013**: The system MUST NOT retry a request after any part of its body has been forwarded.
- **FR-014**: The system MUST bind its client-facing address to loopback by default. It MAY bind
  to a specific private address assigned to a local interface only after an explicit operator
  confirmation and a durable LAN opt-in. It MUST reject wildcard addresses, public addresses, and
  addresses not assigned to a local interface.
- **FR-015**: The system MUST verify upstream TLS certificates and MUST NOT offer a mode that
  disables verification.
- **FR-016**: The system MUST pass client credentials to the selected upstream per request and
  MUST NOT persist credentials in configuration, state, caches, or logs.
- **FR-017**: The system MUST retrieve the model list from the active upstream on demand and
  MUST support both the Anthropic and OpenAI listing dialects.
- **FR-018**: The system MUST normalize model identifiers, display names, provider, context
  limits, output limits, capabilities, lifecycle, and free/paid/unknown status when the upstream
  provides them.
- **FR-019**: The system MUST cache model lists per upstream and dialect, MUST show when a cached
  list is stale, and MUST allow an explicit refresh.
- **FR-020**: The system MUST label models as free, paid, or unknown and MUST NOT default-select
  a paid model. If no free model is available, it MUST say so and require an explicit choice.
- **FR-021**: The system MUST generate Claude Code configuration containing a stable local base
  address, a credential reference, and model identifiers for the main model and the client's
  model aliases.
- **FR-022**: The system MUST generate Codex configuration containing a custom provider block,
  a stable local base address, a credential environment reference, and the protocol that client
  supports.
- **FR-023**: The system MUST generate DeepSeek Harness provider configuration as a merge-ready
  fragment, using one provider per protocol and preserving existing providers.
- **FR-024**: The system MUST print generated configuration by default and MUST write to a
  client configuration file only when explicitly asked.
- **FR-025**: When writing a client configuration file, the system MUST create a timestamped
  backup, preserve unrelated settings, and provide a rollback path.
- **FR-026**: The system MUST provide a status view showing the selected mode, effective
  upstream, per-upstream health, in-flight request count, and last switch reason.
- **FR-027**: The system MUST provide diagnostics for configuration validity, bind address and
  exposure, local upstream reachability, tailnet reachability, certificate validity,
  authentication behavior, and model-listing availability.
- **FR-028**: The system MUST persist operator intent and runtime observations separately, MUST
  tolerate a missing or corrupt state file, and MUST never fail startup because of it.
- **FR-029**: The system MUST run as a single self-contained executable with no runtime,
  database, or external service dependency.
- **FR-030**: The system MUST provide explicit enable and disable aliases for the switch, mapping
  enable to the tailnet profile and disable to the local profile by default.
- **FR-031**: The system MUST serve an embedded operator GUI from the same executable and
  listener, with no external CDN, framework runtime, or separate service.
- **FR-032**: The GUI MUST display and allow editing of the entry IP address and port, and of the
  upstream host and port of each profile. It MUST list the addresses actually assigned to local
  interfaces and MUST reject wildcard addresses.
- **FR-033**: Applying a non-loopback entry address MUST require an explicit confirmation in the
  GUI and MUST record a durable opt-in. Loopback remains the default.
- **FR-034**: Applying an upstream host or port change MUST validate the URL, probe the new
  endpoint, and keep the previous endpoint active if the probe fails.
- **FR-035**: Applying an entry IP or port change MUST rebind the listener without restarting the
  process. If the new bind fails, the previous listener MUST remain active and the error MUST be
  shown.
- **FR-036**: An applied profile endpoint change MUST affect new requests only; in-flight requests
  MUST remain pinned to the endpoint where they started.
- **FR-037**: All GUI mutations MUST require a per-run session token and an origin check.
- **FR-038**: The GUI MUST show the effective profile, per-profile health, in-flight count, and
  last switch reason, and MUST allow manual switch and mode changes.
- **FR-039**: The GUI MUST NOT display, request, or store credentials. Configuration writes from
  the GUI MUST be atomic and MUST preserve comments and unrelated keys.

### Key Entities

- **Upstream Profile**: A named destination with a base address, protocol expectations, probe
  target, timeouts, priority, and enabled flag. The local proxy and the tailnet host are the two
  initial profiles.
- **Operator Intent**: The persisted choice of mode (manual or automatic) and the selected or
  preferred profile. It is the authoritative input to the relay.
- **Runtime Observation**: The effective upstream, per-profile health, in-flight count, last
  switch reason, and counters. It is produced by the relay and is safe to discard on restart.
- **Model Record**: A normalized model entry with identifier, display name, provider, context and
  output limits, capabilities, lifecycle, and free/paid/unknown status, plus the raw upstream
  record for traceability.
- **Model Catalog**: A collection of model records for one profile and dialect, with fetch time,
  source, and staleness information.
- **Client Target**: A supported client (Claude Code, Codex, or DeepSeek Harness) with its
  configuration format, destination path, required protocol, and model-alias rules.
- **Entry Endpoint**: The live listener address and port, whether it is loopback, the local
  interface it belongs to, and the generation of the current bind. It is derived from
  configuration and the bind supervisor, not stored separately.
- **GUI Session**: The per-process random token and same-origin checks that authorize GUI
  mutations. It is transient and never persisted.
- **Request Context**: A transient record for one proxied request with identifier, chosen
  profile, start time, streaming flag, and outcome. It is used for logs and status, not for
  durable storage.

## Success Criteria *(mandatory)*

### Measurable Outcomes

- **SC-001**: In 20 out of 20 trials, a manual switch changes the upstream used by the next
  request within 1 second, with no client restart.
- **SC-002**: Across 50 switches performed while 10 streamed responses are active, zero responses
  are truncated or restarted because of a switch.
- **SC-003**: The relay adds at most 10 ms median and 25 ms p95 latency to a non-streamed
  request, and at most 25 ms to the first streamed byte, measured on the target workstation.
- **SC-004**: The relay uses at most 25 MB of idle resident memory, starts in at most 200 ms, and
  ships as a single executable no larger than 10 MB.
- **SC-005**: In 100 out of 100 failure trials, an unavailable selected upstream produces an
  explicit error; zero requests are silently sent to a different upstream in manual mode.
- **SC-006**: A model list of 500 entries is displayed within 5 seconds on a cold fetch and
  within 100 ms from cache.
- **SC-007**: Generated configuration is accepted by each of the three supported clients on the
  first attempt, using the documented format for that client.
- **SC-008**: After an upstream recovers, automatic mode restores the preferred upstream within
  30 seconds of the configured recovery threshold.
- **SC-009**: Inspection of logs, state, cache, and generated configuration finds zero stored
  credentials or authorization header values.
- **SC-010**: A 24-hour soak with 10,000 requests and 200 switches completes without a crash,
  without a stuck request, and with resident memory growth below 20%.
- **SC-011**: In 20 out of 20 trials, a GUI entry-port change rebinds the listener and serves the
  next request within 1 second without a process restart; a GUI profile-port change applies to
  the next request with zero truncated in-flight streams; wildcard and unconfirmed LAN binds are
  rejected 100% of the time.

## Assumptions

- Claude Code and the other clients capture their API base address at startup and do not re-read
  it during a session; the stable relay address is therefore the only client-side change needed.
- Changing the entry IP or port is a deliberate client-visible change: existing clients keep using
  the old address until their base URL is updated. This is distinct from an upstream switch, which
  never moves the entry address.
- The operator has a browser on the same workstation. The GUI is served on the relay listener,
  so changing the entry address changes the URL the operator uses to reach it.
- A non-loopback bind is a deliberate LAN-only choice. The workstation is not directly exposed to
  the public Internet, the relay never configures a public listener or Funnel, and the operator
  accepts that LAN hosts can use the relay and consume the upstream's quota.
- The local proxy runs on the same workstation at a stable loopback address. The proposed
  default is `http://127.0.0.1:4000`.
- The tailnet upstream is reachable through Tailscale Serve at
  `https://msi.tailc56568.ts.net` with a valid certificate. Raw port 4000 is not assumed to be
  exposed.
- The upstream proxy serves Anthropic Messages, OpenAI Chat Completions, OpenAI Responses, and a
  model-listing endpoint in both dialects, as verified during discovery.
- The same client credential is accepted by the local and tailnet profiles. The relay does not
  issue, refresh, or validate credentials.
- Model-listing responses may include free/paid/tier metadata, but the system does not assume it.
  Missing metadata is reported as unknown.
- A single operator uses the workstation. Multi-user authentication and per-user quotas are out
  of scope.
- Windows 10/11 x64 is the primary platform. Linux and macOS support is secondary and does not
  block the first release.
- Clients use HTTP request/response and streamed responses, not WebSocket transports.
- The relay is a switch and failover mechanism, not a load balancer, an IP pool, or a quota
  manager.

## Out of Scope

- Building or modifying the upstream proxy, its model routing, or its free-IP selection logic.
- Managing free quotas, choosing among hundreds of IPs, or guaranteeing a particular model's
  availability.
- Public exposure through Tailscale Funnel, a public listener, or a firewall change.
- Credential issuance, rotation, or storage.
- Protocol translation between Anthropic and OpenAI formats.
- Public Internet access to the GUI; LAN access is an explicit local opt-in. Multi-user
  accounts and role-based access control are also out of scope.
- Automatic restart or reconfiguration of client applications.
- Multi-user access control or billing.
