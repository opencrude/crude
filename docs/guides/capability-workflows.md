# Skills, MCP, Board, And Memory Workflows

> Status: Current — supported product workflows and validation boundaries.

## Navigation

Settings lists Skills, MCP, Sources and Acquisition immediately after Memory in
one AI group, before Security and IDE. Application and Workspace scope share this
order; Providers and Accounts remain application-only.

The activity rail contains Files, Agents, Source Control, Skills and MCP. Settings
stays at its foot. Right-click a destination to pin/unpin, reorder or reset its
shortcut. This affects presentation, not workspace data. Terminals are available
through the footer, palette and named picker; they have no rail entry.

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

1. Open **Manage Skills**, the Skills rail, or `/skills [query]`. These routes use
   the same Settings manager, defaulting to workspace scope.
2. The catalog combines search results and current registrations without duplicate
   identities. **Installed only** restricts it to registrations without networking.
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
   runtime permission. Use in session stages the exact `/skill <id> [arguments]`
   without sending. Submitting that task performs the normal single submission.

Create/Add local validate standard `SKILL.md`. Removing a registration never
deletes authored files or another workspace's chosen version. Automatic selection
and new acquisition are separate settings under Acquisition; Manual acquisition
does not prevent user-managed install or browsing.

## MCP

Manage MCP or `/mcp [query]` opens the same searchable store in the retained Settings
window. Installed only is an explicit local-only filter. The official Registry
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
sandbox filesystem/network access; unsandboxed local launch requires explicit
approval and is not offered for automatic startup. External MCP writes are outside
native file ownership and undo. Ambiguous mutating outcomes stay unknown and are
not replayed.

Resources/Prompts are separately browsable. Enter required prompt arguments,
Fetch a preview, then Stage context. The + Capability composer control and Insert
Capability palette action offer Skills/Tools/Resources/Prompts over the same
descriptors. Tool chips express user selection, not approval. Resource/prompt
chips retain external attribution; changed arguments/session/agent/draft reject
late results. Nothing is submitted automatically.

Coverage: managed stdio and Streamable HTTP, endpoint-bound OAuth, bounded
tools/resources/prompts discovery, schema changes, text/structured/image tool
results, progress, timeouts and cancellation. Composer resource/prompt previews
are text-only. Sampling, elicitation, task extensions, remote channels and rich
MCP apps are unsupported and not advertised. Resume starts no connection,
download, update or tool replay.

## Agent Discovery, Acquisition, And Durable Use

1. `capability_search` honors the requested result limit and returns matching
   installed entries before querying external sources. A source failure preserves
   usable discovery results with an attributed warning rather than discarding them.
2. `capability_activate` acquires an exact discovered identity under the configured
   source/acquisition policy. Successful acquisition publishes into normal
   **Workspace Settings**. It is not promoted into Application Settings.
3. Skill activation returns the installed absolute path, revision, digest and
   source instructions. Bundled relative files resolve from that path; they are
   not files in the project's current directory. Their execution still uses normal
   tool permissions.
4. MCP activation connects and returns observed tools and their fingerprints,
   separately from the actually usable tools. If access is missing, the agent can
   request `mcp_request_access` with `connection_id` and `grants`: original tool name, fingerprint,
   effect and resource scope. The user reviews that exact durable access change.
   The agent never infers a grant from server annotations or a successful connection.
5. Approved registrations/grants survive a complete workspace close and reopen.
   Reopen is passive: no downloads, connections, model calls or tool replay.
   A later explicit activation reconnects and rechecks current tool fingerprints.
   Disable/remove operates on the same authoritative registration, with no hidden
   install record that can resurrect it.

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

## Git Review

The Source Control sidebar has calm Conflicts/Staged/Changes groups and a filter.
Selecting a file does not stage/discard. Explicit activation opens one typed
main-area Git Changes tab with complete path and compared versions. Unstaged uses
index→disk; staged uses HEAD→index. Unified/side-by-side, search/hunk navigation,
copy/full-path and horizontal access preserve exact content at narrow sizes.

Stage/Unstage acts on reviewed bytes and rejects changed HEAD/index/disk; unsaved
editor text must be explicitly saved and reviewed. Review commit keeps a multiline
draft and the exact staged set; changed membership requires another review.
Discard prepares an exact preimage, defaults to Cancel, captures mandatory Local
History recovery and aborts when the file/index/HEAD changes. Untracked data is
never deleted. Unsupported rename/symlink/deletion discard routes to explicit
Local History instead of guessing. Hunk staging, conflict resolution, worktrees
and remote operations are not presented as working features.

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
  standard skill source and a real SDK HTTP MCP fixture. Optional
  `CRUDE_CAPABILITY_PROOF_DIR` retains a new proof directory. It does not claim
  that every public package, OAuth vendor or source works.
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
