# Chat activity projection

> Status: Current — activity grouping and audit/disclosure contract.

The canonical conversation is the audit log. The chat renderer projects it into a compact, reversible activity view; it never deletes, rewrites, or persists presentation-only groups. Both live events and resumed session snapshots enter the same renderer. A group is a run of adjacent eligible calls (or completed thoughts) between visible boundaries; it may contain active calls until they finish. Its stable identity comes from its first message, not from a transient render index. Click its summary to inspect its original calls in chronological order, and click an individual eligible call to inspect its existing full output. The original result, command, input, permission tag, duration, and child identity remain available.

## Nested scroll ownership

One continuous wheel gesture keeps its starting scroll owner even as streaming
or scrolling moves nested content under a stationary pointer. Direct pane-body
scrolling remains available without a modifier or a tiny-only hit target: move
deliberately into that pane or pause for the shared 500 ms gesture boundary.
The nested gutter remains directly scrollable/draggable. When a pane reaches
its limit, only the unconsumed rows chain through its exact enclosing panes to
the main transcript; the gesture then stays with that ancestor instead of
bouncing between surfaces. Direction reversal retains ownership and resets
acceleration for precise correction. Keys remain focused-owner navigation.

`PaneLineInfo.Ancestors` preserves actual recursive rendering ancestry and each
enclosing viewport row, not a hierarchy inferred from current pointer position.
Headers retain pane identity for paint but do not own their own body scroll.
The same projected row metadata feeds live/hydrated rendering, wheel arbitration,
gutters, and bottom-up containing-renderer invalidation. Changing a conversation
source replaces its per-pane scroll-model lifetime; ordinary appends do not.
Captured selection and scrollbar drags remain Host-owned and independent of wheel
transactions. Window routing follows exact z-order hit owners without stealing
focus or crossing modal boundaries.

## Classification

| Treatment | Tools |
| --- | --- |
| Aggregate after success | `exec_command`, `write_stdin`, `execute_command`, `async_execute_command`, `check_async_command`, `read_async_output`, `read_file`, `list_directory`, `glob_files`, `grep_files`, `web_search`, `web_fetch`, `arxiv_search`, `recall_memory`, `search_history`, `wait`, `agent_list`, `agent_inspect`, `agent_wait`, `board_read`, `artifact_read` |
| Always visible | `apply_patch`, `edit_file`, `write_file`, `delete_file`, `move_file`, `copy_file`, `update_plan`, `get_plan`, `agent_start`, `agent_send`, `agent_respond`, `agent_control`, `publish_report`, `kill_async_command`, `remember`, `update_memory`, `forget_memory`, `question`, `reply` |

Unknown tools stay visible until intentionally classified. A failed or cancelled call, a child failure reported by an otherwise successful poll, and any required user action remain visible as boundaries. A background command's successful *start* is not evidence that the background job finished; label it as started, not completed. Never imply that all calls passed solely because their tool transport succeeded.

Background-command settle notifications are runtime evidence, not peer-message
cards. The journal retains typed handle, command, status, duration, exit
availability, PID and a bounded immutable stdout/stderr excerpt, even when the
agent never called an output-read tool. Their default projection is an
ordinary-tool-styled title (`Completed command build 4s`), with no output teaser;
failures/timeouts keep their failure indicator. Native disclosure exposes
retained command evidence without painting model-facing polling guidance. Live events and SQLite hydration
share that builder, normal chat background and static settled geometry; no body
text parsing supplies lifecycle facts.

All command tools (`execute_command`, background start/status/output reads,
`exec_command` and `write_stdin`) share `types/command.Result`. Process status,
exit availability, command, directory, handle/session, raw byte ranges, omissions
and paging cursors are facts, not values recovered from prose. Cancellation keeps
partial output under a neutral stopped title, distinct from timeout/failure. The tool transport
can succeed while the process is running or failed; grouping and title treatment
must use those facts. Native exec's documented output/exit/session envelope is
decoded by the same tool-layer reader.

Command bodies are literal audit logs, never Markdown or search-result parsers.
The existing x/ansi decoder strips CSI/OSC/DCS metadata before text hygiene;
CRLF is one line break and bare carriage-return progress updates become separate
lines so evidence is not concatenated or overwritten. Tabs use eight-column stops,
indentation and absolute paths remain intact, and body ink uses the theme's subdued
`ColorMuted`, never foreground-white or failure-red. Transport base64 is decoded; binary/non-UTF-8 bytes use
the standard-library hex dump instead of opaque base64 text. Empty capture,
running-with-no-bytes and status-only reads are explicit on expansion. Compact
commands show only their title, process metadata and disclosure control; disclosure
reveals every captured row plus exact multiline command/directory/PID/range/retention evidence. Full-screen
interactive programs belong in the embedded terminal; chat does not implement a
second terminal emulator. Original command-result bytes remain canonical.

## Capabilities and Peer Data

`capability_search`, `capability_activate`, `agent_send`, `board_read`,
`mcp_request_access`, `mcp_read_resource`, `mcp_get_prompt` and `mcp__<connection>__<tool>` calls retain
their exact identity/arguments/results in disclosure. Semantic titles use the
query, capability ID, recipient/type, thread, URI, prompt or adapted tool name;
they never expose a raw JSON argument envelope. Dynamic operations stay visible
unless explicitly classified. A remote mutation interrupted after dispatch is
Result unknown, not success, and is never blindly replayed.

Activated skill instructions and MCP resource/prompt attachments retain their
source attribution; they are not human instructions or permission. Directed peer
events carry their useful attributed body automatically at a safe request
boundary and advance only after durable context admission. Human Board inspection
and unread acknowledgements are independent. A rare broadcast is one peer note
to other agents with active attached runs and paints one bounded broadcast header;
it never targets finished history, wakes idle agents or grants access.

Board, Memory/History, Capabilities and MCP share one semantic result-section
contract. Compact rows show useful body text, named participants/resources,
revision or count facts, and source/lifecycle metadata, not bare JSON or a
per-field character-budget ellipsis. Preview bodies wrap to the actual cell
width; large pages show two or three complete entry headings with bounded body
rows and an explicit remaining-entry/full-text disclosure hint. Expansion keeps
every retained body and evidence field, exact revisions/IDs, attributed authors,
Board roster/cursor/pagination and the readable request. Publication results
distinguish pending/admitting/admitted delivery from request disposition and run
outcome. Peer text remains peer data, not a red failure or proof of agreement.

Agent inspection uses that same disclosure. Current activity and the latest
recorded run are labeled separately; expanded evidence retains the exact trigger,
bounded public recent work, recovery facts and report revision references. An
attached or available participant without a current run is not labeled working.

The family includes `remember`, `recall_memory`, `update_memory`, `forget_memory`,
`memory_feedback` and `search_history`, not only the Board operations. Live
events, passive SQLite hydration, parallel leaves and compact child panes use
the same title/body builders. Exact family inputs are retained before title
derivation so mutation acknowledgements cannot lose authored content. Generic
MCP objects render labelled nested values; malformed structured envelopes fail
closed to an unavailable notice. Binary-only MCP results retain type, MIME,
resource and byte-count evidence instead of appearing empty. Known upstream
limits still apply: history search and compact memory evidence arrive with
backend-bounded snippets, and binary content is described, not rendered as a
document or dumped as base64.

File edits, writes, and patches remain separate and show their full tinted diff
by default; users can explicitly collapse them. Plan cards keep their own layout.
Raw and unclassified active thinking keep their stream and pane promotion. Promotion transfers the
body without changing its transcript identity or position. When thinking ends,
the body returns to that slot and the temporary pane/projection is released.
Completed thoughts therefore join adjacent activity regardless of whether they
were short inline blocks or long promoted panes. Interrupted thoughts retain
their readable body as a boundary. Expanded completed thoughts use the normal
transcript scroll and tree disclosure, not a second retained pane.
Assistant progress messages are real communication boundaries. Empty provider
envelopes neither occupy a row nor split a group.

An independent singleton uses one ordinary tool row, not a redundant tree header.
A parallel batch's groupable first child starts in its tree immediately, so
later siblings do not reparent it. A group's
header summarizes settled work, counts preparing/running calls, and reports how
many members are hidden. Retain at least six recent eligible tool leaves and
at least two tool request rounds in the current user turn. Extend backward by
whole rounds until both floors are met; never split a parallel batch at the
six-leaf boundary. This keeps singleton requests useful without dropping the
previous batch when a new request starts. Completion, retry, provider waits,
ticks, and queued (unconsumed) BTW notes do not retire evidence. Completed
thoughts contribute to the summary without consuming the tool-leaf budget.
The producing request ID travels with each tool event and canonical snapshot;
parallel children inherit it. Older unattributed calls use their stable call
identity, with batch children sharing their parent's identity.
Internal tool projection IDs are allocated uniquely, not from counters that
restart in replacement children or cold sessions. Exact provider call IDs outrank
internal aliases during lookup; a late delta for a settled call never clones or
reactivates its row. Stream updates change one retained leaf, not one row per delta.
Active leaves and explicitly opened evidence stay visible regardless of age.
Retention is source-driven, never a wall-clock timer. Expanding a
group reveals the hidden branches **inside the same tree**, in call order; it
never switches to a stack of legacy standalone tool rows.

Exactly one blank row separates transcript items. The transcript renderer owns
one leading spacer per item, including groups and panes. Subpanes return content
only, never a trailing separator; spacing must not depend on the previous frame's
pane height or the unprojected canonical neighbor. Tree leaves are contiguous.

Thinking blocks and panes, attributed peer messages, agent/Board operations and
sub-agent panes share the theme's `ColorSubPaneBg` as an edge-to-edge transcript
band. The fill includes the row's left/right padding, nested scrollbar and blank
separator; the outer chat scrollbar remains its own neutral rail. Leading
transcript spacers and ordinary conversation/tool rows retain `ColorBg`. Mixed
activity trees shade only their special leaves; a summary containing exclusively
special elements keeps the same band. `PaintLine.PaneBackground` carries this
source-derived treatment through wrapping, animation, disclosure, parallel
projection and hydration. Compose owns geometry only, never a second background
classifier. Markdown/code styling and text selection paint over the band without
changing content or interaction targets.

Every visible leaf uses a structured shared title, with separate action, operand,
and metadata spans. `ColorToolTitle` follows the theme's function accent;
`ColorToolOperand` is a quieter blend of its string and muted colors. Counts,
ranges, durations and permission/disclosure marks use `ColorMuted`. These roles
stay fixed through preparing, running and completion: lifecycle does not recolor
the whole label, and completed tool histories never turn into foreground-white
blocks. Wrapping preserves every span rather than flattening and restyling text.
The title and status line must **never** expose an argument envelope as raw or
truncated JSON, including unknown tools, missing operands, invalid/partial
arguments, parallel children, structured progress labels, invalid tool names,
and structured error reasons. Use one semantic
operand projection for live events and resumed snapshots; if no safe operand
exists, show only the action. Preserve genuine extracted commands, paths and
queries verbatim, even if they themselves contain JSON text. Do not fix one
widget by stripping braces: producer, batch projection, main chat, compact pane,
and resume must share the same source rule. For every tool schema or new argument
encoding, test identical live and hydrated rows (collapsed and expanded),
disclosure and result evidence, with no raw API field names in any title.
Sub-agent titles pair the allocated nickname, exact category and exact handle
once known (`Lily sub Dawn · Researcher (agent-8f21)`), across running panes,
spawn, poll, cancel and report results. Before allocation, show the requested
category plus assignment or the handle, never an invented nickname. A result
whose handle conflicts with the requested handle cannot label that call. The
list title remains bounded; expanded list entries include each nickname AND
handle. Category and identity carry through one canonical progress/display
snapshot so live, parallel-child and hydrated projections paint identically.

Sub-agent terminal events also publish one owner-scoped transient footer status:
nickname, category and explicit completed/cancelled/crashed/failed outcome. The
Update path forces the presentation deadline so cached output cannot hide the
status; the old anonymous `✓ done` text is invalid.

Normal indicators share the title color: active work uses `ui.ActivityDot` on
the shared frame clock, completed work uses a steady dot, and stopped/not-run
work uses a muted hollow dot. Only an actual failure has a red `×` and red action
title. Its command/path, metadata and output retain their ordinary roles. Compact
sub-agent tool lists use the same formatter and blink policy, not workspace-owned
preformatted labels. Expanded code and diff bodies keep their own syntax and
addition/deletion colors. Operands are not truncated at arbitrary
character counts. A short action stays on one line; long commands, paths and
queries wrap to actual terminal width. Each leaf independently expands its full
result. Errors stay outside folded success summaries: a clear failure header,
readable neutral-colored output, and explicit disclosure for long diagnostics.
Native command envelopes are decoded before painting; elapsed-time/exit/session
metadata is not dumped as escaped JSON. A sub-agent retains its named pane.

`multi_tool_use.parallel` remains one canonical call. Its ordered children are
transient projection leaves, not additional persisted messages or a second
painter. They follow the same grouping, mutation, failure, and disclosure rules.
Execution events carry exact arguments, not abbreviated labels. Valid preview
prefixes expose child operands during argument streaming; ordered partial result
snapshots expose each child's real start/completion while siblings are active.
Preview repair never changes execution input. Resume derives the same children
from the original call and final result. Missing results never imply success.
For streamed batch arguments, a child's name must have its closing quote in the
original JSON before it can determine tree versus standalone placement. Repair
may complete partial operands, but must never fabricate that name boundary.
Once the name is present, paths, commands, patterns and edit content continue
streaming without waiting for the child or batch to finish. Unnamed children
occupy no row and keep their original batch indices; an empty live wrapper stays
in the fixed activity strip, not a temporary standalone tool row. Terminal errors
and stopped/not-run outcomes remain visible even when no named child arrived.

Group summaries and visible membership are derived on source/disclosure changes,
not rescanned over a dense transcript on animation ticks. The batch projection
cache is keyed by canonical source changes; geometry and theme remain renderer
responsibilities. Upload progress cannot recreate a provider-wait row after
output has begun.

Stopping settles every pending tool display stage at the canonical conversation
boundary. Already received success/error results remain those outcomes even if
their terminal phase was still waiting behind presentation dwell; transient
queued phases cannot animate after stop. Cancelled and resumed-interrupted batch
parents settle unreturned children while preserving completed children and any
retained partial output. Both live and hydrated projections must remain static
across activity-dot phases.

## Reasoning Presentation

Reasoning visibility is a serving-protocol contract, not a model-name or catalog
heuristic. Decoders retain `raw`, `summary`, or unknown kinds and semantic block
IDs in the dependency-free `types/reasoning` contract. Provider adapters produce
that contract; actors, persistence and UI consume it without importing provider
implementations for reasoning. Responses summary events and OpenRouter `reasoning.summary` are summaries;
Responses reasoning-text events, OpenRouter `reasoning.text`, and Ollama thinking
are raw. Anthropic thinking blocks are visible summaries. Ciphertext and
signatures stay in opaque replay items and never become visible reasoning.
Flattened compatible-gateway fields enter a distinct `provisional` state, not
`raw` or `summary`: the first request can show a tidal `Thinking` preview in
the fixed strip, while its entire trace stays expandable. No pane promotion or
geometry change depends on guessing the kind while the request is streaming.
When reported usage arrives, a strong visible-to-billed reasoning-token gap
can settle the exact request as `summary_inferred`. Insufficient evidence
preserves `provisional`; measured near-parity settles that request as
`raw_inferred`. Explicit
wire-labelled raw/summary events always win. Two independent confirmed groups
from the same agent/account/provider/model may calibrate later requests on that
serving connection for either raw or summary. An exceptional single condensed
response can calibrate summary sooner. Child agents use their own connection's
completed requests within their tool loop; neither the parent's classification
nor its model supplies a hint. The
derivation is off-UI, scoped to the producing connection, and uses existing
persisted messages and request records on resume. It is never a model-name
catalogue, current-request guess, or per-frame transcript scan.

**Promotion to a nested pane is one presentation rule, independent of the wire
kind.** An active thought opens in its own pane once it has outgrown the inline
transcript form: the block already exceeds the inline body budget, or its
observed rows-per-second (measured from its first token, so provider first-token
latency is excluded) projects past that budget within a bounded horizon. A cold
or slow start therefore stays inline; a burst — or a trace long enough to
overflow — gets its pane promptly. `chatprojection.ThinkingOverflowsInline`
owns the decision and measures the same inline body and width the renderer
paints, so disclosure and promotion cannot disagree. Once a session has promoted
one block, every later thought in that session opens in a pane; the latch is
per session, shared by the main agent and its sub-agents, and a settings change
clears it. There is no per-role classifier and no size threshold tied to viewport
height. Wire kind still selects the strip's preview and the tide direction
(`Reasoning` vs `Thinking`), which does not affect promotion.

Live summaries default to one Markdown-styled line in the fixed activity strip,
showing the latest semantic block (or its latest paragraph), not the latest
network packet. While active and collapsed, no empty `Thinking` header occupies
a transcript row. The strip's chevron/click target and the usual disclosure
shortcut expand the canonical message in place; collapsing hides it again.
Earlier text remains in that message, and live/resumed expansion shows the
complete train. Completed
summaries join the ordinary activity tree and stop animating.

`ui.TextTide` is a paint-only light sweep for *every active strip page*,
including upload, provider wait, plan progress, provisional thinking and
summarized reasoning. Provisional thinking reverses at the ends; confirmed
summary thinking and subsequent active pages for that same serving connection
sweep left-to-right, clearing the full highlight beyond the text before
reentering from the left. `ui.TextTideMotion` retains the crest in cell columns:
text growth and resize change the remaining distance without rephasing it.
The nominal on-text traversal remains 2.4 seconds, with a minimum speed for short
labels; entry and exit use that same speed, so the loop duration follows the
distance instead of cutting off a visible tail. Changing account, provider or
model resets this presentation direction. Markdown emphasis and code backgrounds
survive in the thinking preview. `ColorActivityText` blends muted ink 30% toward
the theme foreground for a slightly more distinct supporting line. The tide
preserves every grapheme, semantic span, selection, background, and cell width.
The shared frame clock drives ink and crest emphasis;
there are no private timers, shifting glyphs, source invalidations, or Markdown
reflows on animation frames. Tool leaves retain `ui.ActivityDot`.

The sweep and automatic page rotation both stop when the owning agent becomes
inactive or enters stopping, cancelled, completed, failed or idle state. The
static first page retains the lifecycle label (only `Stopped` after cancellation);
the full plan stays available in the hover reader and transcript. A retained active
plan never implies that the agent is still running, and the presentation does
not alter its persisted lifecycle. A new active turn resumes animation.

Command safety assessments are retained as owner-scoped Notices. They do not
replace the agent's live phase or mark it as needing input: only an outstanding
permission or question does that. Reviewable command warnings use warning
severity; hard blocks remain critical notifications and tool errors. Completion,
cancellation and failure retain their own strip status without replaying old
warnings, and do not mark those notifications read or dismissed.

The chat screen reserves a fixed activity line and one separator row above the
composer. Waiting, preparation, thinking, tools, retry, cancellation, and idle
never change that reservation. Extremely short grids retain a transcript row
before allocating activity chrome. Workspace supplies one neutral activity
projection. The strip never echoes the human prompt or memory-summary title;
those remain available in their existing transcript/details surfaces.

Priority is required action/failure/retry, upload and provider wait, live thinking
reasoning, then the compact current task (`2/4 Refine plan updates`). Ordinary
planned work shows only that task; Goal/Next/completion-count pages and call-count
suffixes are absent. Upload bytes sent/total, percent, provider-wait identity
and elapsed time, and retry reason/attempt/countdown retain the former indicator's
information. These critical states never rotate away. Late upload callbacks for
an already-streaming request cannot rewind the actor or its presentation.

`chatprojection.ActivityStrip` derives and retains bounded rows by source,
geometry and theme. `shellchat` only publishes geometry and paints those rows;
`Paint` never parses Markdown or reads domain storage. Full rows pack ordered
thinking and compact task when they fit. Overflowing optional facts rotate every six seconds on the
shared frame clock. New semantic thoughts regain priority; ordinary token deltas
do not continually reset rotation.

One session-owned `plan.Store` publishes an immutable `types/activity.Plan` view
after successful transactions and restores it off-UI on resume. Existing tool
result events trigger UI reconciliation. Failed revisions cannot change this
view, and completed/cancelled plans release it. The strip never parses plan-tool
JSON, mutates plans, or maintains a second plan owner for the strip. Each child
retains its own store and immutable snapshot across settlement and passive resume;
child chats never borrow the root's plan. The projection
includes every ordered step for read-only inspection. Hovering a chat's status
band opens a content-sized, terminal-bounded WindowFrame popover above it, without
changing the selected pane or taking composer focus. Moving into it keeps it open;
wheel/scrollbar expose long plans. Click enables keyboard navigation and Ctrl+C
copies the plan. Leave, outside click, Esc, owner unmount, modal takeover or plan
completion dismiss it. Revisions refresh the existing reader without storage I/O.

When browsing older messages, a centered native `↓ Latest` button occupies the
already-reserved gap above the status line. It adds no transcript height and
does not cover messages. Click goes to the exact pane's materialized tail and
re-arms stream following; short content, bottom position and tiny grids hide it.
Captured selection/scrollbar drags keep ownership when crossing that band.

Both planning interfaces publish clean desired snapshots. Omitted obsolete steps
remain in immutable revision history, not cancelled entries in the current card.
`replace=true` atomically supersedes the goal and step provenance on the same plan
identity; stale or invalid updates preserve both current state and history.
Prompts require latest-intent review, milestone updates, and honest final closure.

Transient status messages are not additional transcript rows; history and
compaction boundaries remain ordinary transcript content. Ordinary stopped
boundaries and cancelled recovery notices remain canonical evidence but are
excluded before grouping, spacers and height calculation. Internal
actor generations still include stopped/unbilled work for cancellation and
recovery, but are not the displayed successful-call count.
**Automatic compaction paints as one ordinary tool row, never the operation
table.** Automatic, background-prepared and decay boundaries use the shared tool
title (`paintToolTitle`) with no context-boundary rules and no blank rows beyond
the item's single leading spacer: `Compacting context <before> — <stage>` while
it works (`— retry n/m` and `— saving checkpoint` name those phases), `Compacted
context <before> → ~<after>` when it commits (`— prepared in background` or
`— older tool output trimmed` for those triggers), and `Compaction
cancelled/failed/interrupted — …` when it does not. Live events and resumed
records carry the same trigger, stage and token facts, so hydration paints the
identical row. Only a manual `/compact` keeps the ruled operation table, because
the human invoked it and watches it run.
Manual compaction owns the activity strip while its boundary is current:
preparing, extraction, retry, and commit show operation progress without
rotating an unrelated plan. Completion reports "Context compacted · Ready to
continue"; failure and cancellation retain their own settled label. A resumed
boundary provides the same status until the next turn. The old session
transcript stays in the archive; the new checkpoint is the active context.
At the next human turn, the session supplies the current plan revision and
steps in hidden runtime context so the model can assess whether the plan is
still relevant; the system planning contract calls for prompt updates when
the user's request changes the work. The plan store remains the authority.
Markdown retains its established Glamour heading, link and inline-code hierarchy.
Document ink and fenced-code colours follow the active theme; light backgrounds
use readable light-palette inks. Theme changes invalidate the Markdown renderer
as well as its projected rows. Do not override every text leaf's colour: that
overrides inherited heading colours and flattens the hierarchy.

The native Markdown bridge uses the shared ANSI decoder and carries SGR state
across wrapped lines. OSC hyperlink metadata is not printable content and must
not change table widths, wrapping, selection text, or the painted transcript.

## Activity motion

- `ui.ActivityDot(spinFrame)` is the only product activity-dot animation:
  synchronized Unix-millisecond clock, `●` for 400ms then `○` for 400ms.
- Active chat lifecycle/thinking/tool/sub-agent/session rows use it with one
  stable semantic color. Never add local speed formulas, brightness/color
  pulses, braille frames, or `AttrBlink`; see `docs/guides/component-authoring.md`.
- Every active fixed-strip page uses `ui.TextTide`, a paint-only ink sweep that
  fades off both ends before wrapping in forward mode. `ui.TextTideMotion` keeps
  the crest in cell columns on the shared clock, so text growth and resize do
  not rephase a visible sweep. Entry and exit share its travel speed; the full
  highlight clears the last character before reentering from the left.
  Explicit reasoning kind and semantic blocks come from provider wire contracts.
  Flattened unlabelled text gets a neutral provisional strip preview and
  expandable full train, not a guessed
  raw pane or confirmed summary. A strong provider-reported usage discrepancy
  can settle the accepted request and, with repeated connection-scoped evidence,
  calibrate subsequent streams. Explicit wire raw always keeps its pane. Never
  classify by model name or infer from current-request usage before it arrives.
- Active collapsed compact thoughts paint no stray transcript header. The fixed
  strip owns a clickable chevron and the normal disclosure shortcut reveals the
  complete retained trace in the transcript. `ColorActivityText` blends muted
  ink 30% toward the theme foreground, keeping the strip distinct from metadata.
  Provisional thinking tides reversibly; a confirmed summary tides left-to-right
  through the remaining active turn for that serving
  connection. A provider/account/model change resets the direction. Do not
  change transcript text, width or rows on animation frames.
- Reasoning contracts live in `types/reasoning`, request outcomes in
  `types/accounting`, and immutable plan activity in `types/activity`. Rendering
  and the event pump never import provider implementations for these contracts.
  `chatprojection.ActivityStrip` derives cached rows; `shellchat` only publishes
  geometry and paints. Never parse Markdown, decode plan-tool JSON, or access
  storage from `Screen.Paint`.

## Activity strip, plans and notices

- Chat reserves activity-strip geometry independently of phase. Transient
  provider/tool status must not appear/disappear as transcript rows. Durable
  history and compaction boundaries remain visible. Ordinary stopped boundaries
  and cancelled recovery notices remain canonical/context evidence but contribute
  no chat rows, spacers, grouping boundaries or height. One quiet `Stopped` strip
  remains; interrupted tools and genuine actionable recovery keep their evidence.
  Memory-summary titles
  load through session-owned cancellable jobs, never from a screen or Update.
- The strip prioritizes upload bytes/percent, provider waiting, retry and required
  action over optional rotation. Tidal motion applies to every active strip
  state; provisional and summarized thinking get the compact preview. Do not
  echo the user prompt there. Ordinary planned activity is only `a/n Task name`;
  no Goal/Next/progress-count pages or call-count suffix. Thinking can share or
  rotate with that compact task. Stopping, cancelled, completed and idle strips
  are static; cancellation says only `Stopped`. Stopping never changes the
  persisted plan lifecycle.
  The session's one `plan.Store` publishes its committed immutable activity view;
  the strip never becomes another plan owner. Hovering the strip inspects all
  ordered steps in a bounded, scrollable passive WindowFrame popover, owned by
  that exact pane without taking composer focus. Its text is selectable like
  chat: a captured drag paints `TextSelection` colors through the shared
  `PaintLineAtBackground` columns, release copies the exact plain text, and the
  highlight never outlives the gesture. Leave/Esc/outside/owner removal
  dismiss it. `Latest` is a centered native button in the existing reserved gap
  while scrolled up; it adds no transcript height and re-arms exact-pane follow.
- Plans are clean desired snapshots, not execution logs. Omitted obsolete steps
  remain only in immutable revision history. Both plan tool interfaces expose
  `replace=true` to atomically supersede a changed goal and reset step provenance;
  neither silently appends cancelled steps. Prompts require latest-intent review,
  updates at milestones and honest closure before the final response. Explicit
  revision fences and transaction rollback preserve history and current activity.
- Command safety assessments belong to owner-scoped Notices, not retained
  `AgentState` alerts or live `NeedsAction` status. Only pending permissions and
  questions need input. Completed, cancelled and failed turns keep their own
  phase while safety notification history stays unread until acknowledged.

## Tool leaves and titles

- A tool failure may end with one model-facing hint for the next call
  (`prompt.WithToolHint`, text owned by `prompt/`). Rows never show it: every
  tool row, live, hydrated and in sub-agent panes, renders the content before
  `types.ToolHintSeparator` (`types.StripToolHint`). The model still receives
  the full result.
- Activity groups and parallel batches share one staged tool-leaf projection.
  Expansion reveals hidden branches in place; it never replaces the tree with
  standalone legacy rows. Execution events carry exact tool arguments, not
  formatted/truncated summaries. Native command JSON envelopes are decoded only
  at presentation; file edits/writes/patches stay separate and default-open.
  **Never print a raw JSON tool-argument envelope in any tool title, status,
  compact pane, or batch leaf.** `tools.SummarizeArgs` is the single semantic
  operand authority for live events, child projections, and resumed snapshots.
  Missing, malformed, nested, and unfamiliar argument shapes fail closed to a
  name-only title, not a truncated JSON fallback; a real command/query/path
  extracted from a field remains readable. The shared title painter must also
  guard raw structured progress labels, unsafe tool identifiers, and nested
  structured error reasons. Every new tool schema needs live,
  parallel (when applicable), and hydrated tests asserting the same title,
  row geometry, disclosure, and full evidence access across both paths.
  Sub-agent tool identities include allocated nickname, exact category and exact
  handle whenever those facts are available: running panes, launch outcomes,
  polls, cancellations, list entries and authored reports. A call awaiting
  allocation shows requested category + assignment; once allocated it never
  guesses a nickname from the requested role/task, or attaches a name from a
  mismatched result. Lists keep a bounded title and expose each name+handle pair
  in expanded evidence. Live, batch and hydrated rows share the same title/
  progress snapshot and must paint identically.
  Batch preview names come from closed strings in the original input, never
  JSON-repaired partial names. Unnamed children and empty live envelopes occupy
  no transcript row; named children stream their arguments immediately in the
  correct tree/standalone placement. A batch's first groupable child starts in
  its tree rather than reparenting when its siblings arrive.
  Summary counts and visible membership are source/disclosure-derived, not
  rescanned on animation ticks. See `docs/reference/chat-activity.md`.
- Tool titles keep action, operand and metadata as distinct semantic spans,
  with theme-dependent colors that do not change with lifecycle. Wrapping must
  retain those roles, including in compact sub-agent lists. Reserve the red
  title and `×` indicator for actual failures; never recolor the operand or
  whole result red, or turn settled tool rows into full-foreground text.
- Keep at least six recent eligible tool leaves and the last two tool request
  rounds visible, extending backward by whole rounds; completion alone must not
  fold a leaf. Completed thoughts do not consume that tool budget. Promoted
  thinking returns to its original canonical transcript slot on completion and
  releases the temporary pane, so long thoughts cannot orphan adjacent tools.
  Transcript items own one leading spacer; subpanes never add trailing gaps.
  Persist `ToolRoundID` and inherit it in parallel projections. Retry
  feedback is one retained, cancellable row. User stops preserve readable work
  and append a durable `turn_stopped` boundary; never strike through cancelled
  content or invalidate a prior completed turn.

## Markdown and assignment rendering

- **Markdown keeps its established hierarchy:** use Glamour's differentiated
  headings, links and inline code, with theme-bound document/fenced-code ink and
  a readable light palette. Never force `StyleConfig.Text.Color` globally; it
  overrides inherited heading colours. Native bridges tokenize through the
  shared ANSI decoder, suppress OSC/control payloads, and carry SGR state across
  a rendered block (`BridgeAnsiLines`), not only within individual rows.
  `rendering.MarkdownRenderer` is Glamour's ANSI renderer over goldmark with
  only the raw-HTML parsers removed: chat text is not HTML, so `<context>` or
  `Vec<T>` stays literal instead of being sanitized away. Never add an escaping
  pre-pass or restore `glamour.NewTermRenderer` for chat.
- **Child assignments display typed fields, never their envelope.** Providers
  receive `prompt.BuildChild`'s escaped `<delegated_assignment>`; live events,
  persisted snapshots and recovery checkpoints carry `childAssignmentDisplay`
  (description + verbatim instructions as Markdown). Never parse the envelope
  or model-authored structure back out of text.

## Identity And Usage

Current selection and historical production identities are separate. The header
and collapsed Open Tabs identity use the owning session's current configuration,
including after a background-tab settings save. An in-flight request retains its
producing identity. The previous model's context occupancy is not attributed to
a newly selected model.

The workspace request ledger derives per-account/provider/model totals within
each existing scope. A displayed turn is a successful API call, deduplicated by
request ID, not an admitted user input or actor generation. Multiple successful
tool rounds in one actor generation count separately. Failed and interrupted
requests retain their accounting but do not increment this count. Request
outcomes are explicit, durable facts; usage revisions cannot change a known
outcome or count the same call twice. Unreported historical outcomes are unknown,
not inferred successes: counts are labelled unreported or as a known lower bound.
Distinct actor generations remain a separate internal metric. Revisions replace
requests and invalidate the cached rollup. Live and resumed rollups use
stable monetary accumulation order and retain unknown/estimated/partial cost
availability. Open Tabs keeps current identity, activity, turn count and cost in
collapsed rows; expanded usage exposes each producing model alongside the
existing aggregate totals.

## Usage accounting

- Accounting request IDs use `math/rand/v2` for non-security uniqueness, never
  runtime turn/step counters that restart after resume. UI and persistence share
  `sessiondb.UsageRecordFromEvent`; preserve uint64 generations and per-field
  reported availability. Provider omissions are not measured zeros. A revision
  replaces the same request once and older revisions cannot overwrite it. Context
  stays agent-local even when inspecting subtree/session totals.
- Current model selection is read from the owning session's configuration, not
  its last usage record or another tab's backend. Per-model accounting groups
  the immutable producing account/provider/model. Displayed turns count successful
  API calls, not actor generations; request outcomes are durable and immutable
  across billing revisions. Unknown outcomes remain unknown. Actor generations
  are separate cancellation/recovery identities. Preserve cost availability.

## Header and footer telemetry

- The footer is static, low-profile aggregate text. Only required
  user action (`⚠`) and failures (`✕`) receive attention signs.
- A settled sub-agent publishes one short owner-scoped footer transient with
  nickname, category and explicit outcome (completed, cancelled, crashed, timed
  out or failed). It enters through Update and forces the presentation deadline;
  cached output must never hide it. Anonymous status such as `✓ done` is invalid.
- Compact editor status retains current-line blame in the active header; full
  blame identity and Copy blame commit stay available in Status Overview and
  the editor context menu. Footer-right carries meaningful workspace-scoped LSP
  health, project Problems, and background work (Git identity and tracking stay in
  the header branch and change segments); it omits zero counts but must not
  collapse to a placeholder when those facts exist.
- Editor position uses compact `line:column` header geometry in the same muted
  treatment as blame/uptime. Footer-left is the guidance group: unread Notices,
  then a small ranked navigation set (find, next tab, focus tree), then Help.
  It must remain focus-aware and must not regress into the old Save/Undo/Redo
  inventory. The shared footer painter reserves the ranked guidance/feedback
  group up to half the row, then fits the navigation pills in the remainder
  with explicit More overflow. Adding an app utility must not crowd out a
  completion notice or the useful composer hints. `configureFooterScreen`
  supplies the two groups; the component owns their width and hit geometry.
- Agent header is muted telemetry only: model, `↑` latest reported input tokens
  over that request's runtime context capacity/occupancy, `↓` cumulative agent
  output, reported cache-hit rate, known estimated cost, and uptime. It never
  changes `↑` into upload bytes or adds lifecycle/status text. Request age,
  serialized payload and lifecycle belong in agent/tab details and Activity;
  existing permission/question/safety surfaces remain authoritative.
  Background context-summary preparation/readiness is explained in agent details,
  never an unexplained "checkpoint…" suffix. Its exact job lifecycle settles on
  completion, discard, failure and cancellation without changing the live phase.
  An open Open Tabs disclosure refreshes on the existing workspace-status cadence,
  retaining selection, filter and expansion rather than leaving a stale snapshot.

## Recovery And Stop

Unnatural attempt failures enter the [run recovery supervisor](run-recovery.md),
not task death. One retained incident row uses a static severity-colored heading,
and defaults to a single compact content line, not the old ruled notice card.
Automatic recovery shows a red square blinking on the shared activity clock,
real countdown, `(Retry now)` and the ordinary disclosure chevron. A no-drag click
on Retry now wakes only that exact incident; header/keyboard disclosure reveals
the complete neutral diagnostic and muted guidance. Dragging selects/copies
instead of activating either action. Expanded reason rows stay cached on clock
changes. Settled incidents remove retry targets; attention/interrupted states are
static. Transport and DNS task recovery do not require typing `/retry`.
The fixed strip carries concise recovery/countdown or explicit recovery guidance,
never the raw diagnostic. Waiting for attention is static. Repeated failures update
one incident instead of spamming red rows; owner-scoped Notices expose action.

YOLO enable and disable use that same notice-row layout as system status, never
assistant speech. Slash and palette toggles share one policy path, and the
backend session remains authoritative. Confirmation starts on Cancel; cancelling
or a stale confirmation adds no transcript row. The warning accurately describes
bypassed approval prompts while retaining hard blocks and restore safeguards.

The transport owns retries only before model output has been accepted. It must
never replay a stochastic request and compare the new response to an old prefix.
Classification requires positive evidence of permanence (see
[run recovery](run-recovery.md)): connection resets, lost HTTP/2 connections,
premature EOF, stalled progress, gateway stream termination, rate limits,
overloaded providers and unfamiliar transport failures recover. Invalid requests,
authentication/permission failures, exhausted billing/quota and invalid certificates
remain nonretryable request failures. The task supervisor separately rechecks DNS
lookup failures with capped cooldowns, because a temporarily missing provider
record or disconnected resolver should not require human polling. Respect
`x-should-retry: false` for provider responses.

Defaults are six total request attempts with jittered exponential backoff capped
locally at 30 seconds. Server `Retry-After` or `retry-after-ms` advice may extend
that wait, but all automatic cooldowns have an absolute five-minute ceiling.
Zero/past advice cannot remove local pacing. Exhaustion yields to the task
supervisor rather than permanently ending work. A single progress watchdog owns response-header waiting; the TCP/TLS
connection timeout is not a competing header deadline. Account connections own
attempt/backoff budgets and initial/minimum/maximum progress timeouts directly. Defaults are
60 seconds initially, a 45-second adaptive floor, and a 300-second ceiling.
Configured overall request deadlines cover upload, streaming, retries and retry
waits; a caller's earlier deadline still wins. Unlimited overall duration never
disables progress detection. See [connection timeouts](settings.md#connection-timeouts).

After accepted output, preserve the canonical partial response and issue a hidden
continuation. Bounded local interruption recovery yields to the same supervising
task when exhausted; it never replays accepted stochastic output. If tool construction was
interrupted, execute none of that response's calls, discard unfinished protocol
fragments, and ask for complete calls. These are `Not run`, not failed operations.
Retry feedback starts on the first retry, occupies the fixed activity strip, states the
reason/attempt/countdown, and remains immediately cancellable with Esc.

Stopping preserves completed effects and accepted text. Do not strike through
assistant text, thinking, or tool titles. Show one quiet `Stopped` in the fixed
strip, not additional `Stopped by you` or `Recovery stopped` transcript rows,
and keep input ready for the next instruction. Interrupted tool leaves, real
errors and actionable recovery remain visible. The provider context
records the explicit non-human `turn_stopped` origin; it warns that interrupted
running tools may have partial effects and must be inspected before resuming.
Never retroactively mark a prior completed turn as cancelled, restart work on
resume, or treat a request deadline as a user cancellation.

Reference behavior was checked against the installed Claude Code 2.1.251 retry
and cancellation paths and the public [Claude error contract](https://platform.claude.com/docs/en/api/errors),
[TypeScript SDK retry policy](https://github.com/anthropics/anthropic-sdk-typescript/blob/main/src/client.ts),
and [interactive controls](https://code.claude.com/docs/en/interactive-mode).
Crude retains its own protocol-neutral ownership and safety rules; no upstream
implementation is copied or depended on.

## Dense resume

- Hydration publishes one normalized stopped transcript, never each growing
  prefix. Diff previews select their lexer once per file and tokenize only their
  visible preview budget; counting omitted visual rows must not highlight them.
  The 24-row preview stops building rows at its budget, and its "+N more
  changes" footer counts hidden diff lines, not wrapped screen rows.
- File edits render expanded by default, so a row lexes at most
  `diffSyntaxSyncBytes` (1 KiB) of diff text on the UI goroutine. Larger files
  paint at once with their diff bands and fallback colors while one background
  worker lexes them (`tui/chatprojection/diff_syntax.go`, a bounded cache keyed
  by lexer and text); the waiting row is frame-sensitive, repaints on the next
  activity phase and stops once colored. Colors never change a row's text or
  height. Live and hydrated rows take the same path.
- Offscreen history materializes nearest-first within a per-frame budget
  (`chatprojection.DefaultMaterializeBudget`); unrendered items keep estimated
  heights and the line under the viewport top stays fixed while they settle. The
  visible range is never budgeted, and a width-only reflow leaves offscreen
  items at their previous heights until exposed.
- Hydration projects explicit display snapshots first, then excludes context-only
  provider envelopes using visibility/metadata. Recalled memory without a display
  snapshot must contribute zero chat rows, not blank fallback rows sorted at the
  transcript tail by their later persistence timestamp. Restore and compaction
  boundary snapshots remain visible; provider history is not deleted.
  `canonicalizeDisplayMeta` derives no snapshot for a context-only message (turn
  context, runtime continuity, enrichment): resuming an interrupted run rebuilds
  its history from the recovery archive, which holds them, and they painted as
  human messages.

## Data and parity

The activity projector uses canonical role, outcome, duration, tool name, and stable IDs. It should not infer call completion from spinner frames or rely on wall-clock rendering order. A `reply` becomes an assistant progress message (not a second tool row) when its tool result is delivered; recovery projects that same result with the same `reply:<tool-id>` identity. Parallel results are presented in provider call order on both paths. Persist the child agent ID and result identity, including synchronous waits, so recovered parent rows link to the same child review pane as live ones; the child pane owns its display name and outcome. If any source lacks those facts, show an honest standalone row rather than guessing identity or success.

Regressions must compare the rendered live event journey to a session's recovered snapshots: active vs settled, communication boundaries, parallel calls, expansion/copy and disclosure, failure/cancellation, children, resize/scroll, and a settled transcript with many calls. The visible summary may be compact; expanding it must never lose the original audit trail.
