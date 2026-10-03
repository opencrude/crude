# Workspace Tools, Capabilities, and TUI Interaction Plan

> Status: LANDED — revision 2 core product workflows implemented and validated
> 2026-10-01. The original source audit and sketches remain delivery history;
> non-delivered refinements and evaluation boundaries are listed explicitly.

## Implementation record

- Rail: Files, Agents, Source Control, Skills, MCP, with three-row targets,
  overflow, pin/reorder/reset and workspace presentation persistence; Terminals
  is removed from the rail.
- Git: calm grouped sidebar and one typed reusable main-area review tab,
  index/HEAD/disk comparisons, async prepared diffs, unified/side-by-side,
  explicit stage/unstage, staged-set commit review and retained message drafts.
- Workspace dock: dedicated Memory footer control, retained Memory/Board,
  per-panel preferred heights, maximize/restore/hide, footer overflow and terminal
  picker. Session decisions/settings remain in their own PanelDeck.
- Settings: Installed/Discover Skills/MCP catalogs and retained details, source
  and acquisition controls, authored skill operations, exact install/update
  preview, explicit MCP grants/OAuth/connection actions and resource/prompt
  preview. `/skills`, `/mcp`, `/skill` and `/memory` route through the same owners.
- Session capability selection persists role/root-specific skill modes and tool
  exclusions without widening runtime policy. Workspace-switch generations and
  composer ownership guard late results.

Normal/narrow Linux terminal workflows, full build/tests, zero-issue lint,
docs/design/benchmark gates and affected race suites pass. Protocol limitations
are shown in MCP Overview: no sampling,
elicitation, task extensions, remote channels or rich MCP apps are advertised;
resource/prompt composer previews are text-only. No measured quality, latency or
token-cost improvement is asserted without a baseline. The
[capability workflow guide](../guides/capability-workflows.md) records actual routes
and the temporary capture evidence.

## Acceptance Boundaries And Remaining Refinements

- Verified: 80×24, 120×40, 160×40 and 200×50 Linux captures, dark/light and
  nonzero frame padding; retained Settings deep-links, manual discover/install
  preview, source reading, MCP connection/resource preview, composer skill
  staging, session capability controls, typed Git review, Board/Memory and PTY
  hide without termination. Capture windows/processes are closed.
- Unit/integration coverage complements captures for exact staged-set commit,
  stale discard + Local History inverse, OAuth cancellation/retry/identity,
  pagination/durable agent admission, stale memory correction/extraction and
  workspace-switch/shutdown generations. Scripted provider workflows do not
  establish real-model task quality.
- Later refinements: eligible inspector relocation beyond maximize/hide,
  native-platform runtime captures, signed/private repository authentication and
  curated-source presets, richer catalog facets/update comparison, automatic
  local sandbox enforcement, file-sourced import preview and actionable welcome
  count filters. Git hover actions remain explicit review/context-menu actions;
  broad public marketplace adapters are not claimed.
- First-release boundaries: one reusable Git review (no pinned comparisons),
  whole-file staging, inspection/search-only human Board, bounded latest feedback/
  exposure evidence views, text-only resource/prompt composer previews, and no
  sampling/elicitation/task extensions/rich MCP apps. These are declared limits,
  not functioning placeholder controls.
- Held-out quality, prompt/permission interruption rates and provider-token costs
  require separate measured evaluation. No SOTA parity claim follows from green
  tests or a larger feature catalog.

## 1. Purpose and design boundary

The [capabilities, board, and memory plan](agent-capabilities-board-memory-plan.md)
needs a product design that accounts for the existing IDE. Source Control is
already too constrained by its sidebar. Adding skill catalogs, server controls,
board threads, and memory evidence without resolving placement and ownership
would reproduce that problem across several features.

This document specifies navigation, user controls, settings composition, dock
behavior, capability workflows, and acceptance scenarios together. It preserves
the work on Providers, Accounts, Agents, native controls, and ownership. Git
navigation stays in a quieter sidebar and opens changes in the main content
area. Skills and MCP have complete manual managers inside Settings plus quick
workspace navigation. Workspace memory has its own footer entry. Session
decisions remain in their owning deck.

The runtime plan remains authoritative for capability policy, pinned versions,
passive resume, explicit board reads, and evidence-based memory. The proposed
locations and defaults below need visual review before implementation. The
2026-10-01 direction requires existing local memory/session data to be manually
aligned outside product code, with backups and preservation checks, not automatic
compatibility upgrades. No legacy runtime or presentation path remains. Skills, MCP,
Sources and Acquisition share the existing AI group after Memory in both scopes.

### 1.1 Direction incorporated from user review

| User direction | Revised contract |
|---|---|
| Remove Terminals from the navigation rail completely | Terminal access lives in the footer, dock instance selector, palette and keyboard commands. There is no hidden or optional terminal rail entry to restore. |
| Make Git's sidebar calmer; show changes in the main view | Sidebar shows branch and grouped change navigation. Main-area review owns readable diffs, history details and commit drafting. Git is not a workspace bottom-panel mode. |
| Memory is workspace-wide and deserves a dedicated footer control | Add Memory beside the existing footer destinations and mount the workspace memory browser in that panel, independent of active session. |
| Manual Skills/MCP browsing and control in Settings must match Accounts/Agents quality | Add complete Skills and MCP catalog pages, search/discovery, detail editors, explicit lifecycle actions, and scoped preferences using the existing settings composition. |
| Consider Skills/MCP in the slash menu and navigation rail | Recommend separate visible Skills and MCP entries and `/skills`, `/mcp` manager commands. Both use one capability catalog and the same actions; details are specified below. |

Revision 1 proposed a generic Capabilities rail entry, Git review in the bottom
panel, an optionally restorable terminal rail shortcut, and catalog browsing
outside Settings. This revision supersedes those placement suggestions. It
retains the source audit and shared control/lifecycle requirements.

## 2. Verified starting point

These are source observations, not a claim that every interaction was exercised
in a running terminal. Tests cited by the existing documents were read where
relevant, not rerun for this audit.

| Area | Existing implementation | Consequence for the design |
|---|---|---|
| Navigation rail | [`activitybar`](../../tui/screens/activitybar/bar.go) has four destinations, an eight-column width, three-row pointer targets, bounded badges, and a bottom settings gear. [`activity.go`](../../tui/viewmodel/activity.go) routes all four to one sidebar selection. | Preserve its deliberate target sizes and theme language. Supporting a bottom tool destination requires explicit location and focus state; adding an enum alone is insufficient. |
| Sidebar | Files, Agents, Terminals, and Git share `Tree.Width`, capped at half the window. | Width preference belongs to a view/location, and wide review work needs a different host. |
| Source Control | [`source_control.go`](../../tui/screens/sourcecontrol/source_control.go) combines a single-line commit field, changes, eight recent commits, and branches in one scrolling list. Change activation [opens the file](../../tui/viewmodel/source_control.go), not a comparison. Stage/unstage primarily live in [context menus](../../tui/viewmodel/context_menu_builders.go). | The gap is review and action design as well as width. A larger copy of this list would still be incomplete. |
| Workspace bottom panel | [`termdock`](../../tui/screens/termdock/dock.go) retains Workspace, Problems, Notices, and Terminal views. Footer controls select them without a duplicate mode strip. | Extend this owner rather than adding a second workspace dock. Preserve its restrained status views. |
| Terminals | A sidebar inventory, footer entry, and dock instance rail all navigate terminals; float/dock transfers the retained screen and PTY. | Consolidate everyday selection without losing access to floating instances. Hide and process termination need distinct controls. |
| Session deck | [`session_deck.go`](../../tui/viewmodel/session_deck.go) and [`PanelDeck`](../../tui/ui/panel_deck.go) retain settings, questions, confirmations, YOLO, and jobs per session. Blocking panels own input. | Workspace catalogs and discussions do not belong in this deck. Preserve owner-correlated decisions and background update behavior. |
| Settings | [`surface.go`](../../tui/settings/surface.go) already provides native tables, full-width catalog filters, anchored actions, field validation, retained editors, resizable navigation, scope selection, and nested prompts. | Extend this visual and interaction grammar instead of introducing a different capability settings application. |
| Provider/account/agent separation | Provider templates own transport/protocol; accounts own credentials; agents select accounts and models. Agent editor pages are Model / Instructions / Tool Access. | Preserve these distinctions. An MCP connection is not a model provider account. |
| Welcome | [`launcher.go`](../../tui/screens/launcher/launcher.go) has New Session, New Workspace, shortcuts, recent workspaces, and a resizable split. | Add a bounded workspace summary, preserving the primary actions and recent list. |
| Memory browser | [`memorybrowser`](../../tui/screens/memorybrowser/memory_browser.go) already supports search, selection, pin, and forget with a small content preview. | Extend a real existing feature. Evidence, complete readable details, corrections, and revisions are missing from this browser. |
| Runtime tools | [`PrepareStep`](../../agent/agent.go) can refresh tool snapshots; [child construction](../../session/subagent.go) currently filters a static tool list. [`PolicyAllows`](../../agent/policy.go) permits unknown names by default. | A dynamic registry, effects/scopes, and the same request-boundary contract for children must precede automatic acquisition. |
| Capability runtime | The inspected production packages and `go.mod` contain no managed skills loader, MCP client, acquisition catalog, or board implementation. | These are implementation gaps, not UI that can be exposed by adding switches. |

There is an existing service-specific exception:
[`searchExa`](../../search/engines.go) calls Exa's MCP endpoint directly through
`tools/call`. That provides one search integration; it does not supply a general
MCP client with discovery, connections, authentication management and grants.

### 2.1 Existing settings contracts to retain

Use current code and the [settings reference](../reference/settings.md), not
older screenshots, memories, or historical migration recipes:

- One movable, resizable `WindowFrame` under `OverlayHost`. The frame title
  identifies Application or Workspace Settings; the right pane owns page
  breadcrumbs and counts. Opening an editor does not move or resize the window.
- The left category rail, its search, and its divider remain recognizable.
  Providers, Accounts, and Agents use a full-width filter over a compact table,
  with stable, bottom-anchored action groups and a separate progress/error row.
  Narrow layouts stack those groups without putting errors in button cells.
- Application/Workspace is a pair of `ToggleButton` controls in the bottom
  footer. Model / Instructions / Tool Access are content tabs using `ui.Tabs`.
  They serve different purposes. Some surface/dialog documentation still calls
  the scope selector a tab strip; reconcile that documentation during delivery.
- Accounts and provider templates are separately edited entities. Their
  explicit save/change flows acknowledge persistence; Agent Apply stages a
  preference that the Settings Save action persists. Labels must retain those
  actual transaction boundaries.
- Account/model discovery updates the retained draft and open selector in
  place. Missing or disabled selections never silently select another account.
- Dirty editors and destructive decisions preserve the underlying window and
  focus. Provider/account prompts already validate the owner and preimage.
  Agent inline confirmation controls differ; that is a consistency gap to
  resolve deliberately, not permission to rebuild the agent editor.
- Native text selection, undo, paste, arrows, Tab traversal, scroll capture,
  theme changes, and copy rules remain shared component behavior.

### 2.2 Prerequisites found in the audit

These are verified source paths requiring focused reproductions before fixes:

1. `settings.scope` in [`surface.go`](../../tui/settings/surface.go) checks
   agent/account/template editor drafts, but not ordinary unsaved preferences.
   `ScopeMsg` in [`update.go`](../../tui/viewmodel/update.go) recreates the
   window. A normal preference draft can therefore be discarded on scope change.
2. The workspace branch of `SaveMsg` in `update.go` reports a save error and
   still reaches overlay close. The application error branch exits first.
   Both must retain the draft and offer retry on persistence failure.
3. The [memory browser controller](../../tui/viewmodel/memory_browser.go)
   synchronously searches, pins, and forgets; its screen invokes a query
   callback on edits, and [search errors](../../workspace/memory_service.go)
   become empty results. These paths need cancellable commands, typed failure
   states, owner checks, and full detail access before adding evidence views.
4. Git's specialized sidebar coexists with a computed generic Git list, and
   the rail badge is assigned from both tree status and the Git snapshot.
   Consolidate these projections with the new Git surface. A divergence is a
   risk identified in code, not a reproduced user-visible badge failure.

Do not treat every historical reference as current: the status overhaul plan
explicitly records replacement of its old modal with the workspace bottom
panel. The component-authoring guide also contains older per-screen host
examples. The root shell and current controls govern new integration.

## 3. External comparison: workflow gaps, not a ranking

Primary documentation was read on 2026-10-01. These products document concrete
workflows; this audit did not run comparative task benchmarks and cannot label
one implementation universally state of the art. Experimental behavior and
provider-specific limits are not Crude requirements.

| Reference | Documented workflow | Useful requirement for Crude |
|---|---|---|
| [VS Code layouts](https://code.visualstudio.com/docs/configure/custom-layout) | Views can move, panels maximize/restore, locations persist and reset, and hidden toolbar actions remain reachable. | A tool should outgrow its default location without losing state. Provide keyboard equivalents and layout recovery. |
| [VS Code MCP](https://code.visualstudio.com/docs/agent-customization/mcp-servers) | Search/install, user/workspace configuration, explicit lifecycle controls, logs, resources as context, prompts, per-workspace disable. | A complete path from discovery through useful execution and diagnosis, not just a server toggle. |
| [VS Code tools](https://code.visualstudio.com/docs/agents/run/tools) | Searchable grouped tools with mixed checkboxes/counts; availability separate from approval; explicit tool references; terminal output links; browser verification. | Inspect and control the effective tool set, connect outputs to their owners, and validate real workflows. Do not copy its documented 128-tool ceiling as a universal limit. |
| [Claude Code MCP](https://code.claude.com/docs/en/mcp) | HTTP/stdio setup, scoped configuration, OAuth and reauthentication, server status, resources/prompts, deferred tool discovery and schema refresh. | Authentication, context budget, source identity, and recovery must be first-class contracts. |
| [Claude Code skills](https://code.claude.com/docs/en/skills) and [Agent Skills](https://agentskills.io/specification) | Local/project discovery, manual and relevant automatic invocation, incremental loading of instructions/resources. | A useful skill workflow includes authorship, validation, invocation controls, and exact source resolution. Product extensions are not all part of the standard. |

Crude already has substantial IDE primitives, provider/account editing, child
agents, local history, permission routing, context compaction, and workspace
memory. The relevant gaps are:

| Gap | Current level | Delivery expectation |
|---|---|---|
| Configured skills and MCP tools | Absent managed runtime | Load a project skill; connect an authenticated server; use it in both root and restricted child runs. |
| Discovery and acquisition | Proposed only | Curated/private/local sources, explicit import, exact versions, integrity, deduplicated acquisition, update review, and removal. |
| Tool selection at scale | Static flat tool checkboxes | Searchable source groups, effective access reasons, persistent selection intent, and request-bounded schema loading. |
| MCP context interaction | Proposed but unspecified | Resource selection, prompt arguments/preview, structured results, and accurately declared optional protocol support. |
| Capability observability | No shared product projection | Distinguish available/selected/connected/loaded; expose operation stage, source, failures, retry eligibility, and logs. |
| Source Control review | Basic path operations and short history | Separate staged/unstaged comparisons, readable commit drafting, discoverable actions, and stale-preview protection. |
| Cross-agent coordination | Delegation/completion exists; board absent | Explicit collaboration membership, readable threads/evidence, durable agent cursors, and independent human unread state. |
| Memory accountability | Browser and retrieval exist | Complete record inspection, evidence, immutable revisions, correction conflicts, and useful versus merely displayed feedback. |
| Visual application verification | Command/capture recipes exist; no integrated browser tool catalog | Supply an approved browser capability and retain screenshots/logs as evidence; do not call MCP transport alone verification support. |
| Rich MCP apps and remote execution | Not established by this design | Record as later capabilities requiring separate rendering/isolation/ownership work. A text fallback must not imply an interactive app is supported. |

Do not copy automatic trust or startup rules from another client: Crude's
existing plan explicitly requires passive resume and grants independent from
discovery. Browsing or restoring the UI must never start a server to fill a badge.

## 4. Workspace layout and navigation

### 4.1 Revised destinations

The proposed rail order is Files, Agents, Source Control, Skills, MCP, with
Settings pinned at the bottom. Skills and MCP are separate user concepts:
instructions/invocation versus service connections/tools. Their discovery,
versions, acquisition and grants still share one catalog. Do not add a redundant
Capabilities rail entry above them. Board and Memory live in the footer, and
Terminals is removed from the rail entirely.

| Entry | Default destination | Selection/detail behavior |
|---|---|---|
| Files | Left sidebar | Existing tree and file open behavior. |
| Agents | Left sidebar | Existing roster only. Selecting an agent keeps existing open/restore behavior. |
| Source Control | Left sidebar | Branch, filter, grouped changes and explicit review action. Open changes in the main-area Git review view. |
| Skills | Left sidebar | Available workspace skills, invocation mode and current-session use when relevant; Inspect/Manage opens the exact Settings → Skills item. |
| MCP | Left sidebar | Named connections and actual state; Inspect/Manage opens Settings → MCP; logs can open the workspace output inspector. |
| Settings gear | Existing modal | Application Settings; Workspace Settings remains reachable from the gear menu/palette. |
| Footer controls | Workspace bottom panel | Problems, Notices, Terminals, Board, Memory, Workspace. Board and Memory have stable labeled controls. |

“Selection” here separates inspection from execution. Arrows/hover do not open
a manager or start an operation. Enter, double-click, or the visible Inspect
action opens the relevant detail. Once Git review or an unpinned dock inspector
is visible, keyboard selection may update its target without moving focus.
Skills use an explicit Use action; MCP uses explicit connection/tool actions.

Board-thread and operation-output inspectors are contextual workspace-panel
destinations with clear titles and Back where appropriate. A dock view menu and
palette recover those destinations. Memory's dedicated footer entry selects
Memory specifically; the footer never highlights another mode while it is shown.
Git review and Settings can coexist with an open terminal or Memory panel.

### 4.2 Rail interaction contract

- A destination opens in its assigned host. If visible but unfocused, activation
  focuses it; activating its already-focused button hides that tool surface.
  This intentionally refines today's unconditional active-view toggle.
- Open state, keyboard focus, and attention are separate. Multiple tools can
  be open in different hosts; their rail markers show visibility, while the
  focused tool receives the existing focus treatment. A background acquisition
  cannot move the active marker by stealing selection.
- Preserve the three-row hit bands, inset, separators, and calibrated glyphs.
  Hover and keyboard focus expose the full label and action hint. A short rail
  uses a bounded overflow menu, keeping Settings and all hidden actions reachable.
  Do not shrink pointer targets just to fit more icons.
- Badge meaning is explicit: Git counts distinct changed paths, Agents shows
  agent inventory as today, and Skills/MCP show actionable failures/requests
  rather than catalog size or number installed. Board unread belongs inside the
  Board panel; it does not replace the agent count with a number of a different kind.
  Counts never pulse.
- A shared view menu offers only supported locations and Reset location. Git
  navigation and change review retain their sidebar/main-area relationship;
  Skills/MCP management retains its Settings destination. Eligible inspectors
  may use the workspace panel or existing floating hosts. Do not offer a menu
  option that reinstates the removed terminal rail or duplicates a manager.
- Store each view's location, preferred size, filter, selection, and disclosure
  independently by workspace. Restore passive presentation only. Preserve the
  current main tab and focus return target during location changes.
- Let the user pin/unpin and reorder optional rail entries through keyboard
  actions and the view menu, with Reset navigation. Keep Settings reachable
  independently. Hiding an entry removes a shortcut, never its workspace data
  or palette command. Reset restores the five-entry order above; Terminals is
  not part of the rail catalog.

These rules require a workspace-tool view registry and a shell location owner,
not more special cases on `activityView`. The registry supplies labels, actions,
supported hosts, and badges; it is not a second store of Git/MCP/agent state.

### 4.3 Main area and width behavior

Main tabs represent documents, agent conversations, and explicit Git review.
Opening a change selects one reusable Git Changes view for that repository;
subsequent file selection updates it rather than creating a tab for every file.
Return to previous view/closing review restores the prior live document or
agent selection and focus. Opening the Git sidebar alone never replaces it.
Full comparisons can be pinned separately by an explicit action when supported.

Git review has a stable resource identity, distinct from a filename or tab
caption. The current window helpers reuse tabs by title and dispatch primarily
to editor/agent paths; adding review requires typed ownership and close routing.
Do not disguise it as an editor buffer or agent tab to avoid that integration.
Side-by-side diffs collapse into unified diffs at narrow widths without losing
their base/target identity or horizontal access to exact lines.

Maximizing a workspace panel remains useful for terminals, memory and output;
it temporarily fills the content area and restores the selected main view.

Keep a single retained tool instance when relocating/maximizing. Reparent or
adapt the existing component through shared hosts; do not mount two independent
copies with separate selection, scroll, credentials, or network clients.

At narrow widths, use a list page followed by a detail page with Back and the
same selected item. Do not silently remove essential columns/actions or invent
minimum geometry outside the parent's rectangle. Readable labels and explicit
state take priority over decorative metadata. Frame padding remains governed
by the user's existing four-side settings.

## 5. Bottom panel and session deck

### 5.1 Two scopes, one coherent visual language

| Surface | Owner | Appropriate content | Hiding means |
|---|---|---|---|
| Workspace panel above footer | Workspace | Terminals, Problems, Notices, Memory, Workspace, board inspection, operation output | Presentation hidden; work and selected data survive. |
| Session deck between transcript and input | Session/request | Session settings, tool/activation permission, questions, session jobs | Follow the panel policy; pending decisions are not silently discarded. |
| Settings window | Application/workspace draft and entity operations | Providers, Accounts, Agents, Skills/MCP browsing and management, sources and defaults | Close only through dirty-draft and persistence rules; hiding never implies rollback of a completed operation. |

Use shared shell background, full-width separators, shared tab strips only for
real content modes, and the current theme tokens. A common dock title/action
rail provides Maximize/Restore, Hide, and More; reuse a view's search/title row
where possible instead of adding several rows of redundant chrome. Footer
destination controls remain the one default mode navigation for status views.

For terminal instances preserve the working right-hand instance rail. Below its
usable width, replace it with a named terminal picker, not inaccessible hidden
instances. The picker includes floating instances and brings them forward.
New Terminal, Float/Dock, and Close Terminal stay explicit actions. Generic Hide
must never send `ActionClose` to a PTY. Terminal close remains process termination
and must be visibly named as such, distinct from minimizing an agent tab.

### 5.2 Height and focus arbitration

- Retain a preferred normal height per workspace view. Resizing Problems must
  not overwrite a user's larger terminal or Memory preference. Clamp only
  the presented height; resizing back restores the requested size.
- Terminals retain their current compact default. If a panel cannot fit its
  title, essential actions and a useful body, offer an explicit Expand action
  or open its single-page presentation on the user's request. Do not reuse the
  twelve-row terminal default for every new tool or automatically maximize on
  background events. Git comparisons use the main area and do not consume a
  bottom-panel height preference. Exact panel defaults require visual review.
- Maximize is a reversible layout state, not a change to the stored normal
  height. It does not create a new terminal/server or stop the main agent.
- At ordinary heights the session deck and workspace panel can coexist. On a
  short grid, a blocking decision takes the visible budget first. Temporarily
  suppress a passive workspace panel if necessary, retaining its state for
  restoration. Never squeeze approval controls below usable geometry merely to
  preserve a twelve-row terminal preference.
- Esc in a status/inspection surface returns through its detail/page/close
  hierarchy. Esc and Ctrl+C in a terminal remain terminal input. Provide a
  registered global focus/hide action usable from PTYs without stealing their
  ordinary keys.
- Closing an inspector restores its previous live focus target. A pending
  permission, a removed target, or a workspace switch resolves through the
  existing focus/attention owner; never restore focus to an unmounted component.
- Background data updates never open, resize, maximize, or select a dock.
  Current owner-correlated attention routing remains authoritative for actual
  user decisions, including decisions from a background child.
- On workspace switch cancel old view requests, reject stale results, clear
  project-owned projections, and restore the target workspace's passive layout.
  Session switching keeps each session's own deck draft and decision identity.

### 5.3 User control vocabulary

| Control | Result | State it must preserve |
|---|---|---|
| Inspect / Back | Open detail; return to its originating list | List filter, selection and scroll |
| Pin inspection | Keep the inspected resource while selection elsewhere changes | Exact resource identity and refreshed data |
| Focus tool / Focus main | Move input ownership to that live surface | Main tab selection, PTY state and drafts |
| Hide panel | Remove the workspace panel from layout | Connections, processes, operation results and normal size |
| Maximize / Restore | Temporarily use the main content region / restore | Prior layout and resource selection |
| Move view / Reset location | Relocate an eligible retained view | One instance, query, disclosure and focus return |
| Cancel operation | Request cancellation of one identified operation | Other users of shared acquisition/connection work |
| Close terminal / Terminate agent | Explicitly stop the named runtime owner | Unrelated terminal/agent owners |
| Disable capability | Prevent future selected-scope use under runtime policy | History and evidence; no claim of undoing effects |
| Reset layout | Restore default presentation locations and sizes | Data, credentials, permissions and active work |

Register these as actions so menus, palette, hints and keyboard dispatch agree.
Use the existing shortcut vocabulary and resolve collisions before adding
bindings. Resource selection, disclosure, and command buttons must remain
visually distinguishable; a row click must not unexpectedly execute a mutation.

### 5.4 Memory in the footer

Add the labeled Memory control to the existing workspace strip in the order
Problems, Notices, Terminals, Memory, Workspace. It remains present when memory
is empty or automatic extraction is disabled: browsing is a workspace feature,
not an agent-lifecycle badge. Avoid a permanent total-record count; a real store
failure can show attention and open a useful recovery state. Selection uses the
existing active/footer token treatment, without a new color dialect or spinner.

Memory selects a retained workspace-panel browser with search, filters and
full record detail. `/memory`, its existing shortcut, the palette and footer
all invoke the same browser owner; they must not leave a second stale overlay
with independent results. Opening from editor, Welcome, or any agent tab shows
the same workspace records and preserves the browser's selection/query.
Workspace switching changes the owner and invalidates old reads/corrections.

Five labels need a deliberate overflow contract. First omit optional counts
and low-value hints, then keep Memory, Workspace, any selected destination and
actionable attention visible while space permits. Less urgent controls move
into an explicit More menu in their stable order; never simply disappear.
At extremely small widths all destinations remain in that menu and palette.
Reserve the overflow target before fitting labels, never clip a pill midway,
and avoid reordering controls under a pressed pointer when a count changes.

## 6. Settings aesthetics and user controls

### 6.1 Compose capabilities from the existing catalog grammar

Add explicit Skills and MCP catalog pages alongside Providers, Accounts and
Agents. Each offers Installed / Discover browsing inside the right pane and
retained detail/edit pages with Back. Users must be able to find, inspect,
install/configure, update, disable and remove a capability entirely through
Settings without an agent performing the task for them.

Sources and Acquisition are shared configuration pages reachable from both
catalogs. The rail entries are compact projections for everyday workspace use;
they are not the only place to browse or configure. One catalog, entity service
and action implementation serve Settings, rail, slash menu and session pickers.

| Page | Main controls | Scope |
|---|---|---|
| Skills | Installed/Discover; search/filter; Inspect, Create, Add local, Install, Update, Disable, Remove; invocation mode and source inspection | Application or workspace catalog with explicit origin and selection scope |
| MCP | Installed/Discover; searchable connections; Add, Import, Inspect/Edit, Install/configure, Sign in, Connect/Disconnect, Update, Remove; tools/resources/prompts and logs | Definition at chosen app/workspace scope; secrets application-owned and endpoint-bound |
| Sources | Searchable table; Add/Edit/Remove; local directory, repository, registry; supported contributions; precise validation and last refresh error | Application definitions; workspace source choices/overrides |
| Acquisition | Automatic use of available capabilities; acquisition mode; approved sources/access limits; explicit update policy; retry/time/download budgets in advanced details | Application default with leaf-level workspace override |
| Agents → Tool Access | Built-in/MCP/skill groups, search, individual selection, role ceilings, effective reasons, acquisition permission | Existing agent profile/category scope |
| Session settings → Capabilities | Inherit/profile override, current selection, loaded version, pending changes, and link to detailed inspection | Owning session/agent, never implicit promotion to workspace defaults |

Installed and Discover are actual content tabs using the shared `ui.Tabs`;
Application/Workspace remains the existing footer scope switch. These controls
must never silently change one another. Catalog rows show identity, source and
useful state; long descriptions appear in the detail page rather than increasing
every row's height. Search, filters, selected row, paging and scroll survive Back.

Application scope edits application definitions. Workspace scope shows the
effective inherited rows plus local definitions/overrides, with their origin
and Reset to inherited action. Editing an inherited connection creates a
workspace override only for editable non-secret fields; changing its credential
uses an explicit application-owned auth flow. It must never silently edit the
application entity from a workspace table. The operational catalog may inspect
both origins together without changing these write boundaries. New entries
created from a workspace rail/slash command default to that workspace's catalog;
sharing across workspaces is an explicit scope choice.

Keep provider model credentials in Accounts. Reuse credential editing and
OAuth interaction primitives for MCP, with a separate connection identity,
endpoint and grant record. Reusing a form does not make model accounts and MCP
authorization interchangeable. Remote setup needs browser sign-in, a usable
headless return flow when supported, cancellation, expiration, reauthentication,
and redacted failures. A user can inspect or disable an unauthenticated entry.

### 6.1.1 Skills catalog and detail controls

Installed rows contain name, source, invocation mode and availability. Discover
adds publisher, supported environment and acquisition state. Filter by installed,
local/managed, source, scope, enabled and needs attention. Offline search keeps
the last cached results marked as cached; explicit refresh has cancellation and
a failure state distinct from no matches.

Opening a skill retains the Settings frame and replaces the right table with a
breadcrumb detail page. Its content pages are Overview, Instructions, Files,
and Access & use; Versions appears for a versioned managed package. The user
can read the complete instructions, inspect scripts/reference assets, see
requirements and requested tools, compare an update, and open an authored file
in the native editor. Managed artifacts require Make local copy before editing.
File browsing reuses existing tree controls and containment checks.

Automatic / Manual only / Disabled controls invocation preference separately
from permission. Scope labels distinguish the workspace default from an agent
override. Use in session names its target agent and stages a source-qualified
invocation in that composer's draft; it does not send the prompt automatically.
With no eligible session, browsing/management still work and Use explains what
is needed. Create skill and Add local validate before exposing the skill.

Install/Update previews the exact revision, dependencies and access for the
selected scope, then uses the same bounded acquisition operation as an agent.
Remove identifies whether it removes a local registration, workspace binding,
or globally unused managed artifact. Removing a registration must not delete
the user's authored directory or another workspace's selected version.

### 6.1.2 MCP catalog and detail controls

Installed rows summarize name, transport, origin/scope and connection state.
Discover supports publisher/source/transport/compatibility filters and manual
configuration of a remote endpoint or local command without registry discovery.
Add and Import show precise field errors and unsupported definitions. Multiple
named connections to one service remain distinct; neither shares credentials
silently with another or with model-provider Accounts.

The retained detail/editor offers Overview, Connection, Tools, Resources,
Prompts, Access, and Activity as supported. Connection exposes URL or executable
plus arguments, working directory, allowed environment/secret references,
headers and authentication, with timeout/reconnect controls in advanced fields.
Tools supports search, schemas, readable descriptions, selection and effective
access reasons. Resources/Prompts support explicit browse, fetch/preview and
composer attachment; cached discovery is labeled when the connection is stopped.
Activity exposes connection failures and full redacted logs.

Connect, Disconnect, Sign in/out, Test connection, Refresh tools, Update and
Disable are explicit operations, never side effects of opening the page. Only
supported and applicable actions appear, with disabled reasons when relevant.
After a successful Test of a detached draft, report the tested revision; Save
or Save and connect is still required to persist/use changed configuration.
Known operational actions share the rail's commands and owner IDs.

### 6.1.3 Manual browsing is a complete product workflow

A user can open Settings → Skills → Discover, inspect the package, select scope,
install, return to Installed, set Manual only and stage its use in a session.
For MCP the equivalent path adds sign-in/connection and tool selection. These
flows must work with automatic acquisition disabled and without provider calls.
Only an explicit submitted task starts agent work. Catalog queries, downloads,
sign-in, connection tests and resource fetches perform their named operations
directly and display their own progress; opening a row triggers none of them.

### 6.2 Visual rules

- Preserve the shared left and right text rails, pane breadcrumb treatment,
  compact row height, full-width search, muted metadata, and bottom actions.
  A source row summarizes name, kind, scope, and state; version/access details
  expand or open the retained right-pane editor. Avoid nested bordered cards.
- Color describes existing control semantics: accent for focus/selection,
  muted for inherited/supporting text, warning for required attention, red for
  actual failure/destructive action. A connected service needs a readable state,
  not a permanently saturated green tile.
- Keep buttons sized to their labels. Use verb labels such as Add source,
  Save connection, Connect, Review access, Retry, and Disable in workspace.
  Place long secondary actions in More; primary recovery remains visible.
- Show the reason a control is unavailable beside the selection or in its
  accessible detail. A disabled checkbox without “Denied by Researcher role”
  does not explain effective policy. Do not expose controls for settings that
  the runtime cannot enforce.
- Busy operations retain their source row and action geometry. Disable only
  conflicting actions; preserve cancellation and navigation. Result arrivals
  cannot replace a typed draft, close a selector, or move the row under a click.
- Keep the modal's initial readable sizing, allow deliberate maximize/restore,
  and restore saved geometry within the new terminal. On narrow layouts, the
  category rail becomes an explicit navigation page/picker with Back; the active
  form keeps its draft. Scope and Save/Cancel remain visible.
- Model / Instructions / Tool Access stay intact. Dynamic capabilities expand
  Tool Access; they do not replace the model selector or Multi Agent control.
- Extend settings search beyond today's category/group-name matching to field
  labels, descriptions, source names and setting identifiers. Results identify
  their scope/page and focus the actual control. Searching must preserve the
  current editor draft; switching to a result observes the same dirty rules.

### 6.3 Transaction boundaries

Preference editing, entity persistence, and runtime operations are different:

1. Settings Save persists staged preferences. Failure keeps the exact draft.
2. Save connection/source persists that entity through its owner-validated
   operation, matching the established provider/account entity pattern.
3. Install, Connect, Disconnect, Test, and Update are explicit operations with
   progress/cancellation. They do not wait for an unrelated Settings Save.

An operation on an unsaved connection must either test its detached draft or
explicitly say Save and connect. Cancel Settings cannot pretend to roll back an
installation already completed. Test results carry the tested draft revision
and cannot label a later edit “Connected”. Navigating to operational details
preserves the settings draft beneath/behind it or resolves it explicitly; no
close-and-recreate shortcut may discard it.

## 7. Capability catalog, activation, and tool presentation

### 7.1 State is multidimensional

| Dimension | User meaning | Examples |
|---|---|---|
| Acquisition | Bytes or definition are present | Not acquired, acquiring, installed at revision, update available |
| Workspace access | Runtime policy permits this use | Available, disabled here, needs permission, unavailable on this platform |
| Agent selection | This agent may choose it | Inherited, selected, excluded, role denied, pending next request |
| Skill context | Instructions have actually been loaded | Not loaded, loaded at revision, used this turn |
| MCP connection | Transport and authentication state | Not started, connecting, connected, needs sign-in, failed, disconnecting |
| Operation | One request's actual outcome | Running, succeeded, failed, cancelled, result unknown |

Never collapse these into one green “Active” bit. The summary chooses the
most useful state; detail reveals the dimensions and reasons. Loading skill
prose does not mean its script ran. Disabling future use cannot erase instructions
already in an agent's context or reverse completed side effects.

### 7.2 Catalog and inspection

Settings → Skills and Settings → MCP own complete Installed / Discover browsing
and the detail pages described in §6.1. The corresponding rail sidebar offers
compact workspace rows, local filtering, state, Inspect/Manage, and explicit
quick actions such as Use or Connect. Browse catalog opens the matching Settings
Discover page. No essential management action is exclusive to the rail.

Inspect and Manage deep-link to the same retained settings page/entity. If
another settings editor has an unsaved draft, navigation observes its existing
discard/back contract; it cannot silently replace that draft or spawn a second
settings application. Preserve each catalog's filter/scroll on return. Search
external sources through a bounded operation; merely opening Installed or the
sidebar does not search the network.

The inspector answers: what does this add, where did it come from, which version
is selected, what access is requested/effective, which agents use it, and why is
it unavailable? Offer exact source/instructions inspection and links to logs.
Actions depend on state: Install and enable, Use in this session, Sign in,
Connect, Disconnect, Update, Disable here, Remove. Explain dependent active
agents before a disruptive action, through the existing confirmation owner.

Support authored/local skills as a real workflow: open the source folder/file,
validate frontmatter and referenced assets, refresh a changed revision, and use
an explicit invocation. Name collisions show source-qualified choices; no
silent winner. Import a supported MCP definition from pasted JSON or a selected
configuration file with a preview of normalized entries, missing secrets,
scope, and unsupported fields. An external-format adapter is interoperability,
not a compatibility migration for Crude's development data.

Offer Create skill as a local authoring action: choose project or user scope,
create a minimal standard template, and open it in the existing editor. Validate
before making it available; report unsupported client-specific frontmatter.
Managed cached packages remain immutable, so Edit creates a named local copy
instead of corrupting the acquired digest.

The composer gets a searchable capability picker through a visible control and
palette action, with explicit categories for Skills, Tools, Resources and
Prompts. A skill inserts a source-qualified invocation chip; a tool reference
expresses user selection while preserving approval requirements. Built-in slash
commands keep their identities; any shorthand must resolve collisions visibly.
Typed prose remains usable without chips. The same descriptor and selection
service powers this picker and Tool Access, avoiding divergent catalogs.

### 7.3 Automatic use and acquisition

Provide independent settings for automatic selection of available capabilities
and acquisition of new ones. Proposed defaults: relevant automatic selection
enabled; new acquisition asks unless a source and exact access envelope already
have a grant. Users can choose Manual, Ask, or Automatic within approved limits.
Explicit user invocation still passes runtime access checks.

The model sees compact descriptors and a bounded search/activation path. It
identifies a concrete gap, searches installed entries first, then allowed sources,
selects an exact candidate, and activates through one managed operation. A
keyword match alone is insufficient reason to install. Task text must not be
sent wholesale to a public registry merely to construct a query.

The visible activation operation shows the requested agent, task reason,
publisher/version, intended scope, required access, and actual stages. Reuse
the canonical tool/operation record across transcript, dock, and Notices.
Already granted acquisition does not produce repeated permission popups. A new
credential/access request uses the existing owner-correlated permission flow.
If denied, report the unavailable capability and continue with an adequate
available method when one exists; do not repeatedly ask or silently relax policy.

Downloads are deduplicated by immutable identity; cancellation detaches one
requester without aborting another agent's valid use. Activation is atomic;
partial installs stay unusable. Start local processes only where filesystem,
network, and environment restrictions are enforceable. A configured command
that runs successfully is not proof of such enforcement. Show platform/runtime
limitations precisely.

Tool schema and skill versions change at safe request boundaries. Existing
parallel batches retain their descriptor snapshot, while execution checks
current revocation and operation permissions. Revocation can prevent a later
call; it cannot retroactively undo a remote mutation already sent. Ambiguous
mutating outcomes are “Result unknown” and never get a generic automatic retry.

Resume restores selected identities and context history without downloads,
updates, server startup, or tool replay. Connection state is then Not started
or cached/unverified, with explicit activation on subsequent authorized work.

### 7.4 Effective tools and context budget

Within Tool Access and the session capability inspector, separate Skills from
executable Tools. Skills expose automatic/manual/disabled selection and loaded
revision; their scripts still use native execution permissions. Tools group by
Built-in and MCP connection. Search groups and entries; support group checkboxes
with mixed state, enabled/available counts, and a per-tool reason.
Store selection intent by stable identity, so a temporarily disconnected source
does not become unchecked or disappear permanently.

Distinguish “selected for this agent” from “schema sent in this request”. Load
only relevant schemas within the producing model's real context/tool limits.
Keep a bounded discovery mechanism available, include child agents, and record
why definitions were excluded. If a provider cannot support deferred discovery,
send a bounded explicit selection rather than silently omitting required tools.
Do not claim a token savings amount without measurement.

Group selection means the known selected set, not execution approval. Newly
discovered or changed operations must satisfy effect/scope grants before
becoming executable. A server's read-only annotation is not the permission
authority. Parent, category,
profile, session, workspace and user restrictions must have an inspectable
effective result; the UI must not let a child exceed its ceiling.

### 7.5 Resources, prompts, and protocol coverage

Tools are only one MCP surface. Resources need an explicit context picker wired
to the existing attachment/chip flow, with source URI, fetch time, size, and
failure state. Prompt templates need argument controls and a preview before
insertion into the composer. These records retain source attribution and do not
masquerade as user-written instructions. Selection/cancellation follows the
owning draft; late fetches cannot attach to another session.

Protocol implementation must declare its coverage. First release covers managed
stdio/Streamable HTTP, authentication, bounded tool/resource/prompt discovery,
schema changes, structured results, progress, timeouts and cancellation. If
form elicitation is implemented, route it through owner-correlated native
question controls and support reject/cancel; until then report it unsupported.
Do not advertise sampling, task extensions, remote channels, or rich MCP apps
merely because an SDK exposes the API. Each needs its own permission, budget,
presentation and recovery contract. Terminal-native text, tables, links and
attachments are useful results without pretending to render arbitrary web apps.

### 7.6 Transcript and output

Extend the existing [chat activity contract](../reference/chat-activity.md).
Use source/action/operand spans, with full exact identity in disclosure:

```text
Use skill       PDF extraction · Anthropic
GitHub          List issues · project/repository
Board           Post result · Mira → Theo
```

Calls remain one canonical record. An acquisition may have expandable stage
events, but summaries must retain its underlying call outcomes and permissions.
Unknown dynamic tools stay visible until explicitly classified; read-only
success may compact under existing rules, while writes/errors/unknown results
retain evidence. Never print raw argument envelopes as titles.

Expanded output uses bounded text/table/code/attachment adapters, with search,
copy, complete-output access, source link, duration, and actual outcome.
Redaction applies before storage/display where required. A failed tool result
inside a successful transport remains a failure. Live, parallel, completed and
resumed projections must agree. A Hide action on output is not Cancel operation.

### 7.7 Slash commands and manual invocation

Management shortcuts are local UI commands. They work without a provider
request and do not install, connect, grant access or execute tools merely because
the user opens them. Proposed initial command contract:

| Command | Behavior |
|---|---|
| `/skills` | Open/focus Settings → Skills → Installed for the current workspace, or Application scope when no workspace is selected. |
| `/skills <query>` | Open the same catalog with that local filter; Discover is an explicit user choice. |
| `/mcp` | Open/focus Settings → MCP → Installed with current connection state. |
| `/mcp <query>` | Filter connections in that manager; never interpret query text as a command to execute. |
| `/memory` | Open/focus the workspace Memory panel, matching its footer control. |
| `/skill <source-qualified-id> [arguments]` | Explicitly invoke that skill as a task when the user submits the composer. Autocomplete selection stages it without sending. Missing/unavailable skills show a resolution/setup choice. |

Autocomplete clearly separates management commands from skill invocations and
MCP prompt templates. `/skills` is reserved management syntax; a skill cannot
replace it or another built-in command. Exact item IDs travel in selection
events even if labels are shortened, and colliding human names show publisher
or source. Long argument text retains newlines/quotes rather than being rebuilt
from `strings.Fields`. Unsupported invocations produce a useful message and
preserve the draft.

Autocomplete and Use in session are insertion actions, while submitting a fully
specified `/skill` task is the normal single submit gesture. Do not force two
submits or an extra confirmation just because a skill was explicitly selected;
only missing inputs or actual permission requirements interrupt it.

The existing [command list](../../tui/input/input.go),
[dispatcher](../../tui/viewmodel/slash_commands.go), palette, and Help must use
shared command descriptors so labels, filtering and actual routes agree.
Bound dynamic suggestions by the query; do not append every server tool to the
top-level slash menu. For MCP, selecting a prompt opens its argument/preview
flow; selecting a tool stages a tool reference. Neither selection runs a tool.

Opening a manager from the composer preserves existing draft text, attachments
and selected target agent. Closing restores focus; using a skill/resource from
the manager validates that owner before insertion. The user can also operate
everything through Settings or rail controls without knowing slash syntax.

## 8. Calm Git navigation and main-area change review

### 8.1 Sidebar content and visual hierarchy

The sidebar contains a concise repository/branch header, a filter, and grouped
Conflicts (only when present), Staged, and Changes rows. Untracked files have a
small status mark within Changes. A file with staged and unstaged edits remains
in both applicable groups, but the rail badge counts the distinct path once.
Empty groups collapse to a quiet summary. Full history and branch inventories
move to explicit History/Branches actions that open the main review view.

Use normal foreground for filenames, muted path context and small semantic
status glyphs. Conflicts/failures get attention; ordinary modified filenames
do not become a column of red text. Diff additions/deletions retain meaningful
color inside the comparison. Hover reveals relevant inline actions and keyboard
focus makes the same actions available; ordinary selection never stages or
discards. Keep a stable Review changes action and selected-file actions in a
bounded toolbar/More menu instead of a long permanent command list.

Give filenames priority over ancillary metadata. Display enough parent path to
distinguish duplicate basenames, and expose the complete relative path in a
selected-row detail/hover and Copy path action. A long name may use measured
ellipsis in the list, but full identity is always readable in the main view.
Do not repeat truncated paths, hash fragments, counts and branch subjects in
each row. Filtering, disclosure and refresh preserve the selected path and do
not continuously unfold groups or replace the main view.

### 8.2 Main-area review

Opening a changed file or Review changes opens one reusable Git Changes view
in the main tab strip, retaining the prior document/agent tab. Its Changes,
History and Branches content modes use the shared `ui.Tabs` where useful.
The sidebar remains the primary file navigator; a duplicate change list is
shown inside review only when the sidebar is unavailable or the user requests it.

The header identifies repository, complete selected path, and compared versions.
An unstaged row compares working tree to index; a staged row compares index to
HEAD. Provide unified and side-by-side modes, next/previous change/hunk, line
numbers, search/copy, and actual horizontal access. At narrow widths choose
unified mode while preserving the user's preferred wide layout. Renames,
deletions, binary files, empty diffs and untracked files have explicit readable
states; unavailable comparison data is not rendered as an empty successful diff.

Stage file, Unstage file and Open file are explicit actions. Review/Commit opens
a dedicated pane/page inside this main review view: the exact staged set,
multiline message, validation and Commit staged button. Draft text survives
file browsing, tab switches, and failed commits. Keep an unchanged draft when
the staged set changes; require reviewing the new set before submission rather
than committing an unseen selection.

Discard is a clearly named secondary action. Preserve the existing Cancel-first
confirmation and untracked-file protection; add a versioned preview and the
required recovery path before applying destructive changes. History selection
opens commit evidence. Branch selection inspects before an explicit Checkout;
ordinary navigation never mutates the repository.

The Git service needs comparison data and preconditions. Editor buffers can
contain unsaved text: show whether the diff is disk/index/buffer, and stage the
reviewed bytes or require an explicit save decision. Never claim a file-stage
action stages an unsaved buffer. A changed index/HEAD/buffer invalidates a
mutation preview instead of applying the wrong selection.

Initial delivery can stage whole files with accurate comparisons and commit
review. Hunk staging, conflict resolution, graph history, worktrees, and remote
operations are separate capability increments with their own backend and
acceptance tests. Their controls must not be drawn as working placeholders.

## 9. Board, memory, and welcome

The footer Board panel identifies the collaboration before listing participants
and threads. A thread opens in the workspace inspector with sender/recipient,
type, replies, evidence and availability. Human unread and each agent's accepted
read cursor are independent. Viewing a thread does not deliver it to an agent.
No post, HOLD, VETO, or UI unread change wakes workers or approves access.
Initial UI focuses on inspect/search/evidence; any future human-post action needs
an explicit authorship contract rather than impersonating an agent or turning
a comment into a scheduler command.

Move the existing memory browser body into the retained workspace Memory panel
behind the dedicated footer control, then enhance it with complete scrollable
and copyable detail, evidence navigation, revision history, applicability, pin,
correction and retire. Correction uses the expected revision and preserves the
draft on conflict.
Expose missing evidence honestly. Keep knowledge and episodes distinguishable,
and separate retrieval exposure from verified feedback. Do not generate one
unread notification for every ordinary extracted memory. Entry from any agent
shows the whole workspace; an optional session/source filter is labeled and
never inferred silently from the currently selected chat.

Welcome adds a small summary for the current workspace, underneath its primary
New Session action where height permits. It uses cached workspace projections,
not scans of every server/session. Examples describe content, not new badge art:

```text
Skills  6 available · 2 loaded by running agents       Browse skills
MCP     2 connected · 1 needs sign-in                  Manage MCP
```

With no selected workspace, show application availability and omit workspace
claims. With no running agents, omit loaded counts. Last-known connection data
is labeled as such. Counts link to matching Settings catalog filters; a
needs-sign-in link targets that connection's setup. On short grids collapse to
one useful summary/action before sacrificing New Session or recent workspaces.
This is onboarding and orientation, not a dashboard of constantly animated
health indicators.

## 10. Concrete layout sketches

These are content/geometry sketches, not screenshots or final token/color
acceptance. Draw using native components for visual review before shipping.

### 10.1 Desktop: calm Git sidebar and main-area review

```text
workspace / branch                                         selected review path
───────────────────────────────────────────────────────────────────────────────
rail   │ Source Control  │ main.go   Mira   Git Changes
Files  │ main            │─────────────────────────────────────────────────────
Agents │ Search changes  │ parser/scan.go      Index → Working tree
Git    │ Staged      2   │ Stage file   Open file   Review commit   More
Skills │ parser.go   M   │─────────────────────────────────────────────────────
MCP    │ tokens.go   A   │ old / new line numbers and readable diff
       │ Changes     1   │
       │ scan.go     M   │ full content, search, copy, next/previous change
       │                 │
       │ Review changes  │
       │ History  More   │
gear   │                 │
───────────────────────────────────────────────────────────────────────────────
Help                          Problems  Notices  Terminals  Memory  Workspace
```

The Git sidebar stays compact while comparison occupies the main view. Other
document/agent tabs remain available. The workspace bottom panel is optional
and can still show a terminal, Memory or operational output during review.
Rail labels in this sketch identify destinations; product paint retains the
shared glyphs, three-row hit bands and full-label hover/focus treatment.

### 10.2 Settings: Skills catalog

```text
╭ Application Settings                                      maximize  close ╮
│ Search settings ⌕      │ Skills  6                                        │
│                        │ Installed   Discover                             │
│ AI                     │──────────────────────────────────────────────────│
│   Providers            │ Search skills ⌕                                  │
│   Accounts             │ Name          Source       Use          State    │
│   Agents               │ PDF           Anthropic    Automatic    Available│
│   Skills               │ Review        Personal     Manual only  Available│
│   MCP                  │ Release       Team         Disabled     Installed│
│   Sources              │                                                   │
│   Acquisition          │                                                   │
│                        │                                                   │
│ Security               │ [reserved progress / error row]                  │
│ IDE                    │ Create   Add local   Inspect   More              │
├───────────────────────────────────────────────────────────────────────────┤
│ Application Workspace  │                              Save       Cancel   │
╰───────────────────────────────────────────────────────────────────────────╯
```

Discover uses the same frame/filter/table composition, with Inspect and Install
for an exact candidate. Opening its detail replaces the right pane and preserves
the catalog state. Existing Providers/Accounts/Agents pages keep their density,
scope switch, and established editing behavior.

### 10.3 Settings: MCP catalog

```text
╭ Application Settings                                      maximize  close ╮
│ Search settings ⌕      │ MCP  3                                           │
│                        │ Installed   Discover                             │
│ AI                     │──────────────────────────────────────────────────│
│   Providers            │ Search MCP connections ⌕                         │
│   Accounts             │ Name          Transport    State                 │
│   Agents               │ GitHub work   HTTP         Connected             │
│   Skills               │ Browser       stdio        Not started           │
│   MCP                  │ Database      HTTP         Needs sign-in         │
│   Sources              │                                                   │
│   Acquisition          │                                                   │
│                        │                                                   │
│ Security               │ [reserved progress / error row]                  │
│ IDE                    │ Add   Import   Inspect   Connect   More          │
├───────────────────────────────────────────────────────────────────────────┤
│ Application Workspace  │                              Save       Cancel   │
╰───────────────────────────────────────────────────────────────────────────╯
```

The selected row determines Connect/Disconnect or Sign in actions. Details open
through pane breadcrumbs without changing the window title or position. At
narrow widths, Skills/MCP each use a full body page with Categories/Back.

### 10.4 Narrow main-area Git review

```text
Git Changes                                    Return to agent
Changes   History   Branches
───────────────────────────────────────────────────────────────
Back to changes     parser.go · Working tree against index

wrapped/unified diff with horizontal access when needed

Stage file     Open file     Review commit     More
```

Back restores list selection/scroll; resize to a wider grid restores the split
without resetting the review. Inspection may request a bounded background read;
selection/mode changes never mutate Git or invoke an MCP operation on the
user's behalf.

## 11. Shared controls and ownership work

Reuse `Host`, `WindowFrame`, `OverlayHost`, `Form`, `Table`, `List`, `VirtualList`,
`Tabs`, `SplitModel`, `ScrollbarControl`, native editors, and `ActionRegistry`.
The missing layer is consistent product composition and lifecycle metadata.

| Addition | Responsibility | Must not become |
|---|---|---|
| Workspace tool view registry | Stable IDs, labels, supported hosts, open/focus/hide/reset commands, badges | A scheduler or store of service state |
| Main review resource | Typed repository/comparison identity, reusable review tab, correct open/close/return routing | A fake editor buffer, agent tab, or a label used as identity |
| Shell layout preferences | Per-view location/normal size, reversible maximize, focus return, workspace generation | Another component geometry writer |
| Shared catalog composition | Filter, compact rows, stable selection, status row, actions, narrow detail navigation | One giant generic screen forcing all domains into identical fields |
| Grouped access control | Mixed selection, search, inherited/effective states and reasons | A permission grant implemented only in the renderer |
| Operation output view | Stable operation identity, phases, actual progress, owner, cancellation/retry eligibility | A second transcript or fabricated percent complete |
| Credential/setup flow | Endpoint-bound secret references, sign-in ownership, redaction and retry | Reuse of a model account for unrelated service credentials |
| Capability command routes | One command catalog for Settings deep links, `/skills`, `/mcp`, manual invocation and Help | Parallel catalogs or provider calls triggered by browsing |

Workspace supplies immutable, presentation-neutral snapshots and typed action
results. At minimum capability data separates entry/revision, availability
reason, agent binding, connection state, grant, and operation; board data carries
collaboration/thread/cursors; memory data carries record/revision/evidence.
All asynchronous requests identify workspace generation, owner/view epoch,
resource ID and expected revision/request sequence. Close, resize, or selection
changes cannot route a late result to another owner. Shared service work can
outlive a hidden view; cancelling a view read is not cancelling that service.

Every action needs a visible label, keyboard/palette path, enabled reason,
owner/scope, effect, pending/result state, and correct close/cancel behavior.
Expose dangerous actions deliberately, preserve safe default focus, and keep
raw secrets and protocol fields out of ordinary user flows. Hover/focus paints
must not rebuild controls or rewrite policy.

## 12. Delivery and acceptance

Ship vertical workflows with real controls and runtime outcomes. Do not build a
large catalog UI whose buttons have no implementation, or a managed client that
requires editing raw configuration to recover from every failure.

This is the proposed UI work order, not a scheduler for the domain plan. Memory
correctness and board/runtime foundations can proceed independently; finishing
the shell is not a reason to postpone a demonstrated data-correctness fix.

1. **Baseline and design acceptance:** reproduce the identified settings/memory
   paths; capture existing settings, rail, dock and Git at normal/narrow sizes;
   agree the proposed location and control language using native mock data.
2. **Workspace tool shell:** registry, rail routing, per-view sizes,
   terminal rail removal, footer Memory and terminal picker, focus/height
   arbitration, typed main-area Git review, and layout recovery. Remove
   superseded presentation paths as they cut over.
3. **Configured capability workflow:** one local skill and one authenticated MCP
   connection, native settings, exact agent/tool selection, useful output,
   cancellation and diagnosis. Runtime metadata/root-child enforcement land
   before executable dynamic tools.
4. **Acquisition and discovery:** curated/private sources, import, pinned cache,
   policy controls, updates, task-driven activation, and welcome summaries.
5. **Wide review and collaboration:** Git comparisons/commit review, board
   membership/thread UI, and evidence/revision memory. Domain work can proceed
   independently once identity, snapshot and ownership interfaces are fixed.
6. **Measured validation:** evaluate task success and user interaction cost,
   not the number of catalogs, plugins or posts. Tune only after representative
   workflows and held-out tasks expose the useful tradeoffs.

Required scenarios:

| Scenario | Acceptance evidence |
|---|---|
| Switch Files → Git → change review → terminal → agent | Git sidebar does not replace main content until review is requested; typed review return works, no terminal rail remains, no stale marker or lost PTY. |
| Resize/maximize/restore with both decks active | Painted geometry stays in bounds, blocking controls usable, normal sizes and selection return. |
| 80×24, 120×40, 160×40, 200×50; dark/light; nonzero frame padding | Readable labels/actions, overflow reachable, no clipped buttons, scroll/hit regions match paint. Heights/widths are test scenarios, not hardcoded layout breakpoints. |
| Settings ordinary draft → scope switch; disk-save failure | Draft preserved or explicit discard; failed save remains open; focus and live preview stay coherent. |
| Existing provider/account/agent workflows | Add/edit/clone/remove, scope rules, credential isolation, model discovery, open dropdown, and agent tabs remain usable. |
| Manual Skills/MCP browsing with automatic acquisition disabled | Search/discover/inspect/install/configure/sign-in/selection are usable from Settings without a provider call; rail and slash commands resolve the same items and state. |
| `/skills`, `/mcp`, explicit skill invocation, and colliding names | Management opens UI only; exact identity and argument text survive autocomplete; existing composer text/media and editor drafts survive navigation; task submission stays explicit. |
| Memory footer from Welcome, editor, root and child tabs | One workspace browser and independent query state; extraction off/empty still browsable; errors are actionable; narrow overflow keeps Memory reachable. |
| New tool under read-only child and explicit denial | Definition filtering and execution enforce the same policy; acquisition cannot raise authority. |
| Two agents acquire the same skill/server | One verified artifact, distinct grants/bindings, cancellation of one request does not corrupt the other. |
| OAuth cancelled/expired; unsupported local isolation | Clear recoverable state; no false Connected or Available badge; draft and source remain inspectable. |
| Hundreds of tools and changing schemas | Search/group controls remain responsive; bounded request context; changed operations re-evaluated; selection intent retained. |
| Disconnect during a remote mutation | Unknown result stays unknown; no unsafe blind retry or fabricated successful output. |
| Board thread viewed by human, paged by agent, then resumed | Human unread separate; agent cursor advances only on accepted context; posts cause zero new worker invocations. |
| Concurrent memory correction/extraction | Version conflict retains user draft and evidence; retired records are not revived; search error is not an empty result. |
| Resume/switch workspace during acquisition or inspection | Passive resume, stale result rejection, correct credentials/project scope, no focus theft. |
| Git staged/unstaged/unsaved/conflict cases | Correct comparison identity, explicit mutation, stale precondition handling, no silent staging of unseen bytes. |
| Long Git paths, duplicate basenames, large change sets and commit failure | Calm readable rows; complete path/copy access; main diff and selection stable; commit draft and exact staged review retained. |
| Live and resumed dynamic tool call | Same readable title/outcome, full accessible evidence, no raw JSON title or secret leakage. |

For implementation run focused behavior and complete affected packages, relevant
races, full build/tests, docs/design checks, and whole-repository lint. Use
[`scripts/tuishot.sh`](../../scripts/tuishot.sh) sequentially for Linux visual
acceptance, close every capture window, and verify actual pointer/keyboard
flows rather than trusting a component-only screenshot. Respect any active
command restrictions when choosing a capture fixture; do not run repository
operations indirectly through startup if they are not authorized.

Measure first usable paint, filtering and input latency, repeated resize and
focus behavior, acquisition latency, tool-definition context cost, redundant
permission prompts, and task completion. Record real baselines before setting
performance budgets. Until these scenarios pass, this document describes a
proposal and implementation contract, not a completed product.
