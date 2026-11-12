# Workspace suite GC crash on Windows

> Status: OPEN. One uninstrumented workspace test group crashed. Instrumented
> reruns passed without a source change; neither the cause nor a fix is proven.

## Observed failure

During [editor polish validation](2026-11-11-editor-polish.md), the fourth
exact-coverage workspace group (198 top-level cases) failed after 34.973 seconds
on Windows amd64 with Go 1.25.8, CGO enabled, default GC, and no GOEXPERIMENT or
GOFLAGS. All four viewmodel groups and the first three workspace groups had passed.
The checked-out base was `a852ac40`, with the editor-polish changes uncommitted.

The failure was a runtime fatal, not a failed assertion or the group timeout:

```text
runtime: marked free object in span ..., elemsize=9472 freeindex=0
(bad use of unsafe.Pointer or having race conditions? try -d=checkptr or -race)
fatal error: found pointer to free object
runtime.(*mspan).reportZombies
runtime.(*sweepLocked).sweep
runtime.sweepone
runtime.bgsweep
```

The dumped object's bytes began with the test-run argument and its list of test
names. The active test was
`TestInlayUnsupportedNullAndEmptyAreRetainedPartialEvidence`; its cleanup stack
included `newInlayAcceptanceFixture`, `WorkspaceDB.Close`, `database/sql`, the
SQLite Windows WAL-close path and `windows.CloseHandle`. That is where GC
**detected** the failure; it does not identify the code that made the pointer
invalid. Do not attribute the cause to inlay hints, SQLite, the local native
adapters or the Go runtime from this stack alone.

Original full evidence remains in the initiating session's `CRUDE_TEMP_DIR`:
`remaining-groups.log` (including the exact command and fatal dump). The durable
facts and regeneration command are below; temporary log paths are not a release
artifact or the only description of the defect.

## Reproduction and diagnostic evidence

From the repository root, regenerate the same group using the suite's own
partition function. Do not hand-pick only the test active during GC:

```bash
python - <<'PYREPRO'
import re, subprocess, sys
sys.path.insert(0, 'scripts')
import test_go
listing = subprocess.check_output(
    ['go', 'test', './workspace', '-list', '.'], text=True)
names = [name for name in listing.splitlines()
         if re.fullmatch(r'(?:Test|Example|Fuzz)\w*', name)]
group = test_go.partition(names)[3]
print('Workspace group 4:', len(group), 'cases', flush=True)
test_go.run(['go', 'test', '-p', '2', '-count=1', '-timeout=180s',
             './workspace', '-run', test_go.pattern(group)])
PYREPRO
```

The group changes if test discovery changes. Preserve the failing command's exact
name list when reducing a later reproduction.

- The same 198 cases passed once with `-race -v`, using the documented Windows
  `libghostty_env.ps1` and `race_env.ps1` environment (116.400 seconds).
- They passed once under GC pressure (`GOGC=30`, 52.875 seconds). That command
  used `GODEBUG=checkmark=1`, which is not the Go 1.25.8 option; it did **not**
  enable mark verification. The correct option, confirmed in runtime source, is
  `GODEBUG=gccheckmark=1`. No completed Windows run with that option is recorded
  here; it remains part of the investigation.
- Both diagnostic runs exercised the originally active inlay case. No source,
  dependency, compiler or test-filter change was made between failure and runs.
- Five fresh-process repetitions of the original uninstrumented group passed.
  They are diagnostic observations, not a fix or automatic retry-to-green
  behavior in `scripts/test_go.py`.
- On Linux amd64, Go 1.27.4 passed the full grouped suite and then the current
  fourth workspace group (197 cases, 25.370 seconds) with `GOGC=30` and
  `GODEBUG=gccheckmark=1`. No assertion, group deadline or test runner changed.
  This is Linux diagnostic evidence after a local toolchain update, not a
  reproduction or resolution of the original Windows failure.

## Next investigation and acceptance

Retain the failure as an open validation issue even if a later sweep passes.
First obtain another failure with exact test progress, group membership,
Go/OS/native build information and bounded GC evidence. Minimize the workload
while preserving the long-lived process and earlier operations; a clean isolated
inlay case does not exclude earlier memory corruption or a lifetime error.

Review unsafe/native lifetimes at their actual owners only when evidence points
to them. The pinned adapters' lifetime contracts are in
`third_party/modernc-libc/CRUDE_PATCH.md`,
`third_party/modernc-memory/CRUDE_PATCH.md` and
[native syntax](../reference/native-syntax.md). A race-detector pass alone does
not prove correct native pointer lifetimes. A toolchain or dependency experiment
must be reversible and must not silently become the production fix.

Do not disable GC, skip the group, reduce coverage, loosen its deadline, add
retries to the gate, or claim this is environmental without positive evidence.
Close this issue only after a demonstrated cause and a regression/fix pair, plus
the unchanged full-suite and affected native/race checks.
