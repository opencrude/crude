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
| `prompt/runtime.go` | Continuity, turn context, plans, memory recall, delegation, wakes, and interruptions |
| `prompt/recovery.go` | Typed, code-authored continuations for preserved unfinished work; never new human input |
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

`RuntimeCapabilities` gives every root and child a concise, model-neutral map of
Crude's collaboration, memory/history, capability and evidence surfaces. It does
not duplicate schemas or grant tools. Directed peer delivery is automatic;
live-running broadcast is rare and peer-note-only.

`CollaborationWorkflow` supplies the practical decision path in every root/child
mode: direct work versus an existing suitable peer versus a bounded leaf,
exact request revisions/dispositions, no acknowledgement loops, exact artifact
reads, and report/control/lifecycle separation. Actual tool availability remains
authoritative; leaves cannot recursively delegate. Named child selection uses the
offered `agent_start.profile` field, not a nonexistent nickname argument.
Runtime event envelopes include escaped exact evidence references and typed
dispositions. Summaries are notifications, not substitutes for reading evidence.
Enforcement and passive recovery live in the
[workspace collaboration runtime](workspace-collaboration.md), not prompt prose.

Exact tool schemas, descriptions, validation/denial messages and result payloads
stay with the executing interface. They are not competing system prompts. External
MCP prompt templates are source-attributed capability data, not shipped Crude
policy; see [capability workflows](../guides/capability-workflows.md).

There is no bundled vendor behavioral prompt tree and no `agent/prompts.go`
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
`backend/AGENTS.md`. Neither that call nor its siblings execute on that attempt.
The next model request receives the new rules and can issue revised calls.

Current path discovery inspects recognized path/cwd arguments, parallel-call
parameters and parsed native patch operands. It is not an arbitrary shell-script
or remote-tool effects analyzer. New path-bearing tools must extend the actual
guard and its batch-deferral tests, not merely mention scope checking in a prompt.

## Compaction Continuity

`prompt/` owns extraction wording and the checkpoint envelope; it does not own
turn completion, plans, child execution or command lifetime. Session/file owners
publish typed facts for the final code-authored handoff, refreshed after
extraction. Compaction is not a new user request.

Known durable human-turn state decides whether the request is completed. When
unavailable, the current standalone path uses message-order evidence. An answered
request must not be appended as new-looking human input after the checkpoint;
pending input and child assignments are retained within their budget. The
original transcript remains unchanged.

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
by default on Responses templates and account overrides. Crude still attempts the
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
anchor. The session log records the exact decision and the TUI reports the
endpoint limitation instead of silently presenting full uploads as deltas.

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
