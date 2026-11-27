# A checkpoint held an active agent for 26 minutes

> Status: Portable extraction and capacity handling verified on synthetic workloads. The full incident history was not replayed.

A completed automatic checkpoint of an estimated 895,108-token conversation
held the turn for 1,571.6 seconds. The serving account recorded native
compaction activity and a cooldown, not a verified native checkpoint. The
stored operation names Errand's `ollama/deepseek-v4.1-flash:1017` as its final
writer. The available evidence does not identify the exact remote failure.

The previous portable fallback did four separate extractions in sequence.
Each read the long source in parts and asked the model to **rewrite the
running checkpoint** after every part. The request log shows repeated reads
of the same ranges and growing outputs. This was avoidable work, not just a
slow first token. In a read-only sample from this workspace, seven completed
automatic checkpoints on the same writer ranged from 47.4 to 1,571.6 seconds
(median 71.3). Thirteen manual ones ranged from 11.1 to 111.8 seconds.
These are different histories and loads, not an A/B benchmark.

## Fix and evidence

Portable extraction reads the long source once into attributed notes, then
merges them in source order. The account admits the HTTP requests, not the
wire encoder. A known slot limit applies to concurrent requests on the same
configured endpoint and account even when another agent uses a different
conversation API. This admission rule does not make native artifacts portable
or authorize replay after accepted output. See [adaptive context](../reference/adaptive-context.md)
and the [seven-wire capacity matrix](../reference/provider-simulator.md#compaction-capacity-matrix).

The [live manual probe](../guides/validation.md#live-manual-compaction-probe)
used only a synthetic audit. On the configured 1017 model and Responses
connection, Crude read four parts and merged them in five requests with reported
usage. The checkpoint kept the release identifier, test name and
file path from three different source ranges. The run took 14.1 seconds;
Crude's full input estimate fell from 12,933 to 2,361 tokens. The provider
reported 23,824 input tokens across requests, 16,256 cache reads, and 4,539
output tokens. This measures that fixture and account, not the speed of the
895K-token incident or the cost on an invoice. No private session was sent.

A subsequent manual run read 726,374 estimated tokens in eight parts and committed
a 63,208-token checkpoint in 154.1 seconds. The last part returned at 81.2 seconds.
The first merge occupied another 28.2 seconds before the other three started.
The prefix gate now opens on the first content or reasoning delta, not at the
end of that reply. It still respects the account admission limit. Source coverage,
merge order, prompts and output budgets are unchanged.
