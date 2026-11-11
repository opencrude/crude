package viewmodel

import (
	"context"
	"encoding/json"
	"errors"
	"maps"
	"slices"
	"sync"
	"time"

	tea "charm.land/bubbletea/v2"

	"crude/clientstate"
	"crude/tui/editor"
	"crude/tui/screens/activitybar"
)

// viewMemory is this client's presentation memory for one workspace. It is
// stored in the private client profile (clientstate), never in workspace or
// session storage, so each shell keeps its own choices while several shells
// share one workspace. Inventory and canonical state stay with the workspace.
type viewMemory struct {
	Version         int        `json:"version"`
	Layout          ToolLayout `json:"layout"`
	MinimizedAgents []string   `json:"minimized_agents,omitempty"`
	OpenedAgents    []string   `json:"opened_agents,omitempty"`
	SelectedAgent   string     `json:"selected_agent,omitempty"`
	FoldedAgents    []string   `json:"folded_agents,omitempty"`
	ShowHiddenFiles *bool      `json:"show_hidden_files,omitempty"`
	SidebarHidden   bool       `json:"sidebar_hidden,omitempty"`
	Activity        string     `json:"activity,omitempty"`
	// Panes is the editor work this client had open in each docked pane,
	// in visual left-to-right, top-to-bottom order. PaneLayout restores the
	// split tree that held them.
	Panes      []viewMemoryPane    `json:"panes,omitempty"`
	PaneLayout *viewMemoryPaneNode `json:"pane_layout,omitempty"`
	ActivePane int                 `json:"active_pane,omitempty"`
}

// viewMemoryPane is one docked pane's open documents and selected document.
type viewMemoryPane struct {
	Folds  map[string]*editor.FoldMemory `json:"folds,omitempty"`
	Files  []string                      `json:"files,omitempty"`
	Active string                        `json:"active,omitempty"`
}

// viewMemoryPaneNode is one node of a saved pane tree. A leaf names its pane
// by index; a split names its axis and the divider fraction.
type viewMemoryPaneNode struct {
	Leaf      int                 `json:"leaf,omitempty"`
	Split     bool                `json:"split,omitempty"`
	Direction string              `json:"direction,omitempty"`
	Fraction  float64             `json:"fraction,omitempty"`
	First     *viewMemoryPaneNode `json:"first,omitempty"`
	Second    *viewMemoryPaneNode `json:"second,omitempty"`
}

// ToolViewState is one sidebar destination's geometry and list position.
type ToolViewState struct {
	Width     int      `json:"width,omitempty"`
	Filter    string   `json:"filter,omitempty"`
	Selection string   `json:"selection,omitempty"`
	Offset    int      `json:"offset,omitempty"`
	Collapsed []string `json:"collapsed,omitempty"`
}

// ToolLayout is this client's rail order, sidebar views and panel heights.
type ToolLayout struct {
	Rail         []string                 `json:"rail"`
	Views        map[string]ToolViewState `json:"views,omitempty"`
	PanelHeights map[string]int           `json:"panel_heights,omitempty"`
}

func DefaultToolLayout() ToolLayout {
	return ToolLayout{Rail: []string{"files", "agents", "workspaces", "skills", "mcp"}, Views: make(map[string]ToolViewState), PanelHeights: make(map[string]int)}
}

// validToolLayout rejects a record no current shell could have written.
func validToolLayout(layout ToolLayout) bool {
	known := DefaultToolLayout().Rail
	seen := make(map[string]bool)
	for _, id := range layout.Rail {
		if !slices.Contains(known, id) || seen[id] {
			return false
		}
		seen[id] = true
	}
	for id, view := range layout.Views {
		if _, ok := activitybar.ViewFromID(id); !ok || view.Width < 0 || view.Width > 1000 || view.Offset < 0 || len(view.Filter)+len(view.Selection) > 4096 {
			return false
		}
	}
	for _, height := range layout.PanelHeights {
		if height < 0 || height > 1000 {
			return false
		}
	}
	return true
}

const viewMemoryFormat = 1
const viewMemoryIOTimeout = 5 * time.Second

// viewMemoryStore serializes this shell's writes for one workspace binding.
// A write older than the last stored version is dropped, so a slow debounced
// save can never land after the teardown flush.
type viewMemoryStore struct {
	mu      sync.Mutex
	client  *composerDraftClient
	written uint64
}

func newViewMemoryStore(client *composerDraftClient) *viewMemoryStore {
	if client == nil {
		return nil
	}
	return &viewMemoryStore{client: client}
}

// load reads this client's memory. Call off Update. A missing record is the
// default view; seeded records come from the latest other client.
func (s *viewMemoryStore) load() (viewMemory, error) {
	if s == nil {
		return viewMemory{}, nil
	}
	ctx, cancel := context.WithTimeout(context.Background(), viewMemoryIOTimeout)
	defer cancel()
	raw, _, err := s.client.store.LoadViewMemory(ctx, s.client.attachment, s.client.workspaceID)
	if errors.Is(err, clientstate.ErrNotFound) {
		return viewMemory{}, nil
	}
	if err != nil {
		return viewMemory{}, err
	}
	var memory viewMemory
	if err := json.Unmarshal(raw, &memory); err != nil {
		return viewMemory{}, err
	}
	if memory.Version != viewMemoryFormat {
		return viewMemory{}, nil
	}
	if !validToolLayout(memory.Layout) {
		return viewMemory{}, errors.New("the saved layout is not valid")
	}
	if !validViewPanes(memory) {
		// Keep the client's other choices; only the unusable pane record is
		// ignored.
		memory.Panes, memory.PaneLayout, memory.ActivePane = nil, nil, 0
	}
	return memory, nil
}

func (s *viewMemoryStore) save(version uint64, raw []byte) error {
	if s == nil {
		return nil
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if version <= s.written {
		return nil
	}
	ctx, cancel := context.WithTimeout(context.Background(), viewMemoryIOTimeout)
	defer cancel()
	if err := s.client.store.SaveViewMemory(ctx, s.client.attachment, s.client.workspaceID, raw); err != nil {
		return err
	}
	s.written = version
	return nil
}

// viewMemoryMarks are the cheap observations that decide whether a choice
// changed this update. Comparing them once per Update is the single save
// trigger for every choice below; no mutation site tracks versions itself.
type viewMemoryMarks struct {
	agents     uint64
	selected   string
	folds      uint64
	showHidden int8
	sidebar    bool
	activity   activitybar.View
	panes      uint64
}

func (m *App) viewMemoryMarks() viewMemoryMarks {
	marks := viewMemoryMarks{agents: m.AgentView.Revision(), selected: m.AgentView.ActiveID(), folds: m.agentFoldRevision, activity: m.activityView, panes: m.paneMemoryMark()}
	if m.treeHiddenChoice != nil {
		marks.showHidden = 1
		if *m.treeHiddenChoice {
			marks.showHidden = 2
		}
	}
	if m.Tree != nil {
		marks.sidebar = m.Tree.Visible
	}
	return marks
}

// installViewMemory applies remembered choices to a freshly built workspace
// view. It runs before inventory arrives, so agents adopt their choice the
// first time they appear instead of flashing open and closing again.
func (m *App) installViewMemory(memory viewMemory) {
	layout := memory.Layout
	if layout.Rail == nil {
		layout.Rail = DefaultToolLayout().Rail
	}
	if layout.Views == nil {
		layout.Views = make(map[string]ToolViewState)
	}
	if layout.PanelHeights == nil {
		layout.PanelHeights = make(map[string]int)
	}
	m.toolLayout = layout
	m.workspacePanelHeights = maps.Clone(layout.PanelHeights)
	if m.activityBar != nil {
		m.activityBar.SetOrder(layout.Rail)
	}
	m.AgentView.Remember(memory.MinimizedAgents, memory.OpenedAgents)
	m.rememberedAgent = memory.SelectedAgent
	m.agentFolded = make(map[string]bool, len(memory.FoldedAgents))
	for _, id := range memory.FoldedAgents {
		m.agentFolded[id] = true
	}
	m.treeHiddenChoice = memory.ShowHiddenFiles
	if m.Tree != nil {
		m.Tree.Visible = !memory.SidebarHidden
		if m.sidebarScreen != nil {
			// No search is active in a freshly built view, so no command.
			_ = m.sidebarScreen.SetShowHidden(m.treeShowHidden())
		} else if m.Tree.Model != nil {
			m.Tree.Model.SetShowHidden(m.treeShowHidden())
		}
	}
	view, ok := activitybar.ViewFromID(memory.Activity)
	if !ok {
		view = m.activityView
	}
	// Select the view first so setActivity does not capture the previous
	// view's geometry into the layout just installed.
	m.activityView = view
	m.setActivity(view, false)
	m.restoreViewPanes(memory)
	m.viewMemoryVersion, m.viewMemorySavedVersion = 0, 0
	m.viewMemorySaveQueued, m.viewMemorySaving = false, false
	m.viewMemoryMarked = m.viewMemoryMarks()
}

// loadViewMemoryNow binds and installs memory before the first frame. Call
// off Update. A failed read shows the default view and is reported once the
// program runs; it never blocks startup.
func (m *App) loadViewMemoryNow() {
	m.viewMemory = newViewMemoryStore(m.composerDraftClient)
	memory, err := m.viewMemory.load()
	m.viewMemoryLoadErr = err
	m.installViewMemory(memory)
}

// viewMemoryArrival is the destination workspace's memory, read off Update
// while the attachment was prepared.
type viewMemoryArrival struct {
	store  *viewMemoryStore
	memory viewMemory
	err    error
}

func (m *App) reportViewMemoryLoadError() {
	if err := m.viewMemoryLoadErr; err != nil && m.Feedback != nil {
		m.viewMemoryLoadErr = nil
		m.Feedback.NotifyError("Your layout and view choices could not be loaded. Crude shows the default view: " + err.Error())
	}
}

// treeShowHidden is this client's explicit choice, else the setting.
func (m *App) treeShowHidden() bool {
	if m.treeHiddenChoice != nil {
		return *m.treeHiddenChoice
	}
	return m.treeHiddenSetting
}

func (m *App) captureViewMemory() viewMemory {
	m.captureToolView()
	layout := m.toolLayout
	if layout.Rail == nil {
		layout.Rail = DefaultToolLayout().Rail
	}
	layout.PanelHeights, layout.Views = maps.Clone(m.workspacePanelHeights), maps.Clone(layout.Views)
	layout.Rail = slices.Clone(layout.Rail)
	memory := viewMemory{Version: viewMemoryFormat, Layout: layout, Activity: m.activityView.ID()}
	memory.MinimizedAgents, memory.OpenedAgents = m.AgentView.Choices()
	memory.SelectedAgent = m.AgentView.ActiveID()
	if memory.SelectedAgent == "" {
		memory.SelectedAgent = m.rememberedAgent
	}
	for id := range m.agentFolded {
		memory.FoldedAgents = append(memory.FoldedAgents, id)
	}
	slices.Sort(memory.FoldedAgents)
	if m.treeHiddenChoice != nil {
		show := *m.treeHiddenChoice
		memory.ShowHiddenFiles = &show
	}
	memory.SidebarHidden = m.Tree != nil && !m.Tree.Visible
	memory.Panes, memory.PaneLayout, memory.ActivePane, _ = m.captureViewPanes()
	m.captureEditorFolds(memory.Panes)
	return memory
}

type viewMemorySaveMsg struct{ generation uint64 }
type viewMemorySavedMsg struct {
	generation, version uint64
	err                 error
}

// noteViewMemoryChange is the single save trigger, run at the end of Update.
func (m *App) noteViewMemoryChange() tea.Cmd {
	// The tree owns its toggle (Ctrl+., its menu); a value that differs from
	// the setting is this client's choice.
	if m.Tree != nil && m.Tree.Model != nil {
		if show := m.Tree.Model.ShowHidden(); show != m.treeShowHidden() {
			m.treeHiddenChoice = &show
		}
	}
	if marks := m.viewMemoryMarks(); marks != m.viewMemoryMarked {
		m.viewMemoryMarked = marks
		m.viewMemoryVersion++
	}
	// A shell without a client profile has nowhere to store choices.
	if m.viewMemory != nil && m.viewMemoryVersion > m.viewMemorySavedVersion && !m.viewMemorySaving {
		return m.queueViewMemorySave()
	}
	return nil
}

func (m *App) queueViewMemorySave() tea.Cmd {
	if m.viewMemorySaveQueued {
		return nil
	}
	m.viewMemorySaveQueued = true
	generation := m.workspaceSwitchGen.Load()
	return tea.Tick(400*time.Millisecond, func(time.Time) tea.Msg { return viewMemorySaveMsg{generation: generation} })
}

func (m *App) saveViewMemoryCmd() tea.Cmd {
	write := m.viewMemoryWrite()
	generation, version := m.workspaceSwitchGen.Load(), m.viewMemoryVersion
	m.viewMemorySaving = true
	return func() tea.Msg { return viewMemorySavedMsg{generation, version, write()} }
}

// viewMemoryWrite captures the snapshot on Update; the returned write runs
// off Update and is safe to call after the App moved to another workspace.
func (m *App) viewMemoryWrite() func() error {
	store, version := m.viewMemory, m.viewMemoryVersion
	if store == nil || version <= m.viewMemorySavedVersion {
		return func() error { return nil }
	}
	raw, err := json.Marshal(m.captureViewMemory())
	return func() error {
		if err != nil {
			return err
		}
		return store.save(version, raw)
	}
}

// flushViewMemory is the teardown barrier: it stores the latest choices
// before the profile is released.
func (m *App) flushViewMemory() {
	if err := m.viewMemoryWrite()(); err == nil {
		m.viewMemorySavedVersion = m.viewMemoryVersion
	}
}
