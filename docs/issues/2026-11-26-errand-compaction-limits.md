# Errand compaction failed on every run with "Request Entity Too Large"

> Status: Fixed in source. The running core uses the fix after it is rebuilt and restarted.

## What we saw

On 2026-11-26 testers on several machines reported that manual `/compact` and
automatic compaction stopped with `413 Request Entity Too Large` and similar
limit errors, on every attempt. Since 6f69c534 every `/compact` and every
automatic checkpoint of a cold context is Errand's, so Errand's route now
receives the complete history much more often.

A reproduction on the release code (Errand behind a proxy with a 96 KiB body
limit, a 1M-token advertised window and an 80-message history) failed with:

```
intent stage: API 413: <html><head><title>413 Request Entity Too Large</title>...
```

## Cause

Extraction planned each request only from the model's advertised context
window and output ceiling. A route often accepts less than it advertises:

- A proxy in front of the provider caps the request body (nginx defaults to
  1 MiB). A 413 named no context length, so Crude did not treat it as a size
  limit; it sent the same request again for every section.
- Only one context wording family and one output wording (`max_tokens
  (current value: N) must be between 0 and M`) were recognized. Anthropic,
  OpenAI, Gemini, vLLM, TGI, llama.cpp, LM Studio and per-minute token
  rejections were not.
- A context reduction lasted only for the current compaction, so the next run
  failed the same way.
- Oversized histories were split into independent chunks and their summaries
  merged afterwards, so a later chunk could not see what earlier ones had
  established.

## Fix

- One limits owner per extraction plans every request and stage within the
  advertised window, the output ceilings and the limits learned for the
  connection. Provider evidence narrows them: a stated or unstated context
  limit, an output ceiling in any supported wording, a body limit, a
  per-request token rate limit, a refusal without a cause, or a large request
  that keeps timing out. See [adaptive context](../reference/adaptive-context.md#layered-policy).
- Learned limits are saved in `model-catalog.db` per Errand profile,
  endpoint, protocol, account and model. A stated or proved limit lasts seven
  days; an inferred one lasts one day and is saved only after the smaller
  request succeeds. Forgetting compaction checks or removing the account
  deletes them.
- A long history is read in chained parts. Each part's request carries the
  running checkpoint of the earlier parts and returns it updated, so the
  result is one coherent checkpoint. A narrowed limit plans the same part
  again from the same running checkpoint.
- The same classifier recognizes the added context rejections on ordinary
  requests too, so such a request compacts and continues instead of stopping.
- Who writes the checkpoint did not change: `/compact` is still Errand's, and
  automatic compaction still chooses by cache and cost.

## Regression checks

`agent/turnloop/compaction_limits_test.go` runs each limit twice on fresh
agents: the first compaction must adapt and keep every numbered fact of the
history, and the second must not hit the limit again. It covers a proxy body
limit with an inline screenshot, a stated context window, an output ceiling,
an unexplained 400, a per-minute token limit, repeated timeouts, and a route
that refuses every request (bounded requests, nothing remembered). The journey
test sends `/compact` through `Agent.Run` with Errand behind a body limit and
checks that the next request keeps the system prompt, its tools, the
checkpoint and the pending request, with no raw history behind it.
`TestRequestTooLargeReadsTheLimitFromProviderEvidence` checks the provider
error texts. The same reproduction that failed on the release code succeeds
on the fix.

## Limits

- Ollama can drop the start of a prompt longer than its loaded context
  without an error. Crude cannot see that from the reply, so it is not
  detected.
- The main agent's ordinary requests do not use the learned limits; only
  compaction does.
