# Interaction Matrix

Phase 0 baseline of keyboard/mouse contracts that the component system must
preserve or intentionally improve.

## Global

| Action | Binding (current) | Notes |
|--------|-------------------|-------|
| Command palette | Ctrl+Alt+P | Not Ctrl+P; tmux Shift issue |
| Find in Files | Ctrl+Alt+F | Not Ctrl+Shift+F under tmux 3.4 |
| Toggle sidebar | Ctrl+B | |
| Composer | Alt+C | |
| Quit/close | Ctrl+Q | |
| Cancel/back | Esc | Never Ctrl+C for back |
| Copy | Ctrl+C | |
| Paste | Bracketed paste primary; Ctrl+V fallback | |

## Focus precedence (current)

1. Floating window (WM)
2. Session Panel Deck (when focused)
3. Launcher
4. File tree
5. Tab bar
6. Editor
7. Chat input

Passive background-job panels update their owning session deck without taking
focus or displacing an active interactive panel. Failed and killed jobs request
tab attention until the panel is activated or closed.

## Click-outside policies (current)

| Behavior | Surfaces |
|----------|----------|
| CloseAndRoute | context menu, LSP popups, diagnostics |
| KeepAndRoute | find/replace, find-in-files, composer, palette, textoverlay |
| IgnoreAndConsume | dirty prompt, tree prompt, tree delete, filepicker, workspace settings |

## Collection navigation baseline

| Control | Up/Down | Page | Home/End | Wrap | Enter |
|---------|---------|------|----------|------|-------|
| components.List | yes | yes | no | wrap | select |
| Command palette | yes | yes | no | clamp | activate |
| File picker | yes | yes | yes | clamp | open |
| Find in Files | yes | yes | no | clamp | open |
| File tree | yes | custom | no | clamp | expand/open |
| Context menu | yes | no | no | wrap | activate |
| Launcher | yes | step | yes | clamp | activate |

## Known defects to fix during migration

1. TextPrompt cursor is byte-offset naive.
2. Form TextItem shift-selection incomplete.
3. FindInFiles reimplements input paint.
4. Multiple filter strings without selection/word motion/paste.
5. Scrollbar colors hard-coded outside theme.
6. Three chrome systems disagree on padding and close buttons.
7. Modal=true combined with CloseRoute on some popups (contradictory).
