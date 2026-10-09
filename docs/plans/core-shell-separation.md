# Core and Shell Separation Acceptance

> Status: IN PROGRESS. Linux implementation and consolidated gates pass; native Windows, distinct-host acceptance and comparative resource baselines remain actionable.

## Implemented contract (not future work)

Normal CLI shells attach through the user core, with independent rendering,
focus, tabs, drafts, selection and geometry. Core actors reduce semantic ingress
once and retain sessions/resources across detach. Shared documents universally
serve connected Open/Sync/Edit/Close/Save/Format with attachment epochs, author
undo, optimistic pending edits, exact receipts and reconnect reopening. Accepted
unsaved text/savepoints/history restore from workspace SQLite; append deltas avoid
whole-state checkpoint publication per ordinary edit. Stable IDs survive rename,
durable batch intents preserve dirty text and protect conflicted delete. Source
watching and file operations are core-owned. Private unresolved drafts have native
Keep/Compare/Use shared resolution with durable backup and exact tombstone.

SSH CLI selection, fixed strict stdio bridge, typed write-only invocation selectors
and bounded chunked response transfer are implemented. Incoming oversized mutation
upload is not implemented: 9 MiB mutations fail before enqueue. Capability preview
cannot publish defaults without an actual action and exact approval/preimage.

Current contracts: [core client protocol](../reference/core-client-protocol.md),
[shell port](../reference/shell-port.md), [settings](../reference/settings.md),
[review workflows](../guides/core-shell-workflows.md). Keep architecture truth in
those references rather than another historical migration checklist.

## Outstanding acceptance work

1. **Native Windows:** execute native pipe ACL/ownership/cancellation, ConPTY,
   independent shells/shared editor, recovery and desktop-effect journeys using
   the review guide. Linux cross-build evidence is not native acceptance.
2. **Distinct-host SSH:** repeat file/editor/search/rename/save/media/client-cache
   and reconnect journeys with genuinely separate filesystems and strict host-key
   rejection. Genuine loopback OpenSSH is useful but not this gate.
3. **Comparative resource acceptance:** compare the measured twenty-workspace/
   multi-shell sample with a declared prior-architecture baseline. Current RSS,
   CPU and FD measurements establish observed costs, not improved efficiency.
   Do not infer a smaller footprint from ownership consolidation or loosen gates.

## Evidence ledger

Parent checkpoint: ten real CLI tmux shells shared input/output, origin kill with
one provider request, peer Yes with two posts, peer Escape Stop with one post and
accepted input retained, zero-client server completion/passive attach with one
post. Genuine nonroot loopback SSH rejected unknown/replaced keys and passed a
two-CLI stream/origin-kill journey. Final editor all six real two-CLI checks passed,
including shared unsaved text/author undo/save, accepted origin-kill survival,
zero-client core-restart restore and visible external dirty conflict with safe Esc.
Parent read final result/cleanup JSON; the earlier conflict failure is resolved.
Final full tests passed 94 packages. Build, tagged remote vet, complete
core/client/app/remote races, focused editor/private-recovery races, lint (zero),
docs/design and benchmark gates pass. Windows/amd64 no-CGO cross-build passes,
not native execution. The actual 16 MiB connected Open/Sync/giant receipt journey
passes with race instrumentation under independently bounded request deadlines;
read progress renews its inactivity lease and immutable output encodes once.
Final ten-shell and genuine loopback-SSH binary reruns pass, with owned cleanup.
Source-focused normal/format/receipt/reconnect tests are not every release gate.

Initial resource sample: core RSS 138,440,704 B, 222 FDs, roughly 10% CPU in a
2-second sample; ten shells aggregated 740,962,304 B RSS and roughly 10–13% CPU
each. See [performance baselines](../reference/performance-baselines.md) for
measurement limits. Native runtime is unavailable in the current Linux environment.
Final sample: 139,075,584 B core RSS / 222 FDs / 9% of one CPU over two seconds;
ten shells aggregate 730,644,480 B RSS, approximately 9–11.5% CPU each. No
prior-architecture baseline or settled-idle efficiency claim is made. Final
durable-edit metrics and their independent repeated samples are recorded in the
performance reference. Dedicated visual windows at 160×40 and 80×24 were closed.

Delete this active plan only when each genuine remaining gate is resolved and
current references/guide retain the result and explicit limits. Do not delete it
merely because the implementation exists or a cross-build succeeds.
