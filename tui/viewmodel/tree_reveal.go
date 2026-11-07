package viewmodel

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"

	tea "charm.land/bubbletea/v2"

	"crude/shellport"
	"crude/tui/filetree"
	"crude/tui/screens/shelltree"
)

// treeRevealTraversal is shared by the interactive and shell-control routes.
// Directory pages are installed before any ancestor is expanded or selected.
type treeRevealTraversal struct {
	model       *filetree.FileTreeModel
	screen      *shelltree.Screen
	paths       []string
	directories []string
}

func newTreeRevealTraversal(model *filetree.FileTreeModel, screen *shelltree.Screen, paths []string) (treeRevealTraversal, error) {
	c := treeRevealTraversal{model: model, screen: screen}
	root := model.RootPath()
	seen := make(map[string]bool)
	for _, path := range paths {
		if strings.TrimSpace(path) == "" {
			return c, errors.New("empty path")
		}
		if !filepath.IsAbs(path) {
			path = filepath.Join(root, path)
		}
		path = filepath.Clean(path)
		rel, err := filepath.Rel(root, path)
		if err != nil || rel == "." || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
			return c, errors.New("path must name a descendant of the workspace root")
		}
		c.paths = append(c.paths, path)
		parent := root
		for _, segment := range strings.Split(filepath.Dir(rel), string(filepath.Separator)) {
			if !seen[parent] {
				c.directories = append(c.directories, parent)
				seen[parent] = true
			}
			if segment != "." {
				parent = filepath.Join(parent, segment)
			}
		}
		if !seen[parent] {
			c.directories = append(c.directories, parent)
			seen[parent] = true
		}
	}
	return c, nil
}

func (c *treeRevealTraversal) directoryValid(path string, allowLinks bool) bool {
	node := c.model.NodeByPath(path)
	return node != nil && node.IsDir && (allowLinks || !node.IsSymlink)
}

func (c *treeRevealTraversal) selectPaths(showHidden, allowLinks, markPaths bool) bool {
	for _, path := range c.paths {
		node := c.model.NodeByPath(path)
		if node == nil || (!allowLinks && node.IsSymlink) {
			return false
		}
	}
	if showHidden && !c.model.ShowHidden() {
		root := c.model.RootPath()
		for _, path := range c.paths {
			rel, _ := filepath.Rel(root, path)
			for _, part := range strings.Split(rel, string(filepath.Separator)) {
				if strings.HasPrefix(part, ".") {
					c.model.SetShowHidden(true)
					break
				}
			}
		}
	}
	for _, path := range c.paths {
		for parent := filepath.Dir(path); ; parent = filepath.Dir(parent) {
			// Pages were loaded above, so Expand cannot perform I/O.
			_ = c.model.Expand(parent)
			if parent == c.model.RootPath() {
				break
			}
		}
	}
	if !c.model.SelectVisible(c.paths[len(c.paths)-1]) {
		return false
	}
	marks := make(map[string]bool)
	if markPaths {
		for _, path := range c.paths {
			marks[path] = true
		}
	}
	c.model.SetSelectionMarks(marks)
	return true
}

type treeRevealIntent struct {
	treeRevealTraversal
	workspace  shellport.Workspace
	owner      paneMenuOwner
	tabTitle   string
	generation uint64
	sequence   uint64
	ctx        context.Context
	cancel     context.CancelFunc
	retries    int
}

type treeRevealDirectoryDone struct {
	intent *treeRevealIntent
	result tea.Msg
}

func (m *App) cancelTreeReveal() {
	if m.treeReveal != nil {
		m.treeReveal.cancel()
		m.treeReveal = nil
	}
	if state := m.asyncIO.Load(); state != nil {
		state.mu.Lock()
		state.treeControlSequence++
		state.mu.Unlock()
	}
}

func (m *App) beginUserTreeReveal(path string, owner paneMenuOwner) tea.Cmd {
	m.cancelTreeReveal()
	if m.Tree == nil || m.Tree.Model == nil || m.sidebarScreen == nil || !owner.valid(m) {
		return nil
	}
	traversal, err := newTreeRevealTraversal(m.Tree.Model, m.sidebarScreen, []string{path})
	if err != nil {
		m.Feedback.NotifyError("Cannot show this file in the tree. Open its workspace folder and try again.")
		return nil
	}
	m.setActivity(activityFiles, true)
	m.pauseTreePathSearch(m.sidebarScreen)
	m.sidebarScreen.ClearPathSearch()
	m.setFocusTree()
	m.configureShell()
	if m.rootHost != nil {
		m.rootHost.Focus(shelltree.TreeID)
	}
	ctx, cancel := context.WithCancel(context.Background())
	state := asyncIOStateFor(m)
	state.mu.Lock()
	sequence := state.treeControlSequence
	state.mu.Unlock()
	intent := &treeRevealIntent{treeRevealTraversal: traversal, workspace: m.ws, owner: owner, tabTitle: m.editorTabTitle(path),
		generation: m.workspaceSwitchGen.Load(), sequence: sequence, ctx: ctx, cancel: cancel}
	m.treeReveal = intent
	return m.continueUserTreeReveal(intent)
}

// A deliberate tree interaction supersedes an outstanding reveal. Pointer
// motion and unrelated editor input do not cancel it.
func (m *App) cancelTreeRevealOnInput(msg tea.Msg) {
	if m.treeReveal == nil && m.shellControlPending == 0 {
		return
	}
	switch input := msg.(type) {
	case tea.KeyPressMsg, tea.PasteMsg:
		if m.focusTarget() == FocusTree && m.focusedPopup() == nil {
			m.cancelTreeReveal()
		}
	case tea.MouseClickMsg:
		if m.revealTreeContains(input.X, input.Y) {
			m.cancelTreeReveal()
		}
	case tea.MouseWheelMsg:
		if m.revealTreeContains(input.X, input.Y) {
			m.cancelTreeReveal()
		}
	}
}

func (m *App) revealTreeContains(x, y int) bool {
	if m.rootHost == nil || m.Tree == nil || !m.Tree.Visible || m.sidebarScreen == nil || m.activityView != activityFiles {
		return false
	}
	rect, ok := m.rootHost.ScreenRect(m.sidebarScreen.ID())
	return ok && rect.Contains(x, y)
}

func (m *App) treeRevealCurrent(intent *treeRevealIntent) bool {
	if intent == nil || m.treeReveal != intent || intent.ctx.Err() != nil || m.asyncIOStopped.Load() ||
		m.ws != intent.workspace || m.workspaceSwitchGen.Load() != intent.generation ||
		m.Tree == nil || !m.Tree.Visible || m.Tree.Model != intent.model ||
		m.activityView != activityFiles || m.sidebarScreen != intent.screen || !intent.owner.valid(m) {
		return false
	}
	tab := m.Win.TabByID(intent.owner.tab)
	if tab == nil || tab.Title != intent.tabTitle {
		return false
	}
	state := asyncIOStateFor(m)
	state.mu.Lock()
	current := state.treeControlSequence == intent.sequence
	state.mu.Unlock()
	return current
}

func (m *App) continueUserTreeReveal(intent *treeRevealIntent) tea.Cmd {
	if !m.treeRevealCurrent(intent) {
		if m.treeReveal == intent {
			m.cancelTreeReveal()
		}
		return nil
	}
	if len(intent.directories) == 0 {
		if !intent.selectPaths(true, true, false) {
			m.failUserTreeReveal(intent, "Cannot show this file in the tree. Refresh the tree and try again.")
			return nil
		}
		m.treeReveal = nil
		intent.cancel()
		if m.rootHost != nil {
			m.rootHost.MarkLayout()
		}
		m.forceFrame()
		return nil
	}
	path := intent.directories[0]
	if !intent.directoryValid(path, true) {
		m.failUserTreeReveal(intent, "Cannot show this file in the tree. Refresh the tree and try again.")
		return nil
	}
	var cmd tea.Cmd
	if _, connected := m.ws.(shellport.ClientFiles); connected {
		ctx := intent.ctx
		if node := intent.model.NodeByPath(path); node != nil && node.Loading {
			// This read supersedes an existing tree refresh. Keep the refresh
			// alive even if the reveal is cancelled, so its row does not stay
			// loading after the older result is rejected.
			ctx = context.Background()
		}
		cmd = intent.model.RequestRemoteDirectoryContext(ctx, path)
	} else {
		// In-process fixtures and local projections still do disk reads off Update.
		cmd = m.localTreeRevealDirectory(intent, path)
	}
	if cmd == nil {
		m.failUserTreeReveal(intent, "Cannot load the file tree. Try again.")
		return nil
	}
	return func() tea.Msg { return treeRevealDirectoryDone{intent: intent, result: cmd()} }
}

func (m *App) localTreeRevealDirectory(intent *treeRevealIntent, path string) tea.Cmd {
	return m.clientIOCommand(intent.ctx, func(ctx context.Context) tea.Msg {
		listing, err := os.ReadDir(path)
		entries := make([]filetree.DirectoryEntry, 0, len(listing))
		for _, entry := range listing {
			child := filepath.Join(path, entry.Name())
			info, infoErr := os.Lstat(child)
			item := filetree.DirectoryEntry{Name: entry.Name(), Path: child, IsDir: entry.IsDir()}
			if infoErr == nil {
				item.IsSymlink, item.ModTime = info.Mode()&os.ModeSymlink != 0, info.ModTime()
				if item.IsSymlink {
					if target, statErr := os.Stat(child); statErr == nil {
						item.IsDir = target.IsDir()
					}
				}
			}
			entries = append(entries, item)
		}
		if ctx.Err() != nil {
			err = ctx.Err()
		}
		return clientTreeDirectoryMsg{generation: intent.generation, latest: func() uint64 { return 1 },
			result: filetree.RemoteDirectoryMsg{Model: intent.model, Path: path, Generation: 1, Entries: entries, Err: err}}
	})
}

func (m *App) finishUserTreeReveal(done treeRevealDirectoryDone) tea.Cmd {
	intent := done.intent
	if !m.treeRevealCurrent(intent) {
		// A completed page is still a valid tree refresh, but cannot select or
		// expand anything for an obsolete reveal. The directory owner's own
		// workspace and per-path sequence fences decide whether to install it.
		if result, ok := done.result.(clientTreeDirectoryMsg); ok {
			m.applyClientTreeDirectory(result)
		}
		if m.treeReveal == intent {
			m.cancelTreeReveal()
		}
		return nil
	}
	result, ok := done.result.(clientTreeDirectoryMsg)
	if !ok {
		m.failUserTreeReveal(intent, "Cannot load the file tree. Check the folder and try again.")
		return nil
	}
	if result.latest == nil || result.result.Generation != result.latest() {
		intent.retries++
		if intent.retries <= 3 {
			return m.continueUserTreeReveal(intent)
		}
		m.failUserTreeReveal(intent, "The file tree changed while it was loading. Try again.")
		return nil
	}
	if result.result.Err != nil {
		m.applyClientTreeDirectory(result)
		m.failUserTreeReveal(intent, "Cannot load the file tree. Check the folder and try again.")
		return nil
	}
	m.applyClientTreeDirectory(result)
	intent.retries = 0
	intent.directories = intent.directories[1:]
	return m.continueUserTreeReveal(intent)
}

func (m *App) failUserTreeReveal(intent *treeRevealIntent, message string) {
	if m.treeReveal == intent {
		m.treeReveal = nil
		intent.cancel()
		m.Feedback.NotifyError(message)
	}
}
