# Core and Shell Separation

> Status: Active implementation. The existing application is still single-shell; the acceptance gates below, not the presence of an IPC package, determine completion.

## User-visible outcome

One user-owned core serves multiple project workspaces and any resource-supported
number of independently rendered TUI clients. Attaching does not resume/restart an
agent. Closing the originating shell does not stop agents, children, commands,
permissions, or persistence while another client is connected or server mode is
enabled. Remote clients use an authenticated encrypted transport. There is no
claim of physically unlimited clients or immunity to core/host failure.

## Initial audit (before extraction)

- `app.App.Run` closes all sessions when `tui.TUI.Wait` returns.
- `Workspace.SetEventSender` and `EventBus.SetProgram` bind services to one TUI.
- `viewmodel.handleAgentDomainEvent` creates agent inventory and reduces semantic
  conversation state. Broadcasting it to shared mutable state would reduce twice.
- `Workspace.ActiveSession`, `FocusStack`, `AgentManager.active/Hidden`, and
  width-dependent thinking panes mix client selection with shared runtime state.
- `Workspace.SwitchProject` tears down the prior workspace, rather than switching
  one client's attachment to another retained workspace.
- Composer drafts use one session key; concurrent clients would overwrite them.
- LSP lifecycle and editor buffer ownership cross the UI/domain boundary. A PTY
  has one size and protocol-reply owner; multiple emulators must not answer it.
- Terminal width calibration is process-global. Multiple remote-rendered Apps in
  one process cannot safely calibrate different terminals.

## Implemented foundation checkpoint

The following packages now exist, but `app.App.Run` still calls
`tui.Start(*Workspace, ...)` and closes sessions/workspace after the shell exits.
No public server/attach flags or fully connected TUI have replaced this path.
The current transport contract is documented in the
[core client protocol reference](../reference/core-client-protocol.md).

- Shells own selected session/model, focus, exact-node agent visibility and
  reversible width-dependent thinking projections. Canonical conversations and
  explicit session identities belong to workspace; neutral ingress/effects no
  longer require TUI imports. The standalone path still schedules agent reduction
  on the shell, whereas the new core path has its own actor.
- `core.Runtime` retains one actor per canonical workspace, serializes domain
  commands/agent ingress/ticks and establishes snapshot/subscription boundaries.
  Detach releases subscriptions and exact resource owners without closing the
  retained workspace. This is implemented infrastructure, not CLI survival proof.
- `coreprotocol`, `corewire` and `coreclient` provide closed typed service
  operations, framed local/SSH transport primitives, bounded queues, explicit
  resynchronization and durable mutation receipts. Unfinished receipt intent after
  a crash is uncertain and cannot authorize blind replay. Conversation deltas
  avoid full transcript exports on each change.
- `coreclient.WorkspaceView` maintains detached client read models and preserves
  surviving session/node/conversation identities on resync. Paint reads local
  projections without RPC or semantic reduction. Real shell consumers still need
  complete migration to these projections.
- `workspace.DocumentService` owns live document revisions, savepoints,
  owner-specific undo and borrowed save/LSP services. `tui/editor` has a
  watcher-free shared-document adapter with pending/in-flight edits and exact
  acknowledgements. It is not yet the universal product editor path.
- `terminalvt`, `workspace.TerminalService`, `shellterm` and `termview` now form
  one PTY parser/reply owner and immutable client readers. Exact controller
  leases fence input/resize; standalone-shell commands run asynchronously in FIFO
  order. Selection, history pages, clipboard offers and placement are client
  policy. See [in-app terminals](../reference/in-app-terminals.md).
- Typed Git, Memory, Board, files/search, Local History, reference/media, prompt
  history, session settings, account/preferences and capability operations exist.
  Account secrets use write-only keep/replace semantics and redacted read models.
  Existing settings/capability registration authorities remain authoritative;
  transport methods alone do not complete their real form/approval journeys.
- `clientstate` provides private client/workspace/session draft namespaces,
  epoch-fenced attachment/takeover, revision CAS and durable request receipts.
  Product composer wiring, profile/path selection and existing draft data
  alignment remain outstanding. No automatic legacy migration is authorized.

### Remaining product cutover

1. Replace the direct Workspace startup and all shell-scheduled semantic ingress
   with one core-owned product path, without keeping a second mutable owner.
   Wire every real command, decision, recovery, launcher and workspace switch;
   switching must detach one shell rather than tear down peer work.
2. Finish connected settings/account/capability forms, application registration
   publication and immutable acquisition/access approvals. Preserve exact
   preimages, secret redaction and app/workspace binding rules.
3. Integrate private drafts and reference/history transfers in the actual composer.
   Back up and manually align existing development data outside product code;
   verify text, attachments, identities and resume before changing persisted owners.
4. Route every editor/save/undo/LSP/watch journey through shared document authority;
   finish shared document UI projections, independent selection and conflict UX.
   Complete connected terminal control/release/detach and clipboard-offer journeys.
5. Integrate prompt history, Git/Local History, Memory/Board, file operations,
   media and client-local desktop effects without UI-thread I/O or duplicate stores.
6. Implement daemon/autostart, public server/attach/stdio CLI and graceful shutdown
   authorization. The SSH helper's fixed remote command is not implemented CLI.
7. Run the full acceptance matrix below: multi-process origin-shell survival,
   zero-shell server mode, SSH failures, native Windows pipes/ConPTY, desktop and
   narrow UI, failure injection, and measured twenty-workspace resource use.

### Verification boundary

The current integrated checkpoint passes `go test ./... -count=1 -timeout=180s`,
`go build ./...`, `golangci-lint run ./...`, `make bench-gate`, and
`make docs-check design-audit`. Core/client/transport/draft/terminal packages pass
their complete race suites; focused workspace/session/viewmodel ownership and
terminal journeys also pass under the race detector. A Windows/amd64 no-CGO
cross-build passes; it does not establish native Windows pipe or ConPTY behavior.

Ten in-process core connections share one agent/provider execution; wire tests
exercise ordered real-PTY input and disconnect/reconnect leases. Deterministic
tests cover delayed first document open after detach, pending terminal acquire,
unsolicited output/exit and controller-only clipboard offers. These are not ten
independent rendered product shells.

Sequential real-App terminal captures at 160×40 and 80×24 verify asynchronous
creation, rendered output, focus, acknowledged dock resize, retained immutable
snapshots and explicit clipboard/control menu. Both fixture sentinels passed and
dedicated windows/processes closed. System clipboard writing and physical
cross-client takeover were not exercised. No golden or allocation ceiling was
relaxed. The full multi-process/server/SSH acceptance matrix remains outstanding.

## Architecture decisions

Use locally rendered independent shell processes, not a mirrored terminal or
server-side Apps sharing a mutable Workspace. The core owns one retained runtime
per canonical workspace, with serialized domain admission/reduction. Clients own
layout, terminal calibration, focus, selection, scrolling, tab visibility and
drafts. Shared state crosses the boundary as versioned immutable data, never Go
pointers, database handles, callbacks or reflected method invocation.

The application core owns configuration/account refresh, catalogs and workspace
lifetimes. Existing session execution, collaboration epochs, durable context
publication, filesystem mutation leases and Local History remain their respective
authorities. Client detach is not runtime cancel, shutdown or passive resume.

Local transport must be private to the OS user (Unix socket / restricted Windows
pipe). Remote attach uses system SSH with normal host-key verification and a
fixed stdio bridge; no unauthenticated TCP listener, shell interpolation of
project paths, provider secrets in snapshots, or automatic agent forwarding.
Same-user access is full access to that user's core, not multi-tenant isolation.

Attach must establish an atomic snapshot/subscription boundary. Slow clients get
bounded queues and explicit resynchronization rather than blocking domain work
or silently dropping mutations. Requests carry client identity, request identity
and exact workspace/session/run/revision preconditions. Uncertain mutation
responses are recovered by receipt, never blindly replayed. Decisions resolve
once for the exact pending block; losing a client never implies approval.

Shared documents need one live-text/revision/save/LSP authority; client cursors
and selection remain independent. Shared PTYs need one emulator/reply authority
and explicit input/resize control, with client-local scroll/selection. Desktop
and clipboard effects execute only on the originating client after explicit
interaction. These flows are release gates, not optional future adapters.

## Ordered implementation gates

1. **Extract ownership without changing current UX.** Separate shell view state
   and semantic reducers from resource owners; replace concrete program ingress
   with a neutral sink. Preserve current rendering, decisions, drafts and tests.
2. **Own core lifetime and workspace registry.** One core per user/data directory,
   one runtime per canonical root, no duplicate creation on concurrent attach,
   explicit detach/shutdown and independently retained workspace attachments.
3. **Define and integrate the typed client protocol.** Immutable snapshots,
   bounded deltas, durable mutation receipts, cancellation, reconnect and exact
   protocol negotiation. Migrate real TUI commands and projections, not a second
   demonstration UI. Remove the old direct runtime startup path at cutover.
4. **Finish resource and security journeys.** Shared document/LSP and terminal
   authority, media transfer, settings transactions, client-local effects and
   drafts, private local startup and SSH attach, server mode and graceful stop.
5. **Release acceptance.** Multi-process integration, race/build/test/lint/docs,
   cross-platform compile and native platform checks, desktop/narrow visual
   acceptance, resource measurements and failure injection.

## Acceptance matrix

- Two and ten real shells see identical live root/child identities, transcript,
  tool outcomes and usage, with exactly one provider/tool execution.
- Different shell sizes, focus, tabs, drafts, scrolling and glyph calibration
  remain independent; a local tab close does not hide another client's tab.
- Close/kill the originating shell mid-stream/tool/permission. Remaining shells
  stay operational with unchanged runtime epoch/run identity. In server mode,
  repeat with zero shells and attach again without historical replay.
- Concurrent attach/resume and core autostart do not create competing owners.
  Core crash recovery is passive; it is not advertised as uninterrupted work.
- Switching one client to another project leaves peers and old-project work
  alive; failed switching preserves the old attachment.
- Concurrent input, permission, settings and file mutations resolve by exact
  receipts/preconditions. A stale response never consumes another client's draft
  or approves a replacement request.
- Interrupted snapshot, lost response, stale client epoch, slow reader and
  malformed/oversized protocol input fail safely with bounded resources.
- LSP, watchers, memory jobs, capability transports and PTYs have one resource
  owner. Client disconnect releases only its views/subscriptions.
- Socket/pipe permissions, SSH host-key rejection, secret redaction, shutdown
  authorization and same-user threat boundaries receive explicit coverage.
- Measure memory/CPU for twenty workspaces and multiple views; consolidation
  alone is not proof of a smaller footprint.

## Research

Kakoune's independent-client model is the closer ownership analogy than terminal
mirroring. Neovim's remote UI/API documents demonstrate typed UI attachment and
warn that RPC access permits arbitrary execution; multiple attached UIs alone
do not establish independent focus. OpenSSH provides authenticated encrypted
channels, but remote command arguments are shell-joined, so paths belong in the
framed protocol, not an interpolated remote command.

- <https://kakoune.org/why-kakoune/why-kakoune.html>
- <https://neovim.io/doc/user/api-ui-events/>
- <https://man.openbsd.org/ssh>

Delete this plan only after current architecture/reference/guide documents state
the implemented contract and every outstanding limit explicitly.
