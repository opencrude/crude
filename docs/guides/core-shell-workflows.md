# Core and shell workflows

> Status: Current review guide. Implemented routes and observed evidence are distinct from outstanding release gates.

## Start, attach and stop

A normal `crude` starts or attaches to the private per-user core. Each terminal
renders its own shell; closing a shell is detach, not session termination. For
explicitly retained zero-client execution:

```sh
crude --core --server
# In separate terminals, sharing the same data directory:
crude --workspace /absolute/project --client-profile review-a
crude --workspace /absolute/project --client-profile review-b
crude --core-status
crude --core-stop                 # refuses active work/other clients
# --force is explicit destructive shutdown, not ordinary shell close.
```

A started core runs from a content-addressed copy of the executable,
`~/.crude/bin/crude-core-<build-id>[.exe]`, never from the image that started it.
`go run .` can therefore delete its temporary build when the shell exits and a
rebuild can replace `crude.exe` while the previous core lingers. Native image ID
is not protocol identity: different images of the same source cohort attach
without replacing the core. `crude --core-status` prints the running image.

Publication is immutable: concurrent starters close their complete candidates,
then atomically hard-link one winner without replacing its inode. Losers validate
and reuse its exact digest, type, size and executable mode. Unsupported hard-link
filesystems fail explicitly. All staged images currently remain retained: Unix
unlink success is not liveness proof for a core/helper/concurrent starter. Disk
use can grow across builds; automatic pruning is not implemented.

The private owner record binds its generation to the exact retained helper and
full hash. Its lifetime lock stays held through runtime shutdown. Autostart and
crash-left reclaim require positive absence and exclusive lifetime proof, not a
failed handshake. Explicit `--core-stop` uses a frozen minimal local stop/result
channel, independent of application schema/cohort. A mismatched cohort/schema
invokes only the verified original helper's graceful `--core-stop` with exact
`ExpectedOwner`; it never force-stops or kills the core. Retained idle workspaces
can hand over only after transactional admission, session, autonomous work,
memory, resource and storage leases prove them idle. Attached/resumable shells,
opening factories, pending/queued turns, compaction, commands, live terminals or
document/controller leases refuse with named actionable guidance. Refusal releases
all reservations without stopping work or replacing owners. Finish or close the
named work and retry, or select an isolated development profile. Only explicit
`--core-stop --force` overrides those protections. Do not delete owner
records, locks or images to bypass refusal. See the
[current protocol contract](../reference/core-client-protocol.md).

Unix startup publishes only an already-private socket: a short random bind name
inside the private directory is chmod/identity-validated before atomic no-replace
hard-link publication as `core.sock`. Dialers never need to tolerate public mode
during startup. Unsupported socket-link filesystems fail explicitly; canonical
address limits and exact-inode cleanup still apply.

### Developing crude inside crude

A shell refuses automatic handover to another source cohort while the core has
an attached shell, active/pending work or any owner that cannot be fenced idle, so
a dev build started from a terminal inside your running crude
cannot disturb it. Run the dev build with its own profile instead:

```powershell
go run ./internal/wiregen
go run ./internal/wiregen -check
$env:CRUDE_HOME = "$HOME\crude-dev"; go run .
```

```sh
go run ./internal/wiregen       # explicit generation after production-source edits
go run ./internal/wiregen -check # nonmutating freshness check
CRUDE_HOME=~/crude-dev go run .
```

Use the canonical native cgo environment for generation/builds. `make build`,
`scripts/build.sh` and `scripts/release.py` check source-cohort freshness. Direct
`go build` or `go run .` does not implicitly generate or check it.

`CRUDE_HOME` (`buildinfo.Home`) replaces the home directory for every crude data
path — core endpoint, config, workspaces, sessions, history and logs — so the dev
build starts its own core beside yours. Child tools still see the real `HOME`.
Copy `config.json` into `<CRUDE_HOME>/.crude/` if the dev profile should reuse
your accounts. Test binaries always isolate `CRUDE_HOME` through
`internal/testhome`.

For Linux visual capture, the **canonical** `<CRUDE_HOME>/.crude/corewire/core.sock`
address must fit within 103 bytes. Session scratch paths are often too long;
a symlink still resolves to the long target. Use a real short isolated profile:

```sh
profile=$(mktemp -d /tmp/crude-shot-XXXXXX)
CRUDE_HOME="$profile" COLS=160 ROWS=40 FONT_SIZE=11 \
  scripts/tuishot.sh "$CRUDE_TEMP_DIR/settings-capture" \
  key:ctrl+alt+p 'type:application settings' key:Return
CRUDE_HOME="$profile" crude --core-stop
```

The capture script rejects an oversized canonical profile before opening a
window and stops with a nonzero status if Crude exits or capture fails. Read the
boot image and startup diagnostics before continuing input; script exit alone
does not prove the requested UI journey completed. Close the isolated core and
dedicated window after review, leaving the developer's own core untouched.

Without `--server`, the core drains after zero clients and zero active work persist
for `--core-grace` (a Go duration). Core failure interrupts execution; restart and
session resume are passive, never historical provider/tool replay. Private client
profiles are exclusive OS-locked durable namespaces. Use different names for
concurrent shells; reuse the same name for crash-left private recovery. Omitting
it requires stable terminal/tmux identity, not PID-derived storage.

## SSH and invocation selection

Install `crude` on the remote account's PATH, configure public-key authentication
and verify the server host key through a trusted channel before adding it to
known_hosts. The remote workspace is a remote path:

```sh
crude --ssh build-host --workspace /srv/projects/example \
  --ssh-user developer --ssh-port 22 --ssh-identity "$HOME/.ssh/id_ed25519" \
  --client-profile remote-review
```

SSH uses fixed inspect/install/launch helpers and `crude --core-stdio`; no
project path enters remote shell command interpolation. Strict host-key checking,
no PTY, no forwarding,
no shared control session and noninteractive authentication are enforced. Do not
work around unknown/replaced keys with verification disabled. The stdio bridge
attaches to/autostarts the remote core; it is not an unauthenticated TCP service.
SSH diagnostics keep a sanitized bounded 4 KiB stderr tail.

Session create/open accepts `--account ACCOUNT_ID --model MODEL`, optional
`--provider PROVIDER_ID` and additive `--system 'profile instructions'`. Explicit
selectors refuse an already-live session. `--key` requires explicit `--account`;
a new account also requires a known `--provider` preset. Keys are ephemeral,
write-only and never application persistence. Prefer configured Accounts: command
arguments can leak via history/process listings. Snapshots/redacted settings and
receipts must never expose keys, OAuth material or other credentials.

## Shared editors and recovery

Open a file through the tree, picker, search result or palette in both shells.
Typing shares unsaved text; it does not save disk. Selections, cursor and scroll
are independent. Make disjoint changes, undo one author's group, then save: the
other author's accepted text must survive. Rename changes Path metadata, not
stable document ID, preserving unsaved text. Conflicted deletion fails closed.
Core owns source reads, one project watcher and lazy document-directory watchers.
Desktop opening downloads a bounded client-local cache; it does not interpret a
remote project path on the local filesystem.

The coordinator flushes pending edits before format/save, uses author-owned undo,
reopens views after reconnect and reconciles original mutation receipts. Unknown
commands are never replayed under a fresh ID. Accepted unsaved state, savepoints,
undo history and receipts restore from workspace SQLite after core restart.
Private unresolved edits use clientstate recovery, not a second shared authority.

Use **Resolve Shared Editor Changes** in the Command Palette. The native modal
starts with **Keep draft** (Esc changes nothing). **Compare with shared** opens
immutable read-only snapshots. **Use shared text** requires confirmation and a
durable private backup before discarding only the captured local pending draft;
an exact tombstone clears that recovery record. Backup/receipt failure preserves
the draft. Pending unknown commands must settle first. External disk conflicts
need separate disk comparison/reconciliation: Use shared text cannot authorize an
external overwrite. An abrupt kill before a pending private edit is durably
accepted is not proof that every keystroke has reached core storage.

**Private Editor Drafts** in the palette lists retained unresolved drafts and
discard backups for the exact private profile/workspace (up to 128 metadata rows).
Payloads load only on Preview. Copy retains the complete text even when the preview
is clipped at 64 KiB. Open draft stages the exact source record without overwriting
an unread/newer private recovery; if the editor is already open it offers an
immutable comparison instead of replacing its current work. Restored drafts remain
blocked from automatic publication. Close/Esc changes nothing.

Capability installation/access still requires immutable exact source/revision,
binding/preimage and explicit approval. A preview is not approval or a defaults
publication; no-action preview results cannot reset application preferences.
Transport authority never widens grants. Same-user core access is full authority,
not multi-tenant isolation.

## Authoritative implementation and review points

| Concern | Owners to inspect |
|---|---|
| CLI/core lifetime and SSH selection | `app/cli.go`, `app/shell_client.go`, `app/core_host.go`, `corewire/ssh.go` |
| Typed boundary and receipts | `coreprotocol/`, `core/server.go`, `core/runtime.go`, `coreclient/client.go`, `shellport/remote/` |
| Live editor admission and replicas | `shellport/live_documents.go`, `tui/viewmodel/live_editor.go`, `tui/editor/shared_document.go` |
| Durable text/undo/rename and watching | `workspace/documents.go`, `workspace/document_state.go`, `workspace/document_delta.go`, `workspace/document_files.go`, `workspace/document_watch.go`, `core/file_watch.go` |
| Private drafts and decisions | `clientstate/`, `tui/viewmodel/editor_recovery.go`, `tui/viewmodel/editor_conflict.go` |
| Files and client desktop effects | `core/client_files.go`, `core/project_replace.go`, `tui/viewmodel/client_files.go`, `tui/viewmodel/attachment_desktop.go` |
| Invocation secrecy | `core/invocation.go`, `workspace/invocation.go`, `coreprotocol/invocation.go` |
| Bulk reads/uploads | `core/bulk_transfer.go`, `coreclient/bulk_transfer.go`, `coreprotocol/bulk_transfer.go` |
| Cohort and safe handover | `internal/wiregen/`, `buildinfo/cohort_gen.go`, `app/core_handover.go`, `app/core_handover_unix.go`, `app/core_handover_windows.go` |

Large files, reference content and media use method-specific credit-controlled
bulk streams in either direction, not JSON response payloads, response tokens or
`payload.read` polling. Chunks are 256 KiB, with ordered offsets, whole-transfer
digest verification and a two-minute stream lifetime. Current owner limits are
64 MiB file reads and reference/media reads, 16 MiB editable reference uploads,
and 16 MiB document text; media upload admission retains its separate 64 MiB cap.
Bulk support does not bypass edit/mutation limits. Durable receipts retain typed
outcomes; staged uploads publish only through a separate exact durable command.

## Reproduce Linux process acceptance

Use a prebuilt final binary and **new** evidence directories. These harnesses own
isolated HOME/core/tmux processes; never substitute the user's live core. They
produce tmux text evidence, not screenshots. SSH requires installed OpenSSH
client/server and a nonroot owned evidence path compatible with StrictModes.

```sh
BIN=/absolute/path/to/crude
python3 scripts/core_shell_acceptance.py --binary "$BIN" --out "$CRUDE_TEMP_DIR/shell-final"
python3 scripts/core_ssh_acceptance.py --binary "$BIN" --out "$CRUDE_TEMP_DIR/ssh-final"
python3 scripts/core_editor_acceptance.py --binary "$BIN" --out-new "$CRUDE_TEMP_DIR/editor-final"
```

Inspect each retained result JSON, per-stage captures and process cleanup, not
just exit status. The shell harness exercises ten real CLI shells, origin death,
peer Yes, peer Escape Stop and zero-client server completion plus passive attach,
and samples twenty retained workspaces. The SSH harness uses genuine loopback
sshd and verifies unknown/replaced host-key rejection and two-shell stream
survival. Loopback shares a filesystem; it does not prove distinct-host source
or desktop handling. The editor harness enters via real tree/palette/keyboard,
not direct RPC. Origin death follows peer rendering of accepted text; it is not
an injected in-flight lost-response crash.

For visual review use repository `scripts/tuishot.sh` sequentially at 160×40 and
80×24. Never run concurrent captures or leave dedicated capture windows alive.

## Remaining native and distinct-host gates

On native Windows, build/run from a Windows checkout with its native dependencies:

```powershell
make build-windows
. scripts/libghostty_env.ps1; go build -o crude.exe .
.\crude.exe --core --server
# Separate native Windows Terminal windows, never WSL:
.\crude.exe --workspace C:\projects\example --client-profile native-a
.\crude.exe --workspace C:\projects\example --client-profile native-b
.\crude.exe --core-status
pwsh scripts/tuishot.ps1 -Cols 160 -Rows 40 .\tmp\core-review `
  'key:ctrl+alt+p' 'type:Resolve Shared Editor Changes' 'key:Return'
```

Verify protected named-pipe ACL/network-logon rejection, concurrent autostart,
crash-left ownership, detach/cancellation, ConPTY input/resize/replies, shared
unsaved text/author undo/recovery and desktop effects. Record native results and
close only owned windows/processes. A Linux cross-build cannot satisfy these.
On two genuinely separate hosts repeat SSH shared editor/save/rename/search,
media/cache/desktop and reconnect journeys with no client copy of the project.
Reject unknown and replaced host keys without launching work. Record host/OS,
binary identity, exact requests/evidence and cleanup without credentials.

## Evidence boundary at this documentation update

The process harnesses above were used for the earlier core/shell separation
acceptance; their previous full-suite, race, lint, benchmark and capture results
are **historical**, not validation of the current v2 integration. The same is true
of earlier JSON large-response/expiry measurements: v2 removed that transfer
path. Rerun journeys against the final checked binary before using them as current
evidence.

The current v2 implementation and still-open thresholds/matrix live in the
[active v2 acceptance plan](../plans/core-protocol-v2.md). Its checkpoint separates
parent-reported focused results from unrun repository-wide, native and distinct-
host gates. A source audit or documentation check is not product acceptance.
Native Windows, distinct-host SSH, exact PTY-write latency, outer-terminal frame
rate and per-feature visual/fuzz journeys require their own current evidence.
