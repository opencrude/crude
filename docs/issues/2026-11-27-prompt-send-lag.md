# Sending a prompt lagged in dense sessions, worse with several agents

> Status: Fixed in source. Existing session databases must be aligned once
> while Crude is closed; see [Local data](#local-data).

## What we saw

On Windows, pressing Enter in a long session held the composer for one to two
seconds before the turn started, and longer while other agents ran. The
Performance dashboard listed many problems at the same time.

## Cause

The Performance archive showed every `agent.submit` at 1.1–2.0 s on the core.
Almost all of it was one session database write (`storage.session`): 0.7–1.0 s
to prepare the transaction and 0.7–0.8 s to commit it.

Each human message starts a recovery incident. Its archive is the whole
history (122 MB of JSON in a 4,800-message session) and its active context is
the model context. Both lists belonged to the incident, so every send wrote the
whole history again, and the clean end of the turn deleted it. The first
checkpoint inside the turn wrote it a third time: the run's archive began with
the request's system prompt, which the admission archive did not have, so
every message moved by one position. Child agents mirror their checkpoints into
the parent's session database, so the send also waited behind them.

Two dashboard findings were false:

- "Frame handoff waits: 1.0ms p95". A percentile is its histogram bucket's
  upper bound, so the value meant "at most 1 ms", under the 1 ms threshold.
- "Slow core work: agent.compact". Compaction, commit messages written by a
  model and Git transfers wait for a model or a remote server; 30–110 s is
  normal for them.

## Fix

- Recovery lists belong to the runtime agent (`agent_message_lists`). A new
  incident takes over the newest list of its agent that no other unfinished
  incident holds and writes only the messages it adds. A list the process does
  not remember is compared with its rows first. The run's archive no longer
  stores the system prompt. Contract: [run recovery](../reference/run-recovery.md).
- Percentiles cross a threshold only when they are above it.
- Model-bound and remote requests are timed in `core.requests.remote` and
  `link.requests.remote`, without thresholds. `storage.session` saves its
  operation labels, so archived stalls name the write.

Measurements are in [performance baselines](../reference/performance-baselines.md).

## Regression checks

- `BenchmarkNewTurnCheckpoint_2000Msgs` (bench gate): 920 ms and 71,600
  allocations per turn before, 3–4 ms and under 590 allocations after.
- `TestNewTurnsWriteOnlyTheirOwnMessagesToRecoveryLists` runs real turns
  through input admission, restarts the session and checks that rows written
  per turn stay constant while the history grows.
- `agent/sessiondb/recovery_lists_test.go`: lists after a restart, an
  unfinished incident's lists are never taken, a settled incident whose list
  was reused refuses to load, an older base is emptied, a finished child mirror
  leaves no rows.
- `TestPercentileAtThresholdBucketBoundIsNotAFinding`,
  `TestModelAndRemoteRequestsAreNotSlowCoreWork`,
  `TestRemoteBoundSeparatesModelAndRemoteWork`.

## Local data

The new build refuses a session database that still has the old
`agent_recovery_tail` table ("current recovery list schema required; align
local data manually"). Close every Crude window, run `crude --core-stop`, then
run `python %USERPROFILE%\.crude\backups\recovery-lists-20261127\align.py`. It
backs up each database first, copies every incident's lists byte for byte and
checks them before it drops the old tables.

## Still open

- The first turn after a restart usually writes the archive once more: the
  resumed archive is the transcript, without the hidden context messages that
  the live archive held.
- "UI froze": a single offscreen assistant message whose Markdown takes
  50–340 ms to render is materialized on the UI goroutine during `View`.
