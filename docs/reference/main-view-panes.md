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

Existing floating-window and blocking-deck policy precedes pane input. Settings
remains Ignore/consume outside clicks, not click-through to an underlying pane.
Terminal-owned keys remain PTY input. Pane commands do not bypass a modal
decision. Main-resource pop-out/floating-window composition remains deferred;
existing floating dialogs and terminals retain their own contracts.

## Git and local comparisons

Git Changes and local comparison tabs move/split through the same main groups
as documents and agents. Git keeps its exact reviewed-byte stage/unstage/commit
policy. Local **Compare With Disk** now opens a complete main-tab comparison,
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

The reader does not provide writable merge, hunk staging, conflict resolution,
intraline word-diff or remote Git operations. These are not implied by split
presentation.

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
