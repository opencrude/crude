package viewmodel

import (
	"context"
	"strings"

	tea "charm.land/bubbletea/v2"

	"crude/shellport"
	"crude/tui/filetree"
	filepickerscreen "crude/tui/screens/filepicker"
	"crude/workspace"
)

type pickerPathSearchResultMsg struct {
	target     *filepickerscreen.Screen
	generation uint64
	intent     filepickerscreen.IntentToken
	query      string
	catalog    workspace.BrowserCatalog
	matches    []filetree.PathMatch
	err        error
}

func (m *App) beginPickerPathSearch(msg filepickerscreen.SearchMsg) tea.Cmd {
	if msg.Target == nil || !msg.ValidFor(m.FilePicker) {
		return nil
	}
	if strings.TrimSpace(msg.Query) == "" {
		m.pausePathSearch(msg.Target)
		msg.Target.ClearPathSearch()
		m.markFilePickerDirty()
		return nil
	}
	if !msg.Target.BeginPathSearch(msg.Query) {
		return nil
	}
	state := asyncIOStateFor(m)
	root := msg.Target.Current()
	directoriesOnly := msg.Target.Mode() == filepickerscreen.ModeFolder
	includeHidden := msg.Target.ShowHidden()
	state.mu.Lock()
	state.pathSearchQuery[msg.Target] = msg.Query
	state.pathSearchIntent[msg.Target] = msg.Intent()
	if state.pathSearchRoot[msg.Target] == root && state.pathSearchDirsOnly[msg.Target] == directoriesOnly && state.pathSearchHidden[msg.Target] == includeHidden {
		if state.pathSearchRunning[msg.Target] {
			// The active walk is independent of the query. Only the latest query
			// and intent need to be replaced; its completion will rank against
			// these values.
			state.mu.Unlock()
			return nil
		}
		if catalog, ok := state.pathSearchCatalog[msg.Target]; ok {
			state.nextPathSearch++
			generation := state.nextPathSearch
			state.latestPathSearch[msg.Target] = generation
			state.mu.Unlock()
			return rankPickerCatalogCmd(msg.Target, generation, msg.Query, msg.Intent(), catalog)
		}
	}
	if cancel := state.pathSearchCancel[msg.Target]; cancel != nil {
		cancel()
	}
	state.nextPathSearch++
	generation := state.nextPathSearch
	ctx, cancel := context.WithCancel(context.Background())
	state.latestPathSearch[msg.Target] = generation
	state.pathSearchCancel[msg.Target] = cancel
	state.pathSearchRoot[msg.Target] = root
	state.pathSearchDirsOnly[msg.Target] = directoriesOnly
	state.pathSearchHidden[msg.Target] = includeHidden
	state.pathSearchRunning[msg.Target] = true
	state.mu.Unlock()
	files, connected := m.ws.(shellport.ClientFiles)

	return func() tea.Msg {
		var catalog workspace.BrowserCatalog
		var err error
		if connected {
			catalog, err = files.CatalogFiles(ctx, includeHidden)
			if directoriesOnly {
				entries := catalog.Entries[:0]
				for _, entry := range catalog.Entries {
					if entry.IsDir {
						entries = append(entries, entry)
					}
				}
				catalog.Entries = entries
			}
		} else {
			catalog, err = workspace.ScanBrowserPaths(ctx, workspace.BrowserPathRequest{Root: root, DirectoriesOnly: directoriesOnly, IncludeHidden: includeHidden})
		}
		state.mu.Lock()
		query := state.pathSearchQuery[msg.Target]
		intent := state.pathSearchIntent[msg.Target]
		state.mu.Unlock()
		result := pickerPathSearchResultMsg{
			target: msg.Target, generation: generation, intent: intent,
			query: query, catalog: catalog, err: err,
		}
		if err == nil {
			result.matches = filetree.RankPathEntries(catalog.Entries, query)
		}
		return result
	}
}

func rankPickerCatalogCmd(
	target *filepickerscreen.Screen,
	generation uint64,
	query string,
	intent filepickerscreen.IntentToken,
	catalog workspace.BrowserCatalog,
) tea.Cmd {
	return func() tea.Msg {
		return pickerPathSearchResultMsg{
			target: target, generation: generation, intent: intent,
			query: query, catalog: catalog,
			matches: filetree.RankPathEntries(catalog.Entries, query),
		}
	}
}

func (m *App) applyPickerPathSearchResult(result pickerPathSearchResultMsg) tea.Cmd {
	if m == nil || result.target == nil {
		return nil
	}
	state := asyncIOStateFor(m)
	state.mu.Lock()
	latest := state.latestPathSearch[result.target]
	if result.generation == 0 || result.generation != latest {
		state.mu.Unlock()
		return nil
	}
	currentQuery := state.pathSearchQuery[result.target]
	currentIntent := state.pathSearchIntent[result.target]
	if result.err == nil {
		state.pathSearchCatalog[result.target] = result.catalog
	}
	state.pathSearchRunning[result.target] = false
	if cancel := state.pathSearchCancel[result.target]; cancel != nil {
		delete(state.pathSearchCancel, result.target)
	}
	state.mu.Unlock()

	if result.err != nil {
		if result.target.IsCurrentIntent(currentIntent.Epoch, currentIntent.Sequence) {
			result.target.SetError(result.err)
			m.markFilePickerDirty()
		}
		return nil
	}
	if result.query != currentQuery || !result.target.IsCurrentIntent(currentIntent.Epoch, currentIntent.Sequence) {
		return rankPickerCatalogCmd(result.target, result.generation, currentQuery, currentIntent, result.catalog)
	}
	if result.target != m.FilePicker || !result.target.IsOpen() {
		return nil
	}
	result.target.ApplyPathSearch(currentQuery, result.matches)
	m.markFilePickerDirty()
	return nil
}

// pausePathSearch stops an incomplete walk but retains a completed catalog for
// the same picker root so clearing and retyping a query never rescans it.
func (m *App) pausePathSearch(target *filepickerscreen.Screen) {
	if m == nil || target == nil {
		return
	}
	state := asyncIOStateFor(m)
	state.mu.Lock()
	state.nextPathSearch++
	delete(state.latestPathSearch, target)
	delete(state.pathSearchQuery, target)
	delete(state.pathSearchIntent, target)
	state.pathSearchRunning[target] = false
	if cancel := state.pathSearchCancel[target]; cancel != nil {
		cancel()
		delete(state.pathSearchCancel, target)
	}
	state.mu.Unlock()
}

func (m *App) invalidatePathSearch(target *filepickerscreen.Screen) {
	if m == nil || target == nil {
		return
	}
	state := asyncIOStateFor(m)
	state.mu.Lock()
	state.nextPathSearch++
	delete(state.latestPathSearch, target)
	delete(state.pathSearchRoot, target)
	delete(state.pathSearchDirsOnly, target)
	delete(state.pathSearchHidden, target)
	delete(state.pathSearchCatalog, target)
	delete(state.pathSearchRunning, target)
	delete(state.pathSearchQuery, target)
	delete(state.pathSearchIntent, target)
	if cancel := state.pathSearchCancel[target]; cancel != nil {
		cancel()
		delete(state.pathSearchCancel, target)
	}
	state.mu.Unlock()
}
