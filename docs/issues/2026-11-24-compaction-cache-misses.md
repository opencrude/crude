# Cold requests after automatic compaction

> Status: RESOLVED for raw history replay after a model-written checkpoint. The subsequent [native implementation](../reference/adaptive-context.md#native-compaction-on-custom-connections) covers Responses and Messages. Exact gateway billing is not claimed. Research checked on 2026-11-24.

For the later fixes and the stored and stateless threshold checks, see
[checkpoint recovery on custom connections](2026-11-25-checkpoint-recovery.md).

## What happened on the wire

The penalty was real. It was not caused by the Claude model name, nor does it require a broken server cache. A cache matches the **beginning** of a rendered prompt. A summary inserted before an unchanged tool result changes the prefix of that result. A smaller upload, a stored response ID, and a cheaper inference are three different things.

In the November 22 session starting at 11:15, Crude sent Claude Opus 5.5 through a custom **OpenAI Responses** connection. The request log, completed checkpoint and request usage agree:

| | Before | First normal request after |
|---|---:|---:|
| Input tokens reported | 768,407 | 504,854 |
| Cache-read tokens reported | 767,509 | 17,850 |
| Input not reported as a cache read | 898 | 487,004 |
| Crude catalog input-cost estimate | $0.157 | $1.952 |

At adoption the planner estimated a 27,607-token recent tail. The **actual prepared cut was older**: 421 messages stayed verbatim. The resulting local estimate was 289,333 tokens; the gateway counted 504,854. The previous request used a delta; the first request after the checkpoint correctly logged `input_prefix_changed` and sent all 431 messages. The following request read 504,852 of 506,387 tokens from cache. That distinguishes a cold rebuild from a gateway that never caches. Reusing the old response ID would have restored the history Crude meant to replace. On November 24 a GPT-6.2 Sol session also fell from 486,144 of 489,175 tokens cached to **zero of 206,141**. Errand's separate `loop_end` operation was already a full checkpoint, not the background-prefix path.

**Method.** Read only request counts, identities and timings from this workspace's retained November session databases created before this investigation. For each completed `background` boundary, compare the nearest normal request for that agent on either side of `started_at` / `ended_at` (by `created_at`). Require the same model and account, `provider_id=custom`, each row within 120 seconds of its boundary, non-estimated, non-partial, positive input and an explicit valid cache count. Of 184 boundaries, 179 in 38 session databases met those conditions. In 175 the read fraction fell. Median per-request read fraction went from **99.62% to 2.62%**; median input not read from cache went from **1,726 to 284,634 tokens**. These are paired observations, not an A/B test or a representative sample of every provider. The detailed Claude route was verified separately from the request log; `provider_id=custom` alone does not identify a wire standard. No paid replay was needed.

Input minus cache reads includes cache *writes* where applicable. Crude's model catalog is not the gateway's invoice. Its input/output/read rates do not distinguish write lifetimes, custom markups, or all service tiers. The data establishes the cache drop, not an exact dollar loss or a promised saving.

## What each of Crude's seven wires can establish

| Wire | Cache, continuation and compaction are distinct |
|---|---|
| OpenAI Chat Completions (`openai`) | [OpenAI prompt caching](https://developers.openai.com/api/docs/guides/prompt-caching) is automatic for eligible models and prefixes; minimum size, breakpoints, retention and write pricing depend on model. Send history and tools. Read `prompt_tokens_details.cached_tokens`. Crude also recognizes a gateway's `prompt_cache_hit_tokens`. Chat has no Responses compaction-item contract. |
| OpenAI Responses | Eligible exact prefixes can hit; inspect `input_tokens_details.cached_tokens` and reported writes. [`previous_response_id`](https://developers.openai.com/api/docs/guides/conversation-state) reduces upload, not necessarily billed input. [Native compaction](https://developers.openai.com/api/docs/guides/compaction) can use `context_management` with `compact_threshold`, or an explicit operation. Its encrypted item and related output items must survive ordered replay; the API also documents stateless use. Crude implements verified on-demand compaction; it does not enable server thresholds behind the local planner. `truncation:auto` is not a summary. |
| ChatGPT Codex | Crude uses a Responses-derived **HTTP** dialect with `store:false`, streaming and encrypted reasoning. It reads Responses usage; it does not use an HTTP stored-response anchor. [Codex's remote compactor](https://github.com/openai/codex/blob/44308610ef5cbc03d149707735878b0c8743b2df/codex-rs/core/src/compact_remote_v2.rs) has separate transport, retained-item budgets, cancellation and installation. A similar envelope does not prove the public API, a subscription backend and a proxy offer the same feature or billing. |
| Anthropic Messages | **Yes**, [automatic caching exists](https://platform.claude.com/docs/en/build-with-claude/prompt-caching). Messages must opt in: top-level `cache_control` advances the breakpoint, or explicit block markers select it. Crude already uses explicit markers on the last tool definition, marked system and marked message blocks. Order is tools → system → messages. Default TTL is five minutes; one hour is an option. Reads, writes and ordinary input are separate counters; sum them once for total input. [Native on-demand compaction](https://platform.claude.com/docs/en/build-with-claude/compaction-on-demand) is a different beta: request `capabilities.compaction` through the model API with the beta header, then preserve the signed block exactly. [Threshold compaction](https://platform.claude.com/docs/en/build-with-claude/compaction) has its own contract. Neither feature travels automatically through an OpenAI gateway. |
| Gemini generateContent | Supported models offer implicit prefix caching. Explicit [`CachedContent`](https://github.com/googleapis/googleapis/blob/master/google/ai/generativelanguage/v1beta/cache_service.proto) resources have creation, expiry and deletion; support is model-specific and storage can cost money ([Google's cookbook](https://github.com/google-gemini/cookbook/blob/main/quickstarts/Caching.ipynb)). Crude sends `contents`, reads `cachedContentTokenCount`, and does not create billable caches. A reference to cached input is not a summarized history. |
| Gemini Interactions | The [stateful API](https://ai.google.dev/gemini-api/docs/migrate-to-interactions) can chain `previous_interaction_id`; Crude deliberately uses `store:false` and replays steps. [Google's SDK](https://github.com/googleapis/python-genai/blob/10f479b29a9696555964b2664b8127b6d0b4fa90/google/genai/_gaos/types/interactions/usage.py) defines optional `total_cached_tokens`. No portable compaction block was established for this adapter. Do not transfer generateContent cache-resource semantics to Interactions. |
| Ollama native | [`/api/chat`](https://docs.ollama.com/api/chat) takes the messages. `keep_alive` controls model residence; it is not a prompt-cache promise. `prompt_eval_count` is input; `prompt_eval_cached_count`, **if present**, is the subset read from cache. Older/cloud endpoints may omit it. Latency and `prompt_eval_duration` are not billing counters. No summarized-history handle is documented for this endpoint. |

Gateways can translate or discard fields. For example, [OpenRouter](https://openrouter.ai/docs/guides/best-practices/prompt-caching) documents provider-specific cache controls and best-effort sticky routing with fallback. Its behavior is evidence about OpenRouter, not every custom account. In particular, Crude's stock Responses encoder does not forward Anthropic block breakpoints. A gateway could still cache implicitly or insert its own markers. A reported hit establishes past reuse, not portable native compaction.

## Decision and trade-offs

All current adapters can use the [portable checkpoint](../reference/adaptive-context.md#when-to-compact-adaptive-policy-and-background-preparation). Prepare **all** of the current history. If work arrives later, give its complete suffix and the prepared summary to the extractor, not the main model. Archive the exact original history; retain bounded genuine human input or the child assignment and refresh runtime facts. A manual request during a run owns its operation ID and summarizes the whole current source. Missing Gemini cache counts no longer masquerade as measured misses; the Ollama codec no longer drops measured hits. Test the first continuation, not only the summary or local token estimate.

Finishing a background checkpoint can now require one additional extraction (or several chunks); summarization can lose detail. This exchanges extraction on the configured compaction model for a smaller cold request to the main model. The system prompt, tools and permitted human retention can still be large. The provider can miss the entire cache. An unsuccessful extraction is not success: below capacity the existing deferral keeps the original context; at capacity the run stops recoverably if neither model can write a complete checkpoint. Trimming a few reads and resending the rest as a purported success is no longer an escape hatch. There is no invoice cap.

Two useful comparisons: [Gemini CLI v0.30.0](https://github.com/google-gemini/gemini-cli/blob/v0.30.0/packages/core/src/services/chatCompressionService.ts) limits retained function responses separately from its recent-history fraction. That bounds a class of data, not its price after a prefix rewrite. [Kilo's chunk reducer](https://github.com/Kilo-Org/kilocode/blob/e9948b6000697757ceea194403750cd80effe70c/packages/opencode/src/kilocode/session/compaction-chunks.ts) budgets oversized replay and can summarize it before resending. Crude reuses its own complete-source reducer rather than clipping original evidence or guessing a universal token threshold.

**Recommendation for native Auto:** negotiate on the selected connection, not the model name. Key capability evidence by endpoint, wire, account, model and API version; never change endpoint, credentials, retention or service tier to discover a cheaper route. Prefer advertised capabilities. A 200 from a proxy that ignores an option is not success: require a typed compaction artifact and verified continuation. Archive before replacement, keep a portable checkpoint for switching providers and resume, and account separately for preparation, compaction and first continuation. On unknown support use the portable path. Do not send a paid copy of a user's large conversation to probe support or retry an uncertain billable request. The initial cold-tail repair did not add discovery. The follow-up implements the account choice, lazy checks, portable export and recovery on the two checked wires.


**Live follow-up.** The same custom Responses account accepted native compaction and
artifact replay for Claude Opus 5.5 but did not verify for GPT-6.2 Astra (the
standalone operation returned 404 and the inline operation returned 400). In the
Claude journey the normal compaction entry point archived no private conversation:
its input was an invented shelf check. Crude validated a four-section portable
checkpoint, and the next real request replayed the encrypted item and correctly
identified the blue shelf. This establishes that route's behavior at the time of
the check, not lasting capability, a cache hit, or a promised cost saving.

**2026-11-25: why the Astra check fell back.** On one automatic boundary the
session archived 420 messages and completed all four checkpoint sections.
The estimated active context fell from 499,611 to 44,571 tokens. The same turn,
human steering and running command continued. This was an Errand checkpoint,
not a native one: the writer reported 505,146 input tokens. The first normal
continuation reported 42,654 input tokens and zero cached; the second reported
42,880 cached out of 48,731. These are request measurements, not a bill.

At 17:09:32 UTC the gateway recorded HTTP 400 on the inline check:
`'compaction_trigger' requires 'max_output_tokens' to be at least 20000 when specified.`
The earlier standalone check on that route returned HTTP 404. The inline
check had copied its 512-token text allowance to an operation that produces a
compaction item. This was not evidence that the model could not compact.
The trigger no longer sends that optional field or forces a larger text
allowance. `TestNativeCheckpointDiscoveryAndReplay/trigger` reproduces the
rejection and checks discovery, read-back and the next turn through the
Responses simulator. No production conversation was replayed for the check.

**2026-11-25: a model change sent the whole history to the new model.** At
12:24 UTC-5 `gpt-6.2-astra` reached its usage limit. The user switched the
session to `claude-opus-5-5` on the same Responses gateway, stopped the first
turn after 1.8 seconds and typed `/compact`. Auto treated the human request like
any native boundary: two synthetic checks (233 and 380 input tokens), then the
real native compaction with 491,798 input tokens, none cached, $2.04 by the
catalog price. The new model had never read this context, so no cache could
help. The user stopped the read-back. Errand, a cheap model on the same account,
was configured and could have written it. Now Errand writes every `/compact`,
with every Compaction setting. Automatic compaction skips the provider for a
context the serving model did not write, and lets Errand or the cheaper model
write it.

**2026-11-25: a cut Responses stream re-uploaded the whole history.** At 12:09
a stream ended with `stream closed before response.completed` after partial
output. The recovery request planned `response_id_unavailable` and sent 419
messages (3,781,412 bytes, 499,447 input tokens) instead of a delta after the
previous completed response. The gateway still reported 480,896 cached tokens,
so on this route the cost was the upload, not a cache miss. The latest
completed response now anchors the delta, and the cut output travels once
after it. The seven-wire matrix guards that each resume keeps the cut request
as an exact prefix; it does not measure a provider bill.
