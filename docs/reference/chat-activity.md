# Chat activity projection

> Status: Current — activity grouping and audit/disclosure contract.

The canonical conversation is the audit log. The chat renderer projects it into a compact, reversible activity view; it never deletes, rewrites, or persists presentation-only groups. Both live events and resumed session snapshots enter the same renderer. A group is a run of adjacent eligible calls (or completed thoughts) between visible boundaries; it may contain active calls until they finish. Its stable identity comes from its first message, not from a transient render index. Click its summary to inspect its original calls in chronological order, and click an individual eligible call to inspect its existing full output. The original result, command, input, permission tag, duration, and child identity remain available.

## Classification

| Treatment | Tools |
| --- | --- |
| Aggregate after success | `exec_command`, `write_stdin`, `execute_command`, `async_execute_command`, `check_async_command`, `read_async_output`, `read_file`, `list_directory`, `glob_files`, `grep_files`, `web_search`, `web_fetch`, `arxiv_search`, `recall_memory`, `search_history`, `wait`, `poll_sub_agent`, `list_sub_agents` |
| Always visible | `apply_patch`, `edit_file`, `write_file`, `delete_file`, `move_file`, `copy_file`, `update_plan`, `get_plan`, `run_sub_agent`, `cancel_sub_agent`, `kill_async_command`, `write_report`, `remember`, `update_memory`, `forget_memory`, `question`, `reply` |

Unknown tools stay visible until intentionally classified. A failed or cancelled call, a child failure reported by an otherwise successful poll, and any required user action remain visible as boundaries. A background command's successful *start* is not evidence that the background job finished; label it as started, not completed. Never imply that all calls passed solely because their tool transport succeeded.

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
Active leaves and explicitly opened evidence stay visible regardless of age.
Retention is source-driven, never a wall-clock timer. Expanding a
group reveals the hidden branches **inside the same tree**, in call order; it
never switches to a stack of legacy standalone tool rows.

Exactly one blank row separates transcript items. The transcript renderer owns
one leading spacer per item, including groups and panes. Subpanes return content
only, never a trailing separator; spacing must not depend on the previous frame's
pane height or the unprojected canonical neighbor. Tree leaves are contiguous.

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
Sub-agent titles pair the allocated nickname with the exact handle once both
are known (`Lily sub Dawn (agent-8f21)`), across spawn, poll, cancel and report
results. Before identity is available, show the handle or assignment, never an
invented nickname from the requested profile. A result whose handle conflicts
with the requested handle cannot label that call. The list title remains bounded;
expanded list entries include each nickname AND handle. Results carry the same
immutable identity through live, parallel-child and hydrated projections.

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
`raw_inferred` and promotes a long active trace into its own pane. Explicit
wire-labelled raw/summary events always win. Two independent confirmed groups
from the same agent/account/provider/model may calibrate later requests on that
serving connection for either raw or summary. An exceptional single condensed
response can calibrate summary sooner. Child agents use their own connection's
completed requests within their tool loop; neither the parent's classification
nor its model supplies a hint. The
derivation is off-UI, scoped to the producing connection, and uses existing
persisted messages and request records on resume. It is never a model-name
catalogue, current-request guess, or per-frame transcript scan.

Live summaries default to one Markdown-styled line in the fixed activity strip,
showing the latest semantic block (or its latest paragraph), not the latest
network packet. While active and collapsed, no empty `Thinking` header occupies
a transcript row. The strip's chevron/click target and the usual disclosure
shortcut expand the canonical message in place; collapsing hides it again.
Earlier text remains in that message, and live/resumed expansion shows the
complete train. Summary blocks never promote to nested panes. Completed
summaries join the ordinary activity tree and stop animating. Explicit raw
reasoning continues to use its pane.

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
static first page retains the lifecycle label (`Stopped` after cancellation) and
plan facts that fit; the full plan stays in the transcript. A retained active
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
reasoning, active-plan progress/current steps, plan objective/upcoming step, then
successful-call counts. Upload bytes sent/total, percent, provider-wait identity
and elapsed time, and retry reason/attempt/countdown retain the former indicator's
information. These critical states never rotate away. Late upload callbacks for
an already-streaming request cannot rewind the actor or its presentation.

`chatprojection.ActivityStrip` derives and retains bounded rows by source,
geometry and theme. `shellchat` only publishes geometry and paints those rows;
`Paint` never parses Markdown or reads domain storage. Full rows pack ordered
facts when they fit. Overflowing optional facts rotate every six seconds on the
shared frame clock. New semantic thoughts regain priority; ordinary token deltas
do not continually reset rotation. Counts occupy spare trailing space only and
never displace primary facts or consume a rotation page.

One session-owned `plan.Store` publishes an immutable `types/activity.Plan` view
after successful transactions and restores it off-UI on resume. Existing tool
result events trigger UI reconciliation. Failed revisions cannot change this
view, and completed/cancelled plans release it. The strip never parses plan-tool
JSON, mutate plans, or maintain a second plan owner for the strip.

Transient status messages are not additional transcript rows; durable stop,
history, and compaction boundaries remain ordinary transcript content. Internal
actor generations still include stopped/unbilled work for cancellation and
recovery, but are not the displayed successful-call count.
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

## Recovery And Stop

Turn failures use one static notice heading with severity-colored ink and the
full, neutrally colored, word/grapheme-wrapped diagnostic beneath it. The fixed
strip says that the turn failed and points to the transcript; it never repeats
a truncated filesystem/provider diagnostic. One owner-scoped failure Notice
retains the exact details, without an asynchronous foreground-feedback echo.

YOLO enable and disable use that same notice-row layout as system status, never
assistant speech. Slash and palette toggles share one policy path, and the
backend session remains authoritative. Confirmation starts on Cancel; cancelling
or a stale confirmation adds no transcript row. The warning accurately describes
bypassed approval prompts while retaining hard blocks and restore safeguards.

The transport owns retries only before model output has been accepted. It must
never replay a stochastic request and compare the new response to an old prefix.
Connection resets, premature EOF, stalled progress, transient gateway stream
termination, rate limits, and overloaded providers can recover. Invalid requests,
authentication/permission failures, exhausted billing/quota, invalid certificates,
and nonexistent DNS names are not transient. Respect `x-should-retry: false`.

Defaults are six total attempts with jittered exponential backoff capped at 30
seconds. An explicit `Retry-After` or `retry-after-ms` is a minimum, not shortened
by that cap. Delays over two minutes stop automatic recovery rather than sending
too early. A single progress watchdog owns response-header waiting; the TCP/TLS
connection timeout is not a competing header deadline. Per-provider settings can
override attempt/backoff budgets and initial/minimum/maximum progress timeouts;
accounts can override the template's complete connection-tuning set. Defaults are
60 seconds initially, a 45-second adaptive floor, and a 300-second ceiling.
Configured overall request deadlines cover upload, streaming, retries and retry
waits; a caller's earlier deadline still wins. Unlimited overall duration never
disables progress detection. See [connection timeouts](settings.md#connection-timeouts).

After accepted output, preserve the canonical partial response and issue a hidden
continuation, at most three consecutive times with 1/2/4-second cooldowns. A clean
completed response resets that interruption budget. If tool construction was
interrupted, execute none of that response's calls, discard unfinished protocol
fragments, and ask for complete calls. These are `Not run`, not failed operations.
Retry feedback starts on the first retry, occupies the fixed activity strip, states the
reason/attempt/countdown, and remains immediately cancellable with Esc.

Stopping preserves completed effects and accepted text. Do not strike through
assistant text, thinking, or tool titles. Show a quiet, durable `Stopped by you`
boundary, and keep input ready for the next instruction. The provider context
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

## Data and parity

The activity projector uses canonical role, outcome, duration, tool name, and stable IDs. It should not infer call completion from spinner frames or rely on wall-clock rendering order. A `reply` becomes an assistant progress message (not a second tool row) when its tool result is delivered; recovery projects that same result with the same `reply:<tool-id>` identity. Parallel results are presented in provider call order on both paths. Persist the child agent ID and result identity, including synchronous waits, so recovered parent rows link to the same child review pane as live ones; the child pane owns its display name and outcome. If any source lacks those facts, show an honest standalone row rather than guessing identity or success.

Regressions must compare the rendered live event journey to a session's recovered snapshots: active vs settled, communication boundaries, parallel calls, expansion/copy and disclosure, failure/cancellation, children, resize/scroll, and a settled transcript with many calls. The visible summary may be compact; expanding it must never lose the original audit trail.
