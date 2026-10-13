package viewmodel

import (
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"

	"crude/sessiondb"
	"crude/tui/editor"
	"crude/tui/perf"
	filepickerscreen "crude/tui/screens/filepicker"
	"crude/tui/ui"
	"crude/tui/window"
	ttypes "crude/types"
	"crude/workspace"
)

func paneOwnershipSessions(t *testing.T) (*App, *workspace.Session, *workspace.Session, window.GroupID, window.GroupID) {
	t.Helper()
	app := newFullTestApp(t)
	wsOf(app).Logger = stubLogger{}
	app.Tree.Visible = false
	_, _ = app.Update(tea.WindowSizeMsg{Width: 180, Height: 45})
	first := workspace.NewSession("ownership-first", t.TempDir(), nil, nil)
	second := workspace.NewSession("ownership-second", t.TempDir(), nil, nil)
	wsOf(app).SessionsByAgent = map[string]*workspace.Session{"first": first, "second": second}
	app.AddOrchestrator("first")
	left := app.Win.ActiveGroup()
	app.AddOrchestrator("second")
	right := app.Win.SplitGroup(left, ui.Horizontal, false)
	app.Win.MoveTabToGroup(app.Win.TabByTitle(app.agentTabTitle("second")).ID, right, 0)
	app.focusMainGroup(right)
	_ = app.View()
	return app, first, second, left, right
}

func TestMainPaneOwnershipSessionDraftsPointerAndKeyboard(t *testing.T) {
	app, first, second, left, right := paneOwnershipSessions(t)
	paneJourneyKey(app, '[', tea.ModAlt)
	ref, err := first.RefStore.CreateReference(sessiondb.CreateRequest{Kind: sessiondb.KindPastedText, Content: "first attachment only"})
	if err != nil {
		t.Fatal(err)
	}
	app.Input.InsertReference(ref)
	_, _ = app.Update(tea.PasteMsg{Content: " FIRST_DRAFT_MARKER"})
	text := app.Input.Value()
	app.Input.SetCursorFlat(3)
	app.Input.SetSelection(1, 3)
	paneJourneyKey(app, ']', tea.ModAlt)
	if app.ActiveSession != second || app.Input.Value() != "" {
		t.Fatal("second pane inherited first draft")
	}
	_, _ = app.Update(tea.PasteMsg{Content: "SECOND_DRAFT_MARKER"})
	app.forceFrame()
	_ = app.View()
	firstPaint := paneJourneyText(app, app.mainPanes.groups[left].input)
	if !strings.Contains(firstPaint, "FIRST_DRAFT_MARKER") || strings.Contains(firstPaint, "SECOND_DRAFT_MARKER") {
		t.Fatalf("inactive first composer paints wrong owner's draft: %q", firstPaint)
	}
	// Click transcript, not the text control: focus must not reposition its caret.
	r := app.mainPanes.groups[left].chat
	_, _ = app.Update(tea.MouseClickMsg{X: r.X + 2, Y: r.Y + 1, Button: tea.MouseLeft})
	_, _ = app.Update(tea.MouseReleaseMsg{X: r.X + 2, Y: r.Y + 1, Button: tea.MouseLeft})
	app.forceFrame()
	_ = app.View()
	a, b := app.Input.InputSelectionRange()
	if app.ActiveSession != first || app.Win.ActiveGroup() != left || app.Input.Value() != text || app.Input.CursorFlat() != 3 || a != 1 || b != 3 || !slices.Equal(app.Input.ReferencedIDs(), []string{ref.ID}) {
		t.Fatalf("pointer activation lost exact first draft: text=%q cursor=%d selection=%d:%d refs=%v", app.Input.Value(), app.Input.CursorFlat(), a, b, app.Input.ReferencedIDs())
	}
	app.Input.ClearInputSelection()
	paneJourneyKey(app, 'z', tea.ModCtrl)
	if app.Input.Value() != ref.DisplayName() {
		t.Fatalf("retained undo lost: %q", app.Input.Value())
	}
	paneJourneyKey(app, ']', tea.ModAlt)
	if app.Win.ActiveGroup() != right || app.ActiveSession != second || app.Input.Value() != "SECOND_DRAFT_MARKER" || len(app.Input.ReferencedIDs()) != 0 {
		t.Fatal("second draft/ref ownership changed")
	}
}

func TestMainPaneOwnershipCompletedChildKeepsSingleFamilyChrome(t *testing.T) {
	app, first, _, left, right := paneOwnershipSessions(t)
	app.switchToAgentView("first")
	app.Input.SetInput("ROOT_DRAFT_MARKER")
	child := app.ws.AgentMgr().AddSubAgent("completed-child", "first", "Completed", "model", "crafter", workspace.NewAgentState("completed-child", nil, nil))
	child.State.Completed = true
	app.syncAgentWindowTabs()
	app.Win.MoveTabToGroup(app.Win.TabByTitle(app.agentTabTitle(child.ID)).ID, right, 0)
	app.focusMainGroup(right)
	projection := app.activeSessionProjection()
	projection.deck.Open(ui.PanelEntry{ID: "ownership-panel", Title: "Retained", Content: ui.NewButton("ownership-button", "RETAINED_DECK_MARKER", "noop"), PreferredHeight: 5})
	for range 3 {
		app.forceFrame()
		_ = app.View()
		if app.ActiveSession != first || app.AgentView.ActiveID() != child.ID || app.Win.ActiveGroup() != right {
			t.Fatal("paint changed active family/member")
		}
		if app.mainPanes.groups[left].input.Empty() || !app.mainPanes.groups[right].input.Empty() {
			t.Fatal("completed child displaced the unfinished root composer")
		}
		count := 0
		for _, g := range app.mainPanes.groups {
			if !g.deck.Empty() {
				count++
			}
		}
		if count != 1 || !strings.Contains(paneJourneyText(app, app.mainPanes.groups[left].input), "ROOT_DRAFT_MARKER") {
			t.Fatal("family deck/composer is absent or duplicated")
		}
	}
}

func TestMainPaneOwnershipMinimizeMoveRestoresScroll(t *testing.T) {
	app, _, _, left, right := paneOwnershipSessions(t)
	for _, msg := range seedBudgetSession(80, 0) {
		app.appendAgentMessage("first", msg)
	}
	app.focusMainGroup(left)
	app.forceFrame()
	_ = app.View()
	v := app.mainPanes.groups[left].view
	v.chat.ScrollModel().SetAutoScroll(false)
	v.chat.ScrollModel().ScrollTo(17)
	app.forceFrame()
	_ = app.View()
	tab := app.Win.TabByTitle(app.agentTabTitle("first")).ID
	app.Win.MoveTabToGroup(tab, right, 0)
	app.focusMainGroup(right)
	app.forceFrame()
	_ = app.View()
	before := paneJourneyText(app, app.mainPanes.groups[right].chat)
	// Width-only virtualized offscreen heights can refine the thumb without
	// moving the transcript. Compare content and exact offset, not thumb size.
	withoutTrack := func(text string) string { return strings.ReplaceAll(text, "┃", "│") }
	wantOffset := v.chat.ScrollModel().Offset()
	app.minimizeAgent("first")
	app.handleTabSwitch()
	app.forceFrame()
	_ = app.View()
	app.restoreAgent("first")
	app.forceFrame()
	_ = app.View()
	restored := app.mainPanes.groups[app.Win.ActiveGroup()].view
	if restored != v || restored.chat.ScrollModel().Offset() != wantOffset || withoutTrack(paneJourneyText(app, app.mainPanes.groups[app.Win.ActiveGroup()].chat)) != withoutTrack(before) {
		t.Fatalf("move/minimize/restore lost scroll or transcript: same=%v offset=%d want=%d", restored == v, restored.chat.ScrollModel().Offset(), wantOffset)
	}
}

func TestMainPaneOwnershipVisibleBackgroundPermissionKeepsEditor(t *testing.T) {
	app, alpha, beta, channels := newBackendPromptFamilyApp(t)
	app.Tree.Visible = false
	paneJourneySize(app, 180, 45)
	app.switchToAgentView(alpha)
	app.Input.SetInput("PERMISSION_DRAFT_MARKER")
	left := app.Win.ActiveGroup()
	paths := openEditorTabs(t, app, "permission-editor.go")
	doc := app.Win.TabByTitle(app.editorTabTitle(paths[0])).ID
	right := app.Win.SplitGroup(left, ui.Horizontal, false)
	app.Win.MoveTabToGroup(doc, right, 0)
	app.Win.SwitchTo(app.Win.IndexOf(app.Win.TabByTitle(app.agentTabTitle(alpha)).ID))
	app.focusMainGroup(right)
	_ = app.View()
	_, _ = app.Update(ttypes.AgentMsg{AgentID: "child-a", Type: "blocking", Data: map[string]string{"block_id": "background-exact", "tool": "apply_patch", "question": "Allow background exact owner?"}})
	app.forceFrame()
	_ = app.View()
	if app.Win.ActiveTab().ID != doc || app.focusTarget() != FocusEditor || app.overlayHost.IsOpen(ui.OverlayAgentPrompt) || app.mainPanes.groups[left].deck.Empty() || app.Input.Value() != "PERMISSION_DRAFT_MARKER" {
		t.Fatalf("background request: active=%v doc=%v focus=%v modal=%v deck=%v draft=%q family=%v", app.Win.ActiveTab().ID, doc, app.focusTarget(), app.overlayHost.IsOpen(ui.OverlayAgentPrompt), app.mainPanes.groups[left].deck, app.Input.Value(), app.Win.GroupActiveTab(left))
	}
	r := app.mainPanes.groups[left].chat
	_, _ = app.Update(tea.MouseClickMsg{X: r.X + 2, Y: r.Y + 1, Button: tea.MouseLeft})
	_, _ = app.Update(tea.MouseReleaseMsg{X: r.X + 2, Y: r.Y + 1, Button: tea.MouseLeft})
	app.forceFrame()
	_ = app.View()
	if app.mainPanes.groups[left].view.chat.HasActiveSelection() {
		t.Fatal("background permission activation leaked through to transcript selection")
	}
	_, cmd := app.Update(tea.KeyPressMsg{Code: 'y', Text: "y"})
	runAppCommand(t, app, cmd)
	select {
	case response := <-channels["child-a"]:
		if response.RequestID != "background-exact" || response.Value != "allow" {
			t.Fatalf("wrong response %+v", response)
		}
	default:
		t.Fatal("decision did not reach exact background child")
	}
	for _, owner := range []string{alpha, beta, "child-b", "nested"} {
		if len(channels[owner]) != 0 {
			t.Fatalf("decision leaked to %s", owner)
		}
	}
	if app.Input.Value() != "PERMISSION_DRAFT_MARKER" {
		t.Fatal("permission consumed draft")
	}
}

// Execute native batches but retain product results for assertions before Update.
func paneOwnershipResult(t *testing.T, cmd tea.Cmd) tea.Msg {
	t.Helper()
	if cmd == nil {
		t.Fatal("expected returned command")
	}
	msg := cmd()
	if batch, ok := msg.(tea.BatchMsg); ok {
		for _, child := range batch {
			if child != nil {
				result := paneOwnershipResult(t, child)
				if result != nil {
					return result
				}
			}
		}
	}
	return msg
}

func TestMainPaneOwnershipEmptyPickerKeyboardAsyncAndCancel(t *testing.T) {
	app, ws := newRealShellApp(t)
	defer ws.Close()
	paneJourneySize(app, 160, 40)
	openEditorTabs(t, app, "existing.go")
	paneJourneyKey(app, tea.KeyRight, tea.ModCtrl|tea.ModAlt)
	target := app.Win.ActiveGroup()
	_, _ = app.Update(tea.KeyPressMsg{Code: 'o', Text: "o"})
	if app.FilePicker == nil || !app.FilePicker.IsOpen() {
		t.Fatal("empty-pane O did not open real picker")
	}
	// Settle real directory I/O without reopening/replacing the picker owner.
	runAppCommand(t, app, app.beginDirectoryAsyncLoad(app.FilePicker, app.ws.RootPath()))
	app.forceFrame()
	_ = app.View()
	app.FilePicker.FocusTree()
	for steps := 0; app.FilePicker.FocusedPath() != filepath.Join(app.ws.RootPath(), "main.go"); steps++ {
		if steps > 8 {
			t.Fatal("real picker did not expose main.go")
		}
		_, _ = app.Update(tea.KeyPressMsg{Code: tea.KeyDown})
	}
	_, cmd := app.Update(tea.KeyPressMsg{Code: tea.KeyEnter})
	selected := paneOwnershipResult(t, cmd)
	if _, ok := selected.(filepickerscreen.SelectMsg); !ok {
		t.Fatalf("picker returned %T", selected)
	}
	_, cmd = app.Update(selected)
	prepared := paneOwnershipResult(t, cmd)
	if _, ok := prepared.(paneFilesOpenedMsg); !ok {
		t.Fatalf("selection returned %T instead of off-UI pane preparation", prepared)
	}
	if app.EditorState.Tabs[filepath.Join(app.ws.RootPath(), "main.go")] != nil {
		t.Fatal("picker read/installed file on Update")
	}
	_, _ = app.Update(prepared)
	app.forceFrame()
	_ = app.View()
	if app.Win.ActiveGroup() != target || app.Win.ActiveTab().Group != target || app.EditorState.Editor.Value() != "package main\n" || len(app.Input.ReferencedIDs()) != 0 {
		t.Fatal("picker did not open exact pane, or attached instead")
	}
	// Replay the old real picker selection after close: no attachment/tab effect.
	count := app.Win.TabCount()
	_, cmd = app.Update(selected)
	if cmd != nil || app.Win.TabCount() != count || len(app.Input.ReferencedIDs()) != 0 {
		t.Fatal("stale picker selection became an attachment")
	}
}

func paneOwnershipDense(tb testing.TB) *App {
	tb.Helper()
	app := newFullTestApp(tb)
	wsOf(app).Logger = stubLogger{}
	app.width, app.height = 200, 50
	app.Tree.Visible = false
	for _, id := range []string{"dense-first", "dense-second"} {
		app.AddOrchestrator(id)
		for _, msg := range seedBudgetSession(300, 0) {
			app.appendAgentMessage(id, msg)
		}
	}
	left := app.Win.ActiveGroup()
	right := app.Win.SplitGroup(left, ui.Horizontal, false)
	app.Win.MoveTabToGroup(app.Win.TabByTitle(app.agentTabTitle("dense-second")).ID, right, 0)
	app.focusMainGroup(right)
	app.BufferMgr = editor.NewBufferManager()
	path := filepath.Join(tb.TempDir(), "dense-editor.txt")
	if err := os.WriteFile(path, []byte(strings.Repeat("editor dense retained line\n", 300)), 0600); err != nil {
		tb.Fatal(err)
	}
	if _, err := app.openEditorTab(path); err != nil {
		tb.Fatal(err)
	}
	app.Win.SwitchTo(app.Win.IndexOf(app.Win.TabByTitle(app.agentTabTitle("dense-first")).ID))
	app.focusMainGroup(right)
	app.forceFrame()
	_ = app.View()
	return app
}

func TestMainPaneOwnershipDenseIdlePreviewAndDividerWork(t *testing.T) {
	perf.EnableProbe()
	t.Cleanup(perf.DisableProbe)
	app := paneOwnershipDense(t)
	left := app.Win.GroupIDs()[0]
	v := app.mainPanes.groups[left].view
	renderer := app.subPaneProjections.GetExisting("dense-first").renderer
	generation := renderer.ExportMessageRowsSnapshot(v.reflowInnerW).Generation
	base := perf.Snapshot()
	for range 5 {
		_, _ = app.Update(ttypes.TickMsg(time.Now()))
		app.forceFrame()
		_ = app.View()
	}
	if renderer.ExportMessageRowsSnapshot(v.reflowInnerW).Generation != generation {
		t.Fatal("idle changed retained message generation")
	}
	root := app.Win.PaneRoot()
	r, ok := app.rootHost.ScreenRect(app.mainPanes.dividers[root.ID].ID())
	if !ok {
		t.Fatal("divider not mounted")
	}
	_, _ = app.Update(tea.MouseClickMsg{X: r.X, Y: r.Y + 3, Button: tea.MouseLeft})
	for i := range 12 {
		_, _ = app.Update(tea.MouseMotionMsg{X: r.X + i, Y: r.Y + 3, Button: tea.MouseLeft})
		app.forceFrame()
		_ = app.View()
	}
	work := perf.Snapshot().Sub(base)
	if work.ItemsRendered != 0 || work.LinesRendered != 0 || renderer.ExportMessageRowsSnapshot(v.reflowInnerW).Generation != generation {
		t.Fatalf("divider preview invalidated dense content: %+v", work)
	}
	_, _ = app.Update(tea.MouseReleaseMsg{X: r.X + 11, Y: r.Y + 3, Button: tea.MouseLeft})
	app.forceFrame()
	_ = app.View()
	if app.mainPanes.placements[left].W != r.X+11-app.mainPanes.Bounds().X {
		t.Fatalf("release did not commit exact geometry: %+v", app.mainPanes.placements[left])
	}
	if v.reflowContentW != app.mainPanes.groups[left].chat.W {
		t.Fatal("release retained stale chat width")
	}
}

func BenchmarkMainPane(b *testing.B) {
	for _, stage := range []string{"Presentation", "TwoAgentsPresentation", "DividerMutation"} {
		b.Run(stage, func(b *testing.B) {
			app := paneOwnershipDense(b)
			if stage == "TwoAgentsPresentation" {
				app.switchToAgentView("dense-second")
				app.forceFrame()
				_ = app.View()
			}
			r, _ := app.rootHost.ScreenRect(app.mainPanes.dividers[app.Win.PaneRoot().ID].ID())
			_, _ = app.Update(tea.MouseClickMsg{X: r.X, Y: r.Y + 3, Button: tea.MouseLeft})
			b.ReportAllocs()
			b.ResetTimer()
			for i := 0; b.Loop(); i++ {
				if stage == "DividerMutation" {
					_, _ = app.Update(tea.MouseMotionMsg{X: r.X + i%10, Y: r.Y + 3, Button: tea.MouseLeft})
				} else {
					app.forceFrame()
					_ = app.View()
				}
			}
		})
	}
}

func TestMainPaneOwnershipKeyboardNewSessionAndCancelledPicker(t *testing.T) {
	app, ws := newRealShellApp(t)
	defer ws.Close()
	paneJourneySize(app, 160, 40)
	openEditorTabs(t, app, "existing.go")
	paneJourneyKey(app, 'l', tea.ModCtrl|tea.ModAlt)
	target := app.Win.ActiveGroup()
	paneJourneyKey(app, 'n', 0)
	if tab := app.Win.ActiveTab(); tab == nil || !app.isAgentTabTitle(tab.Title) || tab.Group != target || app.focusTarget() != FocusInput {
		t.Fatal("N did not create session in empty pane")
	}
	paneJourneyKey(app, 'd', tea.ModCtrl|tea.ModAlt)
	paneJourneyKey(app, 'o', 0)
	runAppCommand(t, app, app.beginDirectoryAsyncLoad(app.FilePicker, app.ws.RootPath()))
	app.FilePicker.FocusTree()
	_, cmd := app.Update(tea.KeyPressMsg{Code: tea.KeyEnter})
	selection := paneOwnershipResult(t, cmd)
	if _, ok := selection.(filepickerscreen.SelectMsg); !ok {
		t.Fatalf("file selection=%T", selection)
	}
	paneJourneyKey(app, tea.KeyEsc, 0)
	before := app.Win.TabCount()
	_, cmd = app.Update(selection)
	if cmd != nil || app.Win.TabCount() != before || len(app.Input.ReferencedIDs()) != 0 {
		t.Fatal("cancelled picker replay opened or attached a file")
	}
}

func TestMainPaneOwnershipPreviewKeepsDenseRowsWarm(t *testing.T) {
	app := paneOwnershipDense(t)
	left := app.Win.GroupIDs()[0]
	renderer := app.subPaneProjections.GetExisting("dense-first").renderer
	v := app.mainPanes.groups[left].view
	generation := renderer.ExportMessageRowsSnapshot(v.reflowInnerW).Generation
	body := app.mainPanes.groups[left].body
	tab := app.Win.ActiveTab().ID
	paneJourneyDrag(t, app, tab, body.X+1, body.Y+body.H/2)
	if !bufferContainsPlain(app.mainSurf.Buffer(), "Split left") {
		t.Fatal("missing actual drag preview")
	}
	if renderer.ExportMessageRowsSnapshot(v.reflowInnerW).Generation != generation {
		t.Fatal("tab preview reflowed dense transcript")
	}
	paneJourneyKey(app, tea.KeyEsc, 0)
}

func TestMainPaneEmptyButtonsHaveVisibleInk(t *testing.T) {
	for _, preset := range []string{"dark", "light"} {
		t.Run(preset, func(t *testing.T) {
			app, ws := newRealShellApp(t)
			defer ws.Close()
			paneJourneySize(app, 160, 40)
			openEditorTabs(t, app, "main.go")
			paneJourneyKey(app, 'l', tea.ModCtrl|tea.ModAlt)
			app.ApplyThemePreset(preset)
			app.forceFrame()
			_ = app.View()
			g := app.mainPanes.groups[app.Win.ActiveGroup()]
			for _, b := range g.empty.buttons {
				r := b.Bounds()
				found := false
				for x := g.body.X + r.X; x < g.body.X+r.X+r.W; x++ {
					c := app.mainSurf.Buffer().Get(x, g.body.Y+r.Y)
					if c.Char != ' ' && c.Char != 0 && c.FG != c.BG {
						found = true
					}
				}
				if !found {
					t.Fatalf("button %s has invisible ink", b.Semantics().Label)
				}
			}
		})
	}
}

func TestMainPaneThemeIncludesHiddenEditorsBeforeMove(t *testing.T) {
	app, ws := newRealShellApp(t)
	defer ws.Close()
	paneJourneySize(app, 160, 40)
	paths := openEditorTabs(t, app, "hidden.go", "selected.go")
	app.ApplyThemePreset("light")
	paneJourneyKey(app, 'l', tea.ModCtrl|tea.ModAlt)
	target := app.Win.ActiveGroup()
	runAppCommand(t, app, app.openPaneFiles([]string{paths[0]}, target))
	app.forceFrame()
	_ = app.View()
	if app.EditorState.Editor.Theme() != app.Theme {
		t.Fatal("hidden editor retained previous theme")
	}
	r := app.mainPanes.groups[target].editor
	painted := app.mainSurf.Buffer().Get(r.X+r.W/2, r.Y+r.H-3)
	if painted.BG != app.rootHost.Tokens().Color(ui.ColorBg) {
		t.Fatal("reopened editor paints stale background")
	}
}
