// Package filetree provides the file tree sidebar widget.
//
// This file implements the event handling system: TreeMsg types, keyboard
// and mouse event handlers, page scrolling, expand/collapse actions,
// filter commands, and refresh operations.
package filetree

import (
	"fmt"
	"os"

	tea "charm.land/bubbletea/v2"

	"crude/tui/ui"
)

// ---------------------------------------------------------------------------
// Custom message types emitted by the file tree to the parent app
// ---------------------------------------------------------------------------

// FileSelectedMsg is emitted when a file is selected/opened in the tree
// (e.g. via Ctrl+Enter or double-click).
type FileSelectedMsg struct {
	// Path is the absolute path of the selected file.
	Path string
	// Node is the tree node for the selected file.
	Node *TreeNode
}

// EnterFilterModeMsg is emitted when the user presses a key that requests
// filter input (f or /). The parent app should switch to filter-input mode.
type EnterFilterModeMsg struct{}

// TreeRefreshDoneMsg is emitted when the tree has been fully refreshed
// from disk.
type TreeRefreshDoneMsg struct {
	RootPath string
	Model    *FileTreeModel
	Err      error
}

// ---------------------------------------------------------------------------
// TreeMsg carries messages routed through the tree's event loop
// ---------------------------------------------------------------------------

// TreeMsg is the base interface for all file tree internal messages.
type TreeMsg interface{ treeMsg() }

// TreeKeyPressMsg wraps a bubbletea key-press event for the tree.
type TreeKeyPressMsg struct {
	Key tea.KeyPressMsg
}

func (TreeKeyPressMsg) treeMsg() {}

// TreeMouseMsg wraps a mouse event for the tree.
// The Mouse field holds a tea.Msg that is one of:
// tea.MouseClickMsg, tea.MouseWheelMsg, tea.MouseMotionMsg, or
// tea.MouseReleaseMsg.
type TreeMouseMsg struct {
	Mouse tea.Msg
}

func (TreeMouseMsg) treeMsg() {}

// TreeWatcherMsg wraps a file system watcher event.
type TreeWatcherMsg struct {
	Event FileChangeEvent
}

func (TreeWatcherMsg) treeMsg() {}

// TreeGitRefreshMsg carries one exact asynchronous status scan back to the
// model that requested it. Model identity and generation reject retired or
// superseded results after a workspace switch or coalesced refresh.
type TreeGitRefreshMsg struct {
	Model      *FileTreeModel
	Generation uint64
	Statuses   map[string]GitStatus
	Err        error
}

func (TreeGitRefreshMsg) treeMsg() {}

// TreeGitChangedMsg is emitted by the repository-metadata watcher. The app
// verifies Model ownership before scheduling filesystem I/O as a tea.Cmd.
type TreeGitChangedMsg struct {
	Model *FileTreeModel
}

// TreeWidthMsg signals a sidebar width change.
type TreeWidthMsg struct {
	Width int
}

func (TreeWidthMsg) treeMsg() {}

// TreeHeightMsg signals a visible-height change for page scrolling.
type TreeHeightMsg struct {
	Height int
}

func (TreeHeightMsg) treeMsg() {}

// TreeFilterMsg signals a filter change.
type TreeFilterMsg struct {
	Pattern string
}

func (TreeFilterMsg) treeMsg() {}

// ---------------------------------------------------------------------------
// Update is the main event handler
// ---------------------------------------------------------------------------

// Update processes a tree-level message and returns a command for side
// effects (async operations, signals to the parent app, etc.).
//
// This is the entry point for all events targeted at the file tree.
func (m *FileTreeModel) Update(msg TreeMsg) tea.Cmd {
	switch msg := msg.(type) {
	case TreeKeyPressMsg:
		return m.handleKey(msg.Key)

	case TreeMouseMsg:
		return m.handleMouse(msg.Mouse)

	case TreeWatcherMsg:
		m.ProcessWatcherEvent(msg.Event)
		return nil

	case TreeGitRefreshMsg:
		return m.applyGitRefresh(msg)

	case TreeWidthMsg:
		m.width = msg.Width
		return nil

	case TreeHeightMsg:
		m.visibleHeight = msg.Height
		return nil

	case TreeFilterMsg:
		m.SetFilter(msg.Pattern)
		return nil
	}

	return nil
}

// ---------------------------------------------------------------------------
// handleKey performs keyboard navigation
// ---------------------------------------------------------------------------

// handleKey processes a key-press and dispatches it to the appropriate
// tree action. Returns a tea.Cmd for side effects (file open, filter
// request, refresh, etc.).
func (m *FileTreeModel) handleKey(key tea.KeyPressMsg) tea.Cmd {
	if m.filterActive {
		switch key.Code {
		case tea.KeyUp, tea.KeyDown, tea.KeyHome, tea.KeyEnd, tea.KeyPgUp, tea.KeyPgDown,
			tea.KeyEnter, tea.KeyRight, tea.KeyLeft:
			// navigation while filtering
		default:
			m.handleFilterInputKey(key)
			return nil
		}
	}

	// Check for Ctrl+Enter first (file open).
	if key.Mod.Contains(tea.ModCtrl) && key.Code == tea.KeyEnter {
		return m.ToggleExpandOrSelect()
	}

	// Check for Ctrl+Shift+G (git refresh).
	if key.Mod.Contains(tea.ModCtrl) && key.Mod.Contains(tea.ModShift) && key.Code == 'g' {
		return m.RefreshGitStatus()
	}

	// Dispatch by key code for special keys, then by string for
	// printable characters.
	switch key.Code {
	// --- Navigation ---
	case tea.KeyUp:
		m.MoveUp()
		return nil
	case tea.KeyDown:
		m.MoveDown()
		return nil
	case tea.KeyHome:
		m.GoToTop()
		return nil
	case tea.KeyEnd:
		m.GoToBottom()
		return nil
	case tea.KeyPgUp:
		m.MovePageUp()
		return nil
	case tea.KeyPgDown:
		m.MovePageDown()
		return nil

	// --- Expand / Collapse ---
	case tea.KeyEnter:
		// Enter without modifier selects file or toggles directory.
		return m.ToggleExpandOrSelect()
	case tea.KeyRight:
		// Right arrow expands directory or moves into it.
		// Shift+Right previously did ExpandAllNested; now it scrolls right.
		// Ctrl+Shift+Right still does ExpandAllNested.
		if key.Mod.Contains(tea.ModShift) && key.Mod.Contains(tea.ModCtrl) {
			m.ExpandAllNested()
			return nil
		}
		if key.Mod.Contains(tea.ModShift) {
			m.SetHScrollOffset(m.hscrollOffset + 4)
			return nil
		}
		return m.expandOrMoveIn()
	case tea.KeyLeft:
		// Left arrow collapses directory or moves to parent.
		// Shift+Left previously did CollapseAllNested; now it scrolls left.
		// Ctrl+Shift+Left still does CollapseAllNested.
		if key.Mod.Contains(tea.ModShift) && key.Mod.Contains(tea.ModCtrl) {
			m.CollapseAllNested()
			return nil
		}
		if key.Mod.Contains(tea.ModShift) {
			m.SetHScrollOffset(m.hscrollOffset - 4)
			return nil
		}
		return m.CollapseOrParent()

	// --- Filter / Escape ---
	case tea.KeyEsc:
		if m.filterPattern() != "" {
			m.ClearFilter()
		}
		return nil

	default:
		// Printable character keys and modifier combinations.
		return m.handleKeyString(key)
	}
}

func (m *FileTreeModel) handleFilterInputKey(key tea.KeyPressMsg) {
	if m.filterEd == nil {
		m.filterEd = ui.NewTextEdit()
	}
	switch key.Code {
	case tea.KeyEsc:
		m.ClearFilter()
		return
	case tea.KeyBackspace:
		if m.filterEd.Value() == "" {
			m.ClearFilter()
			return
		}
	}
	if editFilterPattern(m.filterEd, key) {
		m.SetFilter(m.filterEd.Value())
	}
}

// handleKeyString processes keys by their string representation, used as
// a fallback for printable and modifier-composite keys.
//
// Any bare printable character starts type-to-filter: it opens the filter
// row (like `/`) and seeds the pattern with the typed character. `/` keeps
// its role as the explicit filter-mode key with an empty pattern. The old
// bare-letter actions moved to modifier chords (refresh r→Ctrl+R, hidden
// toggle .→Ctrl+.) so they cannot collide with filter typing.
func (m *FileTreeModel) handleKeyString(key tea.KeyPressMsg) tea.Cmd {
	switch key.String() {
	case "/":
		return m.EnterFilterMode()
	case "ctrl+r":
		return m.RefreshFromDisk()
	case "ctrl+.":
		m.SetShowHidden(!m.ShowHidden())
		return nil
	case "ctrl+shift+g":
		return m.RefreshGitStatus()
	}
	if key.Mod.Contains(tea.ModCtrl) || key.Mod.Contains(tea.ModAlt) || key.Mod.Contains(tea.ModSuper) {
		return nil
	}
	if key.Text == "" {
		return nil
	}
	cmd := m.EnterFilterMode()
	m.handleFilterInputKey(key)
	return cmd
}

// ---------------------------------------------------------------------------
// handleMouse handles mouse interaction
// ---------------------------------------------------------------------------

// handleMouse processes mouse events (click, scroll, motion, etc.) and
// translates them into tree actions.
//
// Coordinate layout:
//
//	mouseY   →  index in visible nodes (after applying scroll offset)
//	index    =  mouseY + scrollOffset
func (m *FileTreeModel) handleMouse(msg tea.Msg) tea.Cmd {
	switch mm := msg.(type) {
	case tea.MouseClickMsg:
		return m.handleMouseClick(mm)

	case tea.MouseWheelMsg:
		return m.handleMouseWheel(mm)

	case tea.MouseMotionMsg:
		m.handleMouseMotion(mm)
		return nil
	}

	return nil
}

// handleMouseMotion updates the hover state when the mouse moves over
// the tree. The hover index is in visible-node space (same as
// cursorIndex), so render code can compare it directly to the loop
// variable. Returns true if the hover state changed; the caller uses
// this to decide whether to mark the view as dirty.
//
// Indices outside the visible range map to -1 (no hover), which is
// the same convention as every other hover-capable widget; see
// ui.Hover for the rationale.
func (m *FileTreeModel) handleMouseMotion(msg tea.MouseMotionMsg) bool {
	m.mu.RLock()
	so := m.scrollOffset
	visH := m.visibleHeight
	m.mu.RUnlock()

	nodes := m.VisibleNodes()
	idx := msg.Y + so
	if idx < 0 || idx >= len(nodes) || msg.Y >= visH {
		return m.Hover.Clear()
	}
	return m.Hover.Set(idx)
}

// handleMouseClick processes a mouse click event.
func (m *FileTreeModel) handleMouseClick(msg tea.MouseClickMsg) tea.Cmd {
	if msg.Button != tea.MouseLeft {
		return nil
	}

	m.mu.RLock()
	so := m.scrollOffset
	m.mu.RUnlock()

	nodes := m.VisibleNodes()
	idx := msg.Y + so
	if idx < 0 || idx >= len(nodes) {
		return nil
	}

	node := nodes[idx]

	// Select the node at the clicked position.
	m.Select(node.Path)

	// If clicking on a directory, toggle its expanded state.
	if node.IsDir {
		return m.ToggleExpand(node.Path)
	}
	if m.onFileSelected != nil {
		return m.onFileSelected(node.Path)
	}

	// File clicked: emit FileSelectedMsg so the parent opens it.
	return func() tea.Msg {
		return FileSelectedMsg{
			Path: node.Path,
			Node: node,
		}
	}
}

// handleMouseWheel processes a mouse wheel / scroll event.
func (m *FileTreeModel) handleMouseWheel(msg tea.MouseWheelMsg) tea.Cmd {
	scrollLines := 3
	switch msg.Button {
	case tea.MouseWheelUp:
		m.mu.Lock()
		if m.scrollOffset > 0 {
			m.scrollOffset -= scrollLines
			if m.scrollOffset < 0 {
				m.scrollOffset = 0
			}
		}
		m.mu.Unlock()
		for i := 0; i < scrollLines; i++ {
			m.MoveUp()
		}

	case tea.MouseWheelDown:
		nodes := m.VisibleNodes()
		m.mu.Lock()
		maxOffset := len(nodes) - m.visibleHeight
		if maxOffset < 0 {
			maxOffset = 0
		}
		if m.scrollOffset < maxOffset {
			m.scrollOffset += scrollLines
			if m.scrollOffset > maxOffset {
				m.scrollOffset = maxOffset
			}
		}
		m.mu.Unlock()
		for i := 0; i < scrollLines; i++ {
			m.MoveDown()
		}
	}

	return nil
}

// ---------------------------------------------------------------------------
// Page scrolling
// ---------------------------------------------------------------------------

// MovePageUp moves the cursor up by one page (visibleHeight rows).
func (m *FileTreeModel) MovePageUp() {
	pageSize := m.pageSize()
	if pageSize < 1 {
		pageSize = 1
	}
	idx := m.CursorIndex() - pageSize
	m.SetCursorIndex(idx)
	m.updateScrollOffset()
}

// MovePageDown moves the cursor down by one page (visibleHeight rows).
func (m *FileTreeModel) MovePageDown() {
	pageSize := m.pageSize()
	if pageSize < 1 {
		pageSize = 1
	}
	idx := m.CursorIndex() + pageSize
	m.SetCursorIndex(idx)
	m.updateScrollOffset()
}

// GoToTop sets the cursor to the first visible node.
func (m *FileTreeModel) GoToTop() {
	m.SetCursorIndex(0)
	m.mu.Lock()
	m.scrollOffset = 0
	m.mu.Unlock()
}

// GoToBottom sets the cursor to the last visible node.
func (m *FileTreeModel) GoToBottom() {
	nodes := m.VisibleNodes()
	m.SetCursorIndex(len(nodes) - 1)
	m.updateScrollOffset()
}

// pageSize returns the number of rows per page, defaulting to visibleHeight
// or a sensible fallback when the height has not been set.
func (m *FileTreeModel) pageSize() int {
	if m.visibleHeight > 0 {
		return m.visibleHeight
	}
	// Reasonable default when height has not been explicitly set.
	return 10
}

// updateScrollOffset adjusts the scroll offset so that the cursor remains
// within the visible area.  Only scrolls when the cursor is strictly outside
// the viewport: scroll up by 1 when above the top, scroll down by 1 when
// below the bottom.  Page-sized movements set scrollOffset directly.
func (m *FileTreeModel) updateScrollOffset() {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.updateScrollOffsetLocked()
}

// updateScrollOffsetLocked is updateScrollOffset for callers that already
// hold m.mu.
func (m *FileTreeModel) updateScrollOffsetLocked() {
	m.clampViewportLocked()
}

// ---------------------------------------------------------------------------
// Expand / Collapse actions
// ---------------------------------------------------------------------------

// ToggleExpandOrSelect performs the "activate" action on the current node:
// - Directory: toggles its expanded state.
// - File: returns a FileSelectedMsg command for the parent app.
func (m *FileTreeModel) ToggleExpandOrSelect() tea.Cmd {
	node := m.SelectedNode()
	if node == nil {
		return nil
	}

	if node.IsDir {
		return m.ToggleExpand(node.Path)
	}
	if m.onFileSelected != nil {
		return m.onFileSelected(node.Path)
	}

	// File selected: return a command that emits a message the parent app
	// can handle.
	return func() tea.Msg {
		return FileSelectedMsg{
			Path: node.Path,
			Node: node,
		}
	}
}

// CollapseOrParent performs the "back" action on the current node:
// - Expanded directory: collapses it.
// - Collapsed directory or file: moves cursor to the parent directory.
//
// This matches VSCode's Left arrow behaviour.
func (m *FileTreeModel) CollapseOrParent() tea.Cmd {
	node := m.SelectedNode()
	if node == nil {
		return nil
	}

	if node.IsDir && node.IsExpanded {
		m.Collapse(node.Path)
		return nil
	}

	// Move to parent directory.
	if node.Parent != nil {
		m.Select(node.Parent.Path)
	}
	return nil
}

// expandOrMoveIn is the counterpart for the Right arrow:
// - Expanded directory: moves into the first child (if any).
// - Collapsed directory: expands it.
// - File: no-op (select already on file).
func (m *FileTreeModel) expandOrMoveIn() tea.Cmd {
	node := m.SelectedNode()
	if node == nil {
		return nil
	}

	if !node.IsDir {
		return nil
	}

	if !node.IsExpanded {
		return m.Expand(node.Path)
	}

	// Already expanded: move the cursor to the first visible child.
	// Find the first child in visible nodes.
	nodes := m.VisibleNodes()
	cursorIdx := m.CursorIndex()
	for i := cursorIdx + 1; i < len(nodes); i++ {
		if nodes[i].Depth > node.Depth {
			m.Select(nodes[i].Path)
			return nil
		}
	}

	return nil
}

// ExpandAllNested expands the directory at the cursor and all of its
// descendants recursively.
func (m *FileTreeModel) ExpandAllNested() {
	m.mu.Lock()
	defer m.mu.Unlock()

	node := m.nodes[m.selectedPath]
	if node == nil || !node.IsDir {
		return
	}

	m.expandAllRecursive(node)
	m.rebuildVisibleNodes()
}

// CollapseAllNested collapses the directory at the cursor and all of its
// descendants recursively.
func (m *FileTreeModel) CollapseAllNested() {
	m.mu.Lock()
	defer m.mu.Unlock()

	node := m.nodes[m.selectedPath]
	if node == nil || !node.IsDir {
		return
	}

	m.collapseAllRecursive(node)
	m.rebuildVisibleNodes()
}

// ---------------------------------------------------------------------------
// Filter commands
// ---------------------------------------------------------------------------

// EnterFilterMode returns a command that signals the parent app to enter
// filter-input mode.
func (m *FileTreeModel) EnterFilterMode() tea.Cmd {
	m.filterActive = true
	return func() tea.Msg {
		return EnterFilterModeMsg{}
	}
}

// ApplyFilter sets the filter pattern on the tree.
func (m *FileTreeModel) ApplyFilter(pattern string) {
	m.SetFilter(pattern)
}

// ClearFilter removes the active filter and exits filter mode.
func (m *FileTreeModel) ClearFilter() {
	m.filterActive = false
	m.SetFilter("")
}

// FilterActive returns whether the tree is in filter-input mode.
func (m *FileTreeModel) FilterActive() bool {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return m.filterActive
}

// ---------------------------------------------------------------------------
// Refresh operations
// ---------------------------------------------------------------------------

// RefreshFromDisk reloads the entire tree from the file system.
// It re-reads the root directory, reloads all expanded directories, and
// rebuilds the visible-nodes list. Returns a command that emits a
// TreeRefreshDoneMsg when complete.
//
// This is an async operation executed in a background goroutine to avoid
// blocking the event loop on potentially large directory scans.
func (m *FileTreeModel) RefreshFromDisk() tea.Cmd {
	rootPath := m.RootPath()
	if m.noLocalIO {
		return m.RequestRemoteDirectory(rootPath)
	}

	return func() tea.Msg {
		err := m.refreshTreeFromDisk()

		return TreeRefreshDoneMsg{
			RootPath: rootPath,
			Model:    m,
			Err:      err,
		}
	}
}

// refreshTreeFromDisk performs the actual disk re-scan.
func (m *FileTreeModel) refreshTreeFromDisk() error {
	m.mu.Lock()
	defer m.mu.Unlock()

	// Re-stat the root to verify it still exists.
	_, err := os.Stat(m.rootPath)
	if err != nil {
		return fmt.Errorf("tree root no longer accessible: %w", err)
	}

	// Force the root to be expanded and reload it.
	m.Root.IsExpanded = true

	// Walk all loaded directories and reload their children.
	m.reloadAllLoadedDirectories(m.Root)

	m.reapplyFilterAfterMutationLocked()

	// Rebuild visible nodes. The projection boundary restores selection,
	// cursor, hover-independent scroll, and row-count invariants centrally.
	m.rebuildVisibleNodes()

	return nil
}

// reloadAllLoadedDirectories recursively reloads children for every
// directory that has already been loaded (Children != nil).
// Must be called with m.mu held.
func (m *FileTreeModel) reloadAllLoadedDirectories(node *TreeNode) {
	if node == nil || !node.IsDir {
		return
	}

	// Reload this directory's children.
	_ = m.loadDirectory(node)

	// Recurse into children that are themselves loaded directories.
	for _, child := range node.Children {
		if child.IsDir && child.Children != nil {
			m.reloadAllLoadedDirectories(child)
		}
	}
}

// RefreshGitStatus starts one asynchronous status scan. Requests received
// while a scan is active are coalesced into exactly one trailing scan so a
// burst of file/index/ref events cannot lose the final repository state.
func (m *FileTreeModel) RefreshGitStatus() tea.Cmd {
	m.mu.Lock()
	resolver := m.gitResolver
	if resolver == nil || !resolver.Available() {
		m.mu.Unlock()
		return nil
	}
	if m.gitRefreshPending {
		m.gitRefreshQueued = true
		m.mu.Unlock()
		return nil
	}
	m.gitRefreshPending = true
	m.gitRefreshGen++
	gen := m.gitRefreshGen
	m.mu.Unlock()

	return func() tea.Msg {
		statuses, err := resolver.scan()
		return TreeGitRefreshMsg{Model: m, Generation: gen, Statuses: statuses, Err: err}
	}
}

func (m *FileTreeModel) applyGitRefresh(msg TreeGitRefreshMsg) tea.Cmd {
	m.mu.Lock()
	if msg.Model != m || msg.Generation == 0 || msg.Generation != m.gitRefreshGen {
		m.mu.Unlock()
		return nil
	}
	m.gitRefreshPending = false
	if msg.Err == nil && m.gitResolver != nil {
		m.gitResolver.replaceStatuses(msg.Statuses)
		m.syncNodeGitStatusesLocked()
	}
	runAgain := m.gitRefreshQueued
	m.gitRefreshQueued = false
	m.mu.Unlock()

	if runAgain {
		return m.RefreshGitStatus()
	}
	return nil
}

// syncNodeGitStatusesLocked applies the resolver snapshot to every loaded row,
// including GitNone. Updating only aggregates leaves old M/D/U letters stuck
// on file nodes after commits. The caller must hold m.mu.
func (m *FileTreeModel) syncNodeGitStatusesLocked() {
	if m.gitResolver == nil {
		return
	}
	for path, node := range m.nodes {
		if node != nil {
			node.GitStatus = m.gitResolver.Resolve(path)
		}
	}
	m.recomputeAggregateStatuses()
}

// ---------------------------------------------------------------------------
// Scroll offset helpers
// ---------------------------------------------------------------------------

// ScrollOffset returns the current scroll offset (rows scrolled off top).
func (m *FileTreeModel) ScrollOffset() int {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return m.scrollOffset
}

// VisibleHeight returns the viewport row count last set by TreeHeightMsg.
func (m *FileTreeModel) VisibleHeight() int {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return m.visibleHeight
}

// SetScrollOffset sets the scroll offset, clamped to valid range.
func (m *FileTreeModel) SetScrollOffset(offset int) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.scrollOffset = offset
	m.clampScrollRangeLocked(m.visibleHeight)
}

// HScrollOffset returns the horizontal scroll offset.
func (m *FileTreeModel) HScrollOffset() int {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return m.hscrollOffset
}

// SetHScrollOffset sets the horizontal scroll offset, clamped to a reasonable
// maximum (currently 128 columns). Returns the new offset.
func (m *FileTreeModel) SetHScrollOffset(off int) int {
	m.mu.Lock()
	defer m.mu.Unlock()
	if off < 0 {
		off = 0
	}
	if off > 128 {
		off = 128
	}
	m.hscrollOffset = off
	return off
}
