# Skills, MCP and Capabilities

> Status: Current reference — capability registration, acquisition, grants, session gates, built-in sources and connections, secrets and transports.

Skills and MCP servers are user-owned registrations. Discovery, acquisition,
launch, access and execution obey the user-selected autonomy policy (Manual, Auto
Review or YOLO), explicit denials, source trust and host/effect ceilings. Routes,
protocol limits and manual workflows are in
[capability workflows](../guides/capability-workflows.md); Settings presentation
is in [settings design](settings-design.md#skills-and-mcp-store).

## Registration and binding

- **Skills and MCP are application-bound by default; workspace binding is an
  explicit choice.** `capability_activate` publishes user-owned registrations into
  config.json unless the task or user asks for `scope: "workspace"`, so deleting a
  workspace never destroys an install. `mcp_request_access` grants inherit the
  connection's own binding and must never rewrite it. Downloaded/verified artifacts
  and MCP credentials live under the app data directory, outside the workspace
  store; a workspace registration is only an overlay row pointing at that
  app-global path. `types/capability.ApplyRegistration` is the single publication
  owner shared by the workspace overlay store and the app config owner.
- Agent acquisitions and human-approved durable MCP access changes publish through the normal
  settings registration maps (config.json for application scope, the sparse
  workspace overlay for workspace scope), never a second shadow install table.
  Preserve neighboring installs/preferences and fence the exact source, policy,
  selected preimage and workspace lifetime. MCP activation exposes observed
  fingerprints separately from usable tools; separate `mcp_request_access` grants require
  exact authorization: human approval persists durable grants, while reviewer/YOLO
  grants are run-local, never human authority or source annotations. Skill activation includes its installed path. Passive restart
  reconnects/downloads/replays nothing; explicit activation rechecks tool schemas.
  Skill loading/installation validates content independently of host readiness.
  Unknown client fields and nested authoring metadata are inert (hooks never run);
  tool requests accept spaces, commas or lists and never grant authority.
  Compatibility prose remains guidance, not executable-name inference. Explicit
  requirement metadata is checked at activation; missing dependencies retain an
  installed, readable setup-required entry. Recheck readiness on activation.
  Bounded YAML still rejects duplicate keys and anchors/aliases everywhere.
  MCP registry headers and declared-variable defaults survive acquisition;
  required empty headers use the existing editor/Secret references and block
  connection, not install. Required stdio environment checks include actual host
  inheritance; shell-host checks defer to the shell, never the core's environment.
  Unsupported transports/unresolved command or endpoint variables remain explicit;
  never fabricate credentials, grants or executable behavior to claim success.
  Live capability observation refreshes Settings/sidebar/home without configuring
  transports or inspecting skill files on Update. It must keep running with
  Settings/sidebar visible after an agent removes the launcher screen. Inspected
  MCP details retain their exact tool identity and refresh schema/grant evidence
  without replacing filters or editable drafts; removed tools clear old claims.
  Local/installed/pinned-repository discovery ranks normalized keywords before
  applying limits. Agent guidance proactively uses relevant installed capabilities or acquisitions
  when they improve the task, even when native tools exist, except that a workflow
  named by applicable project guidance takes precedence over discovery, within the user-selected
  Manual/Auto Review/YOLO policy, explicit denials, source trust and host/effect
  ceilings. Authentication and consent remain user-owned. Exact stdio launch is
  review/YOLO eligible, not a sandbox or durable tool grant. Use concise queries and exact returned IDs,
  not catalog paths as a substitute for durable installation. Live acceptance
  submits through the actual composer and keeps chat visible; screenshot-driver
  exit alone is not proof that the model/persistence test completed.

## Sharing within a session

- Sub-agents may search for and acquire capabilities like their parent, but
  the session's agents are told about each other's work: a successful
  `capability_activate` or `mcp_request_access` queues one
  `prompt.RuntimeCapabilityShared` notice for every other agent of the session
  (parent, children, siblings), delivered at its next request
  (`session/capability_sharing.go`). Each operation and ID is announced once,
  undelivered notices are bounded, and the activating agent is not told about
  its own change. The notice asks the recipient to activate that exact ID rather
  than search for or install an alternative.

## Acquisition and approval

- Managed install/update and agent acquisition approval carry an immutable
  source/revision/binding preimage. A boolean approval must never widen into
  changed bytes or another source. Dynamic MCP definitions and execution both
  enforce current effect/scope/fingerprint grants and root/child ceilings.
- Application settings contain three disableable built-ins:
  `anthropic-skills`, pinned to the complete commit recorded in
  `types/capability`, `skills-directory` community search, and
  `official-mcp-registry`, including remote HTTP and pinned npm/PyPI packages.
  Skills/MCP use readable store cards with inline Install/Manage, an explicit
  Installed chip, live search over the shared progress rule and Source/Sort
  choice lists, not administration tables.
  All-source search interleaves ranked results; unsupported variants and partial
  source failures stay visible. Built-ins can be edited or disabled but not deleted; source
  trust never bypasses exact acquisition preview or MCP grants.

## Built-in web connection

- `web_search`, `web_fetch` and `arxiv_search` are served by Crude's built-in
  MCP connection `web` (`webmcp`, transport `builtin`, in-process). Its tools
  keep their plain names, so role allow/deny lists name them as before; it is
  usable without activation or grants, starts on an agent's first request, can
  be turned off per session (`/mcps`), disabled in Settings, or replaced by any
  other MCP server. Per-tool switches use the connection's grants
  (`Disabled`). Built-ins can be disabled but not removed.
- Search tries configured providers one at a time in quality order (Exa,
  Brave Search API, Tavily, SearXNG), then the keyless engines in parallel, so
  a missing, revoked or rate-limited key never stops search. Provider attempts
  share a 24-second operation budget inside the ordinary MCP call, reserving
  fallback time. Keyless search returns once enough unique destinations arrive
  (or 150 ms after its first partial answer), cancelling and joining remaining
  requests; a stalled provider cannot hold useful results hostage. Exa's public
  endpoint uses the shared MCP SDK rather than a second JSON/SSE parser.
  Total provider failure is an error, distinct from a completed empty search.
  Generic MCP failure projection retains the actual transport/error cause for
  every connection, not a web-only exception. `web_fetch` pages
  results (32,768 characters by default, the `read_async_output` page) with an
  offset to continue, serves later pages from a bounded per-connection cache,
  never exceeds the MCP result budget, and falls back to Exa contents or Jina
  Reader only when those keys are configured.
- Fetch accepts text media (including Markdown, source and JSON/XML variants),
  sniffs missing/generic media types, and converts HTML to structured Markdown
  with links and code preserved. Nested content containers are read once.
  Concurrent reads of one normalized URL join one request; cached pagination is
  Unicode-character based and ends explicitly, without a repeating continuation.
  Responses over 8 MiB (25 MiB for convertible documents) fail explicitly rather
  than silently truncating source. Site refusals remain ordinary tool errors and
  do not disconnect the built-in server. There is one built-in tool registration
  and the same MCP result path as external connections, not duplicate native web
  tools or special execution bypasses. External outages, bot protection and
  authenticated/JavaScript-only sites remain external constraints.
- Provider keys (`EXA_API_KEY`, `BRAVE_SEARCH_API_KEY`, `TAVILY_API_KEY`,
  `SEARXNG_URL`, `JINA_API_KEY`) come from Crude's secret store, then the core's
  environment.

## Secrets

- Crude's secret store is one owner-only JSON file in the application
  credential vault (`credentials/secrets.json`). Connections reference values as
  `secret:NAME` (or `env:NAME` for the core's environment); values are written
  through Settings (masked fields; blank keeps, `-` removes) or the
  `capabilities.set_secret` self-control command, and are never returned to a
  shell or an agent. Snapshots list only stored names. Changing a secret
  restarts the connections that read it. Resolved values are redacted from
  errors, logs and tool results.

## Transports and recovery

- Remote servers use Streamable HTTP; a server that answers the initialize
  POST like a 2024-11-05 server (400/404/405/406/415) is reconnected over the
  legacy HTTP+SSE transport automatically, and `sse` can be configured
  explicitly. Protocol versions are negotiated by the SDK.
- An active agent run reconnects, off the request path and with exponential
  backoff, connections that dropped after working and connections the user
  durably granted tools on (local servers only with approved launch). A user
  Disconnect is respected; passive app start still connects nothing. An
  explicit connect while one is in flight joins it. Each outage is reported to
  an agent once.

## Settings and session controls

- Skills, MCP, Sources and Acquisition follow Memory in the existing AI settings
  group. Navigation and painting share the same grouped order; never append a
  second AI heading after Security or IDE.
- Skills/MCP settings deep-links retain the Settings frame and explicitly mark
  its Host layout. Async capability results target the owning Model directly,
  not the focused text control, which can consume unrelated custom events.
- `/skills` and `/mcps` open separate Skills / MCP servers views (no tabs) of
  the session capability modal: quick, temporary per-session control. A padded
  body heading names the view and session (no border title), search, a one-column list of
  self-contained bordered cards (chevron, name, compact facts, square-knob switch,
  three description lines) that expand in place on click/Enter to show every
  fact (no separate details page), a framed where-to-start guide when empty,
  and one **Install & manage** button to Settings. Switches set the session-owned
  capability gate (`Session.SetCapabilityEnabled`, `session_capabilities`,
  `settings.session.toggle_capability`), never the registration. Every agent of
  the session reads it live through `RuntimeActor.SessionDisabled`; the capability
  service drops off MCP tools from the next request and refuses their calls,
  resource/prompt reads, access requests and activation. Focus is an accent
  border, never a fill that swallows text. MCP facts are observed only; switching
  never starts a transport or grants access.

## Lifecycle

- Capability snapshot contexts cancel before workspace teardown. Workspace
  capability lifetime locking protects persistence/service replacement; a stale
  read cannot inspect a nil/new WorkspaceDB during Close/SwitchProject.
- Local stdio MCP servers inherit the full host environment, like agent shell
  commands; never reintroduce a variable allowlist. Server stderr tails are
  attached to failures. Registry packages are prepared through the owning
  agent's background command manager after launch authorization, never inside
  the handshake deadline. See `docs/guides/capability-workflows.md`.
