# Core client protocol

> Status: Current connected CLI/core contract. Shared editors and SSH are implemented; native-platform, distinct-host and final resource acceptance remain open.

Normal `crude` startup uses `app.ConnectCore` and `shellport/remote.Attach`.
The per-user core owns runtime workspaces/sessions, configuration/accounts,
catalogs and prompt history; independently rendered shells detach without stopping
peer execution. `--core [--server] [--core-grace d]`, `--core-status`,
`--core-stop [--force]` and `--core-stdio` are implemented. There is no direct
in-process interactive CLI fallback. Follow the
[active separation plan](../plans/core-shell-separation.md) for remaining
integration and release acceptance; connected startup is not universal resource
migration or proof of uninterrupted work after a core crash.

Same-build startup publishes one immutable staged executable under the data
directory using atomic no-replace hard-link publication. Concurrent losers never
replace the inode another shell is executing. Unsupported hard-link filesystems
and invalid existing image type/size/mode return explicit staging failures.

## Ownership and attachment

`core.Runtime` accepts an application-owned workspace factory and private SQLite
receipt journal. Concurrent creation converges on one actor per canonical project.
Actors admit commands and agent events, flush accepted text on their own ticks,
reduce workspace state and publish revision changes. Resource I/O is prepared or
performed off the actor where required and completed under owner fences.

Before publishing a retained actor, its factory worker restores saved Notices
and initializes configured capability metadata. Neither requires a model session
or a shell to open Settings. Initialization never connects MCP, downloads skills
or replays tools; failures retain a usable shell and an explicit workspace notice.

A connection has an immutable client ID and attachment epoch. Every request is
bound to the negotiated owner; selecting another tab is not a change of owner.
`workspace.attach` installs a subscription and captures its snapshot at the actor
boundary. Detach/connection close removes subscriptions and document/terminal
attachment authority, not the retained workspace or its sessions. Explicit runtime
close owns final shutdown. Connected workspace switches prepare the destination before
teardown of client-local resources, then replace only that shell attachment. A
failed destination leaves the old attachment usable; peers keep the old workspace.

Successful attachments record durable workspace access time off the actor loop,
including returns to retained actors. Both recent-workspace views sort this
metadata newest-first; session creation and background activity never reorder it.
Different-workspace UI navigation requires an exact-destination Cancel-default
confirmation before attachment preparation, followed by the existing dirty/draft
checks. A cancelled confirmation performs no destination I/O.

Snapshots are detached values, not service pointers, SQL handles, callbacks or
reflected Workspace calls. `coreclient.WorkspaceView` installs snapshots and
conversation deltas on the client's serialized UI thread, retaining surviving
session/node/conversation identities. Paint reads its local projection without
RPC, persistence or semantic event reduction. The remote collector drains events while a coalesced fetch worker builds batches;
`StartReplica` releases publication only after the shell sink is ready. Update
alone installs batches and presents neutral effects. Pane renderers use the
canonical replica Conversation. Create/open are off-UI; pending root activation
waits for replica inventory and peer-created roots preserve editor focus.

## Ownership across core and shell

Closing a shell detaches it; it never closes sessions or the retained workspace,
and server mode retains work with zero shells. Never attach multiple Apps to one
mutable Workspace.

- `App` owns selected session, model display alias and focus; `Workspace` has no
  active session. Lifecycle, backend lookup and hydration require explicit session
  IDs. `NewAgentSession`/`OpenAgentSession` return identities without selecting a
  shell; opening an already-live session reuses its backend.
- `App.AgentView` owns selected agent and exact-node tab visibility. AgentManager
  owns inventory only; no shared Hidden/active field may return. Width-dependent
  thinking panes are reversible shell projections of the canonical thought, not
  another Conversation or a core ThinkingPaneID. Each owner uses its own retained
  geometry and session latch; promotion never rewrites shared transcript content.
- `EventBus` accepts a neutral `MessageSink`; the TUI owns the concrete program
  adapter. It remains synchronous single-sink ingress, not a queued/replayable
  subscription stream. Its debug log is not delivery or durable evidence.
- `Workspace.ApplyAgentEvent` owns semantic inventory, conversation, prompt,
  status and request-keyed usage reduction; `AgentState.ApplyEvent` returns neutral
  effects. Root registration and cold hydration install retained canonical state,
  never a one-shot client tail. Snapshot messages detach nested attribution and
  references. Core actors now own semantic ingress/ticks; connected shells install
  immutable deltas on Update and present accepted effects without reducing again.
  `workspace/` must not import Bubble Tea or TUI rendering.
- `lspclient/` owns the nonvisual LSP transport. Workspace installs its default
  runtime without TUI construction. Shared documents own didOpen/didChange/save;
  exact per-client views release only their own LSP interest. Detach never stops a
  peer's language server or replaces canonical text with shell-provided bytes.
- The new `core/` path owns retained workspace actors, atomic snapshot/subscription
  admission, bounded resource workers, durable mutation receipts and private framed
  client transport. `coreclient.WorkspaceView` is a detached read mirror, never a
  fake Workspace containing runtime services. `shellport/remote` is the only CLI
  runtime path; `shellport.Local` remains core/test infrastructure. Read
  `docs/reference/core-client-protocol.md` before extending it. `--ssh` selects a
  strictly host-key-verified system-SSH connection to the fixed stdio bridge;
  remote paths stay in frames. Ten real Linux shells, twenty retained workspaces
  and loopback SSH have measured acceptance, not physically unlimited/efficient
  resources or distinct-host/native-Windows acceptance.
- Retained core creation hydrates Notices and initializes capability metadata on
  its factory worker before actor publication, never from shell startup or a
  model session. Passive capability observations bypass long settings operations;
  editable settings reads retain ordered preimages. Explicit refresh sequence plus
  workspace generation fences stale cancellations. Terminal create receipts mount
  exact returned identity before inventory; Notices→Terminals must repaint.
- `workspace.TerminalService` now owns the one `terminalvt` parser and reply source
  before PTY reads start in the retained core. `shellterm`
  consumes immutable rows and typed asynchronous intents; `termview` owns local
  scroll/selection/wrapping-aware copy. Never restore a second shell parser or raw
  reply-capable output feed. Input/resize use exact controller leases and ordered
  queues; OSC clipboard text is an explicit controller-local copy offer. Placement
  is shell-local. Detach revokes authority, not the PTY.
  Historical row requests carry the exact history epoch/base and desired recovery
  viewport through the same typed method; eviction displacement and the complete
  detached page are resolved under the terminal service's serialization. Shells
  atomically commit complete desired viewports, not an active-grid clear followed
  by a competing history refill. See [in-app terminals](in-app-terminals.md).
- `workspace.DocumentService` and the editor's shared-document adapter provide
  revision-fenced live text, safe pending edits and author-specific undo. The
  connected product editors use that authority for every open/edit/undo/save;
  shell PieceTables are optimistic projections with private cursors and no source
  watcher. SQLite current-format checkpoints plus revision-fenced operation deltas
  and exact receipts commit before accepted edits; cold recovery is passive.
  Stable document IDs survive durable file/directory relocation. Core file and
  document watchers publish invalidations; shell reads/transfers use typed RPC.
  Lost edit/save/format outcomes recover their original receipt, never replay an
  effect. Native conflict decisions default to Keep draft; explicit Use shared
  backs up private work and never overwrites collaborators. `document.snapshot_save`
  is checked disk infrastructure, not another live-text authority. `clientstate`
  persists private composers and unresolved editor evidence with profile locks,
  epochs and CAS; old data requires backed-up manual alignment outside product
  code. Prompt history has one core writer. Invocation account/model/provider/key
  selectors are captured per new/cold session, not application defaults; keys are
  write-only ephemeral and `--system` extends the canonical profile overlay.

## Wire format and bounded delivery

Protocol version 1 uses length-prefixed JSON frames: a four-byte big-endian length
and an envelope containing kind, request/client/workspace identity, epoch,
revision, method, typed payload and optional fault. Hello/welcome negotiate version
and frame bounds; `connection.ready` returns the exact owner. Malformed framing,
wrong owners and invalid envelopes close the stream rather than reusing a
partially consumed connection. Concurrent writes cannot interleave.

Current bounds include:

| Boundary | Limit/policy |
|---|---|
| Encoded frame | 8 MiB maximum; negotiated framing bound |
| Actor mailbox | 128 commands, 1024 agent events, 256 service events |
| Connection subscription | 64 changes; overflow invalidates cursors and emits explicit resync |
| Server output | 64 frames and 8 MiB queued bytes; overflow disconnects |
| Server requests | 16 in flight; duplicate active request IDs are rejected |
| Transport deadlines | 10-second handshake, 30-second server write |
| File transfer | 256 KiB chunks, 64 MiB read ceiling |
| Large response payload | 64 MiB raw JSON; 4 payloads / 128 MiB per connection, 30-second inactivity TTL renewed on reads |
| Terminal projection | 512 rows per page; bounded engine grid/history |

Producer mutation is not silently dropped to accommodate slow readers. Change
events advertise committed revisions/changed identities. `agent.changes` returns
conversation order/upsert/removal deltas or a reset when needed; snapshots are a
boundary/resynchronization operation, not a per-frame transcript export. A resync
or disconnected observer must fetch fresh state before trusting its old cursor.

## Typed methods

The exact request/result definitions live in `coreprotocol/*.go`; dispatch is
closed and typed, not arbitrary method invocation. Current families are:

| Family | Operations |
|---|---|
| Workspace | `workspace.attach`, `.detach`, `.snapshot`; `agent.changes` |
| Session/agent | `session.create`, `.open`, `.list`, `.snapshots`; `agent.submit`, `.steer`, `.decide`, `.cancel`, `.terminate`, `.yolo`, `.recover`, `.recover_cancel`, `.recover_reject`, `.stop_command`, `.compact`, `.reset_overrides`, `.append_messages` |
| Receipts | `request.receipt`, `request.cancel` (exact in-flight job) |
| LSP | `lsp.snapshot`, `.open_file`, `.close_file`, `.queue_file_update`, `.notify_file_save`, `.notify_watched_files_changed`, `.set_active_file`, `.clear_diagnostics`, `.start_for_workspace`, `.stop`, `.restart_server`, `.stop_server`, `.references`, `.completions`, `.definition`, `.document_symbols`, `.hover`, `.rename` |
| Status | `status.snapshot`, `status.mutate` (notifications/issues/operations); shell transients stay local |
| Documents | `document.open`, `.read`, `.sync`, `.close`, `.edit`, `.undo`, `.redo`, `.save`, `.format`; separate `.snapshot_save` checked disk commit |
| Response transfer | `payload.read`, `payload.release` (connection-private immutable responses) |
| Terminals | `terminal.list`, `.create`, `.rows`, `.control`, `.release`, `.input`, `.resize`, `.close` |
| Files | browse/catalog, chunked reads, create/folder/rename/delete/edit/search, canonical `files.replace_plan` and saved workspace listing |
| Local History | timeline/file/stats, operation/path/message/agent plans, apply/verify/prune/capture |
| References | create/import/list, content/media reads, exact content save, rename/delete |
| Prompt history | `history.list`, `.record`, `.restore`, `.read-media` |
| Git | snapshot/comparison/annotation, reviewed stage/unstage and hunks, staged commit, commit inspection, checkout and reviewed discard |
| Memory/Board | search/episodes/inspect, revision-fenced pin/forget/correct; Board snapshot/read acknowledgement |
| Settings | redacted fetch, session read/save/reset/delegation toggle, account change/models, application preference save; `workspace.overrides`, `.overrides_save`, `.startup`, `.tool_layout`, `.tool_layout_save` |
| Capabilities | runtime/snapshot/action, acquisition preview and exact skill-input preparation |

Connected adapters use these families; method existence is not proof that the real settings form, composer, Git review,
history preview or capability approval journey uses it. Existing domain rules
remain authoritative, including Local History safety previews and application vs
workspace capability registration; the transport cannot widen a grant.

## Mutation receipts and uncertainty

Journaled mutations persist request intent before effects and their exact response
afterward. Identity is client ID plus request ID; its digest binds workspace,
method and payload, not the reconnect epoch. Reuse with changed content fails.
Completed outcomes are immutable. A crash leaving pending intent marks it
`uncertain` on journal reopen, never completed or safe to replay.

After a lost response, retain the original request identity and query its receipt
after reconnect. Do not issue the same effect under a fresh ID just to discover
whether it happened. Receipt recovery is not a distributed transaction across all
external effects, and uncertainty needs explicit reconciliation. Input admission
also returns its domain receipt; a late acknowledgement cannot consume another
draft. Permission/question decisions resolve the exact pending block once;
disconnect never implies approval.

Connected composer input has one authoritative route: `agent.submit`. The core
asks the execution owner whether that input can steer its exact live run,
otherwise reserves a fresh turn or durably queues input behind compaction or
terminal sealing. A shell's lagging running/idle projection never decides the
publication path. A `steered` receipt names its immutable event, run and runtime
epoch; queued input has no context revision until dequeued; a fresh turn has its
exact generation/context receipt. All consume only the submitting shell's
unchanged draft. Unknown storage/transport results never fall through to another
send, and explicitly stopped/unfinished work is never silently resumed.

Manual compaction has an operation identity independent of the last model-turn
generation. `agent.cancel` carries that exact operation ID when present, rejecting
late Stop requests against replacement work. Compaction jobs observe exact request
and actor shutdown cancellation. Owner preparation starts only after capacity and
receipt checks; receipt replay must not reserve another operation. Queued input
receipts have no turn/context revision until their atomic dequeue/admission, and
pending message flags survive conversation reset/delta JSON projection.

Connected account forms retain their opaque original account revision; a newer
replica cannot silently authorize an old form. Application/workspace forms retain
the corresponding original preference revisions. Sparse workspace saves are
revision-fenced and serialize with other settings jobs: workers commit storage,
then the actor alone installs session/LSP projections. Stored-secret markers mean
explicit Keep, never a credential read or an empty replacement.

Passive `settings.capability.runtime` and `.snapshot` reads bypass the serialized
settings lane so a pending Connect/Sign in cannot starve home polling. Editable
settings reads and writes remain ordered after prior publication. Each shell's
explicit capability refresh has its own sequence as well as workspace generation;
cancelled or superseded results cannot replace newer success or cancel a newer poll.

## Shared resources and private state

`workspace.DocumentService` is the universal connected editor text/revision,
savepoint, author-specific undo and LSP authority. `shellport.LiveDocuments`
provides Open/Sync/Edit/Close/Save and optional Format/receipt interfaces.
`DocumentOwner` includes the attachment epoch. The Update coordinator installs
immutable snapshots/deltas, retains optimistic edits and orders off-UI commands.
Edits, undo, formatting and saves fence exact revisions; a lost response is
observed through its original receipt, not resent as a new mutation. Reconnect
reopens client views before sync. Cursors, selection and scrolling stay private.

Document GroupID identifies a logical editing transaction, not an RPC batch.
The shell retains group boundaries while publishing ordered optimistic edits;
Undo/Redo gestures and later input wait for exact command completion and complete
sync. History deltas preserve the reversed group ID. Core operations retain
explicit inverse-of revisions in checkpoints/deltas, allowing cancelled history
pairs to normalize before safe disjoint rebase; equality of text never proves
cancellation. Undo/redo also roll bounded history by whole units. Old operations
without provenance stay opaque, not inferred. See the
[editor transaction contract](editor.md#edit-transactions-and-history).

Accepted unsaved text, savepoints, history and exact receipts persist in the
workspace SQLite store. Structural checkpoints and append-only command deltas
restore data without executing historical commands. This does not promise an
uninterrupted turn after core failure. Document IDs stay stable across rename;
Path is mutable metadata. Durable batch intents fence rename, preserve dirty
unsaved text and protect conflicted deletes. Core owns one project watcher and
lazy document-directory watching; connected shells do not watch/read project
source files. File browse/catalog/read and replace-plan RPCs use core paths.

The connected tree consumes the same Git snapshot as the header and Git panel,
not a client-local resolver. It retains complete status and diagnostic metadata
before directory pages arrive, including collapsed descendants and filtered search
matches. Ordinary file changes as well as repository metadata arm reconciliation;
clean snapshots and removed diagnostics clear previous markers.
Explicit desktop opens materialize bounded client-local caches, not remote paths
passed to local filesystem readers.

Typed LSP uses per-client views and versioned diagnostics/health; document
open/close/detach releases only that client's views, never shared servers.
`document.snapshot_save` remains a separate checked immutable disk-commit API,
not the ordinary connected editor text authority.

Private unresolved editor recovery lives beside composer drafts in `clientstate`.
The native Resolve Shared Editor Changes dialog defaults to **Keep draft**;
**Compare with shared** opens read-only immutable snapshots. **Use shared text**
requires confirmation, a durable private backup and exact recovery tombstone.
It discards only private pending text, never collaborators' text. Pending/unknown
commands must settle by receipt first and are never automatically replayed.
An external disk conflict is a different precondition: choosing shared text does
not authorize overwriting the changed disk file.

`workspace.TerminalService` installs one neutral `terminalvt` engine before first
PTY output and owns all child query replies. Readers receive detached row pages,
not bytes to parse again. Exact client/epoch/generation leases fence input and
resize. Selection, scroll, wrapping-aware copy, focus and dock/floating placement
are shell-local. OSC 52 writes are offers for explicit client copy, never automatic
broadcast system-clipboard effects. See [in-app terminals](in-app-terminals.md).

`clientstate` is a separate private composer store keyed by client profile,
workspace and session, with attachment epochs, record revision CAS and request
receipts. It preserves text, reference spans, cursor, selection and scroll; it
is wired to the product composer at `~/.crude/client-state.db`.
`--client-profile NAME` selects a durable exclusive namespace; the default uses
stable terminal/tmux identity, never a PID. An OS profile lock excludes a live
owner before exact epoch-CAS crash-left takeover. Old drafts require backed-up
manual alignment outside product code, not automatic conversion.
Prompt history is core-owned (`history.*`); shells install detached lists through
`history.InstallReplica`, record via RPC and restore attachments into the exact
owning session off-UI. A replica is not a second persistence authority.

## Trust, secrets and platform limits

Local transport is same-user authority, not multi-tenant isolation. Unix uses a
0700 user-owned socket directory, a 0600 socket and an exclusive owner lock; it
converges on an exclusive owner lock and reclaims crash-left endpoints only under
that lock, never by deleting a guessed live owner. Windows
uses a user-SID/data-directory named pipe with a protected DACL granting that
user and denying network logons. Neither mechanism restricts an already trusted
same-user client to harmless reads.

SSH transport starts system `ssh` without a local shell, enables strict host-key
checking and disables forwarding, remote/local command overrides and shared
control sessions. The remote command is fixed to `crude --core-stdio`; project
paths belong in frames, never interpolated shell arguments. CLI selection is
`--ssh HOST --workspace REMOTE_PATH`, with optional `--ssh-user`, `--ssh-port`
and `--ssh-identity`. Authentication is noninteractive public-key/system SSH
configuration, not disabled verification. Loopback OpenSSH acceptance is not
distinct-host filesystem proof; cross-build is not native pipe/ConPTY acceptance.
Both terminal read and write failures join the owned SSH process/stderr capture
before formatting their bounded diagnostic, so a negotiation broken pipe cannot
race away the actionable host-key error.

Unsolicited workspace snapshots exclude credentials. Explicit settings reads
redact account/connection secrets and return presence/precondition data.
Secret updates are write-only with explicit keep/replace/clear semantics; no raw
API keys, OAuth tokens, proxy passwords, credential headers or secret-bearing
extra bodies belong in read models or documentation examples. Client-local
desktop/clipboard effects require explicit interaction, not a core-side action
on every connected desktop.

Terminal create acknowledgements mount their exact returned ID/title on Update
without waiting for the independently delivered inventory. Switching from Notices
to Terminals must replace the painted panel regardless of receipt/inventory order.
Saved and newly published Notices reach every shell; stale same-core snapshots
cannot erase newer notices, and reconnect resets the status revision domain.

## Invocation selection and acceptance limits

`--account`, `--model`, `--provider`, `--key` and `--system` enter typed
`session.create` / `session.open` as an immutable write-only invocation selection.
Every explicit selection is refused for an already-live session rather than
silently changing its runtime. `--system` adds lower-priority profile instructions,
not a replacement for canonical policy. Key overrides use ephemeral accounts;
they never update application configuration or appear in snapshots/receipts.
Prefer configured Accounts over command-line keys, which can leak through shell
history/process arguments even though the core read models are redacted.

Small queued `SaveAppSettings` / `SaveWorkspaceSettings` calls return local
admission, not durable completion; the full preference-save command awaits its
core result. Capability previews retain exact approval/preimage authority;
a zero-action preview result cannot publish application defaults.

Large response JSON uses connection-owned `payload.read` / `payload.release`
chunks with digest verification. Tokens expire or are released on disconnect;
they confer no cross-connection authority. Durable receipts contain the original
response, not an ephemeral token. This is response transfer only: an incoming
9 MiB mutation fails before enqueue under the 8 MiB frame bound. There is no
chunked mutation-upload path or claim that every 64 MiB edit can be submitted.

See the [review workflow](../guides/core-shell-workflows.md) for exact commands,
source owners and evidence limits. Ten real CLI shells, peer decision/Stop,
zero-shell server completion and loopback SSH have supplied checkpoint evidence.
The final two-shell editor harness passed all six journeys, including shared
unsaved edits, author undo/save, accepted origin-kill survival, zero-client core
restart restore and visible external-disk conflict with safe Esc. Parent read the
result JSON and cleanup evidence. Final full tests passed 94 packages; build,
tagged remote vet, complete core/client/app/remote races, focused UI/private-draft
races, zero-issue lint, docs/design and benchmark gates pass. The sixteen-MiB
Runtime/ServeConnection snapshot and durable receipt journey passes with race
instrumentation and a separate 60-second deadline for each request, not a shared
clock consumed by previous test decoding. Incoming JSON retains strict key,
syntax, depth and UTF-8 validation; output encodes once for bounded queue accounting.
Final ten-shell and genuine loopback SSH reruns pass. Native Windows and
distinct-host SSH remain unverified; cross-builds and loopback are not substitutes.
Resource sampling does not prove a smaller footprint.

## Semantic control and capability host channels

The implemented [control contract](control-contract.md) shares the command catalog
between native `crude_commands`/`crude_control` and `crude --mcp`. Core/client typed
control describe/resolve/execute, shell registration/intent/results and immutable
external-client registration preserve original shell epoch and caller/run. Four
external tools expose discovery, invocation, informational shells and receipts;
startup effects and optional exact shell binding cannot be widened in-session.
Domain and shell mutations have caller-owned receipts, observed via
`operation.receipt`/`request.receipt`, never replayed or retargeted after ambiguity.
Bounded 1–128 semantic inspection pages are read-only; the retained 256 shell
mutation receipt cap does not consume a read-call budget.

Capability host dispatch separates core-host stdio from an explicitly selected
shell worker. Shell host requires its exact attached epoch, stdio, an absolute
shell-local directory and no forwarded environment/header credential values.
Secret references resolve at the selected host. No shell chooser is inferred from
focus or SSH topology. Managed [execution contexts](managed-executions.md) preserve
logical workspace ownership while native paths, Git and MCP roots resolve to the
selected checkout. Native Windows/SSH host-locality and full UI acceptance remain
separate unverified gates; transport implementation is not universal automation.
