# Architecture Transition Plan

Crude is transitioning from a single-agent orchestrator TUI into a full
IDE that hosts multiple agents (orchestrator + children, each with their
own overridable settings) and multiple edit buffers simultaneously. The
current `App` struct is a 175-method god object across 15 files with 9
parallel maps keyed by agent ID and a `Mode` enum that taxes every new
overlay with 10 touch points. This document is the concrete, grounded
plan to fix that.

## Guiding Principles

- **Excellence, no settling.** No TODOs, no "for now", no dead code, no
  "practical approach" shortcuts. Every line reads like train-of-thought.
- **Two settings surfaces, clearly separated:**
  1. **Orchestrator Settings** (overlay, stays inline) — configures one
     orchestrator and its children: provider, model, thinking level,
     policy, max steps. Nothing about the app, the editor, the theme,
     or the UI. Questions and async tasks stay as overlays alongside it.
  2. **God Settings** (floating panel, Zed/JetBrains style) — configures
     the whole app: providers, profiles, agents system, theme, editor,
     LSP, UI, font. All-powerful, searchable, lives as a floating window.
- **Zero test failures at every step.** Old bugs, new bugs — fix them
  as they surface. The build is always green.
- **Each phase independently shippable.** After every phase the app
  compiles, all tests pass, and the user can run it.

## Current State (the problems)

### The `App` god object

`tui/viewmodel/app.go` is 3159 lines. The `App` struct has 60+ fields.
Methods on `*App` total ~175 across these files:

```
app.go              56 methods   (3159 lines)
keyboard.go         34 methods   (1823 lines)
update.go           23 methods   (892 lines)
mouse.go            18 methods   (1130 lines)
view.go              8 methods   (787 lines)
agent_events.go     13 methods   (513 lines)
popupmouse.go       20 methods
launcher.go          4 methods
command_palette.go   2 methods
cancel.go            1 method
slash_commands.go    1 method
focus.go             5 methods
layout.go            1 method
```

The 7 most cross-cutting fields, each written from 4-6 different concern
groups, making data flow invisible:

| Field | Written by |
|-------|-----------|
| `m.dirty` | every concern group |
| `m.statusbar` | every concern group |
| `m.windowManager` | popup, LSP, composer, command palette, lifecycle |
| `m.editor` | editor mgmt, LSP, clipboard, file tree, lifecycle |
| `m.activeAgentView` | agent mgmt, lifecycle, LSP, focus |
| `m.activeEditorView` | editor mgmt, lifecycle, file tree, LSP |
| `m.treeModel` | file tree, lifecycle, mouse dispatch, LSP |

### The 9 parallel agent maps

All keyed by agent ID, manually kept in sync across `AddOrchestrator`,
`handleSubAgentStarted`, `cleanupAgent`, `completeSubAgent`:

```
agents                map[string]*AgentState
subAgentParents       map[string]string
agentLabels           map[string]string
agentModels           map[string]string
agentCategories       map[string]string
sessionInputChans     map[string]chan<- provider.UserInput
sessionBtwChans       map[string]chan<- provider.UserInput
sessionCancelChans    map[string]chan<- struct{}
sessionBlockResolveCh map[string]chan string
```

One missed map = stale reference = invisible bug.

### The Mode enum tax

```go
type Mode int
const (
    ModeChat Mode = iota
    ModeSettings
    ModeConfirm
    ModeQuestion
    ModeYolo
    ModeTextOverlay
    ModeFilePicker
)
```

Adding a new overlay mode requires editing 10+ locations:
`Mode` const, `setMode()`, `modeName()`, `syncOverlayMode()`,
`dispatchOverlayKey()`, new `handle*Key()`, `getOverlayContentLines()`,
`syncFormHint()`, `handleOverlayTabClose()`, `handleCancel()`.

### No buffer/view separation

`Editor` IS the buffer (`buf *PieceTable` is an owned pointer, not
shared). You cannot have two views of the same buffer, cannot have a
scratch buffer, cannot show a diff view. `EditorTab` is a 3-field
wrapper that adds nothing.

### Settings are global, per-agent settings are ad-hoc

`lastSettings ttypes.Settings` is a single global. Per-orchestrator
overrides exist in `session.RuntimeProfile` (ephemeral, session-side)
but the TUI only tracks them via `agentCategories map[string]string`
(agent to category name string). No per-buffer settings at all. The
current settings overlay mixes app-level config (theme, editor, LSP,
UI, font) with orchestrator-level config (provider, model, thinking)
in one form.

### The LSP diagnostics bug (symptom of the architecture problem)

The status bar shows diagnostics correctly because it PULLS from
`lspManager.ProjectInsights()` in `handleTick` (a polling model). The
file tree needs the PUSH path: LSP goroutine -> `program.Send` ->
`handleLSPDiagnosticsUpdate` -> `treeModel.SetDiagnostics` -> re-render.
This path was rewired to use `program.Send` but the bug persists — and
the reason it's untraceable is that there's no single observability
point for "did this event arrive?". That is what Phase 1 fixes.

---

## Phase 1: EventBus — unified external event ingress

**Goal:** Every external event (LSP diagnostics, file watcher, any
goroutine callback) enters the Bubble Tea loop through one object. The
bus logs every event in a ring buffer so "did this event arrive?" is
answerable by calling `RecentEvents()`.

**New file:** `tui/viewmodel/eventbus.go`

```go
package viewmodel

import (
	"fmt"
	"sync"
	"time"

	tea "charm.land/bubbletea/v2"

	"crude/tui/editor"
)

// ─── External event message types ─────────────────────────────────────────
//
// These messages originate from goroutines outside the Bubble Tea event loop
// (LSP server process, file watcher, background tasks). They are sent through
// the EventBus so all mutations happen on the main goroutine and the event
// flow is traceable.

// lspDiagnosticsMsg carries LSP diagnostics for a file. The LSP server's
// diagnostic callback (running on the server's reader goroutine) sends this
// through the EventBus. The handler in update.go applies the diagnostics to
// the file tree and active editor on the main goroutine.
type lspDiagnosticsMsg struct {
	filePath    string
	diagnostics []editor.LSPDiagnostic
}

// fileWatcherMsg carries a file system watcher event. The file tree's watcher
// goroutine sends this through the EventBus so the LSP notification runs as a
// tea.Cmd (not blocking the watcher) and the UI re-renders.
type fileWatcherMsg struct {
	filePath   string
	changeType int
}

// ─── EventBus ──────────────────────────────────────────────────────────────

const eventLogCapacity = 200

// eventLogEntry records a single event that passed through the EventBus.
type eventLogEntry struct {
	time    time.Time
	msgType string
	detail  string
}

// EventBus is the single ingress point for external events into the Bubble Tea
// event loop. Goroutine-driven callbacks (LSP diagnostics, file watcher) send
// messages through the bus instead of mutating App state directly.
//
// The bus:
//   1. Ensures all state mutations happen on the main goroutine (via program.Send)
//   2. Maintains a ring-buffer log of recent events for debugging
//   3. Drops events gracefully if the program is not yet initialized
type EventBus struct {
	program *tea.Program
	log     []eventLogEntry
	mu      sync.Mutex
}

// NewEventBus creates an EventBus with no program attached. Call SetProgram
// before the program starts so callbacks wired in Init() can use the bus.
func NewEventBus() *EventBus {
	return &EventBus{
		log: make([]eventLogEntry, 0, eventLogCapacity),
	}
}

// SetProgram connects the bus to the Bubble Tea program. Must be called
// before program.Run() so that Init()'s callbacks (which use the bus) work.
func (b *EventBus) SetProgram(p *tea.Program) {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.program = p
}

// Send delivers a message to the Bubble Tea event loop. If the program is not
// yet connected (SetProgram not called or program has quit), the event is
// logged but not delivered — this prevents silent data loss from callbacks
// that fire before Init() or after shutdown.
func (b *EventBus) Send(msg tea.Msg) {
	b.mu.Lock()
	prog := b.program
	b.appendLog(msg)
	b.mu.Unlock()

	if prog != nil {
		prog.Send(msg)
	}
}

// RecentEvents returns a summary of the last N events that passed through the
// bus, newest first. For debugging: when a signal doesn't reach its handler,
// call RecentEvents() to see whether the event was even sent.
func (b *EventBus) RecentEvents(n int) []string {
	b.mu.Lock()
	defer b.mu.Unlock()

	if n > len(b.log) {
		n = len(b.log)
	}
	out := make([]string, 0, n)
	start := len(b.log) - n
	for i := len(b.log) - 1; i >= start; i-- {
		e := b.log[i]
		out = append(out, fmt.Sprintf("[%s] %s %s",
			e.time.Format("15:04:05.000"), e.msgType, e.detail))
	}
	return out
}

// appendLog records an event in the ring buffer. Caller must hold b.mu.
func (b *EventBus) appendLog(msg tea.Msg) {
	entry := eventLogEntry{
		time:    time.Now(),
		msgType: fmt.Sprintf("%T", msg),
		detail:  msgDetail(msg),
	}
	if len(b.log) >= eventLogCapacity {
		copy(b.log, b.log[1:])
		b.log[len(b.log)-1] = entry
	} else {
		b.log = append(b.log, entry)
	}
}

// msgDetail extracts a human-readable detail string from common event types.
func msgDetail(msg tea.Msg) string {
	switch m := msg.(type) {
	case lspDiagnosticsMsg:
		return fmt.Sprintf("file=%s diags=%d", m.filePath, len(m.diagnostics))
	case fileWatcherMsg:
		return fmt.Sprintf("file=%s change=%d", m.filePath, m.changeType)
	default:
		return ""
	}
}
```

**Changes to `tui/viewmodel/app.go`:**

1. Replace the `program *tea.Program` field with `eventBus *EventBus`.
2. Initialize `eventBus: NewEventBus()` in the `NewApp` struct literal.
3. Change `SetProgram` to delegate to the bus:
   ```go
   func (m *App) SetProgram(p *tea.Program) {
       if m.eventBus != nil {
           m.eventBus.SetProgram(p)
       }
   }
   ```
4. In `Init()`, change both callbacks to use `m.eventBus.Send(...)`:
   ```go
   m.lspManager.SetOnDiagnostics(func(fp string, diags []editor.LSPDiagnostic) {
       if m.eventBus != nil {
           m.eventBus.Send(lspDiagnosticsMsg{filePath: fp, diagnostics: diags})
       }
   })
   ```
   ```go
   m.treeModel.SetOnFileChanged(func(filePath string, changeType int) {
       if m.eventBus != nil {
           m.eventBus.Send(fileWatcherMsg{filePath: filePath, changeType: changeType})
       }
   })
   ```

**Changes to `tui/viewmodel/update.go`:**

1. Delete the `lspDiagnosticsMsg` and `fileWatcherMsg` type definitions
   (they move to `eventbus.go`).
2. The handler methods (`handleLSPDiagnosticsUpdate`,
   `handleFileWatcherEvent`) and the `case` statements in
   `handleAppMessages` stay — only the type definitions move.

**Changes to `tui/tui.go`:** None. `a.SetProgram(program)` still works
because `SetProgram` now routes through the bus internally.

**Verification:**
```bash
go build ./...
go test ./tui/viewmodel/... ./tui/editor/... ./tui/filetree/... ./tui/widgets/...
go vet ./tui/viewmodel/...
```

**What this enables:** When the LSP diagnostics bug is investigated
later, call `m.eventBus.RecentEvents(20)` to see whether
`lspDiagnosticsMsg` events are arriving. If they are, the bug is in the
handler or the tree renderer. If they aren't, the bug is in the LSP
callback wiring. One place to look, not four.

---

## Phase 2: AgentManager — structural agent tree

**Goal:** Replace the 9 parallel maps keyed by agent ID with a single
structural tree. Parent-child relationships become pointers, not map
lookups. Adding a per-agent field is one struct field, not a new map
plus sync logic.

**New file:** `tui/viewmodel/agent_manager.go`

```go
package viewmodel

import (
	"crude/provider"
	"crude/tui/theme"
	"crude/tui/widgets"
)

// PaneType identifies what a sub-pane renders (an agent conversation or
// a thinking trace). Carried over from subpane.go unchanged.
type PaneType string

const (
	PaneThinking PaneType = "thinking"
	PaneAgent    PaneType = "agent"
)

// AgentNode is one node in the agent tree. An orchestrator is a root node
// with children (sub-agents). A sub-agent has a non-nil Parent pointing to
// its orchestrator. All per-agent state lives here — no parallel maps.
type AgentNode struct {
	ID       string
	Parent   *AgentNode           // nil for orchestrator
	Children []*AgentNode         // sub-agents spawned by this orchestrator
	State    *AgentState          // streaming, thinking, message list, etc.
	Label   string                // display name
	Model   string                // model name for this agent
	Category string               // orchestrator category ("default", "researcher", ...)

	// Session channels — the only bridge from the UI to the session goroutine.
	InputChan      chan<- provider.UserInput
	BtwChan        chan<- provider.UserInput
	CancelChan     chan<- struct{}
	BlockResolveCh chan string

	// Sub-pane display state (for inline rendering in the parent's message list).
	Pane *SubPane
}

// IsOrchestrator returns true for root nodes (Parent == nil).
func (n *AgentNode) IsOrchestrator() bool { return n.Parent == nil }

// IsActive returns true if the agent is streaming or thinking.
func (n *AgentNode) IsActive() bool {
	return n.State != nil && (n.State.Streaming || n.State.Thinking)
}

// IsCompleted returns true if the agent finished (success, error, or cancel).
func (n *AgentNode) IsCompleted() bool {
	return n.State != nil && n.State.Completed
}

// AgentManager owns the agent tree and the flat ID index. It is the single
// source of truth for "which agents exist" and "what is the active agent".
// All methods are safe for single-goroutine use (Bubble Tea main goroutine).
type AgentManager struct {
	root    *AgentNode          // the orchestrator (single root for now)
	byID    map[string]*AgentNode // flat index for O(1) lookup by ID
	active  string               // ID of the currently-viewed agent
}

// NewAgentManager creates an empty manager.
func NewAgentManager() *AgentManager {
	return &AgentManager{
		byID: make(map[string]*AgentNode),
	}
}

// AddOrchestrator creates the root node and registers it. Called from
// App.AddOrchestrator (which is called from tui.TUI.AddOrchestrator).
func (m *AgentManager) AddOrchestrator(
	id string,
	inputCh, btwCh chan<- provider.UserInput,
	cancelCh chan<- struct{},
	blockResolveCh chan string,
	msgList *widgets.MessageList,
	pane *SubPane,
	logger Logger,
) *AgentNode {
	node := &AgentNode{
		ID:             id,
		State:          NewAgentState(id, msgList, logger),
		InputChan:      inputCh,
		BtwChan:        btwCh,
		CancelChan:     cancelCh,
		BlockResolveCh: blockResolveCh,
		Pane:           pane,
	}
	m.root = node
	m.byID[id] = node
	m.active = id
	return node
}

// AddSubAgent creates a child node under the given parent. Called from
// handleSubAgentStarted when a "sub_agent_started" event arrives.
func (m *AgentManager) AddSubAgent(
	id, parentID, label, model, category string,
	msgList *widgets.MessageList,
	pane *SubPane,
	logger Logger,
) *AgentNode {
	parent := m.byID[parentID]
	if parent == nil {
		return nil
	}
	node := &AgentNode{
		ID:             id,
		Parent:         parent,
		State:          NewAgentState(id, msgList, logger),
		Label:          label,
		Model:          model,
		Category:       category,
		Pane:           pane,
	}
	parent.Children = append(parent.Children, node)
	m.byID[id] = node
	return node
}

// Get returns the node for the given ID, or nil if not found.
func (m *AgentManager) Get(id string) *AgentNode {
	return m.byID[id]
}

// Remove deletes a node and all its descendants from the tree and index.
// Called from cleanupAgent. If the removed node was the active agent, the
// active ID is cleared (the caller re-derives from the tab bar).
func (m *AgentManager) Remove(id string) {
	node := m.byID[id]
	if node == nil {
		return
	}
	// Remove all descendants first.
	for _, child := range node.Children {
		m.removeNode(child)
	}
	m.removeNode(node)
	if m.root == node {
		m.root = nil
	}
	if m.active == id {
		m.active = ""
	}
}

// removeNode deletes a single node (not its children) from the index and
// detaches it from its parent's children slice.
func (m *AgentManager) removeNode(node *AgentNode) {
	delete(m.byID, node.ID)
	if node.Parent != nil {
		for i, c := range node.Parent.Children {
			if c == node {
				node.Parent.Children = append(node.Parent.Children[:i],
					node.Parent.Children[i+1:]...)
				break
			}
		}
	}
}

// ActiveID returns the currently-viewed agent ID ("" if none).
func (m *AgentManager) ActiveID() string { return m.active }

// ActiveNode returns the currently-viewed agent node (nil if none).
func (m *AgentManager) ActiveNode() *AgentNode {
	if m.active == "" {
		return nil
	}
	return m.byID[m.active]
}

// SetActive sets the active agent by ID. No-op if the ID is unknown.
func (m *AgentManager) SetActive(id string) {
	if _, ok := m.byID[id]; ok {
		m.active = id
	}
}

// ClearActive unsets the active agent (used when switching to an editor tab).
func (m *AgentManager) ClearActive() { m.active = "" }

// AllNodes returns every node in the tree (root + all descendants).
func (m *AgentManager) AllNodes() []*AgentNode {
	if m.root == nil {
		return nil
	}
	var out []*AgentNode
	var walk func(n *AgentNode)
	walk = func(n *AgentNode) {
		out = append(out, n)
		for _, c := range n.Children {
			walk(c)
		}
	}
	walk(m.root)
	return out
}

// Orchestrator returns the root node (nil if no orchestrator exists).
func (m *AgentManager) Orchestrator() *AgentNode { return m.root }

// SubAgents returns the direct children of the orchestrator (nil if none).
func (m *AgentManager) SubAgents() []*AgentNode {
	if m.root == nil {
		return nil
	}
	return m.root.Children
}

// AnyActive returns true if any agent in the tree is streaming or thinking.
func (m *AgentManager) AnyActive() bool {
	for _, n := range m.AllNodes() {
		if n.IsActive() {
			return true
		}
	}
	return false
}

// TabOrder returns the agent IDs in the order they should appear as tabs:
// sub-agents first (by pane order), then the orchestrator.
func (m *AgentManager) TabOrder() []string {
	var out []string
	for _, child := range m.SubAgents() {
		if !child.IsCompleted() {
			out = append(out, child.ID)
		}
	}
	if m.root != nil {
		out = append(out, m.root.ID)
	}
	return out
}
```

**Changes to `tui/viewmodel/app.go`:**

1. Replace these 9 fields:
   ```go
   agents                map[string]*AgentState
   subAgentParents       map[string]string
   agentLabels           map[string]string
   agentModels            map[string]string
   agentCategories        map[string]string
   sessionInputChans     map[string]chan<- provider.UserInput
   sessionBtwChans       map[string]chan<- provider.UserInput
   sessionCancelChans    map[string]chan<- struct{}
   sessionBlockResolveCh map[string]chan string
   activeAgentView       string
   ```
   with one field:
   ```go
   agentMgr *AgentManager
   ```

2. Initialize `agentMgr: NewAgentManager()` in `NewApp`.

3. Rewrite `AddOrchestrator` to create the message list, pane, and call
   `m.agentMgr.AddOrchestrator(...)`.

4. Rewrite `cleanupAgent` to call `m.agentMgr.Remove(agentID)` and clean
   up the pane via `m.paneMgr.Remove(agentID)`.

5. Rewrite `handleSubAgentStarted` to call
   `m.agentMgr.AddSubAgent(id, parentID, label, model, category, ...)`
   instead of manually populating 5 maps.

6. Replace every `m.agents[id]` lookup with `m.agentMgr.Get(id)`.

7. Replace every `m.activeAgentView` read with `m.agentMgr.ActiveID()`
   or `m.agentMgr.ActiveNode()`.

8. Replace every `m.activeAgentView = x` write with `m.agentMgr.SetActive(x)`
   or `m.agentMgr.ClearActive()`.

9. Rewrite `syncAgentWindowTabs` to iterate `m.agentMgr.TabOrder()`
   instead of the parallel maps.

10. Rewrite `agentTabIcon`, `agentTabTitle`, `isAgentTabTitle`,
    `agentIDFromTabTitle` — these stay as string helpers but read from
    `m.agentMgr.Get(id)` for state.

**Changes to `tui/viewmodel/agent_events.go`:**

Every `m.agents[sessionID]` becomes `m.agentMgr.Get(sessionID)`.
Every `m.activeAgentView` becomes `m.agentMgr.ActiveID()`.
Every `m.sessionInputChans[id]` becomes
`m.agentMgr.Get(id).InputChan`.
Every `m.sessionCancelChans[id]` becomes
`m.agentMgr.Get(id).CancelChan`.
Same for btw, blockResolve.

`handleSubAgentStarted` stops setting 5 maps — it calls
`m.agentMgr.AddSubAgent(...)` and lets the manager build the tree.

`handleSubAgentCompleted` calls `m.agentMgr.Remove(agentID)` (or
marks the node completed — depends on whether completed sub-agents
should stay visible as collapsed panes; the current code marks
`agent.Completed = true` and collapses the pane, so keep that and
only remove on cleanup).

**Changes to `tui/viewmodel/view.go`:**

`buildMainView` uses `m.agentMgr.ActiveNode()` for the active agent
instead of `m.agentForView()`. The `activeMsgList()` function becomes
a thin wrapper around `m.agentMgr.ActiveNode().State.MsgList`.

**Changes to `tui/viewmodel/focus.go`:**

`focusTarget()` checks `m.agentMgr.Orchestrator() != nil` instead of
`len(m.agents) == 0`.

**Changes to `tui/viewmodel/cancel.go`:**

`handleCancel` uses `m.agentMgr.ActiveID()` and
`m.agentMgr.Get(id).CancelChan`.

**Migration of `PaneManager`:**

`PaneManager` in `subpane.go` stays as-is for now. The `SubPane`
struct is referenced by `AgentNode.Pane`. In a future phase, the
PaneManager could be absorbed into the AgentManager (the tree already
tracks panes via `AgentNode.Pane`), but for Phase 2 keep the
PaneManager as a separate display-state manager to minimize risk.

**Verification:**
```bash
go build ./...
go test ./tui/viewmodel/... ./tui/editor/... ./tui/filetree/...
```

All existing agent tests must pass. The behavior is identical — the
tree is just structural now.

---

## Phase 3: BufferManager — separate data from view

**Goal:** The `PieceTable` (text + file path) becomes a shareable
`Buffer`. The `Editor` becomes a view into a buffer (cursor, scroll,
selection, undo). This enables scratch buffers, diff views, split
views, and per-buffer settings.

**New file:** `tui/editor/buffer_manager.go`

```go
package editor

import (
	"os"
	"path/filepath"
	"sync"
)

// Buffer is the data model for a text file. It owns the PieceTable (the
// actual text), the file path, the dirty flag, cached diagnostics, and
// per-buffer settings overrides (zero value = inherit global).
//
// Multiple Editor views can share the same Buffer (e.g., a split view or
// a diff view). The Buffer is reference-counted via the BufferManager —
// it is only closed when the last view releases it.
type Buffer struct {
	ID       string // absolute path for file buffers, "scratch:<uuid>" for scratch
	Path     string // file path on disk (empty for scratch buffers)
	buf      *PieceTable
	dirty    bool
	diags    []LSPDiagnostic
	diagsMu   sync.RWMutex

	// Per-buffer settings overrides. Zero value means "inherit global".
	Settings EditorSettingsOverride
}

// EditorSettingsOverride holds per-buffer overrides. Each field is a
// pointer: nil means "inherit the global setting", non-nil means "use
// this value for this buffer".
type EditorSettingsOverride struct {
	TabSize       *int
	WordWrap      *bool
	LineNumbers   *bool
	FormatOnSave  *bool
}

// BufferManager owns all open buffers. It is the single source of truth
// for "which buffers exist" and "what is their text". Editors reference
// buffers by ID; when an editor tab opens, it asks the manager for the
// buffer (creating it if needed).
type BufferManager struct {
	mu      sync.RWMutex
	buffers map[string]*Buffer
}

// NewBufferManager creates an empty manager.
func NewBufferManager() *BufferManager {
	return &BufferManager{
		buffers: make(map[string]*Buffer),
	}
}

// OpenFile loads a file from disk into a Buffer and registers it. If the
// buffer already exists (file already open in another tab), it returns
// the existing buffer — no reload, no duplicate.
func (m *BufferManager) OpenFile(path string) (*Buffer, error) {
	abs, err := filepath.Abs(path)
	if err != nil {
		return nil, err
	}

	m.mu.RLock()
	if buf, ok := m.buffers[abs]; ok {
		m.mu.RUnlock()
		return buf, nil
	}
	m.mu.RUnlock()

	pt := NewPieceTable()
	if err := pt.Load(abs); err != nil {
		return nil, err
	}
	buf := &Buffer{
		ID:   abs,
		Path: abs,
		buf:  pt,
	}

	m.mu.Lock()
	defer m.mu.Unlock()
	m.buffers[abs] = buf
	return buf, nil
}

// CreateScratch creates a new unnamed buffer with no file path. Used for
// diff views, temporary notes, or "untitled" buffers.
func (m *BufferManager) CreateScratch() *Buffer {
	id := "scratch:" + nextScratchID()
	buf := &Buffer{
		ID:  id,
		buf: NewPieceTable(),
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	m.buffers[id] = buf
	return buf
}

// Get returns the buffer with the given ID, or nil if not found.
func (m *BufferManager) Get(id string) *Buffer {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return m.buffers[id]
}

// Close releases a buffer. If other editors are still viewing it, it
// stays in memory; the manager only removes it when explicitly closed.
func (m *BufferManager) Close(id string) {
	m.mu.Lock()
	defer m.mu.Unlock()
	delete(m.buffers, id)
}

// SetDiagnostics updates the cached diagnostics for a buffer. Called from
// the LSP diagnostics handler on the main goroutine. Thread-safe.
func (m *BufferManager) SetDiagnostics(path string, diags []LSPDiagnostic) {
	m.mu.RLock()
	buf, ok := m.buffers[path]
	m.mu.RUnlock()
	if !ok {
		return
	}
	buf.diagsMu.Lock()
	buf.diags = diags
	buf.diagsMu.Unlock()
}

// Diagnostics returns the cached diagnostics for a buffer.
func (b *Buffer) Diagnostics() []LSPDiagnostic {
	b.diagsMu.RLock()
	defer b.diagsMu.RUnlock()
	return b.diags
}

// Bytes returns the buffer's text content.
func (b *Buffer) Bytes() []byte { return b.buf.Bytes() }

// LineCount returns the number of lines.
func (b *Buffer) LineCount() int { return b.buf.LineCount() }

// Line returns the text of a specific line.
func (b *Buffer) Line(i int) string { return b.buf.Line(i) }

// Lines returns all lines.
func (b *Buffer) Lines() []string { return b.buf.Lines() }

// Insert inserts text at a byte offset.
func (b *Buffer) Insert(pos int, text string) {
	b.buf.Insert(pos, text)
	b.dirty = true
}

// Delete removes a byte range.
func (b *Buffer) Delete(start, end int) {
	b.buf.Delete(start, end)
	b.dirty = true
}

// Save writes the buffer to disk.
func (b *Buffer) Save() error {
	if b.Path == "" {
		return os.ErrInvalid // scratch buffers must be saved-as
	}
	if err := b.buf.Save(b.Path); err != nil {
		return err
	}
	b.dirty = false
	return nil
}

// IsDirty returns true if the buffer has unsaved changes.
func (b *Buffer) IsDirty() bool { return b.dirty }

// nextScratchID produces a unique ID for scratch buffers. Simple
// incrementing counter; good enough for a single session.
var scratchCounter int
func nextScratchID() string {
	scratchCounter++
	return strconv.Itoa(scratchCounter)
}
```

**Changes to `tui/editor/editor.go`:**

1. Replace `buf *PieceTable` and `filePath string` with `buffer *Buffer`.
   The editor delegates to the buffer for all text access:
   ```go
   func (e *Editor) Value() string      { return string(e.buffer.Bytes()) }
   func (e *Editor) LineCount() int     { return e.buffer.LineCount() }
   func (e *Editor) FilePath() string   { return e.buffer.Path }
   func (e *Editor) Title() string      { return filepath.Base(e.buffer.Path) }
   ```

2. `Open(path)` becomes: ask the `BufferManager` for the buffer. The
   editor no longer owns the PieceTable. `Close()` releases the buffer
   via the manager.

3. `SetDiagnostics(diags)` stores them on the buffer via
   `e.buffer.diags = diags` (or through the BufferManager for thread
   safety). The editor's `Diagnostics()` reads from the buffer.

4. The `diagnostics` and `diagByLine` fields on Editor move to Buffer,
   or the Editor keeps its computed `diagByLine` (the visual-column-
   converted version) since that's view-specific. The raw diagnostics
   live on the buffer; the computed visual spans live on the editor.

**Changes to `tui/viewmodel/editor_tab.go`:**

```go
type EditorTab struct {
	BufferID string   // ID in the BufferManager
	Editor   *Editor   // view into the buffer
	closingWithDirty bool
}
```

`Path` becomes `BufferID` (which is usually the path, but could be
`"scratch:N"` for scratch buffers).

**Changes to `tui/viewmodel/app.go`:**

1. Add `bufferMgr *editor.BufferManager` field, initialize in `NewApp`.
2. Replace `m.editorTabs map[string]*EditorTab` with a simple
   `map[string]*EditorTab` keyed by `BufferID` — or keep the map but
   key by buffer ID.
3. `openEditorTab(path)` calls `m.bufferMgr.OpenFile(path)` to get the
   buffer, then creates an `Editor` viewing that buffer. If the buffer
   already exists (file open in another tab), reuse it.
4. `closeEditorTabForce(path)` calls `m.bufferMgr.Close(bufferID)` and
   removes the tab.
5. `handleTabSwitch` looks up by buffer ID.
6. `handleLSPDiagnosticsUpdate` calls `m.bufferMgr.SetDiagnostics(path, diags)`
   to cache on the buffer, AND `m.editor.SetDiagnostics(diags)` if the
   active editor views that buffer.

**Verification:**
```bash
go build ./...
go test ./tui/editor/... ./tui/viewmodel/...
```

Existing editor tests must pass. The behavior is identical — buffers
are just structural now.

---

## Phase 4: God Settings — floating, all-mighty settings panel

**Goal:** A floating window that configures the entire app: providers,
profiles, agent system (multi-agent, categories), theme, editor, LSP,
UI, font. Zed/JetBrains style — searchable, stays open alongside the
editor, dismisses with Esc or click-outside.

This is the NEW settings surface. It does NOT replace the orchestrator
settings overlay (Phase 5 cleans that up). It runs alongside until
Phase 5 strips the app-level stuff from the overlay.

**New file:** `tui/widgets/godsettings.go`

The God Settings widget wraps the existing `settings.Model` (which
already has all the forms: root, profiles, category, theme, editor,
LSP, UI, font). The widget renders the settings model's `View()` into
a floating window via an `ANSIAdapter`.

```go
package widgets

import (
	tea "charm.land/bubbletea/v2"

	"crude/tui/rendering"
	"crude/tui/settings"
	"crude/tui/theme"
)

// GodSettings is the floating, all-mighty settings panel. It wraps the
// existing settings.Model (which has all the forms) and renders it
// inside a FloatingWindow via an ANSI adapter. The panel:
//   - Floats above the editor (z=10+)
//   - Can be moved and resized
//   - Closes on Esc or click-outside (KeepRoute — editor stays interactive)
//   - Saves via Ctrl+S or the form's Save button
type GodSettings struct {
	model  settings.Model
	theme  *theme.Theme
	width  int
	height int
}

// NewGodSettings creates the panel from an existing settings model.
func NewGodSettings(th *theme.Theme, m settings.Model) *GodSettings {
	return &GodSettings{model: m, theme: th}
}

// View returns the ANSI string for the panel content.
func (g *GodSettings) View(w, h int) string {
	g.width, g.height = w, h
	return g.model.View(0) // spinFrame=0, the panel doesn't animate
}

// Update forwards a message to the settings model.
func (g *GodSettings) Update(msg tea.Msg) (settings.Model, tea.Cmd) {
	return g.model.Update(msg)
}

// Render implements widget.Widget (for compositor integration).
func (g *GodSettings) Render(surf *surface.Surface, ctx *widget.RenderContext) {
	viewFn := func() string {
		w, h := surf.Width(), surf.Height()
		return g.View(w, h)
	}
	adapter := rendering.NewANSIAdapter(viewFn)
	adapter.Render(surf, ctx)
}
```

**Changes to `tui/viewmodel/app.go`:**

1. Add `openGodSettings()` method:
   ```go
   func (m *App) openGodSettings() {
       m.settings.SaveSnapshot()
       adapter := rendering.NewANSIAdapter(func() string {
           if fw := m.windowManager.Window("god-settings"); fw != nil {
               b := fw.ContentBounds()
               return m.settings.View(0) // render at content bounds
           }
           return m.settings.View(0)
       })
       w := 80
       h := m.height - 4
       if h < 20 { h = 20 }
       fw := wm.NewFloatingWindow("god-settings", "Settings", adapter,
           w, h,
           wm.WithMinSize(60, 20),
           wm.WithClickOutside(wm.ClickOutsideKeepRoute),
           wm.WithFocusable(true),
       )
       wm.CenterOnScreen(fw, m.width, m.height)
       m.windowManager.AddWindow(fw)
   }
   ```

2. Wire the keyboard shortcut: `Ctrl+,` (Zed's settings shortcut) or
   `Ctrl+Shift+,` — add to `dispatchKeyPress` global keys:
   ```go
   case keyStr == "ctrl+," || keyStr == "ctrl+shift+,":
       return m.openGodSettings()
   ```

**Changes to `tui/viewmodel/keyboard.go`:**

Add `"god-settings"` case to `dispatchFloatingWindowKey`:
```go
case "god-settings":
    updated, cmd, evt := m.settings.Update(msg)
    m.settings = updated
    if cmd != nil { cmds = append(cmds, cmd) }
    if evt != nil { m.handleSettingsEvent(evt) }
    if fw := m.windowManager.Window("god-settings"); fw != nil {
        fw.MarkContentDirty()
    }
    return m, tea.Batch(cmds...)
```

The `handleSettingsEvent` is extracted from the existing
`handleSettingsKey` — it processes `SaveEvent` / `CancelEvent` the
same way.

**Changes to `tui/viewmodel/popupmouse.go`:**

Add `"god-settings"` to `handlePopupContentClick` and
`handlePopupContentHover` — forward mouse events to the settings form.

**Do NOT remove the old overlay settings yet.** The old
`setMode(ModeSettings)` path stays functional during Phase 4. Phase 5
strips the app-level stuff from the overlay and converts the overlay
into the per-orchestrator settings.

**Verification:**
```bash
go build ./...
go test ./tui/viewmodel/... ./tui/widgets/...
```

The God Settings panel opens with `Ctrl+,`, floats, can be moved/
resized, saves settings. The old overlay settings still work via
`/settings` for now.

---

## Phase 5: Clean Orchestrator Settings — strip app stuff, keep per-orchestrator

**Goal:** The existing overlay settings (`ModeSettings` /
`m.settings` / the `settings.Model`) currently mixes app-level config
(theme, editor, LSP, UI, font) with orchestrator-level config (provider,
model, thinking, policy, max steps). This phase splits them:

- **God Settings** (Phase 4) owns: theme, editor, LSP, UI, font,
  providers, profiles, agent system (multi-agent toggle, category
  definitions).
- **Orchestrator Settings** (the overlay) owns: the CURRENT
  orchestrator's provider, model, thinking level, policy, max steps,
  and children configuration. Nothing about the app.

The overlay stays as an overlay (inline, not floating). It coexists
with questions and async tasks (which also stay as overlays).

**Approach:**

1. **Slim down `settings.Model`** to only the orchestrator-relevant
   forms. Remove the theme, editor, LSP, UI, font pages from the model
   (those are now in God Settings). The model keeps:
   - Provider selection
   - Model selection
   - API key
   - Thinking effort
   - Policy
   - Max steps
   - Children configuration (which categories are enabled, their
     models)

   OR: create a NEW `OrchestratorSettings` model that's a slimmed-down
   version, and keep the full `settings.Model` for God Settings. This
   is cleaner — the two settings surfaces have different data, they
   should be different models.

2. **New file:** `tui/widgets/orchestrator_settings.go`

   ```go
   // OrchestratorSettings is the per-orchestrator settings overlay. It
   // configures ONE orchestrator and its children: provider, model,
   // thinking level, policy, max steps, and child agent categories.
   // It does NOT configure the app (theme, editor, LSP, UI) — that's
   // the God Settings panel.
   type OrchestratorSettings struct {
       orchestratorID string
       // ... form fields for provider, model, thinking, policy, maxSteps
       // ... children config: which categories, their models
   }
   ```

3. **Replace `ModeSettings` overlay path** to use the new
   `OrchestratorSettings` model. `setMode(ModeSettings)` opens the
   orchestrator settings overlay for the active agent. `getOverlayContentLines("Settings")`
   renders `OrchestratorSettings.View()`.

4. **`/settings` slash command** opens the orchestrator settings (for
   the active agent). `Ctrl+,` opens God Settings (the app-level panel).

5. **Save flow:** The orchestrator settings save calls
   `OnOrchestratorSettingsSave(orchID, config)` which updates the
   `RuntimeProfile` override for that orchestrator. It does NOT write
   to `FileConfig` — orchestrator settings are ephemeral. God Settings
   save writes to `FileConfig` (persistent).

6. **Remove the theme/editor/LSP/UI/font pages** from the overlay
   model. These are only in God Settings now.

**Verification:**
```bash
go build ./...
go test ./tui/viewmodel/... ./tui/widgets/... ./tui/settings/...
```

The overlay settings show only per-orchestrator config. God Settings
shows everything. Both work.

---

## Phase 6: FocusStack — replace boolean focus with a stack

**Goal:** Replace `treeFocused bool`, `tabBarFocused bool`, and the
`m.windowManager.Focused() != nil` check in `focusTarget()` with a
single stack. Opening a floating window pushes focus; closing it pops
back to the previous. No priority chain, no mutually-exclusive booleans.

**New file:** `tui/viewmodel/focus_stack.go`

```go
package viewmodel

// FocusTarget identifies which UI element has keyboard focus.
type FocusTarget int

const (
	FocusNone FocusTarget = iota
	FocusTree
	FocusEditor
	FocusTabBar
	FocusInput
	FocusLauncher
)

// FocusStack manages keyboard focus as a stack. When a floating window
// opens, FocusFloatingWindow is pushed. When it closes, it's popped and
// focus returns to whatever was below. Clicking the tree pushes
// FocusTree; clicking the tab bar pushes FocusTabBar. The stack prevents
// the "which boolean wins?" problem that the old priority chain had.
type FocusStack struct {
	stack []FocusTarget
}

// Push sets the current focus, remembering the previous one.
func (s *FocusStack) Push(t FocusTarget) {
	s.stack = append(s.stack, t)
}

// Pop removes the top focus target, returning to the one below.
func (s *FocusStack) Pop() FocusTarget {
	if len(s.stack) == 0 {
		return FocusNone
	}
	top := s.stack[len(s.stack)-1]
	s.stack = s.stack[:len(s.stack)-1]
	return top
}

// Top returns the current focus target without removing it.
func (s *FocusStack) Top() FocusTarget {
	if len(s.stack) == 0 {
		return FocusNone
	}
	return s.stack[len(s.stack)-1]
}

// Remove removes a specific target from the stack (e.g., when a panel
// closes and its focus entry should be gone, not just popped).
func (s *FocusStack) Remove(t FocusTarget) {
	for i := len(s.stack) - 1; i >= 0; i-- {
		if s.stack[i] == t {
			s.stack = append(s.stack[:i], s.stack[i+1:]...)
			return
		}
	}
}

// Clear empties the stack.
func (s *FocusStack) Clear() {
	s.stack = s.stack[:0]
}

// HasFloating returns true if a floating window is on the stack.
// This replaces the old `m.windowManager.Focused() != nil` check.
func (s *FocusStack) HasFloating() bool {
	// Floating window focus is tracked by the WM, not this stack.
	// The WM's focused window takes priority over the stack.
	return false // WM checks this separately
}
```

**Changes to `tui/viewmodel/focus.go`:**

Rewrite `focusTarget()`:
```go
func (m *App) focusTarget() FocusTarget {
	// Floating windows always take priority — the WM tracks this.
	if m.windowManager != nil && m.windowManager.Focused() != nil {
		return FocusFloatingWindow
	}
	// No agents and no editor → launcher.
	if m.agentMgr.Orchestrator() == nil && m.activeEditorView == "" {
		return FocusLauncher
	}
	// The stack holds the non-floating focus.
	return m.focusStack.Top()
}
```

`FocusFloatingWindow` stays in the enum but is never pushed to the
stack — the WM tracks it separately and it always wins.

**Changes to `tui/viewmodel/mouse.go`:**

Replace `m.treeFocused = true` with `m.focusStack.Push(FocusTree)`.
Replace `m.tabBarFocused = true` with `m.focusStack.Push(FocusTabBar)`.
Replace `m.unfocus()` with `m.focusStack.Clear()` (or a targeted remove).

**Changes to `tui/viewmodel/keyboard.go`:**

`dispatchKeyPress` uses `m.focusTarget()` unchanged (the interface is
the same). The internal implementation is cleaner.

**Verification:**
```bash
go build ./...
go test ./tui/viewmodel/...
```

Focus behavior is identical from the user's perspective. The stack
just makes it structurally clean.

---

## Phase 7: Workspace extraction — App becomes thin coordinator

**Goal:** After Phases 1-6, the managers exist but `App` still holds
them and routes everything. Extract a `Workspace` struct that owns the
managers. `App` shrinks to ~300 lines: it implements `tea.Model`
(Update/View/Init) and delegates to the workspace.

**New file:** `tui/workspace/workspace.go`

```go
package workspace

import (
	"crude/tui/compositor"
	"crude/tui/editor"
	"crude/tui/filetree"
	"crude/tui/input"
	"crude/tui/theme"
	"crude/tui/widgets"
	"crude/tui/wm"
)

// Workspace owns all the managers that make up the IDE. It is the
// single composition root: every subsystem is a field here, and every
// field has a clear purpose. The App struct delegates to this.
type Workspace struct {
	EventBus      *EventBus
	AgentMgr      *AgentManager
	BufferMgr     *editor.BufferManager
	LSPMgr        *LSPManager
	Focus         *FocusStack
	WindowMgr     *wm.WindowManager
	Compositor    *compositor.Compositor
	Tree          *filetree.FileTreeModel
	TreeRenderer  *filetree.TreeRenderer
	Input         input.Model
	StatusBar     *widgets.StatusBar
	Settings      settings.Model
	GodSettings   *widgets.GodSettings
	Theme         *theme.Theme
	Width         int
	Height        int
	Dirty         bool
	ContentDirty  bool
}
```

**Changes to `tui/viewmodel/app.go`:**

`App` holds a `*workspace.Workspace` and implements `tea.Model`. All
`handle*Key`, `handle*Click`, `build*View` methods move to the
workspace or its sub-managers. `App.go` shrinks from 3159 to ~300
lines. `Update.go` shrinks from 892 to ~200 lines (delegates to
workspace). `View.go` shrinks from 787 to ~200 lines. `Keyboard.go`
shrinks from 1823 to ~500 lines.

This is the largest mechanical change but the lowest risk — the
managers already exist from Phases 1-6, this just moves the method
bodies.

**Verification:**
```bash
go build ./...
go test ./...
```

All tests pass. The app works identically. The code is now readable:
each manager is one file, one concern, independently testable.

---

## Phase Sequence Summary

| Phase | What | Est | Depends on |
|-------|------|-----|------------|
| 1 | EventBus | 30m | nothing |
| 2 | AgentManager | 2h | Phase 1 (uses EventBus for events) |
| 3 | BufferManager | 3h | Phase 1 |
| 4 | God Settings | 1.5h | Phase 1 |
| 5 | Clean Orchestrator Settings | 2h | Phase 4 (God Settings must exist before stripping overlay) |
| 6 | FocusStack | 1h | Phase 2 (uses agentMgr in focusTarget) |
| 7 | Workspace extraction | 2h | Phases 1-6 |

Total: ~12 hours of focused work.

## After the transition

When the LSP diagnostics bug is investigated after Phase 1, the
debugging path is:

1. Call `m.eventBus.RecentEvents(20)`.
2. If `lspDiagnosticsMsg` appears in the log, the event arrived — the
   bug is in `handleLSPDiagnosticsUpdate` (check
   `m.treeModel.SetDiagnostics` was called, check
   `m.treeRenderer.RenderToLines` reads the updated nodes) or in the
   render path (check `renderSidebarToSurface` was called, check
   `View()` re-rendered).
3. If `lspDiagnosticsMsg` does NOT appear, the bug is in the LSP
   callback wiring — check `m.lspManager.SetOnDiagnostics` was called
   in `Init()`, check `m.eventBus` is non-nil, check the LSP server is
   actually publishing (check `setFileDiagnostics` in
   `lsp_manager.go` — does `m.fileDiagnostics` have entries?).

One observability point replaces four-file archaeology.

## Per-orchestrator settings after the transition

```
Orchestrator Settings (overlay)
├── Provider selection
├── Model selection
├── API key
├── Thinking effort
├── Policy
├── Max steps
└── Children configuration
    ├── Category: "researcher" → model, provider
    ├── Category: "crafter" → model, provider
    └── Category: "aeron" → model, provider

God Settings (floating panel, Ctrl+,)
├── Providers (global API keys, URLs)
├── Profiles (named bundles of category configs)
├── Agent system
│   ├── Multi-agent toggle
│   ├── Max concurrent
│   └── Category definitions (names, descriptions, defaults)
├── Theme
├── Editor (tab size, word wrap, line numbers, format on save, syntax theme)
├── LSP (enabled, auto-detect, servers, diagnostics)
├── UI (sidebar width, show hidden files, icons, git status, animations)
└── Font (family, size, ligatures)
```

The orchestrator settings save to `RuntimeProfile` (ephemeral, per
session). The God settings save to `FileConfig` (persistent, on
disk). They are completely separate models, separate save flows,
separate data ownership.