# Settings layers

Crude settings follow the JetBrains three-level model. Each layer stores only
what it owns. Lower layers overlay the ones above; a nil/empty field means
inherit.

```
Application  (~/.crude/config.json)
    ↓ MergeAppWorkspace
Workspace    (workspace.db / workspace_settings)
    ↓ session dock overlays agent provider/model/thinking
Session      (session.db / session_agent_settings)
```

`workspace.LoadAppSettings()` returns the **effective** snapshot (app +
workspace). Application defaults alone are `AppDefaults()`.

## Who writes what

| Action | Persistence | Must not write |
|--------|-------------|----------------|
| Application Settings → Save | `config.json` via `SaveAppSettings` + provider credentials via `SaveProviderSettings` | `workspace.db` |
| Workspace Settings → Save | sparse `WorkspaceOverrides` in `workspace.db` key `workspace_settings` | `config.json`, API keys |
| Workspace Settings → Reset section | `ClearCategory` then `SaveWorkspaceSettings` | other categories, app config |
| `/settings` dock | session agent overrides | app/workspace IDE prefs |
| Theme cycle (`app.toggleTheme`) | application layer | workspace overlay |

Legacy `workspace.db` key `app_settings` (full `AppSettings` blobs) is still
read: `LoadWorkspaceOverrides` diffs it against current app defaults so existing
projects keep their per-workspace theme/editor/LSP values.

## UI

One floating window (`ui.OverlayWorkspaceSettings`) with a segmented
Application | Workspace switch in the **bottom-left** rail footer. Both
labels stay visible and clickable; the active side uses accent (orange)
text on a distinct background. Save/Cancel stay mounted across overlay
ticks (`SetModel` must not rebuild the action bars). Command Palette
entries:

- `settings.open` — Application Settings
- `settings.workspace` — Workspace Settings

Session settings stay on the per-session Panel Deck (`/settings`).
Up/Down stay inside the agent list; Tab moves to Save/Cancel; Esc
closes. Agent rows use a trailing `▸` (`TreeCollapsed`), not the chat
prompt glyph.

Visual contracts for this window:

- Inherited top padding (`TokenSet.FormTitlePad`) so the body is not flush
  with the window title; the first form section header shares that row
  with Search
- Subsection labels indent one column from section titles (`FormIndent`)
  on both the left rail and the right-hand form
- Search magnifier is trailing (`SearchGlyphEnd` + `PromptEnd`)
- Full-height **resizable** vertical divider between rail and form (`ui.SplitDivider`)
- No right-panel title/description; the form starts after the top pad
- Group gap on the left rail (`ListItem.Spacer` between AI and IDE)
- Section gap on the right (`Form` field gap + separator rows)
- Focused form row uses one background for label and control
- Save/Cancel right-align; Reset section sits on the opposite side of the
  form footer (just right of the vertical divider)
- `FieldSelect` is an overlay (z-order paint pass) with box-drawing chrome.
  Opening a dropdown does not grow form height or push later fields down;
  it flips above the field when there is no room below
- Left and right panels get an on-demand `ScrollbarControl` (`Needed()` is
  false while content fits)
- Form paint clips to the panel viewport. Scrolled fields must not overwrite
  the footer separator or Save/Cancel row

## LSP

Application and workspace settings share the same Language Servers page:
master enable, start-on-open, per-feature gates (diagnostics, completions,
hover, definition, references, outline, rename), diagnostic display
(editor / tree / status + minimum severity), and per-language enable plus
command/args overrides. `workspace.LSPManager` reads the **effective**
`LSPSettings` and refuses to start or request gated features.

## Tests

Layer coverage lives next to the code:

- `types/settings_test.go` — merge, diff, credential stripping, LSP language/severity
- `workspace/settings_test.go` — overlay persistence vs app defaults, legacy migration
- `workspace/lsp_manager_test.go` — feature gates and diagnostic filtering
- `tui/ui/form_select_test.go` — overlay height and paint/hit-testing
- `tui/ui/scrollbar_paint_test.go` — on-demand scrollbar
- `tui/settings/settings_scope_test.go` — workspace save payload and overflow scrollbars
