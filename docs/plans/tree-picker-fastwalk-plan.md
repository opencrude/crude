# Tree Picker and Internal Fastwalk Plan

> Status: IN PROGRESS - APPROVED 2026-09-04. Implementation is authorized under
> this document after manual user review. The scope lock remains authoritative.

## Purpose

Replace the flat directory list inside the existing New Workspace and Attach
File windows with a shared, lightweight tree browser and bounded recursive path
search. Preserve the current Open Workspace UI skeleton. Make Attach File use
that same skeleton and window geometry, adapted for files and multi-selection.

This is a path-browser project, not a general search rewrite. It must remain
inside the persistent file tree, workspace browser, and attachment-picker
scope.

## Approval Gate

This plan was manually approved by the user on 2026-09-04. Implementation may
proceed, but every production change must remain within the scope and
acceptance contract below. Changes outside the scope require a new review.

## Scope Lock

### In scope

- The persistent workspace file tree's recursive filename/path filtering.
- The New Workspace folder browser.
- The Attach File browser.
- A Crude-owned internal path-walking package derived from Fastwalk.
- Lazy, asynchronous directory expansion.
- Bounded, on-demand recursive filename and folder-path discovery.
- Crude-owned fuzzy path ranking and match highlighting.
- Clickable breadcrumbs.
- Multi-file attachment selection.
- Picker request ownership, cancellation, generation checks, and resource
  budgets.
- Removal of the permanent `Parent folder` / `..` pseudo-resource row.

### Out of scope

- `grep_files`.
- `glob_files`.
- Agent tool definitions, dispatch, prompts, results, and rendering.
- Find in Files content search.
- File-content search of any kind.
- Semantic search, BM25, vector search, embeddings, or persistent content
  indexes.
- File previews.
- Git status in either picker.
- LSP diagnostics in either picker.
- Picker-owned filesystem watchers.
- External executables, services, runtimes, FFI, CGo, WASM, or Node.js.
- New third-party Go module dependencies.

The current agent tools must remain behaviorally and structurally untouched.
Repository-wide searches during implementation must verify that no diff lands
in `agent/`, agent-facing tool definitions, or the existing grep/glob paths.

## Product Decisions

1. New Workspace keeps its current visual design. Only its flat folder list is
   replaced by the new tree browser and its plain breadcrumb becomes
   interactive.
2. Attach File adopts the same large window geometry, header structure,
   spacing, breadcrumb position, tree area, and action-bar arrangement as New
   Workspace.
3. Attach File shows files and folders. New Workspace shows folders only.
4. Neither picker has a preview pane.
5. Neither picker initializes Git, diagnostics, an LSP connection, or a
   filesystem watcher.
6. Normal browsing is shallow and lazy. Recursive discovery begins only after
   a non-empty search query.
7. Search covers names and relative paths only. It never reads file contents.
8. The permanent `Parent folder` / `..` row is deleted. Breadcrumbs and normal
   navigation keys own parent navigation.
9. Search discovery and query ranking are separate jobs. Typing another
   character must not restart the recursive filesystem walk.
10. The internal walker has explicit small resource limits. It never scales
    itself to all available CPU cores.

## Preserved Window Contract

Keep the existing `OverlayFilePicker`, `OverlayHost`, `FloatingWindow`, and
`WindowFrame` ownership.

Both picker modes use the current New Workspace window contract:

- Preferred size: 96 columns by 28 rows.
- Minimum size: 60 columns by 18 rows.
- `ui.FrameWindow` chrome.
- Draggable title bar.
- Resizable floating window.
- Centered initial placement.
- Modal click-outside ignore-and-consume behavior.
- Existing theme and token propagation.
- Search field receives initial focus.
- Attach File continues to leave the chat composer mounted behind the modal.

The small 68 by 20 Attach File dialog is retired. Attach File becomes the same
large browser window as New Workspace so both workflows feel like one IDE
surface.

## Shared Visual Skeleton

The current New Workspace body structure remains authoritative:

```text
+----------------------------------------------------------------------------------------------+
| Window title                                                                            [x]  |
+----------------------------------------------------------------------------------------------+
| ◆ crude   OPEN WORKSPACE / ATTACH FILES                                                     |
| Mode-specific descriptive subtitle                                                          |
|----------------------------------------------------------------------------------------------|
| [ Search field                                                                     search ] |
|                                                                                              |
| [root] > [home] > [lenovo] > [ocd] > [current]                         count / status        |
|                                                                                              |
| Tree                                                                                         |
|                                                                                              |
|                                                                                              |
|                                                                                              |
|                                                                                              |
|                                                                                              |
|                                                                                              |
|                                                                                              |
|                                                                                              |
|                                                                                              |
|----------------------------------------------------------------------------------------------|
| [Primary action]                                                              [Cancel]       |
+----------------------------------------------------------------------------------------------+
```

The following existing details stay unchanged unless this plan explicitly
says otherwise:

- Three-row branded header in folder mode.
- Title typography and color roles.
- Subtitle placement.
- Header divider.
- Search-field row and trailing search glyph.
- Breadcrumb/status row position.
- Popup background and separator colors.
- Bottom action bar and button variants.
- Keyboard focus loop through search, tree, breadcrumb, and actions.

## Permanent Parent Row Removal

The current picker manufactures an entry named `..` and renders it as `Parent
folder`. That pseudo-resource is removed in both modes.

Before:

```text
↰ Parent folder
▸ app
▸ docs
▸ tui
```

After:

```text
├─ ▸ app
├─ ▸ docs
└─ ▸ tui
```

Parent navigation remains available through:

- A clickable ancestor breadcrumb.
- `Backspace`.
- `Alt+Left`.
- `Left` on a collapsed top-level node.
- Editable path entry when the breadcrumb receives its path-edit action.

No command row may be presented as though it were a file or folder.

## Clickable Breadcrumb Contract

Replace the current plain `ui.Label` breadcrumb with one retained component
whose segments have independent layout, semantics, hover, focus, and hit
regions.

```text
[⌂ /] > [home] > [lenovo] > [ocd] > [crude]                 17 folders
```

Required behavior:

- Every path segment is independently clickable.
- Separators are visual only and never receive pointer input.
- The current segment uses emphasized foreground.
- A hovered segment uses the normal product hover background.
- Clicking a segment navigates directly to that directory.
- Clicking the root segment navigates to the filesystem root.
- Windows drive roots remain one segment, for example `[C:\]`.
- UNC server and share roots remain valid and are not split incorrectly.
- `Backspace` navigates to the immediate parent.
- Breadcrumb navigation cancels obsolete directory and search requests.
- Breadcrumb navigation creates a new picker intent sequence.
- A stale result cannot replace a newer breadcrumb destination.
- Breadcrumb segments participate in keyboard focus and expose semantic labels.
- The status/count remains right-aligned on the same row.
- An inline error temporarily replaces the count without moving the tree.

Long paths preserve the root and the most relevant trailing segments:

```text
[⌂ /] > [...] > [projects] > [crude] > [tui]
```

The `...` segment is clickable and opens a compact menu containing the hidden
ancestors. Truncation and hit geometry must use the repository's canonical
terminal-width path from
[`../reference/terminal-width.md`](../reference/terminal-width.md).

## New Workspace Design

New Workspace remains visually the same product surface, with a real folder
tree replacing the flat folder list.

```text
+----------------------------------------------------------------------------------------------+
| New Workspace                                                                          [x]  |
+----------------------------------------------------------------------------------------------+
| ◆ crude   OPEN WORKSPACE                                                                    |
| Choose a project folder · fuzzy-search as you type                                           |
|----------------------------------------------------------------------------------------------|
| [ Search folders                                                                  search ]  |
|                                                                                              |
| [⌂ /] > [home] > [lenovo] > [ocd]                                      12 folders           |
|                                                                                              |
| ├─ ▾ crude/                                                                                 |
| │  ├─ ▸ app/                                                                                |
| │  ├─ ▸ docs/                                                                               |
| │  ├─ ▸ provider/                                                                           |
| │  ├─ ▾ tui/                                                                                |
| │  │  ├─ ▸ filetree/                                                                        |
| │  │  └─ ▸ screens/                                                                         |
| │  └─ ▸ workspace/                                                                          |
| ├─ ▸ projects/                                                                              |
| └─ ▸ experiments/                                                                           |
|                                                                                              |
|                                                                                              |
|----------------------------------------------------------------------------------------------|
| [Open Workspace]                                                              [Cancel]       |
+----------------------------------------------------------------------------------------------+
```

### New Workspace tree behavior

- Render directories only.
- Do not load file rows and then hide them; directory enumeration should avoid
  projecting files into the model.
- The initial directory loads asynchronously and shallowly.
- Expanding a folder reads only that folder.
- Collapsing a folder does not trigger filesystem work.
- The root folder itself is a valid selection even though the tree paints its
  children.
- The tree uses the normal file-tree folder icons, branch rails, indentation,
  disclosure glyphs, cursor, hover, truncation, horizontal scroll, and
  scrollbar.
- No Git resolver is constructed.
- No diagnostic resolver is constructed.
- No picker watcher is created.
- No repository detection is performed.

### New Workspace selection behavior

- A single click focuses and selects a folder.
- Clicking the disclosure region expands or collapses the folder.
- Double-clicking a folder expands it.
- `Enter` expands or collapses the focused folder.
- `Space` chooses the focused folder.
- `Left` collapses an expanded folder; on a collapsed folder it moves to the
  parent.
- `Right` expands a collapsed folder; on an expanded folder it moves to the
  first child.
- `Ctrl+Enter` confirms the selected folder.
- The primary action confirms the selected folder, or the current breadcrumb
  directory when no child is selected.
- Failed navigation keeps the previous valid directory and selection.
- Typed paths are validated off the UI thread.
- Relative typed paths resolve from the current breadcrumb directory.
- Absolute paths, drive roots, and UNC paths remain supported.
- Confirmation uses the existing workspace-switch lifecycle and generation
  boundary.

### New Workspace folder search

- Search folder names and normalized relative folder paths.
- Never read file contents.
- Never return files.
- Search unloaded descendants after the query becomes non-empty.
- Paint folder results with the same folder row painter and icons.
- Show a muted relative parent path only when needed to disambiguate results.
- Highlight matched runes in both the folder name and displayed path.
- Activating a result exits search, reveals the real folder in the tree, and
  selects it.
- Clearing search restores the previous expansion, cursor, and scroll state.
- Search must not permanently auto-expand unrelated branches.

Example result presentation:

```text
▸ components                  tui/
▸ components                  web/src/
▸ component-system           docs/plans/
```

## Attach File Design

Attach File uses the exact New Workspace window geometry and body skeleton,
adapted for files, folders, and multi-selection.

```text
+----------------------------------------------------------------------------------------------+
| Attach Files                                                                           [x]  |
+----------------------------------------------------------------------------------------------+
| ◆ crude   ATTACH FILES                                                                       |
| Choose files to add to this conversation · fuzzy-search as you type                          |
|----------------------------------------------------------------------------------------------|
| [ Search files and folders                                                        search ]  |
|                                                                                              |
| [crude] > [tui] > [filetree]                                      8 items · 2 selected      |
|                                                                                              |
| ├─ ▾ filetree/                                                                              |
| │  ├─   fuzzy.go                                                                      ✓     |
| │  ├─   git.go                                                                              |
| │  ├─   icons.go                                                                            |
| │  ├─   tree.go                                                                       ✓     |
| │  ├─   tree_rows.go                                                                        |
| │  └─   watcher.go                                                                          |
| ├─ ▸ screens/                                                                               |
| └─ ▸ ui/                                                                                    |
|                                                                                              |
|                                                                                              |
|                                                                                              |
| Selected: fuzzy.go, tree.go                                                                  |
|----------------------------------------------------------------------------------------------|
| [Attach 2 Files]                                                               [Cancel]      |
+----------------------------------------------------------------------------------------------+
```

### Attach tree behavior

- Render files and directories.
- Directories expand and collapse.
- Files are selectable.
- Directories are navigation resources and cannot be attached.
- The tree uses the same visual geometry and icons as the persistent file
  tree.
- The trailing row-indicator area displays attachment selection, not Git or
  diagnostic state.
- No Git resolver is constructed.
- No diagnostic resolver is constructed.
- No picker watcher is created.
- Browsing and filename search never read file contents.
- Detailed metadata is fetched only when selected-file validation requires it.

### Attach selection behavior

- `Space` toggles the focused file.
- Clicking a file focuses it; activation toggles selection.
- Direct disclosure clicks never toggle a file selection.
- Shift-click selects a contiguous range of visible file rows and skips
  directories.
- `Ctrl+A` selects all visible file results and skips directories.
- `Ctrl+Shift+A` clears all selections.
- `Ctrl+Enter` confirms valid selections.
- Selections are keyed by cleaned absolute path.
- Duplicate selections are impossible.
- Selections survive expansion, collapse, directory navigation, and search
  query changes.
- A deleted, unreadable, or changed selected path becomes visibly invalid and
  blocks confirmation rather than disappearing silently.
- Confirmation revalidates only selected paths.
- Existing reference creation and attachment capability checks remain the
  confirmation destination.

Selected files use a restrained trailing marker:

```text
├─   fuzzy.go                                                        ✓
├─   tree.go                                                         ✓
└─   tree_rows.go
```

The summary is one compact row, not a side panel or preview:

```text
Selected: fuzzy.go, tree.go, icons.go, +4 more
```

Visible names in the summary are individually clickable for removal when
there is room. Narrow layouts retain the total count and omit names before
compromising the action bar.

### Attach path search

- Search file names, folder names, and normalized relative paths.
- Never search file content.
- Files and folders discovered below unloaded directories may appear.
- Files are directly selectable from search results.
- Folders are navigation results.
- Filename is the primary label.
- Relative parent path uses muted text when width permits.
- Match positions are highlighted in the filename and displayed path.
- Normal browsing sorts directories first.
- Search mode sorts by relevance; kind is only a tie-breaker.
- Activating a folder exits search and reveals that directory in the tree.
- Activating a file toggles attachment selection.
- Clearing search restores the previous tree location, expansion, cursor, and
  scroll state.

Example:

```text
Search: [ trw ]

├─   tree_rows.go                                      tui/filetree/    ✓
├─   tree_rows_test.go                                 tui/filetree/
├─   tree.go                                           tui/filetree/
└─   tree_width_test.go                                tui/filetree/
```

## Shared Tree Model

Do not mount `shelltree.Screen` inside the picker. It owns persistent sidebar
chrome and workspace-only behavior. Reuse the underlying `ui.Tree`, row
painter, icon resolver, scroll model, and file-tree node geometry.

Introduce explicit capabilities at model construction:

```go
type TreeOptions struct {
	ShowFiles       bool
	SelectFiles     bool
	SelectFolders   bool
	MultiSelect     bool
	ShowIcons       bool
	ShowGitStatus   bool
	ShowDiagnostics bool
	EnableWatcher   bool
	FollowSymlinks  bool
}
```

Expected configurations:

```go
// Persistent workspace file tree.
TreeOptions{
	ShowFiles:       true,
	SelectFiles:     true,
	ShowIcons:       true,
	ShowGitStatus:   true,
	ShowDiagnostics: true,
	EnableWatcher:   true,
}

// New Workspace.
TreeOptions{
	ShowFiles:     false,
	SelectFolders: true,
	ShowIcons:     true,
}

// Attach Files.
TreeOptions{
	ShowFiles:     true,
	SelectFiles:   true,
	MultiSelect:   true,
	ShowIcons:     true,
}
```

Disabled capabilities must prevent construction and work. They may not merely
hide already-computed output.

The reusable tree model owns only presentation state:

- Nodes and O(1) path lookup.
- Expansion and collapse.
- Visible-row flattening.
- Cursor, hover, selection, and multi-selection.
- Scroll offsets.
- Filter/search projection state.
- Match positions.
- Loading, empty, and per-node error states.

The tree model does not perform filesystem I/O. Workspace-owned browser
results are applied to it through neutral directory and search-result records.

## Internal Pathwalk Import

Fastwalk is source intake, not a new module dependency.

After approval, use this intake procedure:

1. Shallow-clone `https://github.com/charlievieth/fastwalk` with depth one into
   `/tmp/kilo/fastwalk`.
2. Record the exact upstream commit ID in the internal package documentation.
3. Audit the copied source and tests before editing.
4. Copy only the directory-walking implementation and relevant tests into
   `third_party/pathwalk/`.
5. Rename the Go package from `fastwalk` to `pathwalk`.
6. Rewrite package references and internal imports to Crude-owned paths.
7. Remove examples and APIs not required by this plan.
8. Preserve the upstream MIT `LICENSE`.
9. Preserve the Go Authors license notice used by the borrowed
   `golang.org/x/tools/internal/fastwalk` portions as `GO-LICENSE`.
10. Verify no production import references
    `github.com/charlievieth/fastwalk`.
11. Verify `go.mod` and `go.sum` gain no new module.

The internal package should contain a provenance comment describing the source
repository, pinned commit, local modifications, and both applicable licenses.

Proposed layout:

```text
third_party/pathwalk/
├── walk.go
├── config.go
├── dirent.go
├── read-dir-unix.go
├── read-dir-windows.go
├── errors.go
├── walk_test.go
├── cancellation_test.go
├── bounds_test.go
├── platform_test.go
├── LICENSE
└── GO-LICENSE
```

Filenames may follow the audited upstream platform split where Go build tags
require a different organization.

## Internal Pathwalk Contract

The rest of Crude must not depend on copied upstream public types. Expose a
small Crude-owned contract:

```go
type Request struct {
	Root           string
	Kind           EntryKind
	IncludeHidden  bool
	FollowSymlinks bool
	MaxDepth       int
	MaxWorkers     int
	MaxPendingDirs int
	MaxEntries     int
	MaxPathBytes   int64
}

type EntryKind uint8

const (
	EntriesAll EntryKind = iota
	EntriesDirectories
	EntriesFiles
)

type Entry struct {
	Path      string
	Relative  string
	Name      string
	Depth     int
	IsDir     bool
	IsSymlink bool
}

type Stats struct {
	Directories int
	Files       int
	Skipped     int
	Errors      int
	Truncated   bool
}

type VisitFunc func(Entry) error

func Walk(ctx context.Context, req Request, visit VisitFunc) (Stats, error)
```

Size and modification time are intentionally absent from ordinary path
discovery. They require extra metadata calls and are not needed for fuzzy path
search. Selected-file validation may use a separate targeted stat path.

### Pathwalk behavior

- Resolve and clean the root once.
- Keep native filesystem paths for I/O.
- Produce slash-normalized relative paths for ranking and display.
- Sort entries inside each directory for stable local behavior.
- Do not promise global callback order from parallel traversal.
- Apply deterministic ordering in the consumer before publishing final ranked
  results.
- Never follow symlinks by default.
- Detect ancestor cycles when explicit following is enabled in future code.
- Treat inaccessible children as partial errors unless the root itself fails.
- Never skip the explicitly requested root because of its basename.
- Check context before reading a directory, after reading it, and before
  publishing each bounded batch.
- Stop scheduling directories as soon as cancellation is observed.
- Never read regular-file contents.
- Never import workspace, TUI, Bubble Tea, agent, or provider packages.

## Discovery and Ranking Separation

Recursive discovery must not restart on every keystroke.

There are two independent generations:

```text
Discovery generation = root + visibility policy + entry kind
Ranking generation   = discovery generation + current query
```

Behavior:

- Opening a picker performs no recursive discovery.
- Entering the first non-empty query starts one discovery job.
- Discovery publishes bounded path batches.
- Every arriving batch is ranked against the latest query generation.
- Changing the query cancels only obsolete ranking work.
- Changing the query does not restart an active compatible directory walk.
- Clearing the query returns to normal tree browsing and cancels incomplete
  recursive discovery to stop unnecessary resource use.
- A completed catalog may remain in the open picker's bounded cache.
- Changing root, visibility policy, picker mode, workspace, or open epoch
  cancels discovery and ranking.
- Closing the picker releases its external-root catalog.

This design prevents fast typing from creating many simultaneous recursive
walks.

## Resource Budget

Picker path discovery uses explicit conservative defaults:

| Resource | Default bound |
|----------|---------------|
| Directory workers | 2 |
| Ranking workers | 1 |
| Pending directory queue | 128 |
| Result channel batches | 4 buffered batches |
| Entries per published batch | 128 |
| Retained path entries | 250,000 |
| Retained normalized path text | 32 MiB |
| Ranked results retained per query | 2,000 |
| Symlink following | Disabled |
| File-content reads | Zero |

The implementation may tighten a bound if benchmarks show a lower value is
enough. It may not increase a bound or derive it from total CPU count merely to
make a benchmark faster.

Additional resource rules:

- Never use `runtime.NumCPU()` or all available cores as the picker default.
- Never use sleep-based throttling or polling.
- Use bounded channels and context-aware sends for backpressure.
- Never create an unbounded goroutine per directory or result.
- Never hold regular files open during path discovery.
- Close directory handles promptly.
- Keep only the active workspace catalog and current external picker catalog.
- Do not persist arbitrary external filesystem catalogs.
- Show a restrained `results limited` status when an entry, byte, or result
  bound is reached.
- Virtualize result rows; retained search results must not imply retained paint
  buffers for every result.

## Fuzzy Path Ranking

Implement ranking inside Crude without adding a fuzzy-search dependency.

Ranking precedence:

1. Exact filename, case-sensitive.
2. Exact filename, case-insensitive.
3. Filename prefix.
4. Exact relative path.
5. Path-segment prefix.
6. Contiguous filename substring.
7. Word-boundary or camel-case sequence.
8. General subsequence.

Bonuses:

- Earlier match.
- Consecutive matched runes.
- Filename match over parent-only match.
- Path-segment boundary.
- Shallower path.
- Shorter candidate when match quality is otherwise equal.

Penalties:

- Long unmatched gaps.
- Deep irrelevant parent segments.
- Parent-only matches.
- Excessively long relative paths.

Tie-breaking is deterministic:

1. Score descending.
2. Mode-specific kind preference.
3. Relative path ascending.
4. Canonical path ascending.

The matcher returns rune positions for highlighting. It does not compute
terminal cell width; painting remains on the canonical rendering width path.

## Workspace Browser Service

Workspace owns filesystem request execution and lifecycle. Add a browser
service or equivalent cohesive owner with neutral contracts for:

- Root validation.
- Shallow directory listing.
- Recursive path discovery through `third_party/pathwalk`.
- Discovery cancellation.
- Ranking cancellation.
- Request generation.
- Bounded path-catalog ownership.
- Result publication.
- Targeted selected-path validation.

The request identity must include:

```text
picker instance
picker mode
open epoch
intent sequence
workspace-switch generation
root path
discovery generation
ranking generation
query
```

Every result is dropped unless all ownership fields still match. The picker
screen never calls `os.ReadDir`, `filepath.WalkDir`, or `pathwalk.Walk` from
`Update` or `Paint`.

## Persistent File Tree Integration

The persistent file tree remains visually and behaviorally intact outside
recursive filtering.

Changes allowed by this plan:

- Replace synchronous progressive crawling of unloaded directories during
  filtering with the bounded path-discovery service.
- Preserve the current selected path, expanded nodes, hover, cursor, and
  scroll.
- Materialize matching ancestors as projection nodes without permanently
  expanding the source tree.
- Update a completed active-workspace catalog from existing watcher events
  rather than creating another watcher.
- Reuse the same fuzzy ranker and match-position representation as the
  pickers.

Changes forbidden by this plan:

- Removing Git status from the persistent sidebar.
- Removing diagnostics from the persistent sidebar.
- Replacing its existing watcher with picker-owned polling.
- Changing file activation or context-menu behavior.
- Routing persistent tree I/O through agent tools.

## TUI and Workspace Ownership

| Layer | Ownership |
|-------|-----------|
| `third_party/pathwalk` | Bounded recursive path enumeration only |
| `workspace` | Browser requests, path validation, cancellation, generations, catalogs, result publication |
| `tui/filetree` | Tree nodes, projection, row paint, icons, cursor, hover, selection, scrolling |
| `tui/screens/filepicker` | Existing skeleton, breadcrumb, search field, mode configuration, summary, actions |
| `tui/viewmodel` | Overlay lifecycle and routing confirmed selections to existing workspace/attachment methods |

There are no callbacks across application layers. TUI actions call workspace
methods; workspace results return through the established message/event path.

## Required UI States

Both picker modes need explicit states without changing the skeleton:

- Initial shallow directory loading.
- Child directory loading.
- Recursive path discovery active.
- Ranking active.
- Empty directory.
- No search matches.
- Root permission denied.
- Child permission denied.
- Invalid typed path.
- Path deleted during browsing.
- Search result limit reached.
- Discovery canceled.
- Selection invalidated.

Loading and errors occupy tree rows or the existing breadcrumb status area.
They must not add a preview pane, extra permanent header, or second footer.

## Removal Set

After both modes use the new tree browser, remove:

- The picker-specific flat `ui.List`.
- `filepicker.Entry` if no neutral adapter remains necessary.
- The picker-local `fuzzyScore` implementation.
- The plain breadcrumb label.
- The manufactured `Parent folder` / `..` entry.
- `workspace.DirectoryEntry` and `workspace.DirectoryListing` after all
  consumers move to the neutral browser contract.
- `pickerEntries`.
- Picker-specific one-directory result translation.
- Obsolete picker tests that assert flat-list-only behavior.

Preserve:

- `OverlayFilePicker`.
- Existing overlay modal policy.
- Existing launcher New Workspace action.
- Existing chat Attach File action and shortcut.
- Existing attachment reference creation.
- Existing workspace-switch safety.
- Existing theme consumer registration.

## Validation Contract

### Provenance and dependency validation

- Internal source records the exact Fastwalk commit.
- MIT and Go Authors licenses are present.
- No import references the upstream Fastwalk module.
- `go.mod` and `go.sum` add no dependency.
- No FFI, CGo, WASM, Node.js, or external executable is introduced.

### Tree and visual validation

- Picker rows match persistent file-tree rails, indentation, icons, cursor,
  hover, truncation, and scrollbar behavior.
- Git and diagnostic indicators never appear in either picker.
- Disabled picker capabilities perform no Git, diagnostic, LSP, or watcher
  work.
- New Workspace retains its current header, spacing, breadcrumb row, action
  bar, and preferred/minimum size.
- Attach File uses the same size and skeleton.
- No permanent parent pseudo-row is painted.
- Light and dark themes pass.
- Readable desktop and constrained terminal captures pass through
  `scripts/tuishot.sh`.

### Interaction validation

- Every breadcrumb segment has correct hit geometry.
- Breadcrumb root, middle, current, collapsed `...`, drive, and UNC cases pass.
- Folder expansion and collapse work by keyboard and mouse.
- New Workspace permits folders only.
- Attach File permits files only and supports multiple selection.
- Range selection skips directories.
- Search result activation reveals folders or toggles files as specified.
- Clearing search restores source tree state.
- `Esc` clears search before closing the picker.
- Action-bar focus and modal focus restoration remain correct.

### Lifecycle validation

- Initial open does not recursively scan.
- Empty search does not recursively scan.
- Folder expansion reads one directory only.
- Query changes do not restart compatible path discovery.
- Query changes cannot apply stale rankings.
- Breadcrumb navigation cancels obsolete work.
- Root change cancels obsolete work.
- Close/reopen rejects stale results from the prior epoch.
- Workspace switch rejects old picker and catalog results.
- Failed navigation preserves the prior valid tree and selection.

### Resource validation

- Directory workers never exceed two under default picker policy.
- Ranking workers never exceed one per active picker.
- Pending directory and result queues remain bounded.
- Entry, path-byte, and ranked-result limits are enforced.
- Path discovery performs zero regular-file content reads.
- No goroutine or file-descriptor leak occurs after cancellation.
- Dense path search remains responsive on the UI thread.
- Empty, small, and dense synthetic trees have separate benchmarks.
- Benchmarks measure discovery latency, ranking latency, first-result latency,
  total allocations, and cancellation latency.

### Regression validation

- Agent-facing files and tool definitions have no diff.
- Existing `grep_files` and `glob_files` behavior is unchanged.
- Find in Files content search is unchanged.
- Persistent file-tree Git, diagnostics, watcher, activation, and context menus
  remain intact.
- Full build, focused tests, race tests, full repository tests, lint, docs, and
  visual acceptance all pass before completion.

## Completion Definition

The work is complete only when:

- New Workspace looks like the current New Workspace surface and uses the new
  folder tree.
- Attach File uses the same surface geometry and uses the file-capable tree.
- Breadcrumbs are genuinely clickable and keyboard accessible.
- The permanent parent pseudo-row is gone.
- Recursive name/path search is bounded, cancellable, deterministic, and
  shared with persistent file-tree filtering.
- Opening or casually browsing a picker performs only shallow on-demand work.
- No preview, content search, semantic search, Git picker work, diagnostic
  picker work, agent change, external dependency, or native runtime has been
  introduced.

Manual approval of this document is required before any implementation begins.
