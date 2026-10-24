# Reported issues — core stop/update, chat attachments, tree reveal (2026-10-24)

> Status: ACTIVE HANDOFF. Nothing below is implemented; the earlier in-session
> attempt was reverted deliberately. Treat each item as remaining work and
> re-diagnose it through the real product path before editing.

These are the four defects reported on a Windows desktop host, recorded with the
observed symptom, the exact reproduction, and the code owners that must be
inspected. The protocol/lifecycle notes are the reason the first item is not a
one-line fix.

## 1. `--core-stop` and startup handover are unusable across builds

Observed on native Windows:

```text
PS C:\Users\Lenovo\Desktop\crude> go run .
.connect core: cannot safely hand over the existing core: matching owner helper
failed: exit status 2: crude: core stop refused: retained workspaces require an
explicit graceful stop; stop it gracefully with its original installation, or
select a separate profile with CRUDE_HOME

PS C:\Users\Lenovo\Desktop\crude> go run . -core-stop
crude: core stop unavailable: schema_mismatch: core speaks protocol
54bb73355d01e0b0 (build 5636...); client speaks fb0082e9f27916b1 (build 1b37...)
```

Two independent defects:

- **`--core-stop` cannot stop a core built from other source.** The shell dials
  the core with the current application schema, so a mismatch is refused before
  `core.shutdown` is ever decoded. The only cross-build stop path is the
  retained owner helper (`app/core_handover.go`), and that path is only used by
  automatic startup handover, not by an explicit `--core-stop`.
- **Automatic handover refuses any retained workspace, even a completely idle
  one.** `core/lifetime.go` returns `retained workspaces require an explicit
  graceful stop` for `ExpectedOwner` stops whenever `len(status.Workspaces) != 0`
  (and repeats the refusal in the later admission check). Because a normal
  desktop session always retains at least one workspace, startup can never
  replace a mismatched core. The user's report — "stop should be a safe way for
  connected users and running work, but it must not be blocked by version churn"
  — is exactly this: the guard is correct for *work*, wrong for *idle retention*.

Requirements for the fix:

- Lifecycle control (stop/handover) must be version-independent. A helper or
  shell from another cohort must be able to ask the running core to stop without
  decoding that core's application protocol. Whatever channel is chosen must
  carry an immutable, minimal result (accepted / refused + reason) that does not
  depend on the changing schema.
- An explicit stop while a shell is attached or work is running must still
  refuse by default and name the work (`--force` stays the only override).
- Automatic update/handover must be allowed when every retained workspace is
  provably idle, and must refuse when anything can still begin work. A check
  that only samples "no active work" is racy: something must fence new turns,
  background memory jobs, compactions, terminals, document/terminal leases and
  factories, and release that fence if the shutdown is refused. Retained
  workspace actors can admit autonomous work without a shell, so "no shell
  attached" is not idle.
- A refused attempt must leave the old core, its owner record and every retained
  workspace untouched, and the message must say what to do next.

Owners to inspect: `app/core_host.go` (`StopCore`, `dialCore`, `isCoreMismatch`),
`app/core_handover.go` (owner record, helper invocation, `ExpectedOwner`),
`core/lifetime.go` (`coreMethod` shutdown admission), `core/runtime.go`
(`Runtime.Close`, `Status`, actor lifetime), `core/server.go` (handshake
refusal), `session/history_admission.go` and `session/memory_pipeline.go`
(existing idle-reservation primitives), plus
`docs/reference/core-client-protocol.md` and `docs/guides/core-shell-workflows.md`,
which currently document the absolute refusal and must be updated with the new
contract.

## 2. Chat attachments cannot be clicked to open

Clicking `[Pasted Text1]` or `[Pasted IMG1]` in an existing chat message does
nothing. The composer chip opens the attachment (editable pasted text, image
preview), and the chat row only styles the label.

Expected: the same native viewer the composer uses, opened from the transcript,
with images showing the image and pasted text showing the text — no second
viewer path, no external-app hand-off for file-backed pasted text (see
`docs/reference/text-input.md`: "file-backed storage is not a reason to route its
click to an external app").

Owners: `tui/chatprojection/semantic_rows.go` (label ranges and span styling),
`tui/screens/shellchat/selection.go` + `chat.go` (release/inline-action hit
testing already used for disclosure rows), `tui/viewmodel/update.go`
(`openReferenceOverlay` / action routing), `tui/viewmodel/app.go`
(`clickReferences`), `tui/screens/textoverlay/`.

Note: session-scoped reference IDs are not available for historical messages in
every case; the fix must resolve identity from what the message actually retains
instead of inventing a lookup.

## 3. No "Reveal in Explorer" for file-tree items

VS Code offers Reveal in Explorer/Finder from a file's context menu. The file
tree's context menu has no equivalent, although the shared desktop-open helper
and the attachment "Show in file manager" route already exist.

Expected: a file-tree context-menu action that reveals the exact path in the
native file manager, for local and connected cores (connected paths must
materialize a client-local copy or reveal the containing folder, never pass a
remote path to a local OS call). `fileio.OpenDesktopPath(path, true)` and
`App.clientDesktopOpen` are the existing owners; `filetree/desktop.go` only opens
the default application today.

Owners: `tui/viewmodel/context_menu_builders.go` (file-tree menu),
`tui/viewmodel/attachment_desktop.go`, `tui/viewmodel/attachment_open.go`
(the existing open/reveal route), `fileio/desktop.go` (Windows reveal is
currently a plain open), `docs/reference/interaction-matrix.md`.

## 4. Pasted-text viewer actions are laid out outside the shared contract

Screenshot: the pasted-text overlay shows the message line and then
`[ Save (Ctrl+S) ]` / `[ Cancel ]` floating at the right edge of the same row as
the error text, with no separator, no surface fill and no reserved footer. The
labels are also wrong for a preview (`Save`/`Cancel` instead of a close action),
and the bar visibly overlaps content.

Required shape, per `docs/reference/settings-design.md` and
`docs/reference/control-usage.md`: a dedicated footer row with a full-width rule
above it, decisions right-aligned inside that row, primary first and the
dismissal last, and body content that never paints into the footer row. No
per-screen button dialect, no new chrome.

The suspicion recorded during triage is that this overlay hand-builds its
actions instead of reusing the shared `ui.ActionBar` footer anatomy used by
settings and other pages; the follow-up must confirm whether
`tui/screens/textoverlay` is the owner or whether the shared frame's footer
helper is bypassed, and fix the shared helper rather than only this screen.

Owners: `tui/screens/textoverlay/textoverlay.go` (`rebuildActions`, `Layout`),
`tui/ui/action_bar.go`, `tui/ui/window_frame.go`, and the reference overlay
wiring in `tui/viewmodel/reference_overlay.go`.

## Verification expected for all four

- Reproduce through the real product entry points (CLI startup and stop for item
  1; chat click, tree right-click, and the pasted-text overlay for items 2–4).
- Item 1 additionally needs a genuine two-cohort stop: build A runs the core,
  build B stops it and starts its own, with a shell attached and with active work
  in both directions, plus an isolated temp `CRUDE_HOME` run.
- Item 4 needs a visual capture at a desktop and a constrained size
  (`scripts/tuishot.ps1` on Windows), not only a unit assertion.
