# Skills, MCP and Capabilities

> Status: Current reference — capability registration, acquisition, grants, session gates, built-in sources and connections, secrets and transports.

Skills and MCP servers are user-owned registrations. Discovery, acquisition,
launch, access and execution obey the user-selected autonomy policy (Manual, Auto
Review or YOLO), explicit denials, source trust and host/effect ceilings. Routes,
protocol limits and manual workflows are in
[capability workflows](../guides/capability-workflows.md); Settings presentation
is in [settings design](settings-design.md#skills-and-mcp-store).

## Registration and binding

- The `tool` capability kind describes managed project tools, not an MCP
  registration. The native [project setup](settings.md#project-tool-setup) owner
  connects review, human choice, staged evaluation and acquisition. The reserved
  `capabilities.project_tool_decisions` JSON value
  is workspace-only and does not inherit from application settings. Generic
  settings and capability registration are not the authority to grant project
  setup consent; an expiring native-shell preview and consumed one-shot authority
  own that decision. Stored choices are audit evidence, not reusable effect grants.
  Agents can inspect the shared [tool inventory](tool-store.md), but no generic
  capability registration or semantic command grants installation or removal.
- **Skills and MCP are application-bound by default; workspace binding is an
  explicit choice.** `capability_activate` publishes user-owned registrations into
  config.json unless the task or user asks for `scope: "workspace"`, so deleting a
  workspace never destroys an install. Tool settings belong to that same binding. Downloaded artifacts
  and MCP credentials live under the app data directory, outside the workspace
  store; a workspace registration is only an overlay row pointing at that
  app-global path. `types/capability.ApplyRegistration` is the single publication
  owner shared by the workspace overlay store and the app config owner.
- Agent acquisitions and user changes to MCP tool settings publish through the normal
  settings registration maps (config.json for application scope, the sparse
  workspace overlay for workspace scope), never a second shadow install table.
  Preserve neighboring installs/preferences and fence the exact source, policy,
  selected preimage and workspace lifetime. MCP activation lists the tools this agent can use without duplicating their schemas.
  A connected external tool is offered across turns. Its actual call uses the ordinary
  Manual, Auto Review or YOLO decision. A model cannot grant itself a read effect.
  Without a user classification an external operation can have any effect, so a
  restricted role cannot call it. Disabling a tool affects pending calls without
  restarting the server. Changed classifications, schemas or launch targets fence
  earlier requests. Skill activation includes its installed path. Passive restart
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
  `capability_activate` queues one
  `prompt.RuntimeCapabilityShared` notice for every other agent of the session
  (parent, children, siblings), delivered at its next request
  (`agent/session/capability_sharing.go`). Each operation and ID is announced once,
  undelivered notices are bounded, and the activating agent is not told about
  its own change. The notice asks the recipient to activate that exact ID rather
  than search for or install an alternative.

## Acquisition and approval

The old 32 MiB default for skill archives and the 60-second install deadline
came from `530b56a5` (2026-10-03). They were implementation defaults, not a
recorded user choice. **Acquisition → Optional limits** now leaves both off
unless configured. The archive and extracted files are checked against an
explicit size limit if one is set. A package runner's first download is not a
skill archive. A local MCP launch has no implicit startup deadline; the user
can stop the job or set a connection timeout. Separate limits for model input,
protocol responses and bounded evidence still apply.

- Managed install/update and agent acquisition approval carry an immutable
  source/revision/binding preimage. A boolean approval must never widen into
  changed bytes or another source. Dynamic MCP definitions and execution both
  enforce the current tool switch, effect classification and agent role. Call
  permission follows the session mode.
- Application settings contain three disableable built-ins:
  `anthropic-skills`, pinned to the complete commit recorded in
  `types/capability`, `skills-directory` community search, and
  `official-mcp-registry`, including remote HTTP and pinned npm/PyPI packages.
  Skills/MCP use readable store cards with inline Install/Manage, an explicit
  Installed chip, live search over the shared progress rule and Source/Sort
  choice lists, not administration tables.
  All-source search interleaves ranked results; unsupported variants and partial
  source failures stay visible. Built-ins can be edited or disabled but not deleted; source
  trust never bypasses exact acquisition preview or call permissions.

## Built-in web connection

- `web_search`, `web_fetch` and `arxiv_search` are served by Crude's built-in
  MCP connection `web` (`webmcp`, transport `builtin`, in-process). Its tools
  keep their plain names, so role allow/deny lists name them as before; it is
  usable without activation or grants, starts on an agent's first request, can
  be turned off per session (`/mcps`), disabled in Settings, or replaced by any
  other MCP server. Per-tool switches use the connection's grants
  (`Disabled`). Built-ins can be disabled but not removed.
- Every search method starts at the same time under one 24-second operation
  deadline inside the ordinary MCP call: configured provider keys (Exa, Brave
  Search API, Tavily, SearXNG), the user's account search methods and the
  keyless engines (Brave, DuckDuckGo, Startpage, Exa's public MCP). Crude waits
  for each configured method (keys and account methods) until it answers or
  the deadline passes, keeping free-engine results that arrive meanwhile. When
  every configured method has finished, still-running free engines get at most
  150 ms more if a usable result exists. Without configured methods, the
  keyless rule applies: return once enough unique destinations arrive, or
  150 ms after the first partial answer. Every request is cancelled and joined
  before the result returns; a stalled engine cannot hold useful results.
  Exa's public endpoint uses the shared MCP SDK rather than a second JSON/SSE
  parser.
- Results merge by agreement with reciprocal rank fusion (k = 60) over each
  method's deduplicated list (HTTP(S) only, host case and fragment ignored), so
  a page that more methods found ranks above a single top hit. `(Sources: …)`
  names the methods that answered, in completion order; `(Unavailable: …)`
  names failed or late ones with their reasons. Total failure of every method
  is an error with the `web_search_outage` result code; a completed empty
  search is a normal "No results" answer. Generic MCP failure projection
  retains the actual cause for every connection, not a web-only exception.
- **Application Settings → MCP → Web search and fetch → Search methods** lists
  account search methods (`capabilities.web_search_routes`, at most 8). Each
  method stores a generated ID, an account ID, a model ID when the protocol
  needs one, and an Off flag; never a secret. Support is read only from the
  account's protocol declaration (`ProtocolInfo.WebSearch`), never from a host
  or vendor list: Ollama declares an account endpoint (`WebSearchPath`
  `web_search`, resolved against the account base like `DecisionPath`, no
  model); OpenAI Responses, Anthropic Messages and Gemini declare a hosted
  search tool on the chat endpoint and need a model. Codex, Chat Completions
  and OAuth accounts cannot search. A workspace cannot add, change or redirect
  search methods; Workspace Settings shows them read-only.
- An account method sends only the query and result limit in one stateless
  POST (no retry, redirect, replay header, extras, transcript or files;
  `store: false` where the protocol has it). Results come only from typed
  search records (Ollama `results`, Responses `web_search_call.action.sources`,
  Anthropic `web_search_tool_result`, Gemini `groundingMetadata`), never from
  links in generated prose; a reply without search records is a failure, not
  an empty success, because some gateways accept a search tool and do not
  search. Charges can apply. **Test** runs one search with a fixed query and
  saves nothing. The latest outcome of each method (working, sources, latency
  or a short reason) is kept in memory per account and model and shown in the
  list; caller cancellation records nothing. Saves and observation never start
  a search. Built-in and session/tool disable still apply. Only a total outage
  offers a row link to **Search methods**; empty, cancelled, denied and
  disabled calls do not.
- `web_fetch` pages
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

- MCP OAuth uses the existing **Sign in** and **Open browser** controls. In
  **Edit connection**, select OAuth and enter either the public HTTPS **Metadata
  URL** or a pre-registered **OAuth client ID**. A metadata
  document must describe the same public client and allow the native loopback
  return path `/callback` on `127.0.0.1` with an ephemeral port. If the server
  does not advertise metadata support, Crude can use dynamic registration when
  the server supports it.
- For a registered client that needs a secret, set **OAuth issuer URL** and use
  `secret:NAME` in **Secret reference**. Store the value in the
  existing application secret store; do not put it in a resource header.
  Settings only shows that the reference is stored. An unchanged reference
  cannot follow an issuer or client-ID edit in the editor. The SDK checks the
  discovered issuer. Crude sends the secret only to its validated token endpoint
  on that issuer's origin, never to the MCP resource or through a redirect. The
  token file does not contain a copy of the managed secret.
- If a Bearer challenge asks for more access, Crude stops the request and shows
  the scopes. The connection keeps the challenge until the user signs in again,
  disconnects, or changes the connection. Only a new **Sign in** can ask for the
  old and newly requested scopes at the same resource and authorization
  endpoints. Cancellation or refusal does not exchange a token. Sign-in never
  repeats the original tool call or changes the tool's approval.
- Remote servers use Streamable HTTP; a server that answers the initialize
  POST like a 2024-11-05 server (400/404/405/406/415) is reconnected over the
  legacy HTTP+SSE transport automatically, and `sse` can be configured
  explicitly. Protocol versions are negotiated by the SDK.
- An active agent run reconnects, off the request path and with exponential
  backoff, connections that dropped after working and connections with a saved
  user classification (local servers only with approved launch). A user
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

A root Plan mode turn declares the same capability tools as a normal turn. Its
request preparation reads the connected catalog without starting a host,
reconnecting a server or refreshing discovery. Declaration grants no permission;
Plan mode refuses activation and external tool calls before execution.

## Lifecycle

- Capability snapshot contexts cancel before workspace teardown. Workspace
  capability lifetime locking protects persistence/service replacement; a stale
  read cannot inspect a nil/new WorkspaceDB during Close/SwitchProject.
- Local stdio MCP servers inherit the full host environment, like agent shell
  commands; never reintroduce a variable allowlist. Server stderr tails are
  attached to failures; when the server exited, the first line names its own
  reason. Activation that acquires, installs or launches, including first-run
  installation by the server, is one of the owning
  agent's [background jobs](background-commands.md#activation-jobs) from the
  moment it is authorized. A returned handle is not a connected server. See
  [capability workflows](../guides/capability-workflows.md#mcp).
