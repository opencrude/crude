# Checkpointed Core Restart

> Status: Initial local implementation under integrated acceptance. The running
> pre-feature core cannot acquire these entry points without a bootstrap upgrade.
> Native Windows/macOS and remote-shell restart acceptance remain open.

## Ownership

A delay is an earliest start, not proof that work stopped. The application in the
original terminal owns executable staging and foreground process handoff. The core
authenticates the exact pending semantic operation. The session owns its durable
goal reservation and single-use continuation. The TUI projects public operation
state and releases only its own shell after readiness.

This is not `sleep; kill`, an escaped agent background command, a general service
manager, or a way to confirm goal-resume dialogs. Ordinary cold session opens
remain passive. Unbound autonomous turns, child agents, external MCP clients and
SSH shells cannot schedule a local original-terminal handoff.

## Entry points

Discover exact descriptors with `crude_commands` before using them:

| Command | Effect | Result |
|---|---|---|
| `core.restart` | Execute | Stage the specified absolute executable and SHA-256, and reserve the exact active goal. A committed result means scheduled, not restarted. |
| `core.restart.inspect` | Read | Observe only the named public operation state. No readiness or lifecycle effect. |
| `core.restart.cancel` | Write | Cancel only the named operation through its private application owner. |

Schedule takes `executable`, `sha256`, `goal_id` and `delay_seconds` from 0 to
300. Mutations require the inspected shell revision/open epoch and exact target
session. The original shell and native root/run attribution are retained through
the existing permission and receipt owners. A new profile, focused peer or
replacement attachment is never an implicit target.

The application validates an immutable staged image and invokes its bounded,
read-only `--core-handover-identity` route without inherited credentials. Identity
includes full executable SHA-256, build cohort, schema and OS/architecture. A
same-cohort connection alone does not prove the new executable is running.

The private `--core-handover /absolute/manifest.json` startup route is only for
the exact already-admitted foreground handoff. It cannot select another profile,
workspace, session, model, account or authority. It refuses an unexpected owner
instead of using ordinary startup's mismatch handover. It is not an alternative
agent control channel or an initial bootstrap workaround.

## Natural checkpoint and continuation

The current root turn finishes normally. Reservation inhibits its next automatic
goal turn without cancelling accepted output, tools or maintenance. Readiness
requires the exact successful terminal context/run receipt and unchanged goal,
settings, mode, input and execution ownership. Existing joined maintenance may
finish before readiness; its work is not ignored or cancelled.

Human steering/input, goal changes, Plan/settings changes, loop or execution
selection changes invalidate the old reservation. Paused, blocked, complete,
budget-limited, recovered or replaced work is never silently resumed. A fork
drops live continuation permits.

Only an unexpired, exact Ready reservation can satisfy the session's otherwise
unchanged active-goal idle protection. Final core shutdown still reserves saved
storage, actors, sessions, documents, terminal/resource lanes and work queues.
Peer shells/work, open resource leases and unknown inspection block it. The
handoff never force-stops neighbors.

Closing the shell may leave ordered resource releases finishing in their joined
lane. The scheduled predecessor helper makes at most eight bounded-backoff
attempts only after an explicit refused/no-effect exit. Every attempt retains
the exact predecessor; unknown or accepted outcomes are never retried.
The internal helper uses dedicated exit status 75 only for a decoded refusal.
Go panic status 2, generic failures and lost lifecycle replies are uncertain,
one-attempt outcomes.

The TUI acknowledges scheduling before polling and rechecks dirty editors,
save/format/history, paste and other owned I/O before exit. `TUI.Wait` includes
draft flushing and attachment teardown. Only a successful teardown publishes the
ready operation ID to the application; all original clients/profile locks close
before the foreground handoff runs.

The exact retained predecessor helper receives its expected owner generation.
After accepted shutdown, lifetime-lock settlement precedes successor launch in
the same terminal. Successor startup validates both its own image and the new
core image/generation, passively opens the exact session, then consumes only the
private permit. The session atomically marks it consumed and releases only its
own handover wait. The next turn uses the normal scheduler with the newly
authenticated original-profile shell target. It does not reuse old run
credentials, call broad `ResumeGoal`, or submit synthetic human input.

Fresh consumption also reserves the root turn/compaction gate and checks the
history-restore admission owner before sampling its final context. Zero visible
activity is not proof that another owner has not already reserved that boundary.
Re-observing an already consumed permit remains read-only during its fresh turn.

The private successor dialer pins one exact owner generation across initial
navigation, workspace attachment and reconnect. Later absence does not relaunch
it, and a different generation is refused even if its image bytes match. A failed
exact-root attachment returns an error, never the ordinary project repair
chooser. Continuation rechecks the owner's same captured generation.

## Receipts, cancellation and failures

Private, no-replace manifests and sequenced immutable receipts live below the
selected `CRUDE_HOME` data directory. Nonces and their hashes are never public
operation output. Completed evidence is retained; there is no liveness-guessed
image pruning.

Identical readiness polls do not append new receipts. Only observed state
transitions and effect boundaries are journalled; quarter-second presentation
polls cannot grow the durable history on their own.

An uncertain reserve, stop, launch, cancellation or continuation is observed
under its original identity, not replayed. A helper exit status, socket presence
or successful schedule is not proof of activation. Foreground completion must
also observe a consumed continuation receipt.

Before stop, cancellation releases only an unchanged reservation. After stop,
cancellation cannot promise that nothing happened. Failed or ambiguous
successors retain their receipts and data for explicit repair. There is no
automatic rollback to an older executable/data format.

Consumption is at most once. A crash after consuming but before admitting the
fresh turn remains passive on cold reopen and needs explicit recovery; it does
not replay the old turn. Existing general user pause/resume and run recovery
remain separate authorities.

## Verification boundaries

Focused application, session/database and rendered-App tests cover immutable
images, exact owner refusal, descriptor/lifetime fences, natural settlement,
maintenance exclusion, source/destination identity, private receipts, one-shot
consumption and newer user-control preservation. The integrated core regression
is `TestCoreRestartNaturalCheckpointColdSuccessorAndOneContinuation` in
`core/core_restart_test.go`. CLI successor admission is tested in
`app/core_restart_startup_test.go`.

### Linux native observations, 2026-10-30

Sequential `scripts/tuishot.sh` runs at 160x40 and 80x24 drove a disposable local
profile through the real composer, native agent tools, permission reviewer,
semantic shell control, natural turn completion and foreground successor. The
provider/reviewer was an explicitly labelled deterministic fixture, not a live
model, human consent or a durable tool grant.

The runs observed different core owner generations and the same goal identity
after activation. One constrained run upgraded between distinct executable
digests/cohorts. A final constrained run preserved an unsent composer draft,
paused the resumed goal as requested, then quit through both levels of the
ordinary quit ladder. Its parent also exited zero; the final immutable receipt
was `successor_exited` with phase `consumed`. Ten transition/effect receipts
covered the run. Source checkpoint, successor text and the retained draft were
visually inspected, not inferred from the model's success sentence.

Earlier capture windows were closed without using the full quit ladder, so they
do not attest clean foreground exit. An unsupported `await-exit:0` capture step
was rejected as exit evidence; the final run used the script's supported
`quit:ctrl+q` and verified `CRUDE-EXITED rc=0`. All capture windows and disposable
cores were closed; cleanup of an abruptly detached fixture is not evidence of
non-forced handover.

These observations are not physical-device calibration, native Windows/macOS or
remote-shell acceptance, a live-model guarantee, or evidence that the current
pre-feature developer core was upgraded. Keep the first bootstrap boundary
explicit. Use the repository's sequential
[visual capture workflow](../guides/validation.md) for additional native evidence.
