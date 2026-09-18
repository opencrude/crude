package viewmodel

import (
	"sync"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"

	"crude/clipboard"
	filepickerscreen "crude/tui/screens/filepicker"
	"crude/workspace"
)

func TestFindReplaceAsyncPasteKeepsInitiatingTextEdit91d3(t *testing.T) {
	app := newTestAppForPopupMouse(t)
	t.Cleanup(func() { resetAsyncIOStateForTest(app) })
	app.openFindReplace(true)
	fr := app.FindReplace
	if fr == nil {
		t.Fatal("find/replace popup did not open")
	}
	fr.FocusedField = 0
	initiatingEdit := fr.ActiveEdit()

	onChangeCalls := 0
	originalOnChange := fr.OnChange
	fr.OnChange = func() {
		onChangeCalls++
		originalOnChange()
	}

	readStarted := make(chan struct{})
	releaseRead := make(chan struct{})
	var startOnce sync.Once
	var releaseOnce sync.Once
	defer releaseOnce.Do(func() { close(releaseRead) })
	restore := installAsyncClipboardBackendForTest(app, clipboardAsyncBackend{
		read: func(format clipboard.Format) []byte {
			if format != clipboard.FmtText {
				return nil
			}
			startOnce.Do(func() { close(readStarted) })
			<-releaseRead
			return []byte("needle")
		},
		writeText: func(string) error { return nil },
	})
	defer restore()

	cmd := app.handleFindReplaceKey(tea.KeyPressMsg{Code: 'v', Mod: tea.ModCtrl})
	if cmd == nil {
		t.Fatal("Ctrl+V did not start an async find/replace paste")
	}
	results := make(chan tea.Msg, 1)
	go func() { results <- cmd() }()
	select {
	case <-readStarted:
	case <-time.After(time.Second):
		t.Fatal("clipboard read did not start")
	}

	// Focus moves before completion. The captured find edit remains the owner.
	fr.FocusedField = 1
	releaseOnce.Do(func() { close(releaseRead) })
	var result tea.Msg
	select {
	case result = <-results:
	case <-time.After(time.Second):
		t.Fatal("clipboard read did not complete")
	}
	if _, ok := result.(clipboardAsyncResultMsg); !ok {
		t.Fatalf("clipboard completion type = %T", result)
	}
	_, _ = app.Update(result)

	if got := initiatingEdit.Value(); got != "needle" {
		t.Fatalf("initiating find edit = %q, want needle", got)
	}
	if got := fr.Replace(); got != "" {
		t.Fatalf("newly focused replace edit was mutated: %q", got)
	}
	if onChangeCalls != 1 {
		t.Fatalf("OnChange calls = %d, want 1", onChangeCalls)
	}
}

func TestFindReplaceAsyncPasteDropsClosedPopupResult91d3(t *testing.T) {
	app := newTestAppForPopupMouse(t)
	t.Cleanup(func() { resetAsyncIOStateForTest(app) })
	app.openFindReplace(false)
	fr := app.FindReplace
	if fr == nil {
		t.Fatal("find/replace popup did not open")
	}
	initiatingEdit := fr.ActiveEdit()
	onChangeCalls := 0
	fr.OnChange = func() { onChangeCalls++ }

	readStarted := make(chan struct{})
	releaseRead := make(chan struct{})
	var startOnce sync.Once
	var releaseOnce sync.Once
	defer releaseOnce.Do(func() { close(releaseRead) })
	restore := installAsyncClipboardBackendForTest(app, clipboardAsyncBackend{
		read: func(format clipboard.Format) []byte {
			if format != clipboard.FmtText {
				return nil
			}
			startOnce.Do(func() { close(readStarted) })
			<-releaseRead
			return []byte("late")
		},
		writeText: func(string) error { return nil },
	})
	defer restore()

	cmd := app.pasteIntoFindReplace(fr)
	if cmd == nil {
		t.Fatal("find/replace paste did not return an async command")
	}
	results := make(chan tea.Msg, 1)
	go func() { results <- cmd() }()
	select {
	case <-readStarted:
	case <-time.After(time.Second):
		t.Fatal("clipboard read did not start")
	}

	app.closeFindReplace()
	if app.FindReplace != nil {
		t.Fatal("find/replace popup remained live after close")
	}
	releaseOnce.Do(func() { close(releaseRead) })
	var result tea.Msg
	select {
	case result = <-results:
	case <-time.After(time.Second):
		t.Fatal("clipboard read did not complete")
	}
	_, _ = app.Update(result)

	if got := initiatingEdit.Value(); got != "" {
		t.Fatalf("closed popup edit was mutated: %q", got)
	}
	if onChangeCalls != 0 {
		t.Fatalf("closed popup OnChange calls = %d", onChangeCalls)
	}
	state := asyncIOStateFor(app)
	state.mu.Lock()
	pending := len(state.latestClipboard)
	state.mu.Unlock()
	if pending != 0 {
		t.Fatalf("closed popup retained %d clipboard generations", pending)
	}
}

func TestEditorCtrlVAsyncResultKeepsInitiatingEditor91d3(t *testing.T) {
	app := newTestApp(t)
	t.Cleanup(func() { resetAsyncIOStateForTest(app) })

	first := editableClipboardFixture(t, app, "first.go")
	second := editableClipboardFixture(t, app, "second.go")
	app.EditorState.Editor = first
	app.EditorState.ActiveView = "first.go"
	app.EditorState.Tabs["first.go"] = NewEditorTab("first.go", first)
	app.EditorState.Tabs["second.go"] = NewEditorTab("second.go", second)

	readStarted := make(chan struct{})
	releaseRead := make(chan struct{})
	var startOnce sync.Once
	var releaseOnce sync.Once
	defer releaseOnce.Do(func() { close(releaseRead) })
	restore := installAsyncClipboardBackendForTest(app, clipboardAsyncBackend{
		read: func(format clipboard.Format) []byte {
			if format != clipboard.FmtText {
				return nil
			}
			startOnce.Do(func() { close(readStarted) })
			<-releaseRead
			return []byte("owned")
		},
		writeText: func(string) error { return nil },
	})
	defer restore()

	_, cmd := app.dispatchEditorKey(tea.KeyPressMsg{Code: 'v', Mod: tea.ModCtrl})
	if cmd == nil {
		t.Fatal("editor Ctrl+V did not return an async command")
	}
	results := make(chan tea.Msg, 1)
	go func() { results <- cmd() }()
	select {
	case <-readStarted:
	case <-time.After(time.Second):
		t.Fatal("clipboard read did not start")
	}

	app.EditorState.Editor = second
	app.EditorState.ActiveView = "second.go"
	releaseOnce.Do(func() { close(releaseRead) })
	var result tea.Msg
	select {
	case result = <-results:
	case <-time.After(time.Second):
		t.Fatal("clipboard read did not complete")
	}
	if _, ok := result.(clipboardAsyncResultMsg); !ok {
		t.Fatalf("editor clipboard completion type = %T, want owned result", result)
	}
	_, _ = app.Update(result)

	if got := first.Value(); got != "owned" {
		t.Fatalf("initiating editor value = %q, want owned", got)
	}
	if got := second.Value(); got != "" {
		t.Fatalf("newly focused editor was mutated: %q", got)
	}
	if got := app.Input.Value(); got != "" {
		t.Fatalf("chat input was mutated: %q", got)
	}
}

func TestClosedPickerResultCleansAsyncBookkeeping91d3(t *testing.T) {
	app := newTestApp(t)
	t.Cleanup(func() { resetAsyncIOStateForTest(app) })
	root := t.TempDir()
	app.FilePicker = filepickerscreen.New(app.Theme)
	app.FilePicker.Open(root, filepickerscreen.ModeFile, nil)

	restore := installAsyncDirectoryBrowserForTest(app, func(path string) (workspace.DirectoryListing, error) {
		return workspace.DirectoryListing{Path: path}, nil
	})
	defer restore()

	_, cmd := app.Update(pickerNavigateMsgForTest(t, app.FilePicker, root))
	if cmd == nil || !app.FilePicker.AsyncDirectoryLoading() {
		t.Fatal("picker navigation did not start")
	}
	app.FilePicker.Close()
	_, _ = app.Update(cmd())

	if app.FilePicker.AsyncDirectoryLoading() {
		t.Fatal("closed picker retained its directory snapshot")
	}
	state := asyncIOStateFor(app)
	state.mu.Lock()
	_, pending := state.latestDirectory[app.FilePicker]
	state.mu.Unlock()
	if pending {
		t.Fatal("closed picker retained its directory generation")
	}
}
