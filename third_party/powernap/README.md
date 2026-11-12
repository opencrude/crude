# Powernap local fork

This replacement module retains Crude's LSP transport hooks, UTF-16 position
contract, document-save capability handling, and owned subprocess lifetime.

## Launch-bound configuration

`NewConfiguration` builds one detached, immutable settings table before process
construction. `ClientConfig.Configuration` must match the client's root URI.
Server `workspace/configuration` requests select the longest matching directory
scope and preserve item order and null results. Each scope supplies a complete
effective object, not a sparse overlay. Unscoped requests use the global object;
invalid or outside-root scopes return null instead of another project's settings.
URI host, escaping and drive-letter syntax are normalized without filesystem I/O;
other path case is not guessed to be equivalent.

Tables admit at most 128 directory scopes and 1 MiB of encoded settings/URI data.
Requests admit at most 128 items and 64 KiB of encoded parameters; responses are
bounded to 1 MiB and fail atomically if they exceed it. These are encoded-data
bounds, not a hard heap limit or a bound on arbitrary caller `MarshalJSON` code.
The table validates and detaches settings at preparation; replies own their
encoded bytes. Root/configuration replacement requires a new launch-bound table.
This transport contract does not discover modules, infer build configurations,
authorize execution, or establish semantic coverage.

## Complete analyzer environment

`ClientConfig.Environment` is a prepared `ProcessEnvironment`, not an ambient
overlay. Its zero value is refused before executable or transport construction.
`NewProcessEnvironment(nil)` prepares an explicitly empty table; `Env()` returns
a detached, nonnil list even when empty. Formatting and JSON redact its contents.
No environment fingerprint is published. The constructor does no filesystem
I/O and admits at most 256 entries, 128 KiB of encoded `KEY=value` data, 256-byte
ASCII identifier keys and 32-KiB UTF-8, NUL-free values. These encoded bounds are
not OS launchability or heap guarantees.

An explicit PATH admits at most 32767 bytes of absolute native directories of at
most 4096 bytes each, without empty/relative entries, control characters or dot
components. The byte bound also bounds the number of entries; oversized PATH
values fail rather than truncate.
Exact duplicates preserve the first position. Immediately before construction,
the existing subprocess owner resolves only this PATH or a native absolute
executable, then passes that absolute result and the complete environment to
`os/exec`. There is no ambient PATH/PATHEXT lookup, implicit shell or current
directory fallback. Windows resolves only native EXE/COM candidates and refuses
BAT/CMD interpretation; an explicit absolute SystemRoot is required so Go cannot
inject an ambient one. This requirement is not a claim of native Windows
acceptance. Missing/invalid executable lookup retains the missing-command error
classification without echoing search directories or environment values.

Lookup observes a regular file and, on Unix, execute mode bits. It follows
symlinks and does not attest immutable executable bytes, effective access,
noexec/ACL state, authorization, containment or freedom from path races.
The caller owns positive OS/toolchain inheritance and explicit launch choices;
the transport never reads `os.Environ` to fill missing data. Trusted analyzers
still have native filesystem/network access and may discover credentials there.
Environment minimization is not a credential sandbox or a change to MCP.

## Explicit signature help

The standard `textDocument/signatureHelp` request is admitted only when the
initialized server advertises its static provider capability. Client capabilities
describe plaintext/Markdown documentation, UTF-16 parameter-label offsets and
signature-level active parameters. Dynamic registration, automatic trigger context
and the no-active-parameter extension are not advertised. The raw response
preserves optional index presence and both label representations for bounded
consumer validation; it does not certify semantic quality or project context.

## Raw manual code-action transport

`RequestCodeActions` and `ResolveCodeAction` preserve original JSON objects,
including opaque numeric/data leaves. Static initialized provider capability is
required; unsupported/malformed capabilities never trigger a probe. Requests are
explicit `triggerKind=1` with original diagnostics (or `[]`), without an automatic
trigger or kind filter. Ordered positions are bounded to nonnegative int32 range;
the caller still owns source-relative UTF-16 boundaries and diagnostic provenance.

Bounds are 256 original diagnostics and 256 KiB aggregate diagnostic bytes,
64 KiB per resolve object and 1 MiB per raw action-list response. These encoded
data bounds do not bound the external connection decoder's initial allocation.
The transport executes only commands that the initialized server advertised and
accepts create-file workspace edits requested by that server. `workspace/applyEdit`
is answered with `applied: false`; the adapter retains the original edit for the
canonical coordinator's review instead of mutating files. Initialize advertises
literal/data/disabled/preferred support, edit resolution, `workspace/applyEdit`,
create-file resource operations and `workspace/executeCommand`.
`CodeActionSupport` and `CommandSupport` observe only current static provider
capabilities. The adapter and canonical coordinator qualify original
diagnostic/action evidence; the transport alone grants no mutation authority.
Dynamic registration, rename/delete resource operations, annotations and
arbitrary shell execution remain unsupported.

Both module consumers use the pinned local
[`jsonrpc2` request decoder](../jsonrpc2/README.md). This authority preserves
detached original notification params before Powernap dispatch: duplicate nested
members, malformed string evidence and opaque numeric spelling must reach the
consumer validator rather than being erased by interface decode/re-marshaling.
The fork rejects ambiguous top-level envelopes atomically. It does not bound
initial frame allocation or make provider data trusted.

## Range formatting

`RequestRangeFormatting` requires initialized static range-format capability and
sends standard `textDocument/rangeFormatting` with exact UTF-16 range/options.
Initialization advertises no dynamic registration. Unsupported/malformed capability
never sends a probe or substitutes whole-document formatting. The 1 MiB response
bound and cancellation settlement match other passive raw replies. Strict text-edit
validation, source/provider provenance and mutation authority stay with the caller.
Transport tests cover boolean/options capabilities, disabled/missing providers,
exact parameters, invalid widths and cancellation after a reply.

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
Before assigning the first process, the owner associates an I/O completion port
with the job. Shutdown terminates that exact job, joins the leader, and waits
for the job's `ACTIVE_PROCESS_ZERO` completion message before releasing handles.
An application-defined exit code distinguishes our job termination from ordinary
server failures. Completion waiting is bounded to five seconds; the outer join
is bounded to six seconds. A timeout fails closed.

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
