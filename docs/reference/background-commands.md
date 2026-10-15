# Background Commands

> Status: Current reference — tracked command lifetime, termination, completion delivery and bounded output.

## Lifetime and termination

- The background-jobs session deck opens passively; each running row has a
  native Terminate button. Explicit button interaction retains keyboard focus;
  Esc returns it to the composer. Finished rows have no termination target.
- Termination captures the exact owning backend and the observed
  `fileio.AsyncCmdRef` (handle, PID, start time), never the active session or a
  process-name search. Replaced, missing and settled references fail closed.
  Stopping one command must leave neighboring commands and the session usable.
- Command-tree lifetime owns all signals. Unix observes exit without reaping,
  cleans its process group while the leader still pins the identity, then reaps;
  Windows terminates its retained Job Object, never `taskkill /PID`. Completion,
  explicit stop and session shutdown cannot signal a settled/recycled identity.
- Background-job columns are content-sized with terminal-width helpers.
  Optional PID/exit telemetry yields before handles are shortened; elapsed labels
  use whole seconds (`2m 05s`, `1h 03m 09s`), never fractional seconds.
- Noninteractive Windows commands use `CREATE_NO_WINDOW` plus `HideWindow`;
  package managers such as WinGet must never attach to or paint into Crude's host
  console. Stdout/stderr is visible only through the captured tool/job result.

## Completion delivery and output

- Tracked background commands notify their owning agent when they complete,
  fail or time out on their own (`AsyncCommandManager.SetSettleObserver` →
  durable `command_outcome` collaboration event, admitted at a safe boundary or
  waking the idle agent). Never make agents poll or `wait` for completion.
  Explicit kills, exits already returned inline or by an exec-session call, and
  `RetainSettled` output retention are not reported again.
- Command outcomes carry typed immutable command evidence through the journal,
  live conversation and SQLite display snapshots. Their chat projection shares
  ordinary tool title ink/disclosure and normal background, defaults to one static
  row, and reveals exact command/exit/time/output evidence only on expansion.
  Never paint model-facing completion guidance as an oversized peer-message card
  or recover status by parsing its prose.
- Every session publishes one stable session-owned cross-platform
  `<temporary_directory>` and exports it as `CRUDE_TEMP_DIR` to commands. Use it
  for noisy logs and transient artifacts. `read_async_output` returns bounded
  raw-byte pages (32 KiB default, 64 KiB maximum) with an explicit next/total
  cursor; large completed synchronous output is a bounded head/tail excerpt plus
  a retained handle. Never restore an unbounded model-visible output read.
