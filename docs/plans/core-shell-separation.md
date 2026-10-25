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
   The 2026-10-24 lifecycle/attachment fixes also need these native checks:
   - Build two schema/cohort variants with isolated profiles. In both directions,
     attempt foreign-build stop with a shell attached and with active work; verify
     named refusal, unchanged owner/data and uninterrupted work. Finish/detach,
     hand over the retained idle session, then stop the replacement from the
     other build. Do not use force to satisfy the graceful-stop check.
   - Capture the shared attachment window at desktop and constrained sizes with
     `scripts/tuishot.ps1`: editable composer text (Save/Cancel), posted text and
     image, cold historical content, loading/error (Close), and resize/dismiss.
     Verify a full-width footer rule and no body/action overlap. Reference reads
     must not reopen a closed or replaced session/pane.
   - Use the file-tree **Reveal in Explorer** action on files and directories,
     including spaces/non-ASCII names. Verify the original same-host path and a
     labelled client-local preview for a foreign host; never send a foreign path
     to the shell's Explorer. Close only the test-owned windows/processes.
2. **Distinct-host SSH:** repeat file/editor/search/rename/save/media/client-cache
   and reconnect journeys with genuinely separate filesystems and strict host-key
   rejection. Genuine loopback OpenSSH is useful but not this gate.
3. **Comparative resource acceptance:** compare the measured twenty-workspace/
   multi-shell sample with a declared prior-architecture baseline. Current RSS,
   CPU and FD measurements establish observed costs, not improved efficiency.
   Do not infer a smaller footprint from ownership consolidation or loosen gates.

## Evidence ledger

### Lifecycle and attachment regressions (2026-10-24)

The four reported defects are implemented; current contracts are in
[core lifecycle](../reference/core-client-protocol.md#trust-secrets-and-platform-limits),
[attachment interaction](../reference/text-input.md#composer),
[viewer footer](../reference/control-usage.md#buttons) and
[file tree interaction](../reference/interaction-matrix.md#file-tree-context).
Linux two-schema/cohort CLI checks passed in both directions using isolated
profiles, a retained real backend and a loopback-provider turn. Foreign stop
refused active work and attached shells without changing the owner; idle
handover and foreign explicit stop succeeded. Focused lifecycle/socket races and
repeated startup checks also passed, including atomic private socket publication.

Sequential native Alacritty captures at 160x40 and 80x24 verified composer versus
posted read-only text, original image pixels, cold SQLite historical identity,
the ruled reserved footer and the tree desktop action. Rendered-click regression
tests cover exact IDs/spans, duplicate/literal and foreign-reference negatives,
drag precedence, file-backed full content and stale ownership. Desktop effect
tests capture real launcher arguments for local and connected paths; they do not
establish native Explorer selection. An actual native tree-menu click also opened
Nemo at the exact isolated test folder; the test-owned window was closed and the
pre-existing user window preserved. The capture exited cleanly; its first cleanup
inventory raced an X11 BadWindow after closing the test window, so a separate
successful inventory established cleanup. Native Windows checks above remain open.

The connected regression submits text and image through the real composer/core
admission path, attaches a second viewer and opens both retained transcript links
without another model request or losing the peer draft. Bulk metadata strips
Content, so the client validates its complete-entity revision rather than replacing
canonical reference metadata with the transfer projection. Local-only viewer
fixtures did not establish that boundary; the connected regression guards it.

The exhaustive race run also exposed an existing LSP teardown ordering defect:
`Close` could publish `closed` before retiring servers, or retirement could arrive
after the worker's last idle reconciliation but before its exit check. The owner
now publishes terminal admission and retirement together and drains that state
before exit. `TestLSPCloseJoinsServersAcrossWorkerWake` fails the original source
in all five baseline repetitions; the repaired owner and twenty repeated real
gopls headless journeys pass under the race detector without surviving servers.

### Earlier separation acceptance

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
