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
| Session Panel Deck tabs | `tui/ui/tabs.go`, `tui/ui/panel_deck.go`, `tui/viewmodel/session_deck.go` | Per-session native component panel region below messages |
| Legacy inline panel tabs | deleted | Replaced by native per-session PanelDeck tabs; not the floating OverlayHost |
| Status bar | `tui/widgets/statusbar.go` | Activity + contextual help |
| Contextual help | `tui/viewmodel/view.go` | Action-derived hints (target) |
| Main content surface | `tui/viewmodel/view.go` | Editor/chat/launcher compositor layer |

## Primary views

| Surface | Primary files |
|---------|---------------|
| Welcome launcher | `tui/screens/launcher/launcher.go`, `tui/viewmodel/launcher.go` | Native hero + two-column component tree; SearchField, VirtualList, and scrollbar-aware pane viewport |
| Editor | `tui/editor/` |
| Agent chat message list | `tui/widgets/messagelist.go`, `tui/widgets/chatmessage.go` |
| Chat input | `tui/input/`, `tui/components/text.go` |
| Agent/thinking subpanes | `workspace/subpane.go`, message list |
| Session picker | `tui/widgets/sessionpicker.go` |
| Background command details | `tui/screens/backgroundjobs/background_jobs.go`, `tui/viewmodel/session_deck.go` | Native passive per-session PanelDeck Form; global status-bar rows deleted |
| Reference chips | `tui/input/references.go` |

## Floating windows and popups

| Surface | Primary files |
|---------|---------------|
| Command palette | `tui/viewmodel/command_palette.go` |
| Find in Files | `tui/screens/findinfiles/find_in_files.go`, `tui/viewmodel/find_in_files.go` | Native SearchField query + case/regex ToggleButtons + Label status + VirtualList + ScrollbarControl + editor preview adapter + Label hints; search execution/cancellation owned by workspace.ProjectSearchService |
| Find/replace | `tui/widgets/findreplace.go` |
| Workspace settings | `tui/settings/settings.go` |
| File/workspace picker | `tui/screens/filepicker/file_picker.go`, `tui/viewmodel/app.go` | Native TextField + breadcrumb Label + List + ScrollbarControl + ActionBar body in retained OverlayHost/FloatingWindow shell |
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
| Session settings | `tui/screens/sessionsettings/session_settings.go` | Native nested Form pages in per-session docked Panel Deck |
| Tool confirmation | `tui/screens/confirmation/confirmation.go` | Native blocking metadata Form + ActionBar in the per-session Panel Deck |
| Agent question form | `tui/screens/question/question.go` | Native blocking Form + option collection + TextField + ActionBar in the per-session Panel Deck |
| YOLO confirmation | `tui/screens/yolo/yolo.go` | Native blocking warning Form + ActionBar in the per-session Panel Deck |
| Generic confirm prompt | `tui/widgets/confirmprompt.go` |

## Secondary interactive content

Slash-command lists, model/provider selectors, settings category navigation,
breadcrumbs, tree connectors, action cards, toggles, diagnostic indicators,
tab close buttons, scroll gutters, empty/loading/error rows, status dots,
progress animation, key hints, section labels, dividers, selectable plain-text
rows.
