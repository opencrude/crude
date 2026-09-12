# Status System Overhaul Plan

> Status: LANDED - revision 2, 2026-09-10. The footer-preserving redesign,
> Status Overview, request-ledger accounting, typed status/notifications,
> service-health projections, selective hints, and search/input fixes are
> implemented and validated. This document replaces the earlier design.

## 1. Product Direction

Make status useful at a glance without making the workspace look like a dashboard.
The design must answer three different questions in three deliberately different
places:

| Surface | Question | Character |
|---|---|---|
| Header | What am I working with, and what is its state? | Contextual, concise, stable |
| Footer | What elsewhere needs attention, and is there a useful action here? | Quiet, global, selectively helpful |
| Status Overview | What are the details, and where can I act on them? | Inspectable, navigable, explicit |

The footer stays. It is not a duplicate header and is not a permanent keyboard
cheat sheet. The header does not become a dumping ground for everything removed
from the footer. The inspector is not a serialization of all backend fields.

Design principles:

- **One fact, one default location.** Detail views may repeat facts deliberately;
  header and footer must not repeat the same model, state, coordinates, or hint.
- **Silence is valid.** Empty space and a quiet footer are preferable to `0 active`,
  `0 warnings`, `Visibility: Visible`, or an inventory of familiar shortcuts.
- **Detail is progressive.** Show the useful summary first and make the complete
  evidence available without forcing everyone to scan it continuously.
- **Shared controls are necessary, not sufficient.** Their composition, spacing,
  focus, capture, empty states, and action placement still require product design.
- **Interaction is part of appearance.** A divider that looks draggable but does
  not move, a clipped value, or an action that changes while pressed is bad design.
- **Truth outranks tidiness.** Unknown, estimated, partial, and measured-zero values
  stay distinguishable. Do not invent provider data to make a table look complete.
- **No legacy preservation.** Crude is pre-production. Keep one clean authority;
  do not retain old accounting/display adapters or fabricate historical backfill.

## 2. What Went Wrong

The earlier plan specified too many capabilities before establishing a precise
visual hierarchy. Implementation then advanced across many layers without first
passing real product acceptance for the shell and inspector. Restoring the footer
without redefining its content brought the original clutter back.

The rejected prototype is retained here as design history. The revised gates
below were subsequently implemented and validated through real captures,
end-to-end input tests, dense benchmarks, race tests, and repository gates.

### 2.1 Screenshot And Source Findings

| Problem | Observed evidence / source | Revision |
|---|---|---|
| Footer text touches the terminal edges | `shellfooter/footer.go` uses the slot edges; header uses theme padding | Publish one shared shell content rail for both bars |
| Footer says `0 agent active` and repeats idle/model information | `statusActions.ActivityView`, `configureFooterScreen` | Omit zero and routine idle counts; stop repeating the active item |
| Header reads `crude`, model, `crude` on launcher | `statusHeaderLeftSegments` reads global model and app/workspace names | No global model on launcher/editor; avoid identical adjacent identities |
| Row/column/Git information is not coherently relocated | Header facts and old contextual-help prefix both remain | Typed header facts; help contains actions only |
| Obvious hints fill the footer | Full `contextualHelpSegments` forwarded into footer | Rank hints by usefulness and focus, not by registry order |
| First overview tab has extra leading space | Body inset plus `ui.Tabs.tabLeadPad` | First label aligns with search/list rail; no second leading inset |
| Mode tabs run straight into the content | One-row tab layout and no full-width mode separator | Distinct mode row, baseline separator, body region |
| Action divider cuts through button brackets | `Surface.Paint` and `Layout` both use `H-2` for rule/actions | Give rule and command row different layout rectangles |
| Columns cannot be resized in the real window | User reproduction; mounting `SplitDivider` did not prove routing works | Reproduce through WM/Host, verify capture and painted reflow; root cause still open |
| Right pane repeats Idle, then repeats all usage scopes | `detailControl.syncSelected`, `agentUsageDetail` | One identity/state heading; one context section; selectable usage scope |
| Unknown cache/reasoning fields dominate the summary | Repeated `Not reported` rows in every scope | One concise availability explanation; exact fields in expanded details |
| Long values are cut off instead of inspectable | `detailControl.Paint` truncates every line | Wrap detail text, retain exact values, add selection/copy access |
| Buttons and every section are rebuilt on routine refresh | `syncStatusOverview`, `syncItems`, `rebuildActions` | Retain controls, selection, focus and capture; project changed data only |
| Empty groups and incidental ordering add noise | `statusTabItems` always adds headers; editor paths are sorted | Omit empty groups; follow actual workspace order |
| New status adapter still has no-op compatibility methods | `tui/viewmodel/status_ui.go` | Remove them; do not call an empty setter a completed migration |
| Lifecycle/notification updates can still infer the owner from current selection | `statusActions.owner`, `SetAgentRetry`, `SetAgentConfirming` | Carry the operation's identity through events; selection is not ownership |

The divider failure is not claimed diagnosed by reading its primitive. It remains
an end-to-end reproduction task. No alternative drag math should be added before
identifying the failing route or invalidation boundary.

### 2.2 Implementation Status

| Workstream | Honest current status |
|---|---|
| Product/visual acceptance | LANDED at desktop and constrained widths using sequential repository captures |
| Request ledger and scoped usage | LANDED; request identity is authoritative and summaries are generation-cached |
| Lifecycle, operations, LSP health | LANDED; resource snapshots replace rendering-owned lifecycle state |
| Notifications | LANDED; typed, deduplicated, bounded, persisted, and separately resolved/read |
| Status Overview | LANDED; retained master/detail, real divider capture, narrow drill-down, stable actions |
| Context Help | LANDED; compact/expanded/off setting and focus-ranked compact hints |
| Header/footer | LANDED; shared rails, active/global split, branch symbol and plain uptime |
| Cleanup and release gates | PASSED; full tests/races/lint/docs/design and bounded performance scenarios |

Do not discard unrelated work or reset the repository. Preserve useful changes,
replace incorrect ones deliberately, and review broad mechanical replacements.

## 3. Final Surface Responsibilities

This table is the migration authority. It replaces the instruction to remove the
footer and the assumption that status and help should all move to the top.

| Information | Default surface | Inspect/action destination |
|---|---|---|
| App identity | Header left | Existing app/workspace navigation |
| Workspace name | Header left only when it adds identification | Workspace overview/full path |
| Git branch / detached state | Header left, scoped to the relevant repository | Source Control / Git details |
| Active agent model | Header contextual group, only for an agent | Provider/model and request details |
| Agent working/waiting/thinking/stopping state | Header contextual group | Agent detail / Activity |
| Context occupancy and scoped cost | Header contextual group | Agent detail |
| Editor line/column and current mode | Compact `line:column` in the muted header contextual group | Editor detail |
| Editor modified/save/conflict state | Header contextual group; tab dirty marker retained | Existing safe save/reload/close flows |
| Current-file diagnostics | Header contextual group when nonzero | File diagnostics / Problems |
| Compact current-line Git blame | Header when loaded and width permits | Full blame data and copy action in editor details/context menu |
| Document line count, encoding, line endings | Inspector, only when actually known | Editor detail |
| Focused terminal state | Header contextual group | Terminal detail / focus action |
| Other/background agent and operation activity | Footer left, only when meaningful | Activity |
| Workspace diagnostic/IDE health issues | Footer left, scoped and nonzero | Workspace / Problems |
| Background requests needing user action | Footer protected attention entry | Existing owning question/permission form |
| Unread notifications | Footer right entry when nonzero | Notifications |
| Short success feedback | Source-local first; brief footer feedback if needed | Recent activity/notification record where appropriate |
| Contextual hints | Footer right, at most two ranked hints by default | Current-context Help |
| Full shortcut inventory | Help / palette / existing launcher onboarding | Registry-backed actions |
| App uptime and runtime diagnostics | Workspace/Application detail | Copy sanitized diagnostic summary |

### 3.1 Context Identity

Track these separately:

1. Active main item: selected document/agent tab, or launcher.
2. Focused working surface: may be a docked/floating terminal or a sidebar.
3. Focused input control: governs hints and local key semantics.
4. Inspected item: selection in Status Overview; never changes the workspace alone.

When a terminal owns input, explicitly label its contextual header rather than
showing editor cursor coordinates above PTY input. A transient popup retains its
bound resource for status but owns help. Sidebar focus changes help, not which
agent/document is considered the active main item. The model shown on an agent
comes from that agent/request, not global `App.ModelName`.

Opening inspection or Help captures the originating target. Moving focus into
the modal does not change its target or make Help describe only itself. Closing
returns to the previous live focus. Switching requires an explicit action.

## 4. Shell Design

### 4.1 Shared Alignment

Header and footer retain their existing one-content-row-plus-rule geometry.
Their text shares exactly the same left and right rails:

```text
contentLeft           = frame.Left + effectivePadLR
contentRightExclusive = terminalWidth - frame.Right - effectivePadLR
```

`effectivePadLR` is theme-derived and clamped once for the usable shell width.
Both bars consume that geometry, including hit targets and hints. Do not fix
this by adding a literal number of spaces to the footer's strings.

Separators span the shell's usable width inside the outer frame. Text is inset.
The last footer glyph must never extend beyond the header's right text rail.
Sidebar visibility, frame previews, theme changes, width calibration, and resize
must not change this relationship. Preserve footer height even when quiet; do
not make the document viewport jump as counters appear or disappear.

### 4.2 Header

The header has two groups: project/repository identity at the left and active
resource information at the right. These are semantic groups, not fixed thirds
of terminal width. Use shared spacing and complete-value overflow policies.

Examples below describe information content, not replacement window decoration:

```text
Launcher:  crude   Git main                       [no agent model or token fields]
Agent:     Crude   branch main    glm-5.3   ↑115k/1.05M 11%   ↓23.3k   cache 96.7%   ~$0.03
Editor:    crude   Git main              Modified   Ln 50, Col 1   2 warnings
Terminal:  crude   Git main                          Terminal 1   PTY running
```

The branch uses the existing theme glyph treatment in product, not a new icon
collection. Omit Git if there is no repository. Detached HEAD and multi-root
ownership must be explicit. Do not use a branch for the wrong file/repository or
run Git while painting. Workspace identity remains available in details; avoid
`crude ... crude` when the short project and app names are identical.

No generic `Ready` filler on the launcher. An agent's actual `Idle` state is
meaningful, but there is no need to repeat it in the footer. No model label on an
editor/launcher simply because an agent exists in the workspace.

The header contains no permanent shortcut list and no notification paragraphs.
It does not duplicate global fault, unread, or healthy-server summaries below.
An active item's own failure remains contextual; unrelated failures are global.

### 4.3 Footer

Use a quiet utility rail, not a second title bar:

- Left: meaningful LSP health, project Problems, Git changes/tracking, and
  background state; `Status` is only the quiet no-signal fallback.
- Right: unread Notices when present, zero to two contextual hints, and Help.
- In an idle healthy workspace, `Status` and `Help` may be the only text. That
  is intentional, not a loading state or a missing feature.

Suppress `0 active`, `0 idle`, `0 warnings`, and routine idle-agent counts. If
two other agents are working, say so. If the selected agent is the only work,
its header already says that; do not repeat `1 agent active` below it. Minimized
work still counts as background work and must remain discoverable.

Every visible state needs a useful destination. `Status` opens the overview at
Workspace; an agent-work count opens Activity; an LSP failure opens that server;
Notices opens Notifications. Avoid a generic click on the whole footer that
opens an unrelated view. Help is the final right-aligned entry.

Issue/read counters have different meanings. Deduplicate their compact display:
one failed save must not produce an error label, another IDE issue label, and
an extra attention total all describing the same event. Reading a notice does
not resolve the issue. A critical/pending state outranks hints and success text.

Short feedback such as `Saved` or `Copied` may occupy unused footer space without
changing its height, hiding an unresolved failure, or shifting a pressed control.
Long explanations belong in the originating form or inspector.

### 4.4 Hints: Select For Usefulness

Do not take the first one or two registry entries and call the result compact.
Define ranked hint metadata by actual focus and state. Default policy:

| Focus | Default compact behavior |
|---|---|
| Launcher | No duplicate of its already-visible onboarding shortcuts; Help remains |
| File/list navigation | No routine arrows/Enter/delete inventory |
| Idle editor | No permanent Save/Undo/Redo/Esc list |
| Idle composer | At most a non-obvious newline/composer affordance; not Enter-send plus history/attach/cancel all together |
| Running composer | The changed Enter-send-note meaning and safe cancel action may be useful |
| Selection | Copy/cut only when their availability or ownership needs explanation |
| Find/Replace | Mode-specific navigation or replacement action not already evident in the focused control |
| Terminal input | No generic Esc-close or Ctrl+C-copy; preserve child input ownership |
| Question/permission/restore conflict | Exact decision semantics retained; safety hints are not classified as obvious clutter |
| Inspector | A genuinely useful scope/back/resize hint only when relevant; not a permanent full navigation legend |

Compact is the default. It uses available footer capacity for a small ranked
navigation set (for example Find, Next Tab, and Focus File Tree in an editor)
without restoring routine Save/Undo/Redo clutter. Expanded is an explicit preference/help surface, not what
the user must disable to get a clean interface. Off hides automatic hints, not
Help or safety instructions. Full Help is searchable, registry-backed and opens
at the captured current context. The footer must resolve action-backed as well
as explicit-chord hints; `ActionID` entries cannot silently vanish.

### 4.5 Width, Overflow, And Visual State

Preserve complete semantic values. Do not split percentages, currency amounts,
key chords, or owners in the middle. Shortening order is deliberate:

1. Remove routine hints and optional workspace/model labels.
2. Shorten occupancy to percentage, branch/path to an unambiguous short form.
3. Move low-priority metrics to inspection.
4. Collapse global attention to one actionable overflow when necessary.

Keep required decisions, active resource state, and fault discovery reachable.
The two bars have independent budgets; a long path cannot consume the entire
help/status budget. Do not auto-cycle metrics or rotate error messages.

Use muted supporting labels and telemetry; the agent header never uses the
bright content foreground or lifecycle text. `↑` always means the latest
provider-reported input count over that request's runtime context capacity; the
denominator is runtime/catalog configuration, not a number returned in token
usage. The report stays unchanged until the next usage event. Serialized bytes,
report timestamp and lifecycle are inspectable in agent/tab details and Activity,
not alternative units for the same header field. Decisions/failures retain their
dedicated forms and workspace attention routes.
Accent is for selected/focused navigation and active progress, warning/error
colors are semantic, and the shared activity dot remains the only animation.
Hover, active selection, keyboard focus, read state, and severity are not the
same state. Clickable regions use existing interaction treatment and match
layout geometry, including the first click before a subsequent repaint.

## 5. Status Overview Design

Retain the Settings-style floating window and existing bracket buttons. Do not
replace them with web-style pills/cards or add a separate popup framework.

### 5.1 Explicit Vertical Regions

Lay out these regions separately, with no overlapping row ownership:

1. Existing WindowFrame title/close chrome.
2. One mode tab row: Tabs, Workspace, Activity, Notifications.
3. A continuous muted baseline below the mode row, with restrained active-tab accent.
4. Master/detail body, separated from tabs by one content inset row where space allows.
5. One command separator row below the body.
6. One command row, plus the existing frame's bottom breathing room.

The first mode label starts on the same content rail as the left search field.
Remove the extra first-item leading inset, not the window's outer padding.
Subsequent labels have a consistent small gap; no justification across the full
window width, oversized selected rectangle, or stack of separators after every
word. The baseline is the primary separation from the body. Active text is bold
and readable with the existing theme treatment; hover/focus remain distinct.

Implement edge alignment and baseline policy in the shared Tabs configuration
where appropriate, with regressions for its other consumers. Do not fake it with
a negative origin, a duplicate local painter, or mismatched hit coordinates.

### 5.2 Master/Detail Body

The left column is navigation. The right column is an inspector. Default to a
roughly one-quarter to one-third navigation width, not an equal split. Keep a
readable detail minimum rather than shrinking both sides to arbitrary widths.

- Search is inside the left column, aligned with its rows.
- Empty groups are omitted. A single group need not repeat its category heading.
- Main tabs follow their actual order, not alphabetical file paths. Terminals
  and minimized agents are grouped without duplicating a resource.
- Rows show title and one short state. Do not put all metrics in the list.
- The active workspace marker is separate from inspected-row selection.
- Details begin with one title/state heading and one provider/path subtitle.
  Do not repeat `Idle`, then `State: Idle`, then `Visibility: Visible`.

The divider is a one-cell stroke inside the shared 2-3-column pointer target.
It has visible hover/capture feedback. Pointer drag must resize live, preserve
capture outside the target/window, release correctly, and persist the chosen
split through updates and reopen. It stops at readable minima. Keyboard users
can focus the divider and nudge it without a conflicting text-control shortcut.
Offer a reset-to-default layout action without requiring a hidden gesture.

The acceptance is the actual window moving both pane geometries and painted
content, not merely a change to `SplitModel.FirstSize`. Investigate the current
WM/Host/capture/invalidation route before modifying shared drag math.

At narrow widths, use a retained list/detail drill-down once navigation and
readable detail minima cannot both fit. Reference minima for prototyping are
24 navigation columns and 48 detail columns plus the divider/frame/insets; the
80-column case therefore uses the compact drill-down, rather than squeezing the
current two columns into illegibility. At 100/160 columns, verify both panes.
Return/back is visible; selection, query, scroll and width preference survive
the transition. No invisible active detail should be marked read.

### 5.3 Command Row

Keep the current `[ Label ]` controls and variants. Use a real window-wide action
region below, not on, the separator:

```text
Left utility group                             Right action group
[ Copy summary ] [ More ]                      [ Switch to Tab ] [ Close ]
```

This is a placement diagram, not a new button design. More appears only when
additional actions exist. Copy is secondary; navigation is primary; Close is
secondary and rightmost. The command row never scrolls with details. On narrow
windows utility actions move into More before the primary action becomes clipped.
Do not grow an unbounded row of Stop/Restart/Read/Dismiss/Copy/Switch/Close buttons.

Actions reflect the selected target: Switch to Tab, Focus Terminal, Restore
Agent, or Return to Tab. When the target is already active, Return closes the
inspector and restores focus; it does not pretend to switch. A disappeared target
is marked no longer open, and its action is disabled with a reason.

Retain control instances and focused/pressed action identities across updates.
Do not recreate the ActionBar on every tick or selection repaint. The same row
must support keyboard traversal, pointer activation, disabled states, and long
labels without the separator crossing its controls.

### 5.4 Agent Details: Summary Before Accounting

Show context once, outside cumulative usage scope. Context is always tied to the
selected agent's latest relevant request, never a sum of child windows.

Recommended default detail composition:

```text
Main Agent                                      Idle
Ollama Cloud / glm-5.3

Context          71.5k / 1.05M (6.8%)

Usage scope      This Agent              Period  Session
Input            71.5k
Output           177
Cost             <$0.01 estimated
Requests         1

Cache and reasoning usage were not reported for this scope.
Usage details ...
```

Values above are illustrative. Compute them consistently from actual records;
do not divide by a rounded capacity label or truncate 6.8% to 6%. Compact header
percentages may round to a whole percent; inspection provides more precision and
exact raw numbers on copy/focus.

Use the existing Form cycle/select controls for scope and period, not another
horizontal tab strip inside the mode tab strip. Available scopes are This Agent,
With Children, Session, and Auxiliary as applicable. Show one table at a time.
If scopes are semantically identical because no other work exists, omit the
redundant choices or indicate why they are unavailable. Do not build three
identical tables separated by large blank gaps.

Supported cache statistics are useful: show cache hit and read/write quantities
when meaningful. Unknown optional fields collapse into one concise explanation
in the default summary. Expanded usage details retain every field with explicit
availability and provider semantics. A known zero remains zero there; no-data
explanation must not claim a provider never supports a field merely because a
particular scope lacks it.

Label/value spacing follows content, not an arbitrary third of the detail width.
Use aligned numeric values and compact section spacing. Long explanations, errors,
paths and model identifiers wrap in a scrollable document-like detail region;
they are selectable/copyable rather than permanently ellipsized. Copy summary
includes exact values, scope, units and availability, not only rounded labels.

### 5.5 Other Views

| View | Default detail | Actions and progressive detail |
|---|---|---|
| Editor | Path, modified/save/conflict state, cursor, file diagnostics | Open/focus, safe save/reload flows, blame and document metadata |
| Terminal | Title, live/exited state, exit code, placement | Focus/dock/float links; known CWD/PID/grid in details |
| Workspace | Specific service state or reported Problems | Scoped async LSP controls; navigate to existing Git/history/memory/settings surfaces |
| Activity | Owner, operation, phase, elapsed, real progress | Safe cancel, output, completed result; no fake progress or unsafe replay |
| Notifications | Explanation, source, time, resolution | Read/unread/dismiss and supported owner actions; repeated failures coalesce |

Do not make every detail look like an agent metric table. A diagnostic needs its
message and location; a failed command needs its reason and output; a notification
needs an explanation and recovery action. The shared layout stays consistent,
but the content is designed for the question the user is answering.

### 5.6 Input And Retention Contract

| Input/state | Required result |
|---|---|
| Click master row / Up/Down | Inspect without switching the workspace |
| Hover | Hover feedback only; never replace details or acknowledge a notice |
| Enter/Right from master list | Enter the selected detail, not implicit switching |
| Tab/Shift+Tab | Reach every mode/search/list/divider/detail/action control in a stable order |
| Button Enter/Space / click | Perform that enabled, identity-bound action once |
| Wheel | Scroll pane under pointer; selection does not follow wheel accidentally |
| Home/End/Page keys | Operate the focused list/detail, not the background editor |
| Esc in search | Clear nonempty filter first |
| Esc in drill-down/child surface | Back or close child; do not skip the owning focus level |
| Esc at overview root / Close | Close and restore the original live focus |
| Ctrl+C / text selection | Copy actual selected detail text through existing async clipboard ownership |
| Stream/tick/health update | Preserve inspected target, focus, pressed action, split and scroll |
| Target closes | Show no-longer-open state and disable invalid actions; never switch to an adjacent ID |
| Workspace switch | Invalidate the old project target and queued actions/results |

Opening the modal is user-initiated. Background work continues. It must never
bypass permission, question, dirty-close or restore ownership. Read-only inspection
during a blocking form requires an explicit safe routing policy; otherwise the
entry is unavailable rather than a hidden path around the form.

## 6. Information Correctness And Ownership

The visual revision does not abandon the original overhaul's domain requirements.
Their implementation must be completed and verified rather than assumed finished.

### 6.1 Accounting

- One request-ledger authority, stable workspace/session/agent/turn/request identity,
  idempotent revisions, and explicit retry attempts.
- No aggregate-only compatibility fallback or fabricated historical attribution.
- Input total versus uncached input is explicit. Normalize providers before
  computing a cache-hit ratio; use weighted totals over compatible scopes.
- Reasoning availability/inclusion in output is explicit; text duration is not tokens.
- Costs distinguish known, estimated, partial, justified upper bound, unavailable,
  and explicitly free/included pricing. Catalog pricing is not an invoice.
- Last Turn includes all relevant requests/tool continuations, not just the last
  HTTP response. Model changes retain historical request/pricing identity.
- Auxiliary work is separately attributed and included once in session/workspace
  totals. Per-agent summaries cannot silently inherit the full session amount.
- Context occupancy/capacity stays paired with its request and runtime limits.
  Draft estimates and stale measurements are labeled; child contexts are not added.
- Cache summaries by source version. Painting and tab selection never rescan all
  request records or write registry databases.
- Verify root/child/auxiliary ownership through create, stream, retry, cancellation,
  completion, minimize, restore, resume, shutdown and workspace switch.

### 6.2 Lifecycle And Operations

Preserve preparing, uploading, waiting for provider, actual reasoning, response,
tool work, child wait, approval/question, retry backoff, stopping, failed,
cancelled and completed states. Minimized/read-only/unsafe are separate attributes.
One authoritative lifecycle owner feeds all surfaces. A renamed setter facade
must not compete with typed event projections.

Operations include workspace/session opening, context compaction, provider work,
file save/reload/mutation, search/replace, Git, LSP lifecycle, watcher reconciliation,
history capture/restore/verify, memory maintenance, and background commands/PTY
lifecycle. Publish loading before expensive work, run work asynchronously, and
reject stale results. Cancelling remains visible until acknowledged. A restore
cannot pretend to cancel in an unsafe transactional stage.

Terminal liveness does not imply shell idleness or a command name. Use only
observed CWD/exit/bell/process facts. Docking/floating must not change process
identity, attention or usage. PTY key ownership remains intact.

### 6.3 LSP And Workspace Health

Distinguish current-file Problems, reported project diagnostics, and infrastructure
failure. One connected server cannot hide another's failure. Report each server's
root, state, error and observable activity. Disabled/not-configured is not normally
a warning; absence of telemetry is not proof of health.

Project Problems must be actionable by file/location, searchable and filterable
by severity/source. Counts are derived at diagnostic mutation boundaries, not
full-cache scans on every frame. Server restart/stop work is asynchronous, scoped,
generation-safe and joined/cancelled correctly on shutdown/project replacement.

Git, history, memory, persistence, watchers and provider-auth failures use the same
scope and lifetime rules, but retain their actual domain owners and existing
action surfaces. Do not add background provider probes merely for a health badge.

### 6.4 Notifications And Issues

Notifications record events; issues represent unresolved conditions; operations
represent work. Read does not mean resolved, progress does not mean a new notice,
and approvals are not dismissible substitutes for their existing forms.

Use explicit source identity, sanitized text, stable failure-episode deduplication,
timestamps, recurrence, resolution, read/dismissed state, and capability-based
actions. Only actually displayed detail acknowledges an unread notice, including
the narrow-layout case. A new failure episode after recovery can create a new notice.

Routine success is quiet; failures remain inspectable after transient presentation.
Do not suppress an unrelated save error while an agent is working. Defer a
transient that would obscure an active modal/selection/capture; retain discovery
immediately. No automatic modal, focus theft, desktop notification, or sound.

Bound memory, pending persistence and historical retention. Proposed routine
history ceiling is 1,000 notices / 30 days per workspace, with deduplication and
explicit overflow for exceptional floods. Live issues have separate retention.
Persist no clipboard bodies, credentials, provider payloads or full command output.
Status-store failure has a bounded nonrecursive fallback. Old-workspace notices
must not execute their actions against a newly selected workspace.

### 6.5 Layering

| Owner | Responsibility |
|---|---|
| Provider/session | Normalize and persist authoritative request usage |
| Agent/turn owner | Lifecycle, generation, decision and cancellation ownership |
| Workspace services | Resource health, operations, issues, scoped durable notices |
| App | App-global configuration/health and relevant preferences |
| Neutral contracts | Stable identities, optional values, snapshots and action capabilities |
| TUI viewmodel | Resolve presentation context and route explicit actions |
| Shared components/screens | Retained input/focus/selection, geometry and painting |

No workspace imports of TUI, no callback cross-layer architecture, no UI-owned
persistence, and no second domain state machine named StatusManager. A snapshot
may be cached; it does not become permission to write the resource from two places.

## 7. Revised Execution Order

Do not resume another broad implementation sweep. Fix and accept coherent vertical
slices. Parallelize independent reads/tests if useful, not speculative rewrites.
No subagents. Every implementation slice includes its regression, real product
validation and short report before expanding into the next one.

### Gate A: Shell Composition

First implement the shared content rail, meaningful-count policy, and exact
header/footer responsibility table. Extract editor facts from hints; put branch,
line/column and current-file state in the header; remove global model/identity
duplication and obvious footer hints. Preserve footer geometry and safety states.

Accept actual launcher, idle/running agent, editor, terminal and failure views
at 160x40, 100x28, 80x24, and a constrained 60x18 case. Check empty and loaded
states, dark/light themes, frame insets, long paths and sidebars. No downstream
feature expansion while these still look or behave incorrectly.

### Gate B: Inspector Structure And Input

Correct the mode strip, shared gutters, separate action row, real divider capture,
pane scrolling and narrow drill-down. Retain controls across updates. Add actual
WM/Host tests and capture/replay of pointer drag and keyboard navigation.

Accept resizing during live updates, dragging outside the window, focus traversal,
selection retention, button clicks, inactive/active target distinction, and open/
close focus restoration. Do not certify resizing merely from a model-only unit test.

### Gate C: Inspector Content

Replace repeated tables with one context summary and selectable usage scope/period.
Omit empty categories, simplify identity/state, align labels/values, group unsupported
optional fields, and make details wrap/select/copy correctly. Add designed fixtures
for editor, terminal, LSP Problems, operation failure and notification recovery.

Accept that ordinary single-agent details fit usefully without scrolling through
duplicates, while full exact accounting remains available and truthful. Verify
multiple children/auxiliary work and supported/unsupported/partial provider data.

### Gate D: Domain Completion And Lifecycle

Review the existing partial accounting, status adapter, service and persistence
code. Finish the contracts in section 6, including missing producers and actions.
Delete empty compatibility setters and competing writers instead of routing more
events through them. Test corrected async ownership before claiming a phase done.

Complete per-server Problems/actions, operation lifetime, bounded notifications,
focus-aware full Help, settings inheritance, and safe project/session transitions.
Do not introduce a new billing dashboard, log viewer, scheduler, or shell-integration
project as incidental scope.

### Gate E: Coherence And Release Validation

Trace every old responsibility to its replacement; remove obsolete paths/tests
only after moving genuinely shared helpers and guarding the new user outcome.
Audit broad mechanical renames and stub methods. No legacy-preservation branch.

Run full build/tests/lint/design/docs/benchmark gates, affected race paths, relevant
cross-platform compilation, and real visual acceptance. Update AGENTS, surface
catalog, interaction matrix, settings and performance references to describe the
accepted implementation, not an optimistic milestone.

A gate passes only after its evidence exists. Do not mark the request complete
with known failures, incomplete actions, an aborted benchmark gate, or placeholder
UI. If a gate is blocked, report it precisely and keep the workstream partial.

## 8. Acceptance Checklist

| Scenario | Required observable result |
|---|---|
| Launcher, no agents | No model/usage, duplicated app/project label, zero-active or idle-count filler |
| Header/footer rails | Exact same first and last content columns; full-width rules remain correct |
| Idle single agent | Model/state/context/cost once above; no duplicate model or zero-active line below |
| Editor | Branch and line/column/status above; no blame/hash/shortcut inventory below |
| Background activity | Nonzero useful count, minimized work included, selected work not redundantly announced |
| Useful hints | At most two ranked compact hints; no duplicate launcher arrows/Enter/delete legend |
| Fault or decision | Remains visible/discoverable before hints and success text; no duplicate attention counts |
| Overview mode row | First label aligns with search/list rail; baseline clearly separates body |
| Overview actions | Rule never crosses buttons; utility/primary/close placement stable and reachable |
| Divider | Real pointer/keyboard input changes both pane geometry and paint, including outside release |
| View refresh | No recreated focused/pressed controls, lost selection, reset scroll or split |
| Single-agent usage | One context and one selected table; no repeated identical scopes |
| Unsupported usage | Concise explanation by default, exact availability retained in expanded detail |
| Percent/currency | Computed from unrounded values, consistent precision, no unknown-as-zero or tiny-cost-as-zero |
| Scope changes | Correct agent/subtree/session/turn attribution; context never sums child windows |
| Details | Full errors/paths selectable and copyable; not permanently hidden by ellipses |
| Target closes/switches workspace | No stale action targets replacement resource; correct unavailable state |
| Multi-LSP | One healthy server does not hide failure; scoped action affects only intended server |
| Terminal | PTY keys, bell, dock/float/exit identity and focus remain correct |
| Notifications | Read/resolved/dismissed distinct; current-target inspection does not clear unrelated notices |
| Flood/shutdown/resume | Bounded queues/history, joined owned jobs, correct accounting and no stale callbacks |
| Small terminal/Unicode/frame | Useful priority overflow, accurate hit geometry, no clipped command row or half glyph |
| Dense loaded workloads | No per-frame request/diagnostic/transcript scans or layout-driven I/O |

Validation commands after implementation:

```bash
go build ./...
go test ./...
golangci-lint run ./...
make docs-check
make design-audit
make bench-gate
```

Run affected-package race tests and platform coverage as appropriate. Benchmark
mutation and presented-frame latency separately using dense resumed chats, many
agents/tabs, 10k diagnostics, 1k notices and mixed live streams. Keep existing
ceilings; measure before changing them. Keep idle cadence at least 2 FPS and
target 16 ms interactive frames without dropping mutations.

Use `scripts/tuishot.sh` for actual desktop/narrow acceptance and close every
capture process. Review visuals before updating goldens. Live-provider tests are
opt-in/isolated; never send an archived conversation to test status rendering.

## 9. References

### Accounting Correction (2026-09-11)

Read-only reconciliation of session `2026-09-10_162217_9bc04c77` found 82 ledger
rows with 106,756 output tokens versus 92 completed-response log entries with
147,949 output tokens. Nine persisted rows had reused request keys and later
timestamps. Runtime generation/step IDs restarted after resume and replaced old
requests; uint64 generations were also silently parsed as zero.

New request identities are independent of runtime counters. The durable/live
decoder is shared, revisions are ordered, summaries reject stale cache publication,
and child/error events cannot overwrite another agent's latest context. OpenAI,
Responses/Codex, Anthropic and Ollama now have protocol-specific presence tests and
end-to-end local HTTP tests across resume. Responses reasoning is retained, missing
optional fields are not declared zero, and Anthropic cache categories are included
in input exactly once. Native Ollama does not acquire invented cache/reasoning data.

This prevents new loss; it does not silently reconstruct already-overwritten local
records. The original user databases were not changed. Read-only diagnosis remains
available via `CRUDE_TEST_USAGE_ARCHIVE=<session.db> go test ./sessiondb -run
'^TestUsageArchiveReconciliation$' -v`. No archived content is transmitted.

- [Workspace architecture](workspace-refactor-plan.md)
- [Component-system architecture](tui-component-system-plan.md)
- [Surface catalog](../reference/surface-catalog.md)
- [Interaction matrix](../reference/interaction-matrix.md)
- [Dialog design language](../reference/dialog-design-language.md)
- [Terminal width](../reference/terminal-width.md)
- [Settings layers](../reference/settings.md)
- [Performance baselines](../reference/performance-baselines.md)
- [VS Code status guidance](https://code.visualstudio.com/api/ux-guidelines/status-bar)
- [VS Code notification guidance](https://code.visualstudio.com/api/ux-guidelines/notifications)

External guidance informs prioritization and notification restraint, not a new
visual language. Crude's existing frame, glyph, button, hover, focus, scrollbar,
tab and form primitives remain the implementation foundation.
