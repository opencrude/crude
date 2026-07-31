# TUI Surface Catalog

Status: Phase 0 evidence freeze  
Canonical plan: [`TUI_COMPONENT_SYSTEM_PLAN.md`](../../TUI_COMPONENT_SYSTEM_PLAN.md)

Every user-visible surface must appear here. Migration phases claim completeness
only against this catalog.

## Persistent shell

| Surface | Primary files | Notes |
|---------|---------------|-------|
| Header | `tui/screens/shellheader/`, `tui/viewmodel/view.go` | Native Host paint; logo, model, view label, YOLO, usage, uptime; legacy `widgets/header.go` deleted |
| File-tree sidebar | `tui/screens/shelltree/`, `tui/ui/tree.go`, `tui/filetree/tree_rows.go` | Native Host paint into `mainSidebarSurf` via `ui.Tree` + `ScrollbarControl`; connectors, git, diagnostics from theme tokens; divider resize unchanged; ANSI `filetree/render.go` deleted |
| Sidebar divider | `tui/viewmodel/mouse.go` | Drag resize |
| Main tab bar | `tui/screens/shelltabs/`, `tui/ui/tabs.go`, `tui/viewmodel/tab_bar.go`, `tui/window/window.go` | Native Host paint into `mainTabBarSurf`; `Window` retains tab state only (no ANSI render); legacy `RenderTabBar` / `RenderVerticalSidebar` / `widgets/agenttabs.go` deleted |
| Chat input | `tui/screens/shellinput/`, `tui/input/`, `tui/ui/text_area.go` | Native Host paint into `mainInputSurf`; `ui.TextArea` + reference chips + `ui.List` slash popup; no `components.TextInput` on chat path |
| Session Panel Deck tabs | `tui/ui/tabs.go`, `tui/ui/panel_deck.go`, `tui/viewmodel/session_deck.go` | Per-session native component panel region below messages |
| Legacy inline panel tabs | deleted | Replaced by native per-session PanelDeck tabs; not the floating OverlayHost |
| Status bar | `tui/screens/shellstatus/`, `tui/viewmodel/view.go` | Native Host paint; activity + `ui.Hint` segments via ActionRegistry; legacy `widgets/statusbar.go` deleted |
| Contextual help | `tui/ui/hint.go`, `tui/ui/key_chord.go`, `tui/viewmodel/view.go` | Registry-derived Hint/KeyChord segments (Phase 13 item 2) |
| Main content surface | `tui/viewmodel/view.go` | Editor/chat/launcher compositor layer |

## Primary views

| Surface | Primary files | Notes |
|---------|---------------|-------|
| Welcome launcher | `tui/screens/launcher/launcher.go`, `tui/viewmodel/launcher.go` | Native hero + two-column component tree; **Phase 15:** `launcher_contract_test.go`, `viewmodel/launcher_keyboard_contract_test.go`; `focusTarget()` gated on `launcherHostVisible()` |
| Editor | `tui/screens/shelleditor/`, `tui/editor/` | Native Host paint into `mainEditorSurf` via cell-buffer `Editor.Render` + `ScrollbarControl`; diagnostics gutter/undercurl + search highlights; shellstatus owns title/cursor/diag counts/actions; Find-in-Files preview uses cell-buffer `Editor.Render` adapter |
| Agent chat message list | `tui/screens/shellchat/`, `tui/chatprojection/` | **Phase 13.7/14:** Host + `VirtualList` + native gutters/`ScrollbarControl`; markdown/thinking bodies bridge glamour ANSI to native `PaintLine` spans at render time (`BridgeAnsiLine`) — no paint-time ANSI fallback; `tui/scrollbar` **DELETED**; contracts: `scroll_contract_test.go`, `pane_gutter_test.go`, `host_test.go`, `viewmodel/chat_keyboard_contract_test.go` |
| Chat input | `tui/screens/shellinput/`, `tui/input/` | Same as persistent shell chat input |
| Agent/thinking subpanes | `workspace/subpane.go`, `tui/viewmodel/subpane_render.go`, `tui/chatprojection/subpane_build.go`, `tui/screens/shellchat/` | Compact agent headers/tool lines/summaries are structured `PaintLine` rows (semantic tokens, no lipgloss/SGR in `subpane_render.go`); `shellchat` RowPainter paints them into `CellBuffer` + native nested gutter; thinking bodies use the same render-time `BridgeAnsiLine` bridge; per-pane `*ui.ScrollModel`; `Compose`/`LayoutRow` reserve gutter column + gap only |
| Session picker | `tui/screens/sessionpicker/`, `tui/viewmodel/session_picker.go` | Native Host paint via `ui.List` section headers + scrollbar in OverlayHost/FloatingWindow; `/sessions` and `app.sessions` open overlay; legacy `widgets/sessionpicker.go` deleted |
| Background command details | `tui/screens/backgroundjobs/background_jobs.go`, `tui/viewmodel/session_deck.go` | Native passive per-session PanelDeck Form; global status-bar rows deleted |
| Reference chips | `tui/input/references.go` | |

## Floating windows and popups

Click-outside policies (verified in `tui/viewmodel/overlay_click_outside_contract_test.go`):

| Policy | Overlays |
|--------|----------|
| **CloseRoute** (`PopoverPolicy(true)`) | context-menu, references-popup, completion-popup, outline-popup, diag-context |
| **KeepRoute** (`DefaultOverlayPolicy` / `PopoverPolicy(false)`) | find-replace, command-palette, composer, textoverlay, find-in-files, diag-hover |
| **Ignore** (`ModalDialogPolicy`) | dirty-close-prompt, tree-prompt, tree-delete-confirm, filepicker, workspace-settings, session-picker |

| Surface | Primary files | Notes |
|---------|---------------|-------|
| Command palette | `tui/viewmodel/command_palette.go` | WindowFrame + TextField + List |
| Find in Files | `tui/screens/findinfiles/find_in_files.go`, `tui/viewmodel/find_in_files.go` | Native SearchField query + case/regex ToggleButtons + Label status + VirtualList + ScrollbarControl + editor preview adapter + Label hints; search execution/cancellation owned by workspace.ProjectSearchService |
| Find/replace | `tui/widgets/findreplace.go`, `tui/widgets/findreplace_frame.go` | WindowFrame FramePopover + ui.TextField paint; state/keys via ui.TextEdit |
| Workspace settings | `tui/settings/settings.go`, `tui/widgets/workspace_settings.go` | Native List + FormModel/Form/FormField + ActionBar; no `tui/form` |
| File/workspace picker | `tui/screens/filepicker/file_picker.go`, `tui/viewmodel/app.go` | Native TextField + breadcrumb Label + List + ScrollbarControl + ActionBar body in retained OverlayHost/FloatingWindow shell |
| Composer | `tui/screens/composer` | Native TextArea + ActionBar; legacy ANSI widget deleted |
| Text/reference overlay | `tui/screens/textoverlay` | Native TextArea + ActionBar; legacy ANSI widget deleted |
| Context menu | `tui/widgets/contextmenu.go`, `tui/widgets/contextmenu_frame.go` | Product path WindowFrame + ui.List |
| Dirty-close prompt | `tui/viewmodel/dirty_prompt.go` | BuildDirtyCloseFrame |
| Tree create/rename | `tui/widgets/textprompt.go`, `tui/widgets/textprompt_frame.go` | WindowFrame + TextField + TextEdit |
| Tree delete confirm | `tui/viewmodel/tree_operations.go` | BuildTreeDeleteFrame |
| LSP references | `tui/viewmodel/lsp_handlers.go` | BuildListPopupFrame |
| LSP completion | `tui/viewmodel/lsp_handlers.go` | BuildListPopupFrame |
| LSP outline | `tui/viewmodel/lsp_handlers.go` | BuildListPopupFrame |
| Diagnostic popup | `tui/viewmodel/lsp_handlers.go` | BuildDiagnosticFrame |

## Inline / blocking forms

| Surface | Primary files | Notes |
|---------|---------------|-------|
| Session settings | `tui/screens/sessionsettings/session_settings.go` | Native nested Form pages in per-session docked Panel Deck; **Phase 15:** `viewmodel/panel_deck_contract_test.go` |
| Tool confirmation | `tui/screens/confirmation/confirmation.go` | Native blocking metadata Form + ActionBar in the per-session Panel Deck; contracts in `panel_deck_contract_test.go` + `confirmation_test.go` |
| Agent question form | `tui/screens/question/question.go` | Native blocking Form + option collection + TextField + ActionBar in the per-session Panel Deck; contracts in `panel_deck_contract_test.go` + `question_test.go` |
| YOLO confirmation | `tui/screens/yolo/yolo.go` | Native blocking warning Form + ActionBar in the per-session Panel Deck; contracts in `panel_deck_contract_test.go` + `yolo_test.go` |
| Generic confirm prompt | `tui/widgets/confirm_frame.go` | `BuildConfirmFrame` only; legacy `confirmprompt.go` deleted |

## Deleted packages (Phase 14)

| Package / file | Replacement |
|----------------|-------------|
| `tui/form/` | `ui.Form` / `FormField` / `TextField` |
| `tui/widgets/agenttabs.go` | `tui/screens/shelltabs` + `ui.Tabs` |
| `tui/widgets/header.go` | `tui/screens/shellheader` |
| `tui/widgets/statusbar.go` | `tui/screens/shellstatus` |
| `tui/widgets/sessionpicker.go` | `tui/screens/sessionpicker` |
| `tui/widgets/composer.go` | `tui/screens/composer` |
| `tui/widgets/textoverlay.go` | `tui/screens/textoverlay` |
| `tui/widgets/confirmprompt.go` | `tui/widgets/confirm_frame.go` |
| `tui/filetree/render.go` | `tui/screens/shelltree` + `ui.Tree` |

## Secondary interactive content

Slash-command lists, model/provider selectors, settings category navigation,
breadcrumbs, tree connectors, action cards, toggles, diagnostic indicators,
tab close buttons, scroll gutters, empty/loading/error rows, status dots,
progress animation, key hints, section labels, dividers, selectable plain-text
rows.
