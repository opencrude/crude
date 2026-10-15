# Crude Semantic Control Contract

> Status: Current runtime reference — native tools, retained core routing and external MCP are implemented; complete UI-family and platform acceptance remain open.

## One API and its owners

`types/control` owns neutral descriptors, targets, requests, inspection nodes and
results. `types/control/catalog.go` is the command catalog; core owners bind exact
runtime DTO schemas. `core/` authenticates/adopts callers, resolves actual effects,
serializes admission and journals mutations. Domain services own effects;
`coreclient`, `shellport/remote` and the registered TUI Host route shell intents
through Update, never Paint. `cranemcp` adapts this same API, not a second control
engine. Native agents discover `crude_commands`, then invoke `crude_control` using
the returned command ID and exact arguments. Discovery grants no authority.

The catalog includes settings, sessions, managed executions, shared documents,
files, Git, Local History, terminals and shell tree/panes/tabs/editor/panels.
A descriptor's presence does not prove every product component supports every
verb. Inspect live availability and exact schemas; unsupported controls fail
explicitly. [Managed executions](managed-executions.md) describes isolation and
integration. Existing [core protocol](core-client-protocol.md) ownership remains
in force.

Discovery matches by words: commands containing every word of the query are
returned (words in the command ID rank first); when none contains them all,
commands are ranked by how many they contain. Simple plurals match.

MCP connections and their secrets are agent-manageable for the user through
`capabilities.inspect` (read), `capabilities.set_secret` (write),
`capabilities.remove_secret` (delete), `capabilities.set_enabled` and
`capabilities.set_tool_enabled` (write), `capabilities.save_connection`
(execute, since it can define a local launch) and
`capabilities.remove_connection` (delete). They run through the same owner as
Settings, follow the session's Manual/Auto Review/YOLO policy, never return
secret values, and mask secret, authorization, header and environment values
in approval prompts. Enabling an external server's tool for the first time
still uses `mcp_request_access`.

## Exact identity and admission

Targets retain logical workspace, optional execution/session/resource identities
and, for shell operations, exact client ID plus positive attachment epoch. Agent
callers retain their current agent/run; external callers retain their admitted
client. Caller fields are attribution, not authentication or human approval.
The core checks current attachment/run and refuses foreign sessions, stale epochs,
resources and revisions. Domain operations may run headless; shell operations
never select whichever shell or tab happens to be focused.

Preconditions retain revision (including explicit zero) and positive open epoch.
Owners recheck at effect time, not merely discovery. Component identities carry
lifetime/edit generations; close/reopen and edit/undo ABA cannot reuse a prior
inspection. Modal/deck policy applies to semantic actions as to pointer input.
Generic `shell.control` first resolves `shell.describe_control` and requires the
exact `expected_action`; actual effect and human-only metadata are re-resolved
before execution. Generic controls or key injection cannot authorize consent,
permission, secrets or privilege changes.

Memory, Board and the capabilities modal resolve actions against their
live owners. Overflow choices carry their actual selected action's effect:
inspection/refresh/fetch is read, local navigation/curation is write, and an exact
retirement confirmation is delete. Clipboard export is explicitly unavailable
through this generic path without its own host-effect receipt. An admitted read
does not require a mutation approval token on a second resolution.
Capability registration toggles retain human-owned settings authority through
generic controls; opening, searching and selecting never grants activation,
server launch or tool access.

Effects are read/write/delete/execute/communication/authority, never implicit
read. Manual/Auto Review/YOLO authorize eligible exact operations within explicit
denials and owner ceilings. Human-only authority is not reviewer-eligible.
`crude_control` runs on the agent's serial permission lane, so a Manual-mode
mutation reaches a human prompt instead of resolving as a denial. The prompt
and reviewer risk use the resolved effect (write, delete, execute) and show the
exact arguments as readable `path: value` lines. Chat rows project
`crude_commands` as command/effect/description entries and `crude_control`
receipts by state, fault, identities and output, never a raw JSON envelope.
Settings Save considers **all dirty drafts**, including off-page editors; an
ordinary page does not sanitize an authority-bearing pending draft. Field-level
settings paths, schema and revisions are discoverable; account entities and
security authority use their human-owned paths, and MCP connections and secrets
use the `capabilities.*` commands. Questions retain
agent versus external-client answer provenance, never fabricated human input.
See [settings layers](settings.md).

## Receipts, uncertainty and shell presentation

Results distinguish admitted, committed, applied, pending_decision, partial,
failed and uncertain, with request/operation/resource identities and faults.
Domain commitment and shell mounting are separate: a terminal may exist while
its exact originating shell detaches. Such partial/uncertain evidence is not a
successful visible mount. `applied` acknowledges the shell owner, not proof that
physical terminal pixels were displayed.

Both domain and shell mutations use the original caller's durable receipt.
`operation.receipt` observes its original `request_id`; transport clients also
expose `request.receipt`. Do not replay an unknown effect or retarget it after
reconnect. Changed payload under an existing request identity fails closed.
Cancellation after admission is not proof of no effect. Read-only inspection
releases transient channel accounting; the **256 retained shell mutation receipt
cap is not a read-call budget**. Current outstanding/queued-byte limits still
apply. Pending/partial/uncertain receipts require explicit reconciliation.

Original mutation receipt observation precedes new contextual resolution, so an
acknowledged Save/Close remains recoverable after its component unmounts. The
same ID with different input fails; recovering a receipt never creates another
window or repeats a domain effect. Async Memory mutations and Board read markers
retain known committed evidence as partial if their original panel cannot present
it. Entity Apply validation failure must retain the draft, not report applied.

## External MCP startup

`crude --mcp [project]` exposes newline-delimited stdio MCP through the normal
private core connection (or strict SSH bridge), without starting an interactive
shell or modifying its console. `--mcp-effects` is an immutable explicit startup
effect list, default `read`, drawn from read/write/delete/execute/communication.
Authority cannot be granted. `--mcp-shell` and positive `--mcp-shell-epoch` must
be supplied together to bind a shell; a headless launch has no shell authority.
Launch scope cannot be widened by later tool calls.

The four external tools are `crude_commands`, `crude_control`, `crude_shells`
(informational, at most 128 identities; cannot change launch binding) and
`crude_receipt` (original request receipt, not re-execution). Native and external
controls share catalog, owner fences and actual-effect authorization. This is
Crude semantic control, not a bundled desktop driver. External computer-use is
provided only by a user-selected MCP integration and its real OS/vendor permissions.

## Value and inspection bounds

| Boundary | Limit |
|---|---|
| Identity/cursor | 65,536 UTF-8 bytes; no controls; nonblank when required |
| Input/output schema | 64 KiB each |
| Individual input/output/value JSON | 64 KiB |
| Requested inspection page | Explicit 1–128 records |
| Serialized inspection page | 64 KiB, including qualified identities/cursor |
| Actions per node/resource IDs per result | At most 128 |
| Retained shell mutation receipts | 256; read inspections do not accumulate here |

Inspection nodes are flat, owner-redacted semantic records with role, accessible
name, lifetime/revision, state, available actions and optional terminal-cell bounds.
They are not screenshots or arbitrary reflected Go objects. Cursors bind current
inventory/lifetime. Clones detach nested values. Per-value limits do not imply an
aggregate frame, memory, latency or cost guarantee.

Byte-limited pages retain the exact first-unreturned Host cursor instead of
truncating a fully exported collection. A mounted control is addressed directly,
even when more than 4,096 offscreen collection rows precede it. Source hashing
is still proportional to the observed inventory; ordinary unobserved paint/ticks
do not build that inventory. Human unmount/replacement still invalidates an
already observed lifetime.

## Evidence and limits

`types/control`, `core/control_*_test.go`, `coreclient/control_test.go`,
`shellport/remote/control_test.go`, `cranemcp`, `app/mcp_control_test.go` and
`tui/ui/control*_test.go` cover contracts, real transport/admission, receipts,
component generations and native ownership. TUI journey tests cover selected
shells/decks/forms; they are not exhaustive UI-family acceptance. A real framed
control fixture has sequential Linux 160×40 and 80×24 captures for tree hide,
split editors, one actual retained PTY and persisted workspace Tab size=8;
independent test-success files and exit-zero evidence accompany inspected images.
Native Windows 160×40 and 80×24 captures now cover this same framed fixture,
including explicit terminal close, verified unmount and persisted settings, with
independent success markers. This is selected-flow acceptance, not every UI family.
macOS arm64 remains cross-compilation rather than native interaction acceptance.
Remaining family work, final repository gates, native-platform and
SSH/distinct-host acceptance must be reported separately. Neither runnable APIs
nor these test names establish universal automation or completion of the active
[implementation plan](../plans/agentic-control-and-capabilities.md).
