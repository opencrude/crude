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
- Interactive ConPTY commands explicitly invalidate their initial standard
  handles under `STARTF_USESTDHANDLES`, with inheritance disabled, so redirected
  host streams cannot replace the pseudoconsole's captured console streams.

## Completion delivery and output

- Tracked background commands notify their owning agent when they complete,
  fail or time out on their own (`AsyncCommandManager.SetSettleObserver` →
  durable `command_outcome` collaboration event, admitted at a safe boundary or
  waking the idle agent). Never make agents poll or `wait` for completion.
  Explicit kills, exits already returned inline or by an exec-session call, and
  `RetainSettled` output retention are not reported again.
- Command outcomes carry typed immutable command evidence through the journal,
  live conversation and SQLite display snapshots. Their chat projection shares
  ordinary tool title ink/disclosure and normal background, with a static title
  and no output teaser. Settlement captures the same head/tail byte-bounded
  excerpt as synchronous execution without consuming either model output cursor.
  It survives journal/SQLite hydration even if the agent never reads the job.
  Expansion exposes every retained excerpt row and exact command/exit/time/range
  evidence; omitted bytes are explicit, not invented empty output.
  Never paint model-facing completion guidance as an oversized peer-message card
  or recover status by parsing its prose.
- Every session publishes one stable session-owned cross-platform
  `<temporary_directory>` and exports it as `CRUDE_TEMP_DIR` to commands. Use it
  for noisy logs and transient artifacts. `read_async_output` returns bounded
  raw-byte pages (32 KiB default, 64 KiB maximum) with an explicit next/total
  cursor; large completed synchronous output is a bounded head/tail excerpt plus
  a retained handle. Never restore an unbounded model-visible output read.
- `types/command.Result` is the shared command-tool result envelope, consumed by
  one tool-layer decoder for live, batch-child and resumed display. Captured bytes
  are not normalized at process read time: byte cursors address raw bytes, while
  display consumes ANSI sequences and projects progress/whitespace safely. Binary
  pages retain exact base64 bytes on the wire and render as labelled hex evidence.
  Valid UTF-8 remains text, including control-heavy output. The collaboration
  journal budgets JSON escape expansion at publication, without changing command
  output or its model-facing representation.
  Retained handles address the current process manager only; after a core restart,
  the journalled bounded excerpts remain, but omitted bytes are not recoverable
  through a former live handle. No unbounded log is silently persisted.

## Output boundary decision (2026-10-20)

Capture owns raw bytes and byte cursors; tool results own explicit process and
retrieval facts; the journal owns serialized storage bounds; the TUI owns safe,
themed text and reversible disclosure. UI expansion never consumes a model cursor
or changes provider messages. Do not encode valid UTF-8 merely to fit a storage
limit, infer success from printed text, or normalize captured output for painting.

Primary-source comparisons do not establish one universal result format:

- [Codex](https://github.com/openai/codex/blob/e18a20d6eb6f5ede32a69fb56e3fe276d5e9d914/codex-rs/core/src/tools/context.rs)
  sends ordinary exec responses as a metadata header plus truncated text; its
  code-mode result is structured JSON.
- [OpenCode](https://github.com/anomalyco/opencode/blob/d30e990c69efd8a954413a724b35c5a27462f320/packages/core/src/tool/plugin/shell.ts)
  separates structured shell facts from text content and automatic background
  completion notices.
- [Gemini CLI](https://github.com/google-gemini/gemini-cli/blob/76e4d7c046c7dab5727787da378b9c06bf0948cf/packages/core/src/tools/shell.ts)
  separates `llmContent` from `returnDisplay`; its process service also has an
  ANSI/terminal projection. [pi](https://github.com/badlogic/pi-mono/blob/1d01b3b1f8de2e67a3a1c1694b14c984e4edd156/packages/coding-agent/src/core/tools/bash.ts)
  returns bounded text and retains full oversized output in a file.

Sixty controlled real-provider trials compared the previous JSON result,
UTF-8-preserving JSON and a literal-text wrapper: two models × three formats ×
five cases × two repeats. Cases covered failure versus command-printed JSON,
control-heavy text, status-only output, paging and mixed calls. DeepSeek
`deepseek/deepseek-v4.1-flash` passed 8/10 with the previous encoding (both
control-heavy trials misread `quota` as `quot;`), and 10/10 in each text-preserving
variant. `gpt-6.1-sol` passed 10/10 in all three. This small diagnostic experiment
supports removing the unnecessary encoding, not a general model-quality claim
or a format rewrite. The working JSON envelope and execution contracts remain.

Real-process before/after acceptance used separate temporary profiles and the
same deterministic provider schedule: 12 command effects, seven provider requests,
and 11 checks covering ordinary success/failure/empty/binary output, native
truncation and omitted-byte reads, provider multi-calls, mixed parallel batches,
pipe/PTY input, async status/pages, kill/timeout, missing owners, 30-second
promotion, automatic completion and passive SQLite resume without replay. Both
builds passed; the intended backend difference was control-heavy UTF-8 remaining
text. Eight additional real-model trials replayed exact ordinary/failure/native
results captured from those product runs: both models answered every changed-build
trial correctly (four total); the baseline had three correct answers and one
empty DeepSeek response. These small samples are not a latency or quality guarantee.
Desktop and constrained sequential captures separately exercised title-only rows,
real-click disclosure, hydration and dark/light themed body ink.
A separate real-shell Cancel Agent journey stopped concurrent ordinary/native
commands, verified both partial outputs in the next provider request, and ran a
follow-up command without replaying either stopped effect.
