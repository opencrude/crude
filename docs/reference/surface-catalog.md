# TUI Surface Catalog

> Status: Current. Every user-visible surface must be listed here; migration phases claim completeness only against this catalog.

Every user-visible surface must appear here. Migration phases claim completeness
only against this catalog.

## Persistent shell

| Surface | Primary files | Notes |
|---------|---------------|-------|
| Header | `tui/screens/shellheader/`, `tui/viewmodel/view.go` | Native Host paint; app name, active workspace root (`~`-abbreviated), branch, model, view label, YOLO, usage, uptime; legacy `widgets/header.go` deleted |
| Activity bar | `tui/screens/activitybar/`, `workspace/tool_layout.go`, `tui/viewmodel/tool_layout.go` | Eight-column rail: Files / Agents / Source Control / Skills / MCP; Settings stays pinned at the bottom. Whole three-row hit bands, actionable capability badges, overflow, pin/unpin/reorder/reset and per-workspace presentation persistence. No Terminals entry. Opening an unfocused visible tool focuses it; repeating on the focused tool hides it. |
| File-tree sidebar | `tui/screens/shelltree/`, `tui/ui/tree.go`, `tui/filetree/tree_rows.go` | Shared panel title chrome + persistent clickable fuzzy search; native `ui.Tree` + `ScrollbarControl`; a shared viewport context compacts guides only when the breadcrumb names ancestry shared by all rows, and restores a normal breadcrumb plus absolute guides across root branches. Every folder row and local connector remains; git, diagnostics, left inset, and **`ui.SplitDivider` right border/resize** |
| Activity lists | `tui/screens/activitylist/` | Shared titled/filterable sidebar body for Agents, Skills and MCP (`ui.List` + scrollbar + divider); retained per-view filter/selection/offset. Board is workspace-wide and does not occupy an Agents-sidebar row. |
| Source Control | `tui/screens/sourcecontrol/`, `workspace/git_service.go`, `tui/viewmodel/source_control.go` | Quiet branch/tracking summary and grouped Conflicts/Staged/Changes; filenames use normal ink and small semantic status marks. Review/History/Branches open main-area review, with Review/More on narrow sidebars. Opening the sidebar never replaces the selected document/chat. |
| Sidebar divider | `tui/ui/split_divider.go`, `tui/screens/shelltree/`, `tui/screens/activitylist/` | Shared `ui.SplitModel` + `SplitDivider`; viewmodel applies `split.resize` to `Tree.Width` |
| Main tab bar | `tui/screens/shelltabs/`, `tui/ui/tabs.go`, `tui/viewmodel/tab_bar.go`, `tui/window/window.go` | Shared tab strip spanning the whole content column; `shelltabs.RailInset` (one column, via `ui.Tabs.SetLabelInset`) indents only the label content so the first tab label gets breathing room beside the navigation rail — the tab cell, its hover fill, and its active underline still start on the rail; the screen paints no chrome of its own. `Window` retains tab state only (no ANSI render); window tab icons stay out of the strip (tofu risk). Legacy `RenderTabBar` / `RenderVerticalSidebar` / `widgets/agenttabs.go` deleted. Each session's main agent precedes its visible children; completed children hydrated on resume stay closed for review until selected. Background sync repairs agent order without moving editor slots or stealing their selection. See [`docs/reference/interaction-matrix.md`](./interaction-matrix.md) New Agent. Editor tabs show unsaved changes as the strip's prefix attention marker (`● name.go`, `window.TabModified` + `syncEditorTabPresentation`); the label itself stays the plain base name, so no marker is ever appended to a filename. |
| Chat input | `tui/screens/shellinput/`, `tui/input/`, `tui/ui/text_area.go` | Native Host paint into `mainInputSurf`; `ui.TextArea` + reference chips + `ui.List` slash popup; composer **and slash list** fill is shell `ColorBg` (not popup); no leading `❯`; no `components.TextInput` on chat path |
| Session Panel Deck tabs | `tui/ui/tabs.go`, `tui/ui/panel_deck.go`, `tui/viewmodel/session_deck.go` | Per-session native component panel region below messages (settings, questions, confirmation, YOLO, jobs) — not terminals. Deck chrome keeps row 0 as its top rule and gives the shared strip rows 1-2 (labels flush on the deck edge + the baseline the strip owns), so panels keep the same content height as the old rule; docked forms share chat `ColorBg` and the compact single-line form density (`NewDockedForm`); floating windows keep `ColorPopupBg` and the spacious settings form. |
| Workspace bottom panel | `tui/screens/termdock/`, `tui/screens/statusoverview/`, `tui/viewmodel/workspace_panel.go`, `tui/viewmodel/terminals.go` | One resizable retained workspace panel selected by Problems / Notices / Terminals / Board / Memory / Workspace. Per-panel normal heights and reversible maximize/restore/hide preserve data and processes; blocking session decisions take priority on short grids. Status search or panel tabs/title share their first row with host controls; browse search and local actions share a row when they fit. Terminal instances use compact numbered tabs beside actions and a named overflow picker; New/Float/Close are explicit, Hide does not terminate. Status lists retain search/disclosure and explicit Open/Resume actions. No duplicate workspace destination strip. |
| In-app terminal (docked) | `tui/screens/shellterm/`, `tui/term/`, `workspace/terminal.go` | libghostty-vt PTY + VT paint in the workspace dock. See [`docs/reference/in-app-terminals.md`](./in-app-terminals.md) |
| Legacy inline panel tabs | deleted | Replaced by native per-session PanelDeck tabs; not the floating OverlayHost |
| Header + footer status | `tui/screens/shellheader/`, `tui/screens/shellfooter/`, `tui/viewmodel/status_overview.go` | Header retains workspace/branch identity and active-view facts; Open Tabs is a separate statistics modal. Footer left has ranked hints/Help; right has the contiguous Problems / Notices / Terminals / Board / Memory / Workspace controls, with selected/attention priority and explicit More overflow. Board and Memory have no permanent count badges. Health and notifications are workspace-owned; background updates never select a panel. |
| Contextual help | `tui/ui/hint.go`, `tui/ui/key_chord.go`, `tui/viewmodel/view.go` | Registry-derived Hint/KeyChord segments (Phase 13 item 2) |
| Main content surface | `tui/viewmodel/view.go` | Editor/chat/launcher compositor layer |

## Primary views

| Surface | Primary files | Notes |
|---------|---------------|-------|
| Workspace home | `tui/screens/launcher/`, `tui/viewmodel/launcher.go` | Wide: outlined New Session with cycling icon spans the top; folder-icon New Workspace starts the left pane, followed by one blank row and an equal-height Skills/live MCP split, while recent workspaces uses the right pane with a bottom-pinned guide. Cramped mode stacks the two actions with one blank row, hides capabilities, and gives Recent workspaces the full width. Dedicated search row, name/path pairs with quiet separators, native FilterField/VirtualList/ScrollbarControl, ProgressRule loading, error/retry and owner-safe async snapshots. No extra workspace heading or duplicate settings/help/session controls. |
| Git Changes review | `tui/screens/gitreview/`, `workspace/git_comparison.go`, `tui/viewmodel/git_review.go` | Typed reusable main tab, Changes/History/Branches, full path and version identity, async diff preparation, unified/side-by-side, hunk/find/horizontal access, explicit stage/unstage/open and exact staged-set commit review. Returning restores the prior live tab. Stale disk/index/HEAD and unsaved editor bytes fail closed. |
| Workspace Memory | `tui/screens/memorybrowser/`, `tui/viewmodel/memory_browser.go`, `workspace/memory_service.go` | One retained footer/palette/slash browser: knowledge/episodes, search/filter, copyable evidence/provenance/revisions/exposure/feedback, pin/correct/retire. Corrections use expected revision; stale results and workspace switches preserve ownership. Never a second floating overlay. |
| Shared Board | `tui/screens/boardbrowser/`, `tui/viewmodel/board_browser.go`, `workspace/board_service.go` | Workspace panel with explicit collaboration, participants/threads, replies/evidence, bounded search/pages and independent human unread. Joining roots is explicit. Inspection never advances an agent cursor, wakes work or grants approval. |
| Editor | `tui/screens/shelleditor/`, `tui/editor/` | Native Host paint into `mainEditorSurf` via cell-buffer `Editor.Render` + `ScrollbarControl`; diagnostics gutter/undercurl + Git line-edit gutter (`+` added, `~` modified); active file/cursor/diagnostics and compact cursor blame project into the header, full blame and Copy blame commit remain in editor detail/context actions; Find-in-Files preview uses cell-buffer `Editor.Render` adapter |
| Agent chat message list | `tui/screens/shellchat/`, `tui/chatprojection/` | **Phase 13.7/14:** Host + `VirtualList` + native gutters/`ScrollbarControl`; Markdown/thinking bodies bridge Glamour ANSI to native `PaintLine` spans at render time (`BridgeAnsiLines` retains block style across wrapping; `BridgeAnsiLine` handles independent rows). Terminal control payloads never become text. No paint-time ANSI fallback; `tui/scrollbar` **DELETED**; contracts: `ansi_bridge_test.go`, `scroll_contract_test.go`, `pane_gutter_test.go`, `host_test.go`, `viewmodel/chat_keyboard_contract_test.go` |
| Chat input | `tui/screens/shellinput/`, `tui/input/` | Same as persistent shell chat input |
| Composer capability picker | `tui/screens/capabilitypicker/`, `tui/viewmodel/capability_picker.go` | Visible + Capability control and Insert Capability palette action; Skills/Tools/Resources/Prompts share cached descriptors. Skill selection stages an exact invocation, tools stage selection-only reference chips, resources/prompts require explicit argument/fetch/preview before attachment. Owner/draft/workspace checks reject late results; no operation auto-submits. |
| Agent/thinking subpanes | `workspace/subpane.go`, `tui/viewmodel/subpane_render.go`, `tui/chatprojection/subpane_build.go`, `tui/screens/shellchat/` | Compact agent headers name allocated nickname + category, while tool results add exact handle. One canonical progress/display snapshot keeps live and hydrated rows identical. Rows are structured `PaintLine` data (semantic tokens, no lipgloss/SGR in `subpane_render.go`); `shellchat` RowPainter paints them into `CellBuffer` + native nested gutter. Thinking, peer communication, agent/Board operations and sub-agent panes share full-width `ColorSubPaneBg` bands, including row padding and nested gutters; transcript spacers remain on the chat surface. Thinking bodies use the same render-time `BridgeAnsiLine` bridge; per-pane `*ui.ScrollModel`; `Compose`/`LayoutRow` reserve gutter column + gap only. Named terminal outcomes are owner-scoped footer transients and force a presented frame. |
| Session picker | `tui/screens/sessionpicker/`, `tui/viewmodel/session_picker.go` | Native Host paint via `ui.List` section headers + scrollbar in OverlayHost/FloatingWindow; `/sessions` and `app.sessions` open overlay; legacy `widgets/sessionpicker.go` deleted |
| Help | `tui/screens/help/`, `tui/viewmodel/help.go` | Modal WindowFrame + FilterField + VirtualList + ScrollbarControl; catalog rebuilt from `input.AvailableCmds`, ActionRegistry shortcuts, and usage topics on each open; `/help`, `help.show`, F1 |
| Background command details | `tui/screens/backgroundjobs/background_jobs.go`, `tui/viewmodel/background_jobs.go`, `tui/viewmodel/session_deck.go` | Per-session PanelDeck table with content-sized handles, whole-second human durations and native Terminate buttons on running rows. Opens passively without stealing focus; commands retain exact owner/handle/PID/start identity and cannot terminate another job or the session. Narrow layouts drop optional telemetry before shortening handles, then use the shared close glyph for Terminate. |
| Reference chips | `tui/input/references.go`, `tui/input/attachment_chips.go`, `fileio/desktop*.go` | Shared paint/hit geometry. Filename bodies open, a distinct three-cell target detaches the draft, and overflow/wheel navigation reveals further attachments. Context menus expose default-app opening and file-manager reveal. |
| Agents settings catalog | `tui/settings/agents.go` | Settings > Agents lists built-in roles, Memory Agent, Auto Review Agent, and reusable named agents. Auto Review is a no-tools permission reviewer with explicit account/model, bounded extra restrictions, enable state, and one Review tab for its persisted thinking, response, retry, budget and outcome policy. Native Model / Instructions / Tool Access tabs provide discovered-model selectors, multiline instructions and constrained tool checkboxes for other agents. Add/edit/clone/remove/reset and availability are explicit, with validation and discard confirmation. Apply stages a draft; Save persists it. Providers and Accounts share the same full-width search and bottom action rhythm. |
| Prompt history | `history/`, `workspace/references.go`, `tui/viewmodel/prompt_history.go`, `tui/screens/statusoverview/` | `/history` opens a searchable WindowFrame modal with single-line time, prompt preview and attachment-count summaries. Disclosure reveals full prompt, media descriptions and an explicit Use in composer action. Up/Down recall and the browser share the same store, immutable media snapshots and owner/generation-checked session import. |

## Floating windows and popups

Click-outside policies (verified in `tui/viewmodel/overlay_click_outside_contract_test.go`):

| Policy | Overlays |
|--------|----------|
| **CloseRoute** (`PopoverPolicy(true)`) | context-menu, references-popup, completion-popup, outline-popup, diag-context |
| **KeepRoute** (`DefaultOverlayPolicy` / `PopoverPolicy(false)`) | find-replace, command-palette, composer, textoverlay, find-in-files, replace-in-files, diag-hover, **floating terminals** (dynamic `terminal/<id>`, not in the static overlay matrix) |
| **Ignore** (`ModalDialogPolicy`) | dirty-close-prompt, tree-prompt, tree-delete-confirm, workspace-delete, filepicker, workspace-settings, capability-picker, session-picker, help |

| Surface | Primary files | Notes |
|---------|---------------|-------|
| Command palette | `tui/viewmodel/command_palette.go` | WindowFrame + TextField + List |
| Find in Files | `tui/screens/findinfiles/find_in_files.go`, `tui/viewmodel/find_in_files.go` | Native SearchField (glyph at start) + case/regex ToggleButtons on the **right** + Label status + VirtualList + ScrollbarControl + editor preview + right-aligned Open/Close ActionBar; **resizable results/preview split**; search execution/cancellation owned by workspace.ProjectSearchService |
| Replace in Files | same screen, `ModeReplace` | Sibling overlay (`OverlayReplaceInFiles`); replace field + Replace/All on the right; writes via `fileio.PlanProjectReplace` then open buffers or `workspace.ApplyWorkspaceEdit` |
| Find/replace | `tui/ui/findreplace_frame.go`, `tui/viewmodel/findreplace_handler.go` | In-buffer overlay; WindowFrame FramePopover + ui.TextField paint; state/keys via ui.TextEdit. Find row leads with the find input ("needle ⌕ Aa ab .*": value first, themed ⌕/⇄ end-glyphs, Aa/ab/.* pills trailing); replace input full width. Find mode shares one row for counter + right-aligned ↑/↓ ActionBar; replace mode gives the counter its own row above the Replace/Replace All/↑/↓ bar. Both forms share one width (replace mode's action bar via `ui.FindReplaceSharedWidthFloor`), so the Ctrl+F ↔ Ctrl+H transform changes height only and never moves window edges |
| Workspace settings | `tui/settings/settings.go`, `tui/settings/surface.go`, `tui/ui/popup/workspace_settings.go` | Retained floating WindowFrame, category List, forms/catalogs, scope ToggleButtons sharing the Save/Cancel footer row, on-demand scrollbars and resizable rail/form split. Scope is not a content tab strip. Dirty preference/editor decisions remain stacked over the original frame. |
| Skills/MCP management | `tui/settings/capability_catalog.go`, `tui/settings/capability_store.go`, `tui/settings/capability_details.go`, `workspace/capabilities.go` | One searchable store with readable cards, explicit Installed-only filter, Source/Sort choice lists and per-card Install/Manage. Sources uses provenance/coverage cards. Scoped entity operations, full skill/connection detail and native readers retain the same Settings frame. Exact preview, immutable managed bytes, explicit OAuth/connect/test/grants and resource/prompt staging; management needs no provider calls. |
| Security settings | `tui/settings/settings.go`, `tui/settings/agents.go` | Two Security rail entries: Command Safety (classification, confirmation, attention) and Auto Review Policy, a synchronized projection of the same persisted policy edited on Auto Review Agent's Review tab. Enforced, non-configurable boundaries are explained inline and in the settings reference, not exposed as fake controls. |
| Network settings | `tui/settings/network.go`, `network/`, `tui/viewmodel/preferences_save.go` | Application-owned Connection / Proxy / Certificates pages between AI and Security; shared form/search, masked proxy URL, read-only workspace inheritance, exact-host trust/mTLS and owner-correlated off-UI certificate validation/save. |
| Run recovery notice | `agent/recovery.go`, `session/recovery.go`, `workspace/recovery.go`, `tui/chatprojection/recovery.go` | One retained incident with static severity title, full neutral diagnostic and muted action; fixed-strip countdown/attention, exact Continue context/palette/slash routes, accepted work/draft preservation and shared live/hydrated projection. |
| File/workspace picker | `tui/screens/filepicker/file_picker.go`, `tui/viewmodel/app.go` | Native fuzzy FilterField + icon breadcrumb + filesystem browser + scrollbar + ActionBar. Folder mode is a larger resizable branded New Workspace window with tree-style directory rows |
| Help | `tui/screens/help/help.go`, `tui/viewmodel/help.go` | Native FilterField + VirtualList + ScrollbarControl in OverlayHost/FloatingWindow; usage topics + live slash commands + live shortcuts |
| Composer | `tui/screens/composer` | Native TextArea + ActionBar; legacy ANSI widget deleted |
| Text/reference overlay | `tui/screens/textoverlay` | Native TextArea + ActionBar; legacy ANSI widget deleted |
| Context menu | `tui/widgets/contextmenu.go`, `tui/widgets/contextmenu_frame.go` | Product path WindowFrame + ui.List |
| Dirty-close prompt | `tui/viewmodel/dirty_prompt.go` | BuildDirtyCloseFrame; also the guard for tab close, bulk close (Close others / Close to the right), quit, and workspace switch. Cancel re-arms After Delay scheduling |
| Tree create/rename | `tui/widgets/textprompt.go`, `tui/widgets/textprompt_frame.go` | WindowFrame + TextField + TextEdit |
| Tree delete confirm | `tui/viewmodel/tree_operations.go` | BuildTreeDeleteFrame |
| Workspace delete | `tui/viewmodel/workspace_delete.go`, `workspace/deleted_workspace.go` | WindowFrame + native read-only detail and ActionBar; async immutable preview, Cancel by default, recoverable directory move, exact Undo without overwriting replacement workspaces |
| LSP references | `tui/viewmodel/lsp_handlers.go` | BuildListPopupFrame |
| LSP completion | `tui/viewmodel/lsp_handlers.go` | BuildListPopupFrame |
| LSP outline | `tui/viewmodel/lsp_handlers.go` | BuildListPopupFrame |
| Diagnostic popup | `tui/viewmodel/lsp_handlers.go` | BuildDiagnosticFrame |
| Floating terminal | `tui/screens/shellterm/`, `tui/viewmodel/terminals.go` | Per-PTY OverlayHost window; drag/resize/stack via WM z-order; Esc goes to the PTY |

## Inline / blocking forms

| Surface | Primary files | Notes |
|---------|---------------|-------|
| Session settings | `tui/screens/sessionsettings/session_settings.go`, `tui/screens/sessionsettings/capabilities.go` | Compact session-owned form/breadcrumbs, aligned model/account rows, Default agent, Capabilities role/root selection and Multi Agent. Capability changes persist only owning session overrides and remain constrained by effective grants/ceilings. Account/Model use filtered pickers; Save/Cancel/Reset stays docked. |
| Tool confirmation | `tui/screens/confirmation/confirmation.go`, `tui/viewmodel/agent_prompt.go` | Same-family requests share one retained blocking PanelDeck across parent/sibling/nested tabs. Two or more exact-owner requests surface shared `ui.Tabs`; tab close denies its exact call and resolution removes that tab immediately, with no empty strip at one request. Leaving a family hides but retains it. Background-family/editor requests use an owner-labelled FIFO modal and migrate into the dock if that family becomes visible. Owner/subtree termination removes its requests. |
| Agent question form | `tui/screens/question/question.go`, `tui/viewmodel/agent_prompt.go` | Uses the same exact `(agent, block)` registry, family aggregation, retained tabs, FIFO modal routing and teardown ownership as confirmations. Closing a question request submits cancellation for that exact owner. |
| Docked terminal | `tui/screens/shellterm/`, `tui/screens/termdock/` | Terminal mode in the workspace panel; compact numbered instance tabs share the header with actions, with a full-width baseline and PTY body; Float promotes to a window |
| YOLO confirmation | `tui/screens/yolo/yolo.go` | Native blocking warning Form + ActionBar in the per-session Panel Deck; contracts in `panel_deck_contract_test.go` + `yolo_test.go` |
| Generic confirm prompt | `tui/widgets/confirm_frame.go` | `BuildConfirmFrame` only; legacy `confirmprompt.go` deleted |

## Deleted packages (Phase 14)

| Package / file | Replacement |
|----------------|-------------|
| `tui/form/` | `ui.Form` / `FormField` / `TextField` |
| `tui/widgets/agenttabs.go` | `tui/screens/shelltabs` + `ui.Tabs` |
| `tui/widgets/header.go` | `tui/screens/shellheader` |
| `tui/widgets/statusbar.go` | `tui/screens/shellheader` + `tui/screens/shellfooter` + workspace bottom panel |
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
