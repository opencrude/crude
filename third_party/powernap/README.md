# Powernap local fork

This replacement module retains Crude's LSP transport hooks, UTF-16 position
contract, document-save capability handling, and owned subprocess lifetime.

## Process and transport lifetime

`Client.Kill() error` cancels the client and joins connection teardown.
`Client.Exit()` sends the exit notification and joins the same teardown even if
notification fails. Process-pipe, stream, and connection closers cache the original
outcome for repeated and concurrent callers, including cancellation and reader EOF
that occur before an explicit stop. Unexpected pipe, kill, observation, reap,
containment, and handle-close errors remain observable through `errors.Is` and
`errors.As`. Expected already-closed pipes and the exit status caused by our own
terminal signal are not shutdown failures.

On Linux, `waitid(WNOWAIT)` retries `EINTR` and leaves the leader unreaped until
the process group has received its terminal signal. macOS and the BSDs use
`kqueue` process-exit observation without reaping; event and descriptor-close
errors are retained. A failed or unsupported observation is not evidence of exit:
it forbids later negative-PID signals and reports unproven group containment.
`ESRCH` from a group kill is expected absence. Only the direct child can be reaped
by this owner. Unix process groups are not a sandbox: a descendant that changes
group or session can escape, and orphan zombies are reaped by their adopter.
A nil shutdown error proves accepted group signaling and the leader's joined
wait, not universal descendant absence or malicious-process containment.

On Windows, creation is suspended until the exact retained process handle has
been assigned to a retained kill-on-close Job Object. The process is resumed only
after assignment succeeds. Failed setup kills and joins the suspended process.
Shutdown terminates that exact job, joins the leader, and queries active job
membership to zero before releasing its handles. An application-defined exit code
distinguishes our job termination from ordinary server failures. Job-empty polling
is bounded to five seconds; the outer join is bounded to six seconds.

The Unix join is bounded to five seconds. A timeout or failed containment/reap
operation is an error, not a successful join. A blocked reaper retains its exact
owner until it can finish; the cached failure is not later rewritten as success.
Failed launches whose cleanup cannot be verified include
`lsp.ErrProcessCleanupUnverified`. A nil client with that error is not evidence
that the attempted process is absent. The caller must retain its failed lifetime
reservation rather than admit a replacement on the strength of the failed start.
An ordinary missing executable or a setup failure with verified rollback does
not carry that marker.

## Focused checks

Run the nested module tests separately from the root module:

```sh
GOMAXPROCS=2 go test ./pkg/lsp ./pkg/transport
GOMAXPROCS=2 go test -race ./pkg/lsp ./pkg/transport
```

The Linux tests cover owned descendant shutdown, unsupported/lost wait identity,
group-signal and reap errors, expected absence, pipe errors, and repeated/concurrent
public teardown. Windows tests cover exact-job descendants, bounded job observation,
termination/query/handle failures, and failed-launch uncertainty. Cross-compiling
Windows or Darwin tests is not native-platform acceptance.
