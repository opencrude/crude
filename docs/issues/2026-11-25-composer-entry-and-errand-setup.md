# Composer chips, slash parity, workspace entry and Errand setup

> Status: OPEN. Sections 1, 2 and 5 are RESOLVED. Sections 3 and 4 are
> implemented but have open follow-ups: the native captures show that the user
> does not yet get a clear, lasting explanation, and file mode has no in-app
> way to turn language servers on. Read [Handoff](#handoff-2026-11-25) first.
> Mark a section RESOLVED only after its acceptance evidence is recorded here.

The user reported five items in one request. They are separate owners, so each
section stands alone. Do not merge their fixes into one shared workaround.

## 1. Attachment chips break and lose their style

Owner: `tui/ui` text engine (`TextArea`, `AtomicRange`) and `tui/input`
reference ranges.

**Report.** An attachment chip in the composer (for example
`[Pasted text #1 +20 lines]`) must act as one unit. It breaks at line ends. Its
style sometimes disappears after an edit, for example after Space before the
chip to move it forward. The feature then looks broken.

**Causes found by the lead (2026-11-25).**

- Soft wrap ignores chips. `rendering.WordWrap` and `FindSegmentStarts` split a
  logical line at spaces, and a chip label has spaces, so one chip can paint on
  two rows. Cursor movement, hit testing and bidi layout use the same split.
- Chip ranges are flat offsets that the text engine never moves. Only some
  composer edit paths call `applyReferenceRanges` after an edit. Backspace and
  Delete that do not remove a chip, the newline chord, plain paste, clipboard
  cut and other paths leave the old offsets. The next paint then styles the
  wrong runes, which looks like a wiped chip.
- Space with the caret at a chip start returns `RefOpenEvent`, so Space opens
  the attachment instead of inserting a space. The caret at a chip start is the
  normal insertion point before the chip.

**Decided behavior.**

- A chip is one unbreakable unit in every layout: wrap, caret movement, word
  movement, selection, hit testing, bidi arrangement and paint. A chip that
  fits moves to the next row as a whole. A chip wider than the text column
  takes its own row and stays one unit.
- Chip ranges follow every edit. One owner keeps them current; no edit path
  can paint stale offsets.
- Space always inserts a space. A chip opens with a click, as the
  [interaction matrix](../reference/interaction-matrix.md) states.

**Acceptance.** Regression tests that fail on the old code: a chip at a wrap
boundary paints on one row; Space, Enter with the newline chord, Backspace,
Delete, paste, cut, undo and redo before a chip keep the chip style on the
exact chip runes. A native Windows capture at 160x40 and 80x24 shows a wrapped
chip and an edit before it.

**Status: RESOLVED (2026-11-25).** One segmentation in the `TextArea` engine
treats each chip as an unbreakable unit for wrap, paint, caret, hit testing,
selection and bidi; a chip wider than the column takes its own row with a
shortened label. The engine moves chip ranges with every insert, delete, undo
and redo, and the composer re-derives identities once per changed draft. Space
inserts text. Backspace and Delete edit the draft through the same undoable
path and no longer delete the session's attachment, which matches the chip's
`x` target. Tests that failed on `812adcc3`: `TestTextAreaWrapsChipAsOneRow`
(fits, too wide, RTL), `TestTextAreaChipWordDeletionAndUndo`,
`TestTextAreaAdjacentWordsStopAtChip`, `TestComposerChipFollowsEdits` (Space,
newline, Backspace, Delete, paste) and `TestComposerUndoRestoresDeletedChip`.
`TestTextAreaPlainRowsMatchWordWrap` passes on old and new code, so text
without chips wraps exactly as before. Native Windows captures at 160x40 and
80x24 (`TestComposerChipVisualAcceptance`) show the chip whole and styled
after two Space presses before it.

## 2. Slash command matches differ in a dirty composer

Owner: `tui/input` slash list (`syncSlashState`, `setSlashFilter`,
`slashNameMatches`).

**Report.** Slash commands work anywhere in a draft. In an empty composer,
`/model` finds **settings** by its description. In a composer with text, the
same `/model` finds nothing.

**Cause.** `setSlashFilter` ranks name prefixes, then description matches. In a
draft with text, `syncSlashState` opens the list only when a command name
starts with the typed text, so description matches never show.

**Decided behavior.** One matcher serves both states. The list opens when the
token at the caret has a match, with the same items in the same order. Only the
commit differs: a command that starts the draft can take the rest of the line;
a command inside a prompt removes only its name. Paths stay prose because no
command matches them. A description match runs only from the visible list.

**Acceptance.** A test types `/model` in an empty draft and after prose and
gets the same list and the same command. The existing path and mid-prompt
tests still pass.

**Status: RESOLVED (2026-11-25).** One ranked matcher serves every draft; the
list opens only when it has a match, and Enter or Tab acts on it only while it
is visible. A whole-draft `/usr/bin` no longer loses its first Enter to an
invisible list. Esc closes a visible list before the composer's Esc ladder
clears the draft. Tests that failed on `812adcc3`:
`TestComposerSlashMatchesInEveryDraft` and
`TestComposerSlashWithoutChoicesDoesNotCaptureEnter`; the App journey
`TestEscClosesVisibleCommandListBeforeTheDraft` covers the Esc ladder. The
native capture shows `/model` after prose offering **settings**, and Esc
closing the list with the draft kept.

## 3. Welcome page caption and explicit top-level folders

Owner: `tui/startup`, `core/projectpath`, core admission (`inspectProject`),
and the workspace services that own Local History and language servers.

**Report.** When Crude opens in the home folder, Desktop or another top-level
folder, it shows the welcome page. That is correct. The **Open Workspace**
button caption is wrong; it must be **New workspace**. When the user
explicitly opens a top-level folder from that page, Crude must allow it. It
turns off Local History and language servers for that workspace and tells the
user clearly what it did.

**Decision change.** This replaces the earlier rule in the
[first-use handoff](../plans/broad-workspace-ux.md) that top-level folders are
navigation-only and have no open action. A launch in a top-level folder still
shows the welcome page. Only an explicit choice opens it.

**Decided behavior.**

- The welcome button reads **New workspace**.
- An explicit selection of a home, personal or root folder attaches a
  workspace. Its effective Local History and language-server settings default
  to off. An explicit workspace choice in Settings still wins.
- The workspace states the cause once, in plain words, for example: "This is
  your home folder. Crude turned off Local History and language servers here.
  Open a project folder to use them." Settings shows the same reason next to
  the two switches.
- A launch without an explicit choice keeps the welcome page.

**Acceptance.** `scripts/startup_acceptance.py` scenario 3 changes from refusal
to explicit opening. Tests prove that no Local History store and no language
server starts for the top-level workspace, and that a child project keeps both.
Native captures show the welcome page and the notice.

**Status: IMPLEMENTED, FOLLOW-UPS OPEN (2026-11-25).** See [Handoff](#handoff-2026-11-25)
items H1 and H3. The typed attach request carries the entry
intent (`Entry{Explicit, FileOnly}`); core admission stays the only check. The
folder classifier moved to `workspace/folderkind` so the workspace applies the
folder defaults in `setting.MergeAppWorkspace`, below explicit workspace
choices; no implicit choice is stored. A top-level actor builds no Local
History store and starts no language server until the user turns them on;
an explicit Local History opt-in opens the store at once (a store inside its
own profile still fails clearly). The welcome button reads **New workspace**;
the shared folder picker keeps **Open Workspace** because it opens the chosen
existing folder. The notice names the folder and the switched-off services.

Measuring the real home folder exposed sustained background work, so the same
policy also makes the recursive watcher and the text index demand-driven for
top-level folders (browsed folders and open files are watched; an index builds
on the first content query; Find in Files and Go to File still walk on
request). On `C:\Users\Lenovo` (182 root entries), resident memory 60 s after
attach fell from 297 MB to 46 MB and Go heap from 175 MB to 8.4 MB; attach took
45 ms. A filesystem-root watcher no longer retries a parent that cannot exist.
Tests that failed on `812adcc3` include
`TestWireEntryAdmissionAndSharedLanguageDemand`,
`TestFolderServicesFollowLocationAndExplicitChoices`,
`TestExplicitSelectionOpensTopLevelButNotMissingFolder`,
`TestVolumeRootWatchHasNoMissingParent` and
`TestChooserBannerLeavesItsContentReadable`. The Linux-only
`scripts/startup_acceptance.py` was updated and type-checked but not run: this
host has no Linux libghostty build.

## 4. Open one file directly

Owner: `app/cli.go` launch arguments, startup attachment, the shell layout
that owns the sidebar, and the same service policy as section 3.

**Report.** Users ask to open one file directly, as with nano. Crude shows its
normal layout with the file open and the sidebar folders closed. The parent
folder of the file is the workspace. Language servers are not needed in this
mode. Crude tells the user clearly what it did.

**Decided behavior.**

- `crude PATH` with a file path attaches the parent folder and opens the file in
  the editor. The folder tree is collapsed. A missing file opens as a new file
  at that path; it is created on Save.
- This file launch starts no language server. It uses the same service policy
  owner as section 3, not a second gate. If the parent folder is a top-level
  folder, section 3 also applies.
- A short notice names the folder and the change, for example: "Opened
  notes.txt in Desktop. Language servers are off for this file." The user can
  open the folder tree as usual.
- `crude PATH` with a folder path opens that folder. A path argument is an
  explicit choice, so a top-level folder opens with the section 3 defaults.

**Acceptance.** A real-process test launches with a file path and checks the
attached root, the open editor tab, the collapsed tree and the absence of a
language server. A native capture shows the layout and the notice.

**Status: IMPLEMENTED, FOLLOW-UPS OPEN (2026-11-25).** See [Handoff](#handoff-2026-11-25)
items H1 and H2. A file launch attaches its canonical parent
as a file-only actor: language services stay off until a project attachment
of the same root asks for them, and a file shell never stops another shell's
servers. The tree starts hidden for this launch while the saved sidebar
preference is kept. A missing file opens as a named buffer that Save creates.
`TestDirectFileProcessOpensEditorWithoutFolderTree` launches the real
executable with a path argument over a private ConPTY and fails on the old
code (it showed New Session); `TestFileEntryCreatesNamedFileOnlyOnSave` and
`TestPositionalEditorPathReachesStartup` also fail on the old code.

## 5. First AI setup asks for an Errand model

Owner: `tui/viewmodel/onboarding.go` and the Settings setup route.

**Report.** The first **Set up AI** prompt asks only for an account and a Main
Agent model. Many Crude features depend on Errand. Crude must ask the user to
set an Errand model and explain why, clearly and professionally, without
sounding like it begs.

**Decided behavior.** The prompt asks for an account, a Main Agent model and an
Errand model. It says what Errand does (checkpoints, editor suggestions,
memory, language-server checks) and recommends a fast, low-cost model for it.
It also says that Errand uses the Main Agent model until the user selects one.
The setup route continues from Main Agent to the Errand model.

**Acceptance.** Onboarding tests check the text and the route from account to
Main Agent to Errand. A native capture at 80x24 shows the full prompt without
cut text.

**Status: RESOLVED (2026-11-25).** The prompt asks for models for Main Agent
and Errand and explains why; the frame grows so the text is never cut. After
the setup route saves a Main Agent model, Settings opens Errand's **Model** tab
unless Errand already has its own model; the field explains the recommendation
and nothing is saved until the user applies it. Both new tests
(`TestOnboardingReminderAsksForErrandModelInFull`,
`TestOnboardingConnectedAccountModelSaveAndExplicitSession`) fail on the old
code. Native captures at 160x40 and 80x24 show the full text.

## Handoff (2026-11-25)

The lead (Charlotte) stopped here at the user's request. This section is the
complete state for the next agent. Work the open items in order. Each item
names the evidence, the owner and the intended fix. None of them blocks
running Crude on its own repository (see [Run Crude on itself](#run-crude-on-itself)).

### Commits of this request

Newest first, as published on `master`. The unpublished development history is
kept locally under `refs/archive/crude/2026-11-25-development-final`.

| Commit | Content |
|---|---|
| `3b8814e6` | Workspace entry: **New workspace** caption; explicit top-level folders open with Local History and language servers off by default; demand-driven watcher and text index for top-level folders; `crude PATH` opens one file in its parent folder; capture launcher fix for `.cmd` wrappers; this handoff. |
| `3282ec05` | Native capture fixtures for wrapped composer chips and the first AI setup prompt (`tui/viewmodel/composer_entry_visual_test.go`, env-gated). |
| `d5be8d50` | `scripts/tuishot.ps1` sends the requested key through the user's PowerToys remaps (Esc and CapsLock are swapped on this machine). |
| `696a0008` | Composer chips are one unit (wrap, caret, selection, delete, undo, style); `/` matching is the same in an empty and a dirty draft; first Esc closes a visible command list. |
| `b5783f4f` | First AI setup asks for a Main Agent model and an Errand model, and explains why. |
| `0e149b0c` | This issue file (the five actions). |

### Verification at handoff

- Static gates on the integrated tree passed: `wiregen` and `wiregen -check`,
  `go build ./...`, `make conventions` (layoutcheck, golangci-lint 0 issues,
  names gate), `make design-audit`, `make docs-check` with
  `DOC_BASE=origin/master`.
- `python scripts/test_go.py` (grouped repository suite) was still running
  when the user had to leave. The result is recorded in the commit message of
  this commit. If it says "not finished", run it first.
- Native Windows Terminal captures (real product, isolated `CRUDE_HOME`):
  first-use page, folder picker, welcome page with **New workspace** and the
  home-folder reason, explicit home open, and a file launch, each at 160x40 and
  80x24. A run on this repository at 160x40 opened the project normally: folder
  tree with Git status, Home launcher, Trust prompt, then the Set up AI prompt
  with the new Errand text (fresh profile without an account).
- Not run on this host: `scripts/startup_acceptance.py` (Linux tmux; no Linux
  libghostty build here) and the full benchmark sweep (the user runs it).

### Open items

**H1. The entry explanation is a 5-second footer flash. (High.)**
Owner: `tui/viewmodel/workspace_entry.go`.
Evidence: captures `entry-shots.cpARum` (session temp). After an explicit home
open, the footer shows "This is your home folder. Crude turned off Local
History and language servers her…" at 160x40 and "…Crude turned o…" at 80x24,
under a Trust prompt that has the focus. After a file launch, no explanation is
visible at all 4 seconds later. Cause: `showEntryFolderNotice` and
`showFileEntryNotice` call `Feedback.NotifyInfo`, which records a transient,
already-read notice; `footerHelpSegments` shows transient notices for 5 s
only (`LatestTransient(now, 5*time.Second)`), and the Notices list does not
count them. The text also says "Crude turned off" even when the user turned a
service off at application level.
Fix: post one persistent, unread Info notice per workspace, the same pattern as
`tui/viewmodel/project_tools.go` ("Code intelligence tools are available"):
`Owner` = workspace root, `Source` = `workspace-entry`, `DedupKey` =
`workspace-entry:<root>` (a repeat open marks it unread again), `Summary` =
"Local History and language servers are off. This is your home folder."
(services derived from effective settings, reason from
`Entry.FolderSummary`), `Detail` = why (they watch and read many files in
these folders) and how to change it, `Actions` =
`{ID: "settings:workspace", Label: "Open Workspace Settings"}` (already handled
in `handleStatusOverviewNavigate`). The file launch notice: Summary "Opened
notes.txt in project.", Detail names the closed folder tree (Ctrl+B shows it)
and the services that are off. Update `TestFileEntryCreatesNamedFileOnlyOnSave`
(it reads `LatestTransient`) and add a home-folder notice test. Recapture both
flows at 160x40 and 80x24 and check the Notices pill count and the panel text.

**H2. File mode has no in-app way to turn language servers on. (High.)**
Owners: `types/setting/settings.go` (`MergeAppWorkspace`),
`workspace/documents.go` (`fileOnlyOwners`, `languageViewNeeded`),
`tui/viewmodel` workspace navigation.
Evidence (code): `MergeAppWorkspace` forces `LSP.Enabled = false` for
`Entry.FileOnly` after it applies workspace overrides, so the **Code
intelligence** switch is saved but shows off again. Documents opened by a
file-only owner never get a language view. The Settings text says "Open its
folder to use language servers", but opening the same folder from the picker,
Recent Workspaces or a context menu is a no-op: `workspaceRootMatches` returns
early in `remote_lifecycle.go` (`attachRemoteWorkspace`),
`workspace_open_prompt.go` (`requestWorkspaceOpen`) and
`context_menu_builders.go` (Open disabled). Today only a second shell started
with `crude <folder>` promotes the actor.
Intended fix (recommended): let a file-only attachment reopen its own root as a
project through the existing switch lifecycle. `remote.Workspace` already
stores its `entry`; expose "this attachment is file-only" through the shell
port, allow the same-root path in the three guards only in that case, and let
`AttachWorkspace` (which attaches with `Explicit: true`) promote the actor via
`SetFileOnly(false)` as `TestWireEntryAdmissionAndSharedLanguageDemand`
already proves at the protocol level. Add a notice action "Open folder as
workspace" for the file launch and make the Settings switch read-only with that
next step while the attachment is file-only. Test with a connected journey
(`newRemoteJourneyShellWithEntry`): file launch, promote, language view opens,
dirty editor and composer draft survive, tree can be shown. An alternative
(explicit workspace choice wins in file mode) also needs file-only owners to
re-request language views; it is not recommended because it bypasses the
attachment intent that `languageViewNeeded` relies on.

**H3. Caption consistency. (Low.)** The welcome button reads **New workspace**;
the Home launcher card reads **New Workspace**; the Recent Workspaces row and
its context menu read **New workspace**. Pick one casing for buttons and cards
and record it in `docs/reference/user-language.md`.

**H4. Independent review in progress.** Child researcher Rowan
(`agent-8e30a32bcee05770c70789529ea63f37`, read-only) reviews this workspace
entry change for regressions and restrictive assumptions (path edge cases,
second shells, watcher scope, protocol fields, docs drift). Read its report
before H1 and H2, and fix what it proves.

**H5. Linux acceptance.** Run `scripts/startup_acceptance.py` on Linux. Its
scenario 3 now expects explicit opening of a top-level folder.

**H6. Housekeeping.** The managed worktrees of the integrated children Chipa
(execution `e8802ba3e61f44345feb490f1e1dc398`) and Portia (execution
`be6a245414b427a19122693aa4478ff7`) are integrated by patch and idle. Review
and clean them with `execution.review` and `execution.cleanup`.

**H7. Ctrl+Q under a modal prompt. (Check.)** In all five product captures the
final Ctrl+Q was sent while a modal prompt had the focus (Trust this folder, or
Set up AI), and the product did not exit within 30 s. Find out if the modal
consumes the global Quit binding by design (`docs/reference/interaction-matrix.md`
lists Ctrl+Q as Quit application with no exception). If not, route it to the
shell quit flow.

### Run Crude on itself

- Build: `go build ./...` passes. The user runs Crude from source with
  `CRUDE_HOME` set and `go run .` in the default Windows Terminal profile.
- `internal/buildinfo/cohort_gen.go` changed and the core protocol gained
  entry fields (`EntryIntent`, `StartupInspectRequest.Explicit/AllowFile`,
  `StartupSnapshot.ChooseWorkspace/FilePath/FileMissing`). The first launch of
  this build therefore does not reuse a running core from an older build.
- The repository is a normal project folder: it opens with project settings,
  Local History and language servers as before. Only home, personal (for
  example Desktop) and root folders get the new defaults.
- For captures of an uncommitted build, use a `.cmd` wrapper that sets an
  isolated `CRUDE_HOME` (see `docs/guides/windows-visual-capture.md`); since
  this commit `tuishot.ps1` calls such a wrapper with `call`, so `quit:` steps
  can verify the exit. Stop that core afterwards with
  `CRUDE_HOME=<dir> <exe> --core-stop --force`.

### Publication

The 2026-11-25 history contained a raw Gemini v1beta discovery snapshot under
the simulator schema sources (blob `09851aa1c64a407356e22f714ce23a3ea36e093c`)
with unverified redistribution terms. It was replaced by Apache-2.0 Protobuf descriptors, and the published
history is a rewritten, tree-identical copy that never contains the blob
(`refs/crude/publish/2026-11-25-verified`, local backup bundle in the session
temp folder). Every later local commit is re-parented onto that copy before a
push, with an identical final tree. The local `master` is then moved to the
published commit, so after this push local and remote `master` are the same.
