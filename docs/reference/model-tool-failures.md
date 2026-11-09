# Model tool failures

> Status: Current reference (2026-11-04) — tool failures caused by how a model
> family calls tools, with their causes, fixes and tests.

This page records tool failures that come from how a model family calls tools,
not from a defect in one task. Each entry gives the observed failure, its
cause, the fix and the test that keeps it fixed. Add an entry when a model
fails the same tool in the same way more than once.

The source of the 2026-11-04 entries is session `2026-11-04_003227_4a74bd5d`
(Claude Opus, Windows). Its `session.log` showed 16 `phase5_substring` edits,
one failed edit, one failed `agent_wait` call and three calls to tools that do
not exist.

## `edit_file` dropped text after a fragment match (fixed 2026-11-04)

**Failure.** After a large merge, edits seemed to "lose" words and conditions.
For example, a replacement of the start of an `if` line removed
`&& (msg.preview.Quiet || …)` from the end of that line. The tool reported
success, so the damage was found only by a later `git diff`.

**Cause.** Claude Opus models are trained on exact string replacement. Their
`old_string` often starts or ends inside a line. `fileio.ApplySearchReplace`
matched line by line. When the exact text was not a whole-line block, phase 5
(substring match) spliced the *whole* first and last content lines. The text
before the match on the first line and after the match on the last line was
deleted. Other model families copy whole lines more often, so they were
affected less.

**Fix.** `fileio/patcher.go`:

- Phase 0: if `old_string` occurs exactly once as written, replace exactly that
  text. Line-based phases run only when the exact text is absent or ambiguous.
- Phase 5 keeps the text outside the match on both edge lines
  (`spliceFragment`). If a middle line matches only part of its content line,
  the edit is refused and nothing changes.

The failure was reproduced again while this page was written: an edit of
`docs/README.md` whose `old_string` ended at the link text of the "Workspace
collaboration runtime" entry deleted the link target after it. The running
Crude still had the old patcher.

**Tests.** `fileio/patcher_fragment_test.go`. Two older tests in
`fileio/patcher_test.go` pinned the lost function signature as expected
output. They now expect the signature to stay.

## `agent_wait` without a target (fixed 2026-11-04)

**Failure.** The model called `agent_wait` with only `timeout_seconds` to wait
for its background command. Every such call failed with "agent_wait requires
agent_id, run_id, or artifact_id".

**Cause.** The schema had no required field, and the description did not say
what an untargeted wait means. The model's intent (wait until something
addressed to me arrives) was valid, but the runtime rejected it.

**Fix.** An untargeted wait now waits for the next event addressed to the
caller, including a background command outcome
(`session/collaboration_wait.go`). The tool description says so.

**Test.** `TestCollaborationWaitWithoutTargetWakesOnCommandOutcome` in
`session/collaboration_wait_delivery_test.go`.

## Fixed-length `wait` while background work runs (prompt rule; open)

**Failure.** The model used `wait` for 30 to 240 seconds while it had
background commands. Each wait blocked the run for its full length, even when
the command finished sooner.

**Cause.** `wait` is a plain timer. The tool description and the system prompt
already say not to wait for background commands; this model still did.

**State.** No code change. Use `agent_wait` with no target to wait for a
command outcome; it ends as soon as the outcome arrives. If the misuse
continues, end `wait` early when an event addressed to the caller arrives.

## Calls to tools that do not exist (hint added 2026-11-04)

**Failure.** The model called tools that were not in its tool list:
`bash_placeholder`, `bulk_bash_placeholder`, `bash_unavailable_check`. It also
called `jewel_grep_files` for a namespaced grep tool, without the prefix. The
only result was "tool … is not available to this agent".

**Cause.** The tool names offered to the model had a namespace prefix. The
model sometimes dropped or changed the prefix, and sometimes invented a name.

**Fix.** When the rejected name ends with the name of an available tool, the
model-only hint names that tool (`closestToolHint` in `agent/tool_exec.go`). The runtime
never runs a different tool from the one called. Invented names get no hint.

**Test.** `TestUnavailableToolNamesTheClosestTool` in
`agent/tool_hint_test.go`.

## Repair hints leaked through real edit failures (fixed 2026-11-09)

**Failure.** Native errors contained both trailing “Hint:” text and uppercase
patcher advice. Presentation-only string filtering missed some producers and
did not prove live/resumed parity.

**Fix.** Patcher errors contain factual diagnostics only. Native tool results
return model advice separately as `AgentHint`; provider composition retains it
without changing ordinary tool-result roles. Typed outcomes and display evidence
are preserved through live events, checkpoints and SQLite display snapshots.

**Tests.** `TestRealFileFailuresKeepHintsPrivateAndMatchAfterResume` runs real
failed edit/read tools and a native parallel patch/read batch through Session,
App ingress and SQLite hydration at 80 and 160 columns, closed and expanded.
`TestLiteralHintTextInEvidenceRemainsReadable` prevents removing actual source
text merely because it contains “Hint:”.
The opt-in capture journey checks each last-presented shell surface before the
next transition. A fresh paint helper is not a valid oracle: it can conceal a
missed repaint in the captured UI.
