# Process crash reports

> Status: Current — synchronous process-local reports for the TUI and core.

## Location and contents

Each normal interactive shell and `--core` process prepares its own private
report under `buildinfo.Home()/buildinfo.DataDir()/crashes/` before application
startup. With the default profile this is `~/.crude/crashes/`.
`CRUDE_HOME` and beta builds use their own data root. An SSH or direct core
writes on the core host; the TUI writes on the client host.

Files are named `tui-<UTC time>-<PID>-<nonce>.log` and
`core-<UTC time>-<PID>-<nonce>.log`. They use exclusive creation and mode 0600
in a mode-0700 directory. Concurrent shells do not overwrite reports.
Headers record process role, start time, PID, native build hash, source cohort
and Go/OS/architecture. Arguments, environment, credentials, session text and
editor contents are not collected. A panic's own message and stack can still
contain sensitive information; review a report before sharing it.

The file opens with `O_SYNC`; report writes bypass ordinary logging, queues and
UI/core actors. A recovered panic is also explicitly synced before cleanup.
The crash file remains available even if terminal restoration stalls.
Clean exit removes the header-only file. Existing crash reports are never
overwritten or automatically pruned. Expected errors, user cancellation and
recoverable agent/tool failures remain ordinary errors, not process crashes.

## Fatal output and terminal restoration

`runtime/debug.SetCrashOutput` directs unhandled panics in any goroutine and
Go runtime fatal output to the prepared file. All goroutine tracebacks are
enabled. The entry point's deferred `Report.Finish` records a panic before
rethrowing it and keeps runtime crash output installed during unwinding.
No buffered logger or graceful shutdown is needed to deliver that evidence.

Bubble Tea normally recovers its event-loop and command panics, so runtime
fatal capture alone cannot see them. Its narrow `WithPanicHandler` hook sends
the original value and stack to `crashlog.Recovered` before the framework
starts cancellation or terminal restoration. Restoration and `ErrProgramPanic`
still run; there is no duplicate model/command wrapper or swallowed panic.
The production TUI installs this hook. Crash capture setup failure reports a
startup warning on stderr rather than claiming that reports are enabled.

Read-only CLI helpers, `--version`, MCP stdio and core-stdio relays do not create
TUI crash reports. Their stdout contracts remain unchanged.

## Limits and validation

No in-process handler can produce a new stack after SIGKILL, a power failure,
an OS kill that runs no handler, or inaccessible/full storage. A header-only file
left by abrupt termination proves only that the process started; it does not
identify a failure cause. This is crash evidence, not session-data recovery or
a guarantee against hardware data loss.

`go test ./crashlog -count=1` runs subprocess crashes for both roles: unwinding
panic, unrelated worker panic, unrecoverable runtime fatal error, recovered TUI
Update and command panics, and exit before cleanup. It checks actual disk bytes,
private permissions and clean-run cleanup. Race runs keep each installation in
its own process. Native Windows/macOS execution needs separate platform evidence.
`app.TestCoreFatalCrashReportAtCLIEntry` additionally starts a disposable core
through the production CLI and verifies a real SIGQUIT report on Unix.
It targets that exact child process, never the user's attached core.
