package viewmodel

import (
	"context"
	"strings"

	tea "charm.land/bubbletea/v2"

	"crude/shellport"
	"crude/tui/filetree"
	"crude/tui/screens/shelltree"
	"crude/workspace"
)

type treePathSearchState struct {
	next    uint64
	latest  map[*shelltree.Screen]uint64
	cancel  map[*shelltree.Screen]context.CancelFunc
	root    map[*shelltree.Screen]string
	hidden  map[*shelltree.Screen]bool
	running map[*shelltree.Screen]bool
	query   map[*shelltree.Screen]string
	catalog map[*shelltree.Screen]workspace.BrowserCatalog
}

func newTreePathSearchState() *treePathSearchState {
	return &treePathSearchState{
		latest:  make(map[*shelltree.Screen]uint64),
		cancel:  make(map[*shelltree.Screen]context.CancelFunc),
		root:    make(map[*shelltree.Screen]string),
		hidden:  make(map[*shelltree.Screen]bool),
		running: make(map[*shelltree.Screen]bool),
		query:   make(map[*shelltree.Screen]string),
		catalog: make(map[*shelltree.Screen]workspace.BrowserCatalog),
	}
}

func (s *treePathSearchState) clear() {
	for key := range s.latest {
		delete(s.latest, key)
	}
	for key := range s.root {
		delete(s.root, key)
	}
	for key := range s.hidden {
		delete(s.hidden, key)
	}
	for key := range s.running {
		delete(s.running, key)
	}
	for key := range s.query {
		delete(s.query, key)
	}
	for key := range s.catalog {
		delete(s.catalog, key)
	}
}

type treePathSearchResultMsg struct {
	target     *shelltree.Screen
	generation uint64
	query      string
	catalog    workspace.BrowserCatalog
	matches    []filetree.PathMatch
	err        error
}

func (m *App) beginTreePathSearch(query string) tea.Cmd {
	if m == nil || m.sidebarScreen == nil || m.Tree == nil || m.Tree.Model == nil {
		return nil
	}
	target := m.sidebarScreen
	if strings.TrimSpace(query) == "" {
		m.pauseTreePathSearch(target)
		target.ClearPathSearch()
		return nil
	}
	if !target.BeginPathSearch(query) {
		return nil
	}
	state := asyncIOStateFor(m).treePathSearch
	root := m.Tree.Model.RootPath()
	includeHidden := m.Tree.Model.ShowHidden()
	stateParent := asyncIOStateFor(m)
	stateParent.mu.Lock()
	state.query[target] = query
	if state.root[target] == root && state.hidden[target] == includeHidden {
		if state.running[target] {
			stateParent.mu.Unlock()
			return nil
		}
		if catalog, ok := state.catalog[target]; ok {
			state.next++
			generation := state.next
			state.latest[target] = generation
			stateParent.mu.Unlock()
			return rankTreeCatalogCmd(target, generation, query, catalog)
		}
	}
	if cancel := state.cancel[target]; cancel != nil {
		cancel()
	}
	state.next++
	generation := state.next
	ctx, cancel := context.WithCancel(context.Background())
	state.latest[target] = generation
	state.cancel[target] = cancel
	state.root[target] = root
	state.hidden[target] = includeHidden
	state.running[target] = true
	stateParent.mu.Unlock()
	files, connected := m.ws.(shellport.ClientFiles)

	return func() tea.Msg {
		var catalog workspace.BrowserCatalog
		var err error
		if connected {
			catalog, err = files.CatalogFiles(ctx, includeHidden)
		} else {
			catalog, err = workspace.ScanBrowserPaths(ctx, workspace.BrowserPathRequest{Root: root, IncludeHidden: includeHidden})
		}
		stateParent.mu.Lock()
		latestQuery := state.query[target]
		stateParent.mu.Unlock()
		result := treePathSearchResultMsg{target: target, generation: generation, query: latestQuery, catalog: catalog, err: err}
		if err == nil {
			result.matches = filetree.RankPathEntries(catalog.Entries, latestQuery)
		}
		return result
	}
}

func rankTreeCatalogCmd(target *shelltree.Screen, generation uint64, query string, catalog workspace.BrowserCatalog) tea.Cmd {
	return func() tea.Msg {
		return treePathSearchResultMsg{
			target: target, generation: generation, query: query, catalog: catalog,
			matches: filetree.RankPathEntries(catalog.Entries, query),
		}
	}
}

func (m *App) applyTreePathSearchResult(result treePathSearchResultMsg) tea.Cmd {
	if m == nil || result.target == nil {
		return nil
	}
	stateParent := asyncIOStateFor(m)
	stateParent.mu.Lock()
	state := stateParent.treePathSearch
	latest := state.latest[result.target]
	if result.generation == 0 || result.generation != latest {
		stateParent.mu.Unlock()
		return nil
	}
	query := state.query[result.target]
	if result.err == nil {
		state.catalog[result.target] = result.catalog
	}
	state.running[result.target] = false
	delete(state.cancel, result.target)
	stateParent.mu.Unlock()
	if result.err != nil {
		return nil
	}
	if result.query != query {
		return rankTreeCatalogCmd(result.target, result.generation, query, result.catalog)
	}
	if result.target != m.sidebarScreen || !result.target.Visible() {
		return nil
	}
	result.target.ApplyPathSearch(query, result.matches)
	if m.rootHost != nil {
		m.rootHost.MarkLayout()
	}
	return nil
}

func (m *App) pauseTreePathSearch(target *shelltree.Screen) {
	if m == nil || target == nil {
		return
	}
	stateParent := asyncIOStateFor(m)
	stateParent.mu.Lock()
	state := stateParent.treePathSearch
	state.next++
	delete(state.latest, target)
	delete(state.query, target)
	state.running[target] = false
	if cancel := state.cancel[target]; cancel != nil {
		cancel()
		delete(state.cancel, target)
	}
	stateParent.mu.Unlock()
}

func (m *App) invalidateTreePathSearch() {
	if m == nil || m.sidebarScreen == nil {
		return
	}
	stateParent := asyncIOStateFor(m)
	stateParent.mu.Lock()
	state := stateParent.treePathSearch
	state.next++
	if cancel := state.cancel[m.sidebarScreen]; cancel != nil {
		cancel()
	}
	delete(state.latest, m.sidebarScreen)
	delete(state.cancel, m.sidebarScreen)
	delete(state.root, m.sidebarScreen)
	delete(state.hidden, m.sidebarScreen)
	delete(state.running, m.sidebarScreen)
	delete(state.query, m.sidebarScreen)
	delete(state.catalog, m.sidebarScreen)
	stateParent.mu.Unlock()
}
