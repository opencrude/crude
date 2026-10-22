# Settings layers

> Status: Current reference for the three-level settings model (app → workspace → session).

Crude has three settings layers. Each stores only its explicit choices. A value
not configured at that scope inherits from its parent. The last explicit write
to a field wins; ordinary settings saves have no configuration revision,
preimage or conflict gate. Users and agents use the same rule.

```
Application  (~/.crude/config.json)
    ↓ MergeAppWorkspace
Workspace    (workspace.db / workspace_settings)
    ↓ explicit agent choices and session flags
Session      (session DB / session_agent_settings + session_flags)
```

## Explicit choices and live inheritance

**Workspaces inherit live.** `WorkspaceOverrides` stores a partial JSON object
per category holding only the individual leaves the workspace changed. Every
field it does not name keeps following the application layer forever, so
turning something off at the application level reaches the workspace even
years later.

On a project switch, the target workspace database is opened before the old
shell attachment is detached; peer work in that project is retained. Its effective theme is then applied before target-owned
editors, tree, and session projections are constructed, so no frame is built
with the previous workspace palette.

**Sessions also inherit.** A new session does not pin the parent's account,
model or thinking effort. Only an explicit session edit creates a local choice;
Reset removes it. Session metadata and historical request attribution do not
select a future account. Agent definitions and session overrides store account
IDs, not credentials or copied endpoints. Missing required configuration leaves
the connection unavailable and guides the user to configure it; Crude does not
choose another account, migrate a renamed field, or repair the configuration.

Forms compare their draft with its initial values to produce `SettingEdit`
entries: an exact path plus the new value, or removal to inherit. The storage
owner applies those entries to its latest file/row. Untouched siblings are not
replayed. An explicit `false`, zero, empty collection or parent-equal value stays
stored according to the field's declared semantics; it is not re-diffed away
against the parent. Arrays are one field, not separately merged items.

Application commits patch the original config JSON under its existing lock;
workspace commits patch the sparse overlay in one SQLite transaction. Session
edits apply to the current explicit session layer under its existing owner lock.
Read/request snapshots and publication generations are detached projections, not
extra configuration authorities. Parent changes affect later requests without
relabeling old output or scanning and rewriting closed sessions.

**Save as Workspace** patches only explicit eligible session role choices,
including Memory, delegation and the sub-agent limit. It preserves unrelated
workspace settings and the source session's choices. Session-only root identity,
Code Intelligence, log preferences, invocation secrets and capability opt-outs
are not promoted. **Reset section** removes only that workspace section and
preserves unrelated unsaved preferences. Role Reset previews its corresponding
parent role; session Reset removes the session preference layer, not history,
work or separate capability opt-outs. All wait for persistence before success.

Turn logging is also an explicit session flag. It is saved with the other
session flags in one transaction, under `session_runtime_state` key
`settings.turn_logs/<sessionID>`. Reset removes it and updates the running
logger; no workspace log flag is written.

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
never copying API-key or OAuth credentials. Account Save writes that identity's
explicit changes; unrelated preferences never replay the account map. Ordinary
account edits do not require a configuration revision. Credential rotation still
cannot revive a removed/reconnected account. Accounts remain application-only.

Additional request headers use a native **key/value table**
(`ui.KeyValuesFieldState`): Name/Value captions, Add header, row removal, and normal
editing/navigation. There is no inherited read-only header view. Secret fields
stay masked and refuse clipboard commands.

Accounts and Agents catalogs share full-width search, single-line results, and
bottom-anchored actions; narrow screens stack actions below results. The retained
Settings frame never resizes or moves because an editor opened. Back/Esc handles
navigation and exact discard decisions; × and Close close the whole window.

### First-use profile choices and AI setup

The selected core owns two application-only choices under `onboarding`:

- `introduction_completed` records introduction completion.
- `ai_setup_reminder_hidden` suppresses proactive AI setup reminders.

Both choices use the existing sparse field-edit save path.
They have no workspace or session override, separate setup database, trust database, or configuration revision gate.
Local and remote cores keep their own choices and accounts.

Introduction eligibility uses committed profile metadata before startup writes.
A saved completion, saved account, or saved workspace directory identifies previous use.
An absent flag or an empty configuration object alone does not identify previous use.
The metadata check does not open workspace databases or inspect project trees.
Unreadable or malformed configuration remains a repair condition, not proof of first use.

A new profile shows one native introduction with four feature rows and **Let's go!**.
That button opens the shared native folder picker directly at the launch folder.
The picker confirms the current root unless the user highlights another folder.
The completion save runs off the UI thread and does not block folder selection.
A failed save remains visible and passes to the attached shell as an operational issue.
The introduction can appear again if persistence fails.

Esc before **Let's go!** quits without completion.
Picker cancellation returns to detached project selection, not an attached launch-folder workspace.
Project attachment precedes AI setup guidance.
Established project launches keep the current workspace home, controls, and services.
Existing in-project switch confirmation and dirty-work protection remain unchanged.
See [startup admission](core-client-protocol.md#startup-before-workspace-attachment) for navigation-only locations and request bounds.

After attachment, a shell can show **Set up AI** when no saved account exists.
The reminder reads committed state and appears at most once per shell launch.
It has exactly two buttons: **Do not show this again** and **OK**.
OK has initial focus and opens the native Add Account form directly.
Esc defers setup without suppression.
Suppression closes the reminder only after a successful application preference save.

Suppression hides proactive reminders only.
An explicit New Session action still receives typed missing-account or missing-model guidance and offers the matching setup action.
Missing accounts lead directly to Add Account, not the Accounts catalog.
A saved account without a Main Agent model leads to Main Agent settings.
Neither condition creates session artifacts.
Historical-session account repair and request attribution remain unchanged.

Only this setup route continues from successful account Save to the Main Agent model draft.
The draft offers the new account without saving a role choice.
**Apply agent** stages the explicit model choice, and ordinary Settings **Save** persists it.
Account or model errors retain the correct draft, and cancellation leaves project files available.
Setup neither creates a session nor starts inference or retries a failed action automatically.
Normal authentication and model-list requests retain their existing behavior.

### Durable startup and operational recovery

Invalid or missing selected accounts, invalid connections or credentials,
unreadable/malformed configuration, invalid network policy/certificate files, and
failed session resume/startup do not lock users out of the IDE. Crude opens its
usable shell and shows a native, movable/resizable **recovery dialog** with the
reason, helpful repair guidance, and an explicit Accounts/Main Agent/Network or
Edit config file action. Close/Esc dismisses without a retry, fallback account,
reset, or data loss. Long details remain complete, copyable and scrollable.

The same evidence is recorded independently in Notices.
Opening Notices is optional.
New Session with missing configuration opens the matching native setup form, as specified above.
Other New Session and Resume failures show the recovery dialog.
Neither path adds an error to the launcher button or requires Notices to find the reason.
The launcher remains intact beneath the modal.
Failed registration releases unstarted backend workers.
Saved conversations and pending inputs stay with their session owner.

Configuration loading understands only current-schema fields. Unknown or
wrong-type ordinary leaves are ignored independently; valid neighbors remain
usable. This is not a legacy translator, automatic migration or repair write.
If a removed field was needed, the user must configure its current replacement.
Malformed files remain byte-exact and can
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
and Automatic/Manual only/Disabled modes. `/skills` opens the simple activation
modal instead of inserting an ID-based invocation into the composer.
Managed artifacts are immutable; Make local copy creates a distinct authored
identity before native editor opening. Remove unregisters the selected binding,
not the authored directory or another workspace's selected artifact.

An installed skill is a standard `SKILL.md`. The bounded YAML parser validates
identity, text, duplicate keys, anchors/aliases and resource containment. Other
clients' extension fields (including hooks) remain inert: Crude neither executes
them nor rejects the instructions because it does not implement them. Tool
requests accept spaces, commas or YAML lists, retain parenthesized expressions,
and never grant permissions. Scalar metadata keeps its literal text; nested
host objects are uninterpreted. `compatibility` is human-readable source guidance,
not a list of executable names, and accompanies activated instructions.

Loading/installing verifies content, not this host's readiness. Explicit
`crude.os`, `crude.arch`, `crude.product`, `crude.requires`, `requires` and
`dependencies` metadata declarations are checked separately before activation.
Missing dependencies leave a durable, readable **Installed · setup required**
skill; repairing them permits activation without another install or approval.
Disabled skills stay disabled. Corrupt/unsafe packages still fail installation.

MCP registration is likewise separate from connecting or launching. Registry
fixed headers and declared-variable defaults are preserved; missing required
headers install as empty editable header rows with **setup required**, not as a
silently incomplete connection. Fill them or bind Secret references in the
existing connection editor. Stdio setup checks include the actual inherited host
environment, with explicit values taking precedence; shell-host checks run on
that shell. HTTP headers never inherit same-named environment values implicitly.
Only declared registry variables are substituted, never arbitrary brace text.
Unresolved command/endpoint variables require manual configuration; unsupported
transports remain explicit rather than being guessed or silently substituted.
No credentials, launch approvals or tool grants are inferred by installation.

Install and Update first show the exact source, revision/digest, target scope,
requirements, requested access and previous binding. Confirm rechecks that
preimage before publishing. Search sources again explicitly before reviewing an
updated source revision. Downloads share a bounded immutable artifact cache;
cancellation detaches one requester. Changed metadata/source/binding fails closed
and requires another preview. An installation is not execution approval.

Remote skill review treats the selected repository subtree as remote metadata,
never a directory on the core host. The pinned archive is validated before
registration. Connected capability failures retain the actionable reason with
private connection/source/proxy values redacted rather than reporting a generic
settings failure.
Ordinary capability registration edits have no whole-settings revision gate.
Immutable install/update reviews and tool grants retain their exact security
preconditions; changing an unrelated preference does not invalidate a toggle.
Workspace capability commits patch only their addressed registration fields;
unrelated agent, IDE and capability choices stay untouched. Redacted sparse read
projections preserve field presence, including explicit false and entity
deletions, without inventing zero-valued acquisition or connection settings.

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

`/skills` and `/mcps` open separate **Skills** and **MCP servers** views of one
session capability modal (no tabs). It is quick, temporary per-session control,
not configuration: a padded heading naming the view and owning session (the
window border carries no title), local search and a single-column list of self-contained bordered cards: name,
compact facts (use mode, scope and source, or observed connection state and
transport), a switch with a square knob, and up to three description lines.
Clicking a card (or Enter / Right) expands it in place with the full description
and every fact (use mode, scope, version, path, source; or connection, transport,
endpoint and observed tools); clicking again (or Enter / Left) collapses it. The
switch (click or Space) turns that skill or MCP server off or on for the selected
session and every agent it runs (root and children), while they work or idle.
An empty view shows a framed guide explaining what skills or MCP servers are and
where to start, centred in the modal (Install & manage, adding your own in Settings, or asking an
agent); a search miss explains how to recover. One **Install & manage** button
opens the matching Settings page. Switches persist
in the session (`session_capabilities`), survive resume, and never change the
Application/Workspace registration, other sessions, the composer, MCP
transports or access grants. A registration turned off in Settings shows "Off in
Settings" and cannot be turned on per session; without a session switches are
read-only. Running agents observe a change at their next request or tool call:
an off MCP server's tools leave the request, and its calls, resource/prompt reads,
access requests and `capability_activate` are refused; skills cannot be activated
and search marks them off. Instructions already loaded stay in context. The
palette's **Session Skills** and **Session MCP Servers** actions open the same
views; the composer has no
**+ Capability** control and `/skill` and `/mcp` are removed. Toggle receipts are
session-owned explicit sets, without a configuration revision; refreshes are modal-lifetime/workspace-fenced and
preserve search, focus and scroll.

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
`/skills` and `/mcps` own the per-session view and open the manual manager;
`capability_selection` in `session_agent_settings` persists
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
for an explicit account before sending another turn. A New Session action without
an account presents the explicit setup guidance described above. There is no automatic legacy credential
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

#### Worktree hygiene

**Settings > Agents > Main Agent > Model** includes **Worktree hygiene** in
Application and Workspace scopes. These are ordinary sparse preferences under
`agents.execution_hygiene`, not security authority or execution admission limits:

| Setting | Stored leaf | Default |
|---|---|---|
| Remind parent to clean up | `warnings` | On |
| Warn at retained worktrees | `retained_count` | 16 |
| Warn after | `age_hours` | 24 hours |
| Warn at snapshot storage | `storage_mb` | 1024 MB |

Blank numeric fields inherit; explicit zero disables that threshold. The switch
disables inventory reminders altogether. None blocks launches, deletes work,
changes Keep or cancels agents. Thresholds describe nonremoved, non-Keep workspace
inventory, not only the current parent's children. Storage is retained active
manifest/snapshot bytes, not a complete working-directory disk-usage scan.

Parents receive quiet, deduplicated model-context reminders at request boundaries,
plus a short disposition reminder after each child outcome. The early shared
system contract remains even when inventory reminders are off. It requires
respect for intentional human work, exact owner/recovery evidence and reviewed
integration or archived discard, with explicit workspace-wide janitorial tools.
See [managed executions](managed-executions.md#parent-ownership-and-workspace-hygiene).

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
immediately. Save persists the session-only identity. Selecting it does not copy
or pin its account/model; only explicitly edited role fields override its live
definition. Prior turns retain their original identity.
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

**Concurrent sub-agents is a ceiling, not a queue.** Main Agent settings
(Application or Workspace) hold **Concurrent sub-agents** (`max_sub_agents`;
Auto = 4, maximum 32). A session may override it in Session Settings; the
override is persisted with the session (`max_sub_agents`) and, like every
session field, also settable through the control API. Changes apply live to the
session's runner: lowering the limit never cancels running children, it only
refuses new launches until enough finish. A refused launch names the running
children and the remedy (`agent_wait` / `agent_control`). Resetting the
workspace/app field to Auto restores the default, not the last explicit value.
Sub-agents start minimized in the shell; see
[main view panes](main-view-panes.md#new-agents-surface-a-tab-immediately).

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
Connected preference forms retain their original draft values only to identify
which fields were edited. They require no configuration revision. Untouched
stored-secret markers mean Keep. Sparse workspace saves serialize with the
existing settings lane and publish
live session/LSP projections only on the core actor after off-actor persistence.

Preference Save, section/session Reset, Save as Workspace, theme/autosave and
session shortcuts await the typed durable result. There is no fire-and-forget
settings writer reporting admission as Save. Connected startup admits `--account`, `--model`,
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

Saves store only explicitly edited leaves, even when equal to the parent. The
stored overlay is loaded as is. **Reset section** returns that category to full
inheritance. New format alignment is manual outside product code, never an
automatic migration or guessed configuration repair.

Each preference page has exactly one reset, owned by `tui/settings/page_reset.go`:
**Use Application settings** in Workspace scope (removes the page's stored
workspace leaves) and **Restore defaults** in Application scope. A reset removes
only the paths that page owns; pages sharing a stored section (Memory and the
Memory Agent, Security and the Auto Review Agent, Code Intelligence and Language
Servers, Skills and MCP) never clear each other. Collection pages (Accounts,
Agents in Application scope, Skills, MCP, Sources, Language Servers) and Network
have no page reset; their items are reset or removed individually. A reset
clears the page's staged model, so a reverted value cannot be saved back.

Editors have **Save** and **Back**. Back, Esc, ✕ and Close discard unsaved
editor changes without a prompt; navigating to another page from an unsaved
editor still asks. Save keeps the window open, and an agent's settings control
cannot close the window over unsaved edits.

The selected account's `connection` supplies endpoint, authentication mode,
wire protocol, headers, request options and transport budgets directly. Embedded
presets only seed new drafts; editing them is not a runtime inheritance path.
The agent selects its Account and Model separately. There is no global active
provider selector, persisted Providers catalog, or compatibility reader.

`workspace.LoadAppSettings()` returns the **effective** snapshot (app +
workspace). Application defaults alone are `AppDefaults()`.

## Who writes what

CLI `--list-sessions`, `--resume last` (or an exact ID), and `--delete-session`
resolve the selected workspace's current registry/store, never a global legacy
sessions folder. Offline deletion exclusively owns the core endpoint, refuses
active/foreign/unsafe identities, and retains the database, WAL/SHM, attachments
and registry preimage in the printed `.session-delete-*` recovery directory.
Failed registry commit restores staged files; a failed rollback keeps the recovery
copy and reports its location. Recovery is manual while the core is stopped; no
automatic format migration or silent transcript rewrite occurs.

| Action | Persistence | Must not write |
|--------|-------------|----------------|
| Application Settings → Save | scoped edits via `PreparePreferencesSave` → app-owned config transaction | `workspace.db`, API keys on agent profiles |
| Workspace Settings → Save | field-level `WorkspaceOverrides` in `workspace.db` key `workspace_settings` | `config.json`, API keys, untouched fields |
| Workspace Settings → Reset section | remove the addressed section via the same awaited scoped save | other categories, app config, unrelated draft edits |
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

Terminal is an application/workspace IDE category with field-level live
inheritance (`TerminalSettings`, override key `terminal`), placed after
Interface. It holds the shell (Auto, a shell detected on the core's machine, or
Custom with a command), shell-style arguments, Login shell, extra environment
variables, Open new terminals (Docked/Floating) and Scrollback (1,000–10,000
lines, 10,000 default). A workspace may choose a different shell while still
inheriting every other terminal field. Auto order, detection and failure rules
are in [in-app terminals](in-app-terminals.md#shell-selection).

The Local History window exposes logical/physical diagnostics and the command
palette provides `Create History Snapshot`, `Verify Local History`, `Prune Local
History`, and `Open Local History Folder`.

The workspace owner writes and reads only sparse `workspace_settings` overlays.
There is no `app_settings` reader; older rows are dead data.

## UI

One floating window (`ui.OverlayWorkspaceSettings`) with a segmented
Application | Workspace switch in the **bottom-left** rail footer. Both
labels stay visible and clickable; the active side uses accent (orange)
text on a distinct background. Save/Close stay mounted across overlay
ticks (`SetModel` must not rebuild the action bars). Page anatomy, controls,
field help and footer status follow [Settings design language](settings-design.md). Command Palette
entries:

- `settings.open` — Application Settings
- `settings.workspace` — Workspace Settings

Normal Settings opens **Application** scope. A muted `◇` at the rail's right
edge marks a section with workspace choices; the existing subtitle explains
“Workspace overrides some values · switch scope to review.” It never substitutes
workspace values into the Application form, changes geometry or opens a prompt.
The unsaved `●` takes precedence. Workspace scope does not repeat the marker.

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
editors such as `Compact context at Auto`) paints in that one column.
**Compact context at** is blank (shown as Auto) by default: Crude decides from
cost and capacity and prepares checkpoints in the background (see
[adaptive context handling](adaptive-context.md#when-to-compact-adaptive-policy-and-background-preparation)).
An explicit percentage is a provider-context trigger, not a local JSON-size
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
Recent turns always stay verbatim after a checkpoint. Each agent's **Keep older
input** setting bounds older verbatim human requests; blank (Auto) keeps them up
to the checkpoint's own size, and an explicit value is 1,000 to 200,000 estimated
tokens. Application values
flow into workspace defaults field by field, and a session inherits them unless
explicitly overridden, like its compaction threshold. The checkpoint, current runtime
handoff, tool/system envelope, and real continuation capacity remain hard bounds;
raising the preference cannot overflow them. Answered human requirements use the
same budget in their marked context-only history block. A child's latest assignment
may use all remaining continuation space rather than this recent-input cap.
Failure, truncated extraction, or cancellation installs no partial checkpoint.
Zero disables automatic compaction and overflow recovery; `/compact` uses the
same transaction.

Each agent's **Speed** chooses its processing tier: Account (the connection's own
setting), Standard, Fast, Priority or Flex (`service_tier`). The tier applies only
where the connection's protocol offers it and is otherwise ignored, so a choice
never breaks a backend without tiers. Children use their role's tier. `/fast`
toggles the fast tier for the session's main agent as an ordinary explicit
session override. Requests served at a premium tier report their cost as unknown
rather than at the standard rate. See [adaptive context handling](adaptive-context.md).
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

Enabling is lazy, not a workspace scan: with **Start on open** enabled, opening
a supported source file starts its configured server. Saving enable also starts
services for already-open source buffers without reopening or discarding their
unsaved text. With no source open, Problems / Workspace health says **Waiting for
source file** and its expanded details explain the next step. Disabled, paused,
manual-start, unsupported-file and document-limit states have distinct owner-derived
explanations; actual startup failures remain failures with their original reason.
**Detect servers** selects configured commands by file type; it does not install
their executables. The server must already be installed on the core host and
available to its process environment.

The default subprocess transport is `lspclient/`, independent of the editor/TUI.
The workspace supplies it by default; merely constructing a manager launches no
server. Test transports may override that factory. Connected LSP has typed worker requests, replicated diagnostics/health and
per-client diagnostic consumers; rename returns `*protocol.WorkspaceEdit`. Shell
detach releases only its document views, not shared LSP servers. Universal
connected editors use the core live-text authority and per-client open/close
views; final native and distinct-host runtime acceptance remains separate.

Resource fields default to two servers, a 300-second idle timeout and a 2 MiB
document limit, edited on Code Intelligence (the document limit in MB). Remaining
acceptance work is tracked in [LSP acceptance](../plans/lsp-acceptance.md).

**Agent code intelligence is a separate, session-bound switch.** Session
Settings → **Code Intelligence** (`code_intelligence`, persisted per session and
settable through the control API) gives that session's agents the `lsp` tool
(diagnostics, definition, references, hover, document symbols) and a post-edit
diagnostics notice for files an agent changed. It is independent of the editor
master enable: with editor LSP off, an agent's probe may still start the
configured server for the files it asks about; with the session switch off,
agents never start a server and the editor keeps its own behavior. A user stop
(paused server) blocks both. Probes only use servers configured and enabled
under Language Servers and are bounded by the same resource limits; they never
install, download or guess a server. Agent-owned documents are refcounted
separately from editor views, so closing an editor never closes a document an
agent probe holds (and vice versa). Diagnostics settle on the server's progress
reports, not a fixed sleep. Server launch and resource settings (command,
arguments, environment, settings, initialization options, limits) are
human-owned: an agent may toggle `lsp.languages.<id>.enabled` through the
control API but cannot change what runs.

## Semantic settings control and authority

The [control API](control-contract.md) discovers redacted settings, exact field
paths/types and observation revisions; ordinary set/reset operations use the
authoritative app/workspace/session owner with last explicit write wins. No
expected configuration revision is required. Generic native form Save resolves
the effect of **all pending drafts**, including off-page account/capability/agent
editors, not merely the currently visible ordinary field. Open epochs and edit
generations reject close/reopen and edit/undo ABA; modal decision policy is not
bypassed by semantic controls. Credentials, grants, consent and privilege changes
stay human-owned and cannot approve themselves through generic buttons/keys.

Capability acquisitions and exact stdio launch are eligible for Manual/Auto
Review/YOLO within explicit denials, source trust, host and effect ceilings. Human
MCP grant approval persists at the connection binding; reviewer/YOLO grants are
run-local, not durable human authority. Authentication/consent remain user actions.
Core is the default execution host; shell-host stdio requires an explicit absolute
local directory and exact attachment epoch. Full manual shell-host configuration
UI remains a limitation, not a promised automatic host selection.

## Agent settings and session root selection

- Accounts and Agents catalogs share a full-width search and
  bottom-anchored actions; narrow screens stack action rows below results.
  Main Agent remains available as a fallback. Built-in roles, Memory Agent,
  and Auto Review Agent
  can be edited or reset; named agents can be cloned, enabled/disabled, or
  removed. Memory's background jobs are no-tools completions: its account,
  model, instructions and enable flag live in Agents, while job cadence and
  recall limits stay on the Memory page.
- **Accounts own complete connections.** There is no Providers page or persisted
  provider-template map. `provider.Account.Connection` owns endpoint, auth,
  headers, wire protocol/options, dialer and extra body directly. Start from
  copies an embedded preset into a new draft only; Clone creates a new ID with
  detached connection bytes and no credentials. Never reintroduce override
  toggles or live template inheritance. Account IDs, model pins and provenance
  survive explicit development-data alignment outside product code.
- **Operational startup failures never lock out repair.** Missing/invalid account,
  config, network or session startup opens a usable shell plus a native recovery
  dialog with exact reason, guidance and explicit repair. Invalid network policy
  stays fail-closed; no default account/direct connection/reset is inferred.
  Notices are independently recorded but optional. Failed New Session/Resume
  must not paint error text under the launcher action or instruct users to check
  Notices for the reason. Close/Esc dismisses without replay or draft loss.
- Auto Review Agent is the bounded, no-tools permission reviewer. Every root and
  child gets an independent reviewer instance, per-agent-turn budget and audit
  attribution while sharing the persisted application/workspace policy. The
  owning session's live YOLO state bypasses review and confirmation for that
  root's complete descendant tree without affecting another root/session. Its
  enable switch, explicitly pinned account/model, extra restrictions and
  complete runtime policy live on the Agents Model / Instructions / Review
  tabs. Security > Auto Review Policy is a synchronized projection of that
  same persisted authority, not a second configuration owner. Thinking effort,
  response format, timeout, initial/retry token budgets, attempt count/growth,
  input bytes and denial behavior must never become hidden runtime constants.
  There is no lifetime or per-turn review-count cap: every exact non-hard-blocked
  operation remains eligible throughout a multi-day turn. Technical retries
  alone consume the configured attempt/token budget; valid
  allow/deny/ask decisions are never retried. An unresolved-mode choice may ask
  a human or deny the exact call so unattended work continues, but can never
  approve uncertainty, secret/oversized input or exhausted recovery. Unknown,
  network, destructive and shell-composed operations are reviewer evidence, not
  structural exclusions; immutable hard blocks still run before review. Security
  keeps Command Safety separate. Never add toggles for hard blocks, human-owned
  authority changes, or mandatory restore safeguards; explain those constraints beside the
  controls they govern. A rejected reviewer result never becomes an Always
  allow grant.
- `ui.FormField` paints multiline labels (textarea and key/value grid) on the
  containing surface even on hover/focus. Single-line labels may use the row
  highlight; multiline editor bodies retain their own fill and interaction.
- Agent model discovery must refresh retained dropdown items AND geometry on
  the first result, without rebuilding the draft or closing its open selector.
  Fill inherited capabilities without replacing manual overrides. Preserve the
  Model / Instructions / Tool Access presentation and Main Agent's Multi Agent
  delegation toggle; do not replace the model selector with a free-text redesign.
- Breadcrumb labels are separate navigation targets measured with shared
  terminal widths. Settings parents use existing back/discard ownership,
  session parents preserve staged edits, and file-tree parents reveal their
  row. Separators never navigate.
- `/settings` persists a session-only `SessionMainAgent`. A named profile or
  built-in role resolves the root connection/model/instructions at the next
  request boundary; explicit root edits live under a distinct `root/<choice>`
  session override. Selection alone never pins an account. Never collapse those
  overrides into workspace Main Agent
  or silently fall back if a selected agent is removed or disabled. The root
  selector excludes Memory. Save as Workspace must not promote this session-only
  choice into workspace defaults.
  Default agent uses the same compact navigation row and filtered picker page
  as Account/Model, never an inline dropdown or a different chevron dialect.

## Tests

Layer coverage lives next to the code:

- `types/settings_test.go` — field-level merge/diff, inheriting untouched
  fields, explicit `false` overrides, round-trip stability, credential
  stripping, LSP language/severity
- `workspace/settings_test.go` — overlay persistence vs app defaults, live
  inheritance of untouched fields and explicit autosave Off
- `types/settings_edits_test.go` — addressed writes, last-write-wins and inert
  wrong-type stored leaves
- `app/settings_layers_acceptance_test.go` and `app/settings_mixed_journey_test.go`
  — real file owner, framed core RPC, SQLite, interleaved session/agent/delegation/
  proxy/MCP/IDE preferences, restart, scope isolation and invalid trust rejection
- `tui/settings/explicit_layers_test.go` — one-field collectors across page
  families, quiet marker at desktop/constrained sizes and parent-relative reset
- `tui/viewmodel/save_workspace_test.go`, `session/explicit_layers_test.go` and
  `sessiondb/flags_test.go` — promotion with retained draft, reset/removal,
  live parent changes, stored-leaf tolerance and atomic session flags
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
