# Adaptive context handling

> Status: Current - connection-scoped capacity, reasoning-aware extraction, transactional recovery, and owner-authored continuity.

Prompt ownership, guidance budgets and development rules are defined in
[model prompts and project guidance](prompt-architecture.md). This reference owns
context admission, extraction and recovery, not a second set of prompt templates.

## Failure and ownership

The reported manual-compaction failure sent about 498K estimated input tokens to
an extraction connection using a 256K window. The old implementation sent the
entire history to each of four stages, then retried the same impossible input.
Automatic compaction used only the main model's window, and explicit Aeron
category capacity was not bound to its connection.

Provider request usage remains the ordinary-context authority. The per-agent
meter adds reported completion and semantic local deltas, including changes to
tool schemas during the turn. Normal request admission consumes that projection
instead of contradicting it with a fresh full-history estimate. A provider or
account switch cannot reuse another connection's persisted usage baseline.

Each explicitly configured context window is scoped to its category connection.
Unconfigured connections use discovered metadata or the existing default; that
default is a planning estimate, not proof of actual provider capacity.

## Layered policy

1. Admit BTW and runtime child-result notices before measuring pressure. During
   an ordinary provider upload, a newly queued BTW note cancels and rebuilds
   only that request while no model output has started and the uploaded body is
   at most 90 percent complete. The agent drains every ready BTW note, reruns
   context-pressure admission, and uploads one replacement request; this internal
   rebase is not a user cancellation or a successful provider request. Once the
   upload is past 90 percent, accepted, or streaming output, notes retain the
   normal next-request behavior. A queued note is rechecked if the transport
   starts another upload attempt with the old body.
2. Bound the configured percentage by the main model's normal response and
   safety reserves. Extraction plans each request independently against the
   resolved Aeron connection's capacity; a small extraction model uses smaller
   source chunks instead of forcing early compaction of a larger main model.
3. Persist the exact source before any extraction. A fitting source shares its
   immutable prefix across the four extraction requests. Oversized sources use
   chronological fragments;
   individual large messages and tool arguments/results are split on Unicode
   code-point boundaries without deleting their text. Every fragment includes
   source role, origin, and tool-result identity as data, not instructions.
4. Extract each of the four stages from all source chunks, then merge that
   stage's fragments. The selected model's configured reasoning remains active
   on every extraction request. The response allowance follows its model output
   capability and each request's available context, including hidden reasoning;
   it is independent of checkpoint text length. On truncation, the extractor
   reduces and reprocesses the complete source in smaller regions so the next
   request has more response room. It never repeats an identical truncated
   request or installs partial text. Recursive reduction is bounded and must
   shrink its input.
   A typed invalid-request rejection naming the exact sent `max_tokens`,
   `max_completion_tokens`, or `max_output_tokens` and an allowed upper bound
   instead tunes the output allowance immediately without changing the source,
   reasoning, or completed stages. That bound applies to all following stages.
   The app-owned `model-catalog.db` durably stores the lowest confirmed ceiling
   under the Aeron agent profile, exact provider endpoint/protocol, account,
   and model. New sessions, workspaces, and process restarts start below the
   previously rejected value; other agent profiles or connections do not share
   it. Learned limits expire after seven days so a changed provider cap can
   be probed again. This is separate from the user-configured output ceiling,
   which remains an upper bound.
5. Tighten extraction estimates when reported usage exceeds the estimate. A
   canonical context-limit rejection cuts the input budget to at most two-thirds
   of the rejected size and replans; it never retries that impossible input.
6. Size the final XML checkpoint against the actual continuation model's
   remaining context after its system prompt and tool schemas. There is no
   independent 16K checkpoint ceiling or fixed per-stage quota. A complete
   extraction that exceeds the remaining space is refined with the same model;
   verbatim recent human input uses whatever space remains. Oversized individual
   inputs are represented in the checkpoint rather than retained unboundedly
   alongside it; the original source remains archived. Reject empty,
   tool-calling, control-invalid, truncated, nonshrinking, or over-budget results.
7. Commit the complete checkpoint, previous generation, and terminal lifecycle
   atomically. Only then consume notices and BTW admission. Cancellation or any
   pre-commit failure retains the source and installs no partial checkpoint.
8. A normal provider request rejected for canonical context overflow triggers
   bounded compaction and retry only when no output was accepted. Recovery
   targets at most half the projected input, with at most three recoveries before
   a successful normal response. Authorization, malformed schema, cancellation,
   and generic payload-size errors are not misclassified as context overflow.
9. The installed context ends with a fresh code-authored continuity handoff, not
   a retained historical prompt. Known durable human-turn state takes precedence
   when deciding whether the request was answered; the standalone path uses
   message-order evidence only when that state is unavailable. An answered
   request is represented by the checkpoint rather than appended as new-looking
   user input. Pending human input and child assignment remain verbatim when
   they fit.
10. Runtime owners are sampled again after extraction. The handoff carries the
    current plan and session selection plus exact async handle/command/cwd/PID,
    status, output length and observed cursor, and child identity/status. A
    running command has no exit code. Snapshotting never advances output,
    signals or restarts work; model extraction cannot override fresher owner data.

Extraction contracts, stage wording, attributed source-fragment envelopes and
checkpoint assembly live in `prompt/compaction.go`; `agent/` retains admission,
chunk/retry execution and validation. `prompt/runtime.go` renders the continuity
handoff from session/file-owner facts, not model-authored estimates of live work.

Across normal turns, earlier runtime handoffs remain in the accepted prefix and
fresh owner facts append with explicit supersession. This preserves prompt-cache
and [Responses delta-upload](prompt-architecture.md#responses-delta-uploads)
eligibility without making an old snapshot current. Compaction still replaces
the active context transactionally and starts a fresh transport chain. Context
admission and reported usage always use full inherited context, not delta bytes.

`ContextCompactionPercent: 0` still disables automatic pressure and overflow
recovery; manual `/compact` uses the same extractor. Four lifecycle stages stay
four stages; chunk progress is additional truthful detail within a stage.
The chat boundary and the agent activity strip report the same operation:
while manual compaction is active, its progress replaces the paused turn's
plan activity; after success the strip says the context is ready to continue.
The transcript remains archived, while the checkpoint replaces the active
context. On resume, the durable boundary supplies the settled status until a
new turn starts. Automatic compaction resumes its interrupted turn instead of
displaying a manual completion prompt.
The active plan lives in the session's plan store, outside the compacted
conversation. Each new human turn receives its current revision and complete
step snapshot as hidden runtime context; the stable system planning contract
asks the model to compare it with the new request and revise or close stale
work. The runtime snapshot is data and is never attributed as a user message.

The September 29 failure sent roughly 598K input tokens with a 3,936-token
generation cap derived from a 16K checkpoint split. The DeepSeek extraction
model used high reasoning, consuming much of that response allowance before
the work stage returned `length`. The former workaround disabled thinking;
that quality regression has been removed. Request capacity and checkpoint size
are now calculated separately, preserving the user's selected reasoning mode.

## Harness research and decisions

Primary sources were reviewed at pinned commits rather than assuming an older
implementation still matched the product:

- **Codex**, `1983c48fd186614529e4fad418ac38a1ae00c6b6`:
  [context manager](https://github.com/openai/codex/blob/1983c48fd186614529e4fad418ac38a1ae00c6b6/codex-rs/core/src/context_manager/history.rs)
  uses measured usage plus a local tail; live tool-output truncation is separate
  from full rollout persistence.
  [Local compaction](https://github.com/openai/codex/blob/1983c48fd186614529e4fad418ac38a1ae00c6b6/codex-rs/core/src/compact.rs)
  removes oldest paired history on overflow. Crude adopts accounting, reserves,
  and archival separation, but not silent source deletion during extraction.
- **Kilo Code**, `e9948b6000697757ceea194403750cd80effe70c`:
  the current implementation is the OpenCode-derived runtime, not the older Roo
  extension. [Overflow preflight](https://github.com/Kilo-Org/kilocode/blob/e9948b6000697757ceea194403750cd80effe70c/packages/opencode/src/kilocode/session/overflow.ts)
  combines a measured baseline with additions; [chunk extraction](https://github.com/Kilo-Org/kilocode/blob/e9948b6000697757ceea194403750cd80effe70c/packages/opencode/src/kilocode/session/compaction-chunks.ts)
  budgets and reduces oversized history. Its fixed estimate inflation and clipped
  transcript/tool excerpts are not copied; Crude submits complete attributed text
  fragments and learns a smaller budget from provider evidence.
- **DeepSeek Harness**, `639ed015397290b3745d163aafe02ffee4aa3f84`:
  [compaction backend](https://github.com/deepseek-ai/deepseek-harness/blob/639ed015397290b3745d163aafe02ffee4aa3f84/packages/compaction/compaction-basic/src/index.ts)
  reserves output/headroom and allows overflow retries only after surface
  progress. Its [token meter](https://github.com/deepseek-ai/deepseek-harness/blob/639ed015397290b3745d163aafe02ffee4aa3f84/packages/llm/token-meter/README.md#readme)
  scopes usage to the routed envelope. Crude adopts progress-gated recovery and
  connection-scoped capacity while keeping its stricter all-or-nothing checkpoint
  transaction; optional pruning is not a partial commit here.

## Validation and limits

Regressions cover an approximately 540K-token source against a 256K extractor,
real `Session.HandleCompact` histories above 350K with 64K/128K Aeron connections,
huge tool arguments/results, UTF-8 boundaries, late child wakes, provider capacity
rejections, stage-local retries, reasoning-preserving output-cap recovery,
checkpoint refinement, typed output-ceiling learning across new sessions and
catalog reopen, connection/profile isolation, expiry, truncated output,
cancellation, exact source archival, and passive persisted checkpoint
restoration, answered-request non-replay, live async-state refresh, and bounded
project-guidance compilation. Name-allocation tests
also exhaust the full first-name/surname tier without duplicate numeric labels.

This is not a promise of perfect recall: model summaries can omit facts, token
estimation without reported usage remains approximate, and media contents are not
text fragments. Oversized text extraction keeps media descriptors while the
exact media-bearing source remains archived. System/tool envelopes that alone
exhaust a model, unavailable providers, and reductions that cannot converge fail
closed with the source retained. General model-visible tool-output offload and
provider-native remote compaction are separate future work, not implied by this
implementation.

Complete project-guidance intake is a separate pipeline, not conversation
compaction or permission to load every referenced document. Its 64 MiB source
ceiling, target-model token budget, model-backed verification limits and
session-local cache are documented in the
[prompt architecture contract](prompt-architecture.md#complete-guidance-intake).
Neither pipeline's per-request context bound is a total cost guarantee.
