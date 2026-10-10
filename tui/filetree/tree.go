// Package filetree provides the file tree sidebar widget.
//
// This file implements the core tree data model: TreeNode, FileTreeModel,
// lazy directory loading, expand/collapse, filtering, and sorting.
package filetree

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"

	tea "charm.land/bubbletea/v2"

	"crude/tui/ui"
)

// ---------------------------------------------------------------------------
// SortOption
// ---------------------------------------------------------------------------

// SortOption controls how tree nodes are ordered within a directory.
type SortOption int

const (
	// SortByName sorts children alphabetically by name.
	SortByName SortOption = iota
	// SortByType sorts directories first, then files, then alphabetically.
	SortByType
	// SortByModified sorts by modification time (most recent first).
	SortByModified
)

// ---------------------------------------------------------------------------
// TreeNode
// ---------------------------------------------------------------------------

// DiagCounts holds LSP diagnostic counts for a single file.
type DiagCounts struct {
	Errors   int
	Warnings int
	Hints    int
}

// TreeNode represents a single entry in the file tree.
type TreeNode struct {
	// Name is the display name (filename without path).
	Name string

	// Path is the absolute file system path.
	Path string

	// IsDir is true if this entry is a directory.
	IsDir bool

	// IsSymlink is true if this entry is a symbolic link.
	IsSymlink bool

	// IsExpanded controls whether a directory's children are shown.
	IsExpanded bool

	// Depth is the nesting level (0 = root of the workspace).
	Depth int

	// Children contains the sorted child nodes.
	// nil or empty for files; lazy-loaded for directories.
	Children []*TreeNode

	// Parent points to the parent node. nil for the root.
	Parent *TreeNode

	// GitStatus carries the git status for this node.
	GitStatus GitStatus

	// AggregateStatus is the most severe git status among all descendants.
	// Computed recursively after any tree mutation. GitNone if no descendant
	// has any status. Only meaningful for directories.
	AggregateStatus GitStatus

	// Loading is true when children are being fetched (async).
	Loading bool

	// Filtered is true when this node does not match the active filter.
	Filtered bool

	// Selected is a secondary selection mark used by multi-select picker
	// projections. The persistent sidebar leaves it false and uses the cursor.
	Selected bool

	// Hidden is true for dot-files when ShowHidden is false.
	Hidden bool

	// ModTime is the file modification time, used by SortByModified.
	ModTime time.Time

	// DiagnosticCount aggregates LSP diagnostic counts for this node.
	// For files: the count of diagnostics on this file.
	// For directories: the sum of all descendant diagnostic counts
	// (computed by recomputeAggregateDiagnostics).
	DiagnosticCount int
	// DiagErrors is the count of error-level diagnostics.
	DiagErrors int
	// DiagWarnings is the count of warning-level diagnostics.
	DiagWarnings int
	// DiagHints is the count of info/hint-level diagnostics.
	DiagHints int
}

// DirectoryEntry is filesystem metadata supplied by an asynchronous browser
// request. It intentionally contains no presentation or workspace services.
type DirectoryEntry struct {
	Name      string
	Path      string
	IsDir     bool
	IsSymlink bool
	ModTime   time.Time
}

// ModelOptions controls optional services and projections for a tree model.
// The persistent sidebar uses the default service-rich configuration; picker
// models use the no-I/O, no-Git, no-diagnostics configuration.
type ModelOptions struct {
	DirectoriesOnly bool
	EnableGit       bool
	InitialLoad     bool
	// NoLocalIO prevents filesystem resolution, reads, Git and watcher startup.
	NoLocalIO bool
}

// recomputeAggregateStatuses recomputes AggregateStatus for every loaded
// directory node by scanning the flat git status map (which knows about ALL
// files in the repo, not just expanded children) and propagating each status
// up to the nearest loaded ancestor directory.
//
// This is O(git_statuses * depth) and scales to very large repos because it
// never walks unloaded subtrees — it uses the git resolver's complete map.
func (m *FileTreeModel) recomputeAggregateStatuses() {
	if m.Root == nil {
		return
	}
	statuses, repoPath := m.remoteGitStatuses, m.rootPath
	if m.gitResolver != nil {
		statuses, repoPath = m.gitResolver.AllStatuses(), m.gitResolver.RepoPath()
	} else if statuses == nil {
		return
	}

	// Clear all existing aggregate statuses.
	var clearAgg func(n *TreeNode)
	clearAgg = func(n *TreeNode) {
		n.AggregateStatus = GitNone
		if n.IsDir {
			for _, c := range n.Children {
				clearAgg(c)
			}
		}
	}
	clearAgg(m.Root)

	// For each git status entry, walk up the directory hierarchy until we
	// find a loaded node in m.nodes, then propagate up to root.
	for relPath, status := range statuses {
		if status == GitNone {
			continue
		}
		absPath := relPath
		if !filepath.IsAbs(absPath) {
			absPath = filepath.Join(repoPath, relPath)
		}
		if !m.containsPath(absPath) {
			continue
		}
		dir := filepath.Dir(absPath)
		for {
			if node, ok := m.nodes[dir]; ok {
				if gitStatusPriority(status) > gitStatusPriority(node.AggregateStatus) {
					node.AggregateStatus = status
				}
				// Propagate up to root.
				for p := node.Parent; p != nil; p = p.Parent {
					if gitStatusPriority(node.AggregateStatus) > gitStatusPriority(p.AggregateStatus) {
						p.AggregateStatus = node.AggregateStatus
					}
				}
				break
			}
			parent := filepath.Dir(dir)
			if parent == dir {
				break
			}
			dir = parent
		}
	}
}

// ---------------------------------------------------------------------------
// Diagnostic aggregation
// ---------------------------------------------------------------------------

// SetDiagnostics updates the per-file diagnostic counts for the given path
// and recomputes aggregates for all affected ancestor directories.
// This method is safe for concurrent calls (it acquires the model's write lock).
func (m *FileTreeModel) SetDiagnostics(path string, errors, warnings, hints int) {
	m.mu.Lock()
	defer m.mu.Unlock()

	fp := filepath.Clean(path)
	if errors+warnings+hints <= 0 {
		delete(m.diagnosticResolver, fp)
		m.recomputeAggregateDiagnostics()
		return
	}
	m.diagnosticResolver[fp] = DiagCounts{Errors: errors, Warnings: warnings, Hints: hints}

	// If the tree node doesn't exist yet (file is in a collapsed directory),
	// load the parent directory so the node becomes available.  The directory
	// stays collapsed — we only materialise the tree node so its diagnostic
	// counts are set and propagated upward for aggregate display.
	if _, ok := m.nodes[fp]; !ok && !m.noLocalIO && m.containsPath(fp) {
		m.ensureDiagnosticsNode(fp)
	}

	m.recomputeAggregateDiagnostics()
}

// ClearDiagnostics removes all per-file diagnostic counts and recomputes the
// loaded tree aggregates. It is used when the LSP manager is restarted or the
// workspace changes so stale diagnostics do not survive the clean slate.
func (m *FileTreeModel) ClearDiagnostics() {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.diagnosticResolver = make(map[string]DiagCounts)
	m.recomputeAggregateDiagnostics()
}

// ensureDiagnosticsNode recursively loads parent directories until the
// given file path has a node in m.nodes.  It does NOT expand any directory
// — children are loaded but the dir remains collapsed.
func (m *FileTreeModel) ensureDiagnosticsNode(fp string) {
	parentPath := filepath.Dir(fp)
	if parentPath == fp || parentPath == "." {
		return // at filesystem root, can't go higher
	}
	parentNode, ok := m.nodes[parentPath]
	if !ok {
		// Grandparent not loaded yet — recurse.
		m.ensureDiagnosticsNode(parentPath)
		parentNode, ok = m.nodes[parentPath]
	}
	if ok && parentNode.Children == nil {
		_ = m.loadDirectory(parentNode)
	}
}

// recomputeAggregateDiagnostics recomputes DiagnosticCount/DiagErrors/
// DiagWarnings/DiagHints for every loaded directory node by scanning the
// per-file diagnostic counts and propagating them up to root.
func (m *FileTreeModel) recomputeAggregateDiagnostics() {
	// Clear all existing aggregate diagnostic counts.
	var clearDiag func(n *TreeNode)
	clearDiag = func(n *TreeNode) {
		n.DiagnosticCount = 0
		n.DiagErrors = 0
		n.DiagWarnings = 0
		n.DiagHints = 0
		if n.IsDir {
			for _, c := range n.Children {
				clearDiag(c)
			}
		}
	}
	clearDiag(m.Root)

	// Status may arrive before a directory page. Project unloaded descendants
	// onto their nearest loaded ancestor without reading disk or inventing rows.
	for path, counts := range m.diagnosticResolver {
		if !m.containsPath(path) {
			continue
		}
		for current := path; ; current = filepath.Dir(current) {
			if node := m.nodes[current]; node != nil {
				for p := node; p != nil; p = p.Parent {
					p.DiagnosticCount += counts.Errors + counts.Warnings + counts.Hints
					p.DiagErrors += counts.Errors
					p.DiagWarnings += counts.Warnings
					p.DiagHints += counts.Hints
				}
				break
			}
			if parent := filepath.Dir(current); parent == current {
				break
			}
		}
	}
}

func (m *FileTreeModel) containsPath(path string) bool {
	rel, err := filepath.Rel(m.rootPath, path)
	return err == nil && rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator))
}

// IsLeaf returns true if this node is a file (not a directory).
func (n *TreeNode) IsLeaf() bool {
	return !n.IsDir
}

// HasChildren returns true if this node has loaded children.
func (n *TreeNode) HasChildren() bool {
	return len(n.Children) > 0
}

// ---------------------------------------------------------------------------
// FileTreeModel
// ---------------------------------------------------------------------------

// FileTreeModel is the core data model for the file tree.
//
// It maintains a tree of TreeNode instances rooted at the workspace root,
// provides O(1) path-based lookup, lazy directory loading, expand/collapse
// operations, filtering, sorting, and a flattened visible-nodes list for
// efficient cursor navigation.
type FileTreeModel struct {
	// Root is the root node of the tree (represents the workspace root dir).
	Root *TreeNode

	rootPath  string
	noLocalIO bool

	// nodes provides O(1) lookups from absolute path to TreeNode.
	nodes map[string]*TreeNode

	// selectedPath is the absolute path of the currently selected node.
	selectedPath string

	// cursorIndex is the current cursor position in the visibleNodes slice.
	cursorIndex int

	// Hover tracks which visible node the mouse cursor is currently
	// pointing at. -1 means no node is hovered (mouse off-tree or
	// over the scrollbar). Index is in the same space as cursorIndex
	// — i.e. an index into visibleNodes — so render code can compare
	// it directly to the loop variable.
	Hover ui.Hover

	// visibleNodes is a flattened, depth-first list of currently visible nodes.
	visibleNodes []*TreeNode
	// maxVisibleDepth is derived with visibleNodes; row painters use it to
	// keep one consistent guide width for the whole tree at each viewport width.
	maxVisibleDepth int

	// filterEd holds the active filter pattern (empty = no filter).
	filterEd *ui.TextEdit

	// filterMatchPos maps node path → rune positions in Name matched by the
	// active fuzzy filter. Rebuilt by applyFilterState on every SetFilter.
	filterMatchPos map[string][]int

	// filterSubtree maps node path → true when the node or any descendant
	// matches the active filter. Drives visibility and auto-expansion.
	filterSubtree map[string]bool

	// filterAutoExpanded records directories the filter expanded so
	// clearing the filter restores their collapsed state.
	filterAutoExpanded []string

	// sortBy controls how children are ordered within a directory.
	sortBy SortOption

	// showHidden controls whether dot-files (names starting with ".") appear.
	showHidden bool

	// directoriesOnly omits files from directory projections, used by the
	// workspace-folder picker.
	directoriesOnly bool

	// iconResolver resolves file/directory names to NerdFont/Unicode icons.
	iconResolver *IconResolver

	// gitResolver resolves git status (may be nil).
	gitResolver *GitStatusResolver
	// remoteGitStatuses retains the core's complete absolute-path projection,
	// including descendants whose directory rows have not arrived yet.
	remoteGitStatuses map[string]GitStatus
	// gitRefreshPending/gitRefreshQueued coalesce status scans while preserving
	// one trailing refresh when repository changes arrive during an active scan.
	gitRefreshPending bool
	gitRefreshQueued  bool
	gitRefreshGen     uint64

	// diagnosticResolver stores per-file diagnostic counts (may be nil).
	// The map key is the absolute file path.
	diagnosticResolver map[string]DiagCounts

	// watcher watches for file system changes (may be nil).
	watcher *Watcher
	// gitWatcherStop stops the repository-metadata watcher paired with watcher.
	gitWatcherStop func()

	// width is the available display width for the tree sidebar.
	width int

	// visibleHeight is the number of visible rows in the tree view.
	visibleHeight int

	// scrollOffset tracks how many rows are scrolled off the top of the view.
	scrollOffset int

	// filterActive indicates whether filter input mode is active.
	filterActive bool

	// hscrollOffset is the horizontal scroll offset for deeply nested items.
	hscrollOffset int

	// onFileChanged is called when a file changes on disk via the watcher.
	// The viewmodel sets this to forward events to the LSP server.
	onFileChanged func(filePath string, changeType int)
	// onGitChanged asks the viewmodel to schedule an asynchronous status scan.
	// The metadata watcher never performs repository I/O or mutates tree rows.
	onGitChanged func()

	// directoryLoader is installed only by asynchronous picker projections.
	// It returns a command that owns the filesystem request; the tree never
	// performs picker I/O itself.
	directoryLoader func(path string) tea.Cmd

	// onFileSelected adapts file activation for picker selection. The persistent
	// tree leaves this nil and emits FileSelectedMsg as before.
	onFileSelected func(path string) tea.Cmd

	// mu protects all mutable state for thread-safe access.
	mu sync.RWMutex
}

// NewFileTreeModel creates a FileTreeModel rooted at the given path.
//
// It initialises the icon resolver, attempts to create a git status resolver,
// loads the root directory, and builds the initial visible-nodes list.
func NewFileTreeModel(rootPath string) *FileTreeModel {
	return NewFileTreeModelWithOptions(rootPath, ModelOptions{EnableGit: true, InitialLoad: true})
}

// NewFileTreeModelWithOptions creates a tree with explicit service and loading
// behavior. InitialLoad=false performs no filesystem reads, which is required
// before a picker is mounted on the UI thread.
func NewFileTreeModelWithOptions(rootPath string, options ModelOptions) *FileTreeModel {
	// Resolve to an absolute path.
	absPath := filepath.Clean(rootPath)
	if !options.NoLocalIO {
		if resolved, err := filepath.Abs(rootPath); err == nil {
			absPath = resolved
		}
	}

	m := &FileTreeModel{
		rootPath:           absPath,
		noLocalIO:          options.NoLocalIO,
		nodes:              make(map[string]*TreeNode),
		sortBy:             SortByType,
		showHidden:         false,
		directoriesOnly:    options.DirectoriesOnly,
		iconResolver:       NewIconResolver(),
		diagnosticResolver: make(map[string]DiagCounts),
		Hover:              ui.NewHover(),
		filterEd:           ui.NewTextEdit(),
	}
	if options.EnableGit && !options.NoLocalIO {
		m.gitResolver = NewGitStatusResolver(absPath)
	}

	// Create the root node.
	var info os.FileInfo
	if !options.NoLocalIO && options.InitialLoad {
		info, _ = os.Stat(absPath)
	}
	root := &TreeNode{
		Name:       filepath.Base(absPath),
		Path:       absPath,
		IsDir:      true,
		Depth:      -1,
		IsExpanded: false,
		Children:   nil, // lazy load
	}
	if info != nil {
		root.ModTime = info.ModTime()
	}
	m.Root = root
	m.nodes[absPath] = root

	// Initially expand the root so the tree isn't empty once its children are
	// applied. Picker models leave it loading until their async root request
	// completes.
	root.IsExpanded = true
	if options.InitialLoad {
		_ = m.loadDirectory(root)
	} else {
		root.Loading = true
	}

	// Rebuild visible nodes.
	m.rebuildVisibleNodes()

	return m
}

// NewPickerFileTreeModel creates a tree projection with no filesystem I/O and
// no Git, diagnostics, or watcher services. Directory results are applied by
// SetDirectoryEntries after workspace-owned asynchronous reads complete.
func NewPickerFileTreeModel(rootPath string, directoriesOnly bool) *FileTreeModel {
	return NewFileTreeModelWithOptions(rootPath, ModelOptions{
		DirectoriesOnly: directoriesOnly,
		InitialLoad:     false,
	})
}

// ---------------------------------------------------------------------------
// Directory loading
// ---------------------------------------------------------------------------

// loadDirectory reads the contents of the given directory node and populates
// its Children slice. It is the core of the lazy-loading mechanism.
//
// If the node is not a directory, this is a no-op.
func (m *FileTreeModel) loadDirectory(node *TreeNode) error {
	if m.noLocalIO {
		return fmt.Errorf("remote tree requires asynchronous directory installation")
	}
	if node == nil || !node.IsDir {
		return nil
	}

	node.Loading = true

	entries, err := os.ReadDir(node.Path)
	if err != nil {
		node.Loading = false
		return err
	}

	// Build a lookup of existing children by name for re-use.
	existing := make(map[string]*TreeNode, len(node.Children))
	for _, child := range node.Children {
		existing[child.Name] = child
	}

	children := make([]*TreeNode, 0, len(entries))

	for _, entry := range entries {
		name := entry.Name()
		childPath := filepath.Join(node.Path, name)

		// Determine if hidden (dot-file).
		isHidden := strings.HasPrefix(name, ".")

		// Determine file type.
		isDir := entry.IsDir()
		isSymlink := false
		var modTime time.Time

		// For symlinks and mod time we need to stat.
		info, err := os.Lstat(childPath)
		if err == nil {
			isSymlink = info.Mode()&os.ModeSymlink != 0
			modTime = info.ModTime()

			// If it's a symlink to a directory, treat it as a directory.
			if isSymlink {
				targetInfo, err := os.Stat(childPath)
				if err == nil {
					isDir = targetInfo.IsDir()
				}
			}
		}
		if m.directoriesOnly && !isDir {
			continue
		}

		// Re-use existing node if available (updates git status).
		if existingNode, ok := existing[name]; ok {
			wasDir := existingNode.IsDir
			existingNode.IsDir = isDir
			existingNode.IsSymlink = isSymlink
			existingNode.Hidden = isHidden
			existingNode.ModTime = modTime
			if wasDir && !isDir {
				for _, child := range existingNode.Children {
					m.removeNodeIndex(child)
				}
				existingNode.Children = nil
				existingNode.IsExpanded = false
			}
			if m.gitResolver != nil {
				existingNode.GitStatus = m.gitResolver.Resolve(childPath)
			}
			children = append(children, existingNode)
			m.nodes[childPath] = existingNode
			delete(existing, name)
			continue
		}

		child := &TreeNode{
			Name:      name,
			Path:      childPath,
			IsDir:     isDir,
			IsSymlink: isSymlink,
			Depth:     node.Depth + 1,
			Parent:    node,
			Hidden:    isHidden,
			ModTime:   modTime,
			Children:  nil, // lazy
		}

		// Resolve git status if available.
		if m.gitResolver != nil {
			child.GitStatus = m.gitResolver.Resolve(childPath)
		}

		children = append(children, child)
		m.nodes[childPath] = child
	}

	// Sort children according to current sort mode.
	m.sortChildren(children)

	// Remove orphaned children (entries that existed before but no longer),
	// including every loaded descendant. Leaving descendants in the flat index
	// lets a deleted subtree remain selectable by path after its rows disappear.
	for _, orphan := range existing {
		m.removeNodeIndex(orphan)
	}

	node.Children = children
	node.Loading = false

	return nil
}

// SetDirectoryLoader installs the asynchronous child-directory request hook
// used by picker projections. The callback is never invoked while the model
// lock is held.
func (m *FileTreeModel) SetDirectoryLoader(fn func(string) tea.Cmd) {
	if m == nil {
		return
	}
	m.mu.Lock()
	m.directoryLoader = fn
	m.mu.Unlock()
}

// SetFileSelectedHandler adapts file activation for a parent projection such
// as Attach Files. A nil handler preserves the persistent sidebar behavior.
func (m *FileTreeModel) SetFileSelectedHandler(fn func(string) tea.Cmd) {
	if m == nil {
		return
	}
	m.mu.Lock()
	m.onFileSelected = fn
	m.mu.Unlock()
}

// MarkDirectoryLoading marks one loaded directory as awaiting an asynchronous
// child listing. It does not perform I/O.
func (m *FileTreeModel) MarkDirectoryLoading(path string) bool {
	if m == nil {
		return false
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	node, ok := m.nodes[filepath.Clean(path)]
	if !ok || !node.IsDir {
		return false
	}
	if node.Loading {
		return true
	}
	node.Loading = true
	node.IsExpanded = true
	m.rebuildVisibleNodes()
	return true
}

// SetDirectoryEntries applies an asynchronous listing to an existing node.
// Existing child nodes are reused so expansion and selection survive refreshes.
func (m *FileTreeModel) SetDirectoryEntries(path string, entries []DirectoryEntry) bool {
	if m == nil {
		return false
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	path = filepath.Clean(path)
	node, ok := m.nodes[path]
	if !ok || !node.IsDir {
		return false
	}
	existing := make(map[string]*TreeNode, len(node.Children))
	for _, child := range node.Children {
		if child != nil {
			existing[child.Name] = child
		}
	}
	children := make([]*TreeNode, 0, len(entries))
	for _, entry := range entries {
		if m.directoriesOnly && !entry.IsDir {
			continue
		}
		childPath := filepath.Clean(entry.Path)
		if childPath == "." || childPath == path || filepath.Dir(childPath) != path || filepath.Base(childPath) != entry.Name {
			continue
		}
		child := existing[entry.Name]
		if child == nil {
			child = &TreeNode{
				Name: entry.Name, Path: childPath, IsDir: entry.IsDir,
				IsSymlink: entry.IsSymlink, Depth: node.Depth + 1, Parent: node,
				Hidden: strings.HasPrefix(entry.Name, "."),
			}
		} else {
			child.Path = childPath
			child.IsDir = entry.IsDir
			child.IsSymlink = entry.IsSymlink
			child.Hidden = strings.HasPrefix(entry.Name, ".")
			child.Parent = node
			if !entry.IsDir {
				for _, descendant := range child.Children {
					m.removeNodeIndex(descendant)
				}
				child.Children = nil
				child.IsExpanded = false
			}
		}
		child.ModTime = entry.ModTime
		if m.remoteGitStatuses != nil {
			child.GitStatus = m.remoteGitStatuses[childPath]
		}
		children = append(children, child)
		m.nodes[childPath] = child
		delete(existing, entry.Name)
	}
	for _, orphan := range existing {
		m.removeNodeIndex(orphan)
	}
	m.sortChildren(children)
	node.Children = children
	node.Loading = false
	m.reapplyFilterAfterMutationLocked()
	m.rebuildVisibleNodes()
	return true
}

// SetDirectoryLoadError clears the loading state after a failed asynchronous
// listing while preserving the previous children for an already-loaded node.
func (m *FileTreeModel) SetDirectoryLoadError(path string) bool {
	if m == nil {
		return false
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	node, ok := m.nodes[filepath.Clean(path)]
	if !ok || !node.IsDir {
		return false
	}
	node.Loading = false
	m.rebuildVisibleNodes()
	return true
}

// SetSelectionMarks updates picker multi-selection indicators without changing
// the tree cursor selection.
func (m *FileTreeModel) SetSelectionMarks(paths map[string]bool) {
	if m == nil {
		return
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	for path, node := range m.nodes {
		node.Selected = paths[filepath.Clean(path)]
	}
}

// removeNodeIndex removes node and all loaded descendants from the path index.
// The caller must hold m.mu.
func (m *FileTreeModel) removeNodeIndex(node *TreeNode) {
	if node == nil {
		return
	}
	for _, child := range node.Children {
		m.removeNodeIndex(child)
	}
	if current, ok := m.nodes[node.Path]; ok && current == node {
		delete(m.nodes, node.Path)
	}
}

// sortChildren sorts a slice of TreeNode pointers according to the model's
// current sort mode.
func (m *FileTreeModel) sortChildren(children []*TreeNode) {
	sort.Slice(children, func(i, j int) bool {
		a, b := children[i], children[j]

		switch m.sortBy {
		case SortByName:
			return strings.ToLower(a.Name) < strings.ToLower(b.Name)

		case SortByModified:
			// Most recent first.
			return a.ModTime.After(b.ModTime)

		case SortByType:
			fallthrough
		default:
			// Directories first, then files. Alphabetical within each group.
			if a.IsDir != b.IsDir {
				return a.IsDir // directories before files
			}
			return strings.ToLower(a.Name) < strings.ToLower(b.Name)
		}
	})
}

// ---------------------------------------------------------------------------
// Expand / Collapse
// ---------------------------------------------------------------------------

// ToggleExpand expands a collapsed directory (loading children if needed) or
// collapses an expanded directory. Non-directory nodes are ignored.
func (m *FileTreeModel) ToggleExpand(path string) tea.Cmd {
	m.mu.Lock()
	node, ok := m.nodes[path]
	if !ok || !node.IsDir {
		m.mu.Unlock()
		return nil
	}

	if node.IsExpanded {
		node.IsExpanded = false
		m.rebuildVisibleNodes()
		m.mu.Unlock()
		return nil
	} else {
		// Load children if not already loaded.
		if node.Children == nil {
			if m.directoryLoader != nil {
				if node.Loading {
					m.mu.Unlock()
					return nil
				}
				node.Loading = true
				node.IsExpanded = true
				m.rebuildVisibleNodes()
				loader := m.directoryLoader
				m.mu.Unlock()
				return loader(path)
			}
			_ = m.loadDirectory(node)
		}
		node.IsExpanded = true
	}

	m.rebuildVisibleNodes()
	m.mu.Unlock()
	return nil
}

// Expand expands a specific directory node. Non-directory nodes are ignored.
// If children have not been loaded yet, they are loaded first.
func (m *FileTreeModel) Expand(path string) tea.Cmd {
	m.mu.Lock()
	node, ok := m.nodes[path]
	if !ok || !node.IsDir || node.IsExpanded {
		m.mu.Unlock()
		return nil
	}

	// Load children if not already loaded.
	if node.Children == nil {
		if m.directoryLoader != nil {
			if node.Loading {
				m.mu.Unlock()
				return nil
			}
			node.Loading = true
			node.IsExpanded = true
			m.rebuildVisibleNodes()
			loader := m.directoryLoader
			m.mu.Unlock()
			return loader(path)
		}
		_ = m.loadDirectory(node)
	}
	node.IsExpanded = true
	m.rebuildVisibleNodes()
	m.mu.Unlock()
	return nil
}

// Collapse collapses a specific directory node.
func (m *FileTreeModel) Collapse(path string) {
	m.mu.Lock()
	defer m.mu.Unlock()

	node, ok := m.nodes[path]
	if !ok || !node.IsDir || !node.IsExpanded {
		return
	}

	node.IsExpanded = false
	m.rebuildVisibleNodes()
}

// ExpandAll expands every directory node in the tree.
// WARNING: For large trees this may read the entire file system. Use with
// caution on repositories with many files.
func (m *FileTreeModel) ExpandAll() {
	m.mu.Lock()
	defer m.mu.Unlock()

	m.expandAllRecursive(m.Root)
	m.rebuildVisibleNodes()
}

func (m *FileTreeModel) expandAllRecursive(node *TreeNode) {
	if !node.IsDir {
		return
	}

	// Load children if not already loaded.
	if node.Children == nil {
		_ = m.loadDirectory(node)
	}
	node.IsExpanded = true

	for _, child := range node.Children {
		m.expandAllRecursive(child)
	}
}

// CollapseAll collapses every directory node, leaving the workspace root
// expanded so its children remain the explorer's top-level rows.
func (m *FileTreeModel) CollapseAll() {
	m.mu.Lock()
	defer m.mu.Unlock()

	m.collapseAllRecursive(m.Root)
	m.rebuildVisibleNodes()
}

func (m *FileTreeModel) collapseAllRecursive(node *TreeNode) {
	if !node.IsDir {
		return
	}
	if node != m.Root {
		node.IsExpanded = false
	}
	for _, child := range node.Children {
		m.collapseAllRecursive(child)
	}
}

// ---------------------------------------------------------------------------
// Selection and cursor navigation
// ---------------------------------------------------------------------------

// Select sets the currently selected path. If the path is not visible (or
// does not exist), the cursor is moved to the first visible node.
func (m *FileTreeModel) Select(path string) {
	m.mu.Lock()
	defer m.mu.Unlock()

	// Find the index in visible nodes.
	for i, node := range m.visibleNodes {
		if node.Path == path {
			m.setCursorIndexLocked(i)
			return
		}
	}

	// Path not visible — move cursor to first node.
	if len(m.visibleNodes) > 0 {
		m.setCursorIndexLocked(0)
		return
	}
	m.cursorIndex = 0
	m.selectedPath = m.rootPath
	m.scrollOffset = 0
}

// MoveUp moves the cursor up by one visible item.
func (m *FileTreeModel) MoveUp() {
	m.mu.Lock()
	m.clampCursorLocked()
	if m.cursorIndex > 0 {
		m.setCursorIndexLocked(m.cursorIndex - 1)
	}
	m.clampViewportLocked()
	m.mu.Unlock()
}

// MoveDown moves the cursor down by one visible item.
func (m *FileTreeModel) MoveDown() {
	m.mu.Lock()
	m.clampCursorLocked()
	if m.cursorIndex < len(m.visibleNodes)-1 {
		m.setCursorIndexLocked(m.cursorIndex + 1)
	}
	m.clampViewportLocked()
	m.mu.Unlock()
}

// SelectedNode returns the currently selected TreeNode, or nil if the tree
// is empty or the selection is invalid.
func (m *FileTreeModel) SelectedNode() *TreeNode {
	m.mu.RLock()
	defer m.mu.RUnlock()

	if m.selectedPath == "" {
		return nil
	}
	return m.nodes[m.selectedPath]
}

// ---------------------------------------------------------------------------
// visibleNodes reconstruction
// ---------------------------------------------------------------------------

// rebuildVisibleNodes rebuilds the flattened visible-nodes list.
//
// It walks the tree depth-first, descending only into expanded directories.
// Filtered nodes are excluded unless they have children that match the
// filter. Hidden nodes are excluded when showHidden is false.
func (m *FileTreeModel) rebuildVisibleNodes() {
	m.visibleNodes = m.visibleNodes[:0] // reuse backing array
	m.maxVisibleDepth = 0
	if m.Root != nil {
		// The workspace root is never a painted row, so it must stay
		// expanded or the explorer would go blank after Collapse All.
		m.Root.IsExpanded = true
	}

	if m.filterPattern() != "" {
		m.filteredWalk(m.Root, false)
	} else {
		m.unfilteredWalk(m.Root)
	}

	m.recomputeAggregateStatuses()
	m.recomputeAggregateDiagnostics()
	m.reconcileSelectionLocked()
}

// reconcileSelectionLocked restores the model invariant after any operation
// that replaces, filters, sorts, expands, or shrinks visibleNodes:
//
//   - a non-empty tree has a valid cursor whose row matches selectedPath;
//   - a hidden/deleted selection falls back to its nearest visible ancestor,
//     then to the nearest row at the old cursor position;
//   - scrollOffset is valid and keeps the cursor inside the viewport.
//
// Centralising this at the projection boundary prevents future mutation paths
// from having to remember a separate cursor clamp. The caller must hold m.mu.
func (m *FileTreeModel) reconcileSelectionLocked() {
	if len(m.visibleNodes) == 0 {
		m.cursorIndex = 0
		m.selectedPath = m.rootPath
		m.scrollOffset = 0
		return
	}

	if idx := m.visibleIndexLocked(m.selectedPath); idx >= 0 {
		m.cursorIndex = idx
		m.clampViewportLocked()
		return
	}

	// Collapsing a subtree or deleting its selected row should land on the
	// closest surviving parent instead of an unrelated row farther down.
	for path := filepath.Dir(m.selectedPath); path != "" && path != "."; path = filepath.Dir(path) {
		if idx := m.visibleIndexLocked(path); idx >= 0 {
			m.cursorIndex = idx
			m.selectedPath = m.visibleNodes[idx].Path
			m.clampViewportLocked()
			return
		}
		if path == m.rootPath {
			break
		}
		parent := filepath.Dir(path)
		if parent == path {
			break
		}
	}

	m.clampCursorLocked()
	m.clampViewportLocked()
}

func (m *FileTreeModel) visibleIndexLocked(path string) int {
	if path == "" {
		return -1
	}
	for i, node := range m.visibleNodes {
		if node != nil && node.Path == path {
			return i
		}
	}
	return -1
}

// clampCursorLocked is the defensive navigation boundary. Rebuilds already
// reconcile path-aware selection, but movement must remain safe even if a new
// mutation path is added without doing so.
func (m *FileTreeModel) clampCursorLocked() {
	if len(m.visibleNodes) == 0 {
		m.cursorIndex = 0
		m.selectedPath = m.rootPath
		m.scrollOffset = 0
		return
	}
	if m.cursorIndex < 0 {
		m.cursorIndex = 0
	}
	if m.cursorIndex >= len(m.visibleNodes) {
		m.cursorIndex = len(m.visibleNodes) - 1
	}
	m.selectedPath = m.visibleNodes[m.cursorIndex].Path
}

func (m *FileTreeModel) setCursorIndexLocked(i int) {
	m.cursorIndex = i
	m.clampCursorLocked()
}

func (m *FileTreeModel) clampViewportLocked() {
	if len(m.visibleNodes) == 0 {
		m.scrollOffset = 0
		return
	}
	vh := m.visibleHeight
	if vh <= 0 {
		vh = 10
	}
	m.clampScrollRangeLocked(vh)
	if m.cursorIndex < m.scrollOffset {
		m.scrollOffset = m.cursorIndex
	}
	if m.cursorIndex >= m.scrollOffset+vh {
		m.scrollOffset = m.cursorIndex - vh + 1
	}
	m.clampScrollRangeLocked(vh)
}

func (m *FileTreeModel) clampScrollRangeLocked(vh int) {
	if vh <= 0 {
		vh = 10
	}
	maxOffset := len(m.visibleNodes) - vh
	if maxOffset < 0 {
		maxOffset = 0
	}
	if m.scrollOffset < 0 {
		m.scrollOffset = 0
	}
	if m.scrollOffset > maxOffset {
		m.scrollOffset = maxOffset
	}
}

// unfilteredWalk appends visible nodes depth-first with no active filter.
// The workspace root stays in the model for git/diagnostic aggregation and
// path operations, but it is not a painted row — the explorer search hint
// names the workspace instead.
func (m *FileTreeModel) unfilteredWalk(node *TreeNode) {
	if node.Hidden && !m.showHidden {
		return
	}
	if node != m.Root {
		m.visibleNodes = append(m.visibleNodes, node)
		m.maxVisibleDepth = max(m.maxVisibleDepth, node.Depth)
	}

	if node.IsDir && node.IsExpanded {
		for _, child := range node.Children {
			m.unfilteredWalk(child)
		}
	}
}

// filteredWalk appends visible nodes depth-first with an active filter,
// descending only into expanded directories. Visibility comes from the memo
// maps built by applyFilterState: a node shows when it matched, an ancestor
// matched, or its subtree contains a match. The workspace root is omitted
// from the painted list the same way as in unfilteredWalk.
func (m *FileTreeModel) filteredWalk(node *TreeNode, ancestorMatched bool) {
	if node == nil || (node.Hidden && !m.showHidden) {
		return
	}
	_, selfMatched := m.filterMatchPos[node.Path]
	if !ancestorMatched && !selfMatched && !m.filterSubtree[node.Path] {
		return
	}
	if node != m.Root {
		m.visibleNodes = append(m.visibleNodes, node)
		m.maxVisibleDepth = max(m.maxVisibleDepth, node.Depth)
	}
	if node.IsDir && node.IsExpanded {
		for _, child := range node.Children {
			m.filteredWalk(child, ancestorMatched || selfMatched)
		}
	}
}

// filterLoadBudget caps how many unloaded directories one filter application
// may lazily read from disk. Already-loaded directories are free, so repeated
// keystrokes progressively deepen coverage without unbounded scans.
const filterLoadBudget = 512

// applyFilterState recomputes fuzzy-match memo maps for the current pattern
// and auto-expands every directory whose subtree contains a match, recording
// previously-collapsed ones so an empty pattern restores them.
// Must be called with m.mu held.
func (m *FileTreeModel) applyFilterState() {
	pattern := m.filterPattern()
	if pattern == "" {
		for _, path := range m.filterAutoExpanded {
			if n, ok := m.nodes[path]; ok && n.IsDir && n != m.Root {
				n.IsExpanded = false
			}
		}
		m.filterAutoExpanded = nil
		m.filterMatchPos = nil
		m.filterSubtree = nil
		return
	}
	m.filterMatchPos = make(map[string][]int)
	m.filterSubtree = make(map[string]bool)
	budget := filterLoadBudget
	m.computeFilterMatches(m.Root, pattern, &budget)
	m.autoExpandFilterMatches(m.Root)
}

// computeFilterMatches fills filterMatchPos/filterSubtree for node's subtree,
// lazily loading unloaded directories while budget lasts. Returns true when
// the subtree (including node itself) contains a match. The root node never
// self-matches — matching the workspace name would defeat the filter.
func (m *FileTreeModel) computeFilterMatches(node *TreeNode, pattern string, budget *int) bool {
	if node == nil || (node.Hidden && !m.showHidden) {
		return false
	}
	any := false
	if node != m.Root {
		if pos, ok := FuzzyMatch(node.Name, pattern); ok {
			m.filterMatchPos[node.Path] = pos
			any = true
		}
	}
	if node.IsDir {
		if node.Children == nil && m.directoryLoader == nil && *budget > 0 {
			*budget--
			_ = m.loadDirectory(node)
		}
		for _, child := range node.Children {
			if m.computeFilterMatches(child, pattern, budget) {
				any = true
			}
		}
	}
	if any {
		m.filterSubtree[node.Path] = true
	}
	return any
}

// autoExpandFilterMatches expands every directory whose subtree matched so
// filtered rows are reachable, recording directories that were collapsed.
func (m *FileTreeModel) autoExpandFilterMatches(node *TreeNode) {
	if node == nil || !node.IsDir || (node.Hidden && !m.showHidden) {
		return
	}
	if !m.filterSubtree[node.Path] {
		return
	}
	if !node.IsExpanded {
		node.IsExpanded = true
		m.filterAutoExpanded = append(m.filterAutoExpanded, node.Path)
	}
	for _, child := range node.Children {
		m.autoExpandFilterMatches(child)
	}
}

// ---------------------------------------------------------------------------
// Filtering
// ---------------------------------------------------------------------------

func (m *FileTreeModel) filterPattern() string {
	if m.filterEd == nil {
		return ""
	}
	return m.filterEd.Value()
}

// SetFilter sets a case-insensitive fuzzy filter pattern and rebuilds the
// visible nodes list.
//
// A node is shown if the pattern is a subsequence of its name, if an ancestor
// matched, or if any descendant matches. Ancestor directories of matches are
// auto-expanded (and restored when the filter clears). When the filter is
// empty, all (non-hidden, visible) nodes are shown.
func (m *FileTreeModel) SetFilter(pattern string) {
	m.mu.Lock()
	defer m.mu.Unlock()

	if m.filterEd == nil {
		m.filterEd = ui.NewTextEdit()
	}
	m.filterEd.SetValue(pattern)
	m.applyFilterState()
	m.rebuildVisibleNodes()

	// While a pattern is active, selection follows the matches (VS Code
	// behavior): keep the current node only if it matched itself; otherwise
	// jump to the first matching row so Enter opens it immediately instead
	// of toggling whatever directory happened to be selected.
	if m.filterPattern() != "" {
		if m.filterMatchPos[m.selectedPath] != nil {
			for i, node := range m.visibleNodes {
				if node.Path == m.selectedPath {
					m.setCursorIndexLocked(i)
					return
				}
			}
		}
		for i, node := range m.visibleNodes {
			if m.filterMatchPos[node.Path] != nil {
				m.setCursorIndexLocked(i)
				return
			}
		}
	}
}

// FilterMatchPositions returns the rune positions in the node name matched by
// the active fuzzy filter, or nil when the node did not match (or no filter
// is active). The returned slice must not be modified.
func (m *FileTreeModel) FilterMatchPositions(path string) []int {
	m.mu.RLock()
	defer m.mu.RUnlock()
	if m.filterMatchPos == nil {
		return nil
	}
	return m.filterMatchPos[path]
}

// Filter returns the current filter pattern.
func (m *FileTreeModel) Filter() string {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return m.filterPattern()
}

// SetShowHidden controls whether dot-files (names starting with ".") are
// visible in the tree. Rebuilds the visible nodes list on change.
func (m *FileTreeModel) SetShowHidden(show bool) {
	m.mu.Lock()
	defer m.mu.Unlock()

	if m.showHidden == show {
		return
	}
	m.showHidden = show
	if m.filterPattern() != "" {
		m.applyFilterState()
	}
	m.rebuildVisibleNodes()
}

// ShowHidden returns whether dot-files are currently shown.
func (m *FileTreeModel) ShowHidden() bool {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return m.showHidden
}

// ---------------------------------------------------------------------------
// Sorting
// ---------------------------------------------------------------------------

// SetSortBy changes the sort mode and re-sorts all loaded children.
func (m *FileTreeModel) SetSortBy(sort SortOption) {
	m.mu.Lock()
	defer m.mu.Unlock()

	if m.sortBy == sort {
		return
	}
	m.sortBy = sort

	// Re-sort every node that has loaded children.
	m.resortAll(m.Root)

	m.rebuildVisibleNodes()
}

// SortBy returns the current sort mode.
func (m *FileTreeModel) SortBy() SortOption {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return m.sortBy
}

// resortAll recursively re-sorts children of every node.
func (m *FileTreeModel) resortAll(node *TreeNode) {
	if node == nil || !node.IsDir {
		return
	}
	if len(node.Children) > 0 {
		m.sortChildren(node.Children)
		for _, child := range node.Children {
			m.resortAll(child)
		}
	}
}

// ---------------------------------------------------------------------------
// Refresh (for file system watcher events)
// ---------------------------------------------------------------------------

// ProcessWatcherEvent handles a file system change event.
//
//   - FileCreated: adds the new entry to the parent directory's children
//     and rebuilds the visible list.
//   - FileDeleted: removes the entry from the parent's children and rebuilds.
//   - FileModified: refreshes the node's mod time and optionally its git status.
//   - FileRenamed: removes the old entry and adds the new one.
func (m *FileTreeModel) ProcessWatcherEvent(evt FileChangeEvent) {
	m.mu.Lock()
	m.applyMutationLocked(evt)
	onFileChanged := m.onFileChanged
	m.mu.Unlock()

	// Never invoke external code while holding the tree lock. EventBus.Send can
	// wait for Bubble Tea, whose render path reads this model; calling it under
	// the write lock deadlocks the watcher and renderer against each other.
	if onFileChanged != nil {
		// Map FileEvent to LSP FileChangeType: 1=Created, 2=Changed, 3=Deleted.
		var changeType int
		switch evt.Type {
		case FileCreated:
			changeType = 1
		case FileModified:
			changeType = 2
		case FileDeleted, FileRenamed:
			changeType = 3
		}
		if changeType > 0 {
			onFileChanged(evt.Path, changeType)
		}
	}
}

// ApplyLocalMutation immediately updates the tree after a filesystem mutation
// initiated by the TUI. Unlike ProcessWatcherEvent, it does not invoke the
// external watcher callback: local mutations run inside Bubble Tea Update, so
// synchronously publishing back through EventBus would re-enter that same
// update loop. A later fsnotify event remains responsible for notifying
// external consumers such as the LSP.
func (m *FileTreeModel) ApplyLocalMutation(evt FileChangeEvent) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.applyMutationLocked(evt)
}

// applyMutationLocked applies the tree-state portion shared by local
// mutations and background watcher events. The caller must hold m.mu.
func (m *FileTreeModel) applyMutationLocked(evt FileChangeEvent) {
	switch evt.Type {
	case FileCreated:
		m.handleFileCreated(evt.Path)
	case FileDeleted:
		m.handleFileDeleted(evt.Path)
	case FileModified:
		m.handleFileModified(evt.Path)
	case FileRenamed:
		m.handleFileRenamed(evt.OldPath, evt.Path)
	}
}

// SetOnFileChanged sets the callback invoked when a file changes on disk.
func (m *FileTreeModel) SetOnFileChanged(fn func(filePath string, changeType int)) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.onFileChanged = fn
}

// SetOnGitChanged sets the callback used only to schedule a Git refresh on the
// Bubble Tea event loop. Repository watcher goroutines never mutate tree rows.
func (m *FileTreeModel) SetOnGitChanged(fn func()) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.onGitChanged = fn
}

func (m *FileTreeModel) handleFileCreated(path string) {
	parentPath := filepath.Dir(path)
	parentNode, ok := m.nodes[parentPath]
	if !ok || !parentNode.IsDir {
		return
	}

	// Load children of the parent (will pick up the new entry).
	_ = m.loadDirectory(parentNode)
	m.reapplyFilterAfterMutationLocked()
	m.rebuildVisibleNodes()
}

func (m *FileTreeModel) handleFileDeleted(path string) {
	node, ok := m.nodes[path]
	if !ok {
		return
	}

	parentNode := node.Parent
	if parentNode == nil {
		return
	}

	// Re-load the parent directory to pick up changes.
	_ = m.loadDirectory(parentNode)

	// loadDirectory is authoritative: if the path was recreated before this
	// debounced delete arrived, the replacement remains indexed and visible.
	m.reapplyFilterAfterMutationLocked()
	m.rebuildVisibleNodes()
}

func (m *FileTreeModel) handleFileModified(path string) {
	node, ok := m.nodes[path]
	if !ok {
		return
	}

	// Update modification time.
	if !m.noLocalIO {
		info, err := os.Stat(path)
		if err == nil {
			node.ModTime = info.ModTime()
		}
	}

	// Refresh git status if available.
	if m.gitResolver != nil {
		node.GitStatus = m.gitResolver.Resolve(path)
	}

	// Recompute aggregate statuses from the git status map.
	m.recomputeAggregateStatuses()
}

func (m *FileTreeModel) handleFileRenamed(oldPath, newPath string) {
	node, ok := m.nodes[oldPath]
	if !ok {
		// Old path not tracked — treat as create.
		m.handleFileCreated(newPath)
		return
	}

	// Remove old path tracking.
	delete(m.nodes, oldPath)

	// Update node fields.
	oldParent := node.Parent
	if oldParent != nil {
		// Re-load old parent to remove the entry.
		_ = m.loadDirectory(oldParent)
	}

	// Add as new entry via the new parent.
	m.handleFileCreated(newPath)
}

// reapplyFilterAfterMutationLocked lets created/deleted rows join or leave an
// active fuzzy result set. Rebuilding from stale match maps can otherwise
// produce a valid cursor over an out-of-date projection.
func (m *FileTreeModel) reapplyFilterAfterMutationLocked() {
	if m.filterPattern() != "" {
		m.applyFilterState()
	}
}

// ---------------------------------------------------------------------------
// Accessors
// ---------------------------------------------------------------------------

// VisibleNodes returns the current flattened list of visible nodes as a
// snapshot. The caller must not modify the returned slice.
func (m *FileTreeModel) VisibleNodes() []*TreeNode {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return append([]*TreeNode(nil), m.visibleNodes...)
}

// ViewportAncestor is the deepest offscreen directory shared by every painted
// row. When the viewport spans root branches, return the first row's offscreen
// parent for the normal breadcrumb, but mark it non-shared so guides stay full.
func (m *FileTreeModel) ViewportAncestor(rows int) (*TreeNode, bool) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	if m.scrollOffset <= 0 || m.scrollOffset >= len(m.visibleNodes) || rows <= 0 {
		return nil, false
	}
	firstParent := m.visibleNodes[m.scrollOffset].Parent
	if firstParent == m.Root {
		return nil, false
	}
	end := min(len(m.visibleNodes), m.scrollOffset+rows)
	for candidate := firstParent; candidate != nil && candidate != m.Root; candidate = candidate.Parent {
		shared := true
		for _, node := range m.visibleNodes[m.scrollOffset+1 : end] {
			ancestor := node
			for ancestor != nil && ancestor != candidate {
				ancestor = ancestor.Parent
			}
			if ancestor == nil {
				shared = false
				break
			}
		}
		if shared {
			return candidate, true
		}
	}
	return firstParent, false
}

// MaxVisibleDepth is cached with the structural projection. It never scans
// nodes during a paint or changes when only focus/scroll state moves.
func (m *FileTreeModel) MaxVisibleDepth() int {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return m.maxVisibleDepth
}

// VisibleRows returns the paths of currently visible nodes in
// display order. Convenience for callers that need a list of strings
// (e.g. the right-click context menu resolving a click Y to a path).
func (m *FileTreeModel) VisibleRows() []string {
	nodes := m.VisibleNodes()
	out := make([]string, 0, len(nodes))
	for _, n := range nodes {
		if n == nil {
			continue
		}
		out = append(out, n.Path)
	}
	return out
}

// IsDir reports whether the given path is a known directory in the
// tree. Returns false for unknown paths.
func (m *FileTreeModel) IsDir(path string) bool {
	m.mu.RLock()
	defer m.mu.RUnlock()
	n, ok := m.nodes[path]
	if !ok {
		return false
	}
	return n.IsDir
}

// ParentDir returns the parent directory of the given path, or ""
// for top-level entries.
func (m *FileTreeModel) ParentDir(path string) string {
	if path == "" {
		return ""
	}
	// Find the last separator.
	for i := len(path) - 1; i >= 0; i-- {
		if path[i] == '/' {
			parent := path[:i]
			if parent == "" {
				return "/"
			}
			return parent
		}
	}
	return ""
}

// ExpandToPath expands (lazily loading as needed) every ancestor of the
// given path, rebuilds the visible rows, and moves the selection to the path
// so it is revealed and highlighted. Returns true if the path is now visible.
func (m *FileTreeModel) ExpandToPath(path string) bool {
	if path == "" {
		return false
	}
	m.mu.Lock()
	defer m.mu.Unlock()

	target := filepath.Clean(path)
	if target != m.rootPath {
		rel, err := filepath.Rel(m.rootPath, target)
		if err != nil || rel == "." || rel == ".." ||
			strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
			return false
		}
		node := m.Root
		current := m.rootPath
		for _, part := range strings.Split(rel, string(filepath.Separator)) {
			if !node.IsDir {
				return false
			}
			if node.Children == nil {
				if err := m.loadDirectory(node); err != nil {
					return false
				}
			}
			node.IsExpanded = true
			current = filepath.Join(current, part)
			child, ok := m.nodes[current]
			if !ok {
				// Directory contents changed since the last load — refresh
				// once and retry before giving up.
				if err := m.loadDirectory(node); err != nil {
					return false
				}
				if child, ok = m.nodes[current]; !ok {
					return false
				}
			}
			node = child
		}
	}

	m.rebuildVisibleNodes()
	for i, n := range m.visibleNodes {
		if n.Path == target {
			m.cursorIndex = i
			m.selectedPath = target
			m.updateScrollOffsetLocked()
			return true
		}
	}
	return false
}

// NodeByPath looks up a TreeNode by its absolute path. Returns nil if not
// found.
func (m *FileTreeModel) NodeByPath(path string) *TreeNode {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return m.nodes[path]
}

// CursorIndex returns the current cursor position in the visible nodes list.
func (m *FileTreeModel) CursorIndex() int {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return m.cursorIndex
}

// SetCursorIndex sets the cursor position. The index is clamped to the valid
// range [0, len(visibleNodes)-1].
func (m *FileTreeModel) SetCursorIndex(i int) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.setCursorIndexLocked(i)
}

// RootPath returns the absolute path of the workspace root.
func (m *FileTreeModel) RootPath() string {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return m.rootPath
}

// WorkspaceLabel is the workspace folder name used by the explorer search
// hint. Empty when the root has no usable name.
func (m *FileTreeModel) WorkspaceLabel() string {
	m.mu.RLock()
	defer m.mu.RUnlock()
	if m.Root == nil {
		return ""
	}
	name := strings.TrimSpace(m.Root.Name)
	if name == "" || name == "." || name == string(filepath.Separator) {
		return ""
	}
	return name
}

// IconResolver returns the model's icon resolver.
func (m *FileTreeModel) IconResolver() *IconResolver {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return m.iconResolver
}

// Watcher returns the file system watcher (may be nil).
func (m *FileTreeModel) Watcher() *Watcher {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return m.watcher
}

// GitResolver returns the git status resolver (may be nil).
func (m *FileTreeModel) GitResolver() *GitStatusResolver {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return m.gitResolver
}

// StartWatcher creates and starts a file system watcher for the tree's root
// path. Events are delivered to ProcessWatcherEvent in a background goroutine.
func (m *FileTreeModel) StartWatcher() error {
	m.mu.RLock()
	if m.noLocalIO {
		m.mu.RUnlock()
		return nil
	}
	rootPath := m.rootPath
	alreadyStarted := m.watcher != nil
	m.mu.RUnlock()
	if alreadyStarted {
		return nil
	}

	w := NewWatcher(rootPath)
	if err := w.Start(); err != nil {
		return fmt.Errorf("failed to start file watcher: %w", err)
	}
	m.mu.Lock()
	if m.watcher != nil {
		m.mu.Unlock()
		w.Stop()
		return nil
	}
	m.watcher = w
	resolver := m.gitResolver
	m.mu.Unlock()

	var stopGit func()
	if resolver != nil {
		stopGit = resolver.WatchForChanges(func() {
			m.mu.RLock()
			active := m.watcher == w
			callback := m.onGitChanged
			m.mu.RUnlock()
			if active && callback != nil {
				callback()
			}
		})
	}
	m.mu.Lock()
	if m.watcher == w {
		m.gitWatcherStop = stopGit
		stopGit = nil
	}
	m.mu.Unlock()
	if stopGit != nil {
		stopGit()
	}

	go func() {
		for {
			select {
			case <-w.Done():
				return
			case evt := <-w.Events():
				m.ProcessWatcherEvent(evt)
			}
		}
	}()
	return nil
}

// StopWatcher stops the file system watcher if one is active.
func (m *FileTreeModel) StopWatcher() {
	m.mu.Lock()
	w := m.watcher
	stopGit := m.gitWatcherStop
	m.watcher = nil
	m.gitWatcherStop = nil
	m.mu.Unlock()
	if stopGit != nil {
		stopGit()
	}
	if w != nil {
		w.Stop()
	}
}
