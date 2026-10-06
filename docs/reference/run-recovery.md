# Unfinished run recovery

> Status: Current reference — supervised attempts, durable unfinished work, and bounded cooldowns.

## Contract

An unsuccessful attempt is not a completed task. Normal completion, an explicit
user stop/termination, or an owning parent lifecycle decision can settle work.
Transport exhaustion, request deadlines, unexpected returns, truncation, loop
guards, missing child reports, configuration faults and internal failures must
not silently erase ownership or manufacture a successful finish.

Crude is resilient, not literally indestructible. It cannot repair missing
credentials, obtain permission, invent a certificate trust decision, make broken
storage writable, or guarantee progress from an unreachable provider. It retains
unfinished work and makes those conditions actionable instead of hammering them.
Uncertain side effects require inspection rather than replay. Process death,
hardware failure, externally deleted/corrupted data and uncooperative remote
writers remain real limits.

## One supervisor, separate budgets

`agent.Supervisor` owns the root/child recovery lifecycle. `session/` supplies
typed failure facts, exact pending input, generation-fenced wake signals and
transactional `sessiondb` checkpoints. `types/recovery.Status` is the neutral
projection shared by runtime, chat and hydration. `prompt/recovery.go` owns the
code-authored continuation instructions; these are runtime data/control, not a
new human message or a replacement for permission policy.

| Failure | Response |
|---|---|
| Retryable network/provider outage or exhausted request-attempt budget | Automatic continuation with a capped cooldown while unfinished work remains owned |
| DNS lookup failure, including “not found” | The task supervisor rechecks automatically with capped pacing; the bounded request-attempt policy remains separate |
| Request deadline, with the owning turn still live | Recover the attempt; never label it “stopped by you” |
| Accepted partial response | Preserve it and append a semantic continuation, never replay the stochastic request |
| Truncated/unfinished tool arguments | Execute none of that response's calls; retain visible text and ask for complete calls |
| Repeated tool loop, output truncation, missing final report | Bounded corrective continuation, then an actionable pause if correction does not work |
| Authentication, billing/quota, invalid configuration/trust/guidance | Needs attention; no unbounded invalid-request loop or silent account/model failover |
| Persistence failure | Keep the owned snapshot/draft, block unsafe advancement, and allow a checked retry |
| Panic or uncertain effects | Preserve safe boundaries and pause for reconciliation; keep the root input service usable |
| Explicit user or parent cancellation | Stop promptly, preserve completed effects/text and record the explicit stop boundary |

Provider request attempts, compaction extraction attempts, permission reviews and
background memory jobs retain their own bounded budgets. Spending one budget
does not settle the unfinished coding task. Valid permission denials are never
retried as technical errors, and recovery never expands tool/file/network grants.
Children have no arbitrary task deadline; parents and users retain lifecycle control.

## Shared cooldown contract

`retry/` is the one authority for automatic cooldown arithmetic and waits:

- **Absolute maximum: five minutes per cooldown**, including server hints.
- Exponential growth saturates before duration/integer overflow.
- Provider retries use bounded jitter; zero/past hints do not remove local pacing.
- `Retry-After`, HTTP-date values and `retry-after-ms` are clamped to the ceiling.
  Thus an advertised 24-hour wait becomes a five-minute cooldown; this explicitly
  takes precedence over a longer server-advised minimum.
- Cancellation is checked before waiting, including a zero-duration wait.
- Configurable provider/account backoff values cannot exceed 300 seconds.

Five minutes is not a total outage deadline, an attempt count, a progress timeout,
or a normal maintenance cadence. A transient outage may require many separate
cooldowns; no individual automatic retry wait grows into hours. The public model
catalog has the same failure ceiling; its successful six-hour refresh cadence is
not shortened. Reviewer/model-job safety budgets remain independent.

## Durability and replay safety

Pending work is checkpointed before admission/effects, with the original input,
exact owner/generation, input-admission flag, archive and active context. Recovery
uses that same current context owner as normal turns and compaction. Accepted
assistant/tool boundaries are retained; transcript/context persistence must succeed
before a natural completion is acknowledged.

Finished parallel siblings are checkpointed while other calls are still running,
including children of the native parallel wrapper. Interrupted wrapper snapshots
retain real leaf results and mark their overall outcome unknown. Tool panics
pause for inspection, including inside parallel batches; not-yet-started serial
siblings do not execute after an uncertain failure. Per-path locks release even
when the tool panics.

Natural completion also waits for the durable turn and collaboration ledgers.
Retrying finalization acknowledges only an already-committed identical terminal
outcome for the exact run/owner/epoch/context; a different stop/outcome fails closed.
Persistence retries do not request another stochastic answer.
An old recovery cancellation fences the owning turn context itself, not merely
the earlier incident lookup. New human/peer turn admission cannot overwrite a
parked task. The composer refuses an ordinary send before consuming that draft;
Retry now/Stop and slash remediation remain available.

No supervisor automatically reruns a historical tool call, relaunches an existing
command, advances an async output cursor, or treats a running process as exit zero.
Already accepted results remain evidence. Opaque commands/MCP effects that may
have happened are not made idempotent by a retry timer or a file-ownership claim.
Permissions, mutation leases and mandatory Local History safeguards still apply.

Resume is passive. Startup marks unfinished execution as interrupted and restores
its safe checkpoint without provider calls, command replay or implicit child
launch. **Continue run** explicitly requests a checked replacement attempt.
Collaboration `RunID`/version/epoch facts remain immutable attempt identities;
an interrupted attempt does not prove its unfinished task was completed.

The accepted checkpoint tail is restored into the ordinary root transcript before
resume is exposed. Missing tool outcomes are explicitly unknown, never reported as
“cancelled by user.” A newer finalized transcript is not replaced with an older
checkpoint, and a mismatching accepted prefix fails closed. Pending inputs retain
their session DB even before input admission. Pending child work returns to Agents
with its accepted transcript and own incident; child mirror incidents do not
appear in the root chat. A prior attempt's final report remains evidence and cannot
satisfy a replacement collaboration run's deliverable.

## Presentation and interaction

One ID-keyed incident row is updated through Recovering, Needs attention,
Recovered, Interrupted and explicitly Cancelled. It defaults to one compact
content row, without the old full-width notice rule or exposed error paragraphs.
Recovering shows a shared-clock blinking red square, a real countdown, a
clickable **(Retry now)** and ordinary disclosure chevron. Retry now shortens the
exact owner's current cooldown; timers continue automatically without `/retry`.
The owner's acknowledged replacement attempt clears the old deadline and changes
the retained row to a quiet, static **Run resumed** boundary. It keeps the complete
diagnostic behind disclosure without a red square, blink or retry target.
That deadline-free owner acknowledgement releases the live activity phase:
subsequent upload, provider wait, reasoning and response events remain authoritative
through ticks and tab changes. The retained incident must not overwrite them with
“Recovering”. The previous live attention issue resolves on acknowledgement;
its notification remains in Notices. Only a cooldown/attention/interruption exposes
Continue/Retry now, never an already-running replacement attempt. Explicit Stop
outranks a pending incident while cancellation is acknowledged.
Resumed is not a successful task finish: its durable checkpoint stays open until
the supervisor commits completion/cancellation, so passive crash recovery still
keeps the unfinished work. A subsequent failure updates the same incident.
Needs attention and interrupted incidents remain static and expose Retry now.
Recovered/cancelled incidents remove the retry target.

Clicking the header/chevron or the existing disclosure shortcut expands the
actual full reason and remediation; dragging uses ordinary selection/copy and
never activates Retry now. Expanded diagnostics use normal foreground ink and
guidance uses muted ink. Header clock changes reuse cached evidence and do not
change row height or rewrap the reason. Credential-shaped diagnostics are
redacted rather than disclosed. The entire remaining diagnostic is available for
selection/copy; it is not arbitrarily cut at 140/240 characters.

The fixed activity strip carries the concise state/countdown, not raw diagnostics.
Needs-attention and interrupted states are static: no fake working animation or
rotating plan pages. Accepted messages, tools and the composer draft remain intact.
Live and hydrated rows use the same typed projection and timestamps.

Explicit entry points are **Continue run** in chat/agent/tab context menus,
**Continue Agent Run** in the palette, and `/retry`. An asynchronous request is
fenced to exact backend owner, incident ID, turn generation and workspace boundary;
it cannot wake another incident or consume a new draft. Stop remains available
while waiting/recovering. Configuration changes can wake an active waiting owner;
they do not silently restart passively recovered history.

Exact per-message timestamps/producing connection facts are retained when already
present in display snapshots. A crash checkpoint that contains only a canonical
tail can supply its boundary timestamp, not reconstruct unrecorded per-token times
or invent missing producing-account metadata. No terminal blink attribute, new
private timer, or optimistic lifecycle state is used for the row.

See [network transport](network.md) for secure remediation of routing/TLS issues,
[chat activity](chat-activity.md) for transcript contracts, and
[workspace collaboration](workspace-collaboration.md) for exact lifecycle controls.
