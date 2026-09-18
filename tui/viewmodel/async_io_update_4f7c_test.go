package viewmodel

import (
	"errors"
	"path/filepath"
	"sync"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"

	"crude/clipboard"
	filepickerscreen "crude/tui/screens/filepicker"
	"crude/tui/ui"
	"crude/workspace"
)

func TestAsyncClipboardBridgeAndUpdateReturnBeforeSlowWrite4f7c(t *testing.T) {
	app := newTestApp(t)
	t.Cleanup(func() { resetAsyncIOStateForTest(app) })

	writeStarted := make(chan struct{})
	releaseWrite := make(chan struct{})
	var startOnce sync.Once
	var releaseOnce sync.Once
	defer releaseOnce.Do(func() { close(releaseWrite) })
	restoreBackend := installAsyncClipboardBackendForTest(app, clipboardAsyncBackend{
		read: func(clipboard.Format) []byte { return nil },
		writeText: func(string) error {
			startOnce.Do(func() { close(writeStarted) })
			<-releaseWrite
			return nil
		},
	})
	defer restoreBackend()

	requests := make(chan tea.Msg, 1)
	restoreSink := installDetachedMessageSinkForTest(app, func(msg tea.Msg) {
		requests <- msg
	})
	defer restoreSink()

	start := time.Now()
	app.copyToClipboard("asynchronous")
	if elapsed := time.Since(start); elapsed > 100*time.Millisecond {
		t.Fatalf("copy callback blocked for %s", elapsed)
	}

	var request tea.Msg
	select {
	case request = <-requests:
	case <-time.After(time.Second):
		t.Fatal("clipboard bridge did not enqueue a request")
	}
	if _, ok := request.(clipboardAsyncRequestMsg); !ok {
		t.Fatalf("bridge message type = %T", request)
	}

	start = time.Now()
	_, cmd := app.Update(request)
	if elapsed := time.Since(start); elapsed > 100*time.Millisecond {
		t.Fatalf("Update blocked for %s", elapsed)
	}
	if cmd == nil {
		t.Fatal("clipboard request did not return a command")
	}

	results := make(chan tea.Msg, 1)
	go func() { results <- cmd() }()
	select {
	case <-writeStarted:
	case <-time.After(time.Second):
		t.Fatal("clipboard command did not start")
	}
	select {
	case <-results:
		t.Fatal("slow clipboard write completed before it was released")
	case <-time.After(20 * time.Millisecond):
	}

	releaseOnce.Do(func() { close(releaseWrite) })
	var result tea.Msg
	select {
	case result = <-results:
	case <-time.After(time.Second):
		t.Fatal("clipboard command did not complete")
	}
	_, _ = app.Update(result)
}

func TestClipboardCopyThenPasteExecutesInRequestOrder(t *testing.T) {
	app := newTestApp(t)
	t.Cleanup(func() { resetAsyncIOStateForTest(app) })

	target := editableClipboardFixture(t, app, "target.go")
	app.EditorState.Editor = target
	app.EditorState.ActiveView = "target.go"
	app.EditorState.Tabs["target.go"] = NewEditorTab("target.go", target)

	writeStarted := make(chan struct{})
	releaseWrite := make(chan struct{})
	var startOnce sync.Once
	clipboardText := "previous"
	restore := installAsyncClipboardBackendForTest(app, clipboardAsyncBackend{
		read: func(format clipboard.Format) []byte {
			if format == clipboard.FmtText {
				return []byte(clipboardText)
			}
			return nil
		},
		writeText: func(text string) error {
			startOnce.Do(func() { close(writeStarted) })
			<-releaseWrite
			clipboardText = text
			return nil
		},
	})
	defer restore()

	copyRequest := app.prepareClipboardAsyncRequest(clipboardAsyncRequestMsg{
		operation: clipboardAsyncWriteText,
		text:      "new",
	})
	pasteRequest := app.prepareClipboardAsyncRequest(clipboardAsyncRequestMsg{
		operation: clipboardAsyncPasteEditor,
		editor:    target,
	})
	_, copyCmd := app.Update(copyRequest)
	_, pasteCmd := app.Update(pasteRequest)
	if copyCmd == nil || pasteCmd == nil {
		t.Fatal("ordered clipboard requests did not return commands")
	}

	copyResult := make(chan tea.Msg, 1)
	pasteResult := make(chan tea.Msg, 1)
	go func() { copyResult <- copyCmd() }()
	go func() { pasteResult <- pasteCmd() }()
	select {
	case <-writeStarted:
	case <-time.After(time.Second):
		t.Fatal("copy did not start")
	}
	select {
	case <-pasteResult:
		t.Fatal("paste overtook the in-flight copy")
	case <-time.After(20 * time.Millisecond):
	}

	close(releaseWrite)
	_, _ = app.Update(<-copyResult)
	_, _ = app.Update(<-pasteResult)
	if got := target.Value(); got != "new" {
		t.Fatalf("paste read %q, want clipboard value written by prior copy", got)
	}
}

func TestAsyncClipboardPasteKeepsOwnerAndDropsOlderResult4f7c(t *testing.T) {
	app := newTestApp(t)
	t.Cleanup(func() { resetAsyncIOStateForTest(app) })

	first := editableClipboardFixture(t, app, "first.go")
	second := editableClipboardFixture(t, app, "second.go")
	app.EditorState.Editor = first
	app.EditorState.ActiveView = "first.go"
	app.EditorState.Tabs["first.go"] = NewEditorTab("first.go", first)
	app.EditorState.Tabs["second.go"] = NewEditorTab("second.go", second)

	slowStarted := make(chan struct{})
	releaseSlow := make(chan struct{})
	var startOnce sync.Once
	var releaseOnce sync.Once
	defer releaseOnce.Do(func() { close(releaseSlow) })
	restoreSlow := installAsyncClipboardBackendForTest(app, clipboardAsyncBackend{
		read: func(format clipboard.Format) []byte {
			if format != clipboard.FmtText {
				return nil
			}
			startOnce.Do(func() { close(slowStarted) })
			<-releaseSlow
			return []byte("old")
		},
		writeText: func(string) error { return nil },
	})
	defer restoreSlow()

	oldRequest := app.prepareClipboardAsyncRequest(clipboardAsyncRequestMsg{
		operation: clipboardAsyncPasteEditor,
		editor:    first,
	})
	_, oldCmd := app.Update(oldRequest)
	if oldCmd == nil {
		t.Fatal("old paste did not return a command")
	}
	oldResults := make(chan tea.Msg, 1)
	go func() { oldResults <- oldCmd() }()
	select {
	case <-slowStarted:
	case <-time.After(time.Second):
		t.Fatal("slow paste did not start")
	}

	restoreFast := installAsyncClipboardBackendForTest(app, clipboardAsyncBackend{
		read: func(format clipboard.Format) []byte {
			if format == clipboard.FmtText {
				return []byte("new")
			}
			return nil
		},
		writeText: func(string) error { return nil },
	})
	newRequest := app.prepareClipboardAsyncRequest(clipboardAsyncRequestMsg{
		operation: clipboardAsyncPasteEditor,
		editor:    first,
	})
	_, newCmd := app.Update(newRequest)
	if newCmd == nil {
		t.Fatal("new paste did not return a command")
	}
	newResults := make(chan tea.Msg, 1)
	go func() { newResults <- newCmd() }()
	select {
	case <-newResults:
		t.Fatal("new clipboard request overtook the earlier in-flight request")
	case <-time.After(20 * time.Millisecond):
	}

	// Focus moved after the request. The result must still update the editor
	// that initiated the paste, not whichever editor is active on completion.
	app.EditorState.Editor = second
	app.EditorState.ActiveView = "second.go"

	releaseOnce.Do(func() { close(releaseSlow) })
	var newResult tea.Msg
	select {
	case newResult = <-newResults:
	case <-time.After(time.Second):
		t.Fatal("new paste did not run after the earlier request completed")
	}
	restoreFast()
	_, _ = app.Update(newResult)
	if got := first.Value(); got != "new" {
		t.Fatalf("intended editor value = %q, want new", got)
	}
	if got := second.Value(); got != "" {
		t.Fatalf("newly focused editor was corrupted: %q", got)
	}

	var oldResult tea.Msg
	select {
	case oldResult = <-oldResults:
	case <-time.After(time.Second):
		t.Fatal("old paste did not complete")
	}
	_, _ = app.Update(oldResult)
	if got := first.Value(); got != "new" {
		t.Fatalf("stale paste applied after newer result: %q", got)
	}
}

func TestAsyncClipboardImageResultDoesNotCrossSessions4f7c(t *testing.T) {
	app := newTestApp(t)
	t.Cleanup(func() { resetAsyncIOStateForTest(app) })

	first := &workspace.Session{
		SessionID: "first",
		RefStore:  workspace.NewReferenceStore("first", t.TempDir(), nil),
	}
	second := &workspace.Session{
		SessionID: "second",
		RefStore:  workspace.NewReferenceStore("second", t.TempDir(), nil),
	}
	app.ActiveSession = first

	restore := installAsyncClipboardBackendForTest(app, clipboardAsyncBackend{
		read: func(format clipboard.Format) []byte {
			if format == clipboard.FmtImage {
				return []byte("\x89PNG\r\n\x1a\n\x00\x00\x00\rIHDR")
			}
			return nil
		},
		writeText: func(string) error { return nil },
	})
	defer restore()

	request := app.prepareClipboardAsyncRequest(clipboardAsyncRequestMsg{
		operation: clipboardAsyncPasteInputImageFirst,
		session:   first,
	})
	_, cmd := app.Update(request)
	if cmd == nil {
		t.Fatal("image paste did not return a command")
	}
	result := cmd()
	app.ActiveSession = second
	_, _ = app.Update(result)

	if refs := first.RefStore.ListReferences(); len(refs) != 0 {
		t.Fatalf("stale image result created %d references", len(refs))
	}
	if got := app.Input.Value(); got != "" {
		t.Fatalf("stale image result changed new session input: %q", got)
	}
}

func TestClipboardImagePersistenceRunsOutsideUpdate(t *testing.T) {
	app := newTestApp(t)
	t.Cleanup(func() { resetAsyncIOStateForTest(app) })
	active := &workspace.Session{
		SessionID: "active",
		RefStore:  workspace.NewReferenceStore("active", t.TempDir(), nil),
	}
	app.ActiveSession = active

	restore := installAsyncClipboardBackendForTest(app, clipboardAsyncBackend{
		read: func(format clipboard.Format) []byte {
			if format == clipboard.FmtImage {
				return []byte("\x89PNG\r\n\x1a\n\x00\x00\x00\rIHDR")
			}
			return nil
		},
		writeText: func(string) error { return nil },
	})
	defer restore()

	request := app.prepareClipboardAsyncRequest(clipboardAsyncRequestMsg{
		operation: clipboardAsyncPasteInputImageFirst,
		session:   active,
	})
	_, readCmd := app.Update(request)
	readResult := readCmd()

	start := time.Now()
	_, persistCmd := app.Update(readResult)
	if elapsed := time.Since(start); elapsed > 100*time.Millisecond {
		t.Fatalf("image result blocked Update for %s", elapsed)
	}
	if persistCmd == nil {
		t.Fatal("image result did not return asynchronous persistence command")
	}
	if refs := active.RefStore.ListReferences(); len(refs) != 0 {
		t.Fatalf("Update persisted %d image references synchronously", len(refs))
	}

	stored := persistCmd()
	_, _ = app.Update(stored)
	if refs := active.RefStore.ListReferences(); len(refs) != 1 {
		t.Fatalf("persisted image references=%d, want 1", len(refs))
	}
	if got := app.Input.Value(); got == "" {
		t.Fatal("completed image persistence did not insert its reference")
	}
}

func TestAsyncDirectoryUpdateReturnsPromptlyAndDropsStaleResult4f7c(t *testing.T) {
	app := newTestApp(t)
	t.Cleanup(func() { resetAsyncIOStateForTest(app) })

	root := t.TempDir()
	slowPath := filepath.Join(root, "slow")
	fastPath := filepath.Join(root, "fast")
	app.FilePicker = filepickerscreen.New(app.Theme)
	app.FilePicker.Open(root, filepickerscreen.ModeFile, []filepickerscreen.Entry{
		{Name: "slow", Path: slowPath, IsDir: true},
		{Name: "fast", Path: fastPath, IsDir: true},
	})

	slowStarted := make(chan struct{})
	releaseSlow := make(chan struct{})
	var startOnce sync.Once
	var releaseOnce sync.Once
	defer releaseOnce.Do(func() { close(releaseSlow) })
	restore := installAsyncDirectoryBrowserForTest(app, func(path string) (workspace.DirectoryListing, error) {
		switch filepath.Clean(path) {
		case slowPath:
			startOnce.Do(func() { close(slowStarted) })
			<-releaseSlow
			return workspace.DirectoryListing{
				Path: slowPath,
				Entries: []workspace.DirectoryEntry{
					{Name: "old.go", Path: filepath.Join(slowPath, "old.go")},
				},
			}, nil
		case fastPath:
			return workspace.DirectoryListing{
				Path: fastPath,
				Entries: []workspace.DirectoryEntry{
					{Name: "new.go", Path: filepath.Join(fastPath, "new.go")},
				},
			}, nil
		default:
			return workspace.DirectoryListing{}, errors.New("unexpected path")
		}
	})
	defer restore()

	start := time.Now()
	_, slowCmd := app.Update(pickerNavigateMsgForTest(t, app.FilePicker, slowPath))
	if elapsed := time.Since(start); elapsed > 100*time.Millisecond {
		t.Fatalf("directory Update blocked for %s", elapsed)
	}
	if slowCmd == nil {
		t.Fatal("slow navigation did not return a command")
	}
	if !app.FilePicker.AsyncDirectoryLoading() {
		t.Fatal("picker did not enter loading state")
	}

	slowResults := make(chan tea.Msg, 1)
	go func() { slowResults <- slowCmd() }()
	select {
	case <-slowStarted:
	case <-time.After(time.Second):
		t.Fatal("slow directory read did not start")
	}

	// Cancel the first load before issuing the next user intent; activation is
	// deliberately shielded while a directory read is pending.
	app.invalidateDirectoryAsyncLoad(app.FilePicker)
	_, fastCmd := app.Update(pickerNavigateMsgForTest(t, app.FilePicker, fastPath))
	if fastCmd == nil {
		t.Fatal("fast navigation did not return a command")
	}
	_, _ = app.Update(fastCmd())
	if app.FilePicker.AsyncDirectoryLoading() {
		t.Fatal("latest directory result did not clear loading state")
	}
	if got := app.FilePicker.Current(); got != fastPath {
		t.Fatalf("current directory = %q, want %q", got, fastPath)
	}

	releaseOnce.Do(func() { close(releaseSlow) })
	var slowResult tea.Msg
	select {
	case slowResult = <-slowResults:
	case <-time.After(time.Second):
		t.Fatal("slow directory read did not finish")
	}
	_, _ = app.Update(slowResult)
	if got := app.FilePicker.Current(); got != fastPath {
		t.Fatalf("stale result changed directory to %q", got)
	}
}

func TestAsyncDirectoryInitialOpenReturnsBeforeSlowRead4f7c(t *testing.T) {
	app := newFullTestApp(t)
	t.Cleanup(func() { resetAsyncIOStateForTest(app) })

	root := t.TempDir()
	readStarted := make(chan struct{})
	releaseRead := make(chan struct{})
	var startOnce sync.Once
	var releaseOnce sync.Once
	defer releaseOnce.Do(func() { close(releaseRead) })
	restoreBrowser := installAsyncDirectoryBrowserForTest(app, func(path string) (workspace.DirectoryListing, error) {
		startOnce.Do(func() { close(readStarted) })
		<-releaseRead
		return workspace.DirectoryListing{Path: filepath.Clean(path)}, nil
	})
	defer restoreBrowser()

	results := make(chan tea.Msg, 1)
	restoreSink := installDetachedMessageSinkForTest(app, func(msg tea.Msg) {
		results <- msg
	})
	defer restoreSink()

	start := time.Now()
	app.openPicker(root, filepickerscreen.ModeFile)
	if elapsed := time.Since(start); elapsed > 100*time.Millisecond {
		t.Fatalf("initial picker open blocked for %s", elapsed)
	}
	if app.FilePicker == nil || !app.FilePicker.IsOpen() || !app.FilePicker.AsyncDirectoryLoading() {
		t.Fatal("picker did not open immediately in loading state")
	}
	filePickerWindow := app.windowManager.Window(string(ui.OverlayFilePicker))
	if filePickerWindow == nil {
		t.Fatal("picker window was not mounted")
	}
	filePickerWindow.Render(app.renderCtx)
	if filePickerWindow.IsContentDirty() {
		t.Fatal("picker window remained dirty after rendering its loading state")
	}
	select {
	case <-readStarted:
	case <-time.After(time.Second):
		t.Fatal("initial directory command did not start")
	}

	releaseOnce.Do(func() { close(releaseRead) })
	var result tea.Msg
	select {
	case result = <-results:
	case <-time.After(time.Second):
		t.Fatal("initial directory command did not finish")
	}
	_, _ = app.Update(result)
	if !filePickerWindow.IsContentDirty() {
		t.Fatal("async directory result did not invalidate the cached picker window")
	}
	if app.FilePicker.AsyncDirectoryLoading() {
		t.Fatal("initial directory result did not clear loading")
	}
	if got := app.FilePicker.Current(); got != root {
		t.Fatalf("initial directory = %q, want %q", got, root)
	}
}

func TestAsyncDirectoryErrorRestoresPreviousListing4f7c(t *testing.T) {
	app := newTestApp(t)
	t.Cleanup(func() { resetAsyncIOStateForTest(app) })

	root := t.TempDir()
	badPath := filepath.Join(root, "unreadable")
	app.FilePicker = filepickerscreen.New(app.Theme)
	app.FilePicker.Open(root, filepickerscreen.ModeFile, []filepickerscreen.Entry{
		{Name: "kept.go", Path: filepath.Join(root, "kept.go")},
	})

	restore := installAsyncDirectoryBrowserForTest(app, func(string) (workspace.DirectoryListing, error) {
		return workspace.DirectoryListing{}, errors.New("read denied")
	})
	defer restore()

	_, cmd := app.Update(pickerNavigateMsgForTest(t, app.FilePicker, badPath))
	if cmd == nil || !app.FilePicker.AsyncDirectoryLoading() {
		t.Fatal("failed navigation did not start")
	}
	_, _ = app.Update(cmd())
	if app.FilePicker.AsyncDirectoryLoading() {
		t.Fatal("directory error left picker loading")
	}
	if got := app.FilePicker.Current(); got != root {
		t.Fatalf("directory error left picker at %q, want %q", got, root)
	}
	if got := app.FilePicker.VisibleCount(); got != 1 {
		t.Fatalf("directory error discarded previous listing; rows=%d", got)
	}
}

func TestDirectoryInvalidationReleasesBlockedRequestOwner(t *testing.T) {
	app := newTestApp(t)
	t.Cleanup(func() { resetAsyncIOStateForTest(app) })
	root := t.TempDir()
	app.FilePicker = filepickerscreen.New(app.Theme)
	app.FilePicker.Open(root, filepickerscreen.ModeFile, nil)

	started := make(chan struct{})
	release := make(chan struct{})
	var once sync.Once
	defer close(release)
	restore := installAsyncDirectoryBrowserForTest(app, func(path string) (workspace.DirectoryListing, error) {
		once.Do(func() { close(started) })
		<-release
		return workspace.DirectoryListing{Path: path}, nil
	})
	defer restore()

	cmd := app.beginDirectoryAsyncLoad(app.FilePicker, root)
	results := make(chan tea.Msg, 1)
	go func() { results <- cmd() }()
	select {
	case <-started:
	case <-time.After(time.Second):
		t.Fatal("directory request did not start")
	}

	app.invalidateDirectoryAsyncLoad(app.FilePicker)
	select {
	case <-results:
	case <-time.After(200 * time.Millisecond):
		t.Fatal("invalidating a picker did not release its blocked request")
	}
	state := asyncIOStateFor(app)
	state.mu.Lock()
	_, pending := state.latestDirectory[app.FilePicker]
	_, cancelPending := state.directoryCancel[app.FilePicker]
	state.mu.Unlock()
	if pending || cancelPending {
		t.Fatalf("invalidated picker retained ownership: latest=%v cancel=%v", pending, cancelPending)
	}
}
