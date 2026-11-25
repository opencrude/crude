# Prompt cache misses looked like cache hits

> Status: Reporting fixed. A provider or gateway can still omit cache counts or report a miss without saying why.

## What we saw

Copied nine November 22–24 session databases and the available WAL/SHM files to a temporary directory before querying. The copies contained 2,879 request usage rows. All were through the same custom provider (`proxy`); the retained request logs named OpenAI Responses. Of the normal requests, 28 reported exactly zero reads and 30 reported reads below 5%. All 2,879 rows had a read count marked known. Only 1,181 had a write count marked known, and none reported a positive write count. The session log records the normalized request usage, not the raw upstream response or invoice.

| Request | Input | Read | Write | Old agent header | Latest request |
|---|---:|---:|---:|---:|---:|
| `88533cae9903972adcf0332157245b1a`, GPT-6.2 Sol | 306,951 | 0 | 0 reported | 98.1% | 0.0% |
| `69b4d2c1535e5d4b4d569ac0834a937e`, Claude Opus 5.5 | 412,531 | 17,864 | Not reported | 98.1% | 4.3% |
| `d540e52760c6991765e0f96acdd78c35`, Claude Opus 5.5 | 130,849 | 0 | Not reported | 85.8% | 0.0% |

All three rows were measured and complete. The requests logged `input_prefix_changed`. The GPT request's own turn also averaged 98.6%, so changing to a turn average would not expose the miss. In the last Claude request, all 130,849 tokens were **not read from cache**; the old details omitted that result because the gateway did not give a write count. Neither the log nor the stored counters can tell how many tokens the gateway wrote, how it routed the request, or the effective cache lifetime.

Two older sessions also used custom **OpenAI Chat Completions**. Request `d233dd12d1a4e6c1e35481b4c8d02a3e` reported zero reads of 26,337 input, with writes omitted. Request `9d67de5f49028757792ec09c3411abab` recorded 25,635 input from an interrupted request and no known reads or writes. Its cost log still printed `cached=0`: the log does not include the presence or partial flags. The stored evidence, not that log field alone, distinguishes the two. The new header shows 0.0% for the former and **last cache not available** for the latter.

## What changed

- **Last cache** in the agent header now reports reads divided by input for that agent's latest recorded normal request. Later background requests or revisions to earlier requests cannot change which request is last. An explicit zero remains 0.0%; an absent, invalid or incomplete report does not get a percentage. The totals in the Workspace panel remain scoped to the agent, subtree or session.
- **Not from cache** is input minus reads, including any input processed and written to cache. **Cache write** is separate. None of these values is a billing class or an invoice. A missing write count no longer suppresses the not-read count. A read count outside the input for its own request cannot be hidden by aggregating with other requests. Totals with missing evidence say **Not reported** instead of treating missing requests as measured zeroes.
- Anthropic can supply cumulative input, read and write counts in `message_delta`, not only `message_start`. Crude now updates each field that appears and counts ordinary, read and written input once. For example, a delta with 3,200 ordinary input, 0 reads, and 500 writes now means 3,700 input and a miss; it no longer retains 3,000 reads from the start frame. Compaction usage also requires every contributing request or billed iteration to report a count before marking its total known.

## Wire checks and remaining limits

OpenAI Chat, Responses and private Codex use inclusive input; a reported write is not added to it. Anthropic Messages uses separate ordinary/read/write counts. Gemini generateContent and Interactions preserve a cached subset of their prompt counts and account for separately reported tool-use input. Ollama's optional `prompt_eval_cached_count` is also a subset of full input, not an additional count. Explicit zero and absent fields are tested independently; no other wires were identified in these retained request logs. See the [wire evidence](2026-11-24-compaction-cache-misses.md#what-each-of-crudes-seven-wires-can-establish) and [Anthropic's cumulative usage type](https://github.com/anthropics/anthropic-sdk-python/blob/f1edcd7d751a4cd6d72cf8b6454930d8aab2c97c/src/anthropic/types/message_delta_usage.py).

The old rows cannot reveal counters a gateway dropped or how it mapped upstream accounting. Crude's catalog cost is an estimate; it does not establish cache-write billing or retention. No paid requests were replayed for this investigation.
