package viewmodel

import (
	"context"
	"database/sql"
	"errors"
	"path/filepath"
	"testing"

	tea "charm.land/bubbletea/v2"
	_ "modernc.org/sqlite"

	"crude/memory"
	"crude/tui/cell"
	"crude/tui/screens/memorybrowser"
	"crude/tui/screens/termdock"
	"crude/tui/surface"
	"crude/tui/ui"
	"crude/workspace"
)

func memoryBrowserTestStore(t *testing.T) *workspace.WorkspaceDB {
	t.Helper()
	database, err := sql.Open("sqlite", filepath.Join(t.TempDir(), "memory.db"))
	if err != nil {
		t.Fatal(err)
	}
	database.SetMaxOpenConns(1)
	store, err := memory.Attach(database)
	if err != nil {
		_ = database.Close()
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = database.Close() })
	return &workspace.WorkspaceDB{DB: database, Memory: store}
}

func memoryBrowserTestRecord(t *testing.T, database *workspace.WorkspaceDB, title string) memory.Record {
	t.Helper()
	record, err := database.Memory.Remember(context.Background(), memory.Record{
		Title: title, Kind: memory.KindFact, Content: "Supported workspace fact", AppliesWhen: "In the memory browser",
		Evidence: []memory.Evidence{{SessionID: "source", MessageID: "user-1", Kind: memory.EvidenceUser, Outcome: memory.OutcomeUnmeasured, Summary: "Explicit workspace direction"}},
	})
	if err != nil {
		t.Fatal(err)
	}
	return record
}

func memoryBrowserCommandMessage[Message any](t *testing.T, command tea.Cmd) Message {
	t.Helper()
	if command == nil {
		t.Fatal("expected command")
	}
	value := command()
	message, ok := value.(Message)
	if !ok {
		t.Fatalf("command message = %T", value)
	}
	return message
}

func newMemoryBrowserTestApp(t *testing.T) (*App, memorybrowser.SearchMsg) {
	t.Helper()
	app := newFullTestApp(t)
	app.WorkspaceDB = memoryBrowserTestStore(t)
	t.Cleanup(app.resetMemoryBrowser)
	_, command := app.toggleMemoryBrowser()
	return app, memoryBrowserCommandMessage[memorybrowser.SearchMsg](t, command)
}

func TestMemoryBrowserRetainedWorkspaceDockAndUnavailableRecovery(t *testing.T) {
	app, request := newMemoryBrowserTestApp(t)
	record := memoryBrowserTestRecord(t, app.WorkspaceDB, "Retained record")
	result := memoryBrowserCommandMessage[memoryBrowserSearchResultMsg](t, app.handleMemoryBrowserSearch(request))
	app.applyMemoryBrowserSearch(result)
	browser := app.memoryBrowser
	if app.termDock.Panel(termdock.PanelMemory) != browser || app.termDock.ActivePanel() != termdock.PanelMemory {
		t.Fatal("memory browser is not the retained workspace panel")
	}
	if app.overlayHost.IsOpen(ui.OverlayID("memory-browser")) {
		t.Fatal("memory still has a second overlay owner")
	}
	app.setTermDockFocused(false)
	if _, command := app.toggleMemoryBrowser(); command != nil || !app.memoryBrowserVisible() || !app.termDockFocused {
		t.Fatal("activating visible unfocused memory should focus, not close/reload")
	}
	app.closeMemoryBrowser()
	_, command := app.toggleMemoryBrowser()
	reopened := memoryBrowserCommandMessage[memorybrowser.SearchMsg](t, command)
	if app.memoryBrowser != browser || reopened.Epoch == request.Epoch {
		t.Fatal("hide/reopen replaced the retained browser")
	}
	selected, ok := app.memoryBrowser.Selected()
	if !ok || selected.ID != record.ID {
		t.Fatal("hide/reopen lost selection")
	}
	app.WorkspaceDB = nil
	result = memoryBrowserCommandMessage[memoryBrowserSearchResultMsg](t, app.handleMemoryBrowserSearch(reopened))
	app.applyMemoryBrowserSearch(result)
	if !app.memoryBrowserVisible() || !errors.Is(app.memoryBrowser.SearchError(), workspace.ErrMemoryUnavailable) {
		t.Fatal("unavailable memory should remain a browsable recovery panel")
	}
}

func TestMemoryBrowserResultsRequireOwnerSequenceEpochAndWorkspaceGeneration(t *testing.T) {
	app, first := newMemoryBrowserTestApp(t)
	record := memoryBrowserTestRecord(t, app.WorkspaceDB, "Current")
	firstCommand := app.handleMemoryBrowserSearch(first)
	latest := memoryBrowserCommandMessage[memorybrowser.SearchMsg](t, app.memoryBrowser.Refresh())
	latestCommand := app.handleMemoryBrowserSearch(latest)
	staleResult := memoryBrowserCommandMessage[memoryBrowserSearchResultMsg](t, firstCommand)
	if !errors.Is(staleResult.Err, context.Canceled) {
		t.Fatalf("old read not cancelled: %v", staleResult.Err)
	}
	app.applyMemoryBrowserSearch(staleResult)
	if _, ok := app.memoryBrowser.Selected(); ok {
		t.Fatal("stale sequence published rows")
	}
	current := memoryBrowserCommandMessage[memoryBrowserSearchResultMsg](t, latestCommand)
	wrong := current
	wrong.Owner = &workspace.Workspace{}
	app.applyMemoryBrowserSearch(wrong)
	if _, ok := app.memoryBrowser.Selected(); ok {
		t.Fatal("wrong workspace owner published rows")
	}
	wrong = current
	wrong.WorkspaceGeneration++
	app.applyMemoryBrowserSearch(wrong)
	if _, ok := app.memoryBrowser.Selected(); ok {
		t.Fatal("stale workspace generation published rows")
	}
	wrong = current
	wrong.Request.Target = memorybrowser.New(app.Theme)
	app.applyMemoryBrowserSearch(wrong)
	if _, ok := app.memoryBrowser.Selected(); ok {
		t.Fatal("wrong screen owner published rows")
	}
	app.applyMemoryBrowserSearch(current)
	selected, ok := app.memoryBrowser.Selected()
	if !ok || selected.ID != record.ID {
		t.Fatal("current owned result rejected")
	}
	app.closeMemoryBrowser()
	_, command := app.toggleMemoryBrowser()
	_ = memoryBrowserCommandMessage[memorybrowser.SearchMsg](t, command)
	current.Records = []memory.ScoredRecord{{Record: memory.Record{ID: "stale"}}}
	app.applyMemoryBrowserSearch(current)
	selected, _ = app.memoryBrowser.Selected()
	if selected.ID != record.ID {
		t.Fatal("close/reopen ABA published old rows")
	}
}

func TestMemoryBrowserCommandsCaptureOldWorkspaceDBAndCancelAtBoundary(t *testing.T) {
	app, request := newMemoryBrowserTestApp(t)
	oldDatabase := app.WorkspaceDB
	oldRecord := memoryBrowserTestRecord(t, oldDatabase, "Old project")
	command := app.handleMemoryBrowserSearch(request)
	newDatabase := memoryBrowserTestStore(t)
	memoryBrowserTestRecord(t, newDatabase, "New project")
	app.WorkspaceDB = newDatabase
	result := memoryBrowserCommandMessage[memoryBrowserSearchResultMsg](t, command)
	if len(result.Records) != 1 || result.Records[0].ID != oldRecord.ID {
		t.Fatal("queued read followed mutable Workspace into another database")
	}
	request = memoryBrowserCommandMessage[memorybrowser.SearchMsg](t, app.memoryBrowser.Refresh())
	command = app.handleMemoryBrowserSearch(request)
	app.workspaceSwitchGen.Add(1)
	app.resetMemoryBrowser()
	result = memoryBrowserCommandMessage[memoryBrowserSearchResultMsg](t, command)
	if !errors.Is(result.Err, context.Canceled) {
		t.Fatalf("workspace boundary did not cancel old read: %v", result.Err)
	}
	app.applyMemoryBrowserSearch(result)
	if app.memoryBrowser != nil {
		t.Fatal("late result recreated old workspace browser")
	}
}

func TestMemoryBrowserCorrectionCommandUsesCASAndPreservesConflictedDraft(t *testing.T) {
	app, request := newMemoryBrowserTestApp(t)
	record := memoryBrowserTestRecord(t, app.WorkspaceDB, "Conflict record")
	app.applyMemoryBrowserSearch(memoryBrowserCommandMessage[memoryBrowserSearchResultMsg](t, app.handleMemoryBrowserSearch(request)))
	intent := app.memoryBrowser.HandleAction(ui.EventContext{}, ui.Action{ID: "memorybrowser.inspect"})
	detailRequest := memoryBrowserCommandMessage[memorybrowser.DetailMsg](t, intent.Cmd)
	detailResult := memoryBrowserCommandMessage[memoryBrowserDetailResultMsg](t, app.handleMemoryBrowserDetail(detailRequest))
	app.applyMemoryBrowserDetail(detailResult)
	app.memoryBrowser.HandleAction(ui.EventContext{}, ui.Action{ID: "memorybrowser.correct"})
	newer := record
	newer.Content = "Concurrent verified correction"
	if _, err := app.WorkspaceDB.Memory.Update(context.Background(), record.ID, newer, memory.ActorAuto, "other"); err != nil {
		t.Fatal(err)
	}
	mutation := memoryBrowserCommandMessage[memorybrowser.MutationMsg](t, app.memoryBrowser.HandleAction(ui.EventContext{}, ui.Action{ID: "memorybrowser.save"}).Cmd)
	command := app.handleMemoryBrowserMutation(mutation)
	before, err := app.WorkspaceDB.Memory.Get(context.Background(), record.ID)
	if err != nil || before.Revision != 2 {
		t.Fatalf("handler did I/O or lost revision: %#v %v", before, err)
	}
	result := memoryBrowserCommandMessage[memoryBrowserMutationResultMsg](t, command)
	if !errors.Is(result.Err, memory.ErrRevisionConflict) {
		t.Fatalf("missing expected_revision CAS: %v", result.Err)
	}
	wrong := result
	wrong.WorkspaceGeneration++
	app.applyMemoryBrowserMutation(wrong)
	if app.memoryBrowser.MutationError() != nil {
		t.Fatal("stale generation settled current mutation")
	}
	app.applyMemoryBrowserMutation(result)
	draft, ok := app.memoryBrowser.Draft()
	if !ok || draft.Revision != record.Revision || draft.Content != record.Content || len(draft.Evidence) != 1 || draft.Evidence[0] != record.Evidence[0] {
		t.Fatalf("CAS discarded the user's correction: %#v", draft)
	}
	if !errors.Is(app.memoryBrowser.MutationError(), memory.ErrRevisionConflict) {
		t.Fatal("conflict not surfaced in retained form")
	}
	actual, err := app.WorkspaceDB.Memory.Get(context.Background(), record.ID)
	if err != nil || actual.Content != newer.Content || actual.Revision != 2 {
		t.Fatal("stale correction changed durable state")
	}
}

func TestMemoryBrowserNormalNarrowRootHostMountAndFocus(t *testing.T) {
	for _, size := range []ui.Size{{W: 80, H: 24}, {W: 120, H: 40}, {W: 40, H: 24}} {
		app, request := newMemoryBrowserTestApp(t)
		memoryBrowserTestRecord(t, app.WorkspaceDB, "Mounted record")
		app.applyMemoryBrowserSearch(memoryBrowserCommandMessage[memoryBrowserSearchResultMsg](t, app.handleMemoryBrowserSearch(request)))
		app.width, app.height = size.W, size.H
		app.rootHost.Resize(size.W, size.H)
		app.mainSurf = surface.NewSurface("memory-shell", size.W, size.H, cell.TransparentBlank())
		app.configureShell()
		app.rootHost.MarkLayout()
		app.rootHost.Layout()
		if app.rootHost.Component(memorybrowser.SearchID) == nil {
			t.Fatalf("memory children not mounted at %dx%d", size.W, size.H)
		}
		app.rootHost.Focus(memorybrowser.SearchID)
		if !app.memoryBrowserOwnsFocus(app.rootHost.Focused()) {
			t.Fatal("memory child focus not owned by dock")
		}
		app.syncRootFocus()
		if app.rootHost.Focused() != memorybrowser.SearchID {
			t.Fatal("shell focus synchronization steals memory child focus")
		}
		app.rootHost.Paint(app.mainSurf.Buffer())
		app.resetMemoryBrowser()
	}
}

func TestMemoryBrowserTypedDispatcherExecutesOwnedCommands(t *testing.T) {
	app, request := newMemoryBrowserTestApp(t)
	record := memoryBrowserTestRecord(t, app.WorkspaceDB, "Dispatched record")
	_, command := app.Update(request)
	result := memoryBrowserCommandMessage[memoryBrowserSearchResultMsg](t, command)
	app.Update(result)
	selected, ok := app.memoryBrowser.Selected()
	if !ok || selected.ID != record.ID {
		t.Fatal("dispatcher did not apply memory search completion")
	}
	intent := app.memoryBrowser.HandleAction(ui.EventContext{}, ui.Action{ID: "memorybrowser.inspect"})
	detailRequest := memoryBrowserCommandMessage[memorybrowser.DetailMsg](t, intent.Cmd)
	_, command = app.Update(detailRequest)
	detail := memoryBrowserCommandMessage[memoryBrowserDetailResultMsg](t, command)
	app.Update(detail)
	mutation := memoryBrowserCommandMessage[memorybrowser.MutationMsg](t, app.memoryBrowser.HandleAction(ui.EventContext{}, ui.Action{ID: "memorybrowser.pin"}).Cmd)
	_, command = app.Update(mutation)
	completion := memoryBrowserCommandMessage[memoryBrowserMutationResultMsg](t, command)
	if completion.Err != nil {
		t.Fatal(completion.Err)
	}
	app.Update(completion)
	actual, err := app.WorkspaceDB.Memory.Get(context.Background(), record.ID)
	if err != nil || actual.Revision != 2 || !actual.Pinned {
		t.Fatalf("typed mutation not persisted: %#v %v", actual, err)
	}
}

func TestMemoryBrowserDetailStaleResultsAndDuplicateMutationIntent(t *testing.T) {
	app, request := newMemoryBrowserTestApp(t)
	record := memoryBrowserTestRecord(t, app.WorkspaceDB, "Owner checked detail")
	app.applyMemoryBrowserSearch(memoryBrowserCommandMessage[memoryBrowserSearchResultMsg](t, app.handleMemoryBrowserSearch(request)))
	intent := app.memoryBrowser.HandleAction(ui.EventContext{}, ui.Action{ID: "memorybrowser.inspect"})
	detailRequest := memoryBrowserCommandMessage[memorybrowser.DetailMsg](t, intent.Cmd)
	command := app.handleMemoryBrowserDetail(detailRequest)
	result := memoryBrowserCommandMessage[memoryBrowserDetailResultMsg](t, command)
	wrong := result
	wrong.WorkspaceGeneration++
	app.applyMemoryBrowserDetail(wrong)
	if app.memoryBrowser.HandleAction(ui.EventContext{}, ui.Action{ID: "memorybrowser.correct"}).Action != nil {
		t.Fatal("stale detail enabled correction")
	}
	app.applyMemoryBrowserDetail(result)
	mutation := memoryBrowserCommandMessage[memorybrowser.MutationMsg](t, app.memoryBrowser.HandleAction(ui.EventContext{}, ui.Action{ID: "memorybrowser.pin"}).Cmd)
	command = app.handleMemoryBrowserMutation(mutation)
	if command == nil || app.handleMemoryBrowserMutation(mutation) != nil {
		t.Fatal("mutation intent was submitted more than once")
	}
	actual, err := app.WorkspaceDB.Memory.Get(context.Background(), record.ID)
	if err != nil || actual.Pinned || actual.Revision != 1 {
		t.Fatalf("mutation handler performed Update I/O: %#v %v", actual, err)
	}
	mutationResult := memoryBrowserCommandMessage[memoryBrowserMutationResultMsg](t, command)
	if mutationResult.Err != nil {
		t.Fatal(mutationResult.Err)
	}
	actual, err = app.WorkspaceDB.Memory.Get(context.Background(), record.ID)
	if err != nil || !actual.Pinned || actual.Revision != 2 {
		t.Fatalf("command did not commit exact revision: %#v %v", actual, err)
	}
	app.closeMemoryBrowser()
	app.applyMemoryBrowserMutation(mutationResult)
	if app.memoryBrowser.IsOpen() {
		t.Fatal("late mutation reopened hidden panel")
	}
}
