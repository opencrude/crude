package viewmodel

import (
	"path/filepath"
	"strings"

	tea "charm.land/bubbletea/v2"
	"crude/shellport"
	"crude/tui/filetree"
	"crude/tui/screens/shelltree"
	"crude/types/control"
)

type treeControlCompletion struct {
	model       *filetree.FileTreeModel
	screen      *shelltree.Screen
	sequence    uint64
	paths       []string
	directories []string
	query       string
	filter      bool
	message     tea.Msg
}

func (m *App) shellControlStillOwned(msg shellport.ControlRequestMsg, generation uint64) bool {
	port, ok := m.ws.(shellport.ShellControl)
	if !ok || port != msg.Owner || m.asyncIOStopped.Load() || generation != m.workspaceSwitchGen.Load() {
		return false
	}
	want, got := msg.Envelope.Request.Target, port.ControlIdentity()
	if want.WorkspaceID != got.WorkspaceID || want.Shell == nil || got.Shell == nil || *want.Shell != *got.Shell {
		return false
	}
	if want.SessionID != "" && m.ws.SessionsByID()[want.SessionID] == nil {
		return false
	}
	s := m.inspectShell()
	p := msg.Envelope.Request.Preconditions
	return (p.OpenEpoch == nil || *p.OpenEpoch == s.OpenEpoch) && (p.Revision == nil || *p.Revision == s.Revision)
}

func (m *App) beginTreeControl(msg shellport.ControlRequestMsg) tea.Cmd {
	if m.Tree == nil || m.Tree.Model == nil || m.sidebarScreen == nil {
		return m.failShellControl(msg, control.Unavailable, "tree owner unavailable")
	}
	if _, ok := m.ws.(shellport.ClientFiles); !ok {
		return m.failShellControl(msg, control.Unavailable, "connected tree service required")
	}
	c := treeControlCompletion{model: m.Tree.Model, screen: m.sidebarScreen}
	r := msg.Envelope.Request
	if r.CommandID == "tree.filter" {
		var in struct {
			Query *string `json:"query"`
		}
		if err := decodeShellInput(r.Input, &in); err != nil {
			return m.failShellControl(msg, control.Invalid, err.Error())
		}
		if in.Query == nil {
			return m.failShellControl(msg, control.Invalid, "query required")
		}
		c.filter, c.query = true, *in.Query
	} else {
		var in struct {
			Paths []string `json:"paths"`
		}
		if err := decodeShellInput(r.Input, &in); err != nil {
			return m.failShellControl(msg, control.Invalid, err.Error())
		}
		if len(in.Paths) == 0 || len(in.Paths) > control.MaxPageLimit {
			return m.failShellControl(msg, control.Invalid, "1 to 128 paths required")
		}
		root := c.model.RootPath()
		seen := map[string]bool{}
		for _, path := range in.Paths {
			if strings.TrimSpace(path) == "" {
				return m.failShellControl(msg, control.Invalid, "empty path")
			}
			if !filepath.IsAbs(path) {
				path = filepath.Join(root, path)
			}
			path = filepath.Clean(path)
			rel, err := filepath.Rel(root, path)
			if err != nil || rel == "." || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
				return m.failShellControl(msg, control.Invalid, "path must name a descendant of the workspace root")
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
	}
	if m.shellControlPending >= 16 {
		return m.failShellControl(msg, control.Unavailable, "shell operation capacity exceeded")
	}
	state := asyncIOStateFor(m)
	state.mu.Lock()
	state.treeControlSequence++
	c.sequence = state.treeControlSequence
	state.mu.Unlock()
	if !m.Tree.Visible {
		return m.failShellControl(msg, control.Unavailable, "show tree before revealing or filtering")
	}
	if c.filter {
		// BeginPathSearch synchronizes the visible filter field; the existing
		// service owns cancellation, catalog generation and ranking.
		m.pauseTreePathSearch(c.screen)
		cmd := m.beginTreePathSearch(c.query)
		if cmd == nil {
			if strings.TrimSpace(c.query) != "" {
				return m.failShellControl(msg, control.Unavailable, "search command unavailable")
			}
			m.forceFrame()
			return m.replyShellControl(msg, control.Result{State: control.Applied})
		}
		return m.awaitTreeControl(msg, c, cmd)
	}
	m.pauseTreePathSearch(c.screen)
	c.screen.ClearPathSearch()
	return m.continueTreeReveal(msg, c)
}

func (m *App) awaitTreeControl(msg shellport.ControlRequestMsg, c treeControlCompletion, cmd tea.Cmd) tea.Cmd {
	return m.awaitShellControl(msg, cmd, func(message tea.Msg) tea.Msg { c.message = message; return c })
}

func (m *App) continueTreeReveal(msg shellport.ControlRequestMsg, c treeControlCompletion) tea.Cmd {
	if len(c.directories) != 0 {
		path := c.directories[0]
		c.directories = c.directories[1:]
		node := c.model.NodeByPath(path)
		if node == nil || !node.IsDir || node.IsSymlink {
			return m.failShellControl(msg, control.Invalid, "ancestor missing or unsafe")
		}
		cmd := c.model.RequestRemoteDirectory(path)
		if cmd == nil {
			return m.failShellControl(msg, control.Unavailable, "directory owner unavailable")
		}
		return m.awaitTreeControl(msg, c, cmd)
	}
	for _, path := range c.paths {
		node := c.model.NodeByPath(path)
		if node == nil || node.IsSymlink {
			return m.failShellControl(msg, control.Invalid, "requested path missing or unsafe")
		}
	}
	for _, path := range c.paths {
		for parent := filepath.Dir(path); ; parent = filepath.Dir(parent) {
			// Every ancestor was loaded asynchronously; Expand cannot perform I/O.
			_ = c.model.Expand(parent)
			if parent == c.model.RootPath() {
				break
			}
		}
	}
	c.model.SetSelectionMarks(func() map[string]bool {
		out := map[string]bool{}
		for _, p := range c.paths {
			out[p] = true
		}
		return out
	}())
	c.model.Select(c.paths[len(c.paths)-1])
	if node := c.model.SelectedNode(); node == nil || node.Path != c.paths[len(c.paths)-1] {
		return m.failShellControl(msg, control.Unavailable, "path is not visible in tree")
	}
	m.configureShell()
	m.rootHost.MarkLayout()
	m.rootHost.Layout()
	m.forceFrame()
	return m.replyShellControl(msg, control.Result{State: control.Applied, ResourceIDs: c.paths})
}

func (m *App) finishTreeControl(done shellControlDoneMsg, c treeControlCompletion, valid bool) tea.Cmd {
	state := asyncIOStateFor(m)
	state.mu.Lock()
	latest := state.treeControlSequence
	state.mu.Unlock()
	if !valid || c.sequence != latest || m.Tree == nil || m.Tree.Model != c.model || m.sidebarScreen != c.screen {
		return m.failShellControl(done.request, control.Stale, "tree owner or intent changed")
	}
	if c.filter {
		result, ok := c.message.(treePathSearchResultMsg)
		if !ok {
			return m.failShellControl(done.request, control.OperationFailed, "unexpected search completion")
		}
		state.mu.Lock()
		current := state.treePathSearch.latest[c.screen] == result.generation && state.treePathSearch.query[c.screen] == c.query
		state.mu.Unlock()
		if !current || result.query != c.query {
			return m.failShellControl(done.request, control.Stale, "tree query changed")
		}
		if result.err != nil {
			return m.failShellControl(done.request, control.OperationFailed, result.err.Error())
		}
		m.applyTreePathSearchResult(result)
		m.configureShell()
		m.rootHost.MarkLayout()
		m.rootHost.Layout()
		m.forceFrame()
		return m.replyShellControl(done.request, control.Result{State: control.Applied})
	}
	result, ok := c.message.(clientTreeDirectoryMsg)
	if !ok {
		return m.failShellControl(done.request, control.OperationFailed, "unexpected directory completion")
	}
	if result.result.Err != nil {
		return m.failShellControl(done.request, control.OperationFailed, result.result.Err.Error())
	}
	if result.latest == nil || result.result.Generation != result.latest() {
		return m.failShellControl(done.request, control.Stale, "directory changed while loading")
	}
	m.applyClientTreeDirectory(result)
	return m.continueTreeReveal(done.request, c)
}
