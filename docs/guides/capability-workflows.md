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

## MCP

Local (stdio) servers inherit the complete host environment, exactly like agent
shell commands: an authorized launch already runs unsandboxed with the user's
privileges, so a curated variable list was never a boundary and only broke
display, session-bus, locale, proxy and toolchain-dependent servers. Explicit
connection environment and secret references override inherited values. The
server's redacted stderr tail is captured per launch and attached to handshake,
disconnect and tool-call failures and to the connection state, so a crash reads
as its own reason (for example `Could not open main display`), not a bare EOF.

Registry packages (npm/PyPI) are prepared before their first launch in a core
process: after the launch is authorized, the owning agent session runs
`npx --yes --package=<pkg@ver> --call "exit 0"` or `uvx --from <pkg==ver> python -c ""`
through its ordinary background command manager. Quick preparation continues
inline; a slow download is promoted to a normal tracked command (stable
`mcp_prepare_<id>` handle in the background jobs panel, Terminate-able, readable
with `read_async_output`) and reported to the agent as a `command_outcome` event,
after which it activates the same ID again. Preparation has no deadline; the
handshake keeps the ordinary connection budget once the package is local. Edited
argument vectors and shell-host connections launch directly.

`capability_activate` accepts an optional `environment` object for server-specific
settings (empty value unsets). It is one exact, durable registration change whose
values are shown to the approver; approving it also authorizes launching exactly
that configuration for the run. Credential references stay user-owned and cannot
be shadowed. A live connection is reused without another launch authorization,
and a recorded run-local launch authorization survives a failed connect or a
preparation in the same run.

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

Tools → Review grant requires effect, resource scope and observed schema
fingerprint. Changed/unknown operations fail closed. Profile/session selection
cannot exceed the role ceiling or grant permission. A local scope label does not
sandbox filesystem/network access; unsandboxed local launch requires exact
authorization and is eligible for Manual/Auto Review/YOLO within host/effect
ceilings. Authentication and consent remain user-owned. External MCP writes are outside
native file ownership and undo. Ambiguous mutating outcomes stay unknown and are
not replayed.

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

## Host locality and external computer-use

Connection host defaults to `core`; stdio runs on that host with the resolved
execution directory. Explicit `host: "shell"` uses the original connected shell's
worker and exact attachment epoch, never a convenient replacement shell. It requires
stdio and an explicit absolute shell-local directory. Environment/header credential
bytes are not forwarded; supported secret references resolve on the selected host.
Detach, changed run/configuration and cancellation fence late calls and revoke
run-local authority. Manual Settings UI for selecting/configuring shell-host execution
is not yet a complete advertised journey; do not infer it from transport support.

Users can select an external computer-use MCP appropriate to their OS/vendor and
authorize its actual host effects. Crude bundles no universal desktop driver and
cannot guarantee screenshot, mouse, browser or authentication automation for every
integration. Native [Crude control](../reference/control-contract.md) is semantic IDE
control, not external desktop access.

## Agent Discovery, Acquisition, And Durable Use

Relevant installed capabilities and acquisition may be used proactively when they
improve the task, even when native tools exist. Exact activation follows the user's
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
4. MCP activation connects and returns observed tools and their fingerprints,
   separately from the actually usable tools. If access is missing, the agent can
   request `mcp_request_access` with `connection_id` and `grants`: original tool name, fingerprint,
   effect and resource scope. Human approval persists that exact durable access
     change; reviewer/YOLO approval grants only run-local access, revoked at the
     owner/run boundary and rechecked against schema, host, connection and ceilings.
   Grants inherit the connection's existing binding; approving access never moves
   an application-bound connection into the workspace. The agent never infers a
   grant from server annotations or a successful connection.
5. Approved registrations/grants survive a complete workspace close and reopen.
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
workspace browser. Knowledge and Episodes are separate tabs. Detail is scrollable
and copyable: source/applicability, evidence origins/synopses/outcomes, immutable
revisions, feedback and exposure. Actions support pin, correction and retire.
Correction/retire use the exact expected revision; stale extraction cannot replace
the correction or revive retired advice. Exposure is not usefulness. Disabling
automatic extraction does not remove browsing.

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
`[ Generate ]` (Aeron streams a message drafted from exactly what Commit would
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

- Full build/tests, whole-repository lint (zero issues), docs/design/benchmark
  gates and affected race suites are required, not substitutes for interaction.
- `session/capability_task_e2e_test.go` drives the real agent loop against a
  scripted provider: installed-first search → immutable skill acquisition → MCP
  activation → real SDK tool result → passive SQLite resume.
- `capability/access_test.go` verifies exact access approval, usable dynamic tools,
  real SDK calls, passive reopen, and rejection of altered scope/fingerprint or
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
  and grant without new acquisition approvals or registry requests. Optional
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
- `mcpclient/workflow_test.go` exercises real stdio subprocesses, Streamable HTTP,
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
