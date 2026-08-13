# Interaction Matrix

Phase 0 baseline of keyboard/mouse contracts that the component system must
preserve or intentionally improve.

## Global

| Action | Binding (current) | Notes |
|--------|-------------------|-------|
| Command palette | Ctrl+Alt+P | Not Ctrl+P; tmux Shift issue |
| Find in Files | Ctrl+Alt+F | Not Ctrl+Shift+F under tmux 3.4 |
| Replace in Files | Ctrl+Alt+H | Sibling of Find in Files; not Ctrl+Shift+H under tmux |
| Toggle sidebar | Ctrl+B | Hides/shows the sidebar body; the 3-col activity rail stays |
| Files / Agents / Terminals / Source Control | Ctrl+Shift+E / A / U / G | Activity destinations; rail and panel bodies expose destination-aware context menus |
| New terminal | Ctrl+Shift+` | Ghostty-pinned PTY; docks in the workspace terminal dock |
| Toggle terminal | Ctrl+` | Focus last terminal, or leave it if it already owns keys |
| Composer | Alt+C | |
| Quit/close | Ctrl+Q | |
| Cancel/back | Esc | Never Ctrl+C for back |
| Copy | Ctrl+C | |
| Paste | Bracketed paste primary; Ctrl+V fallback | |

## Editor context

| Action | Binding | Notes |
|--------|---------|-------|
| Move line/selection | Alt+Up / Alt+Down | One undo step each |
| Duplicate line/selection | Ctrl+Shift+D | Kitty/CSI-u terminals |
| Delete line(s) | Ctrl+Shift+K | Kitty/CSI-u terminals |
| Toggle line comment | Ctrl+/ (legacy Ctrl+_) | Prefix from language map / chroma |
| Indent / outdent selection | Tab / Shift+Tab | Shift+Tab outdents the current line without a selection |
| Smart Home | Home | Toggles first non-blank column and column 0 |
| Auto-close pairs | ( [ { " ' | Type-over closers; openers wrap an active selection; Backspace deletes an empty pair |

## File tree context

| Action | Binding | Notes |
|--------|---------|-------|
| Search files | click persistent search, `/`, or any printable char | Fuzzy subsequence; ancestors auto-expand; Esc clears; mouse focus and type-to-filter share one field |
| Refresh tree | Ctrl+R | Relocated from `r` (typing now filters) |
| Toggle hidden files | Ctrl+. | Relocated from `.` |

## Source Control panel
| Action | Binding / control | Notes |
|--------|-------------------|-------|
| Commit staged changes | Ctrl+Enter in commit field | Empty messages are rejected; refreshes snapshot after commit |
| Refresh repository | Ctrl+R / context menu | Status, branch tracking, history, and branch list refresh asynchronously |
| Stage / unstage | change context menu | Supports individual paths and stage/unstage all |
| Checkout local branch | Enter / branch context menu | Current branch is disabled |
| Discard tracked changes | change context menu | Opens a danger confirmation with Cancel focused; untracked files are never deleted |

## Terminal panel

| Action | Binding / control | Notes |
|--------|-------------------|-------|
| New / float / close | `+` `⇱` `✕` header icons | Panel header, right of the tabs (VS Code style) |
| Dock a floating terminal | `⇲` title-bar button | Same PTY, no restart |
| Scrollback | Wheel, Shift+PgUp/PgDn/Home/End | Primary screen only; typing snaps to bottom |
| Selection / copy | Drag; double-click word; triple-click line | Copy-on-release + Ctrl+Shift+C; Shift bypasses app mouse tracking |

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
| Launcher | yes | step | yes | clamp | activate; fixed search row; workspace viewport scrolls independently |

## Known defects to fix during migration

1. TextPrompt cursor is byte-offset naive.
2. Form TextItem shift-selection incomplete.
3. FindInFiles reimplements input paint. **FIXED**: native `tui/screens/findinfiles` uses `ui.SearchField` surface paint and debounce identity.
4. Multiple filter strings without selection/word motion/paste.
5. Scrollbar colors hard-coded outside theme.
6. Three chrome systems disagree on padding and close buttons.
7. Modal=true combined with CloseRoute on some popups (contradictory).
