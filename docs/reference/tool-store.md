# Tool store

> Status: Current reference. The `toolstore` package owns acquired code-intelligence tools. Assembly resolves matching installed tools through the store. The [thin assembly checkpoint](../plans/autonomous-code-intelligence.md#current-checkpoint) has not yet connected the acquisition flow.

## Ownership

`toolstore` is a neutral package. It does not import `workspace`, `tui`,
`core`, `capability` or `session`. It knows nothing about languages.

The store owns:

- The read-only folder of each installed tool, with its manifest.
- Bounded downloads through the shared client from `network.NewClient`.
- Safe extraction of zip, tar.gz, tar.xz, gz (one file) and raw downloads.
- The accounting of each tool: size, source, digest, licenses, who installed
  it, which approval allowed it, which workspaces use it and when it was last
  used.
- An append-only history of installs, failed installs, first uses and
  removals.
- Previewed removal that fails closed.

The store does not own approval. The caller passes an approval reference in
`Request.Approval` and the store records it. The capability system and the
[code-intelligence setup](../plans/code-intelligence-setup.md) plan own the
question to the user. The store never deletes a tool on its own. There is no
disk cap.

The workspace decision transaction records the exact review and selected and
rejected IDs as audit evidence only. It does not establish human consent or
authorize installation. The native decision owner must retain and consume the
exact human receipt; agent and generic-settings paths cannot submit it. A
separate effect-admission owner is still needed before this evidence can be
used for project installation. The store exposes a publication-admission hook,
but no project caller supplies it yet. Do not connect the project installer to
this transaction until the native receipt, revocation, stale usage and partial
outcomes are fenced and tested together.

The internal reviewed-install worker requires a caller-owned current-effect
check before each download and again after it returns. It passes the caller's
publication check to the store. It does not record workspace use or start a
server. Those later effects need their own current admission. The worker is
not connected to the product question or an effect owner.

## Layout

The caller derives the root from `buildinfo.Home` (`CRUDE_HOME`) and passes it
to `toolstore.Open`. `Open` refuses a relative root.

| Path | Content |
|---|---|
| `<root>/<package>/<version>-<sha256[:12]>/` | One tool. Read-only after install. |
| `.../manifest.json` | Immutable manifest: identity, digest, digest origin, executable path, size, file count, archive kind, source, URL, licenses, install time, installer and approval. |
| `.../files/` | The extracted files. `Tool.Executable` is an absolute path in this folder. |
| `<root>/usage.json` | Mutable accounting: workspaces and last use for each tool ID. |
| `<root>/history.jsonl` | Append-only history, one JSON object for each line. |
| `<root>/.staging/` | Temporary install and removal folders. |
| `<root>/.publication-pending/<package>/<version>-<sha256[:12]>` | An install whose publication is not yet confirmed. |

The tool ID is `<package>/<version>-<sha256[:12]>`. The digest is the SHA-256
of the downloaded artifact. Package and version names are one portable path
component: letters, digits, `.`, `_`, `-`, `+` and `@`, with no leading dot or
hyphen, at most 128 characters.

The extracted files are in `files/` and not next to the manifest, so an
archive that contains its own `manifest.json` cannot collide with the store
manifest.

## Install

`Install` does these steps:

1. Validate the request. Budgets, installer and approval are required.
2. Take the in-process lock for the package and version. Concurrent installs of
   the same version wait, then get the installed tool. They download once.
3. If the version is installed, return it only when source, URL, archive,
   executable path and command binding also match the request, and the expected
   digest is empty or matches. A different identity fails: remove the tool first.
4. Download into `.staging/install-*/download`.
5. Compare the digest with `ExpectedSHA256` when it is set. Otherwise record
   the digest of the first download (`DigestOrigin` is `first-download`).
6. Extract into the staged tool folder, write the manifest and make the tree
   read-only.
7. Create and sync a publication record for the exact tool identity. After
   preparation, an optional caller-owned admission check runs immediately
   before the rename. It must do no I/O or call back into the store. A refused
   or cancelled check prevents publication. A successful check admits this
   publication, not later use or execution. Existing artifacts do not invoke it.
   The project acquisition flow must provide its own current authority check;
   this hook is not an approval.
8. Move the staged folder without replacing an existing destination. A matching
   installed tool found before the rename is returned without publication. If a
   rename attempt fails, its outcome needs inspection even when a tool is found
   afterward. The store does not overwrite an existing target.
9. Append an `install` event, then clear the exact unchanged publication
   record. The tool becomes available only after this step.

The store attempts to append an `install-failed` event for each failure. A
history-write failure is returned to the caller. If rename was attempted or
completed but its durable outcome is not confirmed, `PublicationUnconfirmedError`
identifies the tool. The caller must inspect the store and must not automatically
retry or use the artifact. Pending records remain after a restart. Lookup,
automatic assembly, usage and ordinary removal do not accept the tool while
its record exists; `Unrecognized` includes its identity for inspection. There
is not yet a product recovery action for these records. The store attempts to
delete the staging folder. `Open` deletes stale installation scratch but keeps
interrupted removal folders, which may contain installed tools, for inspection.
This is not a filesystem security sandbox. Directory synchronization is a
no-op on Windows, so crash durability there has not been verified.

### Download rules

- HTTPS only. URLs with a user name or password are refused. Loopback HTTP is
  accepted only by tests through an unexported store field.
- At most five redirects. Each target must pass the same rules.
- `MaxDownloadBytes` bounds the body. A larger `Content-Length` fails before
  the body is read. A short body fails.
- The client has no total timeout, because large tools take time. A download
  that sends no data for 60 seconds stops. Cancelling the context stops the
  download at once.

### Extraction rules

- `MaxExtractedBytes` bounds the bytes written, counted from the real output
  and not from archive headers. `MaxEntries` bounds the entries (default
  65536). This stops decompression bombs.
- Entry paths must be relative and in their simplest form. Absolute paths,
  `..`, backslashes, colons and control characters are refused.
- Only folders, regular files, symbolic links and tar hard links are
  accepted. Devices, pipes and other special files are refused.
- A symbolic link target must be relative and stay in the tree when read as
  text. The store creates links only after all files are written, never writes
  through a link, and then resolves each link: a link that resolves outside the
  tree, to a missing file or into a loop fails the install.
- A tar hard link must point to an earlier regular file. The store writes a
  copy, which counts against the byte budget.
- Duplicate entries fail.
- `Bin` must be a regular file in the tree, not a link. For `gz` and `raw`
  downloads it is the file name that the store writes.

### Read-only enforcement

On Unix, files become `0444`, executables (`Bin` and files with an execute
bit) `0555`, and folders `0555`, the tool folder last. On Windows, files get
the read-only attribute; folders do not change. Links do not change and are
never followed.

## Accounting

- `Lookup(package, version)` returns the one installed build. It returns
  false when there is no build or more than one.
- `List` reads every manifest from disk and returns the tools sorted by package
  and version, with the sum of their content sizes. `Tool.Size` is the content
  size recorded at install.
- `Unrecognized` lists root entries that are not tools, for example a folder
  without a valid manifest. The store never removes them.
- `MarkUsed(id, workspace)` records the workspace and the time. It replaces
  `usage.json` atomically through `fileio.CommitAtomicFile`. The first use by a
  workspace appends a `use` event; later uses update only the time. `MarkUsed`
  has no caller-owned admission check and must not be used as an authorization.
- A damaged `usage.json` makes `Open` fail with the file name. The store does
  not replace it with empty data.

### History

`History(ctx, offset, limit)` returns events newest first and the total count. The
page size is 1 to 1000. An offset outside the events fails, and the error names
the valid range. Each append is synced. A line that does not decode, such as
the torn final line of an interrupted append, is skipped. `Open` and each
append seal a torn final line with a newline first, so the next event starts on
its own line. History scans the log twice and retains only the requested page,
so memory does not grow with the length of the log.

## Removal

1. `PreviewRemove(ctx, ids)` or `PreviewRemoveUnused(ctx)` returns the tools, their
   folders, the measured bytes of every regular file (manifest included) and a
   token. Unused means that no workspace uses the tool.
2. The token is a SHA-256 of the tool IDs, the digest of each manifest, a
   fingerprint of each tree (path, type, permissions, size, modification time
   and link target of every entry) and the usage entry of each tool.
3. `Remove(ctx, preview, actor)` requires a nonempty actor and takes the install locks of the tools and the store
   mutex, computes the preview again and fails with `ErrPreviewStale` when
   any preview field differs. A changed file, a new file, a removed tool, a use
   after the preview, or altered displayed metadata all fail.
4. Remove makes only each previewed tool folder writable and moves it into
   `.staging/remove-*`. If a move fails or the context ends, the moved folders
   go back and nothing is removed.
5. Remove saves `usage.json` without the tools, appends a `remove` event with
   the actor for each tool, deletes an empty package folder and then deletes the staged
   files. If that final delete fails, the tools are already removed and the
   staged files remain for inspection.

## Limits

- One process owns a store root: the per-user core. The store has no lock
  between processes. Two processes that use the same root can race.
- Store methods do blocking file and network I/O. Do not call them on a UI
  goroutine. List, History, Unrecognized and removal previews honor cancellation
  while waiting for the store lock and between filesystem operations. Removal
  revalidation does the same. An operating-system filesystem call already in
  progress may not be interruptible; cancellation takes effect when it returns.
- On Windows, creating a symbolic link needs a privilege. An archive with links
  fails there with a clear error.
- The xz decoder allocates the dictionary that the stream header declares. A
  hostile `tar.xz` can make the decoder allocate up to that size before the
  byte budget applies. Approved sources and digests limit this risk.

## Dependencies

`github.com/ulikunitz/xz` v0.5.17 decodes tar.xz. Version 0.5.14 and later
include the fix for CVE-2025-58058.

## Tests

The tests in `toolstore/` use only loopback `httptest` servers. Key tests:
`TestInstallEachArchiveKind`, `TestInstallRejectsUnsafeArchives` (zip slip,
link escape, link chain escape, devices, bombs, entry budget),
`TestInstallKeepsContainedLinks`, `TestInstallVerifiesExpectedDigest`,
`TestInstallCancelledDuringDownload`, `TestInstallStopsStalledDownload`,
`TestConcurrentInstallsOfOneVersionDownloadOnce`,
`TestInstalledToolIsReadOnly`, `TestRemoveFailsClosedWhenTreeChanged`,
`TestRemoveFailsClosedWhenUsedAfterPreview` and
`TestHistorySkipsTornFinalLineAndSealsIt`.
