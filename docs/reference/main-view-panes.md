# Main View Panes

> Status: Current reference. Main-view layout, interaction, and ownership contract.

Main views can share the content area in recursive tab groups. This is an
in-memory layout for the current workspace in the current process, not a saved
split layout: restarting does not restore the tree, group selection or ratios.
Workspace switching tears down the old main-pane projections and resets the
window groups; it does not retain a separate warm layout for every visited root.
Session persistence does not imply pane-layout persistence.

## Ownership and identity

| Owner | Responsibility |
|-------|----------------|
| `tui/window/window.go`, `tui/window/groups.go` | One resource-tab identity, group membership/order, selected tab per group, focused group, recursive `PaneNode` topology |
| `tui/viewmodel/main_panes.go` | Retained group controls and per-tab views, parent-local geometry, compact presentation, session-family chrome placement |
| `tui/viewmodel/main_pane_actions.go` | Input policy, group activation, split/move/merge/resize commands, drop targets and preview, owner-bound file picker |
| `tui/viewmodel/floating_panes.go` | Pane windows: one WM window per floating group, window/group focus agreement, host resolution for focus and geometry |
| `tui/viewmodel/panel_placement.go` | Workspace surfaces (Git, Problems, Notices, Workspace, Memory, Board) placed in the dock, a main tab or a pane window |
| `tui/ui/action_registry.go` | Shared shortcut and palette metadata |
| `ui.Host`, `ui.SplitModel`, `ui.SplitDivider`, `ui.Tabs` | Shared focus/capture, split sizing, divider manipulation, strip navigation/overflow and drag gestures |

Moving a tab moves the existing identity, never copies a file, agent or session
into a second tab. Opening an already-open resource selects its existing tab;
the pane-specific Open File route can move that tab into its requesting group.
Each leaf retains its own selected tab. Selecting a tab focuses its group;
ordinary tab cycling stays within that group. Group cycling follows recursive
first-to-second visual order. Removing an empty group collapses its parent and
preserves the sibling subtree and sizing. Moving its last tab out also removes
the empty source group. Merge All keeps the focused group's identity and
selection and moves the other tabs into it without closing their resources.

## Composition and focus

The root shell mounts `mainPaneLayout` under its one `ui.Host` and paints into
the main shell surface. Each visible group mounts its own `shelltabs` strip and
selected editor/chat/empty body. Recursive branches use shared split models:
`ui.Horizontal` divides width (side by side); `ui.Vertical` divides height
(stacked). Splitting either leaf permits mixed, nested layouts.

Editor and main-agent rendering is retained independently per tab, not rebuilt
by mutating one global screen while visiting other groups. Each main-agent view
has an owner-scoped inline-subpane embedding registry; embedded thinking/child
geometry and scroll state must not overwrite another visible main chat's
projection. The viewmodel's focused-screen aliases exist for existing keyboard
and action consumers, not as a second selection or layout authority. Preparing
an unfocused pane must not activate its editor/session.

A root and its descendants share one composer and one retained session deck,
not duplicate drafts or decision panels. The focused eligible family member
owns the composer; otherwise the first eligible visible family member does.
Completed review agents do not acquire a composer. The deck prefers the focused
family member, otherwise the first visible family member. Switching focus
rebinds before pointer dispatch so a click cannot edit another pane's draft.
The global header reports the focused group's resource telemetry and adds
`Pane i/n` (and `compact` when applicable), clickable to select any group;
it is not a sum of neighboring
editor/chat facts. Workspace status remains workspace-owned.

## Pointer interaction

- Drag a tab into a body's left/right/top/bottom edge to split that target
  group and move the tab into the new leaf. The preview uses theme
  `ColorHover`, shared split rounding and a reserved divider. It changes no
  topology or membership until release.
- Drop into the body center to move into the existing group. Drop onto a tab
  strip to insert before the hit tab, or append when no tab is hit. This also
  supports reordering within a group; a center drop is a tab move, not Merge All.
- Esc cancels the preview and captured drag. Release outside a valid main-pane
  target, or over a floating window, leaves membership/topology unchanged.
  A sole tab cannot edge-split its own group through a drag.
- Group dividers are `ui.SplitDivider` controls over the topology's
  `ui.SplitModel`, captured by the root Host. Motion/release remain owned when
  the pointer leaves the divider; there is no second divider-drag service.
- Every group has a native **+** New session action immediately after its
  content-sized labels. With overflow it remains pinned at the trailing edge;
  the shared baseline spans the group, including the action. It creates a
  session in that group, not a new empty split. The bold accent glyph has a
  five-column label-row target (clamped only on tiny grids). Pointer hover
  paints no highlight: the strip is two rows of cells, so any fill either falls
  short of the bar's bottom rule or hangs past it. Keyboard focus still fills
  the label band with the accent chip. `ui.Tabs` alone paints the continuous
  baseline beneath the action; that row does not activate **+**.

## Keyboard and palette routes

| Operation | Route |
|-----------|-------|
| Split focused group right / down, initially empty | Ctrl+Alt+L / Ctrl+Alt+D |
| Arrow aliases for right / down split | Ctrl+Alt+Right / Ctrl+Alt+Down; the OS may intercept these, so letter chords are the displayed defaults |
| Previous / next group | Alt+[ / Alt+] |
| Previous / next tab in focused group | Ctrl+[ / Ctrl+] |
| Grow / shrink focused group's containing split | Ctrl+Alt+= / Ctrl+Alt+- |
| New session in focused group | Ctrl+Alt+N; group **+**; Ctrl+T then N or + |
| Empty pane New Session / Open File | N / O; Tab or Shift+Tab chooses, Enter/Space activates |
| Close an empty group | Ctrl+W, or palette Close Empty Pane; never closes a populated group through this operation |
| Move active tab to new right/lower split, next/previous group; Merge All | Command palette; tab context menu includes split, move and merge operations |
| Reorder selected tab inside its group | Palette Move Tab Left/Right in Group |
| Open File in Pane and all pane operations above | Ctrl+Alt+P Command Palette, registry-backed labels |

An empty pane uses native New Session/Open File buttons. The final unsplit,
tabless main area retains workspace home rather than introducing a second
launcher. Editor closes retain the dirty-safe Save/Don't Save/Cancel path.
Closing an agent tab minimizes it, not terminates its orchestrator; explicit
Terminate remains a separate action.

## Small grids and modal boundaries

Leaves require 24 columns and 7 rows; a new side-by-side split needs 49 columns,
and a stacked split needs 15 rows. Commands refuse an undersized split with an
actionable notice. If the terminal becomes too small for the existing recursive
minimums, the tree and selections remain intact while only the focused group
is shown. Alt+[ / Alt+] or the header's Pane menu visits the other groups; enlargement restores the
layout rather than rebuilding it.

Existing popup and blocking-deck policy precedes pane input. Settings
remains Ignore/consume outside clicks, not click-through to an underlying pane.
Terminal-owned keys remain PTY input. Pane commands do not bypass a modal
decision. Pane windows (below) are main content, not popups: `focusedPopup()`
excludes them, so they never take the `FocusFloatingWindow` route.

## Pane windows

Any tab (file, agent, workspace surface, Git review, local comparison) can live
in its own movable, resizable window. A pane window is a **floating group** of
`window.Window`: the same tab identity, editor, session and selection move into
it, and the same viewmodel policy drives it. There is no second editor, chat or
composer path.

- `Window.DetachTab` moves a tab into a new floating leaf; `DockGroup` merges a
  floating group back into `DockedGroup()` (the focused docked group, else the
  docked group focused before a window took focus). Floating leaves never join
  the split tree: they cannot be split, `GroupIDs()` stays the docked visual
  order, `AllGroupIDs()` appends windows, and Merge All merges only the docked
  layout. A window whose last tab closes disappears.
- `syncPaneWindows` keeps one WM window per floating group and lays it out every
  frame before focused-view geometry is derived. The frame body mounts the
  group's `mainPaneGroup` through the same `configureGroup` docked groups use;
  group rects (`body`, `editor`, `chat`, `input`, `deck`) are screen
  coordinates, so popovers, hit policy and composer ownership need no special
  case. A component is mounted in exactly one host; `hostFor`, `mainHost` and
  `screenRectOf` resolve focus, key dispatch and geometry to the root host or
  the window's frame host.
- Focus: `syncPaneWindowFocus` keeps Window's focused group and the WM's focused
  window in agreement. Code that focuses a window group raises its window; a
  user who raises a window moves the focused group into it; leaving every
  window (a click outside all windows) returns the focused group to the docked
  one. A popup opened from the window (find, completion, dirty prompt) keeps
  the group. A press inside window content activates the group like a click
  into a docked pane; drags captured by a window host follow the pointer until
  release.
- Dragging a tab out of a window's strip onto the main area moves it there
  (edge, center or strip, as for docked drags); a drop inside any window is
  not a target. Window hosts report pointer geometry in window coordinates;
  `windowScreenAction` converts it before the shared drop logic runs.
- The title bar's **⇲** returns the tabs to the main area; a window holding only
  a workspace surface returns it to the bottom dock. The window **✕** also
  returns tabs to the main area and never closes them. Esc is never a window
  close for pane windows; it belongs to the editor or composer.
- Entry points: tab context menu **Open in Window** (in a window: **Move to Main
  Area**), file tree **Open in window**, Agents panel **Open in window**, the
  **⇱ window** chip on a "Started agent" transcript row, the dock header **⇱**,
  and palette **Open Tab in Window** / **Move Window to Main Area**. A
  core-backed document opens asynchronously; the window intent travels with the
  open request and the landed tab floats.
- Pane windows open without the popup reveal animation: their content geometry
  must be exact from the first frame. Windows are process-local like the split
  layout and are not restored on restart.

## Agent tab close is minimize, not terminate

Closing an agent tab **minimizes** it: the shell's `AgentView` hides it, the window
tab goes away, and nothing else changes. The orchestrator keeps streaming,
the conversation, panes, chat projections, scroll offsets, and session
ownership are all retained. A stray click must never destroy work in flight.

- `App.MinimizeAgent` / `RestoreAgent` change only that shell's exact-node
  visibility map; `AgentView.IsHidden()` also checks
  ancestors. Minimizing a root hides its subtree without overwriting archived
  child choices. Restoring it returns previously open children, while completed
  review children remain closed. Explicitly opening a nested child reveals its
  ancestor path and leaves archived siblings closed.
- `AgentTabViews()` filters effectively hidden nodes, so `syncAgentWindowTabs` cannot
  resurrect a minimized tab, and `purgeStaleAgentTabs` closes one that
  lingers.
- Initial tabs for each session put its main agent before visible descendants.
  Background sync preserves deliberate user order and group selections; new
  children belong with their parent rather than another focused empty pane.
  Resumed completed children are
  retained in the Agents panel as closed review surfaces, never mass-opened.
- **`AgentMgr.Count()` vs `AgentView.VisibleCount()`**: shell gates choosing between
  launcher / chat / editor must use the latter, otherwise a workspace
  whose agents are all minimized shows an empty chat instead of the
  launcher. `Count()` stays the true total for the aggregate status report and
  the Agents rail badge. The footer remains a quiet system-level utility rail;
  provider waits and tool-specific lifecycle detail live in the active header
  and chat timeline.
- Restoring: the Agents panel lists minimized agents with a `minimized`
  detail; activating a row calls `restoreAgent`. `switchToAgentView` unhides
  on its own so every path back works.
- Terminating is explicit: `terminateAgent` (tab and Agents-panel context
  menus) still cancels the turn, stops the orchestrator, and drops state.
  `cleanupAgent` remains the destructive path; do not wire it to tab close.
- Guarded by `tui/viewmodel/agent_minimize_test.go`.

## New agents surface a tab immediately

Sub-agents start **minimized**: a child never opens a tab on its own. Its
"Started agent" transcript row carries **⧉ open** (main tab) and **⇱ window**
(pane window) chips, and the Agents panel lists it with Open / Open in window
actions. The rest of this section concerns root agents and explicit opens.

Creating or restoring an agent **must add its window tab and make it the
painted main view even while editor tabs are open**. A stray early return
in `syncAgentWindowTabs` when `EditorState.ActiveView != ""` used to skip
`OpenTab` entirely: New Agent created the session in `AgentMgr`, but the
tab bar stayed on the editors until every editor tab was closed and the
next tick finally synced.

- `syncAgentWindowTabs` always creates/updates/purges **visible** agent
  tabs, including while an editor is showing. Minimized agents still get
  no tab (`AgentTabViews` filters `Hidden`).
- Tick and other background callers must not steal the editor: `OpenTab`
  activates the new tab, so when an editor is the painted view the sync
  restores that editor as the window's active tab **without**
  `handleTabSwitch` (which would clear `ActiveView`).
- Explicit user actions — New Agent / `launcherNewSession` /
  `AddOrchestrator` / restore / `switchToAgentView` — call
  `activateAgentWindowTab` after sync so the agent becomes the selected
  tab and `handleTabSwitch` clears `ActiveView`. Do not call that helper
  from the tick path.
- Guarded by `tui/viewmodel/agent_new_session_focus_test.go`.

## Git and local comparisons

The Git **Changes** review (every changed file, or one commit, stacked in one
`gitdiff.Document`) and local comparison tabs move/split through the same main
groups as documents and agents. Git keeps its exact reviewed-byte stage/unstage
policy and its shown-set commit check. Local **Compare With Disk** now opens a complete main-tab comparison,
not the floating one-hunk inspector. **Compare With Saved Version** compares the
accepted buffer savepoint against unsaved text. Both are available in the editor
context menu and palette; the small HEAD-relative gutter inspector remains a
separate, deliberately scoped operation.

`screens/diffview` is the shared read-only reader: aligned old/new line numbers,
full-row themed addition/deletion bands, shared editor syntax palette, draggable
native split divider, one synchronized vertical scroll model and independent
horizontal scrollbars. Unified presentation preserves complete context; below
60 columns it replaces side-by-side temporarily without losing split preference
or ratio. Ctrl+F, Enter/F3/Shift+F3 and Alt+Up/Down find text and visit changes.
Search highlights exact grapheme-aligned occurrences and reveals horizontal
targets. Selection/copy excludes painted line numbers and absent alignment rows;
full-side copies retain source tabs, CRLF and missing final newline.

`local_diff.go` captures source bytes/savepoint on the UI owner and prepares the
comparison off Update. Tabs are keyed by exact buffer lifetime plus baseline,
and commands fence tab owner, request and workspace generations. Late results do
not steal focus. Source changes mark retained evidence stale; only **Refresh**
replaces it. Closing a source retains its read-only snapshot but cannot refresh
or silently bind it to a replacement editor. Closing a diff never closes/saves
the source. Disk reads are regular-file-only and each version is capped at 64 MiB.
Derivation is off-UI but is not interruptible inside the pure diff/lexer call;
cancelled/stale results are rejected on return. Comparisons are process-local,
not persisted across restart. Local narrow chrome uses **More**, preserving all
actions rather than shrinking seven labels into unreadable fragments.

The reader does not provide writable merge, hunk staging or intraline
word-diff. Conflict resolution and remote operations live in the Git panel
(next section), not in the comparison reader.

See [interaction matrix](interaction-matrix.md),
[control usage](control-usage.md), and [surface catalog](surface-catalog.md).
Focused regression entry points include `tui/window/groups_test.go`,
`tui/ui/tabs_drag_test.go`, `tui/screens/shelltabs/tabs_new_test.go`, and
`tui/viewmodel/main_pane_journey_test.go` / `pane_file_open_test.go`.
The document picker uses its own Open File intent, not the attachment picker
labels or multi-selection behavior. Preparation runs off Update; workspace,
group, manager and request identities fence installation. A newer live buffer
wins over prepared disk bytes. See the recorded measurements in
[performance baselines](performance-baselines.md).

## Rail, Git and Memory destinations

- Rail destinations are Files, Agents, Skills and MCP; Terminals and Git have no
  rail identity. Git is the `gitpanel` bottom-dock destination (footer pill,
  `Ctrl+Alt+G`); its multi-file `gitreview` main tab and the panel preview
  share one `gitdiff.Document` and one prepared change set
  (`prepareGitChanges`, re-derived only while a Git view is visible). Each
  review is its own main tab (`gitReviews`, keyed by tab, per-instance
  component IDs); never return to a single replaced review screen. Commit
  messages are drafted by the Aeron role via `Workspace.PrepareCommitMessageWriter`
  (configuration read on the update goroutine, streaming I/O in the background). Workspace Memory is a retained footer panel shared with
  `/memory`/palette, never a second floating overlay or session PanelDeck.
- Git comparisons are typed main tabs, prepared off the UI goroutine. Index
  mutations require reviewed bytes; sidebar context actions open review instead
  of bypassing it. Discard requires exact preview + mandatory Local History
  safety recovery and never deletes untracked data.
- Git panel sync and merge: the status row offers Fetch, Pull (`--no-rebase`),
  Push (never forced; publishes an upstream when missing), and during a merge
  Resolve n / Abort / Complete. A branch row's menu offers Switch and Merge into
  current. Unmerged paths come from `git diff --diff-filter=U`; the
  `gitmerge` resolver dialog chooses ours/theirs/both per conflict (diff3 and
  CRLF aware) and writes through `Workspace.ResolveGitConflict` with a Local
  History safety snapshot. Remote commands run with `GIT_TERMINAL_PROMPT=0`;
  an authentication failure is reported, never prompted on the TTY. Results
  carry their own sequence and are dropped when stale.
- The commit message **Edit…** button opens a dialog with the full message
  (Save / Commit / Cancel) and the repository's commit conventions.
- The Changes/History split collapses either side with the divider's ◂ ▸
  controls or `[` / `]`; collapse is shared `ui.SplitModel` state.
- Workspace surfaces live in the bottom dock by default. The dock header's **⧉**
  promotes the visible surface to an ordinary main tab (split, moved and closed
  like a file; closing returns it to the dock) and **⇱** opens it in a pane
  window; **⌄** hides the dock. The footer pill reveals the surface wherever it
  is placed instead of opening a second copy. A surface is mounted in exactly
  one place.
