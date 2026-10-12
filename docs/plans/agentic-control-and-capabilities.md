# Agentic Control, MCP Capabilities, and Worktree Execution

> Status: IN PROGRESS — implementation plan prepared on 2026-10-11; product implementation has not started. Unchecked gates below are required outcomes, not current guarantees.

## 1. Goal and product boundary

Make Crude a dependable agent-operated IDE: agents can inspect and operate the
same detailed Crude controls as a person, acquire useful external capabilities
under the user's chosen autonomy policy, and isolate independent coding work
without improvising Git and execution ownership.

The program has four deliverables:

1. **Crude control:** one typed semantic command/inspection API, used by native
   agent tools and an internal MCP server, covering core operations and individual
   TUI controls. Opening a terminal means creating and visibly mounting it;
   opening/closing the tree means changing the addressed shell's actual UI.
2. **MCP interoperability:** correct mixed text, structured and media result
   delivery, bounded discovery/transfer, authentication, lifecycle, and truthful
   protocol capability reporting. No server-specific computer-use workaround.
3. **Capability autonomy:** discovery, acquisition, process launch, access and
   execution obey a coherent user-selected policy. Auto Review and YOLO must not
   encounter redundant, unconditional capability approval dialogs.
4. **Managed execution isolation:** runtime-owned worktrees, appropriate automatic
   selection, faithful starting state, safe integration and recoverable retention.

**Explicit non-goals:** no bundled desktop driver, preferred computer-use MCP,
second external agent loop, automatic OS privilege escalation, or promise of
universal machine control. Users choose integrations and may authorize agents to
discover/install others. "Any MCP server" means interoperating with declared
supported protocol features, not pretending every optional extension works.

This plan does not authorize changing the live core, production configuration,
credentials or existing user sessions during implementation. Risky experiments
use throwaway `CRUDE_HOME`s and unique files under `CRUDE_TEMP_DIR`. No destructive
Git rollback or forced worktree removal may discard unrelated work.

## 2. Current evidence and prerequisite contracts

Inspection on 2026-10-11 established these starting points:

| Area | Existing foundation | Gap this program must close |
|---|---|---|
| Core | Typed `coreprotocol` methods, revision fences, mutation receipts; settings/documents/terminals already have owners | No addressed shell-control channel or complete semantic product-control API |
| TUI | `ui.ActionRegistry`, stable component IDs, root/floating Hosts, normal action handlers | Registry is primarily metadata; incomplete typed dispatch and no complete inspectable control tree |
| MCP | Official Go SDK, stdio/Streamable HTTP, exact schemas/grants, OAuth and result parsing | `session/capabilities.go` flattens image content to byte descriptions rather than `ToolResult.Attachments`; non-image content needs a full typed audit |
| Acquisition | Automatic mode, pinned artifacts/previews, application/workspace registration transactions | `capability/service.go` rejects agent stdio activation; `capability/access.go` requires human access approval; `agent/tool_exec.go` uses an unconditional capability block path |
| Autonomy | Session-owned live YOLO, Auto Review Agent, root/child ceilings | Acquisition/launch/access do not consistently use that authority; generic Always-read is correctly not an installation grant |
| Isolation | Durable agent/run identity, file claims, exact child checkpoints and commands | `agent_start`/`SubAgentSpec` have no isolation/root field; child prompt, files and command defaults use the parent session's `WorkDir` |

The existing `TestTaskAcquiresSkillActivatesMCPUsesToolAndResumesPassively` enables
YOLO but still supplies an MCP access confirmation. It and
`TestCapabilityChangeDoesNotReuseGenericAlwaysReadApproval` passed during the
investigation. That is evidence of the current approval boundary, not acceptance
of the proposed autonomous behavior.

Implementation must preserve or deliberately revise these canonical contracts:

- [Core client protocol](../reference/core-client-protocol.md),
  [shell port](../reference/shell-port.md), and
  [core/shell release limits](core-shell-separation.md).
- [Settings layers](../reference/settings.md),
  [capability workflows](../guides/capability-workflows.md), and
  [network policy](../reference/network.md).
- [Component authoring](../guides/component-authoring.md),
  [control usage](../reference/control-usage.md),
  [surface catalog](../reference/surface-catalog.md),
  [interaction matrix](../reference/interaction-matrix.md),
  [main panes](../reference/main-view-panes.md), and
  [terminal width](../reference/terminal-width.md).
- [Prompt ownership](../reference/prompt-architecture.md),
  [orchestration](../reference/agent-orchestration.md),
  [workspace collaboration](../reference/workspace-collaboration.md),
  [run recovery](../reference/run-recovery.md), and
  [context continuity](../reference/adaptive-context.md).

Existing references remain implementation truth until their corresponding phase
lands. Replace obsolete human-only capability wording across code, schemas,
prompts, tests and docs in that phase; do not add a contradictory prompt overlay
or weaken a gate merely because this plan proposes new behavior.

## 3. Architecture and ownership decisions

### 3.1 One API, adapters rather than duplicate implementations

```text
Human keyboard / mouse / palette ----> shared action handlers
                                             ^
Crude native control tools ----> typed control API <---- Crude MCP adapter
                                  |                 |
                          core/domain commands   exact-shell commands
                                  |                 |
                          existing owners        TUI Update / Hosts

User-selected external MCPs ----> generic MCP runtime ----> typed tool results
                                        ^
                             shared authorization policy

Agent launch ----> execution context ----> shared checkout / managed worktree
```

Use neutral contracts (for example `types/control`) for command descriptors,
targets, inspection nodes and results. Names in this plan are proposed, not
offered interfaces. Core/domain adapters delegate to existing authorities; shell
adapters apply actions on the UI owner. `workspace/` never imports `tui/` and
neither layer receives reflected method calls, pointers or arbitrary field writes.

Reuse the action registry's IDs and metadata, moving transport-neutral portions
out of `tui/` where necessary. UI key bindings remain presentation-owned. There
must be one command catalog and one handler per operation, not a separate MCP
switch that reproduces palette semantics. Controls not represented by palette
commands use shared component actions with explicit semantic adapters.

### 3.2 Exact targets and admission authority

- A control request binds caller agent/run or external-client authority, logical
  workspace, optional execution context/session/resource, and exact shell client
  ID + attachment epoch when it changes UI. Tab focus is not an identity.
- Capture the originating shell when human input is admitted; retain its control
  binding through delegation, compaction and run recovery. A later tab switch
  does not retarget the run. Selecting another shell is an explicit operation
  within the user's granted scope, never an implicit broadcast.
- Serialize shell actions on Update, preserve normal modal/focus/capture policy,
  and validate workspace/component/open epochs and source revisions. Physical
  user changes invalidate stale automation rather than being overwritten.
- A detached/missing shell returns a structured target-unavailable result. Core
  operations remain headless-capable. Do not choose a random remaining shell.
- Capture settings scope explicitly. Application defaults, sparse workspace
  overrides, session settings and shell-local geometry keep their existing owners.
- External MCP clients do not get an unrestricted wrapper over the current
  same-user core RPC transport. Every offered command passes control admission,
  effect/scope checks and human-only boundaries; raw RPC access is not a grant.
- Ordinary settings edits cannot let an agent enable its own YOLO, trust a new
  source outside policy, enlarge its ceiling, approve its own prompt, or invoke a
  privileged control through a generic button/key operation.

### 3.3 Operation outcomes and uncertainty

Return exact resource IDs, operation/request identity, resulting revisions and
separate states for admission, domain commit, UI application, pending decision,
failure and uncertain effects. A UI operation may report a produced frame
revision; that does not prove the physical terminal displayed it. Tests must
inspect rendered output and real captures where relevant.

Classify context-sensitive operations after resolving their target. In particular,
PTY input is execution authority, clipboard/external opens are local-host effects,
and a settings save that changes security policy is not an ordinary preference
write. Per-command metadata must survive the generic dispatcher and MCP adapter.

Use existing durable core receipts for domain mutations. Define bounded,
epoch-fenced shell receipts for UI effects and reconcile by inspection after
disconnect. Multi-stage actions are not a distributed transaction: if terminal
creation commits but mounting fails, retain its identity and report partial
completion. Recovery must not create another terminal or replay PTY input.

Cancellation stops outstanding owned work; it cannot claim to undo an already
committed effect. Reads, mutations, waiting and progress are bounded and off-UI
where they perform I/O. Core actors must not block waiting on shell presentation
or an agent whose command itself needs the actor.

## 4. Deliverable A — precise Crude control

### 4.1 Command and inspection contract

Expose a small discover/inspect/action/operation-status surface with per-command
argument schemas, effects, target requirements, availability/precondition reasons
and result schemas. Retrieve relevant descriptors on demand, not hundreds of
schemas in every provider request. A generic dispatcher must authorize the
resolved operation's actual effect; granting catalog/read access cannot grant all
mutations hidden behind an `execute` name.

Build a semantic UI tree from the owning components and models:

- Stable logical control/resource identity plus lifetime/revision fencing.
- Role, accessible name, non-secret value, bounds in terminal cells,
  enabled/visible/focused/selected/expanded state and allowed actions.
- Activate, select, expand/collapse, set value, focus, scroll/reveal and resize.
- Pageable model-backed rows for virtualized lists/trees, including offscreen
  items; do not equate mounted components with the entire resource inventory.
- Support retained fields routed manually by Forms, floating Hosts, decks and
  nested content. Never invent a second keyboard, selection or scrolling engine.
- Read-only explanation of unavailable/human-only actions. Masked secrets,
  private peer drafts and authorization decisions are not generic control values.

Prefer explicit setters (`visible: false`, exact tab ID) over repeated toggles.
Document selection/range units and Unicode behavior. All UI automation enters
the same action/policy path as human input, not direct mutation of inner editors.

### 4.2 Coverage inventory and acceptance families

Build a checked inventory from the surface/interaction catalogs and runnable
registry entries. Each operation must be implemented and tested, explicitly
human-only, or a documented product limitation. Unsupported entries cannot count
as completed coverage. A few palette wrappers are not completion of this feature.

| Family | Required detailed behavior |
|---|---|
| Shell/tree | Show/hide/select destination; reveal path/ancestors; filter, expand, select, scroll, focus; resize sidebar; rail/panel actions |
| Panes/tabs/windows | Open/select/reorder/move/close tabs; split/merge/resize/focus groups; inspect layout; move/resize supported floating windows; preserve compact-grid behavior |
| Editor/search | Open exact path/position; reveal/select/edit; undo/redo/save/format; find/replace/project search; dirty-close/conflict flow and revision fencing |
| Terminal/jobs | Create + mount/select; dock/float/hide/resize; acquire correct controller lease; read rows, send input, release; separate hide from close/terminate; exact job stop |
| Settings | Search/navigate/inspect fields and effective inheritance; field/entity updates/reset/save; model/account selection by identity; complete draft/conflict/error handling |
| Sessions/agents | Create/open/select; minimize/restore vs terminate; lifecycle and recovery; isolated execution choice; no duplicate backend or accepted input |
| Chat/composer | Exact-pane follow/scroll/disclosure/selection; inspect/edit the authorized draft and references; stage vs send explicitly; preserve drafts on decisions and owner changes |
| Domain views | Git review and reviewed-byte actions; Local History previews/restore; Memory/Board/capability views through their existing owners and safeguards |
| Shared controls | Buttons, action bars, lists/trees/tabs, fields/selectors/toggles, breadcrumbs, scrollbars/dividers, menus, dialogs and decks |

Decision prompts keep their semantic restrictions. An agent may handle ordinary
task choices within policy, but cannot act as the human approving its own tools,
credentials or authority. Dirty-file discard/restore retains exact preview,
mandatory backup and undo even when policy authorizes an unattended decision.

### 4.3 MCP publication

Implement the built-in MCP adapter using the existing official Go SDK. Initial
external route: an opt-in `crude --mcp` stdio adapter attached to the retained core,
with explicit workspace/target selection and the same control admission. Native
Crude agents call the typed API directly; they need no self-MCP subprocess or
recursive acquisition. Never log diagnostics to MCP stdout.

No public unauthenticated HTTP listener. A future remote MCP transport is not an
excuse to bypass the existing core/SSH trust boundary. The connected-core and
exact-shell journeys are required regardless of the MCP adapter transport.

## 5. Deliverable B — generic MCP interoperability

Audit the full SDK → capability → session/tool → provider → persistence → UI
path, including tool results, resource bodies and prompt messages. Define one
ordered neutral result/attachment representation shared with native tools;
preserve source role/identity for external prompts without turning them into
system or human instructions. Support must be based on the selected
protocol/model, not a special case for one server or model family.

- Preserve ordered mixed text, images, supported audio and embedded resources,
  resource links and structured content without stringifying binary envelopes.
  Convert supported media to the same-turn tool attachment path; validate MIME,
  byte/dimension limits and provider modality contracts. Preserve useful evidence
  on `isError` results too.
- Unsupported media returns an explicit reason and retains available evidence;
  never return success with a hidden text-only downgrade. Validate output schemas
  when provided and retain structured-result provenance.
- Resource links are source-bound references, not instructions or permission to
  read a local `file://` path. Fetch through the owning server and applicable
  access policy; do not blindly dereference arbitrary URLs or request credentials.
- Budget encoded and decoded media, content count, schemas and response bytes.
  Use bounded artifact/attachment transfer where appropriate. Oversized results
  fail clearly; optional image resizing must preserve/report coordinate mapping,
  not silently change screenshots used for clicks. Do not globally remove limits.
- Handle tools/resources/prompts pagination and list changes, schema invalidation,
  progressive discovery/selection and bounded refresh without per-frame I/O or
  republishing every connected tool into every agent request.
- Verify progress, timeout, cancellation, reconnect, transport close, OAuth
  renewal and schema changes. Read-only failure may retry under policy; an
  ambiguous external mutation stays unknown and is not reissued automatically.
- Publish an SDK/protocol feature matrix. Tools/resources/prompts and their
  standard content/lifecycle paths are the initial required profile. Sampling,
  elicitation, tasks, channels, roots, subscriptions/completion and rich MCP apps each
  need an explicit support/refusal decision; optional extensions are not silently
  advertised. Expand support only with real handlers and security/lifecycle tests.
  Any implemented server callback binds its session/agent/connection epoch and
  runs through normal admission: no hidden model request, fabricated human
  response or wrong-session dialog. Negotiate only implemented capabilities.
- Support an explicit execution-host binding: core host or selected connected
  shell host. The generic shell-host bridge launches/manages MCP off Update with
  exact attachment and policy fences; it is not a desktop driver. Remote cores
  must not misrepresent a core-side process as access to the user's laptop.
  No broadcast clipboard/desktop effects, automatic host fallback or credential
  forwarding to unrelated machines.

Regression fixtures must include mixed content, malformed/oversized data,
resource identity collisions, errors with attachments and interruption after an
external effect. Verify actual root and child provider requests, batch results,
durable resume and compaction; parsing an image at the MCP client alone is not
acceptance. No remote write or media replay on passive resume.

## 6. Deliverable C — autonomy without inconsistent approval gates

### 6.1 One effective authorization path

Acquisition, launch, tool access and execution remain distinct typed intents but
use the same effective policy resolver as other agent actions. The actor is the
owning session/root/child at its current run generation. Settings, explicit
denials and category/profile/parent ceilings remain authoritative.

Policy precedence is explicit: fixed hard blocks and denials/ceilings first;
selected host/source and Manual-only/Disabled restrictions next; existing exact
authorization next; then configured automatic acquisition, Auto Review or YOLO
for the remaining eligible operation. Acquisition `ask` is a default confirmation
choice, not a hidden veto on an explicitly autonomous mode. Inspect/update is a
new version intent under the same policy: autonomous updates are allowed when
authorized, but never float/replay an unreviewed revision during passive restart.

| Policy | Planned behavior |
|---|---|
| Manual/Ask | Reuse exact existing authorization; ask only when new authority is needed; acquisition and use remain separate visible effects |
| Automatic acquisition | Resolve eligible configured-source versions and acquire without another install click; does not alone change execution authority |
| Auto Review | Evaluate eligible acquisition/launch/access/execution intents through the configured reviewer and policy, without mandatory human-only capability exceptions |
| YOLO | Skip routine confirmation/reviewer for the owning session tree's acquisition, launch, access and use within explicit user-selected host/source authority and fixed ceilings |

Do not redefine a generic Always-read grant as installation authority. Do not
turn reviewer failure/uncertainty into approval. Respect Manual-only/Disabled
choices and explicit source/connection bans; an agent cannot rewrite those to
escape denial. Broad user-selected source/host policies may authorize autonomous
selection among candidates; registry membership or server annotations alone are
not proof of trusted code or enforced scope.

Apply policy before package scripts/process startup, not just before dynamic
tool calls. An unsandboxed launch is a host-execution effect; when the user has
authorized that class of execution, do not reject all stdio servers waiting for
a hypothetical future sandbox. When sandboxed execution is required, prove the
enforcement or return a concrete unsupported-policy result.

### 6.2 Preparation, publication and autonomous use

Prepare exact immutable source/revision/artifact, host, registration preimage,
launch configuration and observed tool intents. Revalidate after approval or
review and before publication/effect. Fetching verified bytes, launch, discovery
and selecting usable tools may span requests, but each step retains its original
identity and phase rather than restarting an uncertain installation.

Record authorization provenance: human, configured policy, Auto Review or YOLO,
with owner/run/policy revision and exact scope/fingerprint. Automated authority
must not be fabricated as human approval or silently create a permanent
unrestricted grant for all sessions. Installed registrations remain durable at
their selected binding (application by default); task/session use authority and
user-chosen persistent grants are separate. Extend current contracts atomically
if needed, with backed-up manual development-data alignment, not legacy readers.

New usable tools become available at the next request boundary for the exact
actor. Respect inherited allow/deny selections and effect ceilings; installing a
server does not widen a read-only child. Scope labels remain evidence, not a
filesystem/network sandbox. De-duplicate concurrent acquisitions/connections and
bound selection/installation costs to prevent marketplace loops.

Authentication, missing credentials, OS consent, explicit user-interaction tools
and genuinely unavailable authority may still need attention. Return typed
reasons and adequate alternatives; unattended deny-mode continues without a
hidden prompt or an endless retry. Passive resume restores data, not transports,
downloads, updated grants or historical effects.

### 6.3 User experience and prompt changes

Explain the effective policy in Acquisition/Security and exact capability details,
including host execution and registration vs use authority. Keep one source of
settings truth; do not add another "autonomous MCP" toggle that disagrees with
the selected mode. Use the existing activity/Notices system for meaningful
install/launch/access outcomes, not a toast or modal for every tool call.

Change canonical `prompt/` guidance from missing-capability-only discovery to
proactive relevant installed use and acquisition that materially improves a task.
Teach exact identity activation, user-chosen autonomy, declared limitations,
external attribution and bounded discovery. Tool descriptions/results must tell
the same truth and must not universally demand a human when policy can authorize.

## 7. Deliverable D — managed worktree execution

### 7.1 Choice and ownership

Add a first-class execution context and proposed
`agent_start(..., isolation: auto | shared | worktree)` plus a session-level
create/enter/select route. A project/workspace remains the logical owner of
collaboration, settings and sessions; a task checkout is an execution root, not a
global workspace switch or silently unrelated collaboration store.

Do not toggle the shared `Session.SetWorkDir` while another agent is running.
An immutable per-run execution binding is persisted before effects; entering or
switching a checkout fences old work at a safe boundary. Live commands and child
runs retain their original binding. Capability installations/registration scope
remain with the logical app/workspace owner, not a new worktree shadow registry.

`auto` prefers isolation for independent concurrent implementation, overlapping
changes, alternative approaches and risky/background experiments. It prefers the
shared checkout for read-only work, small tightly coupled edits and deliberately
partitioned tasks. Allow explicit selection and report the resolved mode/reason.
An explicit `worktree` request in a non-Git project fails clearly; `auto` may
choose shared with a stated reason. Worktrees are not security containers.

The workspace-owned service controls create/inspect/review/apply/retain/cleanup.
Persist exact execution identity, canonical checkout and common Git directory,
base commit, baseline manifest, owner/run, result and integration receipts.
Expose location/status in agent/session details, reports and control inspection.

### 7.2 Faithful baseline and execution binding

- For delegated continuation, start from the parent's exact local commit plus an
  immutable snapshot of applicable current staged/unstaged files and accepted
  shared-document text. Do not default to remote main or modify the parent's
  branch/index to create the snapshot. Independent branch tasks may explicitly
  select another commit and must disclose the difference.
- Handle new/deleted/renamed files, binary bytes, modes and relevant untracked
  files explicitly. Unsaved accepted document revisions are first-class source;
  unresolved private shell edits are not silently shared. A baseline that cannot
  be captured faithfully must not launch a child claiming it has current work.
- Ignored environment files/secrets require an explicit include/setup policy;
  do not copy every ignored directory, symlink/Junction target, build cache or
  credential. Setup scripts/install effects use normal execution policy and
  isolated test data, ports and resource ownership where needed.
- Bind native reads/edits/patches, all command forms, guidance, media, claims,
  permission-review payloads, MCP roots/local launch, history and recovery to the
  execution root. Claims are checkout-qualified;
  resource views (Git/documents/LSP/history) resolve the correct checkout without
  replacing the logical workspace's primary root or creating competing owners.
  Preserve exact absolute external-path grants; never rebase them silently.
- Validate linked-worktree `.git` pointer/common-directory behavior, spaces,
  Windows paths and submodules. Separate indexes do not isolate shared Git refs,
  config, credentials or services. Shared metadata mutations need repository-level
  serialization and ordinary authorization.
- Exclude Git administration from content snapshot/copy/restore/integration for
  both `.git` directories and linked-worktree `.git` files, including the resolved
  common Git directory. The existing directory-ignore pattern is not proof of
  regular-file pointer protection; add a regression fixture before enabling it.

### 7.3 Integration, recovery and cleanup

Produce a delta from the captured baseline, excluding inherited human/sibling
dirty changes. Review exact changed bytes; apply through existing document/file
mutation authorities, Local History safety receipts and precondition checks.
Three-way integration may preserve non-overlapping newer edits, but conflicts,
unsaved/private ownership ambiguity, changed modes/types or unsafe paths stop
with an actionable comparison. Never cherry-pick/copy blindly or use destructive
checkout/reset/clean to make integration succeed.

Auto-authorized integration can proceed after these safeguards; otherwise present
the existing explicit decision path. A child completion/report is not integration
success. Parent validation runs after integrating selected results. Include
operation-scoped undo without assigning opaque command effects to a false author.

Active processes/runs pin their checkout. Cancel/failure retains unintegrated
work; restart hydrates exact checkout/receipts passively, without rerunning setup
or commands. Cleanup requires verified ownership, no active users, and durable
recovery evidence for any result being removed. Respect user-created worktrees,
commits, symlinks/junctions and submodule work. Disk/count budgets refuse new work
or prune proven safe owned artifacts; they never force deletion to meet a quota.

## 8. Implementation sequence and bounded work assignments

All stages begin unchecked. Update evidence and current references in the same
change that lands a stage. Stage 0 fixes interfaces before dependent edits; later
parallel work has exclusive concrete file scopes and no duplicated whole-repo
validation. The parent owns policy decisions, integration and consolidated gates.

| Stage | Deliverable / primary owners | Dependencies and exit condition |
|---|---|---|
| **0. Contracts and regression fixtures** | Neutral control/execution/authorization/result contracts; `coreprotocol`, `types`, service owners; coverage inventory | Pin exact scope/host/receipt semantics, security-sensitive action classification, worktree baseline rules and numerical budgets; capture failing real-path fixtures for current image/approval/root gaps before implementation |
| **1. Crude control vertical slice** | Shared handlers, core→shell routing, native tools and MCP adapter; `core`, `coreclient`, `shellport`, `tui/viewmodel`, `tools` | Stage 0; discover/inspect → hide tree → open exact file/pane → create and show terminal → scoped settings save works in the real connected product and external SDK client |
| **2. MCP content and lifecycle** | `mcpclient`, `types/capability`, `capability`, `session`, provider attachment/persistence paths; generic shell-host bridge | Stage 0; mixed-content provider requests, bounded transfer, list changes, OAuth/cancel/unknown outcomes and explicit host placement pass root/child/batch/resume tests; may run independently of Stage 1 |
| **3. Coherent capability autonomy** | Shared policy admission, prepared acquisition/launch/access, evidence/persistence and settings UX; `agent`, `session`, `capability`, `mcpclient`, `workspace`, `app`, `tui/settings` | Stage 0 + Stage 2 result/lifetime contracts; manual/Auto Review/YOLO matrix passes end-to-end with no unconditional install/stdio/access prompt in authorized autonomous modes |
| **4. Complete semantic control coverage** | Shared component actions/inspection and remaining product adapters; `tui/ui`, screens/viewmodel and domain owners | Stage 1 + Stage 3 authority boundaries; every cataloged actionable control has tests or a justified human-only classification; no broad dispatcher/secret/approval bypass |
| **5. Managed worktree lifecycle** | Execution-context propagation, native Git/checkouts, baseline, integration, retention and UX; `workspace`, `session`, `agent`, `collaboration`, documents/history | Stage 0, using Stage 1 control entry points; isolated workers see faithful dirty/unsaved baseline, all tools use correct roots, independent results safely integrate and survive failure/restart |
| **6. Guidance and final acceptance** | Canonical `prompt/`, tool descriptions, current references/guides and consolidated release evidence | Guidance changes accompany each usable stage, not future promises; finish full cross-layer journeys, performance, native-platform/SSH gates and remove this plan only after remaining obligations are resolved |

First implementation task: Stage 0 contracts and regression fixtures. First
user-visible milestone: Stage 1 connected control journey. Do not begin with
RobotGo/Cua installation or a prompt-only worktree recommendation.

## 9. Acceptance matrix

- [ ] **Control parity:** real composer request hides/shows the tree, reveals a
  file/line, arranges panes, opens a visible terminal, resizes it and changes only
  the requested settings scope; native and MCP routes share the same results.
- [ ] **Minute controls:** discover/select dropdown, edit retained form, scroll an
  offscreen item, resize a divider and close a dirty tab through its decision path.
  Cover root, floating, manual composites and decks; preserve focus and drafts.
- [ ] **Ownership:** two shells + two sessions retain independent UI; original
  shell disconnect/workspace switch, epoch replacement, concurrent human input,
  hidden/minimized views and no-shell operation never retarget effects.
- [ ] **Security:** read-only control discovery cannot execute mutations; generic
  activation/key/terminal input cannot approve itself or evade action policy;
  secrets/private drafts stay absent; external same-user MCP obeys its grant.
- [ ] **Receipts:** lost response, cancellation and domain-commit/UI-failure races
  retain exact outcomes; no duplicate PTY/session/file creation, save, send or
  external mutation; unchanged explicit setters are idempotent.
- [ ] **MCP content:** real SDK fixture returns ordered text + image + structured
  content and error evidence; actual model requests contain the image bytes.
  Check every registered provider's supported encoding and explicit negative
  cases, root/child/native batches, cold history and compaction. No JSON/base64
  envelope in compact chat titles or mistaken success on unsupported media.
- [ ] **MCP lifecycle/host:** pagination, list/schema changes, budgets, timeout,
  cancel, OAuth/secret redaction, revoked policy, disconnected local helper and
  remote-core/local-shell placement pass without automatic host fallback/replay.
- [ ] **Autonomy:** real scripted model loop finds a useful uninstalled skill and
  stdio/HTTP MCP, acquires/activates/selects/uses it without a human-response helper
  under authorized Auto Review/YOLO; manual asks once where required. Test policy
  changes mid-operation, stale versions, denials, ceilings, missing credentials,
  setup failures, concurrent acquisition and passive restart.
- [ ] **Worktree choice:** read-only task stays shared; independent overlapping
  implementations and alternative candidates isolate; explicit modes override
  heuristics; non-Git and unavailable environments report truthful outcomes.
- [ ] **Worktree fidelity:** staged/unstaged/new/deleted/renamed/binary/mode/accepted
  unsaved source reaches the worker correctly; inherited work is not in its delta;
  reads, patches, all command variants, guidance/claims/history/media agree on root.
- [ ] **Worktree integration:** concurrent workers plus newer human edits preserve
  unaffected content, conflicts stop without partial loss, undo is exact, child
  verification and final integrated tests agree; local checkout is never reset.
- [ ] **Worktree retention:** cancel/crash/restart/late results/cleanup races,
  active commands, user-created trees, submodules, Windows junctions and quota
  pressure preserve recoverable work and exact identity; zero passive replay.
- [ ] **Discovery and performance:** unchanged paint/idle/ticks perform no RPC,
  scanning or whole-tree exports; large inventories use bounded pages/deltas;
  schema selection and media admission fit explicit measured budgets. Add dense
  benchmarks before optimizing; retain existing ceilings.
- [ ] **Guidance:** root/child prompts, tool descriptions, runtime results, settings
  explanations, compaction handoffs and docs agree on installed use, autonomous
  acquisition, precise self-control and when isolation is appropriate.

### Validation commands and evidence limits

Run focused regression tests first, complete affected packages next, then:

```sh
go build ./...
go test ./... -count=1 -timeout=180s
golangci-lint run ./...
make docs-check
make design-audit
make bench-gate
```

Run race suites for affected core/client/policy/capability/worktree and UI owner
paths. Cross-compile changed platform code; perform native Windows/macOS and
distinct-host SSH checks for the promised paths. Existing unresolved
core/shell gates remain unresolved unless actual evidence closes them.

Use `scripts/tuishot.sh` sequentially at readable 160×40 and constrained 80×24,
and the native Windows capture workflow for Windows acceptance. Drive real
composer/MCP entry points; inspect screenshots/rendered cells and result JSON.
Close every owned capture/helper/core process and verify cleanup. Never update
goldens or loosen benchmarks just to obtain green output. Record unavailable
platform gates as blocked, not passed.

## 10. Handoff and documentation completion

Each completed stage records changed owners, acceptance evidence, remaining
limitations and exact next task here, while publishing implementation truth to
the linked references and root `AGENTS.md`. Add durable control/execution
references only when their contracts land; do not label this plan current API.

At completion, keep the supported MCP feature matrix, control coverage inventory,
policy semantics, worktree baseline/integration/retention rules and measured
limits in current references, then delete this active plan and its index entry.
No old runtime paths, automatic data migrations or completed-plan archaeology.

### External design evidence (documentation, not product tests)

- [Claude Code worktrees](https://code.claude.com/docs/en/worktrees): first-class
  session/subagent isolation and retention; committed HEAD is not a dirty snapshot.
- [Claude permission modes](https://code.claude.com/docs/en/permission-modes):
  classifier-based Auto differs from bypass; useful precedent, not Crude policy.
- [Codex worktrees](https://learn.chatgpt.com/docs/environments/git-worktrees):
  route-selected provisioning, local-change options, handoff and recovery.
- [Codex subagents](https://learn.chatgpt.com/docs/agent-configuration/subagents)
  and [MCP](https://learn.chatgpt.com/docs/extend/mcp?surface=cli): agent threads
  do not imply universal worktree isolation or unrestricted capability installation.
- [Cursor worktrees](https://cursor.com/docs/configuration/worktrees): managed
  isolated tasks and per-candidate best-of-N, with separate apply workflows.

These sources were reviewed on 2026-10-11. They motivate supported execution
interfaces and clear authority, not claims of identical behavior or live-tested
competitor parity.
