# Skills, MCP, Board, And Memory Workflows

> Status: Current — supported product workflows and validation boundaries.

## Navigation

Settings lists Skills, MCP, Sources and Acquisition immediately after Memory in
one AI group, before Security and IDE. Application and Workspace scope share this
order; Accounts and their complete connections remain application-only.

The activity rail contains Files, Agents, Skills and MCP. Settings
stays at its foot. Right-click a destination to pin/unpin, reorder or reset its
shortcut. This affects presentation, not workspace data. Terminals and Git are
available through the footer, palette and named picker/shortcut; they have no
rail entry.

The footer selects Problems, Notices, Terminals, Board, Memory or Workspace. More retains
access when labels do not fit. Each workspace panel retains its own normal
height; maximize/restore/hide does not stop a process or delete inspected data.
Session settings and required decisions remain in the session's own deck.

Workspace home surfaces named enabled Skills and live MCP connections rather than
just installed counts. Skills distinguish Automatic, Manual and Loaded; Loaded
requires a binding to a currently active agent. Disabled/unavailable skills are
not advertised as enabled. MCP lists actual connected transports and named
failures/sign-in requirements, never cached discovery as a live connection.
Each row opens the same manager at that exact identity; there are no duplicate
Settings or Help links. Lists scroll independently when more names are active.

While home is visible, a cancellable, owner/generation-scoped observation every
two seconds reads the initialized runtime snapshot off the UI thread. It does
not inspect skill files, reload configuration, connect a server or launch a tool.
Explicit manager changes still publish their committed snapshots immediately.

## Skills

1. Open **Manage Skills** or the Skills rail's manager action. These routes use
   the same Settings manager and open at **Application Settings** by default:
   a capability belongs to the user, not to one project. Use the footer
   Application/Workspace toggle first when you explicitly want this workspace
   bound to the installation. Workspace deletion then removes only that overlay,
   never the installed bytes.
2. The catalog combines search results and current registrations without duplicate
   identities. The **Installed** chip restricts it to registrations without networking.
   Cards expose Install/Manage directly. Inspect opens Overview, Instructions,
   Files and Access & use; versioned managed packages also expose Versions.
3. Read instructions/List files are explicit bounded reads. Authored files can
   open in the native editor; managed artifacts require Make local copy first.
4. Enter a query and press Enter or Search to query enabled sources. Source and
   Sort open explicit choice lists. A selected Source is queried directly; an
   all-source search interleaves source-ranked results and retains partial results
   if another source fails. A local
   source is a directory of standard skill directories; repository sources pin
   complete commit identities. Source trust is not an execution grant.
   Application settings include three disableable built-in presets:
   **Anthropic Skills (pinned)** at commit
   `8a1541c4a3ffa5a20a5a91de0dcf3f0bab1d1ef4`, and the
   **Skills Directory** community search at skills.sh, and the
   **Official MCP Registry (preview)** discovery API. Directory skills resolve
   to a reviewed exact GitHub commit. Built-ins can be edited or
   disabled but not deleted; install/update still requires the exact preview.
5. Install/Update opens an exact source/revision/digest/scope/binding preview.
   Confirm rechecks it before publication. Changed metadata requires another
   review; incomplete acquisitions are never available.
6. Access & use selects Automatic, Manual only or Disabled independently of
   runtime permission. For quick, temporary control, `/skills [query]` opens the
   Capabilities modal: each card's switch turns a skill off or on for the current
   session and all its agents, without changing the registration or the composer.
   Turning one off prevents future loading, not removal of accepted context.

Create/Add local validate standard `SKILL.md`. Removing a registration never
deletes authored files or another workspace's chosen version. Automatic selection
and new acquisition are separate settings under Acquisition; Manual acquisition
does not prevent user-managed install or browsing.

Frontmatter is bounded YAML. Foreign extension fields and nested authoring
metadata are inert rather than install blockers; hooks never run. Tool requests
accept space/comma-separated text or lists, without granting tools. Compatibility
prose is guidance, not guessed executable requirements. Content validation and
host readiness are separate: a missing declared dependency leaves the skill
installed and readable with **setup required**; retry activation after repair.

## MCP

Registry installation saves the connection without launching or granting tools.
Required HTTP headers remain visible through review and install: fill their empty
header rows or add Secret references in Manage. Fixed headers and declared
variable defaults are retained. Unknown transports or unresolved endpoint/command
variables give an explicit manual-configuration reason rather than a guessed
launch. Missing credentials are setup needs, not permission to fabricate them.

Local (stdio) servers inherit the complete host environment, exactly like agent
shell commands: an authorized launch already runs unsandboxed with the user's
privileges, so a curated variable list was never a boundary and only broke
display, session-bus, locale, proxy and toolchain-dependent servers. Explicit
connection environment and secret references override inherited values. Required
variable checks use those same host values; shell-host checks run on that shell. The
server's redacted stderr tail is captured per launch and attached to handshake,
disconnect and tool-call failures and to the connection state, so a crash reads
as its own reason (for example `Could not open main display`), not a bare EOF.

Agent activation that must acquire, install or launch is one job from its first
instant, not a package download followed by a second activation call, and not
a wait inside the tool call. Source review, sign-in and launch permission remain
explicit and happen first. Once admitted, a skill acquires its pinned files,
verifies and loads them; an MCP launch uses the reviewed executable and
environment, including any first-run installation by that executable, and
discovers the live tool catalog. The tool returns the job's handle at once and
the job appears in the same **Background jobs** deck as shell commands. An
installed skill or an already connected server has nothing to wait for and
answers in the result. The job link reveals the originating call. **Output**
reads retained phases and the result; **Stop** stops this attempt. A known
transfer length fills the bar; without one, the bar shows activity, not an
estimated percentage. A local server that exits before it answers fails with its
own reason on the first line (`server exited before it answered: Error: Missing
command.`), followed by its stderr tail.

`capability_search` asks every enabled source unless installed matches already
fill the requested count: an installed entry that shares one word with the query
is not an answer. The official registry matches one substring of a server name,
so each keyword is asked separately, together with adjacent keywords joined as
names join them (`computer use` also asks `computer-use`); only the latest
version of each server is requested and the union is ranked by all query words.
Agents receive compact results without entries they cannot install. An activation lists usable tools. Their schemas arrive once as ordinary
function definitions in the next request. There is no separate model grant step.
An external tool without a saved effect classification uses the normal permission
mode on its actual arguments. A role with restricted effects cannot use it.


The owning agent receives the actual activation result as a `command_outcome`.
No polling or second activation is needed. Permission to run a tool is separate from starting its server.
Local stdio bootstrap has no implicit 30-second deadline. An explicitly set
connection timeout still applies, and catalog discovery and tool calls keep
their own limits. Passive restore never restarts a launch. The Settings **Install**
action still only registers an MCP; it neither connects nor wakes an agent.

`capability_activate` accepts an optional `environment` object for server-specific
settings (empty value unsets). It is one exact, durable registration change whose
values are shown to the approver; approving it also authorizes launching exactly
that configuration for the run. Credential references stay user-owned and cannot
be shadowed. A live connection is reused without another launch authorization,
and a recorded run-local launch authorization survives a failed connect or a
slow launch in the same run.

Local MCP process teardown has one pipe/process owner for cancellation, EOF,
bounded termination and reaping. Termination is armed before potentially blocking
Windows pipe closes; it does not race a second `CommandContext` cancellation kill
against a server that already exited gracefully.

Manage MCP opens the searchable store in the retained Settings
window. The Installed chip is an explicit local-only filter. The official Registry
includes remote HTTP and exact-version npm/PyPI packages; required setup is shown,
and unsupported variants remain inspectable with reasons. Search is bounded to 100
results and eight Registry cursor pages, not a claim of exhaustive marketplace
coverage. Add/Edit exposes the HTTP endpoint or stdio executable/arguments,
directory, environment, headers/secret references, authentication and timeout.
Import accepts supported `servers`/`mcpServers` JSON and rejects unknown fields
instead of silently executing unrelated extensions.

Inspect is passive. Connect, Disconnect, detached Test, Refresh tools, Sign in/out
and Save are separate actions. A test does not persist the draft or mark a saved
connection connected. OAuth presents an explicit browser URL and exact loopback
return flow; credentials are bound to both connection ID and endpoint. Cancellation
clears the flow; expired/revoked authorization requires explicit reauthentication.

**Tools → Tool access** lets you turn a tool off or classify its effect and
resource scope for restricted roles. **Review the call** removes the saved
classification. A classification applies only to the observed definition. It
does not sandbox a process. Manual approval, Auto Review and YOLO apply to the
actual call, not an invented lifetime for tool access. Unknown mutating outcomes
are not replayed. Use `capabilities.inspect` with the connection ID to see its
rules. `capabilities.set_tool_enabled` operates on one switch. Test a saved
connection or a draft with `capabilities.test_connection`; it closes its detached
transport, calls no tool and saves nothing.

`/mcps [query]` opens the **MCP servers** view of the session capability modal
(`/skills` opens its separate Skills view).
Its switches turn a server off or on for the current session and all its agents,
even mid-run: the next request drops or restores its tools. They never start a
connection, grant access or change the registration; card info shows only observed
connection state. **Install & manage** opens Settings. The palette's **Session MCP Servers** action
opens this view too. There is no composer **+ Capability** control, `/skill <id>`
invocation, or singular `/mcp` command.

Resources/Prompts remain separately browsable in Settings. Enter required prompt
arguments, Fetch a preview, then Stage context. These source-attributed attachments
reject changed arguments/session/agent/draft. Nothing is submitted automatically.

The declared MCP profile uses Go SDK v1.8.0 negotiation: 2026-07-28,
2025-11-25, 2025-06-18, 2025-03-26 and 2024-11-05. Advertised SDK protocol
versions do not imply support for every optional feature in those specifications.
Crude disables multi-round-trip extensions and advertises only actual client
capabilities; resource links remain references, not automatic fetch permission.

Coverage: managed stdio and Streamable HTTP, endpoint-bound OAuth, bounded
tools/resources/prompts discovery, schema changes, ordered mixed text, structured,
image, audio, embedded-resource and resource-link
results, progress, timeouts and cancellation. Declared output schemas participate
in fingerprints and structured-output validation; external schema references are
not fetched. Encoded projected content has a 1 MiB budget; oversized content fails
explicitly. Model modality and wire support still govern media admission. Roots
use official file-URI roots/list behavior for the resolved execution directory;
roots are context, not a filesystem sandbox. Composer resource/prompt previews
are text-only. Sampling, elicitation, task extensions, rich MCP apps and optional
remote UI channels are unsupported and not advertised. Resume starts no connection,
download, update or tool replay.

### Search methods for web search

Open **Application Settings → MCP → Web search and fetch → Search methods**.
Crude always searches with free engines and with the provider keys you add in
**Edit provider keys**. Add a method to also search with an account you already
use. Choose **Add method**, then:

- **Use this method**: On or Off. A method that is Off stays in the list but
  does not search.
- **Account**: only accounts whose protocol can search the web are listed.
  Accounts that use the Ollama protocol search with the account alone, so
  **Model** shows **Not used**. OpenAI Responses, Anthropic and Gemini accounts
  search through a model.
- **Model**: choose a model from the account's list or enter its exact ID.
  Choose a model that can search the web.

Choose **Test** to run one search and see the result, then **Save method**.
Add more methods to improve results: every method that is on runs with each
search, and pages that more methods find rank first. Charges can apply. Each
row shows the last result, for example **On · Working · 1.4 s** or
**On · Failed**; select a row to read the reason. Use **Edit**, **Test** and
**Remove** on the selected row. Workspace Settings shows the methods without
changing them.

Crude sends only the query to each account. It does not send the conversation
or change the agent's model. A failed search can show a **Set up search
methods** link on its row. Opening the link or saving a method does not repeat
the search.

## Host locality and external computer-use

Connection host defaults to `core`; stdio runs on that host with the resolved
execution directory. Explicit `host: "shell"` uses the original connected shell's
worker and exact attachment epoch, never a convenient replacement shell. It requires
stdio and an explicit absolute shell-local directory. Environment/header credential
bytes are not forwarded; supported secret references resolve on the selected host.
Detach, changed run/configuration and cancellation fence late calls and revoke
run-local authority. Manual Settings UI for selecting/configuring shell-host execution
is not yet a complete advertised journey; do not infer it from transport support.

Crude bundles no universal desktop driver. Computer use (seeing and operating a
desktop or another application) is an MCP server for that OS. An agent asked for it
finds one with `capability_search` and activates it like any capability, under the
user's policy and the server's actual host permissions; it does not install packages
by hand. Crude cannot guarantee screenshot, mouse, browser or authentication
automation for every integration. Native [Crude control](../reference/control-contract.md)
is semantic IDE control, not external desktop access.

## Agent Discovery, Acquisition, And Durable Use

Relevant installed capabilities and acquisition may be used proactively when they
improve the task, even when native tools exist. A workflow, script or tool that
applicable project guidance names for the job (such as `scripts/tuishot.sh` for
screenshots) takes precedence; agents do not search for or install a capability to
replace it. Exact activation follows the user's
Manual/Auto Review/YOLO policy, explicit denials, trusted sources, host and effect
ceilings. Source text and server annotations never impersonate human authorization.

1. `capability_search` honors the requested result limit and returns matching
   installed entries before querying external sources. Local, installed and pinned
   repository descriptors use case/punctuation-normalized keyword ranking, with
   exact names ranked before partial keyword coverage and limits applied afterward.
   Registry APIs retain their own ranking and fuzzy matches. Use concise names or
   distinctive keywords, and shorten/rephrase an empty query rather than scanning
   system directories. A source failure preserves usable discovery results with an
   attributed warning rather than discarding them.
2. `capability_activate` acquires an exact discovered identity under the configured
   source/acquisition policy. Successful acquisition publishes into **Application
   Settings** by default, because a capability belongs to the user rather than to
   the project that happened to install it. Pass `scope: "workspace"` only when the
   task or user explicitly wants the capability bound to this project; that binding
   then lives in **Workspace Settings** and is removed with the workspace.
   Pass the returned ID unchanged, not a display name or directory path. Reading
   source files alone does not install a durable registration.
3. Skill activation returns the installed absolute path, revision, digest and
   source instructions. Bundled relative files resolve from that path; they are
   not files in the project's current directory. Their execution still uses normal
   tool permissions.
4. An active connection exposes its enabled tools, subject to role and session
   policy. Call a tool by its offered name. The actual call, including a mixed
   tool such as a clipboard reader/writer, goes through normal permission review.
5. Registrations and explicit user rules survive close and reopen.
   Reopen is passive: no downloads, connections, model calls or tool replay.
   A later explicit activation reconnects and rechecks current tool fingerprints.
   Disable/remove operates on the same authoritative registration, with no hidden
   install record that can resurrect it.

Closing the MCP manager joins admitted connection attempts and reconfiguration
cleanup. A cancelled connection lifetime rejects late HTTP requests before they
reach the transport. Loopback OAuth cleanup uses bounded graceful shutdown so
receiving the code cannot close the browser acknowledgement mid-response.

The shared passive observer runs while Settings or a capability sidebar is open,
even after an active agent removes the launcher screen. It updates the retained
Settings frame without Refresh/reopen or transport reconfiguration. An inspected
MCP tool tracks its exact identity separately from table selection; schema/grant
changes update that same read-only detail, and removal clears obsolete claims.
Focused filters and editable prompt/grant drafts remain owned by their controls.

Existing development shadow-install data must be manually backed up and aligned
to workspace settings before using the new current runtime. There is no product
compatibility reader or automatic conversion.

## Board And Memory

**Agent Board** in the footer or palette opens the workspace panel.
Select the collaboration explicitly; inspect Threads/Participants, search or page
through results. New collaboration joins selected live roots; children inherit
membership. Replies retain sender/type/evidence. Human unread is independent of
agent context acceptance. HOLD/VETO/posts never wake workers, grant permission,
reserve files or schedule work. The initial human UI is inspection/search only.

**Workspace Memory**, `/memory`, Ctrl+Alt+M and the footer share one retained
workspace browser. Knowledge and Episodes are separate tabs. Enter opens a
knowledge item ready to edit in place (title, content, applicability, scope
paths; evidence is read-only); Ctrl+S or **Save** writes a new revision against
the exact revision that was opened, and the editor stays open. Esc with unsaved
changes asks once, then discards. An unchanged open editor follows newer
revisions; an edited one keeps the user's text on a conflict. **More** holds
pin/unpin, **History and details** (scrollable, copyable source, evidence,
immutable revisions, feedback, exposure) and copy. Retired knowledge and
episodes open read-only.

**Delete** (the Delete key on a list row, or the button in the editor) is the
user's complete removal of any memory. Its confirmation focuses Cancel and shows
the content. Knowledge deletion removes the record, every revision, feedback,
exposure, journal entry and search entry in one transaction, fenced by the
opened revision: a memory changed elsewhere is not deleted until the user opens
it again. Episode deletion removes the episode. Revisions and episodes stay
immutable otherwise; agents retire knowledge with history kept. Exposure is not
usefulness. Disabling automatic extraction does not remove browsing.

During active development, memory and session capability settings require the
current schema. Startup does not automatically convert outdated formats. Breaking
architecture changes must manually align affected local data outside product code:
back up first, preserve IDs, content, provenance, lifecycle, history, summaries,
transcripts and account/model pins, then verify reopen and resume. One-off
conversion tooling is not retained in the codebase. Preserving existing work is
mandatory, but is not a reason to add backward compatibility. New and corrected
claims still require the current evidence rules; pinning, archiving or retiring
an existing assertion does not manufacture new claims.

## Git

Every action that is a button is painted as `[ Label ]` (or `[+]` for a
one-glyph row action) so it reads as clickable.

Git is a bottom-dock destination (footer **Git** pill, `Ctrl+Alt+G`, palette
"Git"), not a sidebar. Its **Changes** tab is a two-pane workbench: on the left a
branch/tracking line, a commit message box with `[ Commit ]` and
`[ Generate ]` (Errand streams a message drafted from exactly what Commit would
record, with an animated progress rule; `[ Stop ]` restores your draft),
and the change list grouped Merge Conflicts / Staged / Changes; on the right a
live hunk preview of the selected file, separated by a draggable divider. Rows show the status letter, name and
muted folder; hover or selection reveals inline `+` stage, `−` unstage, `↶`
discard and `✓` mark-resolved. Section headers carry **Stage all** /
**Unstage all**. Keys: arrows move, Space stages/unstages, Enter opens the full
review, `o` opens the file at its first change, `d` discards, `a`/`-` stage or
unstage everything, `c` focuses the message, Tab walks the regions, Ctrl+R
refreshes. Staging moves the cursor to the next change, never back to the top.
**History** lists recent commits with a read-only diff of the selected commit;
Enter opens it in the main area. **Branches** lists local/remote branches and
switches only after a confirmation that re-checks HEAD.

`[ Review all ]` opens the all-changes review tab; Enter or a double-click on
a file opens that file's own tab; History Enter opens a commit tab. Each is a
separate main tab, so several can be open and split side by side. A review is
one scrolling document of its files with sticky file headers, hunks
with three context lines, folded unchanged runs (click to expand), per-file
`+N −M`, Stage/Unstage/Discard/Open header actions, Stage hunk / Unstage hunk /
Discard hunk on every hunk header (`+`, `-`, `x`), and a file navigator that
follows the reading position. Drag to select and copy (copies on release),
Shift+arrows extend whole lines, Ctrl+A selects the current file, Ctrl+F finds
across all shown lines. Unified or Split (two aligned columns from 100
columns up) toggle with `v` or the header link; `]`/`[` move by file, `n`/`p` by
hunk, `z`/`Z` fold, double-click a line to open the file at that line. History
commits open in the same review read-only. The Explorer and editor context menus
offer **View Changes** for a file or folder (the review is then scoped to that
path, Esc or **Show all** clears the scope) plus Stage/Unstage/Discard.

Unstaged compares index→disk; staged compares HEAD→index. Stage/Unstage from a
painted diff records exactly those bytes (`StageReviewed`) and rejects a changed
HEAD/index/disk; a file with unsaved editor text is refused until saved. Commit
runs Git itself (hooks, signing and templates apply) after verifying the staged
path set equals the set shown; "Commit all N" stages exactly the listed paths
first. A different set fails closed with nothing committed; conflicts disable
Commit. Discard prepares an exact preimage, defaults to Cancel, captures
mandatory Local History recovery and aborts when the file/index/HEAD changes.
Hunk stage/unstage writes only that hunk's lines to the index under the same
exact-bytes check; hunk discard confirms, snapshots Local History and fails
closed on any newer change. Untracked data is never deleted. Managed execution worktrees use the separate
[execution service](../reference/managed-executions.md), not this Git panel as an
isolation/integration shortcut. Remote Git operations are not advertised. Local Compare With Disk / Compare With
Saved Version keep the separate full-file reader in movable main tabs.

## Validation Evidence

- `TestConnectedActivationJobControls` submits one composer turn on a connected
  shell: an ordinary `go test ./...` job, a real MCP initialization held at a
  local HTTP peer, and a local server that exits with `Error: Missing command.`.
  The turn ends without steering while the handshake is still held. The failed
  row leads with the server's own reason. The test reveals the exact call, reads
  its output, stops the handshake, and verifies the neighboring command still
  has its own Stop action.
  For native verification, compile `./tui/viewmodel` as a test binary, set
  `CRUDE_VISUAL_ACTIVATION=1`, select this test, and use the repository capture
  driver at 160×40 and 80×24, sequentially. Set `CRUDE_CAPTURE_EVIDENCE` to a
  fresh temporary file per run. Use `await:motion`, `shot:mixed`, `quit:F9`.
  The marker requires five consecutive painted arc steps in order without an
  idle-clock gap. `TestMeasuredJobMotionVisualAcceptance` with
  `CRUDE_VISUAL_JOB_MOTION=1` uses a held download snapshot in the same mounted
  deck to verify the arc beside measured fill and the ordinary running and
  failed rows. Use the same steps and sizes. A capture without the marker and a
  successful exit is not proof.
- Set `CRUDE_LONG_ACTIVATION_PROOF=1` for
  `TestSlowActivationSettlesWithDiscoveredToolsWithoutReplay` in `./agent/session`.
  The local executable waits beyond the old 30-second handshake limit before
  serving its actual MCP tool catalog. `TestSkillActivationReportsActualDownloadBeforeEOF`
  separately checks the downloaded and total byte counts against a server
  held partway through a response and stops the job before registration.
- Full build/tests, whole-repository lint (zero issues), docs/design/benchmark
  gates and affected race suites are required, not substitutes for interaction.
- `agent/session/capability_task_e2e_test.go` drives the real agent loop against a
  scripted provider: installed-first search → immutable skill acquisition → MCP
  activation → real SDK tool result → passive SQLite resume.
- `agent/capability/access_test.go` verifies exact access approval, usable dynamic tools,
  real SDK calls, live switches, passive reopen, and rejection of a changed fingerprint or
  agent ceilings. `workspace/capability_registration_test.go` exercises normal
  settings persistence, fresh service reopen, disable/remove and stale-owner checks;
  `tui/viewmodel/capability_visibility_test.go` asserts retained painted catalogs.
- `app/live_capability_workflow_test.go` is opt-in real-model acceptance
  (`CRUDE_LIVE_CAPABILITY_WORKFLOW=1`) using an isolated project/home, a local
  standard skill source and a real SDK HTTP MCP fixture. The task states the
  desired audit and future reuse, not tool-call steps/IDs/paths/fingerprints.
  Unpredictable receipt codes and a changed remote code prove actual asset reads
  and fresh SDK calls. The source is deleted after acquisition; follow-up, passive
  workspace/session reopen, resumed use and a new agent must reuse the installation
  without new acquisition approvals or registry requests. Optional
  `CRUDE_LIVE_CAPABILITY_CATEGORY` selects an explicitly configured model category;
  `CRUDE_CAPABILITY_PROOF_DIR` retains a new directory under `CRUDE_TEMP_DIR`.
  Credentials stay in memory.
- `CRUDE_VISUAL_CAPABILITIES=1` adds the production TUI/message pump to that same
  live test. The frontend creates the root pane/renderer, and tasks enter through
  the real composer paste/send path. Chat remains visible; `/skills` and `/mcp`
  inspect Workspace Settings briefly and Esc restores it. Session `/settings`
  is not a capability-manager route. A `visual-chat-ready` file marks completed
  composer rounds. Quit is hierarchical: one Ctrl+Q minimizes the agent, the
  next exits from home. Captures must finish the test and check its proof summary,
  not infer success from the screenshot driver's exit code.
- Explicit `CRUDE_LIVE_CAPABILITY_STORE=false` requests full-history uploads for
  a connection whose stored Responses continuation fails the separate
  `TestLiveResponsesStoredContinuation` acceptance. It is an isolated test choice,
  not an automatic fallback or an edit to the user's account. The tested gateway
  accepted response IDs but lost inherited context; a goal-only DeepSeek v4.1
  Flash run then passed with full uploads. This does not establish that gateway
  continuation works, or that every public package, OAuth vendor or source works.
- `agent/mcpclient/workflow_test.go` exercises real stdio subprocesses, Streamable HTTP,
  OAuth loopback/headless return, grant isolation, cancellation/retry and unknown
  mutations without replay. Board tests use real durable context admission;
  memory tests cover evidence/revision/CAS/exposure and retained browser ownership.
- `TestLiveDefaultCapabilitySources` is an explicit network-gated acceptance
  (`CRUDE_LIVE_CAPABILITY_SOURCES=1`): it discovers and acquires a real pinned
  Anthropic skill, discovers current remote Streamable HTTP entries from the
  official Registry, and materializes a stable MCP connection definition.
- Linux terminal acceptance uses sequential `scripts/tuishot.sh` sessions, never
  concurrent capture windows. Verified flows include Settings manual discover/
  preview/install, instruction/resource reading, exact skill staging, session
  capability controls, Git review, Board inspection, workspace Memory and PTY
  hide without termination at 80×24, 120×40, 160×40 and 200×50, including light
  theme and nonzero frame padding. Temporary evidence is in
  `/tmp/crude-capability-shots/`; every CrudeShot window is closed.

No held-out task-quality, provider-token saving or production latency improvement
is claimed from scripted workflows. Public/live OAuth vendors and native Windows/
macOS terminal interaction remain separate platform acceptance work.
