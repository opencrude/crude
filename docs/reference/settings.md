# Settings layers

> Status: Current reference for the three-level settings model (app → workspace → session).

Crude settings follow the JetBrains three-level model. Each layer stores only
what it owns. Lower layers overlay the ones above; an absent field means
inherit.

```
Application  (~/.crude/config.json)
    ↓ MergeAppWorkspace
Workspace    (workspace.db / workspace_settings)
    ↓ session dock overlays agent provider/model/protocol/thinking
Session      (session.db / session_agent_settings)
```

## Two different inheritance rules, on purpose

**Workspaces inherit live.** `WorkspaceOverrides` stores a partial JSON object
per category holding only the individual leaves the workspace changed. Every
field it does not name keeps following the application layer forever, so
turning something off at the application level reaches the workspace even
years later.

On a project switch, the target workspace database is opened before the old
project is torn down. Its effective theme is then applied before target-owned
editors, tree, and session projections are constructed, so no frame is built
with the previous workspace palette.

Overriding a whole category is what made a workspace behave like a hard copy:
changing one editor field froze every other editor field at whatever the
application held that day. `OverridesFromDiff` recurses into nested objects,
so a workspace that disables one language server keeps inheriting the other
ten, and explicit `false` is a real override rather than an absent key
(`omitempty` elides it, so the diff re-adds the zero).

**Sessions snapshot.** A session captures the agent baseline — which provider,
model, and thinking effort each role runs — when it starts, and keeps it. A
running conversation must not switch models because another tab saved
settings. Agent profiles store a **provider ID only**; API keys live on
`providers` in `config.json` and are resolved at use time. Updating a key in
Application Settings → Providers is enough for every role that references
that provider. `Session.UpdateWorkspaceDefaults` refreshes provider
endpoints, wire standards, and credentials, so fixing a bad API key still
reaches live sessions. `AdoptWorkspaceDefaults` is the deliberate re-inherit
and runs only from user-invoked resets. New sessions always start from the
current effective defaults.

Stored overlays are re-diffed against the current application layer on load,
which narrows legacy whole-category rows to their differing leaves. A
snapshot cannot say which of its values the user meant, so migration keeps
whatever still differs; **Reset section** in the workspace scope is how a
category goes back to full inheritance.

Provider protocol resolution is equally layered:

```
session role override
    ↓
provider's selected-model override
    ↓
provider default
    ↓
built-in preset default
```

Supported standards are OpenAI Responses, OpenAI Chat Completions, and
Anthropic Messages. Existing configs with no protocol fields continue to use
their preset default.

`workspace.LoadAppSettings()` returns the **effective** snapshot (app +
workspace). Application defaults alone are `AppDefaults()`.

## Who writes what

| Action | Persistence | Must not write |
|--------|-------------|----------------|
| Application Settings → Save | `config.json` via `SaveAppSettings` + provider credentials via `SaveProviderSettings` | `workspace.db`, API keys on agent profiles |
| Workspace Settings → Save | field-level `WorkspaceOverrides` in `workspace.db` key `workspace_settings` | `config.json`, API keys, untouched fields |
| Workspace Settings → Reset section | `ClearCategory` then `SaveWorkspaceSettings` | other categories, app config |
| `/settings` or `/model` dock | session agent provider/model/protocol/thinking **and multi-agent** | app/workspace IDE prefs, API keys |
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

Session settings stay on the per-session Panel Deck (`/settings` or
`/model`). Titles are breadcrumbs: `Session Settings · Main Agent · Model`
for any depth. The root page is a compact agent table (model, then
provider, aligned `·` columns) with **Multi Agent** last after a blank
row — that inherited `·` shares the agent table's last column. Enter on
a role opens its overrides; Enter on Provider or Model opens a
filterable picker page. Filter inputs use `FilterSurface`
(`ColorPopupSubtleBg`), never the dock fill. Docked forms are compact:
title + rule, one line per field, a shared value column. Leftover
width is distributed as space after the label and before inherited
text (not filler periods). `·` is a column separator only. Labels and
values use `ColorFg`; inherited/meta use `ColorMuted`. Up/Down
cycle fields and the left-aligned Save/Cancel/Reset
row; Esc closes or goes back. Agent rows keep a trailing `▸`
(`TreeCollapsed`). Never paint `❯`.

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
- The Providers page separates Connection, Communication protocol, and Model
  defaults. A provider protocol can be overridden for one selected model
  without changing other models on the same gateway.
- Model choices show context size, per-million-token pricing, and
  vision/tools/file capability badges when models.dev metadata is available.

## LSP

Application and workspace settings share the same Language Servers page:
master enable, start-on-open, per-feature gates (diagnostics, completions,
hover, definition, references, outline, rename), diagnostic display
(editor / tree / status + minimum severity), and per-language enable plus
command/args overrides. `workspace.LSPManager` reads the **effective**
`LSPSettings` and refuses to start or request gated features.

## Tests

Layer coverage lives next to the code:

- `types/settings_test.go` — field-level merge/diff, inheriting untouched
  fields, explicit `false` overrides, round-trip stability, credential
  stripping, LSP language/severity
- `workspace/settings_test.go` — overlay persistence vs app defaults, live
  inheritance of untouched fields, legacy whole-category migration
- `workspace/lsp_manager_test.go` — feature gates and diagnostic filtering
- `tui/ui/form_select_test.go` — overlay height and paint/hit-testing
- `tui/ui/scrollbar_paint_test.go` — on-demand scrollbar
- `tui/settings/settings_scope_test.go` — workspace save payload and overflow scrollbars
- `tui/settings/provider_switch_test.go` — provider/model protocol isolation
- `tui/screens/sessionsettings/session_settings_test.go` — session protocol overrides and multi-agent toggle
