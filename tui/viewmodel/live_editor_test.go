package viewmodel

import (
	"context"
	"crude/core"
	"crude/core/client"
	"crude/core/port/remote"
	"crude/core/protocol"
	"errors"
	"net"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"crude/core/port"
	"crude/tui/editor"
	"crude/workspace"
	"crude/workspace/document"

	tea "charm.land/bubbletea/v2"
)

func TestLiveDocumentTopicTwoEditorsPendingPeerUndoSave(t *testing.T) {
	runtime := remoteJourneyRuntime(t, "http://127.0.0.1:1")
	root := t.TempDir()
	path := filepath.Join(root, "push.txt")
	if err := os.WriteFile(path, []byte("abc"), 0600); err != nil {
		t.Fatal(err)
	}
	one, two := newRemoteJourneyShell(t, runtime, root), newRemoteJourneyShell(t, runtime, root)
	shells := []remoteJourneyShell{one, two}
	a, b := liveTestOpen(t, one.app, path), liveTestOpen(t, two.app, path)
	remoteJourneyUntil(t, shells, func() bool { return one.app.liveEditors[a].topicReady && two.app.liveEditors[b].topicReady })
	buffer := b.DocumentBuffer()
	b.SetCursorPosition(0, 3)
	b.InsertRune('B') // private, not submitted
	a.InsertRune('A')
	runAppCommand(t, one.app, one.app.drainLiveEditorCommands())
	remoteJourneyUntil(t, shells, func() bool { return b.Value() == "AabcB" && !one.app.liveEditors[a].sync })
	if b.DocumentBuffer() != buffer || b.Value() != "AabcB" {
		t.Fatal("peer push replaced private projection/controller")
	}
	runAppCommand(t, two.app, two.app.drainLiveEditorCommands())
	remoteJourneyUntil(t, shells, func() bool { return a.Value() == "AabcB" && !two.app.liveEditors[b].sync })
	a.Undo()
	runAppCommand(t, one.app, one.app.drainLiveEditorCommands())
	remoteJourneyUntil(t, shells, func() bool { return a.Value() == "abcB" && b.Value() == "abcB" && !a.DocumentState().HistoryQueued })
	one.app.requestEditorSaveWithFormat(a, workspace.DocumentSaveManual, false, false)
	runAppCommand(t, one.app, one.app.drainLiveEditorCommands())
	remoteJourneyUntil(t, shells, func() bool { return !a.Modified() && !b.Modified() })
	disk, err := os.ReadFile(path)
	if err != nil || string(disk) != "abcB" {
		t.Fatalf("disk=%q %v", disk, err)
	}
}

func TestLiveDocumentTopicMalformedBatchAndColdResetPreserveDraft(t *testing.T) {
	runtime := remoteJourneyRuntime(t, "http://127.0.0.1:1")
	root := t.TempDir()
	path := filepath.Join(root, "draft.txt")
	if err := os.WriteFile(path, []byte("abc"), 0600); err != nil {
		t.Fatal(err)
	}
	shell := newRemoteJourneyShell(t, runtime, root)
	ed := liveTestOpen(t, shell.app, path)
	remoteJourneyUntil(t, []remoteJourneyShell{shell}, func() bool { return shell.app.liveEditors[ed].topicReady })
	snapshot := ed.DocumentSnapshot()
	metadata := snapshot
	metadata.Text, metadata.Saved = nil, nil
	metadata.Revision += 2
	var result error
	msg := shellport.LiveDocumentsChangedMsg{Owner: shell.app.ws, DocumentID: snapshot.ID, Header: coreprotocol.TopicHeader{Seq: 2}, Update: coreprotocol.DocumentUpdate{Base: snapshot.Revision, State: metadata, Changes: []document.Change{
		{DocumentID: snapshot.ID, BaseRevision: snapshot.Revision, Edits: []document.Edit{{Start: 0, End: 0, Text: "X"}}},
		{DocumentID: snapshot.ID, BaseRevision: snapshot.Revision + 1, Edits: []document.Edit{{Start: 99, End: 99, Text: "bad"}}},
	}}, Done: func(err error) { result = err }}
	shell.app.applyDocumentTopic(msg)
	if result == nil || ed.Value() != "abc" || ed.DocumentState().Revision != snapshot.Revision {
		t.Fatalf("partially applied malformed batch: %q %+v %v", ed.Value(), ed.DocumentState(), result)
	}
	ed.InsertRune('P')
	reset := snapshot
	reset.Revision += 10
	reset.Text = []byte("peer")
	metadata = reset
	metadata.Text, metadata.Saved = nil, nil
	msg.Header = coreprotocol.TopicHeader{Seq: 3, Reset: true}
	msg.Update = coreprotocol.DocumentUpdate{State: metadata, Snapshot: &reset}
	shell.app.applyDocumentTopic(msg)
	if result == nil || ed.Value() != "Pabc" || ed.DocumentState().PendingEdits != 1 {
		t.Fatalf("cold reset lost private draft: %q %+v", ed.Value(), ed.DocumentState())
	}
}

func liveTestOpen(t *testing.T, app *App, path string) *editor.Editor {
	t.Helper()
	runAppCommand(t, app, app.openPaneFiles([]string{path}, app.Win.ActiveGroup()))
	tab := app.EditorState.Tabs[path]
	if tab == nil {
		t.Fatal("live open did not install a tab")
	}
	liveTestPump(t, []*App{app}, func() bool { return app.liveEditors[tab.Editor].topicReady || app.liveEditors[tab.Editor].err != nil })
	app.View() // settle actual Host geometry after initial asynchronous publications
	return tab.Editor
}

var liveTestFIFOs sync.Map

type liveTestFIFO struct {
	test *testing.T
	fifo chan any
}

func registerLiveTestFIFO(t *testing.T, app *App, fifo chan any) {
	liveTestFIFOs.Store(app, liveTestFIFO{t, fifo})
	t.Cleanup(func() { liveTestFIFOs.Delete(app) })
}

// Pump actual pushed events through Update; no synthetic invalidation or sync RPC.
func liveTestPump(t *testing.T, apps []*App, condition func() bool) {
	t.Helper()
	deadline := time.NewTimer(8 * time.Second)
	defer deadline.Stop()
	tick := time.NewTicker(time.Millisecond)
	defer tick.Stop()
	quiet := time.Now()
	for {
		activity, pending := false, false
		for _, app := range apps {
			entry, ok := liveTestFIFOs.Load(app)
			if !ok {
				t.Fatal("live test FIFO not registered")
			}
			fifo := entry.(liveTestFIFO).fifo
			for draining := true; draining; {
				select {
				case msg := <-fifo:
					// Fault-injection ports delegate the same remote attachment. Only the
					// fixture remaps its identity; production retains exact owner fencing.
					if topic, ok := msg.(shellport.LiveDocumentsChangedMsg); ok && topic.Owner != app.ws {
						switch app.ws.(type) {
						case *lostLiveResponse, *lostSnapshotResponse:
							topic.Owner = app.ws
							msg = topic
						}
					}
					_, cmd := app.Update(msg)
					runAppCommand(t, app, cmd)
					activity = true
				default:
					draining = false
				}
			}
			for ed, state := range app.liveEditors {
				if state.uncertain != nil {
					state.recoverDue = time.Time{}
					runAppCommand(t, app, app.drainLiveEditorCommands())
					activity = true
				}
				ds := ed.DocumentState()
				pending = pending || state.err == nil && (state.busy || state.sync || !state.topicReady || ds.InFlight || ds.UndoPending || ds.HistoryQueued || state.save)
			}
		}
		if condition != nil && condition() {
			return
		}
		if activity || pending {
			quiet = time.Now()
		}
		if condition == nil && !pending && time.Since(quiet) >= 25*time.Millisecond {
			return
		}
		select {
		case <-deadline.C:
			for _, app := range apps {
				for ed, state := range app.liveEditors {
					t.Logf("live %s: state=%+v document=%+v", ed.FilePath(), state, ed.DocumentState())
				}
			}
			t.Fatal("live push did not settle")
		case <-tick.C:
		}
	}
}

func liveTestSync(t *testing.T, app *App) {
	t.Helper()
	var apps []*App
	liveTestFIFOs.Range(func(key, value any) bool {
		if value.(liveTestFIFO).test == t {
			apps = append(apps, key.(*App))
		}
		return true
	})
	if len(apps) == 0 {
		apps = []*App{app}
	}
	liveTestPump(t, apps, nil)
}

func TestLiveEditorRealAppsShareUndoSaveAndClose(t *testing.T) {
	runtime := remoteJourneyRuntime(t, "http://127.0.0.1:1")
	root := t.TempDir()
	path := filepath.Join(root, "shared.txt")
	if err := os.WriteFile(path, []byte("abc"), 0600); err != nil {
		t.Fatal(err)
	}
	one, two := newRemoteJourneyShell(t, runtime, root), newRemoteJourneyShell(t, runtime, root)
	a, b := liveTestOpen(t, one.app, path), liveTestOpen(t, two.app, path)
	a.InsertRune('X')
	a.Undo() // The gesture precedes the edit acknowledgement.
	runAppCommand(t, one.app, one.app.drainLiveEditorCommands())
	liveTestSync(t, two.app)
	if a.Value() != "abc" || b.Value() != "abc" {
		t.Fatalf("queued undo failed: %q / %q", a.Value(), b.Value())
	}
	a.InsertRune('A')
	runAppCommand(t, one.app, one.app.drainLiveEditorCommands())
	liveTestSync(t, two.app)
	b.SetCursorPosition(0, 4)
	b.InsertRune('B')
	runAppCommand(t, two.app, two.app.drainLiveEditorCommands())
	liveTestSync(t, one.app)
	a.Undo()
	runAppCommand(t, one.app, one.app.drainLiveEditorCommands())
	liveTestSync(t, two.app)
	if a.Value() != "abcB" || b.Value() != "abcB" {
		t.Fatalf("author undo touched peer text: %q / %q", a.Value(), b.Value())
	}
	one.app.requestEditorSaveWithFormat(a, workspace.DocumentSaveManual, false, false)
	runAppCommand(t, one.app, one.app.drainLiveEditorCommands())
	liveTestSync(t, two.app)
	data, err := os.ReadFile(path)
	if err != nil || string(data) != "abcB" || a.Modified() || b.Modified() {
		t.Fatalf("save not shared: %q %v", data, err)
	}
	a.InsertRune('C')
	one.app.showDirtyClosePrompt(path)
	if frame := one.app.View().RenderContent(); !strings.Contains(frame, "Discard") || !strings.Contains(frame, "Save") || !strings.Contains(frame, "Cancel") {
		t.Fatal("the close prompt must offer Save, Discard and Cancel")
	}
	// Discard returns the shared document to the saved file, then closes the
	// view. The other shell sees the saved text, clean.
	one.app.dirtyPromptDiscard()
	if one.app.EditorState.Tabs[path] == nil {
		t.Fatal("close ran before the discard reached the document")
	}
	for range 4 {
		runAppCommand(t, one.app, one.app.drainLiveEditorCommands())
		liveTestSync(t, one.app)
	}
	liveTestSync(t, two.app)
	if one.app.EditorState.Tabs[path] != nil || b.Value() != "abcB" || b.Modified() {
		t.Fatalf("discard failed: open=%v text=%q modified=%v", one.app.EditorState.Tabs[path] != nil, b.Value(), b.Modified())
	}
	if data, err := os.ReadFile(path); err != nil || string(data) != "abcB" {
		t.Fatalf("discard must not write the file: %q %v", data, err)
	}
}

// The delegate still uses the real core connection. This seam loses only the
// acknowledgement, not the mutation; the App must re-attach the request by its
// identity and receive the recorded outcome. With unsent, the first edit
// instead never reaches the core: its call ends at the caller's deadline, as
// during a long reconnect, and no receipt exists.
type lostLiveResponse struct {
	shellport.Workspace
	live         shellport.LiveDocuments
	lost, unsent bool
	edits        int
	ids          []string
}

func (p *lostLiveResponse) DocumentOwner() document.Owner { return p.live.DocumentOwner() }
func (p *lostLiveResponse) OpenDocument(c context.Context, path string) (document.Snapshot, error) {
	return p.live.OpenDocument(c, path)
}
func (p *lostLiveResponse) SyncDocument(c context.Context, id string, r uint64) (document.SyncResult, error) {
	return p.live.SyncDocument(c, id, r)
}
func (p *lostLiveResponse) EditDocument(c context.Context, change document.Change) (document.Receipt, error) {
	p.edits++
	p.ids = append(p.ids, change.RequestID)
	if p.unsent {
		p.unsent = false
		return document.Receipt{}, context.DeadlineExceeded
	}
	r, err := p.live.EditDocument(c, change)
	if err == nil && !p.lost {
		p.lost = true
		return document.Receipt{}, errors.New("lost acknowledgement")
	}
	return r, err
}
func (p *lostLiveResponse) UndoDocument(c context.Context, id, request string, r uint64, redo bool) (document.Receipt, error) {
	return p.live.UndoDocument(c, id, request, r, redo)
}
func (p *lostLiveResponse) SaveDocument(c context.Context, id string, r uint64, reason workspace.DocumentSaveReason) (document.Snapshot, error) {
	return p.live.SaveDocument(c, id, r, reason)
}
func (p *lostLiveResponse) CloseDocument(c context.Context, id string) error {
	return p.live.CloseDocument(c, id)
}

func TestLiveEditorLostReceiptRetainsNewerTypingWithoutReplay(t *testing.T) {
	runtime := remoteJourneyRuntime(t, "http://127.0.0.1:1")
	root := t.TempDir()
	path := filepath.Join(root, "receipt.txt")
	if err := os.WriteFile(path, []byte("abc"), 0600); err != nil {
		t.Fatal(err)
	}
	shell := newRemoteJourneyShell(t, runtime, root)
	original := shell.app.ws
	port := &lostLiveResponse{Workspace: original, live: original.(shellport.LiveDocuments)}
	shell.app.ws = port
	t.Cleanup(func() { shell.app.ws = original })
	ed := liveTestOpen(t, shell.app, path)
	ed.InsertRune('X')
	cmd := shell.app.drainLiveEditorCommands()
	ed.InsertRune('Y')
	runAppCommand(t, shell.app, cmd)
	if !ed.DocumentState().InFlight || ed.Value() != "XYabc" {
		t.Fatal("uncertain edit lost draft")
	}
	liveTestSync(t, shell.app)
	// X, X again by its identity (the recorded outcome), then Y.
	if len(port.ids) != 3 || port.ids[0] != port.ids[1] || port.ids[2] == port.ids[0] || ed.Value() != "XYabc" || ed.DocumentState().InFlight {
		t.Fatalf("lost edit replayed under a new identity or stalled: ids=%v state=%+v text=%q", port.ids, ed.DocumentState(), ed.Value())
	}
	s, err := port.live.SyncDocument(t.Context(), path, 0)
	if err != nil || string(s.Snapshot.Text) != "XYabc" {
		t.Fatalf("wrong committed text %+v %v", s, err)
	}
}

// An edit lost before it reached the core has no receipt and an unknown
// outcome only for the shell. Found by the network lab: after an outage on a
// 750 ms link the reconnect outlasted the call, a "Changes not applied"
// dialog took the keyboard, and the save never ran. The editor re-attaches the
// request by its own identity when the core is reachable, asks nothing, and
// the save the user made meanwhile completes with the exact text.
func TestLiveEditorUnsentEditReattachesAndSaves(t *testing.T) {
	runtime := remoteJourneyRuntime(t, "http://127.0.0.1:1")
	root := t.TempDir()
	path := filepath.Join(root, "unsent.txt")
	if err := os.WriteFile(path, []byte("abc"), 0600); err != nil {
		t.Fatal(err)
	}
	shell := newRemoteJourneyShell(t, runtime, root)
	original := shell.app.ws
	port := &lostLiveResponse{Workspace: original, live: original.(shellport.LiveDocuments), lost: true, unsent: true}
	shell.app.ws = port
	t.Cleanup(func() { shell.app.ws = original })
	ed := liveTestOpen(t, shell.app, path)
	ed.InsertRune('X')
	runAppCommand(t, shell.app, shell.app.drainLiveEditorCommands())
	if shell.app.overlayHost.Window(liveConflictOverlay) != nil {
		t.Fatal("a request without a known outcome opened a decision dialog")
	}
	runAppCommand(t, shell.app, shell.app.requestEditorSave(ed, workspace.DocumentSaveManual, false))
	liveTestPump(t, []*App{shell.app}, func() bool {
		data, err := os.ReadFile(path)
		return err == nil && string(data) == "Xabc" && !ed.Modified()
	})
	if shell.app.overlayHost.Window(liveConflictOverlay) != nil || ed.Value() != "Xabc" {
		t.Fatalf("recovery left a dialog or changed the text: %q", ed.Value())
	}
	if len(port.ids) != 2 || port.ids[0] != port.ids[1] {
		t.Fatalf("the lost edit was not re-attached by its identity: %v", port.ids)
	}
}

func TestLiveEditorAsyncNavigationAndStaleOpenOwnership(t *testing.T) {
	runtime := remoteJourneyRuntime(t, "http://127.0.0.1:1")
	root := t.TempDir()
	aPath, bPath := filepath.Join(root, "a.txt"), filepath.Join(root, "b.txt")
	for _, path := range []string{aPath, bPath} {
		if err := os.WriteFile(path, []byte("abc\ndef"), 0600); err != nil {
			t.Fatal(err)
		}
	}
	shell := newRemoteJourneyShell(t, runtime, root)
	a := liveTestOpen(t, shell.app, aPath)
	shell.app.queueLiveNavigation(bPath, editor.LSPDiagnostic{Line: 1, Col: 2, EndLine: 1, EndCol: 3})
	if a.CursorLine() != 0 || a.CursorCol() != 0 {
		t.Fatal("async navigation moved previous editor")
	}
	runAppCommand(t, shell.app, shell.app.drainLiveEditorCommands())
	b := shell.app.EditorState.Editor
	if b.FilePath() != bPath || b.CursorLine() != 1 || b.CursorCol() != 2 {
		t.Fatalf("destination was not positioned: %s %d:%d", b.FilePath(), b.CursorLine(), b.CursorCol())
	}
	old := shell.app.openPaneFiles([]string{aPath}, shell.app.Win.ActiveGroup())().(paneFilesOpenedMsg)
	newer := shell.app.openPaneFiles([]string{aPath}, shell.app.Win.ActiveGroup())().(paneFilesOpenedMsg)
	runAppCommand(t, shell.app, shell.app.applyPaneFilesOpened(newer))
	runAppCommand(t, shell.app, shell.app.applyPaneFilesOpened(old))
	a.InsertRune('Z')
	runAppCommand(t, shell.app, shell.app.drainLiveEditorCommands())
	if state := shell.app.liveEditors[a]; state.err != nil {
		t.Fatalf("stale open closed newer document ref: %v", state.err)
	}
	// A user selection after preparation wins; no position may leak to it.
	shell.app.queueLiveNavigation(bPath, editor.LSPDiagnostic{Line: 0, Col: 1, EndLine: 0, EndCol: 1})
	queued := shell.app.editorCommands
	shell.app.editorCommands = nil
	result := queued[0]().(paneFilesOpenedMsg)
	shell.app.activateEditorTab(bPath)
	runAppCommand(t, shell.app, shell.app.applyPaneFilesOpened(result))
	if shell.app.EditorState.Editor != b || b.CursorLine() != 1 {
		t.Fatal("late navigation stole focus/position")
	}
}

func TestLiveEditorSaveRetainsNewerTypingAndShutdownStartsQueuedEdit(t *testing.T) {
	runtime := remoteJourneyRuntime(t, "http://127.0.0.1:1")
	root := t.TempDir()
	path := filepath.Join(root, "save.txt")
	if err := os.WriteFile(path, []byte("abc"), 0600); err != nil {
		t.Fatal(err)
	}
	shell := newRemoteJourneyShell(t, runtime, root)
	ed := liveTestOpen(t, shell.app, path)
	ed.InsertRune('X')
	runAppCommand(t, shell.app, shell.app.drainLiveEditorCommands())
	liveTestSync(t, shell.app)
	shell.app.requestEditorSaveWithFormat(ed, workspace.DocumentSaveManual, false, false)
	save := shell.app.drainLiveEditorCommands()
	ed.InsertRune('Y')
	runAppCommand(t, shell.app, save)
	data, err := os.ReadFile(path)
	if err != nil || string(data) != "Xabc" || ed.Value() != "XYabc" || !ed.Modified() {
		t.Fatalf("save consumed newer typing: %q %q %v", data, ed.Value(), err)
	}
	liveTestSync(t, shell.app)
	ed.InsertRune('Z')
	// Admission happened, but Bubble Tea has not started the command.
	queued := shell.app.drainLiveEditorCommands()
	if err := shell.app.FlushLiveEditors(); err != nil {
		t.Fatal(err)
	}
	// A late runtime execution must not reapply the mutation.
	_ = queued()
	s, err := shell.app.ws.(shellport.LiveDocuments).SyncDocument(t.Context(), path, 0)
	if err != nil || string(s.Snapshot.Text) != "XYZabc" {
		t.Fatalf("shutdown dropped/replayed queued edits: %q %v", s.Snapshot.Text, err)
	}
}

func TestLiveEditorOverlapRetainsEditableDraftAndRejectsClose(t *testing.T) {
	runtime := remoteJourneyRuntime(t, "http://127.0.0.1:1")
	root := t.TempDir()
	path := filepath.Join(root, "overlap.txt")
	if err := os.WriteFile(path, []byte("abc"), 0600); err != nil {
		t.Fatal(err)
	}
	one, two := newRemoteJourneyShell(t, runtime, root), newRemoteJourneyShell(t, runtime, root)
	a, b := liveTestOpen(t, one.app, path), liveTestOpen(t, two.app, path)
	a.InsertRune('X')
	b.InsertRune('Y')
	runAppCommand(t, one.app, one.app.drainLiveEditorCommands())
	liveTestSync(t, two.app)
	if b.Value() != "Yabc" || two.app.liveEditors[b].err == nil {
		t.Fatal("overlap silently overwrote optimistic text")
	}
	b.InsertRune('Z')
	two.app.closeEditorTabForce(path)
	if b.Value() != "YZabc" || two.app.EditorState.Tabs[path] == nil {
		t.Fatal("conflicted draft was not retained/editable")
	}
}

func TestLiveEditorFormatOnSaveIsAuthorOwnedCoreGroup(t *testing.T) {
	runtime := remoteJourneyRuntime(t, "http://127.0.0.1:1")
	root := t.TempDir()
	path := filepath.Join(root, "format.go")
	const source = "package p\nvar x=1\n"
	const formatted = "package p\n\nvar x = 1\n"
	if err := os.WriteFile(path, []byte(source), 0600); err != nil {
		t.Fatal(err)
	}
	one, two := newRemoteJourneyShell(t, runtime, root), newRemoteJourneyShell(t, runtime, root)
	a, b := liveTestOpen(t, one.app, path), liveTestOpen(t, two.app, path)
	one.app.editorSettings.FormatOnSave = true
	one.app.requestEditorSave(a, workspace.DocumentSaveManual, false)
	runAppCommand(t, one.app, one.app.drainLiveEditorCommands())
	liveTestSync(t, two.app)
	data, err := os.ReadFile(path)
	if err != nil || string(data) != formatted || a.Value() != formatted || b.Value() != formatted {
		t.Fatalf("format/save did not publish authoritative group: %q %q %q %v", data, a.Value(), b.Value(), err)
	}
	a.Undo()
	runAppCommand(t, one.app, one.app.drainLiveEditorCommands())
	liveTestSync(t, two.app)
	if a.Value() != source || b.Value() != source || !a.Modified() {
		t.Fatal("format was not an author-owned undo group")
	}
}

// Lose the first snapshot response after the real command commits. The App
// must send that exact identity again and receive the outcome the core's
// receipt store recorded, never a second execution under a new identity.
type lostSnapshotResponse struct {
	*lostLiveResponse
	commands shellport.LiveDocumentCommands
	kind     string
	dropped  bool
	requests []string // method:identity in call order
}

func (p *lostSnapshotResponse) answer(method, request string, s document.Snapshot, err error) (document.Snapshot, error) {
	p.requests = append(p.requests, method+":"+request)
	if p.kind == method && err == nil && !p.dropped {
		p.dropped = true
		return document.Snapshot{}, errors.New("lost " + method + " response")
	}
	return s, err
}
func (p *lostSnapshotResponse) SaveDocumentRequest(ctx context.Context, id string, rev uint64, reason workspace.DocumentSaveReason, request string) (document.Snapshot, error) {
	s, err := p.commands.SaveDocumentRequest(ctx, id, rev, reason, request)
	return p.answer("save", request, s, err)
}
func (p *lostSnapshotResponse) FormatDocumentRequest(ctx context.Context, id string, rev uint64, request string) (document.Snapshot, error) {
	s, err := p.commands.FormatDocumentRequest(ctx, id, rev, request)
	return p.answer("format", request, s, err)
}
func (p *lostSnapshotResponse) FormatDocument(ctx context.Context, id string, rev uint64) (document.Snapshot, error) {
	return p.live.(shellport.LiveDocumentFormatter).FormatDocument(ctx, id, rev)
}
func TestLiveEditorLostSnapshotResponsesReattachWithoutReplay(t *testing.T) {
	for _, kind := range []string{"save", "format"} {
		t.Run(kind, func(t *testing.T) {
			runtime := remoteJourneyRuntime(t, "http://127.0.0.1:1")
			root := t.TempDir()
			path := filepath.Join(root, "format.go")
			if err := os.WriteFile(path, []byte("package p\nvar x=1\n"), 0600); err != nil {
				t.Fatal(err)
			}
			shell := newRemoteJourneyShell(t, runtime, root)
			port := shell.app.ws
			seam := &lostSnapshotResponse{lostLiveResponse: &lostLiveResponse{Workspace: port, live: port.(shellport.LiveDocuments), lost: true}, commands: port.(shellport.LiveDocumentCommands), kind: kind}
			shell.app.ws = seam
			ed := liveTestOpen(t, shell.app, path)
			shell.app.editorSettings.FormatOnSave = true
			shell.app.requestEditorSaveWithFormat(ed, workspace.DocumentSaveManual, false, kind == "format")
			runAppCommand(t, shell.app, shell.app.drainLiveEditorCommands())
			state := shell.app.liveEditors[ed]
			if state.uncertain == nil || shell.app.overlayHost.Window(liveConflictOverlay) != nil {
				t.Fatal("a lost snapshot was not retained quietly")
			}
			state.recoverDue = time.Time{}
			runAppCommand(t, shell.app, shell.app.drainLiveEditorCommands())
			if state.uncertain != nil {
				t.Fatalf("the re-attached request did not settle: %+v", state)
			}
			liveTestSync(t, shell.app)
			first := seam.requests[0]
			want := []string{first, first}
			if kind == "format" {
				want = append(want, "save:")
			}
			if len(seam.requests) != len(want) || seam.requests[1] != first ||
				(kind == "format" && (!strings.HasPrefix(seam.requests[2], "save:") || seam.requests[2] == "save:"+strings.TrimPrefix(first, "format:"))) {
				t.Fatalf("lost %s replayed or not re-attached by its identity: %v", kind, seam.requests)
			}
			data, err := os.ReadFile(path)
			if err != nil || string(data) != ed.Value() || ed.Modified() {
				t.Fatalf("saved bytes differ from the editor: %q %v modified=%v", data, err, ed.Modified())
			}
		})
	}
}

func TestLiveEditorActualPipeReconnectReopensViewAndPreservesAuthorUndo(t *testing.T) {
	runtime := remoteJourneyRuntime(t, "http://127.0.0.1:1")
	root := t.TempDir()
	path := filepath.Join(root, "reconnect.txt")
	if err := os.WriteFile(path, []byte("abc"), 0600); err != nil {
		t.Fatal(err)
	}
	connections := make(chan net.Conn, 4)
	dial := func(ctx context.Context, id string) (*coreclient.Client, error) {
		server, client := net.Pipe()
		connections <- server
		go func() { _ = core.ServeConnection(t.Context(), runtime, server) }()
		return coreclient.Dial(ctx, coreclient.SingleCarrier(client), id)
	}
	port, err := remote.Attach(t.Context(), remote.Options{Root: root, Dial: dial})
	if err != nil {
		t.Fatal(err)
	}
	app := NewApp(port, nil)
	fifo := make(chan any, 1024)
	registerLiveTestFIFO(t, app, fifo)
	app.eventBus.SetSink(remoteJourneySink{fifo})
	port.StartReplica()
	app.Update(tea.WindowSizeMsg{Width: 120, Height: 40})
	t.Cleanup(func() { app.StopAsyncIO(); _ = app.CloseWorkspaceAttachment() })
	ed := liveTestOpen(t, app, path)
	ed.InsertRune('X')
	runAppCommand(t, app, app.drainLiveEditorCommands())
	liveTestSync(t, app)
	before := port.DocumentOwner()
	first := <-connections
	if err = first.Close(); err != nil {
		t.Fatal(err)
	}
	remoteJourneyUntil(t, []remoteJourneyShell{{app: app, fifo: fifo}}, func() bool { return port.DocumentOwner().Epoch > before.Epoch })
	ed.SetCursorPosition(0, 4)
	ed.InsertRune('Y')
	runAppCommand(t, app, app.drainLiveEditorCommands())
	liveTestSync(t, app)
	if app.liveEditors[ed].err != nil {
		t.Fatal(app.liveEditors[ed].err)
	}
	ed.Undo()
	runAppCommand(t, app, app.drainLiveEditorCommands())
	liveTestSync(t, app)
	if ed.Value() != "Xabc" {
		t.Fatalf("reconnect author undo failed: %q", ed.Value())
	}
	ed.Undo()
	runAppCommand(t, app, app.drainLiveEditorCommands())
	liveTestSync(t, app)
	if ed.Value() != "abc" {
		t.Fatalf("prior author group lost: %q", ed.Value())
	}
}

func TestLiveDocumentTopicRejectedEditAlreadyPushedDoesNotWait(t *testing.T) {
	runtime := remoteJourneyRuntime(t, "http://127.0.0.1:1")
	root := t.TempDir()
	path := filepath.Join(root, "stale.txt")
	if err := os.WriteFile(path, []byte("abc"), 0600); err != nil {
		t.Fatal(err)
	}
	shell := newRemoteJourneyShell(t, runtime, root)
	ed := liveTestOpen(t, shell.app, path)
	ed.InsertRune('X')
	change, err := ed.CaptureDocumentEdits(shell.app.ws.(shellport.LiveDocuments).DocumentOwner(), "rejected", "")
	if err != nil {
		t.Fatal(err)
	}
	state := shell.app.liveEditors[ed]
	shell.app.applyLiveEditorResult(liveEditorResult{owner: shell.app.ws, generation: shell.app.workspaceSwitchGen.Load(), ed: ed, state: state, kind: "edit", receipt: document.Receipt{DocumentID: change.DocumentID, RequestID: change.RequestID, Before: 1, Revision: 1, Error: document.ErrRevision.Error()}})
	if state.sync || ed.DocumentState().InFlight || ed.DocumentState().PendingEdits != 1 {
		t.Fatalf("rejection parked retained edit: %+v %+v", state, ed.DocumentState())
	}
	runAppCommand(t, shell.app, shell.app.drainLiveEditorCommands())
	liveTestSync(t, shell.app)
	if ed.Value() != "Xabc" || ed.DocumentState().Revision != 2 || state.err != nil {
		t.Fatal("retained edit did not publish")
	}
}

func TestLiveDocumentTopicInitialUndoAndClosedViewFence(t *testing.T) {
	runtime := remoteJourneyRuntime(t, "http://127.0.0.1:1")
	root := t.TempDir()
	path := filepath.Join(root, "initial.txt")
	if err := os.WriteFile(path, []byte("abc"), 0600); err != nil {
		t.Fatal(err)
	}
	shell := newRemoteJourneyShell(t, runtime, root)
	runAppCommand(t, shell.app, shell.app.openPaneFiles([]string{path}, shell.app.Win.ActiveGroup()))
	ed := shell.app.EditorState.Tabs[path].Editor
	ed.Undo() // initial topic has not reached Update; receipt cannot deadlock it
	runAppCommand(t, shell.app, shell.app.drainLiveEditorCommands())
	liveTestSync(t, shell.app)
	if ed.DocumentState().UndoPending || ed.DocumentState().HistoryQueued {
		t.Fatal("initial undo blocked first publication")
	}
	var held shellport.LiveDocumentsChangedMsg
	// A fresh topic gives an exact token that the lifecycle boundary invalidates.
	shell.app.ws.(shellport.LiveDocumentEpoch).FenceDocumentTopics()
	shell.app.ws.(shellport.LiveDocumentEpoch).ResumeDocumentTopics()
	deadline := time.NewTimer(5 * time.Second)
	defer deadline.Stop()
	for held.DocumentID == "" {
		select {
		case msg := <-shell.fifo:
			if topic, ok := msg.(shellport.LiveDocumentsChangedMsg); ok {
				held = topic
			} else {
				_, cmd := shell.app.Update(msg)
				runAppCommand(t, shell.app, cmd)
			}
		case <-deadline.C:
			t.Fatal("missing topic")
		}
	}
	shell.app.closeEditorTabForce(path)
	runAppCommand(t, shell.app, shell.app.drainLiveEditorCommands())
	shell.app.applyDocumentTopic(held)
	if len(shell.app.pendingDocumentTopics) != 0 || held.Valid() {
		t.Fatal("closed editor retained stale queued topic")
	}
}
