# Core client protocol

> Status: Current implementation reference for the core foundation, not a public CLI or completed multi-client product.

`core/`, `coreprotocol/`, `corewire/` and `coreclient/` implement a retained
runtime and typed client boundary. The actual application still starts one
TUI-owned Workspace through `tui.Start`, schedules its domain ingress in the
shell and shuts down that workspace when the shell exits. There are no public
server/attach/stdio CLI routes or fully connected product shells yet. Follow the
[active separation plan](../plans/core-shell-separation.md) for remaining wiring
and release acceptance; do not infer product completion from these APIs.

## Ownership and attachment

`core.Runtime` accepts an application-owned workspace factory and private SQLite
receipt journal. Concurrent creation converges on one actor per canonical project.
Actors admit commands and agent events, flush accepted text on their own ticks,
reduce workspace state and publish revision changes. Resource I/O is prepared or
performed off the actor where required and completed under owner fences.

A connection has an immutable client ID and attachment epoch. Every request is
bound to the negotiated owner; selecting another tab is not a change of owner.
`workspace.attach` installs a subscription and captures its snapshot at the actor
boundary. Detach/connection close removes subscriptions and document/terminal
attachment authority, not the retained workspace or its sessions. Explicit runtime
close owns final shutdown. This lifetime exists in the new core path only:
standalone product workspace switches still tear down the old project.

Snapshots are detached values, not service pointers, SQL handles, callbacks or
reflected Workspace calls. `coreclient.WorkspaceView` installs snapshots and
conversation deltas on the client's serialized UI thread, retaining surviving
session/node/conversation identities. Paint reads its local projection without
RPC, persistence or semantic event reduction. Installing this projection does not
by itself migrate the existing shell's direct service consumers.

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
| Session/agent | `session.create`, `.open`, `.list`; `agent.submit`, `.steer`, `.decide`, `.cancel`, `.terminate` |
| Receipts | `request.receipt` |
| Documents | `document.open`, `.read`, `.edit`, `.undo`, `.redo`, `.save` |
| Terminals | `terminal.list`, `.create`, `.rows`, `.control`, `.release`, `.input`, `.resize`, `.close` |
| Files | browse/catalog, chunked reads, create/folder/rename/delete/edit/search and saved workspace listing |
| Local History | timeline/file/stats, operation/path/message/agent plans, apply/verify/prune/capture |
| References | create/import/list, content/media reads, exact content save, rename/delete |
| Prompt history | `history.list`, `.record`, `.restore`, `.read-media` |
| Git | snapshot/comparison/annotation, reviewed stage/unstage and hunks, staged commit, commit inspection, checkout and reviewed discard |
| Memory/Board | search/episodes/inspect, revision-fenced pin/forget/correct; Board snapshot/read acknowledgement |
| Settings | redacted fetch, session read/save/reset/delegation toggle, account change/models, application preference save |
| Capabilities | runtime/snapshot/action, acquisition preview and exact skill-input preparation |

Method existence is not proof that the real settings form, composer, Git review,
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

## Shared resources and private state

`workspace.DocumentService` owns live text/revisions, savepoints, per-owner undo
and borrowed save/LSP services. Edits identify owner and expected revision rather
than a selected editor. The `tui/editor` shared-document adapter opens without a
second watcher, retains pending/in-flight edits and applies exact acknowledgements
without silently replacing conflicting work. It is a checkpoint adapter, not yet
the universal editor/save/LSP product route.

`workspace.TerminalService` installs one neutral `terminalvt` engine before first
PTY output and owns all child query replies. Readers receive detached row pages,
not bytes to parse again. Exact client/epoch/generation leases fence input and
resize. Selection, scroll, wrapping-aware copy, focus and dock/floating placement
are shell-local. OSC 52 writes are offers for explicit client copy, never automatic
broadcast system-clipboard effects. See [in-app terminals](in-app-terminals.md).

`clientstate` is a separate private composer store keyed by client profile,
workspace and session, with attachment epochs, record revision CAS and request
receipts. It preserves text, reference spans, cursor, selection and scroll; it
does not choose product profile paths or automatically convert old session drafts.
Those integrations and manual backed-up development-data alignment are pending.
Prompt history can be installed as one application-owned store before core clients
attach; connected composer/history UI wiring remains a separate acceptance gate.

## Trust, secrets and platform limits

Local transport is same-user authority, not multi-tenant isolation. Unix uses a
0700 user-owned socket directory, a 0600 socket and an exclusive owner lock; it
refuses existing/stale endpoints instead of deleting a guessed owner. Windows
uses a user-SID/data-directory named pipe with a protected DACL granting that
user and denying network logons. Neither mechanism restricts an already trusted
same-user client to harmless reads.

SSH transport starts system `ssh` without a local shell, enables strict host-key
checking and disables forwarding, remote/local command overrides and shared
control sessions. The remote command is fixed to `crude --core-stdio`; project
paths belong in frames, never interpolated shell arguments. That remote CLI route
does not yet exist in the application. Helper tests or cross-compilation do not
establish real SSH attach or native Windows pipe/ConPTY acceptance.

Unsolicited workspace snapshots exclude credentials. Explicit settings reads
redact account/connection secrets and return presence/precondition data.
Secret updates are write-only with explicit keep/replace/clear semantics; no raw
API keys, OAuth tokens, proxy passwords, credential headers or secret-bearing
extra bodies belong in read models or documentation examples. Client-local
desktop/clipboard effects require explicit interaction, not a core-side action
on every connected desktop.

The remaining daemon startup/shutdown authorization, reconnect UX, complete
connected resource journeys, secret-boundary audit and native-platform checks
must pass before release. Twenty-workspace and multiple-real-shell resource
measurements are outstanding; retained ownership alone proves neither lower
memory use nor uninterrupted work across a core crash.
