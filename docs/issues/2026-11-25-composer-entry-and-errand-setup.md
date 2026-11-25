# Composer chips, slash parity, workspace entry and Errand setup

> Status: OPEN. Immediate actions reported on 2026-11-25. Each section names
> its owner, the decided behavior and the acceptance evidence. Mark a section
> RESOLVED only after its acceptance evidence is recorded here.

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
- `crude PATH` with a folder path opens that folder, with the same admission
  rules as any other attachment.

**Acceptance.** A real-process test launches with a file path and checks the
attached root, the open editor tab, the collapsed tree and the absence of a
language server. A native capture shows the layout and the notice.

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
