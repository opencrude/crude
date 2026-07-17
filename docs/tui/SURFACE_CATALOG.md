# TUI Surface Catalog

Status: Phase 0 evidence freeze  
Canonical plan: [`TUI_COMPONENT_SYSTEM_PLAN.md`](../../TUI_COMPONENT_SYSTEM_PLAN.md)

Every user-visible surface must appear here. Migration phases claim completeness
only against this catalog.

## Persistent shell

| Surface | Primary files | Notes |
|---------|---------------|-------|
| Header | `tui/widgets/header.go`, `tui/viewmodel/view.go` | Logo, model, view label, YOLO, usage, uptime |
| File-tree sidebar | `tui/filetree/`, `tui/viewmodel/view.go` | Icons, diagnostics, git, scrollbar |
| Sidebar divider | `tui/viewmodel/mouse.go` | Drag resize |
| Main tab bar | `tui/window/window.go` | Editor + agent tabs |
| Overlay tab bar | `tui/window/window.go` | Settings/confirm/question/yolo tabs |
| Status bar | `tui/widgets/statusbar.go` | Activity + contextual help |
| Contextual help | `tui/viewmodel/view.go` | Action-derived hints (target) |
| Main content surface | `tui/viewmodel/view.go` | Editor/chat/launcher compositor layer |

## Primary views

| Surface | Primary files |
|---------|---------------|
| Welcome launcher | `tui/widgets/launcher.go`, `tui/viewmodel/launcher.go` |
| Editor | `tui/editor/` |
| Agent chat message list | `tui/widgets/messagelist.go`, `tui/widgets/chatmessage.go` |
| Chat input | `tui/input/`, `tui/components/text.go` |
| Agent/thinking subpanes | `workspace/subpane.go`, message list |
| Session picker | `tui/widgets/sessionpicker.go` |
| Async command rows | `tui/widgets/asyncbar.go` |
| Reference chips | `tui/input/references.go` |

## Floating windows and popups

| Surface | Primary files |
|---------|---------------|
| Command palette | `tui/viewmodel/command_palette.go` |
| Find in Files | `tui/widgets/find_in_files.go` |
| Find/replace | `tui/widgets/findreplace.go` |
| Workspace settings | `tui/settings/settings.go` |
| File/workspace picker | `tui/widgets/filepicker.go` |
| Composer | `tui/widgets/composer.go` |
| Text/reference overlay | `tui/widgets/textoverlay.go` |
| Context menu | `tui/widgets/contextmenu.go` |
| Dirty-close prompt | `tui/viewmodel/dirty_prompt.go` |
| Tree create/rename | `tui/widgets/textprompt.go` |
| Tree delete confirm | `tui/viewmodel/tree_operations.go` |
| LSP references | `tui/viewmodel/lsp_handlers.go` |
| LSP completion | `tui/viewmodel/lsp_handlers.go` |
| LSP outline | `tui/viewmodel/lsp_handlers.go` |
| Diagnostic popup | `tui/viewmodel/lsp_handlers.go` |

## Inline / blocking forms

| Surface | Primary files |
|---------|---------------|
| Session settings | `tui/widgets/session_settings.go`, `tui/form/` |
| Tool confirmation | `tui/widgets/confirmdialog.go` |
| Agent question form | `tui/widgets/questionform.go` |
| YOLO confirmation | `tui/widgets/yolodialog.go` |
| Generic confirm prompt | `tui/widgets/confirmprompt.go` |

## Secondary interactive content

Slash-command lists, model/provider selectors, settings category navigation,
breadcrumbs, tree connectors, action cards, toggles, diagnostic indicators,
tab close buttons, scroll gutters, empty/loading/error rows, status dots,
progress animation, key hints, section labels, dividers, selectable plain-text
rows.
