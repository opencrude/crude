# Model Prompts And Project Guidance

> Status: Current reference (2026-10-02) - prompt ownership, bounded guidance intake, and runtime continuity.

This is the canonical development contract for model-facing instructions. Read
it before changing prompt assembly, instruction intake, compaction, or a
background model job. Root startup rules remain in [AGENTS.md](../../AGENTS.md).
This reference documents the system; it is not a runtime prompt asset or an
automatically ingested guidance file.

## Ownership

`prompt/` owns code-authored model instruction templates and their assembly.
It is a shared library, not another application layer or execution engine.
Callers import it directly; do not recreate forwarding APIs in `agent/`,
`session/`, or `memory/`.

| Owner | Responsibility |
|---|---|
| `prompt/build.go`, `contracts.go` | Root/child policy, role contracts, runtime-capability awareness, bounded command-output/temp policy, escaped profile and identity overlays |
| `prompt/compaction.go` | Four extraction stages, source attribution, manifests, checkpoint envelope |
| `prompt/guidance.go`, `guidance_compile.go` | Named-file discovery, hashes, verbatim representation, compiler/verifier wording, compiled ledger envelope |
| `prompt/memory.go` | No-tools memory extraction/consolidation policy and input envelopes |
| `prompt/permission.go` | Fixed no-tools review policy and narrowing restrictions |
| `prompt/runtime.go` | Continuity, turn context, plans, memory recall, delegation, wakes, interruptions and exact unavailable-attachment explanations |
| `prompt/recovery.go` | Typed, code-authored continuations for preserved unfinished work; never new human input |
| `prompt/execution.go` | Quiet context-only inventory and post-child disposition reminders; escaped execution facts |
| `agent/` | Model requests, admission budgets, guidance compilation/verification execution, tool-batch deferral, checkpoint validation |
| `session/`, `memory/`, `workspace/`, `fileio/` | Configuration and source selection, typed live facts, evidence validation, domain lifecycle and persistence |
| `provider/`, `tools/`, `codexio/` | Wire encoding/replay, exact offered schemas, native execution interfaces |
| `tui/`, `history/`, `sessiondb/` | User input/dialogs, prompt history, references and persistence; not behavioral prompt templates |

Root and child callers use `prompt.BuildRoot` and `prompt.BuildChild`. Background
jobs use their specialized `prompt/` builders; a memory curator or permission
reviewer must not inherit the whole coding-agent prompt or acquire its tools.
Domain packages may redact, bound and serialize source data before rendering;
validation and state mutation do not move into a prompt builder.

The stable environment projection includes the exact session-owned
`temporary_directory`, also exported to command processes as `CRUDE_TEMP_DIR`.
The canonical tool contract tells agents to redirect noisy transient output there
and inspect bounded ranges rather than injecting whole logs into model context.
`read_async_output` enforces 32 KiB default / 64 KiB maximum pages with raw byte
cursors; a fast synchronous command that completes with larger output returns a
bounded head/tail excerpt and retains the full capture under an async handle.
Background command completion is an owner notification (`command_outcome`
runtime event through the collaboration journal), not a polling contract; the
async/wait tool descriptions and canonical command rule say so.

`RuntimeCapabilities` gives every root and child a concise, model-neutral map of
Crude's collaboration, memory/history, capability and evidence surfaces. It does
not duplicate schemas or grant tools. Directed peer delivery is automatic;
live-running broadcast is rare and peer-note-only.
Capability guidance asks for concise name/keyword discovery and exact returned
identity activation; reading catalog files is not a durable installation. The
tool descriptions own installed-path resolution, workspace persistence, explicit
post-restart MCP activation and the separate exact-access review workflow. The
goal-only live acceptance is documented in
[capability workflows](../guides/capability-workflows.md).

`CollaborationWorkflow` supplies the practical decision path in every root/child
mode: direct work versus an existing suitable peer versus a bounded leaf,
exact request revisions/dispositions, no acknowledgement loops, exact artifact
reads, and report/control/lifecycle separation. Actual tool availability remains
authoritative; leaves cannot recursively delegate. Named child selection uses the
offered `agent_start.profile` field; the separate optional `nickname` names the
child itself and never selects a profile.
Runtime event envelopes include escaped exact evidence references and typed
dispositions. Summaries are notifications, not substitutes for reading evidence.
Enforcement and passive recovery live in the
[workspace collaboration runtime](workspace-collaboration.md), not prompt prose.

One shared workflow rule covers passive inspection in solo, orchestrator and child
prompts: resolve a name with `agent_list`, inspect the exact ID, and distinguish
presence, current run, latest attempt and durable recent work. Follow exact report
revisions with `artifact_read`, targeted events/threads with `board_read`, older
work with `search_history`, and changes with `execution.review`. Observation alone
never opens a session, sends a message, controls execution, waits or polls. Prefer
supported evidence to SQLite/log inspection and state missing evidence explicitly.

The child report contract describes `publish_report` as its required return
channel, outside optional task-tool allowlists. Launch validation rejects an
explicit denial; the prompt never overrides one. Runtime schema projection and
dispatch share that selection rule, and unavailable reporting is a configuration
failure rather than an endlessly repeated model reminder.

### Execution lifecycle and workspace-wide hygiene

`ExecutionHygieneContract` appears once, immediately after `instruction_priority`
in every root and child system prompt. It makes the parent primarily responsible
for each child's execution through final disposition, not merely launch and
report collection. Later collaboration, orchestrator and bounded-child rules
reference this shared contract rather than repeat it. Read final evidence and
inspect the authoritative terminal outcome, including failed or cancelled runs.
Discover the exact `execution.list`,
`execution.inspect`, `execution.review`, `execution.apply`, `execution.cleanup`,
and `execution.keep` schemas with `crude_commands`, and use `crude_control` for
the exact resource. Integrate useful changes through the document/Local History
owner, or explicitly discard unwanted work through reviewed cleanup, then clean
finished or failed worktrees. Explicit discard uses the review's `discard_digest`,
not its integration digest, so ignored files and HEAD changes are reviewed too.
Respect intentional human work and reasoned Keep; active users and recovery must
settle before destructive cleanup. Completion never automatically merges or
deletes a worktree. A blocked cleanup retains the work and reports why, rather
than improvising forced Git
removal. Hygiene warnings are configurable reminders, not launch blockers or an
automatic cleanup policy.

Parent responsibility does not make janitorial inspection or cleanup
descendant-only. Any capable root or child may review and clean another parent's
settled worktrees in the same workspace within its permitted tools and policy.
Inspect workspace inventory with `execution.list`/`execution.inspect` and exact
agent/run/recovery facts with `agent_inspect`. Contact the exact owner or parent
through `agent_send` when disposition is uncertain. Silence, age or offline
presence alone never proves abandonment; respect reasoned Keep and explicitly
settle live or recoverable work instead of blindly releasing its pins.

Discover `execution.reconcile` through `crude_commands` and use `crude_control`
with the exact execution ID to release forgotten pins only when the runtime
proves their runs terminal with no live commands or open recovery. It is not a
force-unpin operation. Review evidence, apply useful changes or archive an exact
reviewed discard, then clean settled worktrees. Reconcile only proven-stale pins
before cleanup; anything still live, recoverable, kept or uncertain stays
protected and receives an explicit blocker, not a guessed disposition.

For a deliberately unwanted parked task, `execution.settle` routes its exact
recovery ID/generation through the owning session's normal cancellation path.
Cold sessions are opened passively with `session.open` first. This lets a capable
workspace janitor settle another parent's neglected parked experiment without
manually editing databases or terminating unrelated live work. It preserves
history and does not itself remove the checkout. A blocker is an instruction to
inspect and resolve the named owner/reference with supported tools, not an excuse
to abandon the harness or ask the human to perform routine cleanup.

`BuildChild` includes one escaped `execution_context` with the supplied execution
ID, root and resolved isolation mode. Missing facts remain empty, not inferred
from directory names; a resolved shared mode with no ID means no separate
checkout, not an identity the child should fabricate. Every child category's
final report must name those facts, changes, focused verification, risks and a
recommended integrate/discard/Keep disposition with a reason for Keep. A blocked
child sends the parent an `agent_send` blocker and publishes its final
partial/blocked evidence instead of waiting for unrelated work. It does not
delete its active worktree or integrate automatically. Its parent remains
responsible for that execution's disposition; this does not forbid permitted
hygiene of other settled executions. Backend ownership and safety checks remain
authoritative; prompt text alone neither
enforces cleanup nor grants permission to discard work or to bypass a role's
read-only policy.

`ExecutionHygieneNotice` renders a context-only warning only when the session
supplies threshold reasons. The session owns thresholds, configured in
**Settings > Agents > Main Agent**, and delivery cadence; the renderer does not
decide whether an artifact is stale or block a launch. Counts, age, storage,
reasons and a sample of up to five exact execution IDs describe workspace-wide
inventory, not just the recipient's descendants. Reason/ID text is escaped so it
cannot become instruction markup. The notice references the early shared policy
without repeating it or requesting user-facing narration. It never requests
automatic merge/deletion, treats inventory age as authorization, or makes a
count threshold block launches.

`ChildExecutionDisposition` supplies one concise context-only reminder for each
child run outcome, separately from the visible collaboration event body. It
contains escaped exact ID/root/mode facts, references the shared contract, and
reminds the parent to inspect evidence and terminal outcome, review/integrate or
explicitly discard and clean when safe. With no ID and no mode it returns empty,
even if a root was supplied. Shared mode with an empty ID explicitly has no
separate worktree, and its root must not be deleted. Missing facts are never
guessed. The session owns outcome-only delivery; this pure helper does not
schedule repeated reminders, mutate the visible event or trigger automatic
cleanup. Kept, human-owned, live and recoverable work retain the same protection.

Exact tool schemas, descriptions, validation/denial messages and result payloads
stay with the executing interface. They are not competing system prompts. External
MCP prompt templates are source-attributed capability data, not shipped Crude
policy; see [capability workflows](../guides/capability-workflows.md).

There is no bundled vendor behavioral prompt tree and no agent/prompts.go
compatibility facade. If a future change genuinely needs shipped instruction
assets, their sole owner remains `prompt/`, not a second provider-local loader.
Names such as `tui/ui/textprompt.go`, `dirty_prompt.go`, or
`sessiondb/promptref.go` describe unrelated UI/reference concerns and remain in
their owning packages.

## Policy And Adapters

GPT, Claude, Gemini, routed and local models receive the same canonical Crude
behavioral policy for the same role and settings. Different roles and no-tools
jobs still have different contracts. A model change does not select a different
root personality or lifecycle policy.

User-editable Instructions are escaped, lower-priority overlays. They do not
replace canonical policy, weaken permission review, expand tool grants, or turn
runtime/peer data into human instructions. Permissions remain enforced by the
execution system independently of prompt text. See [Agents settings](settings.md#agents-catalog).

Adapt only a real interface difference: system-message placement, function schema,
native freeform patch format, multimodal encoding, or signed reasoning replay.
For example, Responses/Codex uses native freeform `apply_patch`, while Chat
Completions wraps the same patch in a function argument. This does not justify
forking the behavioral prompt. Current model-sensitive tool selection is
documented in [Codex tool adapters](codex-tools.md); it is separate from policy
selection. Tool schemas/descriptions must match what the request actually offers.

`read_file` is the native local multimodal read interface, not merely a text
reader. Its tool description owns the discoverability contract: line/range and
hashline reads, Markdown conversion for supported Office/EPUB/notebook formats,
directory listing, and image/PDF/audio/video attachments. Media admission uses
the active model's modality selection and the selected protocol's MIME support,
with size and filesystem safety checks; unsupported media fails explicitly.
Native image inspection does not require an additional viewer or discovered
integration. Do not duplicate this contract in a provider-specific behavioral
prompt or describe every binary document as automatically converted to text.

## Complete Guidance Intake

Before a normal project-task request, the runtime consumes the complete supported
set of applicable `SOUL.md`, `CLAUDE.md`, and `AGENTS.md` files, or fails closed.
Compilation and verification may themselves call a model before normal work;
they receive no tools. This is not a promise that the task model sees every raw
byte, that arbitrarily large files are supported, or that an LLM follows every
instruction perfectly.

Discovery starts at the configured workspace root and known active/owned paths.
For an in-workspace path it visits the root-to-target directory chain, not every
directory in the project or ancestors above the workspace root. External paths
use their nearest directory rather than importing an unrelated ancestor tree.
Filename matching is case-insensitive; multiple case variants in one directory
are ambiguous and fail closed. Guidance must be readable regular UTF-8 text;
symlink guidance and invalid text are rejected by the current loader.

Within each directory the order is `SOUL.md`, `CLAUDE.md`, then `AGENTS.md`:
persona, model guidance, repository policy. Deeper applicable scope wins broader
scope. Persona never grants permissions. Current human instructions, assignment
ownership and system/safety policy remain authoritative.

README, design, plan and reference files are not automatically ingested. Read
task-relevant documents or sections, including references explicitly required by
applicable guidance. A link is not a command to recursively read an entire doc
tree.

### Small And Large Sets

These terms refer to the estimated tokens of the complete rendered guidance,
not a fixed file size. `agent/project_guidance.go` calculates the target budget:

```text
available = context_window - base_system_tokens - output_reserve - safety_reserve
guidance_budget = min(65,536, max(4,096, context_window / 8), available / 2)
```

Admission fails when available capacity is below 2,048 tokens or the resulting
guidance budget is below 1,024. Context discovery and token estimates are
planning inputs, not proof of the provider's actual capacity.

A set whose verbatim XML representation fits is supplied with every file body,
scope, byte count and SHA-256. An oversized set is compiled instead:

1. Read each accepted source file completely and retain its identity.
2. Process complete UTF-8-safe chunks with path, scope, hash and byte-range data
   through the configured extraction connection, or the acting connection when
   no extraction connection is provided. Its own capacity bounds each request.
3. Maintain a complete replacement instruction ledger; reject an empty,
   truncated or over-budget compilation result rather than deleting rules.
4. Verify the final ledger against every source chunk. A correction restarts the
   sweep; at most three sweeps are attempted. Normal work requires one full pass
   with no correction.
5. Supply the task model with the ledger and source manifest, not the original
   oversized blob. On failure, do not continue with a preview or partial ledger.

For example, with a 128K context and sufficient response headroom, the guidance
budget is about 16K tokens. A rendered 6K-token guidance set is verbatim; an
80K-token set requires compilation. The actual budget depends on the base prompt
and output/safety reserves.

Verification is model-backed, not a mathematical proof of semantic completeness.
Hashes establish which bytes were processed, not that no meaning was lost.

Both representations end with an explicit intake-complete notice after the
source bodies or ledger. Consuming supplied content satisfies a source's
startup requirement to find/read itself. The model should avoid redundant
discovery and unchanged rereads, but tools remain unrestricted: discovering
additional scopes, requesting exact source/hashline evidence, and explicitly
auditing or editing guidance files are legitimate. A reread returns the normal
tool result; it does not republish another guidance snapshot or change precedence.

### Resource Limits And Cache

`prompt.MaxGuidanceBytes` is a **64 MiB aggregate source-content ceiling**. The
loader reads at most the remaining allowance plus one byte to detect overflow.
A 10 GB `AGENTS.md` is rejected; its first 64 MiB is never used as a truncated
substitute. Only accepted sets are completely consumed.

The ceiling bounds accepted source volume, not total compiler calls, elapsed
time or monetary cost. Near-ceiling inputs can still be expensive. Streaming
intake, durable compilation caches, or additional cost budgets are not implied
by the current implementation.

Compiled projections are cached in session memory, with at most eight entries
and clearing on work-directory change. Keys include exact source digest,
compiler version, target model/budget, and compiler provider/account/protocol/
endpoint/model identity. The cache is not persisted across process restarts and
is not evidence that files were delivered verbatim.

Snapshot format version and content digest identify the delivered guidance.
Directory and source order is deterministic regardless of active-path discovery
order; there are no per-request timestamps or counters in the guidance prefix.
Unchanged rereads leave its bytes unchanged. Model/protocol switches retain
canonical source identity and use the existing target/connection-specific
compilation cache; returning to the same model and budget reuses its cached
ledger. A different capacity may require another fitting representation, not
truncation. Provider cache residency and measured hit rates remain provider-owned.

### Newly Discovered Scope

If a planned tool batch first enters a directory with additional named guidance,
the runtime defers **all calls in that batch before effects**, supplies a
superseding complete guidance projection, and requires reassessment. It does not
execute the original batch automatically after reading the files.

A glob operand (for example a `grep_files` path of `workspace/lsp*.go`) scopes
guidance discovery to its literal directory prefix. Operand validity is each
tool's own per-call decision: one call's operand never fails or defers its
siblings.

Known scopes are rechecked before a tool batch and after its execution, including
creation, modification, deletion and native/freeform patch operands. Source
loading is transactional: failure retains the previous complete snapshot and
stops further task effects. Only an actual source-digest change publishes an
update. The update replaces the complete earlier guidance set, so deleting the
last file publishes an explicit empty snapshot rather than leaving stale rules.
The accepted system/conversation prefix remains unchanged within the turn;
updates append as hidden runtime context. Compaction uses one current snapshot,
not both the startup and current copies.

These boundary checks are not a filesystem transaction covering external
writers during a running tool call. A guidance mutation and other calls in the
same admitted batch use the pre-batch rules; the post-batch update governs the
next request. They do not recursively scan unrelated project directories.

For example, a first call targeting `backend/server.go` discovers
`backend/AGENTS.md`. No effectful call in that batch executes on that attempt;
pure reads (`read_file`, `grep_files`, `glob_files`, `list_directory`, memory,
history, plan, async-output, Board, artifact and agent inspection reads, and a
parallel wrapper made only of them) cannot violate rules and run now, sparing a
round trip. The next model request receives the new rules with those results
and can issue revised calls. Unreadable guidance defers the whole batch.

Current path discovery inspects recognized path/cwd arguments, parallel-call
parameters and parsed native patch operands. It is not an arbitrary shell-script
or remote-tool effects analyzer. New path-bearing tools must extend the actual
guard and its batch-deferral tests, not merely mention scope checking in a prompt.

## Compaction Continuity

### Current plan lifecycle

The shared root/child planning contract asks for plans only for three or more
substantive outcomes, review against latest human intent, milestone updates before
the next stage, and honest closure before the final answer. A supplied step array
is the exact desired ordered snapshot: obsolete steps are omitted, not accumulated
as cancelled entries. Immutable revision events retain prior snapshots.

Both generic and native Codex `update_plan` expose `replace=true`: supply a new
objective and complete nonempty steps (`steps` generically, `plan` natively) to
atomically supersede the goal within the same plan ID. Replacement resets step
provenance and records `plan_replaced`; validation, revision, storage or commit
failure leaves the old goal, history and activity view unchanged. Generic updates
require the current revision; native updates may omit it to use the current owner
revision but reject any supplied stale revision. Native plans expose explicit
active/paused/completed/cancelled lifecycle states and all generic step statuses.
An empty native snapshot cancels existing work by default; it never implies
success. Fully completed native step snapshots retain their declarative completion
behavior. No runtime heuristic infers a new user goal or successful work.

The plan store publishes full ordered immutable activity steps in addition to its
existing current/next projections. Presentation and resumed context consume the
committed current snapshot, never the historical cancelled-step graveyard.

Plan drift is measured, not counted by a fixed rule (`session/plan_discipline.go`).
While an active plan has open steps and stands still, the session counts model
steps since its last revision. Past a threshold (at least 8 steps, raised to
twice the plan's own observed pace per completed step), the next request carries
a context-only `RuntimePlanDrift` notice naming the open steps; an unheeded
notice doubles the threshold, and any plan revision resets it. Before a run
would finish with tool work done and open steps still on the plan, the agent's
`BeforeFinish` hook asks once per run (`RuntimePlanReconcile`) to update or close
the plan honestly. Neither notice infers progress or edits the plan; both live
in `prompt/runtime.go`.

Capability discovery is proactive, but applicable project guidance takes
precedence: a workflow, script or tool that guidance names for a job (for
example `scripts/tuishot.sh` for screenshots) is used, never replaced by a
searched-for capability (`RuntimeCapabilities`). When another agent of the
session makes a capability usable, the next request carries a context-only
`RuntimeCapabilityShared` notice (see
[capabilities](capabilities.md#sharing-within-a-session)). Tool-failure hints
are model instruction text too: `prompt/tool_hints.go` owns them and their
separator, and presentation strips them from rows.

`prompt/` owns extraction wording and the checkpoint envelope; it does not own
turn completion, plans, child execution or command lifetime. Session/file owners
publish typed facts for the final code-authored handoff, refreshed after
extraction. Compaction is not a new user request.

Known durable human-turn state decides whether the request is completed. When
unavailable, the current standalone path uses message-order evidence. An answered
request must not be appended as new-looking human input after the checkpoint;
it is carried verbatim in one code-authored, context-only
`<answered_human_requests>` history block so its requirements stay binding
without being re-answered. Pending input is retained within the recent-input
budget; a child's latest assignment may use all available continuation space.
In-run compaction rebuilds the continuation system message from the current
guidance render. The original transcript remains unchanged.

Current command handles/status/cursors, plan state and child identities override
stale model-generated descriptions. Snapshotting observes existing owners; it
does not consume output, restart work or fabricate a successful exit for a
running process. Earlier handoffs remain in the immutable accepted prefix;
new turns append current facts that explicitly supersede earlier snapshots.
Full admission, transaction and resume contracts live in
[adaptive context handling](adaptive-context.md).

Unnatural attempt failures use the [run recovery supervisor](run-recovery.md).
Recovery continuation wording belongs to `prompt/recovery.go`; pending input,
safe checkpoints, retry classification, lifecycle settlement and exact wake
fences remain domain-owned. A timer never grants permission or turns uncertain
tool effects into safe replay.

## Protocol Transitions

Changing a connection, model, wire protocol or generation setting is an ordinary
request boundary. The canonical local conversation remains intact, including
human/BTW/runtime origin, accepted assistant text and exact tool call/result
identities. Resume restores that same conversation passively. Never clear or
rewrite history, restart tools, or force the user back to the producing protocol
to make an adapter accept older messages.

The destination adapter owns the wire projection. Responses and Codex preserve
eligible native output items byte-for-byte; a canonical historical assistant
without such an item uses the documented string-content input-message form,
never user-only `input_text` blocks or invented output-message IDs. Chat,
Anthropic and Ollama expose freeform/custom tools through one shared required
`input` string object schema; their returned JSON goes through the same canonical
execution path. The synthetic parallel tool has one legal flat wire name on
every protocol. The transport client does not override adapter capabilities with
another protocol-specific tool veto. Ollama maps developer instructions to its
supported system role.

Protocol/endpoint/prefix-bound signed or encrypted replay data is filtered only
for the outgoing request, never from local history. The first request after a
meaningful change may need a fresh cache/continuation seed. With unchanged
configuration, subsequent projections retain stable system/tool definitions and
the earlier semantic wire prefix, and newly produced replay state remains warm.
Neither cache residency nor support for a particular model/modality can be
guaranteed against an incompatible endpoint. No schema-error string matching,
automatic protocol fallback, JSON body replacement or alternate transcript owner
is used to repair transitions.

`TestEveryWireProtocolReprojectsCanonicalHistoryWithoutMutation` and
`TestAllWireTransitionsResumeAndContinueTheToolLoop` cover every registered
protocol pair: decoding, resume, real file-tool rounds, protocol and compound
account/model/config changes, exact local-history preservation and stable
subsequent wire prefixes. The gated `TestLiveResponsesHistoricalSessionReplay`
acceptance test loads an explicitly selected local session read-only and verifies
its historical context against a configured endpoint without executing tools or
altering that session.

## Responses Delta Uploads

With Responses server storage requested and accepted by the endpoint, a completed
assistant message retains a typed server response reference in the session's
durable active context. The next
request uses `previous_response_id` and uploads only subsequent input when the
model, account, endpoint, request configuration, tool definitions, complete input
prefix and accepted output still match. Multipart hashes include actual media
bytes, not attachment labels. Inline images remain eligible for the documented
delta path. Ordinary and native custom-tool results retain their exact call IDs
and output kinds even when their calls are omitted from the upload. The full
local conversation remains authoritative and is never replaced with a server ID.

The first request, a changed prefix/configuration, compaction, or a partial or
unreferenced assistant response requires a complete upload. A model/account/
protocol switch starts a new chain; later compatible requests can use deltas.
Resume restores the reference passively and can continue it if the exact context
still matches. No provider call or tool restart occurs solely to validate an ID.

A structured error identifying `previous_response_id` allows one full-context
rebase before any model output, within the same overall deadline. Generic errors,
authorization failures, cancellation and accepted partial output never trigger
that recovery. A rejected reference lookup is separate from transient retry
attempts; a second reference error after rebasing is final.

**Inline image replay recovery** is a separate, configurable safeguard, enabled
by default on Responses account connections. Crude still attempts the
documented `previous_response_id` request first. Only an exact pre-output 400/422
that identifies an invalid Base64 `image_url`, while the inherited local data URL
passes Crude's own validation, permits one full-context retry. Successful
evidence records an exact endpoint/account/model/request-scope limitation in the
app model catalog for seven days; later matching prefixes use full uploads
without repeating the known-bad request. Different connections continue probing
the standard path. Disabling the setting bypasses both recovery and learned
routing, so the provider error is returned unchanged. Generic schema errors,
remote image URLs, malformed local images, cancellation and accepted output
never trigger this recovery.

Crude's stream decoder follows the official typed lifecycle: every
`response.created`, `response.in_progress` and terminal `response.completed`
contains a `Response` with one consistent `id`. The first valid lifecycle ID is
retained and all later IDs must match. This also tolerates a compatible gateway
that omits the redundant ID from its terminal event, without parsing text or
inventing an identity. A completed response that explicitly reports
`store:false`, or a stream that returns no ID, cannot become an HTTP continuation
anchor. When the request enabled continuation, the session log records the exact
decision and the TUI reports the endpoint limitation instead of silently
presenting full uploads as deltas. When request policy disabled continuation,
the completion logs `disabled` and neither retains a new response reference nor
emits an endpoint-limitation notice; an expected `store:false` response is not a
capability observation.

Tool execution identity is the exact provider call ID, never an output position.
Streamed calls and terminal snapshots bind to one accumulator even when a gateway
changes their output indices; repeated snapshots must produce one execution,
one display identity and one provider result. Distinct IDs with identical arguments
remain distinct operations. This is request-scoped identity handling, not a
semantic deduplication policy or a retry of accepted model output.

Storage off, endpoint-declined storage, ZDR, Codex's mandatory `store:false`,
automatic server truncation and caller-overridden wire context use full uploads.
HTTP delta continuation requires a Responses endpoint supporting stored response
IDs. It is not WebSocket
connection-local continuation for ZDR/Codex. Prompt-computation caching is a
separate provider feature: a smaller upload still has the full inherited input
for context accounting and billing. Provider cache residency/hit rates are not
guaranteed by either upload mode.

The Store setting is a request, not a capability claim. Crude cannot force an
OpenAI-compatible gateway to retain responses. The official alternatives are
HTTP stored-response chaining, a Conversations resource, or Responses WebSocket
mode (which can continue with `store:false`). Crude currently implements the
first. A gateway that declines storage and exposes neither of the latter APIs
cannot provide protocol-correct delta uploads; replaying an ID anyway would risk
missing context. The gated `TestLiveResponsesStoredContinuation` acceptance test
checks a configured endpoint's returned ID, actual second request body, upload
reduction and inherited model context.

## Development Checklist

1. Classify the change: policy template in `prompt/`, typed facts in the domain
   owner, tool contract in its interface, or wire adaptation in `provider/`.
2. Use the appropriate direct `prompt/` builder. Do not add forwarding helpers,
   provider-local prompt assets, or copies of canonical policy in callers.
3. Keep dynamic state, source attribution, escaping and instruction priority
   explicit. Pass facts as data; do not hide new policy in an arbitrary context
   string or make a no-tools job inherit coding-agent permissions.
4. Cover real request assembly, not only template substring checks. Relevant
   cases include root/child/profile selection, model/account changes, complete
   guidance byte coverage, bounds/cache isolation, failed intake with zero task
   effects, and newly scoped batch deferral.
5. For compaction changes, test completed versus pending requests, live owner
   refresh, cancellation, exact source preservation and passive resume. Do not
   substitute model-authored guesses for runtime state.
6. Run focused suites and the ownership check below, then the affected full
   packages, `go test ./...`, `go build ./...`, `golangci-lint run ./...`, relevant
   race/cross-platform checks, and `make docs-check` for implementation changes.
7. Keep this reference and `AGENTS.md` aligned with implemented guarantees.
   Report unsupported sizes, coverage gaps and interrupted gates explicitly;
   never loosen admission or claim perfect recall to make a check pass.

```sh
go test ./prompt -run TestProductionPromptTemplatesHaveOneOwner -count=1
```

`prompt/ownership_test.go` scans top-level production Go files in `agent`, `app`,
`session`, `workspace`, `memory`, and `codexio` for prompt/instruction constants
and known template text. It is a regression guard, not exhaustive data-flow
analysis. Extend its coverage for new packages/templates and manually review new
request paths; renaming a constant or embedding instructions in a local string
does not make a second owner acceptable.

## Placement rules

- Provider and model differences belong to the narrowest real adapter: wire
  encoding/replay in `provider/`, exact offered schemas in `tools/`, and native
  interfaces such as OpenAI/Codex freeform `apply_patch` in `codexio/`. A native
  tool does not justify replacing Crude's behavioral prompt.
- Guidance snapshots must not churn: no timestamps or counters in republished
  guidance, no raw JSON tool-name rewriting, and unchanged rereads never rewrite
  an accepted cache prefix.
