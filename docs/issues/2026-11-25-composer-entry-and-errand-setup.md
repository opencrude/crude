# Composer chips, slash parity, workspace entry and Errand setup

> Status: RESOLVED for the five reported product behaviors. See the
> [follow-up evidence](#follow-up-evidence) for the nine review findings,
> Linux captures, platform coverage and host-specific housekeeping limit.

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

**Status: RESOLVED (2026-11-25).** The lasting notice and caption are verified
in the [follow-up evidence](#follow-up-evidence). The typed attach request carries the entry
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
request). On a home folder with 182 root entries, resident memory 60 s after
attach fell from 297 MB to 46 MB and Go heap from 175 MB to 8.4 MB; attach took
45 ms. A filesystem-root watcher no longer retries a parent that cannot exist.
Tests that failed on `812adcc3` include
`TestWireEntryAdmissionAndSharedLanguageDemand`,
`TestFolderServicesFollowLocationAndExplicitChoices`,
`TestExplicitSelectionOpensTopLevelButNotMissingFolder`,
`TestVolumeRootWatchHasNoMissingParent` and
`TestChooserBannerLeavesItsContentReadable`. The revised Linux startup acceptance passed all six scenarios.

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

**Status: RESOLVED (2026-11-25).** The file notice now offers **Open folder as
workspace** without closing or saving the editor. See the [Linux captures and
regressions](#follow-up-evidence). A file launch attaches its canonical parent
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

The initial Windows implementation and capture history are below. The
[follow-up evidence](#follow-up-evidence) supersedes the original open items.

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

### Initial Windows verification

- Static gates on the integrated tree passed: `wiregen` and `wiregen -check`,
  `go build ./...`, `make conventions` (layoutcheck, golangci-lint 0 issues,
  names gate), `make design-audit`, `make docs-check` with
  `DOC_BASE=origin/master`.
- Native Windows Terminal captures (real product, isolated `CRUDE_HOME`):
  first-use page, folder picker, welcome page with **New workspace** and the
  home-folder reason, explicit home open, and a file launch, each at 160x40 and
  80x24. A run on this repository at 160x40 opened the project normally: folder
  tree with Git status, Home launcher, Trust prompt, then the Set up AI prompt
  with the new Errand text (fresh profile without an account).
- Not run on this host: `scripts/startup_acceptance.py` (Linux tmux; no Linux
  libghostty build here) and the full benchmark sweep (the user runs it).

### Follow-up evidence

**H1, H2, H3 and H7.** Explicit home and file launches create one persistent,
unread notice per workspace. Reading, reopening and promoting update that
record. The notice stops offering promotion to a project shell. The welcome
page, Home and Recent Workspaces all use **New workspace**. Ctrl+Q
closes the Trust or AI setup prompt without saving a decision, then follows
the usual tab and quit rules. It does not bypass dirty-work checks.

Linux native captures used isolated profiles, Alacritty and `scripts/tuishot.sh`.
At both **160×40** and **80×24**, the **1 Notice** pill survived the old
five-second timeout. Expanding the home notice showed both services, the
reason and the Settings action. The file notice offered both actions. Selecting
**Open folder as workspace** kept the dirty editor and its text; Ctrl+B showed
the tree. Disk bytes stayed unchanged. All four capture windows exited cleanly.
[See the six before/after frames](../guides/validation.md#workspace-entry-captures).

`TestHomeEntryNoticeSurvivesReadingAndReopen` checks the stable identity and
unread renewal. `TestDockedNoticeDisclosesClippedHeadlineAndWrapsProse` checks
that disclosure reveals the full clipped headline without repeating it when it
fits. `TestFileEntryOpenFolderPreservesLiveWork` keeps the same dirty editor
and composer draft. `TestFileOwnerPromotionAnalyzesItsUnsavedText` verifies the
language manager receives that owner's current text, not the disk copy or a
peer file-only document. `TestFileOnlyCodeIntelligencePreservesProjectChoice`
checks that saving another setting does not overwrite the saved switch.
`TestQuitClosesStartupPromptsWithoutChoosing` covers both startup prompts.

**H4. Review results.** These fixes use the same owners for UI and core access:

| Finding | Observed failure and verified correction |
|---|---|
| R1: volume-root listing | Cleaning lost the slash in `C:/` and the share in UNC paths. `TestCanonicalClientPathKeepsHostVolume` covers drive, UNC, extended and POSIX spelling. `TestBrowseDirectoryAtAttachedVolumeRoot` lists real children through the port on Linux and on the Windows target under Wine. |
| R2: delete recovery | An unavailable mandatory store let delete remove the file. A managed delete also backed up the logical workspace instead of the actual checkout. `TestFilesDeleteKeepsBytesWhenHistoryCannotOpen`, `TestControlDeleteRestoresFromTargetCheckout` and `TestControlDeleteDoesNotFallBackToLogicalHistory` verify refusal and restoration of distinct bytes under the same relative name. |
| R3: replaced root | Holding takeover while replacing the directory admitted the wrong identity. `TestScopeRejectsReplacementDuringRecursiveAdmission` keeps it fenced. `TestCoreWatchWindowsRejectsDifferentNativeRoot` checks the native handle on the Windows target under Wine. |
| R4: access-denied subtree | Both query-first and analysis-first could prevent ranking indefinitely. `TestScopeObservesWhileRecursiveAdmissionPending` keeps scoped updates live. `TestScopeIndexesReadableSiblingsAfterDeniedFolder` checks both demand orders, readable results and permission recovery. `TestTopLevelWatchReportsAndResolvesDeniedCoverage` verifies the real warning and its resolution. `TestUnreadableDirectoryKeepsRankingButNotCompleteCoverage` prevents false completeness. |
| R5: collapsed tree | Reconnect renewed only visible directories. `TestClientTreeCollapsedListingsSurviveReconnect` drops the connection, reconnects at a new epoch and verifies an outside edit in a collapsed, previously empty directory. It also checks that a peer event does not open an untouched subtree. |
| R6: handover | The original directory-only resume lost file mode. `TestCoreRestartScheduleDurablePrivateAndIdempotent` checks the private manifest, `TestCoreRestartFailedAttachmentNeverOpensChooserOrAlternateWorkspace` checks exact routing, and `TestCoreRestartReadyRejectsExpiredTamperedAndRetargeted` rejects a stale entry after promotion. |
| R7: missing directory | A positional missing directory with a separator became a new file. `TestPositionalDirectorySpellingNeverCreatesAFile` checks both a trailing separator and `/.` through startup inspection. |
| R8: missing image | File-mode inspection of a missing `.png` used to affect later opens. `TestFileEntryCreatesNamedFileOnlyOnSave` verifies no creation before Save and the real image viewer after closing and reopening the saved path. |
| R9: long launch path | A saved-layout limit hid an explicitly selected long file. `TestExplicitFileOpensBeyondSavedPanePathLimit` opens the admitted file and round-trips the other saved tabs without extending the profile limit. |

**H5.** All six `scripts/startup_acceptance.py` scenarios passed on Linux, with
no surviving owned processes. The core-termination case initially timed out
when Browse discovered the dead session before the connection monitor did.
`TestStartupBrowseRecoversWhenNavigationDiscoversSessionLoss` covers that race;
`TestStartupClientUncertainSaveIsNotReplayed` protects the mutation boundary.

**H6 (host-specific).** Executions `e8802ba3e61f44345feb490f1e1dc398` and
`be6a245414b427a19122693aa4478ff7` were not registered on this Linux host.
No cleanup is claimed for them and no other execution was removed in their
place. Review and clean them on the host that owns their manifests.

**Platform limits.** The Windows test executable regenerated its launcher
fixture under headless Wine only after the old caption matched the old golden.
The 110×16 grids differed in one letter. Wine is not a native Windows Terminal
or a live UNC share. Native filesystem/terminal and distinct-host SSH acceptance
remain separate in the [first-use plan](../plans/broad-workspace-ux.md).

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
