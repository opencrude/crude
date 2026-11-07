# Problems, code copy and menu consistency (2026-11-07)

> Status: RESOLVED 2026-11-07. Contracts:
> [client view memory](../reference/main-view-panes.md#client-view-memory),
> [user language](../reference/user-language.md).

## Reported

- Context menus did not follow pointer movement. Their labels used different
  words for the same action.
- A workspace switch or a new client lost the open editor panes. The previous
  view-memory change kept minimized agents, but not the documents and splits.
- An agent code block could not be copied cleanly after Glamour painted it.
  Selection copy kept the layout padding and syntax spacing.
- The Problems pane explained every language server. It did not order the
  problems, and its text could not be selected or copied.

## Cause

- A menu hover trusted the list selection to match the menu cursor. When the
  two disagreed, pointer movement did nothing.
- View memory stored agent, tree and rail choices, but no pane topology and no
  document list.
- A code block was available only as painted rows. The source text was not
  bound to any clickable row.
- The Problems projection was built from server health and assembly records.
  It grouped by file and kept the language-server inventory in the pane. Its
  list had selection but no copy action.

## Fix

- A menu hovered row now writes the menu cursor directly and marks the popup
  dirty. Labels use one sentence-case verb-and-object dialect.
- View memory stores the docked split tree, each pane's open documents, the
  selected document in each pane and the active pane. A switch or a new client
  rebuilds the tree and opens the documents again. Paths outside the workspace
  are ignored, and a missing remembered file is skipped without a notice.
- Each complete fenced code block gets a `Copy code` chip. The chip carries the
  exact source text, not the painted rows, so copying never includes Markdown
  padding.
- The Problems pane counts the rows it shows, lists one short language status
  row with a direct Language settings action, and orders problems by severity,
  file and source position. Ctrl+C copies the selected row. Each problem and
  the summary also offer an explicit copy action.

## Verification

- Full mouse dispatch over a real context menu moves the highlighted row, and a
  native capture shows the highlight follow the pointer across rows.
- A saved two-pane layout survives JSON persistence and restores both panes,
  tab order, selected documents, focus and divider fraction.
- A rendered assistant message exposes a `Copy code` chip whose payload is
  byte-identical to the fenced source, and prose after the fence still paints.
- Problems rows sort errors before warnings, the language row opens Language
  settings, and Ctrl+C copies the selected item.
- `go run ./internal/wiregen` was required: the previous commit left the
  protocol cohort stale, so `make bench-gate` could not pass at that commit.
  With the regenerated cohort and schema, `make bench-gate` passes.

## Second review (2026-11-07)

Reported after the first fix: the Problems pane did not say how many language
servers work, wasted the width and had uneven columns; the References popup
did not follow the pointer; menus mixed ellipses, descriptions and missing
shortcuts; and folder expand felt slow.

Cause and fix:

- **Problems** was a generic Status Overview list. Its rows had a label, a
  free-text summary and a right column, so the message, the full path and a
  second file name competed for one row and nothing lined up. The pane is now
  its own screen (`tui/screens/problems`): one header row (filter, present
  counts, language status with its next step), then problems grouped by file
  with right-aligned source and position columns. The status counts running,
  starting and failed language servers. Drag and Shift+keys select rows;
  Ctrl+C copies plain `path:line:col` lines.
- **Pointer hover** in popups was an allow-list of window IDs; the References
  popup was not in it, and its list moved only a hover tint, so Enter still
  opened the first row. Every popup without its own hover owner now receives
  motion, and `ui.List.SetFollowPointer` makes a choice list select the row
  under the pointer.
- **Menus** used the right column for descriptions ("opens a window") and
  hard-coded key text. The column now shows only the registry shortcut for
  the row's action (`menuShortcut`). Ellipses and descriptions are gone. The
  pane menu is three sections (Split, Move, Window) in one side order (right,
  down, left, up), and rows that cannot act with one pane are not shown.
- **Folder expand** through the real core measured 0.3 ms on a synthetic tree
  and about 1 ms on this repository (click to painted rows), with the core
  read under 1 ms. The delay was not reproduced; the live core showed CPU peaks
  of about 800% while builds and tests ran, which is the likely cause but is
  not proven. A connected test now fails when an expand takes over 100 ms.

