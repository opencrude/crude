# Interaction Matrix

> Status: Current. Phase 0 baseline of keyboard/mouse contracts the component system must preserve.

Phase 0 baseline of keyboard/mouse contracts that the component system must
preserve or intentionally improve.

## Global

| Action | Binding (current) | Notes |
|--------|-------------------|-------|
| Command palette | Ctrl+Alt+P | Not Ctrl+P; tmux Shift issue |
| Find in Files | Ctrl+Alt+F | Not Ctrl+Shift+F under tmux 3.4 |
| Replace in Files | Ctrl+Alt+H | Sibling of Find in Files; not Ctrl+Shift+H under tmux |
| Toggle sidebar | Ctrl+B | Hides/shows the sidebar body; the 8-col activity rail (outer gutter plus padding before the panel left border) stays |
| Files / Agents / Terminals / Source Control | Ctrl+Shift+E / A / U / G | Activity destinations; rail and panel bodies expose destination-aware context menus |
| Application Settings | activity-bar gear | Bottom rail gear opens Application Settings (VS Code manage control); right-click also offers Workspace Settings |
| Rail counts | Upper/lower-right of destination icons | Normal-size bold digits keep the script-like placement without tiny Unicode glyphs: Agents and other destinations use the top row of their three-row band; Source Control uses the bottom row. Counts above nine use `+` at the same level. Badges sit diagonally beyond the measured icon, never widen the rail or overwrite the panel rule, and remain inside their destination's whole-band click/hover target. Zero counts disappear. |
| New terminal | Ctrl+Shift+` | Ghostty-pinned PTY; docks in the workspace terminal dock |
| Toggle terminal | Ctrl+` | Focus last terminal, or leave it if it already owns keys |
| Composer | Alt+C | |
| Quit application | Ctrl+Q | |
| Close active editor tab | Ctrl+W | Uses the dirty-close Save / Don't Save / Cancel prompt; terminal focus keeps the key as PTY input |
| Cancel/back | Esc | Never Ctrl+C for back; in editor focus, Back closes the tab after transient editor UI is dismissed. On a tool permission dock, Esc stops the turn without denying permission; Deny / `n` is the explicit rejection path. In a text control Esc first drops the selection; the chat composer's ladder is selection → draft → cancel the turn. |
| Previous / next tab | Ctrl+[ / Ctrl+] | Global outside terminal key ownership, including editor focus |
| Attach file / choose current folder | Ctrl+O | Opens Attach File from non-terminal shell views; a focused Open Workspace folder picker owns the same key locally and selects its current folder |
| Copy | Ctrl+C / Ctrl+Shift+C | Copies the selection of the chat input, chat messages, editor, overlay editors, terminals, and every inline text control. Ctrl+Shift+C is the explicit copy chord (terminals already used it); unhandled Ctrl+C clears the chat draft, and Ctrl+C never navigates back. |
| Cut | Ctrl+X | Text controls: cuts the selection, or the whole value when nothing is selected (masked fields refuse it) |
| Copy on release | drag release over a text control | Text areas and inline inputs copy the dragged selection and drop the highlight on release, matching the chat message area and docked terminals |
| Whole-text Copy/Cut | right-click on a text control | Context menu Copy/Cut act on the entire value; the composer menu (Paste / Copy / Cut / Clear input) acts on the draft. Cut runs through the owning control so composite models stay in sync. |
| Paste | Bracketed paste primary; Ctrl+V / Ctrl+Shift+V fallback | Routes to the focused control; launcher/tree paste never leaks into hidden chat input. Windows Terminal image-only clipboard pastes arrive as empty bracketed pastes: only the chat composer asynchronously reads and attaches the clipboard image. Non-empty text remains exact; an empty paste without an image is a silent no-op, never a read of unrelated clipboard text. |

## Chat and Workspace

| Action | Binding / control | Notes |
|--------|-------------------|-------|
| Browse prompt history | Up / Down at composer boundaries | Prefix-filtered recall preserves text and attachment snapshots; Down past the newest match restores the original draft. File I/O runs outside Update, with session and input-generation checks. |
| Search prompt history | `/history` | Searchable single-line summaries show time, prompt preview, and attachment count. Click or Enter expands full text and attachment details; **Use in composer** restores without sending. Missing media fails closed and leaves the draft intact. |
| Open attachment | Click its filename chip or a linked history attachment | File-backed media opens with the OS default application; inline text opens the native reference editor. History previews use session-owned copies, never writable history assets. |
| Reveal attachment | Attachment context menu > Show in file manager | Windows Explorer selection, macOS Finder reveal, or the containing folder in the Linux file manager. Paths are arguments, never shell commands. |
| Remove attachment | Separate three-cell `x` target on the chip | Detaches tokens from the draft without deleting references owned by sent messages. Overflow and wheel navigation keep hidden chips reachable. |
| Session account / model | `/settings` or `/model` | Account labels only, no template groups. Model discovery uses that account's resolved connection and cannot update another session's deck. |
| Session default agent | `/settings` > Default agent | Pick Main Agent, a built-in role, or an enabled named agent for future root turns. Its account stays pinned to this session, switching choices retains the other root settings, and disabled/removed selections fail closed. |
| Auto Review Agent | Application/Workspace Settings > Agents | Select an explicit reviewer account/model, edit bounded extra restrictions, and configure thinking, response format, timeout, retries, token growth/ceiling, denied/unresolved behavior, and request/recovery bounds on the Review tab. There is no review-count cap. Every exact non-hard-blocked call, including unknown commands, can be reviewed. Each root and child has an independent reviewer lifecycle. YOLO bypasses the owning session tree only. Unresolved can ask or safely deny for unattended continuation, never approve. Security > Auto Review Policy is a synchronized view of the same persisted policy. |
| Permission completion | Allow / Deny / Always / cancel | Closing a confirmation releases blocking focus and its deck/overlay only. It never clears or rewrites the existing composer text, attachment references, selection, undo state, or history position; only explicit send/clear actions consume a draft. |
| Permission presentation | Agent or child requests confirmation | Any painted member of the owning root family uses that family's retained dock. Concurrent family requests receive exact-owner tabs only while two or more exist; tab close denies that exact call. Leaving the family hides without discarding it, and returning restores selection. A background family, or a request received while an editor/file is painted, uses the FIFO global modal without switching tabs or sessions. Owner/subtree termination removes its active and queued requests. |
| Breadcrumb navigation | Click an individual ancestor label | Settings editors retain discard confirmation; session settings retain staged role edits. File-tree ancestors reveal their row, and file-picker ancestors navigate directories. Separators do not navigate. |
| Close Settings | Window × or footer Close/Cancel | Closes the entire Application/Workspace Settings window at every depth, never Back. Unsaved preferences and editor drafts require one explicit discard decision; Keep settings open preserves the current page and draft. Breadcrumbs/Back/Esc own navigation. |
| File-tree search and deep folders | Type while the tree is focused, or click its search field | Both paths edit the same retained filter and request the async path projection. Backspace on the final letter clears it; late results cannot restore the query. Every folder remains a row. Shared offscreen ancestry is compacted into a clickable breadcrumb; when visible rows reach another root branch, the normal top-row breadcrumb stays while absolute guides return. Narrow breadcrumbs keep the nearest folders visible. |
| Workspace details | Footer **Workspace** | Explicit Open Agents, Open Files, and Previous Sessions sections. Collapsed resources occupy one row; click, Enter, or Space toggles details; Right expands and Left collapses. Saved sessions expose an explicit Resume action inside their details. |
| Open Tabs details | Click header model / context telemetry | Same single-row disclosure pattern, restricted to visible tabs. Identity, current model, known call/cost totals and lifecycle share the row; less important facts yield first at narrow widths. Expanded details expose an Open action. |
| Terminate background command | Its row's Terminate button; compact close glyph on narrow grids | Only running rows expose the action. Click selects the exact command; Enter/Space activates a focused button, Up/Down or Tab moves between buttons, and Esc returns to the composer. Handle, PID, start time and owning session must still match. Finished/stale/replaced rows never signal a process, and the session and other jobs remain alive. |

Prompt history owns content-addressed media beside `history.json` with
owner-only permissions. Multimodal entries never merge by text similarity.
Restoration imports fresh session references and rewrites only the recorded
chip spans; literal bracketed prose is not treated as an attachment. Imported
media is session-owned, so pruning the bounded history cannot break a resumed
session. Individual binary reads are limited to 64 MiB. Capture failures are
reported and marked unavailable rather than silently recalling text-only
versions. Application shutdown flushes pending history before closing sessions.

Windows background-command termination uses the retained Job Object for both
pipe and ConPTY commands; normal completion cleans descendants before waiting
for inherited output pipes. Native acceptance remains a Windows-machine check:
`go test ./fileio -run 'TestWindows(KillRef|CompletedCommand|InteractiveCommands)|TestKillRef|TestSettledCommand' -count=1`.
These tests do not skip missing/broken process ownership: failures include
`TODO(Windows)` and the exact ownership, child-cleanup, unrelated-process or
ConPTY behavior to investigate. Linux cross-compilation is not a native pass.

## Editor context

Dirty state means an exact byte difference from the accepted disk savepoint,
not a different revision number. Manual reversal, undo/redo, and an in-flight
save acknowledgement all use the same immutable piece-span checkpoint. Restoring
saved bytes cancels pending autosave deadlines and closes without a save prompt.

The Git gutter uses an immutable HEAD baseline plus an exact incremental live
diff. Additions, modifications and deletion boundaries update on the edit, as
do change inspection and next/previous-change navigation. There is no speculative
local marker or separate settled representation. Background refreshes of the
same HEAD update metadata without replacing live hunks; branch/HEAD changes
establish a new baseline. Shared views and grouped undo beyond the edit-log
window derive from current bytes, not replayed change guesses.

| Action | Binding | Notes |
|--------|---------|-------|
| Move line/selection | Alt+Up / Alt+Down | One undo step each |
| Duplicate line/selection | Ctrl+Shift+D | Kitty/CSI-u terminals |
| Delete line(s) | Ctrl+Shift+K | Kitty/CSI-u terminals |
| Toggle line comment | Ctrl+/ (legacy Ctrl+_) | Prefix from language map / chroma |
| Indent / outdent selection | Tab / Shift+Tab | Shift+Tab outdents the current line without a selection |
| Smart Home | Home | Toggles first non-blank column and column 0 |
| Auto-close pairs | ( [ { " ' | Type-over closers; openers wrap an active selection; Backspace deletes an empty pair |
| Close tab / contextual back | Ctrl+W / Esc | Both use the dirty-safe close path; Ctrl+W is the explicit Close Tab command |
| Git line edits / blame | (gutter + header + inspector) | `+` added / `~` modified in the gutter; branch and compact current-line blame are in the header; full blame and Copy blame commit remain in editor detail/context actions |
| Horizontal scroll | Two-finger horizontal / wheel-left/right; Shift+vertical wheel fallback | Scrollbar extent is the widest line in the whole document and does not change with vertical viewport |
| Text cursor | Shared 400 ms phase after 800 ms activity hold | Muted reverse-video block; idle shell remains at 2.5 FPS so blink never freezes |

## File tree context

| Action | Binding | Notes |
|--------|---------|-------|
| Search files | click persistent search, `/`, or any printable char | Hint is `Search in <workspace>`; trailing badge shows aggregated git + diagnostics like the Agents count; fuzzy subsequence; ancestors auto-expand; Esc clears; mouse focus and type-to-filter share one field |
| Refresh tree | Ctrl+R | Relocated from `r` (typing now filters) |
| Toggle hidden files | Ctrl+. | Relocated from `.` |

## Source Control panel
| Action | Binding / control | Notes |
|--------|-------------------|-------|
| Commit staged changes | Ctrl+Enter in commit field | Empty messages are rejected; refreshes snapshot after commit |
| Refresh repository | Ctrl+R / context menu | Status, branch tracking, history, and branch list refresh asynchronously. The header's own Git identity also reconciles automatically after file and repository-metadata changes, so opening this panel is never required to see current state; an automatic reconcile never shows the loading state or raises a failure of its own |
| Stage / unstage | change context menu | Supports individual paths and stage/unstage all |
| Checkout local branch | Enter / branch context menu | Current branch is disabled |
| Discard tracked changes | change context menu | Opens a danger confirmation with Cancel focused; untracked files are never deleted |

## Agents panel

| Action | Binding / control | Notes |
|--------|-------------------|-------|
| New Agent | `+ New Agent` command row | Same single-click button pattern as New Terminal. Starts a session from the sidebar (also palette `agent.newSession` / launcher New Session via `launcherNewSession`). **Must add a window tab and make it the active/painted view immediately**, even when editor tabs are already open — closing those editors must never be required to see the new agent. Background `syncAgentWindowTabs` (tick) still creates missing agent tabs but must not steal the editor; only explicit user actions call `activateAgentWindowTab`. Guarded by `tui/viewmodel/agent_new_session_focus_test.go`. |

## Terminal panel

| Action | Binding / control | Notes |
|--------|-------------------|-------|
| New / float / close | `+` `⇱` `✕` header icons | Panel header, right of the tabs (VS Code style) |
| Select terminal instance | Compact numbered tabs; picker `≡` for overflow | Tab labels and terminal actions share one row; selecting retains the same PTY and focuses its body. All tabs use the shared baseline and active-tab scrolling. |
| Browse overflowing tabs | Horizontal trackpad/wheel, wheel up/down over strip, `‹` / `›` | Browsing never changes the active PTY or snaps back on repaint. Labels are `Terminal 1` / `Terminal 2`, adapting to `T 1` / `T 2` when cramped; picker numbers match and placement is named, never `0`. |
| Dock a floating terminal | `⇲` title-bar button | Same PTY, no restart |
| Scrollback | Wheel, Shift+PgUp/PgDn/Home/End | Primary screen only; typing snaps to bottom |
| Selection / copy | Drag; double-click word; triple-click line | Copy-on-release + Ctrl+Shift+C; Shift bypasses app mouse tracking |

Workspace, Problems, and Notices put search and More/Maximize/Hide on the same
header row. Memory and Board put their tabs or detail title beside those host
controls; browse search and local actions share a row. Narrow panels replace the
local buttons with a same-row More button; its action list preserves all commands
and restores the retained search/query on Back or Esc. Extremely constrained
slots retain a vertical action fallback rather than clipping buttons.

## Focus precedence (current)

1. Floating window (WM)
2. Blocking session Panel Deck
3. Focused workspace terminal dock
4. Launcher
5. File tree
6. Tab bar
7. Editor
8. Chat input

Passive background-job panels update their owning session deck without taking
focus or displacing an active interactive panel. Failed and killed jobs request
tab attention until the panel is activated or closed.

## Click-outside policies (current)

| Behavior | Surfaces |
|----------|----------|
| CloseAndRoute | context menu, LSP popups, diagnostics |
| KeepAndRoute | find/replace, find-in-files, replace-in-files, composer, palette, textoverlay, floating terminals |
| IgnoreAndConsume | dirty prompt, tree prompt, tree delete, filepicker, workspace settings |

## Collection navigation baseline

| Control | Up/Down | Page | Home/End | Wrap | Enter |
|---------|---------|------|----------|------|-------|
| Command palette | yes | yes | no | clamp | activate |
| File picker | yes | yes | yes | clamp | open |
| Find in Files | yes | yes | yes | clamp | open; Cc/.* sit on the right of the query; Cc click toggles case, .* click or Ctrl+R toggles regex |
| Replace in Files | yes | yes | yes | clamp | same search surface; Replace/All on the right of the replace row; Ctrl+Enter replaces all |
| File tree | yes | custom | no | clamp | expand/open |
| Context menu | yes | no | no | wrap | activate |
| Launcher | Up/Down | viewport | yes outside the search editor | clamp | open exact resource; Tab/Shift+Tab visit original action controls, Skills, MCP, search, resources and Retry; hover never redirects keyboard activation |

### Workspace home

- On wide grids, the original four-row New Session control (cycling sparkle,
  project path and Enter hint) spans the complete top section. Beneath it, the
  original folder-icon New Workspace control starts the left pane. There is no
  additional workspace heading or duplicate Settings, Help, palette or Sessions
  control. On cramped grids the two action controls stack with exactly one blank
  row between them. Retry belongs to the Recent workspaces heading row and never
  shortens the dedicated search row.
- Skills and MCP retain their named current resources and independent scrolling
  in the wide left pane. After one blank row below New Workspace, they divide all
  remaining height equally. Clicking a resource opens its exact source-qualified
  detail in the existing manager. The left pane, including both capability panels,
  is hidden in cramped mode.
- Recent workspaces occupy the right pane, never a full-width resource grid.
  Each name and path share a two-row hit target, with one quiet, non-interactive
  row between resources. The last resource has no trailing spacer. Search always
  owns a separate full-width row below the heading and remains fixed while
  scrolling. Its navigation guide is pinned to the bottom of the pane regardless
  of resource count. Below 72 available columns Recent workspaces takes the full
  content width beneath the stacked actions.
- Loading uses a thin `ProgressRule` ink sweep, not blinking dots or fake
  percentages. The last successful listing stays usable during refresh. Only
  the current owner/generation can settle loading; errors expose Retry.
- Right-click a workspace name or path offers Open, Copy path and Delete
  workspace. Delete opens an asynchronous review with Cancel focused. It moves
  saved workspace/session data into recovery storage; project files are never
  removed. Undo in the completed dialog refuses a later replacement workspace.
  Recovery location is recorded in Notices and an on-disk manifest; after the
  dialog closes, recovery is manual. The bare D key never deletes saved data.

## Known defects to fix during migration

1. TextPrompt cursor byte offsets. **FIXED**: prompts use shared grapheme-aware TextField/TextEdit geometry.
2. Form TextItem shift-selection. **FIXED**: shared TextField/TextArea own selection, drag capture, and shortcuts.
3. FindInFiles reimplements input paint. **FIXED**: native `tui/screens/findinfiles` uses `ui.SearchField` surface paint and debounce identity.
4. Multiple filter strings without selection/word motion/paste.
5. Scrollbar colors hard-coded outside theme.
6. Three chrome systems disagree on padding and close buttons.
7. Modal=true combined with CloseRoute on some popups (contradictory).
8. Blocking PanelDeck pointer shielding. **FIXED**: while a blocking panel is
   active, clicks/wheel/motion/releases outside its slot are consumed before
   root-Host hit testing. Deck-owned drags may still release outside; stale
   captures owned by underlying shell controls are canceled. Guarded by
   `TestPanelDeckViewmodelContractTable/blocking_panel_shields_pointer_outside_deck_rect`.
