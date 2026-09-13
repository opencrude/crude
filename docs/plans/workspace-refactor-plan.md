# Crude IDE — Architecture Plan & Progress

> Status: DONE (2026-07) — the three-layer split is complete; retained as the workspace-refactor history. The component-system successor plan is [tui-component-system-plan.md](tui-component-system-plan.md).

The canonical TUI component-system and design-language migration plan is
[`docs/plans/tui-component-system-plan.md`](./tui-component-system-plan.md).

**Workspace refactor status:** Phases 1–6 (below) are **DONE** (completed
2026-07). The three-layer split is complete: `workspace/` owns all business
state with zero `tui/` imports, `tui/` is a dumb renderer + input router
holding `*workspace.Workspace`, and `app/` is a thin CLI entry.

**TUI component-system status:** In progress — see
`docs/plans/tui-component-system-plan.md` § Snapshot. The root Host wiring (Phase 13.9)
is DONE (2026-08-01): one `App.rootHost` + `shellRoot` tree painted into one
`App.mainSurf`; per-screen Hosts and layer surfaces deleted.

**Agent-view regression fixes (2026-08-01):** the root-host migration
introduced a double-origin bug — `Host.paintNode` paints children at
(parent origin + child bounds), but shellchat passed its absolute slot rect
to its children, so the chat list painted shifted by the slot origin and
long conversations overwrote the input prompt and status bar. Fixed by
laying children out parent-local (`{X:0,Y:0,...}`); guarded by
`TestAgentView_ChatPaintsInsideSlot` /
`TestAgentView_LongChatDoesNotOverflowInputOrStatus`. Short chats remain
bottom-anchored above the input prompt through `ui.VirtualList.SetBottomGravity`,
guarded by `TestAgentView_ShortChatAnchorsToBottom`. The launcher workspace listing is
loaded asynchronously (`refreshLauncherAsync` + `launcherWorkspacesMsg`,
generation-deduped) so the ~200ms DB scan never blocks the back-to-launcher
hotpath; guarded by `launcher_async_test.go`.

**LSP engineering (2026-08-01):** LSP subprocess I/O is now fully decoupled
from caller goroutines — powernap is vendored
(`third_party/powernap`, `TransportWrap` hook), all writes go through a
bounded async serialized writer (`tui/editor/lsp_pipe.go`), connection death
is observable (`LSPClient.Done()`), the `LSPManager` reaps and restarts dead
servers (`reapOnDone`), and launcher workspace reads are context-bound so a
stalled read cannot leak the workspace DB handle (Windows file lock). See
AGENTS.md → LSP I/O Architecture.

**Workspace-switch lifecycle (2026-08-13):** a project switch is a generation
boundary. The old tree watcher and every editor projection are closed before
replacement; project search is canceled without reusing request IDs; git and
launcher generations advance; LSP requests, diagnostics, and result messages
are canceled/generation-bound; workspace/session/sub-pane projections, tabs,
the input draft, and project-bound overlays are cleared. Target directory and
persistence preflight completes before old state is torn down. The target
workspace theme is applied before rebuilding projections, while root-tree/Git
construction and recursive watcher startup run asynchronously behind a
generation-bound placeholder. Application-global infrastructure (compositor,
root Host, action registry, history, Help, and Command Palette) remains live.

**Editor Git blame and line edits (2026-08-13):** the editor gutter paints
HEAD-vs-buffer line edits (`+` added, `~` modified) beside the diagnostic
column. Blame for the cursor line appears in shellstatus; Copy blame commit
lives on the editor context menu. `workspace.GitService.AnnotateFile` owns
`git show` / `git blame --line-porcelain` off the update goroutine, with
editor-git and workspace-switch generations so a stale result cannot paint
the wrong buffer.

**TUI reliability audit closeout (2026-08-13):** verified deadlock, terminal,
input-routing, narrow-geometry, rendering, and blocking-I/O findings are fixed
with focused contracts. File and terminal close commands no longer re-enter
the synchronous event bus; PTY input is bounded with explicit backpressure;
cursor blinking is activity-gated; clipboard, directory, and source-control
subprocess work is asynchronous and time-bounded where applicable; clipboard
operations run through one ordered worker and image references persist off the
update goroutine; picker results require pointer/epoch/intent/generation
ownership; blocking PanelDecks shield both keyboard and pointer input; and
Unicode/narrow layouts
derive geometry only from columns that exist. The live audit artifact records
the evidence and remaining intentional conventions.

## Architecture: Three Layers

```
app/       — IDE Shell: config file, workspace lifecycle, CLI flags
workspace/ — Per-project state machine: owns sessions, buffers, LSP, file tree, workspace DB
tui/       — Dumb renderer + input router: holds *workspace.Workspace, calls methods
```

Communication: TUI → Workspace via method calls. Workspace → TUI via event bus.

## Config Levels

JetBrains-style inheritance. Each layer stores only what it owns; lower layers
overlay the ones above.

| Level | File | Contents | Editor |
|-------|------|----------|--------|
| Application | `~/.crude/config.json` | Theme, editor, LSP, UI, font, provider credentials, default agents | Command Palette → Application Settings |
| Workspace | `~/.crude/workspaces/<hash>/workspace.db` key `workspace_settings` | Sparse overrides (`types.WorkspaceOverrides`). Nil fields inherit from application. Credentials never persist here. | Command Palette → Workspace Settings |
| Session | per-session DB `session_agent_settings` | Agent provider/model/thinking for this chat | `/settings` dock |

Effective IDE prefs = `MergeAppWorkspace(app, workspace)`. Session settings inherit that baseline, then apply per-agent overrides. `LoadAppSettings()` returns the effective (merged) snapshot. Application save writes `config.json` only; workspace save writes `workspace_settings` only. Legacy `app_settings` rows in `workspace.db` are read as overrides.

See [`settings.md`](../reference/settings.md).

## Data Directory Layout

```
~/.crude/
├  config.json                    ← app config (FileConfig)
├  history.json                   ← input history
└  workspaces/
    ├  <hash-of-project-dir>/
    │   ├  workspace.db           ← workspace-level DB (settings + session registry)
    │   └  sessions/
    │       ├  <session-id>.db    ← session DB
    │       └  <session-id>/      ← session logs, reports, turns
    └  <hash-of-another-project>/
        ├  workspace.db
        └── sessions/
```

## Phase Status (workspace refactor — all DONE)

### Phase 1 — Workspace owns sessions ✅ DONE
- [x] Remove `session.SessionManager` from workspace
- [x] Add `Sessions []*Session` and `SessionsByID map[string]*Session` (canonical)
- [x] Add `session.NewWithSessionDir()` and `session.ResumeSessionWithDir()` for workspace-local sessions
- [x] Workspace.CreateSession / ResumeSession / Bootstrap methods
- [x] Workspace.ListSessions / DeleteSession use WorkspaceDB (workspace-scoped)
- [x] Remove all TUI callback indirection (OnListSessions, OnDeleteSession, OnHardCancel, etc.)
- [x] viewmodel.App calls workspace methods directly
- [x] tui/tui.go simplified: no callbacks, takes *workspace.Workspace
- [x] `go build ./...` passes, `go test ./...` passes

**Removed in Phase 4:** `OnNewSession` and `OnResumeSession` callbacks.

### Phase 2 — Workspace-scoped session registry enrichment ✅ DONE
- [x] Enrich `workspace_sessions` table: project_name, turn_count, first_prompt, last_prompt, message_count
- [x] WorkspaceDB.RegisterSession stores richer metadata
- [x] WorkspaceDB.UpdateSessionMeta wired for live updates (SyncSessionMeta, CloseAllSessions, enrichSessionMeta)
- [x] Workspace.enrichSessionMeta reads from session DB for full metadata + syncs back to registry

### Phase 3 — Session storage under workspace folder ✅ DONE
- [x] Session DBs live under `~/.crude/workspaces/<hash>/sessions/`
- [x] Session logs/reports live under `~/.crude/workspaces/<hash>/sessions/<id>/`
- [x] Legacy fallback: if session DB not found in workspace, look in `~/.crude/sessions/`
- [x] New sessions always created under workspace folder

### Phase 4 — TUI decoupling + launcher becomes workspace selector ✅ DONE
- [x] Remove OnNewSession/OnResumeSession callbacks from Workspace
- [x] Workspace.NewSessionFromLauncher() / ResumeSessionFromLauncher() handle full flow
- [x] Launcher lists workspaces when no project dir specified
- [x] Launcher shows workspace-scoped sessions when inside a workspace
- [x] Resume session stays in same workspace (no quit/restart)

### Phase 5 — App shell wiring ✅ DONE
- [x] app.Run() creates workspace and starts TUI
- [x] App stores session.Config for reuse via SetBackendConfig
- [x] Provider presets defined properly

### Phase 6 — Config split and settings persistence ✅ DONE
- [x] Split Settings into AppSettings + SessionSettings
- [x] AppPart() / SessionPart() methods on Settings
- [x] HandleSettingsSave persists app settings to workspace.db
- [x] /settings form has two sections (Session + Preferences)
- [x] JetBrains-style workspace settings window (two-column, mouse-enabled)
- [x] Session settings form (provider/model/thinking) as overlay
- [x] Ctrl+Alt+P command palette "Settings" opens workspace settings

## Key Decisions

1. **Launcher = workspace selector** — top-level lists workspaces/projects. Inside a workspace, sessions are managed via agent tabs and slash commands.
2. **No callback chains** — viewmodel calls workspace methods directly.
3. **Sessions are workspace-scoped** — listed from workspace.db, stored under workspace folder.
4. **Config split** — app config (visual/tooling) vs session config (AI behavior). Three inheritance levels.
5. **TUI owns presentation** — the component system, layer surfaces, focus, and projections live in `tui/`; workspace exposes neutral view data (`TreeRowView`, `TabView`, `StatusView`, `ChatMessageView`, `SessionSettingsState`) over the event bus / method calls.
