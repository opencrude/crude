# Provider compaction worked, but Crude threw it away

> Status: Fixed in source. The running core uses the fix after it is rebuilt and restarted.

## What we saw

On 2026-11-25 the user ran `/compact` on a root session of 817,283 estimated
tokens. The account is a custom proxy that serves Claude Opus 5.5 over the
OpenAI Responses wire. The shell showed "Reading the provider checkpoint" for
more than 20 seconds, and then the session continued from a checkpoint written
by the Errand model (DeepSeek), not by the provider.

The session log (`23:36:01`–`23:37:49`) shows the sequence:

| Request | Input | Output | Result |
|---|---:|---:|---|
| Native `responses/compact` from the stored-response delta | 1,273 | 1,019 | Window returned |
| Read-back of that window ("Reading the provider checkpoint") | 9,688 | 8,192 | Stopped at the output limit |
| Errand portable extraction of the whole session | 470,807 | 7,159 | Installed |

The model catalog then held a one-minute cooldown for the route
(`failures=1`, `method=responses/compact`).

## Cause

The read-back asks the serving model to write the four-section portable text
from the native window. Its response allowance was a fixed 8,192 tokens, and
the provider layer rejected any larger value. The same request told the model
that it had the whole continuation space for the checkpoint: the 817K-token
source size minus the system prompt, tools and runtime notes. Hidden reasoning also uses the allowance. The reply reached
8,192 tokens, the provider returned an incomplete response, and Crude treated
the paid native result as a failure. It then paid another model to read the
whole session again.

The four commits made earlier the same day (chunk budget, retry advice,
stable prompt, cache reporting) did not touch this path.

## Fix

- The read-back is a normal request of the serving model, so it uses that
  model's normal response allowance (configured maximum, model card, or the
  window-based default), never a fixed cap. Automatic in-run compaction now
  passes the serving agent's configured maximum too.
- The instructions never offer more checkpoint space than that allowance.
- On the Responses wires the read-back streams. A long reply then keeps bytes
  moving instead of looking like a stalled connection before its first byte.
- A reply that still stops at its limit reads the same paid window once more
  and asks for half the space. Only after that does Crude write a portable
  checkpoint. The notice names the cause: "The provider checkpoint reached
  its output limit."
- The provider layer no longer has its own text-size check; the caller's
  checkpoint validator is required and owns the size.

Regression tests: `TestNativeCheckpointReadBackUsesTheServingAllowance` and
`TestNativeCheckpointReadBackAtItsLimitAsksForLess` in `agent/turnloop`. They run
the real manual compaction path against the provider simulator's Responses
wire. The first fails on the previous code with "read-back allowance = 8192".

Contract: [adaptive context](../reference/adaptive-context.md#native-compaction-on-custom-connections).
