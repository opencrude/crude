package viewmodel

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"crude/fileio"
	"crude/shellport"
	findinfiles "crude/tui/screens/findinfiles"
	"crude/tui/ui"

	tea "charm.land/bubbletea/v2"
)

func TestLiveUndoWaitsForPriorEditPublicationBeforeAdmission(t *testing.T) {
	for _, heldTopic := range []bool{false, true} {
		name := "topic-after-gesture-drain"
		if heldTopic {
			name = "topic-before-gesture-drain"
		}
		t.Run(name, func(t *testing.T) { testLiveUndoWaitsForPriorEditPublication(t, heldTopic) })
	}
}

func testLiveUndoWaitsForPriorEditPublication(t *testing.T, heldTopic bool) {
	runtime := remoteJourneyRuntime(t, "http://127.0.0.1:1")
	root := t.TempDir()
	path := filepath.Join(root, "publication.txt")
	if err := os.WriteFile(path, []byte("alpha"), 0600); err != nil {
		t.Fatal(err)
	}
	shell := newRemoteJourneyShell(t, runtime, root)
	ed := liveTestOpen(t, shell.app, path)
	// Deliberately hold the independent FIFO while delivering command results.
	// The ordinary journey helper also consumes publications and therefore cannot
	// guarantee this legal interleaving when both are ready at once.
	runWithoutTopics := func(command tea.Cmd) {
		queue := []tea.Cmd{command}
		for steps := 0; len(queue) > 0; steps++ {
			if steps >= 64 {
				t.Fatal("held-publication command journey did not settle")
			}
			cmd := queue[0]
			queue = queue[1:]
			if cmd == nil {
				continue
			}
			msg := cmd()
			if batch, ok := msg.(tea.BatchMsg); ok {
				queue = append(queue, batch...)
				continue
			}
			if msg != nil {
				_, follow := shell.app.Update(msg)
				queue = append(queue, follow)
			}
		}
	}
	ed.InsertRune('X')
	// Decode/apply the real command receipt without consuming the independently
	// queued topic. This ordering is legal even on a healthy local connection.
	runWithoutTopics(shell.app.drainLiveEditorCommands())
	state := shell.app.liveEditors[ed]
	if !state.sync || state.pushedRevision != 1 || ed.DocumentState().Revision != 2 {
		t.Fatalf("fixture did not separate receipt/publication clocks: %+v %+v", state, ed.DocumentState())
	}
	ed.Undo()
	intent := state.undo
	if intent == nil || !ed.DocumentState().UndoPending {
		t.Fatal("gesture did not reach the editor history owner")
	}
	var queued []tea.Msg
	if heldTopic {
		deadline := time.NewTimer(5 * time.Second)
		defer deadline.Stop()
		for state.topic == nil {
			select {
			case msg := <-shell.fifo:
				if topic, ok := msg.(shellport.LiveDocumentsChangedMsg); ok {
					shell.app.applyDocumentTopic(topic)
				} else {
					queued = append(queued, msg)
				}
			case <-deadline.C:
				t.Fatal("prior edit topic did not arrive")
			}
		}
	}
	runWithoutTopics(shell.app.drainLiveEditorCommands())
	if !heldTopic && (state.busy || state.undo != nil || ed.DocumentState().UndoPending || !ed.DocumentState().HistoryQueued || !state.sync) {
		t.Fatalf("unsubmitted undo blocked the preceding publication: %+v %+v", state, ed.DocumentState())
	}
	for _, msg := range queued {
		_, follow := shell.app.Update(msg)
		runAppCommand(t, shell.app, follow)
	}
	// The prior topic can now ACK, then Undo is admitted at its resulting revision.
	liveTestSync(t, shell.app)
	if ed.Value() != "alpha" || ed.Modified() || ed.DocumentState().HistoryQueued || ed.DocumentState().UndoPending || state.sync || state.err != nil {
		t.Fatalf("undo did not settle through pushed history: %q %+v %+v", ed.Value(), ed.DocumentState(), state)
	}
	// A retired intent is not valid against the later history generation.
	if err := ed.DeferDocumentUndoUntilSync(*intent); err == nil {
		t.Fatal("stale undo intent re-entered the queue")
	}
	canonical, err := shell.app.ws.(shellport.LiveDocuments).SyncDocument(t.Context(), ed.DocumentState().DocumentID, 0)
	if err != nil || string(canonical.Snapshot.Text) != "alpha" || canonical.Snapshot.Revision != 3 {
		t.Fatalf("undo was replayed or not committed: %+v %v", canonical, err)
	}
}

func TestSearchCommandsSeedSelectedTextAndKeepModeDrafts(t *testing.T) {
	app := newTestAppForPopupMouse(t)
	app.EditorState.ActiveView = "/tmp/search.txt"
	ed := app.EditorState.Editor
	ed.ReplaceDocument("one alpha.* two alpha.*")
	ed.SetSelection(0, 4, 0, 11)
	app.dispatchKeyPress(tea.KeyPressMsg{Code: 'f', Mod: tea.ModCtrl})
	if app.FindReplace.Query() != "alpha.*" || ed.Search().Query != "alpha.*" || app.FindReplace.MatchCount != 2 {
		t.Fatalf("selected query not installed: %+v", ed.Search())
	}
	if !app.FindReplace.FindEdit().HasSelection() {
		t.Fatal("query not selected for immediate replacement")
	}
	app.dispatchKeyPress(tea.KeyPressMsg{Code: 'b', Text: "beta"})
	if app.FindReplace.Query() != "beta" {
		t.Fatal("typing appended instead of replacing seeded query")
	}
	app.dispatchKeyPress(tea.KeyPressMsg{Code: 'h', Mod: tea.ModCtrl})
	app.dispatchKeyPress(tea.KeyPressMsg{Code: tea.KeyTab})
	app.dispatchPasteMsg(tea.PasteMsg{Content: "replacement"})
	findEdit, replaceEdit := app.FindReplace.FindEdit(), app.FindReplace.ReplaceEdit()
	app.dispatchKeyPress(tea.KeyPressMsg{Code: 'f', Mod: tea.ModCtrl})
	app.dispatchKeyPress(tea.KeyPressMsg{Code: 'h', Mod: tea.ModCtrl})
	if app.FindReplace.Query() != "beta" || app.FindReplace.Replace() != "replacement" || app.FindReplace.FindEdit() != findEdit || app.FindReplace.ReplaceEdit() != replaceEdit {
		t.Fatal("find/replace transition lost retained edit owners")
	}
}

func TestSearchSelectedMultilineRegexIsLiteralAndInvalidQueryPaintsError(t *testing.T) {
	root := t.TempDir()
	path := filepath.Join(root, "search.txt")
	if err := os.WriteFile(path, []byte("a.*\n\tb\naZZ\n\tb"), 0600); err != nil {
		t.Fatal(err)
	}
	app := newEditorPaintApp(t, root)
	if _, err := app.openEditorTab(path); err != nil {
		t.Fatal(err)
	}
	ed := app.EditorState.Editor
	ed.SetSelection(0, 0, 1, 5)
	app.openFindReplace(false)
	if app.FindReplace.MatchCount != 1 || !app.FindReplace.Regex || !strings.Contains(app.FindReplace.Query(), `\n\t`) {
		t.Fatalf("multiline selection=%q matches=%d", app.FindReplace.Query(), app.FindReplace.MatchCount)
	}
	app.FindReplace.SetQuery("[")
	app.FindReplace.OnChange()
	if app.FindReplace.Error == "" || app.FindReplace.MatchCount != 0 {
		t.Fatal("invalid regex not reported")
	}
	if painted := app.View().RenderContent(); !strings.Contains(painted, "Invalid regex") {
		t.Fatal("invalid regex absent from actual App paint")
	}
}

func TestProjectSearchModeTransitionsKeepDraftsResultsAndOptions(t *testing.T) {
	app := newTestAppForPopupMouse(t)
	app.toggleFindInFiles()
	s := app.findInFiles
	s.SetQuery("needle")
	s.SetOptions(true, true, false)
	s.ScheduleSearch()
	s.ApplySearchResults(s.Generation(), []fileio.ProjectSearchMatch{{Path: "a", RelativePath: "a", Line: 1, EndColumn: 6, LineText: "needle"}}, nil, 0)
	_, cmd := app.toggleReplaceInFiles()
	if cmd != nil {
		t.Fatal("mode transform reran accepted search")
	}
	if app.findInFiles != s || len(s.Results()) != 1 || !s.WholeWord() || !s.CaseSensitive() {
		t.Fatal("mode transform lost search owner/results/options")
	}
	s.SetReplacement("replacement")
	app.toggleFindInFiles()
	app.toggleReplaceInFiles()
	if s.Replacement() != "replacement" || s.Mode() != findinfiles.ModeReplace {
		t.Fatal("mode transform lost replacement")
	}
	app.closeFindInFiles()
	app.toggleReplaceInFiles()
	if app.findInFiles.Query() != "needle" || app.findInFiles.Replacement() != "replacement" || !app.findInFiles.WholeWord() {
		t.Fatal("close/reopen lost retained workspace search draft")
	}
}

func TestRemoteProjectReplacementReviewAndPeerConflict(t *testing.T) {
	runtime := remoteJourneyRuntime(t, "http://127.0.0.1:1")
	root := t.TempDir()
	aPath, bPath := filepath.Join(root, "a.txt"), filepath.Join(root, "b.txt")
	for _, path := range []string{aPath, bPath} {
		if err := os.WriteFile(path, []byte("alpha alpha"), 0600); err != nil {
			t.Fatal(err)
		}
	}
	one, two := newRemoteJourneyShell(t, runtime, root), newRemoteJourneyShell(t, runtime, root)
	a := liveTestOpen(t, one.app, aPath)
	b := liveTestOpen(t, two.app, bPath)
	one.app.toggleReplaceInFiles()
	s := one.app.findInFiles
	s.SetQuery("alpha")
	s.SetReplacement("omega")
	s.ScheduleSearch()
	hits, err := fileio.SearchProject(t.Context(), root, "alpha", fileio.ProjectSearchOptions{})
	if err != nil {
		t.Fatal(err)
	}
	s.ApplySearchResults(s.Generation(), hits, nil, 0)
	request := findinfiles.ReplaceMsg{Screen: s, Generation: s.Generation(), All: true, Query: s.Query(), Replacement: s.Replacement(), Options: s.Options()}
	runAppCommand(t, one.app, one.app.applyProjectReplace(request))
	p := one.app.projectReplace
	if p == nil || !strings.Contains(p.area.Value(), "4 exact replacements") {
		t.Fatal("exact immutable review missing")
	}
	one.app.forceFrame()
	one.app.View()
	frame := one.app.overlayHost.Window(projectReplaceOverlay).Content.(*ui.WindowFrameWidget)
	if frame.Host().Focused() != p.bar.ID() {
		t.Fatalf("review focus after paint=%q", frame.Host().Focused())
	}
	if data, _ := os.ReadFile(aPath); string(data) != "alpha alpha" {
		t.Fatal("planning wrote disk")
	}
	frame.Focus(p.area.ID())
	_, escape := one.app.Update(tea.KeyPressMsg{Code: tea.KeyEsc})
	runAppCommand(t, one.app, escape)
	if one.app.projectReplace != nil {
		t.Fatal("read-only review reader swallowed Escape")
	}
	runAppCommand(t, one.app, one.app.applyProjectReplace(request))
	p = one.app.projectReplace
	if p == nil {
		t.Fatal("review did not reopen after reader Escape")
	}
	// Enter lands on Cancel, including through the actual dispatch chain.
	_, cancel := one.app.dispatchKeyPress(tea.KeyPressMsg{Code: tea.KeyEnter})
	runAppCommand(t, one.app, cancel)
	if one.app.projectReplace != nil {
		t.Fatal("default Enter did not cancel")
	}
	runAppCommand(t, one.app, one.app.applyProjectReplace(request))
	p = one.app.projectReplace
	if p == nil {
		t.Fatal("review did not reopen")
	}
	// A peer changes a canonical target after preview. The transaction must
	// reject all files, including the still-clean first target.
	b.InsertRune('X')
	runAppCommand(t, two.app, two.app.drainLiveEditorCommands())
	runAppCommand(t, one.app, one.app.handleProjectReplaceAction(projectReplaceActionMsg{p, "apply"}))
	if one.app.projectReplace == nil || !strings.Contains(p.area.Value(), "did not complete") {
		t.Fatal("peer conflict not shown")
	}
	for _, path := range []string{aPath, bPath} {
		if data, _ := os.ReadFile(path); string(data) != "alpha alpha" {
			t.Fatalf("partial commit to %s", path)
		}
	}
	if b.Value() != "Xalpha alpha" || a.Value() != "alpha alpha" {
		t.Fatal("peer draft overwritten")
	}
	runAppCommand(t, one.app, one.app.handleProjectReplaceAction(projectReplaceActionMsg{p, "cancel"}))
	b.Undo()
	runAppCommand(t, two.app, two.app.drainLiveEditorCommands())
	liveTestSync(t, one.app)
	runAppCommand(t, one.app, one.app.applyProjectReplace(request))
	p = one.app.projectReplace
	if p == nil {
		t.Fatal("clean review did not reopen")
	}
	cmd := one.app.handleProjectReplaceAction(projectReplaceActionMsg{p, "apply"})
	if cmd == nil {
		t.Fatal("apply command missing")
	}
	// Exact retained execution is idempotent even if shutdown joins it.
	result := cmd()
	duplicate := cmd()
	if result.(projectReplaceAppliedMsg).safety == "" || result.(projectReplaceAppliedMsg).safety != duplicate.(projectReplaceAppliedMsg).safety {
		t.Fatal("command replayed or missing safety history")
	}
	_, follow := one.app.Update(result)
	runAppCommand(t, one.app, follow)
	liveTestSync(t, one.app)
	liveTestSync(t, two.app)
	for _, path := range []string{aPath, bPath} {
		if data, _ := os.ReadFile(path); string(data) != "omega omega" {
			t.Fatalf("disk %s=%q", path, data)
		}
	}
	if a.Value() != "omega omega" || b.Value() != "omega omega" || a.Modified() || b.Modified() {
		t.Fatalf("canonical mirrors not reconciled: %q / %q", a.Value(), b.Value())
	}
	if one.app.overlayHost.IsOpen(projectReplaceOverlay) {
		t.Fatal("successful review not closed")
	}
	if one.app.windowManager.Window(string(ui.OverlayReplaceInFiles)) == nil {
		t.Fatal("search surface lost on apply")
	}
}

func TestProjectSearchSeedsSelectionAndReinvocationFocuses(t *testing.T) {
	app := newTestAppForPopupMouse(t)
	app.EditorState.ActiveView = "/tmp/search.txt"
	app.EditorState.Editor.ReplaceDocument("one selected two")
	app.EditorState.Editor.SetSelection(0, 4, 0, 12)
	_, cmd := app.toggleFindInFiles()
	runAppCommand(t, app, cmd)
	if app.findInFiles.Query() != "selected" {
		t.Fatalf("project query=%q", app.findInFiles.Query())
	}
	owner := app.findInFiles
	app.toggleFindInFiles()
	if app.findInFiles != owner {
		t.Fatal("search command unexpectedly closed the search surface")
	}
}
