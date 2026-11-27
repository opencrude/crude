package viewmodel

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"

	"crude/core/shellport"
	"crude/tui/screens/termdock"
	"crude/tui/screens/workspacehub"
	"crude/workspace"
	"crude/workspace/localhistory"
)

// hubFixtureWorkspace answers the core's Overview and Storage reads with
// fixed reports, so a capture shows the real pages without a core.
type hubFixtureWorkspace struct {
	shellport.Workspace
	usage   workspace.UsageReport
	storage workspace.StorageReport
}

func (w *hubFixtureWorkspace) WorkspaceUsage(context.Context) (workspace.UsageReport, error) {
	return w.usage, nil
}

func (w *hubFixtureWorkspace) WorkspaceStorage(context.Context) (workspace.StorageReport, error) {
	return w.storage, nil
}

func (w *hubFixtureWorkspace) CleanWorkspace(_ context.Context, kind string) (workspace.CleanupResult, error) {
	return workspace.CleanupResult{Kind: kind, Removed: 3, FreedBytes: 180 << 20}, nil
}

func hubFixtureUsage(now time.Time) workspace.UsageReport {
	days := make([]workspace.DayCost, workspace.UsageReportDays)
	total, requests := 0.0, 0
	for i := range days {
		day := now.AddDate(0, 0, i-len(days)+1)
		cost := float64((i*37)%23) * 1.7
		if i%6 == 5 {
			cost = 0
		}
		n := int(cost * 9)
		days[i] = workspace.DayCost{Day: day.Format("2006-01-02"), Requests: n, CostUSD: cost}
		total, requests = total+cost, requests+n
	}
	return workspace.UsageReport{
		Requests: requests, CostUSD: total, UnknownCost: 14, First: now.AddDate(0, 0, -29).UnixNano(), Last: now.UnixNano(),
		Models: []workspace.ModelCost{
			{ModelID: "claude-opus-5-5", ProviderID: "anthropic", Requests: requests * 6 / 10, InputTokens: 182_400_000, CachedTokens: 171_200_000, OutputTokens: 2_310_000, CostUSD: total * 0.78},
			{ModelID: "deepseek-v4.1-flash", ProviderID: "ollama", Requests: requests * 3 / 10, InputTokens: 41_900_000, CachedTokens: 30_100_000, OutputTokens: 820_000, CostUSD: total * 0.15},
			{ModelID: "gpt-5.2-mini", ProviderID: "openai", Requests: requests / 10, InputTokens: 9_300_000, OutputTokens: 410_000, CostUSD: total * 0.07},
		},
		Days:     days,
		Sessions: []workspace.SessionCost{{SessionID: "a", Requests: requests / 2, CostUSD: total / 2}, {SessionID: "b", Requests: requests / 4, CostUSD: total / 4}, {SessionID: "c", Requests: requests / 4, CostUSD: total / 4}},
	}
}

func hubFixtureStorage(now time.Time) workspace.StorageReport {
	categories := []workspace.StorageCategory{
		{ID: workspace.StorageSessions, Bytes: 4 << 30, Items: 221, Oldest: now.AddDate(0, -3, 0).UnixNano(), Newest: now.UnixNano()},
		{ID: workspace.StorageSubagents, Bytes: 860 << 20, Items: 412},
		{ID: workspace.StorageTemporary, Bytes: 410 << 20, Items: 64, Reclaimable: 380 << 20},
		{ID: workspace.StorageHistory, Bytes: 213 << 20, Oldest: now.AddDate(0, 0, -30).UnixNano(), Newest: now.UnixNano()},
		{ID: workspace.StorageWorktrees, Bytes: 120 << 20, Items: 2},
		{ID: workspace.StorageIndex, Bytes: 75 << 20},
		{ID: workspace.StorageOther, Bytes: 4 << 20},
	}
	var bytes int64
	for _, category := range categories {
		bytes += category.Bytes
	}
	return workspace.StorageReport{Bytes: bytes, Categories: categories}
}

// seedHubHistory records a baseline, an agent turn and a later outside edit
// in a real Local History store.
func seedHubHistory(t *testing.T, store *localhistory.Store, root, agentID string) {
	t.Helper()
	ctx := t.Context()
	write := func(path, text string) {
		full := filepath.Join(root, filepath.FromSlash(path))
		if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(full, []byte(text), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	mainBefore := "package main\n\nfunc main() {\n\tprintln(\"hello\")\n}\n"
	mainAfter := "package main\n\nimport \"fmt\"\n\nfunc main() {\n\tfmt.Println(greeting(\"Crude\"))\n}\n"
	greeting := "package main\n\nfunc greeting(name string) string {\n\treturn \"Hello, \" + name\n}\n"
	write("main.go", mainBefore)
	write("go.mod", "module journey\n\ngo 1.27\n")
	if _, err := store.Snapshot(ctx, localhistory.SnapshotOptions{Kind: "baseline"}); err != nil {
		t.Fatal(err)
	}
	// Like the product, a turn reuses the last snapshot when nothing changed.
	before, _, err := store.SnapshotIfChanged(ctx, localhistory.SnapshotOptions{Kind: "before_message"})
	if err != nil {
		t.Fatal(err)
	}
	if err := store.BeginMessageScope("visual-session", "visual-message", before.ID, time.Now()); err != nil {
		t.Fatal(err)
	}
	write("main.go", mainAfter)
	write("greeting.go", greeting)
	file := func(text string) *localhistory.OperationState {
		return &localhistory.OperationState{Type: localhistory.EntryFile, Mode: 0o644, Data: []byte(text)}
	}
	op := localhistory.AgentOperation{ID: "visual-edit", SessionID: "visual-session", MessageID: "visual-message", AgentID: agentID, Source: "edit_file",
		Paths: []localhistory.OperationPath{{Path: "main.go", Before: file(mainBefore), After: file(mainAfter)}, {Path: "greeting.go", After: file(greeting)}}}
	if err := store.RecordAgentOperation(ctx, op); err != nil {
		t.Fatal(err)
	}
	if err := store.CompleteAgentOperation(op.ID, true); err != nil {
		t.Fatal(err)
	}
	after, err := store.Snapshot(ctx, localhistory.SnapshotOptions{Kind: "after_message"})
	if err != nil {
		t.Fatal(err)
	}
	if err := store.CloseMessageScope("visual-session", "visual-message", after.ID, []string{"main.go", "greeting.go"}, nil, time.Now()); err != nil {
		t.Fatal(err)
	}
	write("go.mod", "module journey\n\ngo 1.27\n\nrequire example.com/tool v1.2.0\n")
	if _, err := store.Snapshot(ctx, localhistory.SnapshotOptions{Kind: "external_change"}); err != nil {
		t.Fatal(err)
	}
}

// TestWorkspaceHubVisualAcceptance drives the Workspace panel for native
// captures: F2 Overview, F3 Local History with the agent turn selected,
// F4 Storage, F5 the Remove temporary files confirmation, F6 Restore
// preview for undoing the agent turn, F7 docks the panel's window, F8 shows
// the next page, F9 exits.
func TestWorkspaceHubVisualAcceptance(t *testing.T) {
	if os.Getenv("CRUDE_VISUAL_WORKSPACE_HUB") != "1" {
		t.Skip("sequential native capture")
	}
	root := t.TempDir()
	app := newFullTestApp(t)
	ws := wsOf(app)
	ws.Logger = stubLogger{}
	// A decided folder: the captures show the panel, not the trust question.
	restrictSyntheticFixture(t, app, root)
	store, err := localhistory.Open(filepath.Join(t.TempDir(), "history"), root)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = store.Close() })
	ws.LocalHistory = store
	ws.HistoryService = workspace.NewLocalHistoryService(root, store, stubLogger{})
	app.AddOrchestrator("main")
	seedHubHistory(t, store, root, "main")
	now := time.Now()
	app.ws = &hubFixtureWorkspace{Workspace: app.ws, usage: hubFixtureUsage(now), storage: hubFixtureStorage(now)}
	model := &hubVisual{App: app}
	p := tea.NewProgram(model, tea.WithWidthMethod(ansi.GraphemeWidth))
	app.SetProgram(p)
	restore := installDetachedMessageSinkForTest(app, p.Send)
	defer restore()
	if _, err := p.Run(); err != nil {
		t.Fatal(err)
	}
}

type hubVisual struct{ *App }

func (*hubVisual) Init() tea.Cmd { return tickCmd() }

func (m *hubVisual) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	if key, ok := msg.(tea.KeyPressMsg); ok {
		switch key.Code {
		case tea.KeyF2:
			return m, m.openWorkspaceHub(workspacehub.TabOverview)
		case tea.KeyF3:
			// The agent turn is selected when the timeline arrives.
			cmd := m.openLocalHistory()
			m.historyPanel.SelectMessage("visual-session", "visual-message")
			return m, cmd
		case tea.KeyF4:
			return m, m.openWorkspaceHub(workspacehub.TabStorage)
		case tea.KeyF5:
			m.confirmWorkspaceCleanup(workspace.StorageTemporary)
			return m, nil
		case tea.KeyF6:
			if event, ok := m.historyPanel.SelectedEvent(); ok {
				return m, m.openRestorePreview(m.undoEventRequest(event))
			}
			return m, nil
		case tea.KeyF7:
			if tab := m.panelTab(termdock.PanelWorkspace); tab != nil && m.Win.IsFloating(tab.Group) {
				m.dockPaneWindow(tab.Group)
			}
			return m, nil
		case tea.KeyF8:
			next := workspacehub.Tabs[0]
			for i, tab := range workspacehub.Tabs {
				if tab == m.hub.Active() && i+1 < len(workspacehub.Tabs) {
					next = workspacehub.Tabs[i+1]
				}
			}
			return m, m.openWorkspaceHub(next)
		case tea.KeyF9:
			return m, tea.Quit
		}
	}
	_, cmd := m.App.Update(msg)
	return m, cmd
}
