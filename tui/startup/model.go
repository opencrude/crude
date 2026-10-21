// Package startup owns the native, detached introduction and project chooser.
// It does not construct a workspace or own the startup service's connection.
package startup

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"path/filepath"
	"strings"
	"sync"
	"time"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"

	"crude/coreprotocol"
	"crude/shellport"
	"crude/tui/cell"
	"crude/tui/compositor"
	"crude/tui/filetree"
	"crude/tui/screens/filepicker"
	"crude/tui/surface"
	"crude/tui/theme"
	"crude/tui/ui"
	"crude/types"
)

const requestTimeout = 20 * time.Second
const retainedEntryLimit = 4096

// Selection retains recovery context across the detached startup boundary.
type Selection struct {
	Root   string
	Issues []types.OperationIssue
}

// Run returns a freshly inspected project root, or an empty root on Quit.
// Every accepted read is cancelled and joined before the caller attaches.
func Run(ctx context.Context, service shellport.Startup, initial coreprotocol.StartupSnapshot) (Selection, error) {
	m := NewModel(ctx, service, initial)
	_, err := tea.NewProgram(m, tea.WithContext(ctx), tea.WithWidthMethod(ansi.GraphemeWidth)).Run()
	return m.finish(), err
}

// commandOwner prevents queued Tea commands from starting after shutdown and
// joins only commands which actually started, not commands Tea never dispatched.
type commandOwner struct {
	mu     sync.Mutex
	closed bool
	wg     sync.WaitGroup
}

func (o *commandOwner) command(ctx context.Context, fn func(context.Context) tea.Msg) tea.Cmd {
	return func() tea.Msg {
		o.mu.Lock()
		if o.closed || ctx.Err() != nil {
			o.mu.Unlock()
			return nil
		}
		o.wg.Add(1)
		o.mu.Unlock()
		defer o.wg.Done()
		return fn(ctx)
	}
}

type browseResult struct {
	target *filepicker.Screen
	intent filepicker.IntentToken
	gen    uint64
	path   string
	offset int
	expand bool
	page   coreprotocol.StartupBrowseResult
	err    error
}
type searchResult struct {
	target  *filepicker.Screen
	intent  filepicker.IntentToken
	gen     uint64
	query   string
	matches []filetree.PathMatch
	notice  string
	err     error
}
type inspectResult struct {
	gen      uint64
	target   *filepicker.Screen
	intent   filepicker.IntentToken
	snapshot coreprotocol.StartupSnapshot
	err      error
}
type savedMsg struct{ err error }
type tickMsg struct{}

// Model is a standalone Tea model, exposed for native host and visual tests.
// Close must be called after a program using NewModel exits.
type Model struct {
	service       shellport.Startup
	ctx           context.Context
	cancel        context.CancelFunc
	saveParent    context.Context
	saveCmd       tea.Cmd
	owner         commandOwner
	requestCancel context.CancelFunc
	gen           uint64
	initial       coreprotocol.StartupSnapshot
	root          *screen
	host          *ui.Host
	comp          *compositor.Compositor
	surf          *surface.Surface
	picker        *filepicker.Screen
	result        string
	issues        []types.OperationIssue
	browseRoot    string
	closed        bool
	pagePath      string
	nextOffset    int
	pageExpand    bool
	retained      int
	pageEntries   []filepicker.Entry
}

func NewModel(ctx context.Context, service shellport.Startup, initial coreprotocol.StartupSnapshot) *Model {
	saveParent := ctx
	ctx, cancel := context.WithCancel(ctx)
	th := theme.FromPreset(initial.Theme.Preset)
	m := &Model{service: service, ctx: ctx, cancel: cancel, saveParent: saveParent, initial: initial, browseRoot: initial.Root, nextOffset: -1}
	m.picker = filepicker.New(th)
	m.root = newScreen(th, m.picker, initial)
	m.addIssues(initial.Issues...)
	m.host = ui.NewHost(th, m.root)
	m.picker.SetFocusHandler(func(id ui.ComponentID) { m.layout(); m.host.Focus(id) })
	m.comp = compositor.NewCompositor(80, 24)
	m.surf = surface.NewSurface("startup", 80, 24, cell.BlankCell(ui.NewTokenSet(th).Color(ui.ColorBg)))
	m.host.Resize(80, 24)
	m.layout()
	if initial.ShowIntroduction {
		m.host.Focus(goID)
	} else {
		m.host.Focus(openID)
	}
	return m
}

func (m *Model) Init() tea.Cmd { return tick() }
func tick() tea.Cmd {
	return tea.Tick(400*time.Millisecond, func(time.Time) tea.Msg { return tickMsg{} })
}
func (m *Model) Result() string { return m.result }

// Issues returns detached recovery context. Call Close first for late save results.
func (m *Model) Issues() []types.OperationIssue {
	return append([]types.OperationIssue(nil), m.issues...)
}

func (m *Model) finish() Selection {
	m.Close()
	return Selection{Root: m.Result(), Issues: m.Issues()}
}

func (m *Model) addIssues(issues ...types.OperationIssue) {
	for _, issue := range issues {
		duplicate := false
		for _, existing := range m.issues {
			if existing == issue {
				duplicate = true
				break
			}
		}
		if !duplicate {
			m.issues = append(m.issues, issue)
		}
	}
	m.root.setIssues(m.issues)
}

func (m *Model) saved(v savedMsg) {
	if v.err != nil {
		m.addIssues(types.OperationIssue{
			Title:   "Introduction choice was not saved",
			Summary: "The introduction can appear again when you start Crude.",
			Detail:  v.err.Error(), Guidance: "Check application settings before you start Crude again.",
			Target: types.RepairSettings, Path: "onboarding.introduction_completed",
		})
	}
}

// Close cancels and joins all started service calls. It is idempotent.
func (m *Model) Close() {
	m.owner.mu.Lock()
	m.owner.closed = true
	m.owner.mu.Unlock()
	m.cancel()
	m.owner.wg.Wait()
	// Completing the introduction accepts one bounded save attempt. Fast Quit
	// must not cancel it merely because no Tea worker started it yet.
	if m.saveCmd != nil {
		if v, ok := m.saveCmd().(savedMsg); ok {
			m.saved(v)
		}
	}
}

func (m *Model) layout() { m.host.MarkLayout(); m.host.Layout() }

func (m *Model) View() tea.View {
	m.layout()
	m.host.SetFrame(int(time.Now().UnixMilli()))
	m.comp.BeginFrame()
	m.comp.Root().AddChild(m.surf)
	m.host.Paint(m.surf.Buffer())
	m.comp.Composite()
	v := tea.NewView(m.comp.Present())
	v.AltScreen = true
	v.MouseMode = tea.MouseModeCellMotion
	return v
}

func (m *Model) stopRequest() {
	if m.requestCancel != nil {
		m.requestCancel()
		m.requestCancel = nil
	}
	m.gen++
}
func (m *Model) request() (context.Context, uint64) {
	m.stopRequest()
	ctx, cancel := context.WithTimeout(m.ctx, requestTimeout)
	m.requestCancel = cancel
	return ctx, m.gen
}
func (m *Model) pickerCurrent(target *filepicker.Screen, intent filepicker.IntentToken, gen uint64) bool {
	return !m.closed && m.root.mode == picking && target == m.picker && gen == m.gen && target.OpenEpoch() == intent.Epoch && target.IntentSequence() == intent.Sequence
}

func (m *Model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	if m.closed {
		return m, nil
	}
	switch v := msg.(type) {
	case tea.WindowSizeMsg:
		w, h := max(0, v.Width), max(0, v.Height)
		m.host.Resize(w, h)
		m.comp.Resize(w, h)
		m.surf.Resize(w, h)
		m.layout()
		return m, nil
	case tickMsg:
		return m, tick()
	case savedMsg:
		m.saved(v)
		m.layout()
		return m, nil
	case filepicker.NavigateMsg:
		if v.ValidFor(m.picker) {
			return m, m.browse(v.Path, 0, false, v.Intent())
		}
		return m, nil
	case filepicker.LoadMsg:
		if v.ValidFor(m.picker) {
			return m, m.browse(v.Path, 0, true, v.Intent())
		}
		return m, nil
	case filepicker.SearchMsg:
		if v.ValidFor(m.picker) {
			return m, m.search(v)
		}
		return m, nil
	case filepicker.SelectMsg:
		if v.ValidFor(m.picker) {
			return m, m.inspect(v.Path, m.picker, v.Intent())
		}
		return m, nil
	case filepicker.CancelMsg:
		if v.ValidFor(m.picker) {
			m.leavePicker()
		}
		return m, nil
	case browseResult:
		if !m.pickerCurrent(v.target, v.intent, v.gen) {
			return m, nil
		}
		m.requestCancel()
		m.requestCancel = nil
		if v.err != nil {
			m.picker.FailAsyncDirectoryLoadFor(v.intent, v.err)
			m.root.more.SetDisabled(m.nextOffset < 0)
			return m, nil
		}
		if v.page.NextOffset >= 0 && v.page.NextOffset <= v.offset {
			m.picker.FailAsyncDirectoryLoadFor(v.intent, errors.New("folder listing did not advance. Retry navigation"))
			return m, nil
		}
		entries := make([]filepicker.Entry, 0, min(len(v.page.Entries), retainedEntryLimit))
		for _, e := range v.page.Entries {
			if e.IsDir && len(entries) < retainedEntryLimit {
				entries = append(entries, filepicker.Entry{Name: e.Name, Path: e.Path, IsDir: true, IsSymlink: e.IsSymlink})
			}
		}
		previousPageSize := 0
		if v.offset > 0 {
			previousPageSize = len(m.pageEntries)
			if len(m.pageEntries)+len(entries) > retainedEntryLimit {
				m.picker.CancelAsyncDirectoryLoadFor(v.intent)
				m.nextOffset = -1
				m.root.pickerNotice = "Folder display limit reached. Enter a more specific path to continue."
				return m, nil
			}
			entries = append(append([]filepicker.Entry(nil), m.pageEntries...), entries...)
		}
		if !v.expand {
			m.retained = 0
			previousPageSize = 0
		}
		if m.retained-previousPageSize+len(entries) > retainedEntryLimit {
			m.picker.SetDirectory(v.path, entries)
			m.retained = 0
			previousPageSize = 0
			v.expand = false
			m.root.pickerNotice = "Expanded listing limit reached; browsing this folder directly."
		} else {
			m.picker.CompleteAsyncDirectoryLoadFor(v.intent, v.path, entries)
		}
		m.retained += len(entries) - previousPageSize
		m.pagePath = v.path
		m.pageEntries = entries
		m.nextOffset = v.page.NextOffset
		m.pageExpand = v.expand
		if v.page.Truncated && v.page.NextOffset < 0 {
			m.root.pickerNotice = "Folder scan limit reached. Enter a more specific path to continue."
		}
		m.root.path.SetValue(m.picker.Current())
		m.browseRoot = m.picker.Current()
		m.root.more.SetDisabled(m.nextOffset < 0)
		m.layout()
		return m, nil
	case searchResult:
		if !m.pickerCurrent(v.target, v.intent, v.gen) {
			return m, nil
		}
		m.requestCancel()
		m.requestCancel = nil
		if v.err != nil {
			m.picker.SetError(fmt.Errorf("search folders: %w", v.err))
		} else {
			m.picker.ApplyPathSearch(v.query, v.matches)
			m.root.pickerNotice = v.notice
		}
		m.layout()
		return m, nil
	case inspectResult:
		if v.gen != m.gen || (v.target != nil && !m.pickerCurrent(v.target, v.intent, v.gen)) {
			return m, nil
		}
		m.requestCancel()
		m.requestCancel = nil
		if v.err != nil {
			m.root.pickerNotice = "Could not inspect folder: " + v.err.Error()
			m.root.notice = m.root.pickerNotice
			return m, nil
		}
		m.addIssues(v.snapshot.Issues...)
		if v.snapshot.PathError != "" || v.snapshot.FolderKind != "" {
			reason := selectionReason(v.snapshot)
			m.root.notice = reason
			m.root.pickerNotice = reason
			if v.snapshot.PathError == "" && strings.TrimSpace(v.snapshot.Root) != "" {
				if v.target != nil {
					return m, m.browse(v.snapshot.Root, 0, false, v.intent)
				}
				m.browseRoot = v.snapshot.Root
				m.root.initial.Root = v.snapshot.Root
				m.root.initial.FolderKind = v.snapshot.FolderKind
				m.root.initial.PathError = ""
				cmd := m.openPicker()
				m.root.pickerNotice = reason
				return m, cmd
			}
			return m, nil
		}
		if strings.TrimSpace(v.snapshot.Root) == "" {
			m.root.notice = "Folder inspection returned no project path"
			m.root.pickerNotice = m.root.notice
			return m, nil
		}
		m.result = v.snapshot.Root
		return m, m.quit()
	case tea.KeyPressMsg:
		if v.String() == "ctrl+q" {
			return m, m.quit()
		}
		if m.root.mode == introduction && v.Code == tea.KeyEsc {
			return m, m.quit()
		}
		if m.root.mode == picking && v.String() == "ctrl+o" {
			return m, m.picker.ConfirmSelection().Cmd
		}
		if m.root.mode == picking && v.String() == "ctrl+l" {
			m.host.Focus(pathID)
			return m, nil
		}
		// The shared action bar owns its individual buttons and Tab/Shift+Tab
		// between them. Do not steal those keys at the page boundary.
		if v.Code == tea.KeyTab && (m.root.mode != picking || m.host.Focused() != filepicker.ActionsID) {
			m.cycleFocus(v.Mod.Contains(tea.ModShift))
			return m, nil
		}
		if m.root.mode == selection && v.Mod == 0 && (v.Code == tea.KeyUp || v.Code == tea.KeyDown) && m.moveChooserFocus(v.Code == tea.KeyUp) {
			return m, nil
		}
	}
	previousFocus := m.host.Focused()
	r := m.host.Handle(msg)
	if key, ok := msg.(tea.KeyPressMsg); ok && key.Code == tea.KeyTab && m.root.mode == picking && previousFocus == filepicker.ActionsID && m.host.Focused() != previousFocus {
		// The picker has finished traversing its own action buttons. Rejoin
		// this page's cycle so path controls and the issue reader stay reachable.
		m.host.Focus(previousFocus)
		m.cycleFocus(key.Mod.Contains(tea.ModShift))
	}
	if r.Action != nil {
		switch r.Action.ID {
		case "startup.go":
			cmd := m.openPicker()
			ctx, cancel := context.WithTimeout(m.saveParent, requestTimeout)
			var once sync.Once
			var saveErr error
			m.saveCmd = func() tea.Msg {
				once.Do(func() {
					defer cancel()
					_, saveErr = m.service.Save(ctx, []types.SettingEdit{{Path: []string{"onboarding", "introduction_completed"}, After: json.RawMessage("true")}})
				})
				return savedMsg{saveErr}
			}
			return m, tea.Batch(cmd, m.saveCmd)
		case "startup.open":
			return m, m.openPicker()
		case "startup.quit":
			return m, m.quit()
		case "startup.cancel":
			// Use the picker's own Esc ladder, including search clearing.
			m.picker.FocusFilter()
			return m, m.host.Handle(tea.KeyPressMsg{Code: tea.KeyEsc}).Cmd
		case "startup.path":
			path := strings.TrimSpace(m.root.path.Value())
			if path == "" {
				m.root.pickerNotice = "Enter a folder path"
				return m, nil
			}
			if !filepath.IsAbs(path) {
				path = filepath.Join(m.picker.Current(), path)
			}
			return m, m.browse(filepath.Clean(path), 0, false, filepicker.IntentToken{Epoch: m.picker.OpenEpoch(), Sequence: m.picker.IntentSequence()})
		case "startup.more":
			if m.nextOffset >= 0 {
				return m, m.browse(m.pagePath, m.nextOffset, m.pageExpand, filepicker.IntentToken{Epoch: m.picker.OpenEpoch(), Sequence: m.picker.IntentSequence()})
			}
		case "startup.workspace":
			if item, ok := r.Action.Payload.(ui.ListItem); ok {
				if path, ok := item.Payload.(string); ok {
					return m, m.inspect(path, nil, filepicker.IntentToken{})
				}
			}
		case "window.close":
			m.leavePicker()
			return m, nil
		}
	}
	m.layout()
	return m, r.Cmd
}

func (m *Model) quit() tea.Cmd { m.closed = true; m.stopRequest(); m.cancel(); return tea.Quit }

// moveChooserFocus lets arrows traverse the vertical page without requiring a
// mouse or a Tab just to reach the list. Inside the list, the shared control
// keeps owning movement until the user reaches its first/last selectable row.
func (m *Model) moveChooserFocus(back bool) bool {
	focused := m.host.Focused()
	if focused != openID && focused != quitID && focused != previousID {
		return false // read-only issue text keeps its own scroll keys
	}
	if focused == previousID {
		items := m.root.previous.Items()
		step := 1
		if back {
			step = -1
		}
		for i := m.root.previous.SelectedIndex() + step; i >= 0 && i < len(items); i += step {
			if items[i].Selectable() {
				return false
			}
		}
	}
	m.cycleFocus(back)
	if m.host.Focused() == previousID {
		key := tea.KeyHome
		if back {
			key = tea.KeyEnd
		}
		m.host.Handle(tea.KeyPressMsg{Code: key})
	}
	return true
}

func (m *Model) cycleFocus(back bool) {
	ids := []ui.ComponentID{goID}
	switch m.root.mode {
	case selection:
		ids = []ui.ComponentID{openID, previousID, quitID}
	case picking:
		ids = []ui.ComponentID{pathID, pathGoID, moreID, filepicker.FilterID, filepicker.ListID, filepicker.ActionsID}
	}
	if len(m.issues) > 0 {
		ids = append(ids, issuesID)
	}
	// An empty list and disabled More folders are not keyboard destinations.
	available := ids[:0]
	for _, id := range ids {
		component := m.host.Component(id)
		bounds, mounted := m.host.ScreenRect(id)
		if component == nil || !mounted || bounds.Empty() || component.Semantics().Disabled {
			continue
		}
		if id == previousID {
			if item, ok := m.root.previous.Selected(); !ok || !item.Selectable() {
				continue
			}
		}
		available = append(available, id)
	}
	ids = available
	if len(ids) == 0 {
		return
	}
	idx := -1
	for i, id := range ids {
		if id == m.host.Focused() {
			idx = i
			break
		}
	}
	if idx < 0 {
		idx = 0
		if back {
			idx = len(ids) - 1
		}
	} else if back {
		idx = (idx + len(ids) - 1) % len(ids)
	} else {
		idx = (idx + 1) % len(ids)
	}
	if bar, ok := m.host.Component(ids[idx]).(*ui.ActionBar); ok {
		if back {
			bar.FocusLast()
		} else {
			bar.FocusFirst()
		}
	}
	m.host.Focus(ids[idx])
}
func (m *Model) openPicker() tea.Cmd {
	m.root.mode = picking
	m.root.pickerNotice = ""
	m.retained = 0
	m.pageEntries = nil
	m.nextOffset = -1
	m.root.more.SetDisabled(true)
	root := m.browseRoot
	if root == "" {
		root = string(filepath.Separator)
	}
	m.picker.Open(root, filepicker.ModeFolder, nil)
	m.root.path.SetValue(root)
	m.layout()
	m.picker.FocusFilter()
	return m.browse(root, 0, false, filepicker.IntentToken{Epoch: m.picker.OpenEpoch(), Sequence: m.picker.IntentSequence()})
}
func (m *Model) leavePicker() {
	m.stopRequest()
	m.picker.Close()
	m.root.mode = selection
	m.root.pickerNotice = ""
	m.layout()
	m.host.Focus(openID)
}
func (m *Model) browse(path string, offset int, expand bool, intent filepicker.IntentToken) tea.Cmd {
	ctx, gen := m.request()
	var ok bool
	if expand {
		ok = m.picker.BeginAsyncDirectoryExpandFor(path, intent)
	} else {
		ok = m.picker.BeginAsyncDirectoryLoadFor(path, intent)
	}
	if !ok {
		return nil
	}
	m.root.more.SetDisabled(true)
	target := m.picker
	return m.owner.command(ctx, func(ctx context.Context) tea.Msg {
		page, err := m.service.Browse(ctx, coreprotocol.StartupBrowseRequest{Path: path, Offset: offset})
		return browseResult{target: target, intent: intent, gen: gen, path: path, offset: offset, expand: expand, page: page, err: err}
	})
}
func (m *Model) search(v filepicker.SearchMsg) tea.Cmd {
	m.stopRequest()
	if strings.TrimSpace(v.Query) == "" {
		m.picker.CancelAsyncDirectoryLoad()
		m.picker.ClearPathSearch()
		m.root.pickerNotice = ""
		m.root.more.SetDisabled(m.nextOffset < 0)
		return nil
	}
	m.picker.BeginPathSearch(v.Query)
	m.root.more.SetDisabled(true)
	root := m.picker.Current()
	hidden := m.picker.ShowHidden()
	ctx, gen := m.request()
	return m.owner.command(ctx, func(ctx context.Context) tea.Msg {
		cat, err := m.service.Catalog(ctx, root, hidden)
		var matches []filetree.PathMatch
		if err == nil {
			matches = filetree.RankPathEntries(cat.Entries, v.Query)
		}
		notice := ""
		if cat.Truncated {
			notice = "The search stopped at its work limit. Open a more specific folder to continue."
		}
		if cat.Errors > 0 {
			notice = fmt.Sprintf("Search skipped %d unreadable folders. %s", cat.Errors, notice)
		}
		return searchResult{target: v.Target, intent: v.Intent(), gen: gen, query: v.Query, matches: matches, notice: notice, err: err}
	})
}
func (m *Model) inspect(path string, target *filepicker.Screen, intent filepicker.IntentToken) tea.Cmd {
	ctx, gen := m.request()
	m.root.pickerNotice = "Checking project folder…"
	m.root.notice = "Checking project folder…"
	return m.owner.command(ctx, func(ctx context.Context) tea.Msg {
		snapshot, err := m.service.Inspect(ctx, path)
		return inspectResult{gen: gen, target: target, intent: intent, snapshot: snapshot, err: err}
	})
}
