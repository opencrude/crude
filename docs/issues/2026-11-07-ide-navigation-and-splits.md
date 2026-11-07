# IDE navigation and splits (2026-11-07)

> Status: RESOLVED 2026-11-07. Contracts:
> [main view panes](../reference/main-view-panes.md),
> [editor](../reference/editor.md#qualified-location-navigation),
> [interaction matrix](../reference/interaction-matrix.md).

## Reported

1. Following a definition, reference or implementation was slow and could not
   be done with Ctrl+click, as in established IDEs.
2. Split opened an empty pane when the user wanted to see another part of the
   same file. A file could not be shown in more than one pane. There was no
   easy way to open or move a tab into the pane on its right.
3. A navigation started in a split pane opened its result in another pane.
4. There was no Back/Forward navigation (VS Code's title-bar arrows,
   keyboard and mouse buttons).
5. Every repeated view of one file must stay instantly in sync, without
   duplicated work.

## Cause

- One editor per path: `EditorState.Tabs` was the only identity, so a second
  tab of a file was impossible and every open "selected the existing tab",
  which pulled the file into whatever pane already had it.
- Navigation and file opens resolved their target by tab title, not by the
  pane that asked.
- Code navigation always opened the chooser, even for one destination.

## Fix

- Linked editor views (`tui/editor/linked_view.go`): one piece table, history,
  canonical projection, Git baseline, watcher, syntax parse and width model per
  document; each view keeps its own caret, selection and scroll. An edit
  remaps the other views' anchors in the same call, so all views paint the
  new text in the same frame.
- `EditorState.Views` (one per tab) beside `EditorState.Tabs` (one per
  document). Every open lands in the requesting pane; a pane shows a file at
  most once (moves and merges absorb a duplicate view).
- Split shows the file again; Split with Agent / with File; Open in / Move to
  the pane on a side.
- Ctrl+hover link, Ctrl+click definition, Ctrl+Alt+click implementation,
  direct jump for one destination, references from a declaration.
- Go Back / Go Forward: Alt+Left/Right, mouse buttons, header arrows.

## Follow-up user decisions (same day)

- The header arrows are always readable: an arrow with no place to go uses
  muted ink, not the pale border colour.
- The session elapsed timer left the header, because it is rarely read and
  costs room that navigation and identity need. Uptime stays in the Workspace
  panel.
