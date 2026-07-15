# Crude IDE — Architecture Plan & Progress

## Architecture: Three Layers

```
app/       — IDE Shell: config file, workspace lifecycle, CLI flags
workspace/ — Per-project state machine: owns sessions, buffers, LSP, file tree, workspace DB
tui/       — Dumb renderer + input router: holds *workspace.Workspace, calls methods
```

Communication: TUI → Workspace via method calls. Workspace → TUI via event bus.

## Config Levels

| Level | File | Contents |
|-------|------|----------|
| App | `~/.crude/config.json` | Theme, editor, LSP, UI, font, provider API keys |
| Workspace | `~/.crude/workspaces/<hash>/workspace.db` | Session registry, workspace settings overrides, state tracking |
| Session | per-session DB | Model/provider, thinking effort, agent profiles, max steps, tool permissions, messages |

## Data Directory Layout

```
~/.crude/
├── config.json                    ← app config (FileConfig)
├── history.json                   ← input history
└── workspaces/
    ├── <hash-of-project-dir>/
    │   ├── workspace.db           ← workspace-level DB (settings + session registry)
    │   └── sessions/
    │       ├── <session-id>.db    ← session DB
    │       └── <session-id>/      ← session logs, reports, turns
    └── <hash-of-another-project>/
        ├── workspace.db
        └── sessions/
```

## Phase Status

### Phase 1 — Workspace owns sessions ✅ DONE
- [x] Remove `session.SessionManager` from workspace
- [x] Add `Sessions []*Session` and `SessionsByID map[string]*Session` (canonical)
- [x] Add `session.NewWithSessionDir()` and `session.ResumeSessionWithDir()` for workspace-local sessions
- [x] Workspace.CreateSession / ResumeSession / Bootstrap methods
- [x] Workspace.ListSessions / DeleteSession use WorkspaceDB (workspace-scoped)
- [x] Remove all TUI callback indirection (OnListSessions, OnDeleteSession, OnHardCancel, etc.)
- [x] viewmodel.App calls workspace methods directly
- [x] tui/tui.go simplified: no callbacks, takes *workspace.Workspace
- [x] tui/viewmodel.NewApp takes *workspace.Workspace
- [x] app.Run() creates workspace, bootstraps session, starts TUI
- [x] `go build ./...` passes
- [x] `go test ./...` passes (safety test failures fixed)

**Kept for Phase 4:** `OnNewSession` and `OnResumeSession` are still callback fields on Workspace. They carry backend knowledge (session.Config, homeDir, EventSender) that the viewmodel doesn't have.

### Phase 2 — Workspace-scoped session registry enrichment ⬜ TODO
- [ ] Enrich `workspace_sessions` table: add project_name, turn_count, first_prompt, last_prompt, message_count
- [ ] WorkspaceDB.RegisterSession stores richer metadata
- [ ] WorkspaceDB.UpdateSessionMeta for live updates
- [ ] Workspace.enrichSessionMeta reads from session DB for full metadata

### Phase 3 — Session storage under workspace folder ⬜ TODO
- [ ] Session DBs live under `~/.crude/workspaces/<hash>/sessions/`
- [ ] Session logs/reports live under `~/.crude/workspaces/<hash>/sessions/<id>/`
- [ ] Legacy fallback: if session DB not found in workspace, look in `~/.crude/sessions/`
- [ ] New sessions always created under workspace folder

### Phase 4 — TUI decoupling + launcher becomes workspace selector ✅ DONE (partial)
- [x] Remove OnNewSession/OnResumeSession callbacks from Workspace
- [x] Workspace stores sessionCfg, homeDir, sender (EventSender)
- [x] Workspace.NewSessionFromLauncher() handles full flow
- [x] Workspace.ResumeSessionFromLauncher() handles resume
- [x] Launcher calls workspace methods directly
- [ ] Launcher lists workspaces (not sessions) when no project dir specified
- [ ] Launcher shows workspace-scoped sessions when inside a workspace
- [ ] Resume session stays in same workspace (no quit/restart)

### Phase 5 — App shell wiring ✅ DONE (basic)
- [x] app.Run() creates workspace and starts TUI
- [x] main.go is functional (not yet ~50 lines but works)
- [x] App stores session.Config for reuse via SetBackendConfig
- [ ] Provider presets defined properly

### Phase 6 — Config split and settings persistence ✅ DONE
- [x] Split Settings into AppSettings + SessionSettings
- [x] AppPart() / SessionPart() methods on Settings
- [x] HandleSettingsSave persists app settings to workspace.db
- [x] LoadAppSettings reads from workspace.db
- [x] /settings form has two sections (Session + Preferences) — UI polish for later
- [x] New JetBrains-style workspace settings window (two-column, mouse-enabled)
- [x] Session settings form (provider/model/thinking) as overlay
- [x] Ctrl+P "Settings" opens workspace settings
- [x] /settings opens session settings

## Key Decisions

1. **Launcher = workspace selector** — top-level lists workspaces/projects. Inside a workspace, sessions are managed via agent tabs and slash commands.
2. **No callback chains** — viewmodel calls workspace methods directly. Only OnNewSession/OnResumeSession remain as callbacks (Phase 4 target).
3. **Sessions are workspace-scoped** — listed from workspace.db, stored under workspace folder.
4. **Config split** — app config (visual/tooling) vs session config (AI behavior). Three inheritance levels.

## Files Changed in Phase 1

| File | What Changed |
|------|-------------|
| `workspace/workspace.go` | Removed sessionManager, removed most callbacks, added Sessions slice, NewWorkspace simplified |
| `workspace/bootstrap.go` | Direct session creation via session.NewWithSessionDir, workspace-scoped ListSessions/DeleteSession |
| `workspace/session.go` | Added AgentID, HydratedMessages(), HydratedRefs() |
| `workspace/paths.go` | Added SessionDir(), SessionDBPath() |
| `workspace/db.go` | RegisterSession takes projectPath |
| `session/session.go` | Added NewWithSessionDir() |
| `session/resume.go` | Added ResumeSessionWithDir() |
| `tui/tui.go` | Removed all callbacks, Start takes *workspace.Workspace |
| `tui/viewmodel/app.go` | NewApp takes *workspace.Workspace, removed callback fields |
| `tui/viewmodel/cancel.go` | Direct workspace methods |
| `tui/viewmodel/command_palette.go` | Direct workspace methods |
| `tui/viewmodel/update.go` | Direct workspace methods |
| `tui/viewmodel/editor_tabs.go` | Direct workspace methods |
| `tui/viewmodel/launcher.go` | Direct workspace methods |
| `tui/viewmodel/keyboard.go` | Direct workspace methods |
| `tui/viewmodel/slash_commands.go` | Direct workspace methods |
| `tui/viewmodel/context_menu_builders.go` | Direct workspace methods |
| `app/app.go` | Run() creates workspace, bootstraps session, starts TUI |
