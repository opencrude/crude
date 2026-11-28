# Stable v1

> Status: ACTIVE PLAN (2026-11-28). The current release is v0.9.0. v1 needs the
> six gates on this page, each closed by evidence on real hardware. Feature
> plans keep their own scope; this page owns the v1 definition and the release
> decision.

## What v1 means

A version number does not make a release stable. v1 is a promise: the product
below works on a weak laptop, the risky work runs in a throwaway sandbox, a new
person can bring their history and their existing agent subscriptions,
right-to-left languages keep working, and people can extend Crude with plugins
without forking it.

Rules for this plan:

- A gate closes only with recorded evidence in this repository. A passing local
  test on faster hardware is not evidence for a gate.
- No gate is dropped, renamed or postponed silently to make a date. Change this
  page first and say why.
- v0.x keeps shipping while the gates are open. A release is never called v1
  early to look finished.

## What already ships

The v1 work builds on a product that already exists. This list is the baseline,
not a wish:

| Area | Shipped behavior | Reference |
|---|---|---|
| Shell and core | A per-user core keeps sessions, agents and terminals alive after the window closes. A shell attaches and detaches without stopping work. Local, SSH, TLS and QUIC carriers. | [core protocol](../reference/core-client-protocol.md), [core restart](../reference/core-restart.md) |
| Remote work | `--ssh HOST --workspace PATH` keeps the editor local and runs agents, language servers and terminals on the server. Strict host keys, no extra exposed port. | [network](../reference/network.md) |
| Agents | Child agents in isolated Git worktrees, immutable reports with evidence, exact identities, cancellation and recovery. | [agent orchestration](../reference/agent-orchestration.md), [managed executions](../reference/managed-executions.md) |
| Collaboration | Board, directed requests with revisions, claims, unread state and durable delivery. | [workspace collaboration](../reference/workspace-collaboration.md) |
| Context | Native-first compaction with whole-history fallback, continuity that keeps the system prompt and tools, and manual compact that never waits for another loop. | [adaptive context](../reference/adaptive-context.md) |
| Editor | Completion, hover, rename, code actions, inlay hints, semantic highlighting, project-wide problems for files that are not open. | [editor](../reference/editor.md) |
| Terminals | Embedded PTY terminals on Linux, ConPTY on Windows, owned by the core. | [in-app terminals](../reference/in-app-terminals.md) |
| Review and recovery | Hunk-level Git staging, risky-command approval, Local History previews and restore. | [local history](../reference/local-history.md) |
| Memory | Workspace memory with evidence, search over past sessions, user edit and removal. | [documentation index](../README.md) |
| Right-to-left | UAX #9 levels, Arabic joining, editor isolates, logical caret and copy, arranged cells for terminals that cannot arrange text. | [bidi](../reference/bidi.md) |
| Capabilities | Agent Skills and MCP servers with exact install previews, approval of exact bytes and revocable grants. | [capabilities](../reference/capabilities.md) |
| Session control | Plan mode, goals with an independent verifier, forks, slash commands and the palette. | [session commands](../reference/session-commands.md) |
| Errand | A background workspace agent that investigates reported problems and reports back. | [Ask Crude and Errand](ask-crude-and-errand-suggestions.md) |
| Releases | 35 releases from v0.1.1 to v0.9.0, reproducible Linux and Windows installers, checksums and a verification workflow. | [releases](../guides/releases.md) |
| Performance | Allocation ceilings, per-frame budgets and a dashboard, with recorded baselines. | [performance baselines](../reference/performance-baselines.md) |
| Crashes | Fatal capture before cleanup, crash reports and TUI recovery. | [crash reports](../reference/crash-reports.md) |

## Decisions

Each gate below is a release requirement, not a roadmap suggestion.

| Date | Decision | Why |
|---|---|---|
| 2026-11-28 | v1 is gated on Gates 1 to 5. | Each gate names a promise that a version number alone does not make. |
| 2026-11-28 | Gate 6, plugins, joins the gates: a plugin platform, a registry with a browser, and three complete plugins. | People must extend Crude without forking it, and the tools developers leave the terminal for must come to it. Three real plugins prove that the API is enough. |
| 2026-11-28 | The plugin runtime is Lua or WebAssembly. The choice is open. | Both embed in a Go core and limit a plugin to the host API. The spike in Gate 6 decides. |

## Gate 1: smooth on a weak machine

**Requirement.** Crude shows no visible slowdown on a ThinkPad T450-class
machine. This is the machine a person actually owns, not the fastest one in the
office.

**Reference hardware.** Dual-core, four-thread mobile CPU (T450 class), 8 GiB
RAM, SSD, 1600 by 900 panel, local terminal. The current smoothness targets name
a T470-class reference; v1 must hold on the weaker T450 class.

**Workloads.**

- A dense session with 200 retained messages, an active tool row and streaming
  output.
- An embedded terminal that streams while the session repaints.
- A Go project of about 100k lines with an installed language server, problems
  open and a rename in flight.
- Local History on, with one agent editing files.
- Sizes 80 by 24 and 160 by 40.

**Budgets.** These are the existing acceptance targets, applied on that
hardware:

- p95 input-to-paint at most 16 ms, with no UI-thread stall over 32 ms.
- Idle heartbeat at 2 Hz or more.
- No dropped model event, tool result or source update for presentation speed.
- Memory, CPU and startup recorded and bounded; the ceiling is set from three
  runs with limited headroom.

**Evidence.** Three runs before and three after on the T450-class machine, at
the same sizes and workloads. GOMAXPROCS=2 on a faster processor is a contention
test, not acceptance.

**Status: open.** Baselines exist for faster hardware. The weak-machine
acceptance run does not.

## Gate 2: trampoline sandbox

**Requirement.** Untrusted agent work runs in a throwaway sandbox, and the host
keeps control of what enters and leaves it.

**Design.**

- Two cores. The **master core** runs on the host and owns the shell, policy,
  approvals, auditing and host identity. The **trampoline core** runs inside a
  microVM and owns the workspace, agents, terminals, language servers and Git.
- The trampoline runs on [microsandbox](https://github.com/superradcompany/microsandbox):
  local microVMs with hardware isolation, OCI images, snapshot and fork, and an
  embeddable SDK. A sandbox starts in under 100 ms on that project's numbers.
- **The master hides host facts.** The trampoline gets no host home directory,
  hostname, environment, SSH agent, keyring, clipboard or network identity. The
  project arrives through one explicit mount. Secrets injected by microsandbox
  never enter the guest.
- **The master audits every message.** Each inbound and outbound message passes
  the master. The record holds the direction, the peer identity, the message
  type and size, the decision and the time. A refused message is a visible error
  in Crude, never silence.
- **Network is denied by default.** A session gets an explicit allow list. DNS
  goes through the master.
- **Lifecycle is throwaway.** Create, start, stop, fail, restart and delete are
  explicit and visible. A failed sandbox never leaves a host process behind. A
  fork copies the state; deleting the sandbox removes it.

**Non-goals.** A general container manager. Replacing the host core. Running
untrusted extensions outside the sandbox.

**Risks to measure.** Nested virtualization cost on a T450-class machine, file
sync latency, terminal latency through the audit hop, Windows host support
(WhPX or Hyper-V), and the audit log write cost.

**Acceptance.**

- An escape suite fails: path traversal, host environment read, clipboard read,
  keyring read, direct network attempt, forged peer identity.
- The audit log is complete: a scripted session produces exactly one record per
  message, in both directions.
- Kill and restart: a killed trampoline loses no host authority and reports the
  failure; a restart reattaches or starts clean, as chosen.
- Measured overhead on the reference hardware stays inside Gate 1 budgets.

**Status: open.** Nothing is implemented.

## Gate 3: history import

**Requirement.** A person can bring sessions from the coding agents they already
use, read them inside Crude, and keep working from them.

**Shape.** A visual browser in the product, in the spirit of
[agent-dump](https://github.com/xingkaixin/agent-dump): list, search, preview,
then import the chosen sessions. Imports are read-only against the source.

**Sources for the first cohort.** Codex, Claude Code, Cursor, Gemini CLI,
OpenCode, Kimi, Droid, Pi. The set is data, not code: a provider entry names the
path, the format parser and the fields it can map. New agents are added by data
where the format allows it.

**What an import carries.** Session identity, messages in order, tool calls and
their results, timestamps, model and provider, workspace root and attachments.
Unknown fields are kept as raw evidence instead of being dropped.

**Rules.**

- Never write to another tool's store. Checksums before and after prove it.
- Show the person what will be stored before storing it. Imported history can
  contain secrets.
- Mark imported sessions as imported. They do not silently join the live agent
  history.
- Repeat import does not duplicate.

**Acceptance.**

- One real fixture per provider, taken from that tool's own export.
- Round trip counts: messages, tool calls and results match the source.
- Search finds text across imported and native sessions with the same behavior.
- Large session, corrupt file, missing workspace and unknown format each fail
  clearly without touching the source.
- Source tree checksum is unchanged after every one of these cases.

**Status: open.** Crude has its own session store and resume; it has no import
from other tools.

## Gate 4: native ACP

**Requirement.** Crude runs the agents a person already pays for, under that
person's own login, without breaking the vendor's terms.

**What ACP is.** The [Agent Client Protocol](https://agentclientprotocol.com/)
standardizes the conversation between an editor and a coding agent over stdio
JSON-RPC. The current stable protocol version is 1. It defines session setup,
prompt turns, streamed updates, tool calls, permission requests and terminal and
file access negotiated through capabilities.

**Crude's role.** Crude is an ACP **client**. It starts the vendor's own agent
program, which authenticates with the person's own account the same way it does
in its own terminal. Crude does not extract, store, replay or share a
subscription credential, and it does not proxy one person's account to another.
The vendor's limits, login flow and terms stay with the vendor.

**Product integration.**

- ACP agents appear next to native agents, with their own model and session
  list.
- Permission requests become Crude approvals. No silent auto-approve path.
- File edits become ordinary workspace changes, so Git review and Local History
  still apply.
- Plan updates and streamed output paint through the existing chat rows.
- Cancellation, stop and resume follow the same run recovery rules.

**Non-goals.** Speaking a vendor model API directly. Sharing one subscription
between people. Bypassing a vendor limit or a login.

**Acceptance.**

- The ACP schema version 1 conformance tests pass for the client role.
- At least three real agents run end to end: a prompt, streamed output, a tool
  call, a permission prompt, a cancel and a resume.
- One live journey on Linux and one on Windows with a real subscription, driven
  only through the vendor's own login.
- A short written terms review that names what Crude does and does not do with
  the credential.

**Status: open.** Nothing is implemented.

## Gate 5: bidirectional text stays a release gate

**Requirement.** The bidi behavior that ships today cannot regress before v1.

**Criteria.** UAX #9 level suites, Arabic joining, editor isolates, logical
caret, selection and copy, arranged output in Windows Terminal and Linux
terminals, and correct RTL layout in chat, diffs, dialogs and the embedded
terminal.

**Evidence.** The existing conformance and layout tests, plus visual capture at
80 by 24 and 160 by 40 on Windows Terminal and one Linux terminal.

**Status: implemented.** Kept as a release gate.

## Gate 6: plugins

**Requirement.** People extend Crude with plugins without forking it. A registry
and a browser in the product find, install, update and remove them. Three
complete plugins ship with v1 and prove the platform: an HTTP client, a
database browser and a source-grounded notebook.

**Why a gate.** An environment that only its own team can extend stays small.
The three plugins are tools that developers now leave the terminal for. If they
can be built on the public API alone, the API is good enough for other people.

### Runtime: Lua or WebAssembly

The runtime is not decided. Both embed in the Go core, and both limit a plugin to
what the host API gives it.

| Question | Lua | WebAssembly |
|---|---|---|
| Embedding in Go | [gopher-lua](https://github.com/yuin/gopher-lua) (Lua 5.1 in pure Go) or a cgo binding to Lua 5.4 or LuaJIT | [wazero](https://wazero.io/) (pure Go, no cgo) or Wasmtime through cgo |
| Plugin languages | Lua only, familiar from Neovim, WezTerm and Yazi | Any language that compiles to WebAssembly: Rust, Go through TinyGo, Zig, C |
| Isolation | Crude loads only the libraries it allows, with no `os` or `io` | A memory-isolated module; only the imported host functions exist |
| Limits | Cancellation through the VM context; memory is shared with the core | A memory ceiling per module; a call stops when its context ends |
| Author loop | Edit and reload, no build step | A compile step for each change |
| Precedent | Neovim, WezTerm, Yazi | [Zed](https://zed.dev/blog/zed-decoded-extensions), [Zellij](https://zellij.dev/documentation/plugin-ui-rendering) |

**How the choice is made.** Build the same small plugin in both runtimes: one
view with a table filled from an HTTP request, one command and one setting.
Measure it on the Gate 1 machine: cold start, memory for each plugin, the cost
of one view update and of 1,000 table rows, and what happens when the plugin
loops forever or crashes. Record the numbers and the choice in the Decisions
table before the API is frozen. The rules below hold for either runtime.

### Where a plugin runs

A plugin runs on the core that owns the workspace: local, remote over SSH, or
inside the trampoline sandbox. The shell never runs plugin code. This is the
[VS Code remote model](https://code.visualstudio.com/api/advanced-topics/remote-extensions)
for workspace extensions: code runs where the files and the network are, and
only the UI reaches the client. The HTTP client sends requests from the
server's network, and the database browser reaches the databases that the
server can reach.

**Host API, no ambient access.** A plugin reaches files, network, processes,
secrets, models and the clipboard only through Crude's host API, and only with
the permissions the person granted. As a result:

- Crude's network rules apply: proxy, certificates and timeouts. A proxy failure
  never falls back to a direct connection.
- In the trampoline sandbox, plugin traffic passes the master core's audit and
  allow list like any other message.
- A revoked permission makes the next call fail with a clear error, which the
  plugin must handle.

### Plugin UI is Crude's UI

Plugins do not draw characters or write ANSI. They describe views with Crude's
shared components, and the shell paints them like its own panes. Like the
built-in components of Zellij plugins, this makes every plugin match the theme
and the controls of the product.

- **Exposed components:** tabs, list, virtual list, tree, table with virtual
  rows, key-value grid, forms and fields (text, text area, select, toggle,
  secret), buttons, action bar, split panes, text reader, Markdown, code view
  with syntax colors, chart, progress, spinner, status items, notices, context
  menus, palette commands, the shared confirm dialog, settings pages, and an
  editor tab bound to a real file.
- **The shell keeps ownership** of geometry, focus, keyboard and mouse rules,
  scrolling, selection and copy, width, theme tokens and bidi arrangement. Plugin
  views follow the [interaction matrix](../reference/interaction-matrix.md), and
  right-to-left text works in them with no plugin code.
- **Updates are changes against the last view.** Large data arrives by window: a
  table asks for its visible rows, never the whole result.
- **Every plugin event carries** the plugin, the surface and a generation. A
  stale result is dropped. A plugin call never runs on the UI thread.
- **View state lives in the core,** so a second shell, a shell that attaches
  again, or a reconnect after a slow link paints the same view.

**Contribution points** are declared in the manifest, so Crude can show them
before the plugin starts: views and tabs, palette commands and key bindings,
editor contributions (file types, gutter run actions, completion and hover),
status items, settings pages and agent tools. Agent tools follow the normal
approval rules. Being a plugin never approves a tool.

### Lifecycle

The lifecycle is per core, explicit and recoverable.

- **Install:** preview the manifest, permissions, publisher and exact bytes.
  Approval binds those bytes, the same rule as Skills and MCP servers.
- **Activate** on the first use of a contribution, not at startup. Startup with
  plugins installed stays inside the Gate 1 budgets.
- **Deactivate** an idle plugin and keep its state.
- **Update** atomically. A new permission needs a new approval. A failed update
  keeps the old version.
- **Fail alone.** A crash or a hang stops only that plugin. Its views show the
  state and a Restart action; automatic restarts use `internal/retry` with
  backoff. The core, the shell and the agentic loop continue.
- **Disable or remove** per core or per workspace. Removal asks before it
  deletes plugin data.
- **Workspace switch and shutdown** are generation boundaries: plugin calls are
  cancelled and plugin instances stop before the workspace closes.

### Several cores, different plugin sets

Each core has its own plugin set, as each remote host has its own extensions in
VS Code. The TUI must not break when the person switches cores in the Remotes
rail, opens a remote with `--ssh`, or enters a trampoline sandbox.

- The shell shows the contributions of the core it is attached to, and nothing
  else. Menus, the palette and the rail change with the core.
- A plugin tab from the previous core closes cleanly. A saved layout that names
  a plugin the new core does not have shows a placeholder, for example "HTTP
  Client is not installed on build-host.", with **Install on build-host** and
  **Close**.
- The shell and the core agree on a plugin API version when the shell attaches.
  A view that needs a newer component set than the shell has shows "This view
  needs a newer Crude." instead of a broken view.
- A workspace can recommend plugins. Crude asks before it installs them on a
  core.

### Registry and browser

- **Registry:** an index of plugin entries with versions, publisher, source
  repository, permissions, supported API versions, checksums and signatures.
  A plugin is published by a pull request to the index repository, as with
  [Zed's extensions repository](https://github.com/zed-industries/extensions).
- **Private registries** are extra sources, like the Skills and MCP sources
  today, so a team can publish internal plugins.
- **Browser:** in Settings beside Skills and MCP, with the same collection
  rhythm: search, filters, details with the readme, permissions and versions,
  then **Install on** the selected core. Installed plugins show updates,
  permissions and data size.
- Crude never installs or updates a plugin without consent. Removing a registry
  source does not remove installed plugins.

### The three plugins

All three use only the public plugin API and install from the registry. A
private shortcut that a third-party plugin cannot use does not count.

**HTTP client,** in the class of the
[JetBrains HTTP Client](https://www.jetbrains.com/pages/idea/http-client/) and
Postman.

- Requests live in `.http` files compatible with the JetBrains HTTP Client
  format, so they diff, get reviewed and travel through Git. A gutter action
  runs a request. A form view edits the same request for people who prefer
  forms.
- HTTP/1.1 and HTTP/2, TLS with client certificates, WebSocket, Server-Sent
  Events, GraphQL and gRPC.
- Environments in a shared file and a private file for secrets; variables,
  dynamic values, and response handlers that capture values and check results.
- Authentication: Basic, Bearer, API key, and OAuth 2.0 with authorization code
  and PKCE, client credentials, and device code.
- Response view: status, timing (DNS, connect, TLS, first byte, total), headers,
  cookies, formatted JSON, XML and HTML, large bodies streamed to disk, images
  through the existing Kitty and Sixel path, and a diff of two responses.
- History for each workspace. Run all requests in a file and get a pass and
  fail summary.
- Import from cURL, Postman collections, OpenAPI and HAR. Copy a request as
  cURL.
- Network tools: DNS lookup, TLS certificate details and a port check.
- Agents can run a named request as a tool, with approval.

**Database browser,** in the class of DataGrip.

- First cohort: PostgreSQL, MySQL and MariaDB, SQLite, and SQL Server.
- Connections run on the core, so a database that only the server can reach
  works without a tunnel. Passwords go to the secret store, never to workspace
  files.
- Schema tree with lazy introspection: schemas, tables, views, columns, keys,
  indexes and functions.
- Data grid with virtual rows, sort and filter. NULL shows apart from empty
  text. A cell viewer shows JSON and long text. Copy rows as CSV, JSON or
  INSERT statements.
- Query console in Crude's editor: completion from the schema, run the
  statement or the selection, several result tabs, cancel a running query,
  explain plan and query history.
- Safe changes: grid edits are staged and shown as SQL before they run, in one
  transaction with commit and rollback. A connection marked as production is
  read-only until the person unlocks it. A statement that drops, truncates, or
  deletes without a WHERE clause needs confirmation.
- Export a result or a table to CSV and JSON.
- Agents can read the schema and run read-only queries as tools, with approval.

**Open decision: where the drivers live.** Neither runtime can open a database
socket by itself. Either the host API offers raw TCP and TLS streams under the
network permission and the plugin speaks the wire protocol, or the host API
offers SQL through drivers that Crude ships. Decide this with the runtime.

**Notebook,** in the class of NotebookLM.

- A notebook is a set of sources, a chat and notes. It is stored as files in
  the workspace, so it can go through Git.
- Sources: workspace files and folders, Markdown, PDF, web pages, pasted text,
  and Crude sessions and agent reports.
- Grounded answers: each claim cites the exact passage in a source. A citation
  opens the source at that passage. When the sources do not answer a question,
  the answer says so.
- Studio outputs as Markdown notes: briefing, study guide, FAQ, timeline, and a
  mind map shown as a tree.
- It uses the person's model accounts and shows the cost, like any agent run.
  Sources are indexed on the core.
- Arabic, Persian and Hebrew sources work in every view, under Gate 5.
- Not in v1: audio and video overviews.

### Acceptance

- The runtime choice is recorded with the spike measurements from the Gate 1
  machine.
- A conformance suite covers the host API, the view protocol and the
  lifecycle, and runs against a test plugin.
- Isolation: a plugin without the permission cannot read files, open a
  connection, start a process, or read secrets or the clipboard. Each attempt
  fails with a clear error. An endless loop or a crash affects only that plugin.
- Cores: with two cores that have different plugin sets, switch 50 times with
  plugin views open and requests in flight, locally and over the slow link of
  the [network lab](../guides/remote-network-lab.md). No crash, no stale view, no
  event sent to the wrong core, and no plugin instance left after its workspace
  closes.
- Registry: install, update with a new permission, a failed update that keeps
  the old version, removal, a private source, and a bad signature that refuses
  to install.
- Each plugin: an end-to-end journey on a local core and on a remote core, on
  Linux and on Windows, with captures at 80 by 24 and 160 by 40.
- Performance: the Gate 1 budgets hold with the three plugins installed and one
  view of each open. A table with 1,000,000 rows scrolls inside the budgets.
- Trampoline: when Gate 2 is done, the HTTP client in the sandbox reaches only
  allowed hosts, and each request has one audit record.

**Status: open.** Nothing is implemented. Skills and MCP servers already have
install previews, approval of exact bytes, sources and revocable grants; the
plugin store reuses that flow.

## Order

1. Gate 1 measurement, because it sets the budget every other gate must fit.
   The Gate 6 runtime spike can run beside it, because it needs the same
   machine.
2. Gate 3 import, which is independent and useful early.
3. Gate 4 ACP, independent of the sandbox.
4. Gate 6 plugins. The platform comes before the trampoline, so that the
   sandbox design includes plugin traffic.
5. Gate 2 trampoline, the largest change, using the Gate 1 budgets.
6. Gate 5 acceptance, then the release decision.

## What v1 does not promise

- A macOS release.
- A multi-user server or a hosted sandbox service.
- Hardware isolation for plugins outside the trampoline sandbox. Outside it, a
  plugin is limited by its runtime and the permissions the person grants.
- Paid plugins, ratings or reviews in the registry.
- Any vendor credential handling beyond the vendor's own login.

## Acceptance record

| Gate | Area | Status |
|---|---|---|
| 1 | Smooth on a T450-class machine | Open |
| 2 | Trampoline sandbox | Open |
| 3 | History import | Open |
| 4 | Native ACP | Open |
| 5 | Bidirectional text | Implemented, kept as a gate |
| 6 | Plugins: platform, registry and three plugins | Open |
