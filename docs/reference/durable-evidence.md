# Durable evidence

> Status: Current reference — Crude's working-set, run, file and performance evidence.

Crude exposes read-only evidence through its existing tools. Aeron is a role in
that runtime and uses the same owners and access checks. An inspection does not
start agents, resume sessions, launch language services or change any file.
The underlying stores are not agent interfaces.

## Inspect the working set

- `shell.inspect` with `include_diagnostics: true` reads the originating shell's
  tabs, dirty markers and diagnostic provenance. It reads only already-applied
  facts. A newer private edit can make a previous result out of date, including
  a previous result with no problems. Private text and other shells' tabs are
  absent. The bounded tab inventory reports how many tabs it omits.
- `diagnostics.workspace`, `page.view: documents`, reads the core's open named
  documents and matches reported diagnostics to each accepted revision. This
  works without a shell binding. It cannot read private drafts or say which
  editor has focus. An unavailable owner is not an empty working set.
- `diagnostics.workspace`, `page.view: runs`, reads the 128 most recent retained
  attempts, with optional exact agent ID as `page.filter`. It distinguishes a
  bounded read from the journal's full history. Use `search_history` for topics
  and prior sessions, `board_read` for specific collaboration events and
  `history.timeline` for retained Local History. Times from different owners
  are not a global transaction clock. The run brief names its run and reports
  whether it has an originating shell. It includes a small accepted working set
  for the logical checkout when available. Detailed reads are on demand.

## Find a past prompt by model

Discover `history.prompts` with `crude_commands`. Then call `crude_control`:

```json
{"command":"history.prompts","arguments":{"model":"opus","category":"default"}}
```

This returns each matching agent's first human-led turn with a recorded,
successful normal request for that model. Omit `category` to include all
roles. Use `mode: "first"` to require that model on the agent's very first
human prompt. Use `agent_id` alone to read that agent's first human prompt
regardless of model. A prompt may list more than one model when requests in
its turn used different models. Current settings, failed and background
requests, peer assignments, and model names mentioned in chat do not match.
Human steering text is readable, but Crude does not infer which model saw it.

Read full saved text with the returned `agent_id` and `prompt_id`. For a long
prompt, echo `next_text_offset` as `text_offset`. The offsets are bytes and
never split a UTF-8 character. This returns the public text, not expanded
references, raw provider input or attachment bytes. It does not open a session.

Echo `next` as `page` to finish an immutable result page stream. Then follow
`source.next_scan` as new arguments to search the next batch of agents, even
if the current batch has no match. An unavailable transcript or a prompt
without a recorded turn-model link is a gap, not proof a model was never
used. The scan covers registered participants in this workspace.

## Inspect an exact run

1. Find an agent with `agent_list`. Use `agent_inspect` with `list_runs: true`
   and that agent's exact ID. To read older runs, echo its `before_run` value.
2. Use `agent_inspect` with the same agent ID and a `run_id`. Follow `work_cursor`
   to read older public activity in that attempt. The trigger links to the
   recorded human input or Board event when available. Reports link to exact
   `artifact_read` revisions. A report is a claim; the run state and typed tool
   outcome are separate facts. No private reasoning, raw tool output or provider
   requests are returned. Output buffered in the active runner and older archive
   rows without exact attempt attribution are not guessed.
3. Use `history.run_changes` with the exact run ID in the permitted checkout.
   It reads direct file operation receipts from Local History. Use its `after`
   cursor for more rows or select one `operation_id` and `path` for a retained
   diff. A diff compares recorded before and after bytes, not today's file.
   A receipt that did not commit does not prove a change. Changes made through
   opaque shell or MCP operations, early builds without attempt attribution,
   and pruned or disabled Local History are not reconstructed. Use the
   managed execution review tools for checkout-wide integration evidence.

## Compare builds

`performance.core` and `performance.shell` still read the live rings.
`performance.archive.core` and `performance.archive.shell` read the saved
evidence on their respective hosts. A remote core and a local shell do not
silently share a clock or a store. Start with `builds`, then select `hours`,
`stalls`, or `compare`. Set `build_id`, `against_build_id` and the exact metric in
`page.filter` for `compare`. An example metric is `core.actor` or `ui.update`.
Echo the returned `next` as `page` on continuation, without changing the selection.

Both producers save closed windows from the existing metric rings about once
per minute in local SQLite (`performance.db` under the selected Crude profile).
Hourly rows retain observed seconds, sample counts, sums, peaks, and the
producer's histogram buckets and thresholds. The archive records build image,
source cohort, release metadata, process incarnation, host and runtime facts.
It holds up to 90 days of hourly rows and seven days of selected stall events.
Retention runs on writes. The database has a page cap; its WAL is separate.
A write failure or a process crash can leave a gap. Neither pauses the UI nor
causes a missing figure to become a confirmed zero. No tool labels, source text,
terminal output or paths are included in the measurements or stall records.

Compare counts and coverage as well as percentiles. Summaries merge histogram
counts, not calculated percentiles. Observation from everyday work cannot prove
that a specific optimization caused a change. Use a repeatable workload on both
builds on the same machine for that conclusion.

Run `BENCH_EVIDENCE=/absolute/path/before.txt make bench-gate` for a local
checkpoint. Use another file for the after checkpoint and compare the raw Go
benchmark evidence with `benchstat`. Explicit Linux verification and verified
release workflows retain the same raw evidence as a workflow artifact, tagged
with the exact commit. These artifacts are separate from the runtime archive.
An instant release does not generate verified benchmark evidence. No timing
or allocation gate is relaxed by turning on artifact output.
