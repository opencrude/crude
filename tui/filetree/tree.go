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

// recomputeAggregateStatuses recomputes AggregateStatus for every loaded
// directory node by scanning the flat git status map (which knows about ALL
// files in the repo, not just expanded children) and propagating each status
// up to the nearest loaded ancestor directory.
//
// This is O(git_statuses * depth) and scales to very large repos because it
// never walks unloaded subtrees — it uses the git resolver's complete map.
func (m *FileTreeModel) recomputeAggregateStatuses() {
	if m.gitResolver == nil {
		return
	}

	statuses := m.gitResolver.AllStatuses()
	repoPath := m.gitResolver.RepoPath()

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
		absPath := filepath.Join(repoPath, relPath)
		dir := filepath.Dir(absPath)
		for {
			if node, ok := m.nodes[dir]; ok {
				if status > node.AggregateStatus {
					node.AggregateStatus = status
				}
				// Propagate up to root.
				for p := node.Parent; p != nil; p = p.Parent {
					if node.AggregateStatus > p.AggregateStatus {
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
	m.diagnosticResolver[fp] = DiagCounts{Errors: errors, Warnings: warnings, Hints: hints}

	// If the tree node doesn't exist yet (file is in a collapsed directory),
	// load the parent directory so the node becomes available.  The directory
	// stays collapsed — we only materialise the tree node so its diagnostic
	// counts are set and propagated upward for aggregate display.
	if _, ok := m.nodes[fp]; !ok {
		m.ensureDiagnosticsNode(fp)
	}

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

	// First pass: set file-level counts from the resolver.
	for path, counts := range m.diagnosticResolver {
		if node, ok := m.nodes[path]; ok {
			node.DiagnosticCount = counts.Errors + counts.Warnings + counts.Hints
			node.DiagErrors = counts.Errors
			node.DiagWarnings = counts.Warnings
			node.DiagHints = counts.Hints
		}
	}

	// Second pass: propagate all counts up from files to root.
	for path := range m.diagnosticResolver {
		if node, ok := m.nodes[path]; ok {
			for p := node.Parent; p != nil; p = p.Parent {
				p.DiagnosticCount += node.DiagnosticCount
				p.DiagErrors += node.DiagErrors
				p.DiagWarnings += node.DiagWarnings
				p.DiagHints += node.DiagHints
			}
		}
	}
}

// IsLeaf returns true if this node is a file (not a directory).
func (n *TreeNode) IsLeaf() bool {
	return !n.IsDir
}

// HasChildren returns true if this node has loaded children.
func (n *TreeNode) HasChildren() bool {
	return len(n.Children) > 0
}

// childByName finds a child node by name. Returns nil if not found.
func (n *TreeNode) childByName(name string) *TreeNode {
	for _, child := range n.Children {
		if child.Name == name {
			return child
		}
	}
	return nil
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

	rootPath string

	// nodes provides O(1) lookups from absolute path to TreeNode.
	nodes map[string]*TreeNode

	// selectedPath is the absolute path of the currently selected node.
	selectedPath string

	// cursorIndex is the current cursor position in the visibleNodes slice.
	cursorIndex int

	// visibleNodes is a flattened, depth-first list of currently visible nodes.
	visibleNodes []*TreeNode

	// filter is the current filter pattern (empty = no filter).
	filter string

	// sortBy controls how children are ordered within a directory.
	sortBy SortOption

	// showHidden controls whether dot-files (names starting with ".") appear.
	showHidden bool

	// iconResolver resolves file/directory names to NerdFont/Unicode icons.
	iconResolver *IconResolver

	// gitResolver resolves git status (may be nil).
	gitResolver *GitStatusResolver

	// diagnosticResolver stores per-file diagnostic counts (may be nil).
	// The map key is the absolute file path.
	diagnosticResolver map[string]DiagCounts

	// watcher watches for file system changes (may be nil).
	watcher *Watcher

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

	// mu protects all mutable state for thread-safe access.
	mu sync.RWMutex
}

// NewFileTreeModel creates a FileTreeModel rooted at the given path.
//
// It initialises the icon resolver, attempts to create a git status resolver,
// loads the root directory, and builds the initial visible-nodes list.
func NewFileTreeModel(rootPath string) *FileTreeModel {
	// Resolve to an absolute path.
	absPath, err := filepath.Abs(rootPath)
	if err != nil {
		absPath = rootPath
	}

	m := &FileTreeModel{
		rootPath:           absPath,
		nodes:              make(map[string]*TreeNode),
		sortBy:             SortByType,
		showHidden:         false,
		iconResolver:       NewIconResolver(),
		gitResolver:        NewGitStatusResolver(absPath),
		diagnosticResolver: make(map[string]DiagCounts),
	}

	// Create the root node.
	info, err := os.Stat(absPath)
	if err != nil {
		info = nil
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

	// Initially expand the root so the tree isn't empty.
	root.IsExpanded = true
	m.loadDirectory(root)

	// Rebuild visible nodes.
	m.rebuildVisibleNodes()

	// Set initial selection to the first visible node.
	if len(m.visibleNodes) > 0 {
		m.selectedPath = m.visibleNodes[0].Path
		m.cursorIndex = 0
	} else {
		m.selectedPath = absPath
	}

	return m
}

// ---------------------------------------------------------------------------
// Directory loading
// ---------------------------------------------------------------------------

// loadDirectory reads the contents of the given directory node and populates
// its Children slice. It is the core of the lazy-loading mechanism.
//
// If the node is not a directory, this is a no-op.
func (m *FileTreeModel) loadDirectory(node *TreeNode) error {
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

		// Re-use existing node if available (updates git status).
		if existingNode, ok := existing[name]; ok {
			existingNode.IsDir = isDir
			existingNode.IsSymlink = isSymlink
			existingNode.Hidden = isHidden
			existingNode.ModTime = modTime
			if m.gitResolver != nil {
				existingNode.GitStatus = m.gitResolver.Resolve(childPath)
			}
			children = append(children, existingNode)
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

	// Remove orphaned children (entries that existed before but no longer).
	for _, orphan := range existing {
		delete(m.nodes, orphan.Path)
	}

	node.Children = children
	node.Loading = false

	return nil
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
func (m *FileTreeModel) ToggleExpand(path string) {
	m.mu.Lock()
	defer m.mu.Unlock()

	node, ok := m.nodes[path]
	if !ok || !node.IsDir {
		return
	}

	if node.IsExpanded {
		node.IsExpanded = false
	} else {
		// Load children if not already loaded.
		if node.Children == nil {
			_ = m.loadDirectory(node)
		}
		node.IsExpanded = true
	}

	m.rebuildVisibleNodes()
}

// Expand expands a specific directory node. Non-directory nodes are ignored.
// If children have not been loaded yet, they are loaded first.
func (m *FileTreeModel) Expand(path string) {
	m.mu.Lock()
	defer m.mu.Unlock()

	node, ok := m.nodes[path]
	if !ok || !node.IsDir || node.IsExpanded {
		return
	}

	// Load children if not already loaded.
	if node.Children == nil {
		_ = m.loadDirectory(node)
	}
	node.IsExpanded = true
	m.rebuildVisibleNodes()
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

// CollapseAll collapses every directory node, leaving only the root expanded.
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
	node.IsExpanded = false
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

	m.selectedPath = path

	// Find the index in visible nodes.
	for i, node := range m.visibleNodes {
		if node.Path == path {
			m.cursorIndex = i
			return
		}
	}

	// Path not visible — move cursor to first node.
	if len(m.visibleNodes) > 0 {
		m.cursorIndex = 0
		m.selectedPath = m.visibleNodes[0].Path
	}
}

// MoveUp moves the cursor up by one visible item.
func (m *FileTreeModel) MoveUp() {
	m.mu.Lock()
	if m.cursorIndex > 0 {
		m.cursorIndex--
		m.selectedPath = m.visibleNodes[m.cursorIndex].Path
	}
	m.mu.Unlock()
	m.updateScrollOffset()
}

// MoveDown moves the cursor down by one visible item.
func (m *FileTreeModel) MoveDown() {
	m.mu.Lock()
	if m.cursorIndex < len(m.visibleNodes)-1 {
		m.cursorIndex++
		m.selectedPath = m.visibleNodes[m.cursorIndex].Path
	}
	m.mu.Unlock()
	m.updateScrollOffset()
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

	if m.filter != "" {
		m.filteredWalk(m.Root, false)
	} else {
		m.unfilteredWalk(m.Root)
	}

	m.recomputeAggregateStatuses()
	m.recomputeAggregateDiagnostics()
}

// unfilteredWalk appends visible nodes depth-first with no active filter.
func (m *FileTreeModel) unfilteredWalk(node *TreeNode) {
	if node.Hidden && !m.showHidden {
		return
	}
	m.visibleNodes = append(m.visibleNodes, node)

	if node.IsDir && node.IsExpanded {
		for _, child := range node.Children {
			m.unfilteredWalk(child)
		}
	}
}

// filteredWalk appends visible nodes depth-first with an active filter.
// It returns true if the node or any of its descendants matched the filter.
func (m *FileTreeModel) filteredWalk(node *TreeNode, parentVisible bool) bool {
	if node.Hidden && !m.showHidden {
		return false
	}

	// Does this node match the filter?
	selfMatch := parentVisible || strings.Contains(
		strings.ToLower(node.Name),
		strings.ToLower(m.filter),
	)

	// If this is a directory, check children.
	childMatch := false
	if node.IsDir {
		// Only descend into expanded directories or if there's potential.
		// We still need to check children even when collapsed for filter
		// matching purposes — but we can only do this if children are loaded.
		if node.Children != nil {
			for _, child := range node.Children {
				if m.filteredWalk(child, selfMatch || childMatch) {
					childMatch = true
				}
			}
		}
	}

	visible := selfMatch || childMatch

	if visible {
		m.visibleNodes = append(m.visibleNodes, node)
	}

	return visible
}

// ---------------------------------------------------------------------------
// Filtering
// ---------------------------------------------------------------------------

// SetFilter sets a case-insensitive filter pattern and rebuilds the visible
// nodes list.
//
// A node is shown if its name contains the filter string OR if any of its
// descendants match. Ancestors of matching nodes are also shown. When the
// filter is empty, all (non-hidden, visible) nodes are shown.
func (m *FileTreeModel) SetFilter(pattern string) {
	m.mu.Lock()
	defer m.mu.Unlock()

	m.filter = pattern
	m.rebuildVisibleNodes()

	// Clamp cursor index to new visible list.
	if m.cursorIndex >= len(m.visibleNodes) {
		if len(m.visibleNodes) > 0 {
			m.cursorIndex = len(m.visibleNodes) - 1
			m.selectedPath = m.visibleNodes[m.cursorIndex].Path
		} else {
			m.cursorIndex = 0
			m.selectedPath = m.rootPath
		}
	} else if len(m.visibleNodes) > 0 {
		m.selectedPath = m.visibleNodes[m.cursorIndex].Path
	}
}

// Filter returns the current filter pattern.
func (m *FileTreeModel) Filter() string {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return m.filter
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

// refreshNode re-reads the contents of a directory node, diffs old vs new
// children, and updates the tree accordingly. For use after file system
// watcher events.
func (m *FileTreeModel) refreshNode(path string) {
	node, ok := m.nodes[path]
	if !ok || !node.IsDir || node.Children == nil {
		return
	}

	_ = m.loadDirectory(node) // loadDirectory handles diff via existing map
	m.rebuildVisibleNodes()
}

// ProcessWatcherEvent handles a file system change event.
//
//   - FileCreated: adds the new entry to the parent directory's children
//     and rebuilds the visible list.
//   - FileDeleted: removes the entry from the parent's children and rebuilds.
//   - FileModified: refreshes the node's mod time and optionally its git status.
//   - FileRenamed: removes the old entry and adds the new one.
func (m *FileTreeModel) ProcessWatcherEvent(evt FileChangeEvent) {
	m.mu.Lock()
	defer m.mu.Unlock()

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

	// Notify the LSP server about disk changes.
	if m.onFileChanged != nil {
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
			m.onFileChanged(evt.Path, changeType)
		}
	}
}

// SetOnFileChanged sets the callback invoked when a file changes on disk.
func (m *FileTreeModel) SetOnFileChanged(fn func(filePath string, changeType int)) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.onFileChanged = fn
}

func (m *FileTreeModel) handleFileCreated(path string) {
	parentPath := filepath.Dir(path)
	parentNode, ok := m.nodes[parentPath]
	if !ok || !parentNode.IsDir {
		return
	}

	// Load children of the parent (will pick up the new entry).
	_ = m.loadDirectory(parentNode)
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

	// Remove from nodes map.
	delete(m.nodes, path)
	m.rebuildVisibleNodes()
}

func (m *FileTreeModel) handleFileModified(path string) {
	node, ok := m.nodes[path]
	if !ok {
		return
	}

	// Update modification time.
	info, err := os.Stat(path)
	if err == nil {
		node.ModTime = info.ModTime()
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

// ---------------------------------------------------------------------------
// Accessors
// ---------------------------------------------------------------------------

// VisibleNodes returns the current flattened list of visible nodes as a
// snapshot. The caller must not modify the returned slice.
func (m *FileTreeModel) VisibleNodes() []*TreeNode {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return m.visibleNodes
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

// ExpandToPath walks the tree and ensures all ancestors of the
// given path are expanded so the path is visible. Returns true if
// the path is now visible.
func (m *FileTreeModel) ExpandToPath(path string) bool {
	if path == "" {
		return false
	}
	m.mu.Lock()
	defer m.mu.Unlock()

	// Walk up the path components and expand each ancestor dir.
	parts := splitPath(path)
	current := ""
	for i, p := range parts {
		if i > 0 {
			current += "/"
		}
		current += p
		if i == len(parts)-1 {
			// Leaf: just check it exists.
			_, ok := m.nodes[current]
			return ok
		}
		// Ancestor: must be expanded.
		n, ok := m.nodes[current]
		if !ok {
			return false
		}
		if n.IsDir && !n.IsExpanded {
			n.IsExpanded = true
		}
	}
	// Rebuild visible nodes.
	m.rebuildVisibleNodes()
	return true
}

// splitPath is a small helper to break a path on /.
func splitPath(p string) []string {
	if p == "" {
		return nil
	}
	out := []string{}
	cur := ""
	for _, r := range p {
		if r == '/' {
			if cur != "" {
				out = append(out, cur)
				cur = ""
			}
		} else {
			cur += string(r)
		}
	}
	if cur != "" {
		out = append(out, cur)
	}
	return out
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

	if len(m.visibleNodes) == 0 {
		m.cursorIndex = 0
		m.selectedPath = ""
		return
	}

	if i < 0 {
		i = 0
	}
	if i >= len(m.visibleNodes) {
		i = len(m.visibleNodes) - 1
	}
	m.cursorIndex = i
	m.selectedPath = m.visibleNodes[i].Path
}

// RootPath returns the absolute path of the workspace root.
func (m *FileTreeModel) RootPath() string {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return m.rootPath
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
	w := NewWatcher(m.rootPath)
	if err := w.Start(); err != nil {
		return fmt.Errorf("failed to start file watcher: %w", err)
	}
	m.watcher = w
	go func() {
		for evt := range w.Events() {
			m.ProcessWatcherEvent(evt)
		}
	}()
	return nil
}

// StopWatcher stops the file system watcher if one is active.
func (m *FileTreeModel) StopWatcher() {
	if m.watcher != nil {
		m.watcher.Stop()
		m.watcher = nil
	}
}
