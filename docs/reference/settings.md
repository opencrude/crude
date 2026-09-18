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

Each admitted turn also snapshots the resolved provider ID and model ID from
the exact backend request. That identity is stored on every assistant,
thinking, and tool display snapshot and restored unchanged on resume. Session
settings choose only later turns; changing a model never relabels prior chat
rows. Rows from legacy sessions that predate attribution use the session's
current model as an explicit display fallback only.

**Model modalities are request contracts.** Image, PDF, audio, and video
toggles publish the complete per-role capability snapshot used by `read_file`.
When a supported binary is read, its bytes are attached to that tool result in
the same agent turn; success never means "queued for a later prompt." The wire
encoder maps the neutral attachment to the resolved protocol: OpenAI-compatible
`image_url`, `file`, `input_audio`, or `video_url`; Responses/Codex
`input_image` or `input_file`; Anthropic `image` or `document`; and Ollama
`images`. Unsupported model, protocol, MIME, symlink, and size combinations fail
before a provider request with an explicit reason. Binary edit/write operations
remain forbidden regardless of read capability.

**Workspace root is execution truth.** Every workspace-created or resumed
backend session is bound to that workspace's `RootPath`. Relative file, glob,
grep, directory, synchronous command, async command, main-agent prompt, and
subagent prompt paths resolve against the bound root. Switching projects closes
the old sessions and newly created sessions cannot inherit the process startup
directory.

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

Editor autosave is an application/workspace field-level setting and is **Off by
default**. Modes are After Delay (1,000 ms default, 250-60,000 ms), On Editor
Focus Change, and On Terminal Focus Loss. The last mode depends on terminal
focus reporting; Crude does not infer window blur from idle time. Workspace Off
is an explicit override of application On. The palette's Enable/Disable Auto
Save actions write only that workspace override.

All automatic modes persist the exact buffer snapshot: no formatting, import
organization, reload, cursor movement, selection change, or success toast.
Format on manual save applies only to explicit saves. Shared editor buffers own
live text and savepoint state; `workspace.DocumentSaveService` alone owns save
deadlines and checked atomic disk commits. Revision acknowledgements cannot
clear newer edits, filesystem watcher echoes are reconciled by content identity
rather than a time window, and a detected external change blocks writes until
the user compares or discards it.

Crude-owned writers share one cancellable mutation lease and editor commits
recheck before and after atomic replacement. No portable filesystem primitive
can compare-and-replace against an uncooperative external process atomically;
the final check-to-rename interval is therefore narrowed and post-verified, not
misrepresented as a universal race-free guarantee. Local History remains the
recovery boundary for external states it has observed.

Local History is an application/workspace IDE category with field-level live
inheritance. Defaults are enabled, `.gitignore` on, external capture on, 100 MB
per file, 750 ms debounce, 60 second reconciliation, 24 hourly snapshots, 30
daily snapshots, 500 total unprotected snapshots, and a 1 GiB budget. Safety
snapshots are mandatory and displayed read-only. Additional ignore patterns are
one Git-style pattern per line.

The Local History window exposes logical/physical diagnostics and the command
palette provides `Create History Snapshot`, `Verify Local History`, `Prune Local
History`, and `Open Local History Folder`.

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
title + rule, one line per field, a shared value column — every
second-column value (nav columns, cycle controls, and single-line
editors such as `Compact context at 85`) paints in that one column.
The compaction percentage is a provider-context trigger, not a local JSON-size
threshold. At the next legal request boundary Crude asks the resolved Aeron
category model for four plain-text continuation sections against one unchanged
cached source, then wraps them deterministically. Zero disables automatic
compaction; `/compact` uses the same transaction.
Leftover width is distributed as space after the label and before
inherited text (not filler periods). `·` is a column separator only.
Labels and values use `ColorFg`; inherited/meta use `ColorMuted`.
Up/Down cycle fields and the left-aligned Save/Cancel/Reset
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
  inheritance of untouched fields, explicit autosave Off, legacy whole-category migration
- `tui/settings/autosave_test.go` and `tui/viewmodel/editor_save_test.go` —
  autosave defaults/round-trip, stale revisions, formatting separation,
  conflicts, disable, discard, watcher reconciliation, and committed save
  acknowledgements that must survive a mid-flight rename
- `tui/viewmodel/dirty_prompt_test.go` — cancel close re-arms After Delay
  scheduling, and bulk close (Close others / Close to the right) prompts for
  dirty buffers instead of discarding them
- `workspace/lsp_manager_test.go` — feature gates and diagnostic filtering
- `tui/ui/form_select_test.go` — overlay height and paint/hit-testing
- `tui/ui/scrollbar_paint_test.go` — on-demand scrollbar
- `tui/settings/settings_scope_test.go` — workspace save payload and overflow scrollbars
- `tui/settings/provider_switch_test.go` — provider/model protocol isolation
- `tui/screens/sessionsettings/session_settings_test.go` — session protocol overrides and multi-agent toggle
- `session/model_attribution_test.go`, `workspace/message_view_test.go`, and
  `tui/chatprojection/model_attribution_test.go` — request-time model switching,
  durable resume attribution, and immutable per-row chat headers
- `session/workspace_media_e2e_test.go`,
  `session/protocol_media_matrix_test.go`, and
  `workspace/tool_root_e2e_test.go` — same-turn multimodal tool delivery,
  protocol schemas, persistence/resume, capability and safety gates, and
  workspace-switch execution roots
