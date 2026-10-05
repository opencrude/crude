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

## Presentation and interaction

One ID-keyed incident row is updated through Recovering, Needs attention,
Recovered, Interrupted and explicitly Cancelled. Repeated failures do not add a wall of identical red
errors. The title carries severity; wrapped diagnostics use normal foreground
ink, and recovery guidance uses muted ink. Credential-shaped diagnostics are
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

See [network transport](network.md) for secure remediation of routing/TLS issues,
[chat activity](chat-activity.md) for transcript contracts, and
[workspace collaboration](workspace-collaboration.md) for exact lifecycle controls.
