# Immediate Action Required: Runtime Integrity

> Status: Current actionable handoff — implementation is incomplete. Final source build passes and the app launches; runtime integration comes first, manual existing-database alignment comes last. Updated 2026-10-07.

## Start here

**Complete the runtime integrity implementation and validate it using isolated
current-schema fixtures first. Only after that work is finished, manually align
the existing databases with backups and preservation checks. Do not repair or
convert the user's existing workspace database immediately.**

The user corrected the earlier startup report: **the app starts**, but it reports
that the workspace database is unaligned and damaged. That is the observed error,
not independently verified physical corruption. The exact database failure has
not been diagnosed; do not conflate the workspace database with session databases
or assume one particular schema change explains it.

### Compact purpose of this session

The session began by investigating why an original prompt appeared in chat but
was missing from the model's context after Stop. Its purpose expanded into one
reliability correction: **stopping execution must never discard submitted input
or accepted work, and recoverable interruptions must not strand an agent.**

The fixes cover durable input/context ownership, Stop/queued steering/shutdown/
resume consistency, guidance-path validation, structural child failure and parent
claim repair, discoverable retained command output, graceful lifecycle-aware
waiting, and automatic safe partial-stream continuation across every provider
protocol. Tested components exist; their end-to-end integration is still unfinished.

This report is self-contained for a follow-up developer on another machine. It
does not require prior conversations, agent history, saved sessions, private
reports, temporary logs, credentials, or the original machine. The implementation
and regression sources in this repository are the reproducible evidence. Results
summarized below are historical observations; rerun them against the transferred
worktree and current environment before relying on them.

All current implementation workers finished before this handoff. Work is paused;
no background implementation is expected to finish it. No reported issue has been
cancelled. Do not restart completed effects or infer execution permission from
this document.

### Current outcome at a glance

| Area | Actual status |
|---|---|
| Source compilation | Final `go build ./...` passed after the last edits |
| App launch and existing workspace data | App launches; user reports an unaligned/damaged workspace DB error. Diagnose and manually align existing data only after implementation and fixture validation are finished |
| Original missing-prompt diagnosis | Confirmed: visible input was not locally admitted before Stop, and the next model request omitted it |
| Stop/context preservation | Partial implementation; full root/child/queued/teardown journey remains incomplete |
| Guidance-path poisoning | Focused literal-path/native/parallel and unreadable-guidance tests pass; complete blocked-child lifecycle proof missing |
| Partial-stream recovery | All five registered protocols pass the agent-level local root/child matrix; real session persistence integration remains incomplete |
| Large output and wait handling | Native and focused race evidence passes; whole-product acceptance not completed |
| Context publication/storage | Transactional revision API and indexed immutable identity implemented/tested; competing transcript writers remain |
| Parent/child claim remediation | Tested internal primitives exist; public self-narrowing route and structural supervisor classification are not integrated |
| Full repository acceptance | Last complete run failed in `session`, `workspace`, and `tui/viewmodel`; no final full-suite/lint/visual release bundle |

## 1. Required continuation order

1. Inspect the transferred worktree and preserve all local changes, including
   untracked source files. Continue the unresolved runtime implementation in
   section 5; do not divert into immediate existing-database repair.
2. Complete each owner-fenced user journey and its regressions using isolated
   current-schema homes/workspaces, local providers and synthetic data. Preserve
   the usable shell and actionable error behavior without touching existing user
   databases or credentials.
3. Run the integrated acceptance bundle in section 7. Settle the architecture and
   persisted contracts before preparing any manual data alignment.
4. **After implementation and validation finish**, capture and diagnose the exact
   workspace-database error. Separate workspace and session DB schemas, format
   mismatch and actual integrity damage. Do not infer physical corruption merely
   from the displayed wording.
5. Back up affected existing databases consistently, then perform the necessary
   manual alignment outside product code. Verify content, identities, provenance,
   history and passive resume as specified in section 6. If the data is unavailable
   on the follow-up machine, leave this final operation explicitly pending until
   it is available; do not invent a repair or substitute empty databases.

Do not silently delete/recreate data, choose another account, weaken network
policy or drop historical work. Workspace persistence lives under `workspace/`;
session-format checks live under `sessiondb/`. The session schema changes listed
in section 6 are real but are **not a confirmed diagnosis** of the reported
workspace DB error. Root `AGENTS.md` and relevant references remain applicable.

## 2. Non-negotiable behavior

### Stop is execution-only

- Human Stop and main-agent Stop/control terminate the exact execution loop,
  request, or attempt. They do **not** cancel submitted conversation content.
- A message shown as sending remains in its exact owner's conversation and future
  model context exactly once, even if provider preflight or upload never included
  it. Do not automatically return submitted input to the composer.
- Retain accepted text, plain reasoning and actual tool outcomes. Incomplete
  tool calls never execute or replay as valid calls.
- Preservation must not launch another provider request, tool, command or child.
  Later execution requires explicit intent. Passive resume replays zero effects.
- Newer unsubmitted drafts, references, cursor, selection, undo/history and scroll
  must not be consumed by late acknowledgements or lifecycle cleanup.

### Structural blockers are not transient recovery

- Invalid literal path operands such as `tui/ui/text*` or ownership `agent/**`
  produce ordinary actionable tool errors before guidance scope mutation.
  Wildcards belong in `glob_files.pattern`, with a concrete `base_dir`.
- Real unreadable applicable guidance still fails closed. Invalid-operands
  handling must not bypass complete guidance intake.
- A structural child configuration/guidance/ownership/scope blocker ends the
  **attempt**, preserves its task/context/work, and promptly informs its parent
  with exact identity, run/generation, incident, cause and remedy.
- Repair must be exact and auditable. No permission widening, implicit wake,
  guessed worker-settled boolean, stale-run control or automatic effect replay.

### Recovery is safe and protocol-neutral

- Accepted partial-stream interruption before the official terminal automatically
  waits on a real deadline and issues a semantic continuation, never the original
  stochastic request again.
- Cover every protocol returned by `provider.ProtocolList`: `openai`,
  `openai-responses`, `anthropic`, `ollama`, and `codex`.
- Schema/auth/quota/trust errors remain permanent/actionable; human Stop prevents
  automatic continuation. Cooldowns use shared `retry/`, capped at five minutes
  per wait, not a competing timer owner.

### Shared output, waiting and authority contracts

- Retained huge output must be announced before its excerpt, with exact handle,
  byte ranges/cursor/status and bounded retrieval guidance. Do not rerun commands
  or repeatedly status-check for content. Preserve captured stdout/stderr on Stop.
- Running, killed or unknown work is not exit zero or synthetic exit -1.
- `agent_wait` applies all supplied exact conditions conjunctively and respects
  lifecycle/addressed events. Ordinary expiry is a typed neutral no-match result,
  not raw deadline/SQL error. It must not mutate delivery receipts or read cursors.
- One owner per concern: local admission, canonical context publication,
  lifecycle supervision, claim mutation, output paging and UI projection.
  Do not synchronize duplicate authorities with fragile ad hoc code.
- Use standard library/SQLite/framework primitives, not handwritten foundational
  parsers. Runtime/TUI diagnostics use the existing logger, never direct process
  stdout/stderr tracing.

## 3. Confirmed original failure

The affected session showed the original request in chat, but the request existed
only in a cancelled recovery record with `input_admitted=0`. Its first model
request contained a later `continue` message, not the original request.

Early cancellation happened before durable local context admission. Chat display,
recovery ownership and provider context were separate authorities. The audit then
found related omissions in queued steering, Stop-before-loop, parked Stop, parent
child controls, late semantic events and immediate shutdown.

The earlier return-to-composer proposal was explicitly superseded. Do not restore
it as a fallback. No historical affected session was edited or resubmitted; the
follow-up must work from reproducible fixtures rather than depend on that data.

## 4. Implemented components and evidence

These are useful implemented changes, **not whole-system completion**.

### 4.1 Revision-owned context publication

`sessiondb.PublishContext` in `sessiondb/context_state.go` is the SQLite transaction
API for active context, transcript deltas, recovery, collaboration receipts and
compaction publication.

Important contracts:

- `ContextMutation` chooses append or replacement, not both.
- `TranscriptAppend` allows new visible transcript rows to commit alongside a
  compacted/current-context replacement.
- `ExpectedRevision` fences stale writers. Revision is independent of compaction
  generation and turn generation.
- `AgentID` is the context-row owner; `RuntimeAgentID` is the actor/recipient of
  related execution records. Root context owner is a session, not its orchestrator.
  Do not equate context owner with message producer.
- Immutable ID collisions, duplicate replacement IDs and incompatible metadata
  fail closed. Related rows roll back together on failure.

Relevant files: `sessiondb/context_state.go`, `messages.go`, `recovery.go`,
`collaboration.go`, `compaction.go`, `db.go`,
`sessiondb/context_publication_test.go`.

### 4.2 Indexed immutable identity

An earlier replacement validator decoded every archived generation. It has been
replaced with an indexed `immutable_message_identities` registry inside the
publication transaction, keyed by `internal_id` and verified against context-row
owner plus complete canonical typed message bytes. Standard `encoding/json` and
explicit nil-shape facts preserve distinctions hidden by omitted slice fields.
`idx_messages_internal_id` bounds transcript identity lookup.

Full message identity is separate from the lossy persisted transcript-column
projection; content/meta/turn/time/producer collisions still fail. Review this
boundary carefully when integrating callers, rather than silently discarding
fields to make equality pass.

Reported validation: full `go test ./sessiondb` and full
`go test -race ./sessiondb` passed after this correction. Earlier independent
full storage race validation also passed before the final change.

Windows benchmark, `-benchtime=50x -count=3`:

| Path | No unrelated archives | 2,000 unrelated archives |
|---|---:|---:|
| Indexed real replacement | ~1.12–1.30 ms/op | ~1.48–1.81 ms/op |
| Old archive-scan reproduction | ~0.195–0.383 ms/op | ~5.26–5.58 ms/op |

Reproduce with `BenchmarkPublishContextReplaceUnrelatedArchiveHistory` and
`BenchmarkLegacyReplacementArchiveScan` in the publication test file. These
measurements demonstrate removed history-wide JSON scanning, not a complete
performance gate or zero-cost publication.

### 4.3 Root admission and presentation

`PrepareInputAdmission` reserves an exact owner/generation without I/O.
`CommitInputAdmission` performs off-UI serialized/idempotent local publication
before dispatch. Stop can fence the prepared generation; Commit still preserves
the message and returns `preservation_pending` rather than executing it.

Workspace prepare/commit adapters and the TUI receipt path deduplicate by input ID,
clear only the exact submitted draft revision, and leave newer drafts alone.
Per-owner draft persistence records text, reference IDs/spans, cursor, selection,
scroll and revision. Typed runtime event identity fences owner/run/epoch/
generation/sequence. Late real tool results replace cancelled placeholders
without reactivating execution.

Relevant files: `session/admission.go`, `context_publication.go`,
`runtime_events.go`, `types/runtime_event.go`, `workspace/bootstrap.go`,
`agent_state.go`, `conversation.go`, `tui/viewmodel/agent_events.go`,
`composer_draft.go`, `tui/input/`, `tui/tui.go`.

`agent.SupervisedRunResult` returns terminal archive/active/state/failure/incident.
Session finalization must consume that snapshot, not a stale second
`Agent.ActiveContext()` owner.

### 4.4 Fresh-send and initial peer corrections

Ordinary pre-admitted inputs carried a recovery checkpoint and were incorrectly
treated as explicit Continue. `turnRequest.retry` now distinguishes explicit
recovery submission. Initial peer delivery uses its immutable journal identity
and timestamp; receipt publication after prior event admission is receipt-only,
not a second metadata/delta authority. Recovery retains existing canonical bytes
over lossy provider projections. Display-only text/labels live in transcript meta.

The following real local HTTP/provider/completion fixtures passed after those
corrections, before the final combined storage changes:

- `TestPromptWithFreshHistoryReachesProvider`.
- `TestWorkspacePeerMessageWakesExactIdleAgentWithoutPolling`.
- `TestPastedTextSendsCompleteContentButKeepsChatCompact`, through actual Bubble
  Tea command/admission handling.

`TestWorkspaceHumanSteeringTargetsExactActiveRun` still failed. See blocker 5.1.

### 4.5 Guidance operands

`agent/project_guidance.go` validates literal tool paths before discovering new
guidance. Native patch paths and parallel child parameters share the preflight.
`session/subagent.go` rejects invalid ownership before allocation/claim/launch.
Brackets remain valid ordinary path characters; `*` and `?` do not.

Focused root/native/parallel validation and a real unreadable-guidance fixture
passed. The unreadable fixture uses an `AGENTS.md` directory and asserts zero
provider/tool effects. Full blocked-child/control integration remains required.

### 4.6 Accepted partial-stream recovery

`provider.StreamInterruptionError` distinguishes missing official terminal and
narrow typed protocol disconnect evidence. Client-level retry stops after accepted
output; no partial response becomes a continuation reference. Responses classifies
specific structured disconnects, not generic invalid-request prose.

Supervised `Agent.Run` now bypasses its inner partial-output retry branch through
`!a.preserveReportState`. The existing Supervisor receives the canonical partial
snapshot, persists owned Recovering status with nonzero `RetryAt`, waits, then
appends semantic continuation. Do not use this report to assume session SQLite
callback integration is correct.

`agent/supervised_protocol_matrix_test.go` dynamically requires fixtures for all
registered protocols. Actual SSE/NDJSON incomplete-tool frames test root and
child-shaped execution, two requests, exact-once original/accepted prefix, zero
incomplete tool effects, no partial replay reference, and a real owned deadline.
Permanent schema/auth/quota negatives issue one request and have no retry deadline;
Stop during cooldown makes no second request.

Reported full `go test ./provider`, provider race, full `go test ./agent`, and
focused agent protocol race tests passed at their respective component milestones.
Live external-provider acceptance was not performed.

### 4.7 Output and wait edge fixes

Files: `fileio/async_exec.go`, `interactive.go`, `session/command_output.go`,
`codex_exec.go`, `collaboration_wait.go`, and their contract tests.

- Shared descriptor-first output states retained bytes/ranges/omission/handle and
  retrieval guidance before a large excerpt.
- Native cancellation kills and joins the exact owned command tree and returns
  captured partial stdout/stderr with the real context error. The Codex consumer
  preserves the result. Killed exit is unavailable, not synthetic -1.
- Output pages keep raw half-open ranges/cursors. An independently invalid UTF-8
  interval is standard base64 with explicit `output_encoding`, not replacement
  characters or cursor adjustment.
- Wait starts one deadline before the initial Store observation and uses it for
  every read. Ordinary expiry returns `timeout_no_match`; caller interruption is
  separate. Final refresh has a bounded 100ms grace and labels stale/no observations.
- Wait readiness is conjunctive; terminal-with-unmet-report is
  `terminal_without_match`. Addressed events/lifecycle are observed without
  receipt admission or read-cursor mutation.

Full native `fileio`, focused native wait/Codex/formatter and focused race tests
passed. Full `fileio` and current wait tests were independently rerun. `fileio`
test binaries cross-compiled for Linux amd64 and Darwin arm64. A process built
before these changes still emitted raw wait deadline/transaction errors; source
tests are not proof that an older running binary was repaired.

### 4.8 Child terminal publication and claims

Child terminal active context, new transcript deltas and recovery now commit in
one `PublishContext`. Stable existing IDs, timestamps, metadata and assignment
roles are not restamped on Continue. The committed checkpoint revision is retained.

Stopped-child `UpdateClaims` uses exact interrupted run/epoch/version and all
configuration-claim versions, original resource ceiling, transactional audit and
atomic local `FileOwnership.ReplaceClaims`. Audit identity derives from the
immutable repair preimage. Resumed writes cannot silently regrow repaired scope.

Active-self `Store.NarrowActiveClaims` and internal `executeClaimsTool` also exist:
strict subset of observed active run-bound claim ceiling, exact run/epoch/version/
preimages, optional explicit relinquishment, same run binding, audit, no wake/grant.
`CommitReplaceClaims` holds local ownership publication across the durable commit.

`tools.StructuralFailureError` now supplies typed ownership scope/conflict facts
and `agent/tool_exec.go` propagates them as `execResult.failure`, retaining ordinary
tool evidence. **Supervisor classification and public self-narrowing routing are
still missing.** Internal primitives passing is not a usable completed feature.

## 5. Concrete unresolved blockers

### 5.1 Active steering checkpoint stall

`TestWorkspaceHumanSteeringTargetsExactActiveRun` begins its first request and
durably journals steering, but subsequent safe-boundary checkpoint publication
enters `persistRecoveryBoundary -> recoveryRunState.publish -> Supervisor.Await`
instead of completing the continuation.

Inspect `session/run.go`, `recovery.go`, `collaboration.go`, and runtime-message
admission. The shared context revision advances between requests; checkpoint
bytes and exact acknowledged source identity are not consistently reconciled.
**Never fetch latest revision and label an older checkpoint with it.**

Acceptance: provider request two contains original/steering/accepted prefix once,
completion settles, immediate/durable/resumed content agrees, and Stop in the
same window starts no replacement request or loses input.

### 5.2 Prepared ContextParts and selected-reference loss

The `locallyAdmitted` branch still bypasses ordinary request context assembly,
including `UserInput.ContextParts`, plan/recall/runtime enrichment. Selected
MCP/tool/context data can therefore be absent even when the user row is present.

`buildUserMessageWithDisplay` still falls back to plain text on reference load
failure, skips missing/inactive IDs and unreadable media/files, and lacks backend
`KindMCPTool`/`KindMCPContext` handling.

Authoritative existing selection representation is `sessiondb.Reference.AgentText()`
in `sessiondb/promptref.go`: exact tool/source identity and captured content;
selection is not a permission grant, external content is source data, not instructions.

Acceptance: locally preserve original input plus exact reference/selection
identity/preimage before acknowledging; valid selected payload reaches the real
request once; missing/unreadable/unsupported preparation blocks provider/tools
with actionable state instead of executing a weakened prompt. Do not create a
second text-expansion authority or discard the submitted input on preparation error.

### 5.3 Finalization rollback race

`finalizeTurn` snapshots and mutates session arrays under `msgsMu`, releases the
mutex to publish, then on CAS/storage failure restores the old arrays/revision.
A concurrent admission can commit a newer source in that interval; unconditional
rollback can erase it from the live in-memory projection while SQLite stays newer.

Serialize authoritative publication or fence rollback against the captured memory
source revision. Add a deterministic barrier test: finalization at N, a real
admission to N+1, stale finalization failure, both durable and live states still
retain the new input once. Also test storage failure without a concurrent writer.

### 5.4 Child parked Stop can still publish stale bytes

`CancelRecoveryFor` child branch separately persists whole transcript, then loads
latest context revision and replaces from an older stopped checkpoint. Route it
through the atomic child terminal publication/current-authority seam instead.

Acceptance: park child, commit newer fact, Stop exact incident, passive resume;
all original and newer facts survive once and zero provider/tool work runs.

### 5.5 Closing sessions and durable source fence

`PrepareInputAdmission` does not yet explicitly reject session-level closing.
Close snapshots pending intents and commits them before closing orchestrators;
verify that those commits are passive preservation and cannot enqueue/start work.

Root-only prepared-intent tests do not prove child submission or journal/mailbox
high-water coverage across pending/reserved/admitting/received records. Stop must
commit all owned pre-boundary inputs once and must not reclassify old buffered
evidence as a fresh wake. Test shutdown and crash/resume boundaries, not only a
volatile channel or one in-memory intent.

### 5.6 Competing whole-transcript writers

`session/messages.go` root `flushWriteBufSync` and `session/subagent.go`
`persistSubAgentTranscript` retain `ReplaceMessagesWithMeta` delete/reinsert
publication outside `PublishContext`. They lack an exact revision/context-owner
contract and can introduce IDs not registered in the new identity authority.

Remove or route these production writers through the current publication owner.
Do not guess an actor, add an implicit compatibility reader, or retain a second
transcript writer just to make existing fixtures compile. Preserve immutable
metadata, timestamps, content and accepted prefixes.

### 5.7 Structural classification and public claim management missing

- `agent/run.go` treats propagated `execResult.failure` as uncertain/panic. It
  lacks ownership-specific `errors.As` classification and nonautomatic recovery
  kind. Integrate it before generic panic handling; child OnAttention must park
  the attempt, root repair policy must remain explicit.
- `session/collaboration.go` does not route `agent_narrow_claims` to the internal
  handler in `session/claims_tool.go`. No public schema/effect entry is offered;
  it was removed rather than advertise an unrouteable tool.
- Current tests for parent repair compose primitives or manually create a run.
  There is no real ownership-block -> parent incident -> self narrow -> stopped
  child repair -> `RetryRecoveryFor` -> `ResumeRecovery` -> provider-requested
  write integration proof.

Required negatives: stale/foreign identities, widening, missing claim versions,
fractional/out-of-range numeric versions, mutation races, repeated repair and
intentional empty relinquishment. Do not silently truncate numbers. Validate the
actual file-effect lifetime under claim mutation, not only mutex-held publication.
Require zero pre-repair effects, no repair-triggered wake, one post-Continue effect,
retained task/prefix, new run identity and narrow scope enforcement.

### 5.8 Current-contract fixture failures and incomplete gates

`TestCollaborationRecoversSettledChildAdmissionWithoutRelaunch` failed before its
journey because its fixture used replacement Generation 1 with empty Reason.
Fatal also preceded child DB closure. Align fixtures to the valid current contract
and use immediate cleanup; this is not evidence that passive recovery is fixed.

`TestCollaborationWaitRequiresAllExactConditions` still expected
`caller_interrupted` for a terminal run with unmet artifact revision. Intended
behavior is `terminal_without_match`, retaining the exact unmet condition.
Fix only the obsolete disposition expectation, not conjunction/identity checks.

The final combined repository test suite was not rerun. Last complete run had
78 passing package groups and failures in `session`, `workspace`, `tui/viewmodel`.
Failures included account pin/resume, recovery/admission, prompt history (one
panic), compaction and model/protocol transitions, besides the named real sends.
Do not infer green from shell wrappers that echo success/failure and exit zero.

## 6. Persisted format and preservation requirements

Current task changes include:

```text
agent_context_state.revision INTEGER NOT NULL
agent_recoveries.context_revision INTEGER NOT NULL DEFAULT 0
immutable_message_identities(
    internal_id TEXT PRIMARY KEY,
    session_id TEXT NOT NULL,
    agent_id TEXT NOT NULL,
    message_json BLOB NOT NULL,
    created_at INTEGER NOT NULL
)
idx_messages_internal_id
```

Manual existing-data alignment is the final operation, **after the implementation
and isolated integration acceptance are finished**, not the immediate task.
Inspect the actual workspace/session schema owners and identity encoder before
aligning data. No
automatic upgrade/backfill or live data conversion was performed. Existing
old-schema session opening deliberately fails closed.

If aligning known data is needed, use a consistent offline backup with SQLite
WAL handling; one-off tooling stays outside product code. Preserve IDs, exact
content, timestamps/meta, origin/visibility, context owner versus runtime actor,
account/model pins, receipts, checkpoints, history and reports. Detect collisions
without inventing past evidence/revisions. Verify current schema, row counts,
canonical content identity, provenance and passive resume with zero effects.

Do not initialize an empty identity registry over old history and claim that
immutability is preserved. Do not automatically delete/recreate a failing DB,
replay old tools, mutate the original missing-prompt session or restore code by
discarding unrelated work.

## 7. Reproducible verification bundle

Read actual test names and outputs so a regex matching zero tests is not counted
as proof. Use repository paths below; no prior temporary overlay is required.

### Focused local protocol proof

```sh
go test ./provider -count=1
go test ./agent -count=1
go test -race ./agent -run 'TestRegisteredProtocols(SupervisedPartialToolRecovery|PermanentFailuresParkWithoutRetry)$|TestSupervisedPartialContinuationStopsDuringCooldown$' -count=1
```

### Real send/steering entry points

```sh
go test ./workspace -run 'Test(PromptWithFreshHistoryReachesProvider|WorkspacePeerMessageWakesExactIdleAgentWithoutPolling|WorkspaceHumanSteeringTargetsExactActiveRun)$' -count=1 -timeout=60s
go test ./tui/viewmodel -run 'TestPastedTextSendsCompleteContentButKeepsChatCompact$' -count=1 -timeout=60s
```

### Storage/claim/admission/wait checks

```sh
go test ./sessiondb -count=1
go test -race ./sessiondb -count=1
go test ./sessiondb -run '^$' -bench 'Benchmark(PublishContextReplaceUnrelatedArchiveHistory|LegacyReplacementArchiveScan)$' -benchmem -benchtime=50x -count=3
go test ./collaboration -run 'Test(UpdateClaims|NarrowActiveClaims)' -count=1
go test ./agent -run 'TestFileOwnership' -count=1
go test ./session -run 'Test(CommitInputAdmission|ReserveTurn|ChildTerminalPublication|ParentClaimRepair|ActiveActorCanNarrowOwnClaims|CollaborationWait)' -count=1 -timeout=60s
go test ./tui/viewmodel -run 'Test(PreparedInputStop|StoppedSteering|CancelledPendingSteering)' -count=1
go test ./fileio -count=1
```

Guidance regressions live in `agent/project_guidance_test.go`:
`TestGuidanceCallPathsRejectsWildcardLiteralWithoutPoisoningSet`,
`TestRunWildcardLiteralPathIsOrdinaryToolErrorAndContinues`,
`TestGuidanceCallPathsRejectsNativeAndParallelWildcardOperands`, and
`TestUnreadableGuidanceStopsBeforeProviderAndTools`.

Add missing real-journey regressions from section 5. Tests must fail for the actual
omission/stale-write/effect-replay reason, not merely confirm a chosen helper.
Assert immediate rendered chat, durable transcript/current context, next actual
provider request, exact owner and negative effect counts.

### Complete integration acceptance, before manual existing-data alignment

```sh
go build ./...
go test -json ./... -count=1 -timeout=180s
go test -race ./agent ./session ./sessiondb ./workspace ./collaboration ./provider ./fileio
golangci-lint run ./...
bash scripts/docs_check.sh
bash scripts/tui_design_audit.sh
bash scripts/tui_bench_gate.sh
```

Adapt shell quoting to the actual machine. Use its session-owned temporary
directory for noisy logs. Cross-compile/platform-test changed command and lifecycle
code. Consult root guidance for separately documented pre-existing shelltree
golden/glyph and warm-switch allocation failures; do not update goldens or loosen
ceilings to hide a failure.

Use native UI capture only through repository workflows: `scripts/tuishot.ps1`
on Windows, `scripts/tuishot.sh` on Linux. Read
`docs/guides/windows-visual-capture.md` where applicable. Use isolated current
data/local provider, readable and constrained dimensions, immediate/settled/
resumed comparison, one capture at a time, exact owned window/process only,
and close every capture. None of that final acceptance was performed here.

## 8. Transfer and stopping state

The code is broadly uncommitted. **A tracked `git diff` alone is insufficient**:
it omits new source and tests. Transfer the complete worktree safely, or explicitly
include every untracked file. Preserve unrelated user work; no destructive Git
rollback is allowed. No release installation or live data alignment was performed.

Important new files include:

```text
agent/supervised_protocol_matrix_test.go
collaboration/claims_update.go
collaboration/claims_update_test.go
fileio/interactive_output_contract_test.go
session/admission.go
session/admission_test.go
session/child_runtime_integrity_test.go
session/claims_tool.go
session/codex_output_contract_test.go
session/collaboration_wait.go
session/collaboration_wait_test.go
session/command_output.go
session/command_output_test.go
session/context_publication.go
session/parent_claim_repair_integration_test.go
session/root_context_recovery_integration_test.go
session/runtime_events.go
session/runtime_events_test.go
sessiondb/context_publication_test.go
types/runtime_event.go
IMMEDIATE-ACTION-REQUIRED.md
docs/plans/input-admission-integrity-audit.md
```

Re-run `git status --short` on the new machine; this inventory is not a substitute
for preserving the whole actual tree. Supporting docs in this repository include
the detailed acceptance ledger, but this report contains all necessary current
requirements, outcomes, blockers, source locations and reproduction commands.
Do not depend on private artifacts or saved sessions to reconstruct the task.

Final source build passed after all implementation workers settled. Documentation
and whitespace verification apply to this report; there is no final full-release
acceptance claim. Continue with one complete owner-fenced runtime user journey
at a time, retaining every unresolved requirement above. Finish implementation
and fixture-based validation before the final backed-up manual database alignment.
