# Provider compaction handoff

> Status: ACTIVE HANDOFF. The wire changes are on `origin/master`. Integration tests and server-side threshold compaction are not complete.

## What reached master

| Commit | Change |
|---|---|
| `8ae8e87f` | Avoid raw tool-history replay after a portable checkpoint. Accept LSP configuration dynamic registrations and preserve optional Gemini and Ollama cache counters. |
| `f379d8bd` | Discover native Responses and Anthropic Messages compaction on the selected account. Keep portable text for recovery. Add Auto, Provider only, Errand only, and Forget compaction checks. |
| `faaa1f3f` | Make `/compact` take precedence at the next safe request boundary. Discard stale preparation, wake interruptible waits, and keep the running turn's prompt and steering distinct from answered requests. |
| `bb53f8a9` | Add the native Codex HTTP path and the serving-model portable writer. This commit is pushed, but the turnloop suite is not green. |

The last commit tries `responses/compact` and can discover the streamed
`compaction_trigger` operation for Codex HTTP. For Chat Completions, Gemini
generateContent, Gemini Interactions, and Ollama native, no bounded native
operation was established. The serving model can write a complete portable
checkpoint. Auto considers known prices, observed cache reuse, fit, and Errand
availability. Provider only must never charge another model. Errand only must
never probe. Declaring the original tools with tool use disabled can preserve a
cache prefix on wires that support it. It does not establish a cache hit.

Focused tests for both Codex HTTP paths, the standalone Responses request
shape, and serving-model selection pass. The Codex tests use a local fake
server, and the serving-model selection test uses a Chat Completions fixture.
No live Codex subscription check or per-wire cost comparison was completed.
The running core must be rebuilt and restarted before it can use these changes.

## Resume here

1. Run `go test ./agent/turnloop -count=1` in the native cgo environment.
   At `bb53f8a9` it reproduced 12 failing tests:
   - `TestAutomaticNativeCheckpointRecoversRejectedArtifactBeforeOutput`
     and `TestAutoUncertainNativeCheckpointUsesErrandAndContinues`: the fake
     server reads the synthetic ticket from top-level `instructions`. The
     standalone Responses request now sends it in `input`. Keep the original
     rejection, fallback, request-count, and continuation assertions.
   - The other ten cover capacity replanning, runtime notice pressure,
     truncation, cancellation, reasoning allowances, learned output ceilings,
     and the prohibition on sending partially rewritten history. Trace writer
     identity and `extractionTools` through `context_compaction.go` and
     `context_extraction.go` before changing expectations. `request` counts
     the added tools, but `partition` does not subtract them from its chunk
     budget. Some fixtures still estimate input without the tools or expect
     none. Align the reducer's budget with the request it sends, then update
     the fixtures without weakening their recovery assertions.
2. Test request shape and the first continuation for each wire. Compare the
   serving extraction request against the previous normal request, including
   system prompt, tool list and history ordering. Test staged and single-pass
   extraction separately; a stage that changes or splits the prefix cannot
   assume the previous cache ratio. Preserve the full archive and never
   attach a large raw tool tail to the rewritten history. For Codex HTTP,
   retain the typed item and test missing or ignored operations, rate limits,
   cancellation, and portable recovery. Check that a transient failure
   cools down rather than becoming a permanent refusal.
3. Implement and verify threshold compaction separately from the on-demand
   codecs. The design below is decided (2026-11-25); implement it as written.

### Threshold compaction at Crude's boundary (decided)

Threshold compaction is a native route for accounts where on-demand
compaction is not available. On-demand stays first when it is verified, as
[Anthropic recommends](https://platform.claude.com/docs/en/build-with-claude/compaction#choose-how-to-compact).

- **Crude owns the schedule.** Ordinary requests never carry a compaction
  edit, so the server never compacts on its own. Only when Crude reaches its
  own compaction boundary (automatic, required or a manual `/compact`) does
  the *next ordinary request* carry the edit. That request is the one Crude
  sends anyway: same system prompt, tools, history and cache breakpoints. No
  separate upload of the same source is made.
- **Messages:** add `context_management.edits[{type: "compact_20260112",
  trigger: {type: "input_tokens", value: V}, pause_after_compaction: true,
  instructions: I}]` and the `compact-2026-01-12` beta header. `V` is the
  documented minimum of 50,000 tokens, so the edit fires on any request at
  Crude's boundary that is larger than that. `I` is Crude's portable
  checkpoint instruction from `agent/prompt/`. It must say that the model
  writes text only and calls no tools, because a request with tools can
  return `content: null` ([current limitations](https://platform.claude.com/docs/en/build-with-claude/compaction-threshold#current-limitations)).
- **Result handling.** Look for the `compaction` block, not the stop reason.
  A block with text content is a portable checkpoint body. It goes through
  `ValidatePortable` and the same install path as an on-demand read-back, so
  answered prompts, the pending turn, the system prompt and the tools stay
  exactly as they do today. Crude does not replay the provider block, so later
  requests need no edit and no second schedule exists. A `null` block (no
  summary, or a refusal) is a failed native attempt: Auto uses the portable
  writer for the same source, Provider only reports it.
- **No compaction happened.** If the request was below the trigger, the
  response is an ordinary turn. Crude keeps it as the turn's answer. Nothing
  was wasted, and the next boundary tries again.
- **Discovery at a real boundary.** No synthetic probe. Model metadata that
  advertises `context_management.compact_20260112` marks the route as
  likely. A 400 that rejects the edit or the beta records "unsupported" for
  the route. A returned compaction block records "verified". A 429 follows the
  rate-limit rules above and is never "unsupported".
- **Responses `compact_threshold`:** same rule, for gateways that have no
  `/responses/compact`. The edit goes only on the boundary request. The
  same-stream compaction item is an opaque artifact, so it goes through the
  existing provider read-back with the serving model's allowance (the path
  fixed in `2a973258`), and is archived like an on-demand artifact.
- **Cost and notices.** Usage comes from `usage.iterations`; the compaction
  iteration is reported as the checkpoint cost, the message iteration as the
  turn. The user sees the same compaction notices as for on-demand.
- **Tests** run against `internal/providersim`, which already models
  `compact_20260112`, `pause_after_compaction`, the replay rule and Responses
  `compact_threshold`. Cover: fired with text, fired with `null`, below the
  trigger (ordinary answer kept), 400 unsupported, 429, manual `/compact`
  mid-run, and the first continuation request (system prompt, tools, answered
  prompts, no provider block, no edit).
4. Update [adaptive context](../reference/adaptive-context.md#native-compaction-on-custom-connections),
   [Settings](../reference/settings.md), the account editor hints in
   `tui/screens/settingspages/account_connections.go`, and the
   [agent roadmap](agent-experience-roadmap.md). Some text still says that
   Auto always uses Errand on fallback or that Codex HTTP has no native
   check. Verify the corrected hints in the real shell at both required sizes.
5. Regenerate and check the cohort after source edits. Run the build,
   conventions, design, docs, focused race, benchmark, and full test gates in
   the [validation guide](../guides/validation.md). The Linux visual LSP tests
   need the pinned `gopls` fixture described there. Do not mark the feature
   complete based on the focused tests alone.

### Threshold evidence already collected

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
probes just to recover the contract. No threshold mode is implemented in
Crude yet.

### HTTP 429 should retry, not stop the run

For temporary throttling on any wire, preserve the checkpoint source and
pending turn. Retry via the existing bounded backoff and recovery supervisor
instead of ending the run. Honor `Retry-After`, `retry-after-ms` and
`RetryInfo` within the five-minute cooldown cap. Keep explicit retry vetoes
and confirmed billing or quota failures distinct from temporary throttling.
See [run recovery](../reference/run-recovery.md#one-supervisor-separate-budgets).

Normal requests already share the classification across wires. Test the
compaction paths separately. The portable extractor, the transport backoff and
the run supervisor now take the server hint from one owner,
`provider.AdvisedRetryDelay`. Native discovery records a cooldown and must not
learn "unsupported" from a 429.
Check both an HTTP 429 and a typed rate-limit error inside a stream.
Cover 429 followed by success in discovery, portable extraction and required
compaction. Check both Auto fallback and Provider only recovery. A confirmed
rate-limit rejection can be retried after the cooldown; an unconfirmed paid
request must keep its replay guard.
