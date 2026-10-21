package filepicker

import (
	"errors"
	"path/filepath"
	"testing"

	tea "charm.land/bubbletea/v2"

	"crude/third_party/pathwalk"
	"crude/tui/filetree"
	"crude/tui/surface"
	"crude/tui/ui"
)

func assertNoPickerRow(t *testing.T, screen *Screen, host *ui.Host) {
	t.Helper()
	for i := 0; i < 2; i++ {
		screen.sync()
		host.Resize(80+i, 14)
		host.Layout()
		buf := surface.NewCellBuffer(81, 14)
		host.Paint(buf)
		if entry, ok := screen.Selected(); ok || screen.tree.tree.ActiveID() != "" {
			t.Fatalf("paint/layout restored row: selected=%#v ok=%v tree=%q", entry, ok, screen.tree.tree.ActiveID())
		}
		if screen.FocusedPath() != screen.Current() {
			t.Fatalf("unselected confirmation target=%q root=%q", screen.FocusedPath(), screen.Current())
		}
		if got := buf.Get(10, 7).BG; got == screen.tokens.TreeCursorBg() {
			t.Fatal("deselected first row still paints cursor background")
		}
	}
}

func TestFolderPickerRootHasNoImplicitChildSelection(t *testing.T) {
	screen, host := pickerHost(t, ModeFolder)
	assertNoPickerRow(t, screen, host)
	host.Handle(tea.MouseMotionMsg{X: 4, Y: 7})
	assertNoPickerRow(t, screen, host)
	selected := commandMessage[SelectMsg](t, screen.ConfirmSelection())
	if selected.Path != pickerPath("") || !selected.Folder || !selected.ValidFor(screen) {
		t.Fatalf("root confirmation=%#v", selected)
	}
	host.Focus(ListID)
	host.Handle(tea.KeyPressMsg{Code: tea.KeyDown})
	if screen.FocusedPath() != pickerPath("src") {
		t.Fatal("first Down did not explicitly select first row")
	}
	nav := commandMessage[NavigateMsg](t, host.Handle(tea.KeyPressMsg{Code: tea.KeyEnter}))
	if nav.Path != pickerPath("src") || !nav.ValidFor(screen) || screen.model.NodeByPath(nav.Path).IsExpanded {
		t.Fatalf("Enter must browse without expanding: %#v", nav)
	}
	if !screen.BeginAsyncDirectoryLoadFor(nav.Path, nav.Intent()) || !screen.CompleteAsyncDirectoryLoadFor(nav.Intent(), nav.Path, []Entry{
		{Name: "nested", Path: pickerPath("src/nested"), IsDir: true},
	}) {
		t.Fatal("root navigation failed")
	}
	assertNoPickerRow(t, screen, host)
	selected = commandMessage[SelectMsg](t, screen.ConfirmSelection())
	if selected.Path != pickerPath("src") {
		t.Fatalf("new root confirmation=%#v", selected)
	}
}

func TestFolderPickerArrowDisclosureAndClickKeepRoot(t *testing.T) {
	screen, host := pickerHost(t, ModeFolder)
	host.Focus(ListID)
	host.Handle(tea.KeyPressMsg{Code: tea.KeyDown})
	load := commandMessage[LoadMsg](t, host.Handle(tea.KeyPressMsg{Code: tea.KeyRight}))
	if load.Path != pickerPath("src") || screen.Current() != pickerPath("") {
		t.Fatalf("Right navigation=%#v root=%q", load, screen.Current())
	}
	if !screen.BeginAsyncDirectoryExpandFor(load.Path, load.Intent()) || !screen.CompleteAsyncDirectoryLoadFor(load.Intent(), load.Path, []Entry{
		{Name: "nested", Path: pickerPath("src/nested"), IsDir: true},
	}) {
		t.Fatal("Right expansion failed")
	}
	host.Handle(tea.KeyPressMsg{Code: tea.KeyRight})
	if screen.FocusedPath() != pickerPath("src/nested") {
		t.Fatal("Right on expanded row did not enter tree children")
	}
	host.Handle(tea.KeyPressMsg{Code: tea.KeyLeft})
	if screen.FocusedPath() != pickerPath("src") {
		t.Fatal("Left did not move to tree parent")
	}
	host.Handle(tea.KeyPressMsg{Code: tea.KeyLeft})
	if screen.VisibleCount() != 1 || screen.model.NodeByPath(pickerPath("src")).IsExpanded {
		t.Fatal("Left did not collapse")
	}
	host.Handle(tea.MouseClickMsg{X: 4, Y: 7, Button: tea.MouseLeft})
	if screen.VisibleCount() != 2 || screen.Current() != pickerPath("") {
		t.Fatal("single click stopped expanding inline")
	}
}

func TestFolderPickerBackspaceOwnership(t *testing.T) {
	for _, focus := range []ui.ComponentID{FilterID, ListID, ActionsID} {
		t.Run(string(focus), func(t *testing.T) {
			screen, host := pickerHost(t, ModeFolder)
			host.Focus(focus)
			result := host.Handle(tea.KeyPressMsg{Code: tea.KeyBackspace})
			if focus == FilterID {
				if !result.Handled || result.Cmd != nil || screen.Current() != pickerPath("") {
					t.Fatalf("empty search navigated: %#v", result)
				}
				host.Handle(tea.KeyPressMsg{Code: 's', Text: "s"})
				clear := commandMessage[SearchMsg](t, host.Handle(tea.KeyPressMsg{Code: tea.KeyBackspace}))
				if clear.Query != "" || screen.Current() != pickerPath("") {
					t.Fatalf("nonempty search Backspace navigated: %#v", clear)
				}
				if result = host.Handle(tea.KeyPressMsg{Code: tea.KeyBackspace}); result.Cmd != nil {
					t.Fatal("second empty search Backspace navigated")
				}
				return
			}
			nav := commandMessage[NavigateMsg](t, result)
			if nav.Path != filepath.Dir(pickerPath("")) || !nav.ValidFor(screen) {
				t.Fatalf("parent navigation=%#v", nav)
			}
			screen.SetDirectory(filepath.VolumeName(pickerPath(""))+string(filepath.Separator), []Entry{})
			if result = host.Handle(tea.KeyPressMsg{Code: tea.KeyBackspace}); result.Cmd != nil {
				t.Fatal("filesystem root Backspace navigated")
			}
		})
	}
}

func TestFolderPickerConfirmationUsesVisibleSelectionAcrossFocus(t *testing.T) {
	for _, via := range []string{"public", "button", "actions-enter", "filter-shortcut", "tree-shortcut", "actions-shortcut"} {
		t.Run(via, func(t *testing.T) {
			screen, host := pickerHost(t, ModeFolder)
			host.Focus(ListID)
			host.Handle(tea.KeyPressMsg{Code: tea.KeyDown})
			host.Focus(ActionsID)
			buf := surface.NewCellBuffer(80, 14)
			host.Paint(buf)
			if buf.Get(10, 7).BG != screen.tokens.TreeCursorBg() {
				t.Fatal("selected row disappeared when action controls took focus")
			}
			var result ui.EventResult
			switch via {
			case "public":
				result = screen.ConfirmSelection()
			case "button":
				b, _ := screen.actions.ButtonBounds("filepicker.select")
				result = host.Handle(tea.MouseClickMsg{X: b.X + 1, Y: screen.actions.Bounds().Y, Button: tea.MouseLeft})
			case "actions-enter":
				result = host.Handle(tea.KeyPressMsg{Code: tea.KeyEnter})
			default:
				focus := map[string]ui.ComponentID{"filter-shortcut": FilterID, "tree-shortcut": ListID, "actions-shortcut": ActionsID}[via]
				host.Focus(focus)
				result = host.Handle(tea.KeyPressMsg{Code: 'o', Mod: tea.ModCtrl})
			}
			selected := commandMessage[SelectMsg](t, result)
			if selected.Path != pickerPath("src") || !selected.Folder || !selected.ValidFor(screen) {
				t.Fatalf("selected confirmation=%#v", selected)
			}
		})
	}
}

func TestFolderPickerEscapeLadderAndQueuedSearchClear(t *testing.T) {
	screen, host := pickerHost(t, ModeFolder)
	search := commandMessage[SearchMsg](t, host.Handle(tea.KeyPressMsg{Code: 's', Text: "s"}))
	screen.BeginPathSearch(search.Query)
	screen.ApplyPathSearch(search.Query, []filetree.PathMatch{{Entry: pathwalk.Entry{Name: "src", Path: pickerPath("src"), IsDir: true}}})
	clear := commandMessage[SearchMsg](t, host.Handle(tea.KeyPressMsg{Code: tea.KeyEsc}))
	if clear.Query != "" || search.ValidFor(screen) || !clear.ValidFor(screen) {
		t.Fatalf("search Escape fences wrong: search=%#v clear=%#v", search, clear)
	}
	// The retained browse projection started deselected. Search clear skips an
	// empty row stage; an explicitly selected browse row needs its own Escape.
	host.Focus(ListID)
	host.Handle(tea.KeyPressMsg{Code: tea.KeyDown})
	host.Focus(FilterID)
	host.Handle(tea.KeyPressMsg{Code: 's', Text: "s"})
	clear = commandMessage[SearchMsg](t, host.Handle(tea.KeyPressMsg{Code: tea.KeyEsc}))
	if result := host.Handle(tea.KeyPressMsg{Code: tea.KeyEsc}); result.Cmd != nil || !clear.ValidFor(screen) {
		t.Fatalf("row Escape lost queued clear or closed: %#v valid=%v", result, clear.ValidFor(screen))
	}
	screen.ClearPathSearch() // settle the older empty-query message
	assertNoPickerRow(t, screen, host)
	cancel := commandMessage[CancelMsg](t, host.Handle(tea.KeyPressMsg{Code: tea.KeyEsc}))
	if !cancel.ValidFor(screen) || clear.ValidFor(screen) {
		t.Fatal("final Escape did not close with exact current intent")
	}

	_, host = pickerHost(t, ModeFolder)
	commandMessage[CancelMsg](t, host.Handle(tea.KeyPressMsg{Code: tea.KeyEsc}))
}

func TestFolderPickerRootFailureAndCancelRestoreSelectedProjection(t *testing.T) {
	for _, fail := range []bool{false, true} {
		screen, host := pickerHost(t, ModeFolder)
		search := commandMessage[SearchMsg](t, host.Handle(tea.KeyPressMsg{Code: 's', Text: "s"}))
		screen.BeginPathSearch(search.Query)
		screen.ApplyPathSearch(search.Query, []filetree.PathMatch{{Entry: pathwalk.Entry{Name: "src", Path: pickerPath("src"), IsDir: true}}})
		nav := commandMessage[NavigateMsg](t, host.Handle(tea.KeyPressMsg{Code: tea.KeyEnter}))
		if !screen.BeginAsyncDirectoryLoadFor(nav.Path, nav.Intent()) {
			t.Fatal("navigation not admitted")
		}
		if fail {
			screen.FailAsyncDirectoryLoadFor(nav.Intent(), errors.New("denied"))
		} else {
			screen.CancelAsyncDirectoryLoadFor(nav.Intent())
		}
		if screen.Current() != pickerPath("") || screen.FocusedPath() != pickerPath("src") || screen.filter.field.Value() != "s" || screen.VisibleCount() != 1 || !screen.searchActive {
			t.Fatalf("failed=%v did not restore selected root/query/rows", fail)
		}
	}
}

func TestFolderPickerClearRejectsQueuedAndRunningDirectoryIntents(t *testing.T) {
	for _, expand := range []bool{false, true} {
		for _, started := range []bool{false, true} {
			screen, host := pickerHost(t, ModeFolder)
			host.Focus(ListID)
			host.Handle(tea.KeyPressMsg{Code: tea.KeyDown})
			key := tea.KeyEnter
			if expand {
				key = tea.KeyRight
			}
			result := host.Handle(tea.KeyPressMsg{Code: key})
			var intent IntentToken
			if expand {
				intent = commandMessage[LoadMsg](t, result).Intent()
			} else {
				intent = commandMessage[NavigateMsg](t, result).Intent()
			}
			if started {
				if expand {
					screen.BeginAsyncDirectoryExpandFor(pickerPath("src"), intent)
				} else {
					screen.BeginAsyncDirectoryLoadFor(pickerPath("src"), intent)
				}
			}
			host.Handle(tea.KeyPressMsg{Code: tea.KeyEsc})
			if screen.IsCurrentIntent(intent.Epoch, intent.Sequence) || screen.CompleteAsyncDirectoryLoadFor(intent, pickerPath("src"), []Entry{{Name: "late", Path: pickerPath("src/late"), IsDir: true}}) {
				t.Fatalf("late directory intent survived Escape: expand=%v started=%v", expand, started)
			}
			if screen.Current() != pickerPath("") {
				t.Fatal("Escape kept pending root")
			}
			assertNoPickerRow(t, screen, host)
			if screen.model.NodeByPath(pickerPath("src")).Loading {
				t.Fatal("canceled expansion left directory loading")
			}
		}
	}
}

func TestFolderPickerHiddenPolicyAndSearchMatchingSurviveNavigation(t *testing.T) {
	screen, host := pickerHost(t, ModeFolder)
	host.Handle(tea.KeyPressMsg{Code: '.', Mod: tea.ModCtrl})
	screen.SetDirectory(pickerPath("src"), []Entry{{Name: ".nested", Path: pickerPath("src/.nested"), IsDir: true}})
	if screen.VisibleCount() != 1 {
		t.Fatal("root replacement lost hidden policy")
	}
	assertNoPickerRow(t, screen, host)
	search := commandMessage[SearchMsg](t, host.Handle(tea.KeyPressMsg{Code: 'n', Text: "n"}))
	screen.BeginPathSearch(search.Query)
	screen.ApplyPathSearch(search.Query, []filetree.PathMatch{{Entry: pathwalk.Entry{Name: ".nested", Path: pickerPath("src/.nested"), IsDir: true}}})
	if screen.FocusedPath() != pickerPath("src/.nested") {
		t.Fatal("search did not select matching hidden folder")
	}
	nav := commandMessage[NavigateMsg](t, host.Handle(tea.KeyPressMsg{Code: tea.KeyEnter}))
	if nav.Path != pickerPath("src/.nested") {
		t.Fatalf("search Enter=%#v", nav)
	}
}

func TestFolderPickerNewMovementInvalidatesQueuedNavigationAndExpansion(t *testing.T) {
	for _, expand := range []bool{false, true} {
		screen, host := pickerHost(t, ModeFolder)
		screen.SetDirectory(pickerPath(""), []Entry{
			{Name: "a", Path: pickerPath("a"), IsDir: true},
			{Name: "b", Path: pickerPath("b"), IsDir: true},
		})
		host.Focus(ListID)
		host.Handle(tea.KeyPressMsg{Code: tea.KeyDown})
		key := tea.KeyEnter
		if expand {
			key = tea.KeyRight
		}
		result := host.Handle(tea.KeyPressMsg{Code: key})
		var intent IntentToken
		if expand {
			intent = commandMessage[LoadMsg](t, result).Intent()
		} else {
			intent = commandMessage[NavigateMsg](t, result).Intent()
		}
		host.Handle(tea.KeyPressMsg{Code: tea.KeyDown})
		if screen.IsCurrentIntent(intent.Epoch, intent.Sequence) || screen.FocusedPath() != pickerPath("b") {
			t.Fatal("new selection did not supersede queued directory intent")
		}
		if screen.BeginAsyncDirectoryLoadFor(pickerPath("a"), intent) || screen.BeginAsyncDirectoryExpandFor(pickerPath("a"), intent) {
			t.Fatal("stale directory intent started after new movement")
		}
		if screen.model.NodeByPath(pickerPath("a")).Loading {
			t.Fatal("superseded queued expansion retained Loading")
		}
	}
}

func TestFolderPickerEscapeLadderFromActions(t *testing.T) {
	screen, host := pickerHost(t, ModeFolder)
	host.Handle(tea.KeyPressMsg{Code: 's', Text: "s"})
	host.Focus(ActionsID)
	clear := commandMessage[SearchMsg](t, host.Handle(tea.KeyPressMsg{Code: tea.KeyEsc}))
	if clear.Query != "" {
		t.Fatal("action Escape did not clear search")
	}
	if result := host.Handle(tea.KeyPressMsg{Code: tea.KeyEsc}); result.Cmd != nil {
		t.Fatal("action Escape closed before deselection")
	}
	assertNoPickerRow(t, screen, host)
	commandMessage[CancelMsg](t, host.Handle(tea.KeyPressMsg{Code: tea.KeyEsc}))
}
