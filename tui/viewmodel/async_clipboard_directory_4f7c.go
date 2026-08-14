package viewmodel

import (
	"encoding/base64"
	"errors"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"time"

	tea "charm.land/bubbletea/v2"

	"crude/clipboard"
	"crude/sessiondb"
	"crude/tui/editor"
	filepickerscreen "crude/tui/screens/filepicker"
	"crude/tui/ui"
	"crude/tui/ui/popup"
	"crude/workspace"
)

type clipboardAsyncOperation uint8

const (
	clipboardAsyncWriteText clipboardAsyncOperation = iota
	clipboardAsyncPasteEditor
	clipboardAsyncPasteInputText
	clipboardAsyncPasteInputImageFirst
	clipboardAsyncPasteFindReplace
)

type clipboardAsyncRequestMsg struct {
	operation   clipboardAsyncOperation
	generation  uint64
	result      <-chan clipboardAsyncResultMsg
	text        string
	editor      *editor.Editor
	session     *workspace.Session
	findReplace *popup.FindReplaceState
	textEdit    *ui.TextEdit
}

type clipboardAsyncResultMsg struct {
	request      clipboardAsyncRequestMsg
	content      string
	image        bool
	imageMIME    string
	imageBytes   int
	operationErr error
}

type clipboardAsyncKey struct {
	lane        clipboardAsyncOperation
	editor      *editor.Editor
	session     *workspace.Session
	findReplace *popup.FindReplaceState
	textEdit    *ui.TextEdit
}

type clipboardAsyncBackend struct {
	read      func(clipboard.Format) []byte
	writeText func(string) error
}

type clipboardAsyncJob struct {
	request clipboardAsyncRequestMsg
	backend clipboardAsyncBackend
	result  chan<- clipboardAsyncResultMsg
}

type clipboardAsyncImageStoredMsg struct {
	session    *workspace.Session
	store      *workspace.ReferenceStore
	ref        sessiondb.Reference
	imageBytes int
	err        error
}

type directoryAsyncResultMsg struct {
	target     *filepickerscreen.Screen
	generation uint64
	intent     filepickerscreen.IntentToken
	listing    workspace.DirectoryListing
	err        error
}

type directoryAsyncBrowser func(string) (workspace.DirectoryListing, error)

const directoryAsyncTimeout = 10 * time.Second

type appAsyncIOState struct {
	mu sync.Mutex

	nextClipboard       uint64
	latestClipboard     map[clipboardAsyncKey]uint64
	clipboardBackend    clipboardAsyncBackend
	clipboardJobs       []clipboardAsyncJob
	clipboardCond       *sync.Cond
	clipboardClosed     bool
	clipboardWorkerDone chan struct{}
	clipboardWorkerRun  bool
	nextDirectory       uint64
	latestDirectory     map[*filepickerscreen.Screen]uint64
	directoryCancel     map[*filepickerscreen.Screen]chan struct{}
	directoryBrowser    directoryAsyncBrowser
	detachedMessageSink func(tea.Msg)
}

func asyncIOStateFor(m *App) *appAsyncIOState {
	if state := m.asyncIO.Load(); state != nil {
		return state
	}
	state := &appAsyncIOState{
		latestClipboard: make(map[clipboardAsyncKey]uint64),
		clipboardBackend: clipboardAsyncBackend{
			read:      clipboard.Read,
			writeText: clipboard.WriteText,
		},
		latestDirectory:     make(map[*filepickerscreen.Screen]uint64),
		directoryCancel:     make(map[*filepickerscreen.Screen]chan struct{}),
		directoryBrowser:    workspace.BrowseDirectoryPath,
		clipboardWorkerDone: make(chan struct{}),
	}
	state.clipboardCond = sync.NewCond(&state.mu)
	if m.asyncIO.CompareAndSwap(nil, state) {
		state.mu.Lock()
		if m.asyncIOStopped.Load() {
			state.clipboardClosed = true
			state.mu.Unlock()
			return state
		}
		state.clipboardWorkerRun = true
		state.mu.Unlock()
		go state.runClipboardWorker()
		return state
	}
	return m.asyncIO.Load()
}

func (r clipboardAsyncRequestMsg) key() clipboardAsyncKey {
	switch r.operation {
	case clipboardAsyncPasteEditor:
		return clipboardAsyncKey{lane: clipboardAsyncPasteEditor, editor: r.editor}
	case clipboardAsyncPasteFindReplace:
		return clipboardAsyncKey{
			lane: clipboardAsyncPasteFindReplace, findReplace: r.findReplace, textEdit: r.textEdit,
		}
	case clipboardAsyncPasteInputText, clipboardAsyncPasteInputImageFirst:
		return clipboardAsyncKey{lane: clipboardAsyncPasteInputText, session: r.session}
	default:
		return clipboardAsyncKey{lane: clipboardAsyncWriteText}
	}
}

func (m *App) prepareClipboardAsyncRequest(request clipboardAsyncRequestMsg) clipboardAsyncRequestMsg {
	state := asyncIOStateFor(m)
	result := make(chan clipboardAsyncResultMsg, 1)
	state.mu.Lock()
	state.nextClipboard++
	request.generation = state.nextClipboard
	request.result = result
	state.latestClipboard[request.key()] = request.generation
	state.clipboardJobs = append(state.clipboardJobs, clipboardAsyncJob{
		request: request,
		backend: state.clipboardBackend,
		result:  result,
	})
	state.clipboardCond.Signal()
	state.mu.Unlock()
	return request
}

// enqueueClipboardAsyncRequest is the bridge for callbacks whose signature
// cannot return tea.Cmd. It only sends the request from a detached goroutine;
// the clipboard operation itself starts when Update returns the request's Cmd.
func (m *App) enqueueClipboardAsyncRequest(request clipboardAsyncRequestMsg) {
	request = m.prepareClipboardAsyncRequest(request)
	m.runDetachedAsyncCmd(func() tea.Msg { return request })
}

func (m *App) clipboardAsyncCommand(request clipboardAsyncRequestMsg) tea.Cmd {
	if request.generation == 0 || request.result == nil {
		return nil
	}
	return func() tea.Msg {
		return <-request.result
	}
}

func (s *appAsyncIOState) runClipboardWorker() {
	defer close(s.clipboardWorkerDone)
	for {
		s.mu.Lock()
		for len(s.clipboardJobs) == 0 && !s.clipboardClosed {
			s.clipboardCond.Wait()
		}
		if len(s.clipboardJobs) == 0 {
			s.mu.Unlock()
			return
		}
		job := s.clipboardJobs[0]
		s.clipboardJobs[0] = clipboardAsyncJob{}
		s.clipboardJobs = s.clipboardJobs[1:]
		latest := s.latestClipboard[job.request.key()]
		s.mu.Unlock()

		result := clipboardAsyncResultMsg{request: job.request}
		if job.request.generation == latest {
			result = executeClipboardAsyncRequest(job.backend, job.request)
		}
		job.result <- result
		close(job.result)
	}
}

func executeClipboardAsyncRequest(
	backend clipboardAsyncBackend,
	request clipboardAsyncRequestMsg,
) clipboardAsyncResultMsg {
	result := clipboardAsyncResultMsg{request: request}
	switch request.operation {
	case clipboardAsyncWriteText:
		result.operationErr = backend.writeText(request.text)
	case clipboardAsyncPasteEditor, clipboardAsyncPasteInputText, clipboardAsyncPasteFindReplace:
		data := backend.read(clipboard.FmtText)
		if len(data) == 0 {
			result.operationErr = errors.New("clipboard is empty or unavailable")
		} else {
			result.content = string(data)
		}
	case clipboardAsyncPasteInputImageFirst:
		data := backend.read(clipboard.FmtImage)
		if len(data) == 0 {
			data = backend.read(clipboard.FmtText)
			if len(data) == 0 {
				result.operationErr = errors.New("clipboard is empty or unavailable")
			} else {
				result.content = string(data)
			}
			break
		}
		result.image = true
		result.imageBytes = len(data)
		result.imageMIME = strings.SplitN(http.DetectContentType(data), ";", 2)[0]
		switch result.imageMIME {
		case "image/png", "image/jpeg", "image/gif", "image/webp":
			result.content = base64.StdEncoding.EncodeToString(data)
		default:
			result.operationErr = errors.New("clipboard image format is not supported")
		}
	}
	return result
}

func (m *App) applyClipboardAsyncResult(result clipboardAsyncResultMsg) tea.Cmd {
	request := result.request
	if !m.consumeClipboardAsyncRequest(request) {
		return nil
	}

	switch request.operation {
	case clipboardAsyncWriteText:
		if result.operationErr != nil {
			m.setClipboardAsyncError("clipboard error: " + result.operationErr.Error())
			return nil
		}
		if m.StatusBar != nil {
			m.StatusBar.SetInfo("Copied " + strconv.Itoa(len(request.text)) + " chars")
		}
	case clipboardAsyncPasteEditor:
		if !m.ownsClipboardTargetEditor(request.editor) {
			return nil
		}
		if result.operationErr != nil {
			m.setClipboardAsyncError(result.operationErr.Error())
			return nil
		}
		request.editor.HandleEvent(tea.PasteMsg{Content: result.content})
		if m.StatusBar != nil {
			m.StatusBar.SetInfo("Pasted " + strconv.Itoa(len(result.content)) + " chars")
		}
	case clipboardAsyncPasteFindReplace:
		if !m.ownsClipboardFindReplaceTarget(request.findReplace, request.textEdit) {
			return nil
		}
		if result.operationErr != nil {
			m.setClipboardAsyncError(result.operationErr.Error())
			return nil
		}
		request.textEdit.Insert(result.content)
		if request.findReplace.OnChange != nil {
			request.findReplace.OnChange()
		}
	case clipboardAsyncPasteInputText, clipboardAsyncPasteInputImageFirst:
		// The chat input is a shared projection of ActiveSession. If the
		// session changed while the OS clipboard call was in flight, there is
		// no safe inactive draft to mutate, so discard the stale result.
		if m.ActiveSession != request.session {
			return nil
		}
		if result.operationErr != nil {
			m.setClipboardAsyncError(result.operationErr.Error())
			return nil
		}
		if request.session != nil && request.session.RefStore != nil {
			m.Input.SetReferenceStore(request.session.RefStore)
		} else {
			m.Input.SetReferenceStore(nil)
		}
		if result.image {
			return m.persistClipboardAsyncImageCmd(request.session, result)
		}
		blocking := m.OverlayState != nil && m.OverlayState.IsBlocking()
		updated, _, _ := m.Input.Update(
			tea.PasteMsg{Content: result.content},
			!blocking,
			blocking,
		)
		m.Input = updated
		if m.StatusBar != nil {
			m.StatusBar.SetInfo("Pasted " + strconv.Itoa(len(result.content)) + " chars into input")
		}
	}
	return nil
}

func (m *App) beginEditorClipboardPaste(target *editor.Editor) tea.Cmd {
	if target == nil {
		return nil
	}
	request := m.prepareClipboardAsyncRequest(clipboardAsyncRequestMsg{
		operation: clipboardAsyncPasteEditor,
		editor:    target,
	})
	return m.clipboardAsyncCommand(request)
}

func (m *App) beginFindReplaceClipboardPaste(
	findReplace *popup.FindReplaceState,
	textEdit *ui.TextEdit,
) tea.Cmd {
	if findReplace == nil || textEdit == nil {
		return nil
	}
	request := m.prepareClipboardAsyncRequest(clipboardAsyncRequestMsg{
		operation:   clipboardAsyncPasteFindReplace,
		findReplace: findReplace,
		textEdit:    textEdit,
	})
	return m.clipboardAsyncCommand(request)
}

func (m *App) consumeClipboardAsyncRequest(request clipboardAsyncRequestMsg) bool {
	state := asyncIOStateFor(m)
	state.mu.Lock()
	defer state.mu.Unlock()
	key := request.key()
	if request.generation == 0 || state.latestClipboard[key] != request.generation {
		return false
	}
	delete(state.latestClipboard, key)
	return true
}

func (m *App) persistClipboardAsyncImageCmd(
	session *workspace.Session,
	result clipboardAsyncResultMsg,
) tea.Cmd {
	if session == nil || session.RefStore == nil {
		return nil
	}
	store := session.RefStore
	return func() tea.Msg {
		ref, err := store.CreateReference(sessiondb.CreateRequest{
			Kind:    sessiondb.KindPastedImage,
			Title:   "Clipboard image",
			Mime:    result.imageMIME,
			Content: result.content,
		})
		return clipboardAsyncImageStoredMsg{
			session:    session,
			store:      store,
			ref:        ref,
			imageBytes: result.imageBytes,
			err:        err,
		}
	}
}

func (m *App) applyClipboardAsyncImageStored(result clipboardAsyncImageStoredMsg) {
	if result.session == nil || result.store == nil ||
		m.ActiveSession != result.session ||
		result.session.RefStore != result.store {
		return
	}
	if result.err != nil {
		m.setClipboardAsyncError("image paste failed: " + result.err.Error())
		return
	}
	m.Input.SetReferenceStore(result.store)
	m.Input.InsertReference(result.ref)
	if m.StatusBar != nil {
		kilobytes := (result.imageBytes + 1023) / 1024
		m.StatusBar.SetInfo("Attached clipboard image (" + strconv.Itoa(kilobytes) + " KB)")
	}
}

func (m *App) ownsClipboardTargetEditor(target *editor.Editor) bool {
	if target == nil || m.EditorState == nil {
		return false
	}
	if m.EditorState.Editor == target {
		return true
	}
	for _, tab := range m.EditorState.Tabs {
		if tab != nil && tab.Editor == target {
			return true
		}
	}
	return false
}

func (m *App) ownsClipboardFindReplaceTarget(
	findReplace *popup.FindReplaceState,
	textEdit *ui.TextEdit,
) bool {
	if findReplace == nil || textEdit == nil || m.FindReplace != findReplace {
		return false
	}
	if m.windowManager == nil ||
		m.windowManager.Window(string(ui.OverlayFindReplace)) == nil {
		return false
	}
	return textEdit == findReplace.FindEdit() || textEdit == findReplace.ReplaceEdit()
}

func (m *App) invalidateClipboardFindReplace(findReplace *popup.FindReplaceState) {
	if findReplace == nil {
		return
	}
	state := asyncIOStateFor(m)
	state.mu.Lock()
	for key := range state.latestClipboard {
		if key.lane == clipboardAsyncPasteFindReplace && key.findReplace == findReplace {
			delete(state.latestClipboard, key)
		}
	}
	state.mu.Unlock()
}

func (m *App) setClipboardAsyncError(message string) {
	if m.StatusBar != nil {
		m.StatusBar.SetError(message)
	}
}

func (m *App) beginDirectoryAsyncLoad(target *filepickerscreen.Screen, path string) tea.Cmd {
	return m.beginDirectoryAsyncLoadFor(target, path, filepickerscreen.IntentToken{})
}

func (m *App) beginDirectoryAsyncLoadFor(
	target *filepickerscreen.Screen,
	path string,
	intent filepickerscreen.IntentToken,
) tea.Cmd {
	if target == nil {
		return nil
	}
	if intent.Epoch == 0 {
		intent = target.BeginAsyncDirectoryLoad(path)
		if intent.Epoch == 0 {
			return nil
		}
	} else if !target.BeginAsyncDirectoryLoadFor(path, intent) {
		return nil
	}
	state := asyncIOStateFor(m)
	state.mu.Lock()
	state.nextDirectory++
	generation := state.nextDirectory
	if cancel := state.directoryCancel[target]; cancel != nil {
		close(cancel)
	}
	cancel := make(chan struct{})
	state.latestDirectory[target] = generation
	state.directoryCancel[target] = cancel
	browse := state.directoryBrowser
	state.mu.Unlock()

	return func() tea.Msg {
		state.mu.Lock()
		latest := state.latestDirectory[target]
		state.mu.Unlock()
		if generation != latest {
			return directoryAsyncResultMsg{
				target: target, generation: generation, intent: intent,
			}
		}
		type browseResult struct {
			listing workspace.DirectoryListing
			err     error
		}
		done := make(chan browseResult, 1)
		go func() {
			listing, err := browse(path)
			done <- browseResult{listing: listing, err: err}
		}()
		timer := time.NewTimer(directoryAsyncTimeout)
		defer timer.Stop()
		select {
		case result := <-done:
			return directoryAsyncResultMsg{
				target: target, generation: generation, intent: intent,
				listing: result.listing, err: result.err,
			}
		case <-cancel:
			return directoryAsyncResultMsg{target: target, generation: generation, intent: intent}
		case <-timer.C:
			return directoryAsyncResultMsg{
				target:     target,
				generation: generation,
				intent:     intent,
				err:        errors.New("directory read timed out"),
			}
		}
	}
}

func (m *App) applyDirectoryAsyncResult(result directoryAsyncResultMsg) {
	if result.target == nil {
		return
	}
	state := asyncIOStateFor(m)
	state.mu.Lock()
	latest := state.latestDirectory[result.target]
	if result.generation != 0 && result.generation == latest {
		delete(state.latestDirectory, result.target)
		delete(state.directoryCancel, result.target)
	}
	state.mu.Unlock()
	if result.generation == 0 || result.generation != latest {
		return
	}
	if result.target != m.FilePicker || !result.target.IsOpen() {
		result.target.CancelAsyncDirectoryLoadFor(result.intent)
		return
	}
	if result.err != nil {
		result.target.FailAsyncDirectoryLoadFor(result.intent, result.err)
		m.markFilePickerDirty()
		if m.StatusBar != nil {
			m.StatusBar.SetError(result.err.Error())
		}
		return
	}
	result.target.CompleteAsyncDirectoryLoadFor(
		result.intent,
		result.listing.Path,
		pickerEntries(result.listing.Entries),
	)
	m.markFilePickerDirty()
}

func (m *App) markFilePickerDirty() {
	if m == nil || m.windowManager == nil {
		return
	}
	if fw := m.windowManager.Window(string(ui.OverlayFilePicker)); fw != nil {
		fw.MarkContentDirty()
	}
}

func (m *App) invalidateDirectoryAsyncLoad(target *filepickerscreen.Screen) {
	if target == nil {
		return
	}
	state := asyncIOStateFor(m)
	state.mu.Lock()
	state.nextDirectory++
	delete(state.latestDirectory, target)
	if cancel := state.directoryCancel[target]; cancel != nil {
		close(cancel)
		delete(state.directoryCancel, target)
	}
	state.mu.Unlock()
	target.CancelAsyncDirectoryLoad()
}

func (m *App) runDetachedAsyncCmd(cmd tea.Cmd) {
	if cmd == nil {
		return
	}
	go func() {
		msg := cmd()
		if msg == nil {
			return
		}
		if m.asyncIOStopped.Load() {
			return
		}
		state := asyncIOStateFor(m)
		state.mu.Lock()
		sink := state.detachedMessageSink
		state.mu.Unlock()
		if sink != nil {
			sink(msg)
			return
		}
		if m.eventBus != nil {
			m.eventBus.Send(msg)
		}
	}()
}

// StopAsyncIO releases app-owned background workers after the Bubble Tea
// program exits. Pending clipboard jobs are drained in FIFO order so commands
// already waiting for their result can finish without a goroutine leak.
func (m *App) StopAsyncIO() {
	if m == nil {
		return
	}
	m.asyncIOStopped.Store(true)
	state := m.asyncIO.Swap(nil)
	if state == nil {
		return
	}
	state.mu.Lock()
	state.clipboardClosed = true
	workerRunning := state.clipboardWorkerRun
	for target, cancel := range state.directoryCancel {
		close(cancel)
		delete(state.directoryCancel, target)
	}
	if state.clipboardCond != nil {
		state.clipboardCond.Broadcast()
	}
	state.mu.Unlock()
	if workerRunning {
		<-state.clipboardWorkerDone
	}
}
