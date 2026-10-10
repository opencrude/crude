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
shell attachment is detached; peer work in that project is retained. Its effective theme is then applied before target-owned
editors, tree, and session projections are constructed, so no frame is built
with the previous workspace palette.

Overriding a whole category is what made a workspace behave like a hard copy:
changing one editor field froze every other editor field at whatever the
application held that day. `OverridesFromDiff` recurses into nested objects,
so a workspace that disables one language server keeps inheriting the other
ten, and explicit `false` is a real override rather than an absent key
(`omitempty` elides it, so the diff re-adds the zero).

**Sessions snapshot.** A session captures the selected account ID, model, and
thinking effort when it starts. There is no provider/account default and no
silent account choice. Agent profiles and session overrides store account IDs,
not credentials. The account owns its complete connection configuration;
model selection remains on the agent. `AdoptWorkspaceDefaults` is the deliberate
re-inherit action; normal settings edits affect later turns only.

**Accounts own complete connections.** Application Settings → Accounts is the
single catalog for credentials and request configuration. There is no separate
Providers page, saved provider map, live template link, or override switch. Each
account has a stable ID, display name, complete `connection`, and its API key or
OAuth credentials. Provider attribution is retained for produced requests and
history; it does not resolve another configuration owner.

New account offers **Start from** to copy an immutable embedded preset into the
draft. The copy is independent immediately. Existing account edits expose endpoint,
authentication/key header, additional headers, wire protocol and options,
connection tuning, and extra JSON request fields together. Models and capability
limits stay on Agents. **Clone** copies the selected connection into a fresh ID,
never copying API-key or OAuth credentials. Every account save is an exact
preimage-checked entity transaction; unrelated preferences never replay a stale
account map. Accounts remain application-only.

Additional request headers use a native **key/value table**
(`ui.KeyValuesFieldState`): Name/Value captions, Add header, row removal, and normal
editing/navigation. There is no inherited read-only header view. Secret fields
stay masked and refuse clipboard commands.

Accounts and Agents catalogs share full-width search, single-line results, and
bottom-anchored actions; narrow screens stack actions below results. The retained
Settings frame never resizes or moves because an editor opened. Back/Esc handles
navigation and exact discard decisions; × and Close close the whole window.

### Durable startup and operational recovery

Invalid or missing selected accounts, invalid connections or credentials,
unreadable/malformed configuration, invalid network policy/certificate files, and
failed session resume/startup do not lock users out of the IDE. Crude opens its
usable shell and shows a native, movable/resizable **recovery dialog** with the
reason, helpful repair guidance, and an explicit Accounts/Main Agent/Network or
Edit config file action. Close/Esc dismisses without a retry, fallback account,
reset, or data loss. Long details remain complete, copyable and scrollable.

The same evidence is recorded independently in Notices; opening Notices is
optional. Failed New Session/Resume never annotates the launcher button or tells
users to inspect Notices to find the reason. The launcher remains intact beneath
the modal. Failed registration releases unstarted backend workers and leaves
saved conversations/pending inputs with their session owner.

Configuration loading accepts only the current structure; removed fields are a
repair issue, not a migration trigger. Malformed files remain byte-exact and can
be opened in the native editor; repairing JSON requires save and restart. Invalid
or unknown network policy blocks outbound requests until an explicitly validated
repair; keeping the UI accessible never bypasses a proxy or trust policy. Genuine
terminal initialization failure is distinct: a dialog cannot be painted when no
terminal is available.

### Skills, MCP, Sources, and Acquisition

These entries follow Memory within the existing AI category group, before Security
and IDE. The category rail, filtering and keyboard navigation use the same grouped
order in both settings scopes.

Skills and MCP use a single store-style catalog, not administration tables or
separate Installed/Discover modes. Cards show names, publisher/source, descriptions,
setup state and a native Install/Manage action. The **Installed** chip is an
explicit local filter: it never starts an external search, process or provider
request. Typing searches enabled sources live after a short pause (Enter searches
at once) with the shared progress rule; Source and Sort open explicit choice
lists, not hidden cycles. See [Settings design language](settings-design.md). Source selection queries that exact source rather
than filtering an already capped all-source page. Inspect replaces only the right
pane; Back retains query, selection and scroll. Scope remains
Application/Workspace in the footer. Source names, field labels/descriptions and
stable setting IDs participate in Settings search. Hidden catalog controls have
empty geometry and cannot paint or activate on other Settings pages.

Skills offers local Create/Add, instructions and contained file browsing,
Automatic/Manual only/Disabled modes, and explicit Use in session. Use stages
`/skill <source-qualified-id>` in the selected root composer without sending it.
Managed artifacts are immutable; Make local copy creates a distinct authored
identity before native editor opening. Remove unregisters the selected binding,
not the authored directory or another workspace's selected artifact.

Install and Update first show the exact source, revision/digest, target scope,
requirements, requested access and previous binding. Confirm rechecks that
preimage before publishing. Search sources again explicitly before reviewing an
updated source revision. Downloads share a bounded immutable artifact cache;
cancellation detaches one requester. Changed metadata/source/binding fails closed
and requires another preview. An installation is not execution approval.

Agent acquisitions publish through the same sparse registration maps as manual
installs, but the binding follows the target scope: **Application Settings** by
default (an install belongs to the user, not the project), or Workspace Settings
when `capability_activate` is called with `scope: "workspace"`. The writing layer
commits only the selected identity against its reviewed preimage, preserving
unrelated preferences and neighboring installs. There is no second runtime
installation registry. Application registrations are inherited by every
workspace; workspace registrations appear only in Workspace Settings. Live agent
changes refresh retained catalogs/sidebar/home through bounded passive
observation without reconfiguring transports or reading skill files on the UI
thread. Observation does not depend on a launcher screen: an active agent may
remove that screen while Settings or a capability sidebar remains visible. Open
inspected MCP tool details refresh their exact schema/grant in place, or clear
obsolete evidence when a tool disappears, without replacing focused filters or
editable drafts.

Cold retained-workspace creation initializes capability metadata off the actor
before shell attachment, without starting transports or downloading/executing
anything. Passive capability reads are independent of long-running settings
operations; editable policy reads still follow preceding saves. Explicit refresh
results are workspace- and sequence-fenced. Failed observation keeps usable named
resources visible with refresh-failed status, and invalid/setup/approval-required
MCP configuration cannot be hidden by an idle or cached transport state.

MCP details include Overview, Connection, Tools, Resources, Prompts, Access and
Activity. Connect/disconnect, detached Test, refresh, sign-in/out and Save
connection are explicit operations. Each tool requires a selected effect, scope
and current schema fingerprint. Unknown/changed operations remain denied; role,
profile, session and runtime ceilings still apply. A server annotation is not a
grant, and a scope label is not a local filesystem/network sandbox. Unsandboxed
local launches require explicit approval. External MCP mutations are not covered
by native file ownership or undo.

Agent `capability_activate` returns observed MCP tool fingerprints and the actual
usable tool set. A connected server with zero usable tools is not reported as
ready for tool execution. A separate `mcp_request_access` operation requests exact
`grants` (original tool name, observed fingerprint, intended effect and resource
scope). Activation itself accepts only the capability identity plus an optional
`scope`, so providers that require every schema property cannot confuse skill
activation with MCP access. New/changed grants require an immutable, explicit
approval and are written at the connection's own binding; source trust, automatic
acquisition, generic Always-read permission and agent selection cannot authorize
them. Grants persist after restart, but restart itself never reconnects or
replays tools. Explicit activation verifies current schemas before offering
dynamic tools again.

OAuth credentials are application-owned and bound to connection ID and endpoint.
Sign in presents the authorization URL with an explicit Open browser action;
headless users may paste the exact loopback return URL. State/issuer mismatch,
expired credentials, cancellation and reauthentication have recoverable states.
Secrets and callback payloads are never tool output. Resources and prompt
templates support explicit text-only fetch/preview and source-attributed composer
attachments. Changed session, agent or draft rejects a late attachment. Nothing
is submitted automatically.

The composer's visible **+ Capability** control and **Insert Capability** palette
action open the same descriptor-backed Skills/Tools/Resources/Prompts picker.
Skill selection stages a source-qualified invocation. Tool selection inserts a
selection-only reference chip, not a grant. Resources/prompts require arguments,
Fetch, exact preview and Stage before a source-attributed attachment appears.
Changing arguments, selected source, session/agent or composer rejects stale data;
closing cancels pending fetches. Management remains available through Settings
when no agent is selected.

Sources supports local directories, commit-pinned repositories and registries.
Trust/disable and acquisition budgets are independent controls. Automatic
selection and new acquisition are separate preferences; new acquisition defaults
to Ask. Resume restores selection/context passively without downloads, updates,
server startup or tool replay. Unsupported protocol features are declared in MCP
Overview instead of drawn as working controls.

Application settings include **Anthropic Skills (pinned)**, **Skills Directory**
(skills.sh community search), and **Official MCP Registry (preview)**. Community
skill review resolves GitHub HEAD to an exact commit before acquisition. The
Registry includes remote Streamable HTTP and version-pinned npm/PyPI stdio packages;
local packages stay unlaunched until their explicit setup and launch approval.
Unsupported transports/package requirements remain visible with a reason rather
than disappearing from search. All-source discovery interleaves source-ranked
results, preserves fuzzy API matches and displays usable results alongside partial
source failures. Results are bounded at 100 per search; the Registry reads up to
eight cursor pages. These sources do not cover every published skill or MCP server.
Built-ins are discovery presets, not permission:
they can be edited or disabled but not removed, and every acquisition still uses
the exact preview/grant flow.

Preference Save, entity Save and runtime operations have distinct transactions.
Cancel Settings does not undo a completed install/connect. Capability map removals
use explicit workspace deletion entries; Reset inherited restores application
definitions without changing them.

Agents → Tool Access groups built-ins, MCP tools and skill invocation modes.
`/settings` → Capabilities stages the owning role/root selection and links to the
manual manager. `capability_selection` in `session_agent_settings` persists
AllowedTools, DeniedTools and SkillModes; `root/<selected-agent>` overrides remain
separate. Selection cannot widen a category ceiling or create an execution grant.

Account IDs are stable; labels and provider-side OAuth account
IDs are not local identity. Removal requires confirmation and never reassigns
sessions. A missing account requires explicit selection, not silent failover.
OAuth refreshes are serialized per account and rotated tokens are persisted
before use. Model discovery, if offered in an agent/model picker, is scoped to
the selected account; stale auth and model results must not cross account
boundaries. Credentials remain application-only and do not enter workspace or
session data. Older or disconnected session history remains readable and asks
for an explicit account before sending another turn. Creating a session without
an account presents a setup warning. There is no automatic legacy credential
migration path; existing local credentials must be explicitly transferred or
re-entered.

The `/settings` session deck shows only configured account labels, without
provider-template groups or template names. Changing accounts clears the old
model choice. Model discovery resolves the selected account's endpoint and
protocol configuration, and its result belongs to the
requesting session deck and account generation even if another tab becomes
active. Changing account credentials or connection configuration invalidates that
account's cached models. Connection configuration remains application-owned.

### Server Storage

Accounts -> Edit account -> Protocol options exposes **Server storage** for
protocols that support storage. On OpenAI Responses, On (including its default)
requests stored response IDs and exact-prefix delta uploads: the first request
sends the complete context, then compatible requests upload only new messages
and tool results. Each account owns its protocol policy directly. This control
cannot grant a capability the endpoint does not implement. If a gateway returns
`store:false` or omits every response ID, Crude keeps full local context, uses
full uploads and reports the reason in both the session log and a transient UI
notice.
Changing model/account/protocol or context starts a new chain rather than
guessing that another server context is equivalent.

Responses protocol options also expose **Inline image replay recovery**. It is
On by default and configurable on each account. Crude first uses the documented
stored-response delta. If the endpoint returns an exact pre-output invalid
Base64 `image_url` rejection while Crude's inherited local image is valid, the
safeguard retries once with the complete authoritative context. That exact
endpoint/account/model/request scope is remembered in `model-catalog.db` for
seven days, so later inline-image continuations avoid a request already proven
broken. Other connections still use deltas normally. Off disables both the
recovery and the learned full-upload route; it does not alter Server storage.

Off or **Zero data retention** keeps full uploads and never silently opts into
retention. Disabled continuation logs `disabled`, does not retain new response
references, and never treats `store:false` or a missing response ID as evidence
that the endpoint lacks delta support. The endpoint-limitation notice is emitted
only when the request enabled continuation. Codex requires storage off.
Automatic server truncation and custom
wire-context overrides also disable exact-prefix delta reuse. The endpoint must
support Responses stored-response continuation; this HTTP mechanism is separate
from connection-local WebSocket continuation. Prompt-computation caching may
still operate during full uploads and is not the Store switch. See
[Responses delta uploads](prompt-architecture.md#responses-delta-uploads) for
expiry recovery, durable resume and safety boundaries.

### Network

Application Settings has a dedicated **Network** group ordered last in the rail
after AI, Security, and IDE:
**Connection**, **Proxy**, and **Certificates**. Workspace scope displays these
application-owned values read-only, never a project-controlled trust or credential
override. Connection reuse and HTTP/2, HTTP(S)/SOCKS routing, custom roots,
exact-host leaf-certificate exceptions and mTLS identities share one transport.
Certificate validation and preferences persistence run off the UI thread; failed
saves retain the exact busy/draft owner. See [network transport](network.md) for
defaults, supported protocols, security boundaries and explicit limitations.

### Connection Timeouts

Application Settings -> Accounts -> Edit exposes **Connection tuning** directly.
There is no template inheritance or Override dialer switch. Unset values use the
shared transport defaults; explicit values belong only to that account.

| Control | Default | Enforced scope |
|---------|---------|----------------|
| Request timeout | Unlimited | Entire chat request, including upload, streaming, retries and retry waits |
| Connect timeout | Network defaults | An explicit provider value bounds its TCP connection and TLS handshake; unset inherits separate Network budgets |
| Initial progress timeout | 60 seconds | Silence before positive byte progress in each upload/header/download phase |
| Min progress timeout | 45 seconds | Lower bound on adaptive silence after bytes start moving |
| Max progress timeout | 300 seconds | Upper bound on adaptive silence after bytes start moving |
| Max attempts | 6 | Total tries, including the first, before accepted model output |
| Max backoff | 30 seconds | Local exponential retry cap; all cooldowns including server hints have an absolute five-minute ceiling |

Progress limits must satisfy **minimum <= initial <= maximum**. Invalid
combinations stay in the editor with field errors and cannot be saved. The
watchdog resets only on positive byte progress; a slow moving stream can exceed
the initial timeout without being terminated. Raising only Request timeout does
not increase the progress watchdog. Unlimited disables the overall chat deadline,
not progress detection. For a model that stays silent for five minutes, set both
Initial progress timeout and Min progress timeout to 300 seconds, with Max
progress timeout at least 300 seconds.

Request deadlines apply to streaming and non-streaming calls even when their
caller has a deadline; the earlier deadline always wins. Model discovery honors
a configured Request timeout and uses a 30-second fallback when it is Unlimited.
Saved transport edits reach later turns and new auxiliary clients; requests
already in flight retain the values captured when their client was constructed.
Request-attempt exhaustion does not permanently end an unfinished agent task;
the [run supervisor](run-recovery.md) recovers transient conditions or waits for
actionable configuration repair. The retry ceiling is separate from the progress
watchdog and from reviewer/background-job attempt budgets.
The fields persist under the account's `connection.dialer` in `config.json`: `request_timeout_seconds`,
`connect_timeout_seconds`, `progress_startup_timeout_seconds`,
`progress_timeout_floor_seconds`, `progress_timeout_ceiling_seconds`,
`max_attempts`, and `max_backoff_seconds`. An omitted/zero field uses its built-in
default; zero Request timeout means Unlimited.

### Agents Catalog

Application Settings has one **Agents** catalog for Main Agent, Researcher,
Crafter, Aeron, **Memory Agent**, **Auto Review Agent**, and reusable named agents. Built-in roles can
be edited and reset to shipped defaults but not removed; Main Agent stays
available as a fallback. Named agents can be added, cloned, edited, disabled,
re-enabled, reset when shipped, and removed. None of those actions cancels work
already running. Search matches names, roles, models and purposes.

Each editor uses the shared tab strip: **Model**, **Instructions**, and **Tool
Access**. Model/account selectors use configured connections and discovered
models. Named agents persist their own thinking effort, context/output caps,
modalities, sampling, instructions, file ownership, tool limits, account, model,
and availability; the role supplies unspecified values. Instructions are an
actual multiline editor, not encoded JSON. Tool access offers role defaults or
explicit checkboxes; role safety ceilings remain in force, unavailable tools
are disabled, and child reporting stays required.

**Instructions extend policy; they do not replace it.** `prompt/` owns the
canonical model-neutral root/child policy and specialized background-job
contracts. User profile text is escaped as a lower-priority overlay; changing it
cannot remove core lifecycle/safety rules, grant tools, or select a vendor base
prompt. Memory and permission jobs keep their own no-tools contracts rather
than inheriting the coding-agent prompt. See
[model prompts and project guidance](prompt-architecture.md).

Memory Agent uses no tools:
its model, account, instructions, and availability live in Agents; automatic
recall/extraction cadence remains on the Memory page. Disabling Memory Agent
stops its automatic jobs and recall without deleting stored memories.
Auto Review Agent is also a no-tools built-in. Its explicit account, model,
enable switch, additional restrictions, and complete runtime policy live on its
Model / Instructions / Review tabs in Agents. It cannot inherit the Main
Agent's connection or change its fixed safety prompt; restrictions can only
narrow approvals and are limited to 1,024 bytes. Enabling it requires an
account and model. Security → Auto Review Policy is a synchronized projection
of the same persisted policy, not a second runtime authority. Resetting the
agent identity does not reset that policy. Hard constraints are stated beside
the controls they constrain and in this reference; they are not exposed as
editable controls.
Configured restrictions also apply to native model-command aliases and cannot
be bypassed by a model-supplied delegation allow list.

**Apply agent** stages the entity in the preferences draft; **Save** persists
settings. Invalid names stay in the editor, and closing or changing scopes
requires an explicit choice before discarding a changed draft. Named profiles
round-trip through `types.AgentProfileSettings` and session configuration; the
orchestrator advertises their names/purposes, and delegation resolves their
role, model/account, instructions and restrictions.

The launch tool exposes `profile` as the exact reusable child-profile selector,
separate from the allocated personal name. Unknown/disabled profiles and category
mismatches fail explicitly before launch; root-only/background profiles are not
advertised as child choices. Profile-owned path lists are copied and rooted to
the workspace without mutating the settings catalog.

The existing Model dropdown remains the selector. Discovery refreshes both its
retained items and open-list geometry, so the first response paints every visible
row without reopening or moving the pointer. Discovered capabilities fill
inherited values, including an already-selected model, without replacing saved
or in-progress manual overrides. Multi Agent remains a separate delegation
toggle in Main Agent and `/settings`; Main Agent has no redundant availability
toggle. The Instructions tab accepts native multiline edits for every built-in
role, including Memory.

Breadcrumb labels are individual navigation targets; separators are not.
Agents and Accounts return through the normal back/discard policy.
Session Settings and its role labels navigate to the corresponding level and
retain staged role edits. Their appearance and the Model / Instructions / Tool
Access tab layout are unchanged.

The floating Settings window's **×** and footer **Close/Cancel** buttons always
close the entire window, regardless of page depth. They never navigate Back.
Breadcrumbs, explicit Back controls and Esc retain their contextual navigation.
Closing with unsaved preferences or account, agent or capability drafts
opens one stacked discard prompt covering all current drafts. Keep settings open
preserves the exact page, draft and focus; Discard drafts closes Settings rather
than returning to a catalog. A changed or replaced draft invalidates the decision.

The `/settings` root form offers **Default agent** for this session: built-in
roles and enabled named agents (but not background-only Memory Agent). Choosing
the shared navigation row opens the same filtered picker page as Account and
Model, with the shared navigation chevron, breadcrumb, and Back/Esc behavior;
it is not an inline dropdown. Selection updates the root model/account
immediately. Save persists a
session-only identity plus that agent's independent account pin. Prior turns
retain their original identity. Future turns resolve the live profile, so later
agent edits update the model/instructions while keeping the account pinned.
Root overrides remain separate for each choice; switching back to Main Agent
restores its settings. A disabled/removed selected profile reports an error and
cannot start another turn until replaced; it never silently falls back. Session
reset returns to Main Agent. Save as Workspace requires Main Agent because the
root selection is session-only.

Each admitted turn also snapshots the resolved provider, account, and model IDs from
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

**Agents have persistent personal identities.** Root sessions receive names such
as Jeff, Tom, and Jake. Child identities include their parent: `Tom sub Jeff`.
Task/profile hints supplied to the delegation tool do not replace these names.
An app-wide transactional counter in `agent-names.db` allocates identities across
workspaces and processes. The catalog contains 544 given names and 528 surnames.
After the given names, meaningful full names such as `Jeff Anderson` extend the
namespace to 287,776 identities before compound family names are needed.
Arbitrary-precision surname tiers continue without numeric suffixes.
Names are retained in session metadata, tabs, agent lists, reports, and history;
resuming a named session does not allocate another name. Unnamed sessions obtain
their identity before becoming live. The guarantee assumes the allocator database
is retained, just as session identity assumes its session database is retained.

**Multi-agent mode is a live capability, not a startup-only setting.** A fresh
session creates no runner until its first permitted launch. The session owns the
runner; initialization binds its event sender and parent identity before any
child starts. Changing the switch does not replace that owner. Disabling blocks
new launches immediately, including calls from a model response already in
flight, while retaining running children, cancellation controls, and review
history. Already-running children finish normally unless explicitly cancelled.
The next parent model request receives a context-only settings notice, separate
from child failures; a refused stale launch returns `disabled_by_user` with
`user_disabled_multi_agent`, not an execution error. Re-enabling restores launch
tools on the next model request, including within an active turn. Toggling alone
does not start an idle model request. Persisted child history may instantiate a
runner on resume for review; this does not enable new launches.

**Auto permissions are an explicit, bounded opt-in.** Settings → Agents →
Auto Review Agent selects an explicit account and model; blank values never
silently inherit the main model. Its Review tab owns thinking effort, response
format, denial handling, per-attempt timeout, initial and retry token budgets,
retry count/growth, and input bytes. There is no lifetime/per-turn review-count
cap. Security → Auto
Review Policy edits the same persisted values. Security → Command Safety still
owns command severity and confirmation. Hard blocks, human-only permanent
grants, and uncertainty fallback remain enforced independently and are
described beside the relevant controls.
Application/workspace settings propagate live to existing sessions. The root
agent reviews eligible operations only when
a permission prompt would otherwise be required and YOLO is off. Children keep
their existing capability policies; this setting does not loosen those policies.

The reviewer sees only a fixed safety instruction, optional extra restrictions,
and bounded tool arguments—not conversation history, environment variables, or
file contents fetched behind the user's back. Recognizable credential markers,
out-of-workspace paths, symlinks, oversized arguments, delete/move operations,
and unknown/network/destructive shell commands stay on the human path without a
review request. Shell metacharacters and compound commands are also excluded.
This filter reduces risk; it is not a guarantee that arbitrary secrets or prompt
injections can be recognized. Eligible arguments are sent to the chosen provider.

A strict `allow` response grants only this call. A model `deny` asks the human by
default; users may choose automatic denial instead. **Unresolved review**
independently controls structural ineligibility, a model `ask`, and exhausted
technical recovery: `ask` pauses for a person, while `deny` rejects the exact
operation so an unattended agent can continue without widening authority. Valid
`allow`, `deny`, and `ask` decisions are never retried. Timeout, truncation,
transport failure, unsupported
automatic structured output, and malformed output consume only the configured
technical retries. Each retry gets a fresh configured timeout and its output
budget follows the persisted fixed / 2x / 4x strategy up to the persisted
ceiling. Stale settings and turn cancellation always abandon the result. No
reviewer result creates an “Always allow” grant, and no unresolved mode can turn
failure or uncertainty into approval. Hard command blocks apply to synchronous
and asynchronous execution before review.

System-path classification protects the filesystem root itself and descendants
of named system directories such as `/etc` and `/usr`. An absolute path under
`/tmp` or a user's project is not a system path merely because it begins with
`/`; normal command classification and approval rules still apply.

Defaults: human handling for denied and unresolved reviews, low thinking effort,
automatic response format, 15 seconds per
attempt, 256 initial output tokens, three total attempts, 4x retry growth, a
1,024-token retry ceiling and a 64 KiB complete-argument default (configurable
from 64 KiB to 1 MiB). Arguments are never truncated. Every exact non-hard-blocked
operation remains eligible for the whole turn, including unknown, network,
destructive and shell-composed commands; immutable hard blocks still run first,
while secret-shaped and oversized payloads stay local and fail closed. Roots and
children have independent reviewer instances. YOLO
bypasses review and confirmation for one owning session's complete agent tree;
another root/session keeps its own policy.
Each value is persisted and configurable within enforced bounds. Auto response
format tries strict JSON schema on OpenAI Chat endpoints and falls back to the
prompt contract when another configured attempt is available; explicit JSON
formats fail closed on incompatible protocols. Impossible combinations, such as
a growing retry budget whose ceiling is not above its initial value, stay in the
editor with a clear error. Response bodies remain capped at 16 KiB. Extra
restrictions are limited to 1,024 bytes. Usage is recorded per actual attempt as
`permission_review`, contributing to cost totals without replacing the root's
context measurement. Approved/denied tool rows carry a persisted reviewer label,
distinct from human permission tags. Choose a fast, inexpensive
instruction-following model; model judgment remains fallible, so keep
high-impact operations on the human path.

**Workspace root is execution truth.** Every workspace-created or resumed
backend session is bound to that workspace's `RootPath`. Relative file, glob,
grep, directory, synchronous command, async command, main-agent prompt, and
subagent prompt paths resolve against the bound root. Switching projects changes
only the shell attachment; retained old-project sessions continue for peers. New
sessions cannot inherit the process startup directory.

**Session operations are explicitly addressed.** Workspace creation/opening,
orchestrator startup, backend lookup and hydration name the session rather than
reading a globally selected one. The TUI owns its selected session, model display
alias and focus stack. Opening an already-live session reuses its runtime and
does not replay provider work. Normal CLI startup always attaches to the user core through `ConnectCore` and
`shellport/remote`; shell exit detaches rather than closing sessions. Remaining
release gates are in [core and shell separation](../plans/core-shell-separation.md).

Agent tab selection/visibility, focus and reasoning geometry are shell-local.
Core actors own semantic ingress, decisions and usage. Remote settings, sessions,
status and LSP are detached replicas installed only on Update; account reads are
redacted and unchanged stored-secret markers mean Keep, never credential replay.
Application defaults are republished to retained workspaces after core commit.
Connected account and preference forms retain their original opaque preimages;
a background refresh cannot authorize an older draft against newer credentials
or settings. Sparse workspace saves serialize with the settings lane and publish
live session/LSP projections only on the core actor after off-actor persistence.

Small queued application/workspace settings writes acknowledge local admission
before durable completion and report failures later; full preference Save awaits
the typed core result. Connected startup admits `--account`, `--model`,
`--provider`, `--key` and `--system` through immutable write-only invocation
selection on session create/open. All explicit selectors refuse already-live
sessions. Keys use ephemeral accounts, never application persistence; `--system`
adds profile instructions, not canonical policy replacement. Prefer configured
Accounts over keys in shell arguments. See [core client protocol](core-client-protocol.md).

Session form saves capture an exact backend operation and detached form on the
owner thread, run persistence off-thread, then install a revisioned snapshot on
the owner thread. Background work never reads mutable workspace session maps or
writes the frontend settings projection. The session lifetime fence rejects work
after closing begins and joins already-admitted saves before database teardown.
Snapshot revisions are process-local and meaningful only with the exact session
object and workspace generation. Connected saves additionally use typed core requests and durable mutation receipts;
the local snapshot revision alone is not a remote receipt.

The palette's Multi Agent toggle is a single-field operation on the captured
session's current delegation policy. It does not replay `LastSettings`, role
overrides, root choice, model/account pins or tool restrictions. Repeating the
same prepared operation returns its prior result instead of toggling again;
late/out-of-order completions cannot replace another owner or newer projection.

Stored overlays are re-diffed against current application defaults on load.
`Workspace.LoadWorkspaceOverrides` still contains a pre-existing legacy
whole-category `app_settings` reader. This is a remaining implementation/policy
boundary, not a new connected-shell conversion guarantee. **Reset section**
returns a workspace category to full inheritance. New format alignment must be
backed up and performed manually outside product code.

The selected account's `connection` supplies endpoint, authentication mode,
wire protocol, headers, request options and transport budgets directly. Embedded
presets only seed new drafts; editing them is not a runtime inheritance path.
The agent selects its Account and Model separately. There is no global active
provider selector, persisted Providers catalog, or compatibility reader.

`workspace.LoadAppSettings()` returns the **effective** snapshot (app +
workspace). Application defaults alone are `AppDefaults()`.

## Who writes what

| Action | Persistence | Must not write |
|--------|-------------|----------------|
| Application Settings → Save | `config.json` via `SaveAppSettings` | `workspace.db`, API keys on agent profiles |
| Workspace Settings → Save | field-level `WorkspaceOverrides` in `workspace.db` key `workspace_settings` | `config.json`, API keys, untouched fields |
| Workspace Settings → Reset section | `ClearCategory` then `SaveWorkspaceSettings` | other categories, app config |
| `/settings` or `/model` dock | session agent connection/model/protocol/thinking **and multi-agent** | app/workspace IDE prefs, API keys |
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
live text and savepoint state in the retained core; the connected coordinator
retains optimistic private edits and shell-local scheduling, while core services
own revision-checked `document.save` and atomic disk commits. Ordinary connected
editors use Open/Sync/Edit/Close/Save/Format, not local source reads/watchers or
`document.snapshot_save` as text authority. Accepted unsaved text and author undo
are durable workspace state. Revision acknowledgements cannot
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

The workspace owner writes sparse `workspace_settings` overlays. Its existing
`app_settings` legacy reader remains in `workspace/settings.go` and diffs those
values against current defaults. Removal/manual data alignment is outside this
connected-shell documentation change; do not infer that every legacy reader has
already been removed.

## UI

One floating window (`ui.OverlayWorkspaceSettings`) with a segmented
Application | Workspace switch in the **bottom-left** rail footer. Both
labels stay visible and clickable; the active side uses accent (orange)
text on a distinct background. Save/Close stay mounted across overlay
ticks (`SetModel` must not rebuild the action bars). Page anatomy, controls,
help strip and footer status follow [Settings design language](settings-design.md). Command Palette
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
threshold. The effective trigger reserves the main model's response and safety
headroom; the Aeron model budgets each extraction request against its own
capacity while preserving the configured reasoning effort. Each category's
explicit context window belongs to its own connection, not a global model
override. BTW and child-result notices count before the next request is admitted.
Crude extracts four plain-text continuation sections, then wraps them
deterministically. A source that fits uses the unchanged prefix; oversized
histories and individual tool outputs use chronological, attributed source
fragments and bounded model-backed chunk-and-reduce instead of deleting history.
Provider-confirmed overflow retries only after reducing the request; extraction
capacity adapts to reported usage and explicit context-limit rejections.
Checkpoints and verbatim human retention are sized for the continuation model.
Failure, truncated extraction, or cancellation installs no partial checkpoint.
Zero disables automatic compaction and overflow recovery; `/compact` uses the
same transaction. See [adaptive context handling](adaptive-context.md).
Leftover width is distributed as space after the label and before
inherited text (not filler periods). `·` is a column separator only.
Labels and values use `ColorFg`; inherited/meta use `ColorMuted`.
Up/Down cycle fields and the left-aligned Save/Cancel/Reset
row of the session deck; Esc closes or goes back. Agent rows keep a trailing `▸`
(`TreeCollapsed`). Never paint `❯`.

Visual contracts for this window:

- Inherited top padding (`TokenSet.FormTitlePad`) so the body is not flush
  with the window title; the first form section header shares that row
  with Search
- Subsection labels indent one column from section titles (`FormIndent`)
  on both the left rail and the right-hand form
- Search magnifier is trailing (`SearchGlyphEnd` + `PromptEnd`)
- Full-height **resizable** vertical divider between rail and form (`ui.SplitDivider`)
- Every page shares the three-row header (title or breadcrumb, subtitle,
  breathing row); fields are one row and the focused field's help is wrapped
  below the body
- Group gap on the left rail (`ListItem.Spacer` between AI and IDE)
- Section gap on the right (`Form` field gap + separator rows)
- Focused form row uses one background for label and control
- The Application/Workspace background toggle shares the Save/Close footer
  row; one full-width separator is directly above the entire row
- Save/Close right-align behind the unsaved-changes status; Save is enabled
  only while something is unsaved. Reset section sits on the opposite side of
  the form footer (just right of the vertical divider)
- `FieldSelect` is an overlay (z-order paint pass) with box-drawing chrome.
  Opening a dropdown does not grow form height or push later fields down;
  it flips above the field when there is no room below
- Left and right panels get an on-demand `ScrollbarControl` (`Needed()` is
  false while content fits)
- Form paint clips to the panel viewport. Scrolled fields must not overwrite
  the footer separator or Save/Close row
- Accounts is one searchable compact table with Add account/Edit/Clone/Remove.
  Errors/progress occupy its reserved status row, not the action row. Its editor
  replaces only the right pane; the frame title remains Application Settings or
  Workspace Settings, and the pane owns `Accounts ▸ Add account/Edit account`.
- Remove, dirty editor Back/Esc, and dirty Settings close use separate stacked
  modal WindowFrames. They retain the exact parent/draft and immutable selected
  preimage; no optimistic removal or persistence before acknowledgement.
- Account authentication, connection and credentials are one entity transaction.
  Clone has no copied credentials. Changing authentication clears incompatible
  credential state; OAuth/model results remain account/generation-owned.

## LSP

Application and workspace settings share two pages. **Code Intelligence** holds
the master enable, auto-detect, start-on-open, per-feature gates (diagnostics,
completions, hover, definition, references, outline, rename), diagnostic display
(editor / tree / status + minimum severity) and resource limits. **Language
Servers** is a searchable server table with a per-server editor: enable,
command, shell-style arguments, file extensions/language ID (custom servers),
project markers, environment table, and the JSON `settings`/`initializationOptions`
objects. Per-language `Languages` entries are the only server authority. `workspace.LSPManager` reads the **effective**
`LSPSettings` and refuses to start or request gated features.

The default subprocess transport is `lspclient/`, independent of the editor/TUI.
The workspace supplies it by default; merely constructing a manager launches no
server. Test transports may override that factory. Connected LSP has typed worker requests, replicated diagnostics/health and
per-client diagnostic consumers; rename returns `*protocol.WorkspaceEdit`. Shell
detach releases only its document views, not shared LSP servers. Universal
connected editors use the core live-text authority and per-client open/close
views; final native and distinct-host runtime acceptance remains separate.

The unfinished LSP runtime now has its minimal launch/settings contract so the
normal source build remains runnable. Keep LSP disabled until the lifecycle work
in [the active handoff](../plans/lsp-runtime-handoff.md) is validated. Its resource
fields default to two servers, a 300-second idle timeout and a 2 MiB document limit,
edited on Code Intelligence (the document limit in MB).

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
- `tui/settings/account_connections_test.go` — unified account connection, presets, clone and protocol isolation
- `tui/settings/dialer_test.go`, `provider/client_test.go`, and
  `provider/stall_test.go` cover shared provider/account controls, invalid limits,
  real HTTP header/body stalls, streaming request deadlines, and timer cleanup.
- `session/account_routing_test.go` and `app/config_test.go` cover live transport
  edits, account override isolation, persistence, and resumed account routing.
- `tui/settings/layered_prompt_test.go` and `accounts_regression_test.go` —
  owner-bound prompts, explicit discard/removal decisions, retained painted
  settings tables/editors at desktop and constrained terminal sizes
- `tui/screens/sessionsettings/session_settings_test.go` — account selection and multi-agent toggle
- `session/model_attribution_test.go`, `workspace/message_view_test.go`, and
  `tui/chatprojection/model_attribution_test.go` — request-time model switching,
  durable resume attribution, and immutable per-row chat headers
- `session/workspace_media_e2e_test.go`,
  `session/protocol_media_matrix_test.go`, and
  `workspace/tool_root_e2e_test.go` — same-turn multimodal tool delivery,
  protocol schemas, persistence/resume, capability and safety gates, and
  workspace-switch execution roots
