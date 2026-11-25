# Checkpoint recovery on custom connections

> Status: RESOLVED. [Adaptive context](../reference/adaptive-context.md#native-compaction-on-custom-connections) defines the native, portable and retry behavior.

## What changed

| Failure | Fix and evidence |
|---|---|
| Automatic extraction sent chunks that fit the history estimate but not the real request. | `0ee97c8f` budgets the system prompt and tool declarations before partitioning the source. |
| The provider completed a native compact, but Crude discarded it after a capped read-back. | `2a973258` sizes the read-back for the serving model. A shorter second read can use the same paid artifact without resending the original conversation. See the [incident](../issues/2026-11-25-native-checkpoint-read-back.md). |
| A repeated checkpoint lost earlier human requests. | `ebee319a` keeps answered prompts verbatim up to the retention budget, distinct from the active instruction. The session test closes and reopens a real database. |
| A settings change or misleading cache header obscured a real cache loss. | `9ca845fb` preserves the root prefix and tool set; `a7a16a5f` distinguishes a measured miss from an unreported counter. |
| Throttling could discard native evidence, and quota/veto could fall back to the same rejected route. | `e5b39ca3` distinguishes temporary and permanent evidence, honors retry advice, and retains the source and possibly paid write. |
| A threshold-only route could not compact at the boundary, including after both stored-prefix on-demand operations refused. | The actual running turn can carry the edit; see `d29a9256` and the persisted session test below. The default stored and stateless variants both verify one settled tool round, one checkpoint, passive reopen with no requests, and the first continuation. |

Manual `/compact` still takes precedence at the next safe request boundary, even
mid-run. The continuation keeps the system prompt, tools and pending human
instruction. The archive is not appended behind Errand's replacement history.

## Regression checks

`agent/turnloop/threshold_test.go` exercises the manual and automatic boundaries
through `Agent.Run`: a missed trigger keeps the ordinary answer, a null or missing
checkpoint keeps the source, and quota and interrupted streams do not repeat a
possibly paid submission. `TestResponsesThresholdMixedRoundPersistsThroughSessionReopen`
checks both stored and stateless Responses: one tool result, one installed checkpoint,
no request during reopen, and the system prompt and tools on the next turn.
The rate-limit tests cover HTTP and stream errors during discovery, writing and
read-back. The [recovery matrix](../reference/run-recovery.md#one-supervisor-separate-budgets)
explains when a different writer can take over.

## Earlier live evidence

A custom Anthropic Messages route serving Claude Haiku 5.5 accepted the
[on-demand request](https://platform.claude.com/docs/en/build-with-claude/compaction-on-demand)
and returned a signed block. On the same route,
[threshold compaction](https://platform.claude.com/docs/en/build-with-claude/compaction)
with a 50,000-token trigger and `pause_after_compaction: true` returned
`stop_reason: "compaction"` and a content-only block. Replay without the
`compact_20260112` edit returned HTTP 400. Replay with the edit and
`compact-2026-01-12` beta header recalled the synthetic ticket. The proxy
omitted capabilities from model metadata, so this is observed behavior,
not evidence that the model advertises the feature. Do not repeat these paid
probes just to recover the contract. This historical observation is not used as a model-name capability rule.
