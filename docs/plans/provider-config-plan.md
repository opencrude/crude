# Provider / Agent Configuration Fix — Implementation Plan

> Status: SUPERSEDED (2026-08-21) — the capability-based provider protocol landed (commit c84e4ec); this plan remains as design history. The defects it lists were fixed in that rework.

## Confirmed defects (from exploration)

### 1. Provider name shows in front of "crude"
`app/app.go:96` passes `a.fc.ActiveProvider` (a provider ID like `openrouter`) as the
`modelName` into `workspace.NewWorkspace(...)`. That becomes `Workspace.ModelName` and flows to
the header (`crude  openrouter`) and chat headings. The real main-agent model only replaces it
once a session/turn publishes `UpdateModelMsg` or a settings save lands. So at startup the badge
is wrong.

### 2. Provider settings leak a default model into agent config
- Provider form has a "Default model" field (`tui/settings/settings.go:279`), persisted as
  `ProviderConfig.Model`.
- `session/config.go:305` `if cat.Model == "" { cat.Model = prov.Model }` injects it into any
  agent that doesn't set a model.
- `applyProviderToEffective` (`session_settings.go:307`) copies `providerCfg.Model` straight into
  the current role.

### 3. Agent inheritance is a copy, not a reference
- `inheritMain()` (`session_settings.go:878`) copies the main agent's `CategorySettings` into the
  role **and** records a session override. Once that override exists, `categoryFor()` returns the
  frozen copy and the live `default` fallback is bypassed.
- `resetRole()` / "Reset to workspace" restores `s.base` — a **snapshot** captured at session
  creation, not today's live workspace value.

### 4. Other issues you flagged
- `DefaultAgentsConfig()` bakes provider-prefixed model strings into researcher/crafter/aeron.
- Agent forms expose a "Communication protocol" field, which belongs on the provider, not an agent.
- The workspace/app settings "Main Agent" form lacks the Multi-Agent toggle that `/settings` has.

---

## Target model (from your answers)

Two-level **live** inheritance resolved at read time — never copied:

```
Workspace main (provider/model)  ← 'inherit from workspace' points here
        ↓ (default for a session main that isn't manually overridden)
Session main (manual override allowed)
        ↓ (default for a sub-agent that isn't manually overridden)
Sub-agent (manual override allowed; 'inherit from main' points here)
```

Rules:
- Absence of an override = **inherit**, following the chain dynamically. Editing the source
  propagates to everything that inherits it.
- `Inherit from workspace` on **any** agent points directly at the **workspace** main agent config.
- `Inherit from main` (sub-agent only) points at the **session** main agent config.
- No default model anywhere. All agents (main + sub) start with no model; user configures them.
- Provider settings = connection only (API key / OAuth, base URL, communication protocol). No model.
- Communication protocol is removed from every agent form (main + sub, both surfaces).
- Main agent form (workspace/app settings) gets a Multi-Agent enable/disable toggle.

**Display:** after `crude` show the **model name only**, with any `provider/` prefix stripped
(e.g. `crude  gemini-2.0-flash-exp:free`). The provider name never appears in front of `crude`.

---

## Data model change: explicit inheritance source

A per-category "config source" marker replaces the implicit copy-inherit. A category is one of:

| Source | Meaning | Stored data |
|--------|---------|-------------|
| `manual` | User set provider/model/thinking for this role | provider, model, thinking (the override) |
| `main` | Follow the **session** main agent live (sub-agents) | none (reference) |
| `workspace` | Follow the **workspace** main agent live (any) | none (reference) |

Resolution (read time, live):

```
effective_category(session, category):
  if source[category] == manual          → stored override
  if source[category] == workspace       → workspace_main_category()     # live
  if source[category] == main            → effective_category(session, "default")
  # default ("main"/raw): follow chain
  if category == "default"               → workspace_main_category()     # live
  else                                   → effective_category(session, "default")
```

Key: **"inherit" is represented by the absence of a stored override / the source marker, not by a
copied value.** Deleting a role's override returns it to dynamic inheritance.

### Note on the "sessions snapshot" doc contradiction
`docs/reference/settings.md` states sessions snapshot their agent baseline and stay stable. The new
dynamic inheritance intentionally reverses that for **agent** settings (provider/model/thinking).
This is a deliberate behavior change: a running conversation won't retroactively switch an
in-flight turn's model, but *new* sub-agent spawns and the displayed model follow the live source.
I'll update docs/reference/settings.md to reflect this.

---

## File-by-file changes

### `app/app.go`
- Stop passing `a.fc.ActiveProvider` as the model name. Resolve the actual main-agent model from
  the app settings agent chain (empty if unset) and pass that; seed `Workspace.ModelName` from it.

### `session/config.go`
- `DefaultAgentsConfig()`: remove the model strings on researcher/crafter/aeron (leave policy,
  description, allowed/denied tools). No model anywhere.
- `resolveCategoryDefaults()`: delete the `if cat.Model == "" { cat.Model = prov.Model }` fallback
  so a provider default never injects into an agent.
- Add the chain resolver `EffectiveCategory(category string) CategoryConfig` that follows the
  source markers (manual / main / workspace) live.
- `GetActiveDefaultCategory()` / `GetCategoryConfig()` delegate to the new resolver.
- Remove agent-level protocol concepts where the agent form no longer owns them (keep the stored
  standard for manual overrides, but stop exposing it in forms).

### `session/handlers.go`
- `configWithSessionOverrides()`: apply **source-marked** overrides (`manual` = full field set,
  `main`/`workspace` = reference) instead of always materializing a copy. Absence = inherit.
- `SettingsSnapshot()` / `SessionOverridesSnapshot()` return source markers so the UI can show
  what a role inherits from.

### `sessiondb/settings.go`
- Extend `AgentSettingOverride` with an `Inherit string` column (`"manual" | "main" | "workspace"`)
  and a migration for existing rows → `"manual"` (current behavior is a full copy, so migrate as
  manual to avoid silently changing a user's existing override).
- Update load/save/clear accordingly.

### `session/runtime_profile.go`
- `mergeCategoryConfig` / per-orchestrator effective config: keep field-level merge for manual
  overrides, but resolve `main`/`workspace` sources against the live chain at use time.

### `session/subagent.go`
- Sub-agent spawn already falls back to the main category at launch. Update to use the new resolver
  so an inherited sub-agent picks up the current live main model, not a frozen copy.

### `tui/screens/sessionsettings/session_settings.go` (`/settings` deck)
- `inheritMain(role)`: instead of copying, **set the role's source to `main`** (no stored copy)
  and clear any manual provider/model. Editing the main agent later propagates.
- `resetRole(role)`: instead of restoring `s.base` copy, set the role's source to `workspace`
  (or `main` for sub-agents) — i.e. point back at the live source.
- Add an explicit "Inherit from workspace" action (for main and sub-agents) → source `workspace`.
- Remove the "Communication protocol" field from every role page.
- Root agent table: show each role's effective source (inherited-from label reflects `main` /
  `workspace` / manual), not a hardcoded "workspace".
- Keep the Multi-Agent toggle; expose the same flag for the main agent.

### `tui/settings/settings.go` (Application | Workspace settings — "main settings")
- Provider form: **remove** the "Default model" field and its persistence
  (`storeProviderForm` `cfg.Model = ...`, `fieldModel`, `fieldModelStandard`, and the
  `storeCurrentModelStandard` / `ModelStandards` wiring stays only if still used for manual
  overrides — likely remove entirely). Provider = connection + protocol only.
- Agent forms: **remove** the "Communication protocol" field (Provider + Model + Thinking remain;
  protocol is resolved from the provider at use time).
- Main Agent form: **add** a Multi-Agent enable/disable toggle (mirrors `/settings`).
- `collectAgents()`: stop writing `APIStandard` from the form; never inject provider model.

### Display (model name only, no provider prefix)
- `tui/screens/shellheader/header.go`: keep `crude` + model. The model text must be provider-
  prefix-stripped.
- `tui/chatprojection/semantic_rows.go`: same for `Crude <model>` headings.
- Centralize stripping once: a `displayModelName(model string) string` helper using
  `provider.StripProviderPrefix`, and apply it everywhere `m.ModelName` / renderer model name is
  set, so both surfaces agree. `m.ModelName` must always be the resolved main-agent model, never a
  provider ID.

### `ProviderConfig.Model`
- Keep `Model` field on the struct for JSON round-trip compatibility but stop populating/reading it
  as an agent fallback. `types.ProviderConfig.Model` likewise left as legacy-unused.

---

## Sequencing

1. Data model: add inherit-source marker to `sessiondb`, `types`, `session`, `runtime_profile`.
2. Session resolution: the live chain resolver in `session/config.go`; rewire handlers & subagent.
3. Remove provider→agent model fallback + strip default models.
4. `/settings` deck: inherit semantics (main/workspace as sources), remove protocol field, wire
   main-agent multi-agent toggle.
5. Main/app settings: remove provider default model + agent protocol, add multi-agent toggle.
6. Display: strip provider prefix; fix the initial model-name seed in `app/app.go`.
7. Update `docs/reference/settings.md` (reverse the snapshot note for agent settings, document source
   markers). Update `docs/reference/settings.md` visuals section (providers page no longer has model
   defaults).
8. Update/extend tests: `session_settings_test.go` (inherit main now a live reference, not a
   copy), provider-switch tests (no model injection), agent defaults (no model), display tests
   (model-only after `crude`).

## Validation
- `go build ./...`
- `go test ./tui/editor/... ./tui/viewmodel/... ./tui/workspace/...`
- `go test ./... 2>&1 | grep -v "^ok|cached|^?"`
- `golangci-lint run ./...`
- `scripts/tuishot.sh` capture of `/settings` to confirm the visible inherit labels and that no
  provider name precedes `crude`.

---

## Remaining logic point I want to confirm before coding
The plan treats `main` source (sub-agent inherits session main) as the DEFAULT for sub-agents, and
`workspace` source as the DEFAULT for the session main. That means a fresh sub-agent with no
override implicitly follows the session main — which matches "other agents should be able to
inherit from the main agent". I also treated the existing `snapshot` semantics as intentionally
replaced for agent settings. If you instead want sub-agents to default to the **workspace** main
(only opting into the session main via an explicit "Inherit from main"), that changes only the
default source in the resolver, not the architecture. I've chosen session-main-by-default as the
recommended reading of your "other agents inherit from main agent".
