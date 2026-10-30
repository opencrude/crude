# Core client protocol

> Status: Current — protocol v2 carriers, retained sessions, typed commands and pushed replicas.

Normal `crude` startup uses `app.ConnectCore` and `shellport/remote.Attach`.
The per-user core owns runtime workspaces/sessions, configuration/accounts,
catalogs and prompt history; independently rendered shells detach without stopping
peer execution. `--core [--server] [--core-grace d]`, `--core-status`,
`--core-stop [--force]` and `--core-stdio` are implemented. There is no direct
in-process interactive CLI fallback. Connected startup is not universal resource migration or proof of uninterrupted
work after a core crash. Validation and release evidence live in the active plans,
not in a wire-version compatibility promise.

Same-build startup publishes one immutable staged executable under the data
directory using atomic no-replace hard-link publication. Concurrent losers never
replace the inode another shell is executing. Unsupported hard-link filesystems
and invalid existing image type/size/mode or digest return explicit staging failures.
All staged images are currently retained: Unix unlink success cannot prove that a
core, helper or concurrent starter no longer needs an image. Disk use can therefore
grow across builds; there is no automatic liveness-based pruning.

## Startup before workspace attachment

The connected core can serve startup without a workspace attachment.
The application owns profile metadata and saves through the existing settings owner.
`tui/startup/` owns the detached introduction and project-selection screen.
The current project layout, settings, and services remain unchanged.

`projectpath/` resolves the candidate on the core host before the workspace factory runs.
OS-known home and personal folders, filesystem roots, and volume roots are navigation locations only.
The check uses directory identity, not a folder name or project-tree scan.
Children of these locations, empty folders, and non-Git folders remain valid projects.
Optional personal-folder configuration failures produce a warning, not a block on unrelated projects.

Every attachment uses this admission check, including explicit paths, control clients, resume, and SSH.
A refused attachment returns `project_folder_required` with an instruction to select a project folder.
Interactive startup keeps path entry, breadcrumbs, previous projects, and bounded folder browsing available.
An unavailable previous-project list does not block folder selection.
Startup rechecks the selected path before attachment.

A navigation location creates no workspace actor or database, Local History, LSP manager, project watcher, search catalog, Git work, session, or terminal.
It also starts no memory job or workspace capability initialization.
Saved top-level sessions and history remain unchanged and do not resume automatically.
Selecting another project cancels the original startup resume intent.
Previously attached work and peer shells remain unchanged.

| Operation | Contract |
|---|---|
| `startup.inspect` | Return path classification, introduction eligibility, theme, profile choices, issues, and relevant previous-workspace metadata. No workspace creation. |
| `startup.browse` | Read directory-only pages from a requested path. Offsets count raw entries, including files. |
| `startup.catalog` | Discover directories for an explicit, non-empty picker query. Request-owned work reads no file contents. |
| `startup.create_folder` | Create exactly one named child of an existing browsed directory on the core host, without attaching a workspace. The opened parent anchors the operation; names cannot escape it, existing entries are never overwritten, and the request has a durable receipt. An uncertain outcome is not replayed. |
| `startup.save` | Save only the two application-owned onboarding choices through sparse field edits. |
| `startup.onboarding` | Read committed application settings and redacted accounts from the selected core. |

| Startup boundary | Limit or policy |
|---|---|
| Directory read batch | 128 raw entries |
| Browse page | 512 returned directories |
| Browse scan | 100,000 raw entries per request, including skipped offsets |
| Search catalog | 10,000 visited entries and 2 MiB of accounted path storage |
| Detached picker retention | 4,096 directory entries across retained browsing rows |
| Detached UI request | 20-second deadline with cancellation and owner checks |

Browse offsets are best-effort cursors, not filesystem snapshots.
Limit results show an explicit notice and preserve more-specific path navigation.
Directory symlinks remain browsable, but catalog discovery does not follow them.
The catalog skips dependency, build, Git, and Crude data directories.
Shutdown cancels and joins started startup requests before attachment.
The same folder-creation operation is available from an attached shell for
workspace destinations outside its current project. Successful creation browses
the new directory; it does not attach or bypass workspace-switch confirmation.
These bounds do not establish native Windows or distinct-host SSH acceptance.

See [settings](settings.md#first-use-profile-choices-and-ai-setup) for introduction and AI setup behavior.
See [shared folder-picker movement](interaction-matrix.md#shared-folder-picker-movement) for input behavior.

## Ownership and attachment

`core.Runtime` accepts an application-owned workspace factory and private SQLite
receipt journal. Concurrent creation converges on one actor per canonical project.
Actors admit commands and agent events, immediately reduce accepted text and
workspace state, and publish revisioned changes. Presentation pacing never holds
accepted tokens in a second 100 ms mutation buffer. Resource I/O is prepared or
performed off the actor where required and completed under owner fences.
Workspace publication arms a 16 ms one-shot only when accepted facts become
pending, coalesces the burst, and disarms after publishing. Retained idle actors
do not run a periodic publication clock; accepted mutations and state-topic
source-generation fences remain immediate and independent of presentation.

Before publishing a retained actor, its factory worker restores saved Notices
and initializes configured capability metadata. Neither requires a model session
or a shell to open Settings. Initialization never connects MCP, downloads skills
or replays tools; failures retain a usable shell and an explicit workspace notice.

A retained session has an immutable client ID and attachment epoch. Every request is
bound to the negotiated owner; selecting another tab is not a change of owner.
`workspace.attach` installs a subscription and captures its snapshot at the actor
boundary. Explicit detach/session close removes subscriptions and document/terminal
attachment authority, not the retained workspace or its model sessions. Carrier
loss alone retains attachment authority during the resumable session grace period. Explicit runtime
close owns final shutdown. Connected workspace switches prepare the destination before
teardown of client-local resources, then replace only that shell attachment. A
failed destination leaves the old attachment usable; peers keep the old workspace.

Saved workspace deletion is an explicit core-host operation, never a client-side
directory move. `workspace.delete_preview` releases a retained workspace only
after its actor proves there are no attached views, in-flight operations, active
agents/commands/maintenance, or live terminals. Session admission is fenced before
retirement; the full watcher, resource-lane, session, collaboration, history and
database shutdown joins off the actor. Immutable directory review follows those
flushes, not before. Cancelling the dialog leaves saved data intact; a later
attachment constructs a fresh actor.

Attachment preparation, including factories which outlive a cancelled request,
and delete/Undo share the saved-storage identity boundary. Catalog metadata
readers retain a read lease until their actual SQL handles close, including
timeout unwinding; directory review/move excludes them. Preview tokens are exact
connection-owned, single-use, ten-minute reviews; commit rechecks use and directory
preimages. `workspace.delete_saved` moves the complete saved directory to recovery
without touching project files. `workspace.restore_deleted` never closes or
overwrites a replacement workspace. Both mutations have durable receipts; lost
acknowledgements expose **Check result**, which observes the original receipt
without repeating an effect. Blocked dialogs explain the owner/activity and next
action, offer **Review again**, and default to Cancel/Close. Other processes can
still hold files open; genuine OS failures preserve saved/recovery data and remain
explicit rather than triggering retries or stopping unrelated work.
Preimages exclude only regular SQLite derived SHM and empty WAL sidecars after
checking the database header. Nonempty WALs, journals and accepted later writes
remain protected preconditions; ordinary catalog reads do not invalidate consent.

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
RPC, persistence or semantic event reduction. Acknowledged push subscriptions
deliver detached batches; there is no invalidation-then-fetch worker in normal
replication. `StartReplica` releases publication only after the shell sink is
ready. Update preflights and atomically installs each batch, then releases its ACK
and presents neutral effects. Pane renderers use the
canonical replica Conversation. Create/open are off-UI; pending root activation
waits for replica inventory and peer-created roots preserve editor focus.

## Ownership across core and shell

### Operational inspection

`core.performance.inspect` and `core.diagnostics.inspect` serve bounded immutable
pages before workspace attachment without constructing a workspace. They reuse
the process metrics registry and retained runtime/connection/topic owners.
Semantic agent/MCP callers use the existing control gateway's Read descriptors;
core-owned diagnostic reads do not wait behind the workspace actor they inspect.
Their authorization still rechecks the exact native run/root session or immutable
external launch grant. Current-workspace capacities are read on their actor with
bounded waits and explicit partial evidence. See [diagnostic retrieval](control-contract.md#on-demand-diagnostics).

`core.status` preserves its successful per-workspace observations alongside
`partial` and per-workspace `observation_error` facts. Internal Status callers
still receive the inspection error; unknown workspace work is busy for lifetime
decisions. Shutdown reservation remains fail-closed and separate from diagnostic
partial success. Topic diagnostics copy metadata at existing send/applied-ACK,
reset and publication boundaries, never read live encoder state or own another
cursor. Normal replication is unchanged.

### Presentation ownership

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
  remote paths stay in frames. Transport choice does not establish native-platform, distinct-host or
  resource-efficiency acceptance.
- Retained core creation hydrates Notices and initializes capability metadata on
  its factory worker before actor publication, never from shell startup or a
  model session. Passive capability observations bypass long settings operations;
  editable settings reads remain ordered after preceding saves. Explicit refresh sequence plus
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
  Stable document IDs survive durable file/directory relocation. Core file watchers publish ordered file facts; per-open document topics push
  canonical revision operations/savepoints. Explicit shell reads/transfers use
  typed commands or bulk streams.
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

Protocol v2 uses generated MessagePack tuple codecs from the Go types, not JSON
frame envelopes or a second handwritten schema. A uvarint byte length bounds each
application message; a role/header value is followed directly by its typed value.
Generated decoders bound collection counts against available bytes before
allocation, validate tuple shapes, and reject malformed or trailing data. JSON
required by an external contract (for example an LSP WorkspaceEdit or arbitrary
JSON document) remains a typed leaf, not the transport envelope.
`Hello.Schema` must exactly equal `coreprotocol.Schema`, and `Hello.Build` must
exactly equal `buildinfo.ProtocolBuild`. Incompatible peers are refused before
application traffic, not adapted.

The build cohort is a full 64-hex SHA-256 of platform-independent production
source, not the native image ID. `internal/wiregen/` includes all platform/tag
alternatives, checked-in local forks, embedded assets, prompt templates, module
manifests, generated codecs and native dependency pins; it does not hash local
native archives or executable bytes. Supported builds use the canonical cgo/native
profile. `.gitattributes` normalizes known text formats to LF and preserves
unlisted files byte-for-byte, including arbitrary embedded assets. This keeps
checked-out cohort inputs stable across hosts without treating binary data as
text. This establishes source equality, not user authentication or proof that an
arbitrary binary was built correctly.

Regenerate explicitly with `go run ./internal/wiregen` after production-source
changes. `go run ./internal/wiregen -check` verifies the cohort without writing or
regenerating anything. Make build/test/benchmark targets, `scripts/build.sh` and
`scripts/release.py` invoke that check; arbitrary direct `go build` does not run
generators or establish freshness. `app.BuildID` / `buildinfo.ExecutableID` identify
the exact local executable for staging and status only. Native images from the
same source cohort may differ and are admitted without replacing their core.

`corewire.Conn` supplies ordered, flow-controlled streams over the local
socket/named-pipe mux, strict SSH stdio, TLS 1.3 TCP mux or QUIC. The pinned
quic-go fork and byte-stream mux both schedule control/input/interactive/bulk
classes with bounded weighted service **8:4:2:1**. This is local scheduling, not a
peer-negotiated priority protocol or a guarantee against physical network delay.
Every network carrier uses the same per-direction compression wrapper: control,
input and a first write below 256 bytes select identity; eligible directions keep
one standard zstd frame/history through FIN, with synchronous flushes and bounded
records. Streams never share compression history across trust boundaries. Local
IPC is uncompressed. There is no timer-delayed compression batch.

| Boundary | Limit/policy |
|---|---|
| Application message | 64 MiB maximum; narrower role-specific reader bounds apply |
| Byte-stream mux data frame | 16 KiB maximum |
| Mux stream receive credit | 256 KiB per stream; 256 admission slots per direction |
| Actor mailbox | 128 commands, 1024 agent events, 256 service events |
| Retained command results | 16 unacknowledged commands; 128 MiB aggregate request preimages; capacity refuses new commands without evicting unknown results |
| Ordered topic journal | 256 entries / 8 MiB, with explicit snapshot reset on an evicted gap |
| Bulk chunk | 256 KiB, ordered offsets and whole-transfer digest verification; two-minute transfer lifetime |
| Private clipboard inbox | 16 outstanding offers / 1 MiB accounted bytes; explicit local refusal on overflow |
| Terminal history read | 512 rows per page; bounded engine grid/history |
| Retained session grace | 15 minutes by default after carrier loss |

The first control stream exchanges Hello/Welcome. The independent session secret
resumes the same owner over a replacement carrier; lease and topic cursors survive
within grace. Clean session close ends it; carrier failure does not. Resume
reattaches to exact command preimages and applied topic cursors, never replays an
effect under a new identity. Heartbeats detect failed carriers, not slow replica
application. Credit and bounded journals prevent unbounded backlog; a slow viewer
is paced or reset, not disconnected merely for reading slowly.

Clean close sends control FIN, releases the core's topic/lease/document attachment
authority, then returns `SessionEnd` as the cleanup receipt. The client consumes
that receipt before closing its carrier; a queued FIN or successful write alone
does not prove cleanup. Live close drains for at most one second and reports a
missing receipt or deadline explicitly. Closing an already-lost carrier only
joins local workers; the core retains that session through its grace period.
The server joins its control reader and drains different clients concurrently,
so a hostile peer cannot multiply the shutdown timeout by client count.

## Push publication and application ACKs

Domain replication uses only the acknowledged workspace log, seven state topics,
and per-open document/terminal topics. There is no invalidation `Events`/`Change`
queue or command-progress event broadcast. The workspace topic is an ordered
semantic/inventory/conversation log. Status, settings, LSP, registry, terminal
inventory, Git and Board each have independent
latest-state slots and joined derivation workers. Slow Git or settings work cannot
hold status or input behind it. Each paced topic sends at most the latest state
at `max(display interval, min(RTT / 2, 250 ms))`, after the prior applied ACK; a
change after quiet is sent at once (terminal grids wait mosh's 8 ms
`SEND_MINDELAY`) and idle topics arm no timer. Conflation affects observation
only, never canonical mutation.

Workspace transcript publications compare semantic agent metadata independently
of conversation/token and Status Center observation clocks. Token suffixes carry
their own `LastTokenTime`; an unchanged status with a newer `UpdatedAt` does not
force another complete agent tuple. The independent Status topic retains the
actual observation timestamp. A real-carrier 20 KiB reply regression counts all
mux/application bytes across forty separately applied suffix publications rather
than using a re-encoded conversation-size proxy.

An initial or reset state subscription captures the actor's requested derivation
generation and waits for that source boundary to finish before sending its first
snapshot. Completion wakes waiters even when byte deduplication keeps the topic
sequence unchanged. Post-save Settings therefore cannot report an old projection
as refreshed merely because the new worker is still running; application ACK is
a separate, later boundary.

The shell owns the display clock. `perf.LoopObserver` measures actual nonempty
renderer writer duration, clamps it to 16–250 ms, and publishes it atomically.
Update declares that interval through `shellport.DisplayPacer`; the SDK carries
it on topic acknowledgements and terminal viewport declarations. Idle gaps are
not display cadence. This pacing is a contract, not evidence of 30 fps on an
outer emulator.

`ApplyReplicaChanges` runs on Update. It checks owner/client/topic generation,
sequence/reset boundary, the complete candidate inventory and every transcript
before committing any retained objects. Surviving sessions, nodes and Conversations
keep identity. Only a successful commit advances the applied cursor, emits neutral
effects and releases the subscription's `TopicAck`. Invalid publication leaves the
old projection intact and requests a bounded snapshot reset; a repeated malformed
or rejected publication surfaces failure rather than an infinite resync loop.

`SessionEvent` carries only private shell-control requests, capability-host
requests and controller-local terminal clipboard offers, never domain state or
command progress. The session topic is a one-event stop-and-wait journal. A successful carrier write is not delivery: the
ACK follows admission to the exact owner's inbox, not arbitrary callback return
or completion of the requested operation. The retained event crosses carrier
replacement; no second event bypasses an unacknowledged one. Clipboard inboxes
are capped at 16 offers / 1 MiB. A full shell inbox produces a visible, coalesced
local refusal rather than waiting for presentation and starving owner requests.
Offers are fenced by exact workspace, client/attachment and terminal lease
generation, never broadcast or automatically written to a system clipboard.

## Typed methods

The exact request/result definitions live in `coreprotocol/*.go`; dispatch is
closed and typed, not arbitrary method invocation. Current families are:

| Family | Operations |
|---|---|
| Detached startup | `startup.inspect`, `.browse`, `.catalog`, `.save`, `.onboarding` |
| Workspace | `workspace.attach`, `.detach`, `.snapshot`, `.delete_preview`, `.delete_saved`, `.restore_deleted`; `agent.changes` |
| Session/agent | `session.create`, `.open`, `.list`, `.snapshots`; `agent.submit`, `.steer`, `.decide`, `.cancel`, `.terminate`, `.yolo`, `.plan_mode`, `.goal`, `.loop`, `.recover`, `.recover_cancel`, `.recover_reject`, `.stop_command`, `.compact`, `.append_messages` |
| Receipts | `request.receipt`, `request.cancel` (exact in-flight job) |
| LSP | `lsp.snapshot`, `.notify_watched_files_changed`, `.set_active_file`, `.start_for_workspace`, `.stop`, `.restart_server`, `.stop_server`, `.references`, `.completions`, `.definition`, `.document_symbols`, `.hover`, `.rename` |
| Status | `status.snapshot`, `status.mutate` (notifications/issues/operations); shell transients stay local |
| Documents | `document.open`, `.read`, `.close`, `.edit`, `.undo`, `.redo`, `.save`, `.format`; per-open document topic; `.sync` only for explicit recovery/shutdown; separate `.snapshot_save` checked disk commit |
| Bulk transfer | `StreamBulk` method-specific metadata, ordered chunks, final digest; no connection-private response tokens |
| Terminals | `terminal.list`, `.create`, `.rows`, `.control`, `.release`, `.close`; terminal grid topics and ordered lease input streams; explicit typed input/resize commands remain available |
| Files | browse/catalog, chunked reads, create/folder/rename/delete/edit/search, canonical `files.replace_plan` and saved workspace listing |
| Local History | timeline/file/stats, operation/path/message/agent plans, apply/verify/prune/capture |
| References | create/import/list, content/media reads, exact content save, rename/delete |
| Prompt history | `history.list`, `.record`, `.restore`, `.import-attachment`, `.read-media` |
| Git | snapshot/comparison/annotation, reviewed stage/unstage and hunks, staged commit, commit inspection, checkout and reviewed discard |
| Memory/Board | search/episodes/inspect, revision-fenced pin/forget/correct; Board snapshot/read acknowledgement |
| Settings | redacted fetch, session read/save/reset/delegation toggle, account change/models, application preference save; `workspace.overrides`, `.overrides_save`, `.startup`, `.tool_layout`, `.tool_layout_save` |
| Capabilities | runtime/snapshot/action, acquisition preview and exact skill-input preparation |
| Remote host administration | `remote.access.read`, `remote.pairing.create`, `remote.device.revoke` (authenticated local host only); `remote.ssh.bootstrap` (authenticated SSH authority only) |

Connected adapters use these families; method existence is not proof that the real settings form, composer, Git review,
history preview or capability approval journey uses it. Existing domain rules
remain authoritative, including Local History safety previews and application vs
workspace capability registration; the transport cannot widen a grant.

## Mutation receipts and uncertainty

One command stream carries one request, progress notes in typed `Reply` values
and a final typed result. Progress never enters a session or domain event queue.
The core retains the exact admitted preimage/result across carrier loss until the
client sends `TopicAck{Seq: 1}` after decoding the final result (including a known
fault). A failed final ACK or ACK-close cannot turn that known outcome into
uncertainty. Unknown outcomes still require exact receipt observation. Reusing an
identity with changed method, workspace or payload fails closed.

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
pending message flags survive conversation reset/delta tuple projection.

Connected preference saves carry exact path/value edits, not resolved form
snapshots or expected configuration revisions. Last explicit write wins;
untouched fields keep inheriting. Session saves likewise carry only edits to
their explicit local layer, never a second whole-settings wire writer. Account
and capability changes remain identity-scoped and preserve write-only secret
Keep/Replace/Clear. They do not have whole-settings conflict gates. Existing
settings jobs serialize persistence, then the actor alone publishes session/LSP
projections. Observation revisions prevent out-of-order replica installation;
they never reject an ordinary configuration write. Immutable install/grant
reviews and actual attachment/session lifetimes retain their own safeguards.

Passive `settings.capability.runtime` and `.snapshot` reads bypass the serialized
settings lane so a pending Connect/Sign in cannot starve home polling. Editable
settings reads and writes remain ordered after prior publication. Each shell's
explicit capability refresh has its own sequence as well as workspace generation;
cancelled or superseded results cannot replace newer success or cancel a newer poll.

## Shared resources and private state

`workspace.DocumentService` is the universal connected editor text/revision,
savepoint, author-specific undo and LSP authority. `shellport.LiveDocuments`
provides Open/Edit/Close/Save and optional Format/receipt/topic interfaces; Sync
is explicit recovery/shutdown infrastructure, not a normal post-command poll.
`DocumentOwner` includes the attachment epoch. The Update coordinator installs
immutable snapshots/deltas, retains optimistic edits and orders off-UI commands.
Edits, undo, formatting and saves fence exact revisions; a lost response is
observed through its original receipt, not resent as a new mutation. Each open
subscribes to a document topic: a snapshot boundary, then ordered revision
operations and savepoint metadata. The shell prepares a complete rebase of
canonical and optimistic private state before committing it atomically; receipt,
publication and draft identities remain distinct. Warm session resume retains
applied cursors; a cold reset with private pending work enters conflict/recovery,
not silent text replacement. Close/reopen, conflict resolution, workspace switch
and shutdown fence queued publications with exact topic-generation tokens.
Cursors, selection and scrolling stay private.

The independent `insight` document topic pushes bounded
`types.DocumentInsight` snapshots derived from those canonical revisions.
Its service generation and version order analysis, not text or author history.
An insight ACK cannot delay document admission, text-topic ACKs or peer analysis.
Close/reopen and workspace/attachment fences use the same exact-open token.
The shell waits for matching source before projecting future analysis; pending,
limited, failed and unsupported results never become zero-error claims. See
[native syntax](native-syntax.md) for the initial producer and remaining limits.

Document GroupID identifies a logical editing transaction, not an RPC batch.
The shell retains group boundaries while publishing ordered optimistic edits;
Undo/Redo gestures and later input wait for exact command completion and matching canonical
topic publication. History deltas preserve the reversed group ID. Core operations retain
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
Desktop effects use original paths only with immutable authenticated same-host
admission provenance. Foreign-host effects require bounded client-local copies or
a containing directory preview, never a remote path guessed to be local.

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
PTY output and owns all child query replies. Readers receive packed changed-row grid publications and explicit on-demand
history pages, not bytes to parse again. Exact client/epoch/generation leases fence input and
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
Opening one history attachment uses `history.import-attachment` with the exact
immutable entry ID and attachment index. The core resolves its retained snapshot
and imports only that item into the addressed session; neither a destination-session
reference lookup nor a client-provided source path may stand in for history.

## Trust, secrets and platform limits

Local transport is same-user authority, not multi-tenant isolation. Unix uses a
0700 user-owned socket directory and a 0600 socket. It binds a random unpublished
name no longer than `core.sock`, validates identity and private mode, then
atomically hard-links the canonical endpoint without replacement. Dialers see
absence or an already-private socket, never the bind-before-chmod interval.
Unsupported socket hard-link filesystems fail explicitly; no permission relaxation,
process-wide umask or replacement rename is used. Cleanup removes only exact owned
inodes, and listener Addr remains canonical. Windows
uses a user-SID/data-directory named pipe with a protected DACL granting that
user and denying network logons. Neither mechanism restricts an already trusted
same-user client to harmless reads.

`app/core_handover.go` and its Unix/Windows path-security implementations own a
private corewire/owner.json record: random owner generation, exact staged helper path
and full image digest, schema and source cohort. An exclusive `lifetime.lock` is
held through runtime shutdown, including the listener-close/data-flush interval.
Autostart requires positive endpoint absence and exclusive lifetime proof before
reclaiming a crash-left record; failed admission, permission errors and incomplete
probes never mean absence.

Explicit `--core-stop` uses a frozen local lifecycle exchange before Hello, not
the generated application schema or source cohort. On the first control stream,
one bounded message is `CRUDE-STOP\0`, a byte (`0` graceful / `1` force), and an
optional exact owner generation (at most 256 bytes). The immutable reply is
`CRUDE-STOP-RESULT\0`, an accepted byte (`0` refused / `1` accepted), and an
actionable reason, at most 8 KiB total. FIN drains the result. This creates no
session and carries no application objects. Authenticated network and SSH carriers
cannot use this same-user local-host channel. Ordinary Hello/schema/cohort checks
remain strict; pre-contract cores require their retained original helper, not a
compatibility decoder. A lost response is an unknown outcome, never permission to
kill or replay against a replacement owner.

For a mismatched cohort/schema, startup verifies and invokes only the retained
owner image with `--core-stop`, passing the exact generation as `ExpectedOwner`.
It does not decode foreign application traffic, force shutdown or kill the core.
Both explicit graceful stop and automatic handover refuse attached/resumable
shells, opening factories and storage operations. Idle retained actors are allowed
only under a transactional reservation: runtime admission closes first, then each
actor reserves commands/control, resource lanes, every session's existing history
admission and turn gate, autonomous child/collaboration/control work and memory
jobs, document views, terminal creation/controllers and live PTYs. Saved-storage
leases exclude catalog SQL readers and directory moves. Pending/queued input,
turn finalizers, commands, compaction and active children all refuse. No active-work
sample alone authorizes shutdown. A refusal unwinds every reservation in reverse
order; it closes/removes no actor and changes no owner record or accepted work.
The reason names the workspace/session/resource and next action. Accepted leases
stay held through resource joins; the lifetime lock stays held through final data
flush. Force is available only as an explicit user override, never handover.
Same-cohort native-image variants attach directly.

An explicitly admitted [checkpointed restart](core-restart.md) is a separate
local original-terminal path. It stages an exact image, waits for its root's
durable natural checkpoint, then releases the original shell before this same
idle-reserved stop. Only the exact unexpired Ready goal reservation can satisfy
active-goal protection. It never exempts peer work or resource joins. The
scheduled helper can retry up to eight dedicated refused/no-effect exits while
detached resource releases finish; it never retries an unknown or accepted stop,
and rechecks the same owner before every attempt. Internal status 75 is emitted
only after a decoded refusal; Go panic status 2 is never retried. Ordinary mismatch startup
retains its existing immediate-refusal behavior. Successor admission is exact
image/generation checked, not ordinary same-cohort attach, and an opaque
single-use permit authorizes only the original session's fresh continuation.

SSH transport starts system `ssh` without a local shell, enables strict host-key
checking and disables forwarding, remote/local command overrides and shared
control sessions. Fixed helper commands inspect/install/launch the exact image;
project paths remain typed payloads, never interpolated shell arguments. A missing
or mismatched image offers an explicit user-consented, expiring exact-image
installation review. It rechecks executable digest and host facts, retains old
images/data, and never kills an active older core to make installation succeed.
Unsupported platform/artifact combinations fail explicitly; there is no backwards
adapter or unverified executable download. CLI selection is
`--ssh HOST --workspace REMOTE_PATH`, with optional `--ssh-user`, `--ssh-port`
and `--ssh-identity`. SSH uses noninteractive public-key/system configuration.
Owned process/stderr shutdown preserves bounded actionable host-key diagnostics.

Direct QUIC/TLS sessions authenticate before admission, with TLS 1.3 and an exact
host certificate fingerprint. Restricted `Hello.Pair` / `Welcome.Paired`
enrollment verifies the pin before transmitting a single-use pairing code and
never creates a normal session. Host-local authenticated settings own pairing,
device revocation and listener policy. `Welcome.LocalHost` is immutable admission
provenance, not inferred from carrier or UI labels; resuming an SSH/direct session
on a local carrier cannot grant host administration. `Hello.SSHRelay` can only
narrow an already OS-authenticated local admission to SSH authority, never
authenticate a network caller. See [remote access](network.md#remote-core-access).

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
cannot erase newer notices. Warm resume preserves the retained status domain;
a replacement core admits a new revision domain explicitly.

## Invocation selection and acceptance limits

`--account`, `--model`, `--provider`, `--key` and `--system` enter typed
`session.create` / `session.open` as an immutable write-only invocation selection.
Every explicit selection is refused for an already-live session rather than
silently changing its runtime. `--system` adds lower-priority profile instructions,
not a replacement for canonical policy. Key overrides use ephemeral accounts;
they never update application configuration or appear in snapshots/receipts.
Prefer configured Accounts over command-line keys, which can leak through shell
history/process arguments even though the core read models are redacted.

All settings saves and shortcuts await their typed durable core result; the
queued application/workspace shell writers are removed. Capability previews
retain exact approval/preimage authority;
a zero-action preview result cannot publish application defaults.

Large files, reference content and media use credit-controlled bulk streams
with method metadata, bounded chunks and whole-transfer digest verification.
Current owner caps are 64 MiB file and reference/media reads, 16 MiB editable
reference uploads and document text, and 64 MiB media upload admission. These
come from `coreprotocol.MaxFileRead`, `coreprotocol.ReferenceMaxBytes`,
`coreprotocol.ReferenceUploadMaxBytes` and `document.MaxBytes`, not the message
limit. Chunks are 256 KiB with a two-minute transfer lifetime. Bulk bytes are neither JSON nor
connection-private response tokens. Durable command receipts contain typed
outcomes, not ephemeral transfer tokens.
The 64 MiB application-message bound is not permission for arbitrary-size edits;
each owner validates its own content, collection and transfer limits.

This reference describes contracts, not a release acceptance result. Native
Windows pipe/ConPTY, physically distinct-host SSH, full-process install/resume and
resource/performance gates require their own recorded evidence; cross-builds,
loopback and deterministic fault tests are not substitutes. Current remaining
acceptance belongs in the active plans and consolidated validation report.

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
