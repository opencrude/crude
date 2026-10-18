package viewmodel

import (
	"context"
	"errors"
	"net/http"
	"os"
	"strconv"
	"strings"
	"sync"
	"time"

	tea "charm.land/bubbletea/v2"

	"crude/clipboard"
	"crude/sessiondb"
	"crude/shellport"
	"crude/tui/editor"
	"crude/tui/rendering"
	filepickerscreen "crude/tui/screens/filepicker"
	"crude/tui/ui"
	"crude/workspace"
)

type clipboardAsyncOperation uint8

const (
	clipboardAsyncWriteText clipboardAsyncOperation = iota
	clipboardAsyncPasteEditor
	clipboardAsyncPasteInputText
	clipboardAsyncPasteInputImageFirst
	clipboardAsyncPasteInputImage
	clipboardAsyncPasteNative
)

type clipboardAsyncRequestMsg struct {
	operation           clipboardAsyncOperation
	generation          uint64
	result              <-chan clipboardAsyncResultMsg
	text                string
	editor              *editor.Editor
	session             *workspace.Session
	inputEpoch          uint64
	overlayEpoch        uint64
	workspaceGeneration uint64
	nativePaste         ui.ClipboardPasteTarget
	// nativeDeliver re-enters the clipboard text through the component that
	// requested it (owner-keyed; it performs its own liveness checks), so
	// composite controls observe Ctrl+V exactly like a bracketed paste.
	nativeDeliver func(string) tea.Cmd
	overlayID     string
}

type clipboardAsyncResultMsg struct {
	request      clipboardAsyncRequestMsg
	content      string
	image        bool
	imageMIME    string
	imageBytes   int
	imageData    []byte
	operationErr error
}

type clipboardAsyncKey struct {
	lane    clipboardAsyncOperation
	editor  *editor.Editor
	session *workspace.Session
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
	store      workspace.SessionReferences
	ref        sessiondb.Reference
	imageBytes int
	err        error
	inputEpoch uint64
	generation uint64
}

type directoryAsyncResultMsg struct {
	target     *filepickerscreen.Screen
	generation uint64
	intent     filepickerscreen.IntentToken
	listing    workspace.DirectoryListing
	expand     bool
	err        error
}

type directoryAsyncBrowser func(string) (workspace.DirectoryListing, error)

const directoryAsyncTimeout = 10 * time.Second

type appAsyncIOState struct {
	mu sync.Mutex

	nextClipboard           uint64
	latestClipboard         map[clipboardAsyncKey]uint64
	clipboardBackend        clipboardAsyncBackend
	clipboardJobs           []clipboardAsyncJob
	clipboardCond           *sync.Cond
	clipboardClosed         bool
	clipboardWorkerDone     chan struct{}
	clipboardWorkerRun      bool
	nextDirectory           uint64
	latestDirectory         map[*filepickerscreen.Screen]uint64
	directoryCancel         map[*filepickerscreen.Screen]chan struct{}
	directoryBrowser        directoryAsyncBrowser
	nextPathSearch          uint64
	latestPathSearch        map[*filepickerscreen.Screen]uint64
	pathSearchCancel        map[*filepickerscreen.Screen]context.CancelFunc
	pathSearchRoot          map[*filepickerscreen.Screen]string
	pathSearchDirsOnly      map[*filepickerscreen.Screen]bool
	pathSearchHidden        map[*filepickerscreen.Screen]bool
	pathSearchCatalog       map[*filepickerscreen.Screen]workspace.BrowserCatalog
	pathSearchRunning       map[*filepickerscreen.Screen]bool
	pathSearchQuery         map[*filepickerscreen.Screen]string
	pathSearchIntent        map[*filepickerscreen.Screen]filepickerscreen.IntentToken
	treePathSearch          *treePathSearchState
	treeControlSequence     uint64
	detachedMessageSink     func(tea.Msg)
	desktopCaches           []string
	editorRecoveryRevisions map[string]uint64
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
		latestPathSearch:    make(map[*filepickerscreen.Screen]uint64),
		pathSearchCancel:    make(map[*filepickerscreen.Screen]context.CancelFunc),
		pathSearchRoot:      make(map[*filepickerscreen.Screen]string),
		pathSearchDirsOnly:  make(map[*filepickerscreen.Screen]bool),
		pathSearchHidden:    make(map[*filepickerscreen.Screen]bool),
		pathSearchCatalog:   make(map[*filepickerscreen.Screen]workspace.BrowserCatalog),
		pathSearchRunning:   make(map[*filepickerscreen.Screen]bool),
		pathSearchQuery:     make(map[*filepickerscreen.Screen]string),
		pathSearchIntent:    make(map[*filepickerscreen.Screen]filepickerscreen.IntentToken),
		treePathSearch:      newTreePathSearchState(),
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
	case clipboardAsyncPasteInputText, clipboardAsyncPasteInputImageFirst, clipboardAsyncPasteInputImage:
		return clipboardAsyncKey{lane: clipboardAsyncPasteInputText, session: r.session}
	case clipboardAsyncPasteNative:
		return clipboardAsyncKey{lane: clipboardAsyncPasteNative}
	default:
		return clipboardAsyncKey{lane: clipboardAsyncWriteText}
	}
}

func (m *App) prepareClipboardAsyncRequest(request clipboardAsyncRequestMsg) clipboardAsyncRequestMsg {
	request.workspaceGeneration = m.workspaceSwitchGen.Load()
	if request.session != nil {
		request.inputEpoch = m.Input.DraftEpoch()
	}
	if request.overlayID == string(ui.OverlayTextOverlay) {
		if screen := m.sessionTextOverlay(); screen != nil {
			request.overlayEpoch = screen.OpenEpoch()
		}
	}
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
	case clipboardAsyncPasteEditor, clipboardAsyncPasteInputText, clipboardAsyncPasteNative:
		data := backend.read(clipboard.FmtText)
		if len(data) == 0 {
			result.operationErr = errors.New(ui.ClipboardReadUnavailable())
		} else {
			result.content = string(data)
		}
	case clipboardAsyncPasteInputImageFirst, clipboardAsyncPasteInputImage:
		data := backend.read(clipboard.FmtImage)
		if len(data) == 0 {
			// An empty terminal paste is evidence of an image-only gesture,
			// not permission to insert unrelated system-clipboard text.
			if request.operation == clipboardAsyncPasteInputImage {
				break
			}
			data = backend.read(clipboard.FmtText)
			if len(data) == 0 {
				result.operationErr = errors.New(ui.ClipboardReadUnavailable())
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
			result.imageData = data
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
		// An unreachable or remote (SSH) system clipboard falls back to the
		// OSC 52 terminal clipboard, as rendering.CopyToClipboardCmd does.
		if result.operationErr != nil || rendering.RemoteTerminalSession() {
			if m.Feedback != nil {
				m.Feedback.NotifyInfo("Copied " + strconv.Itoa(len(request.text)) + " chars via the terminal clipboard")
			}
			return tea.SetClipboard(request.text)
		}
		if m.Feedback != nil {
			m.Feedback.NotifyInfo("Copied " + strconv.Itoa(len(request.text)) + " chars")
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
		if m.Feedback != nil {
			m.Feedback.NotifyInfo("Pasted " + strconv.Itoa(len(result.content)) + " chars")
		}
	case clipboardAsyncPasteNative:
		if request.nativePaste == nil || !request.nativePaste.PasteActive() {
			return nil
		}
		if request.overlayID != "" && m.windowManager.Window(request.overlayID) == nil {
			return nil
		}
		if request.overlayID == string(ui.OverlayTextOverlay) {
			if screen := m.sessionTextOverlay(); screen == nil || screen.OpenEpoch() != request.overlayEpoch {
				return nil
			}
		}
		if result.operationErr != nil {
			m.setClipboardAsyncError(result.operationErr.Error())
			return nil
		}
		if request.nativeDeliver != nil {
			return request.nativeDeliver(result.content)
		}
		request.nativePaste.PasteText(result.content)
	case clipboardAsyncPasteInputText, clipboardAsyncPasteInputImageFirst, clipboardAsyncPasteInputImage:
		// The chat input is a shared projection of ActiveSession. If the
		// session changed while the OS clipboard call was in flight, there is
		// no safe inactive draft to mutate, so discard the stale result.
		if m.ActiveSession != request.session || m.Input.DraftEpoch() != request.inputEpoch || request.workspaceGeneration != m.workspaceSwitchGen.Load() {
			return nil
		}
		if result.operationErr != nil {
			m.setClipboardAsyncError(result.operationErr.Error())
			return nil
		}
		if request.operation == clipboardAsyncPasteInputImage && !result.image {
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
		updated, command, event := m.Input.Update(
			tea.PasteMsg{Content: result.content},
			!blocking,
			blocking,
		)
		m.Input = updated
		m.persistComposerDraftIfChanged()
		if event != nil {
			return tea.Batch(append([]tea.Cmd{command}, m.handleInputEvent(event)...)...)
		}
		if m.Feedback != nil && command == nil {
			m.Feedback.NotifyInfo("Pasted " + strconv.Itoa(len(result.content)) + " chars into input")
		}
		return command
	}
	return nil
}

func (m *App) beginInputClipboardPaste(operation clipboardAsyncOperation) tea.Cmd {
	if m.ActiveSession == nil || m.ActiveSession.RefStore == nil {
		return nil
	}
	request := m.prepareClipboardAsyncRequest(clipboardAsyncRequestMsg{
		operation: operation,
		session:   m.ActiveSession,
	})
	return m.clipboardAsyncCommand(request)
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

// beginNativeClipboardPaste starts the Ctrl+V system-clipboard read for a
// text control. deliver, when set, re-enters the text through the control's
// owner (see nativePasteDelivery); otherwise the text goes to target itself.
func (m *App) beginNativeClipboardPaste(target ui.ClipboardPasteTarget, overlayID string, deliver func(string) tea.Cmd) tea.Cmd {
	if target == nil || !target.PasteActive() {
		return nil
	}
	request := m.prepareClipboardAsyncRequest(clipboardAsyncRequestMsg{
		operation:     clipboardAsyncPasteNative,
		nativePaste:   target,
		nativeDeliver: deliver,
		overlayID:     overlayID,
	})
	return m.clipboardAsyncCommand(request)
}

// nativePasteDelivery binds a Ctrl+V result to the mounted component that
// requested it: the focused component of one of hosts whose text editor is
// target (a search field, filter, form, composite write-in). The result is
// delivered to that exact component as a tea.PasteMsg through its own Handle,
// so debounced searches, change callbacks and form state all observe it. A
// replaced or unmounted owner drops the late result. When no composite owner
// is found the target receives the text directly.
func nativePasteDelivery(target ui.ClipboardPasteTarget, after func(ui.EventResult) tea.Cmd, hosts ...*ui.Host) func(string) tea.Cmd {
	for _, host := range hosts {
		if host == nil {
			continue
		}
		id := host.Focused()
		owner := host.Component(id)
		if owner == nil || !pasteTargetOf(owner, target) {
			continue
		}
		return func(content string) tea.Cmd {
			if host.Component(id) != owner || !pasteTargetOf(owner, target) {
				return nil
			}
			result := host.Deliver(id, tea.PasteMsg{Content: content})
			if after != nil {
				return after(result)
			}
			return result.Cmd
		}
	}
	return nil
}

// pasteTargetOf reports whether component is, or currently edits through,
// the paste target.
func pasteTargetOf(component ui.Component, target ui.ClipboardPasteTarget) bool {
	if any(component) == any(target) {
		return true
	}
	editor := ui.TextClipboardTargetIn(component)
	return editor != nil && any(editor) == any(target)
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
	inputEpoch, generation := m.Input.DraftEpoch(), m.workspaceSwitchGen.Load()
	return func() tea.Msg {
		ref, err := store.CreateReference(sessiondb.CreateRequest{
			Kind:  sessiondb.KindPastedImage,
			Title: "Clipboard image",
			Mime:  result.imageMIME,
			Data:  result.imageData,
		})
		return clipboardAsyncImageStoredMsg{
			session:    session,
			store:      store,
			ref:        ref,
			imageBytes: result.imageBytes,
			err:        err,
			inputEpoch: inputEpoch,
			generation: generation,
		}
	}
}

func (m *App) applyClipboardAsyncImageStored(result clipboardAsyncImageStoredMsg) {
	if result.session == nil || result.store == nil ||
		m.ActiveSession != result.session ||
		result.session.RefStore != result.store {
		return
	}
	if result.inputEpoch != m.Input.DraftEpoch() || result.generation != m.workspaceSwitchGen.Load() {
		return
	}
	if result.err != nil {
		m.setClipboardAsyncError("image paste failed: " + result.err.Error())
		return
	}
	m.Input.SetReferenceStore(result.store)
	m.Input.InsertReference(result.ref)
	m.persistComposerDraftIfChanged()
	if m.Feedback != nil {
		kilobytes := (result.imageBytes + 1023) / 1024
		m.Feedback.NotifyInfo("Attached clipboard image (" + strconv.Itoa(kilobytes) + " KB)")
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

func (m *App) setClipboardAsyncError(message string) {
	if m.Feedback != nil {
		m.Feedback.NotifyError(message)
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
	return m.beginDirectoryAsyncLoadInternal(target, path, intent, false)
}

func (m *App) beginDirectoryAsyncExpandFor(
	target *filepickerscreen.Screen,
	path string,
	intent filepickerscreen.IntentToken,
) tea.Cmd {
	return m.beginDirectoryAsyncLoadInternal(target, path, intent, true)
}

func (m *App) beginDirectoryAsyncLoadInternal(
	target *filepickerscreen.Screen,
	path string,
	intent filepickerscreen.IntentToken,
	expand bool,
) tea.Cmd {
	if target == nil {
		return nil
	}
	if expand {
		if intent.Epoch == 0 || !target.BeginAsyncDirectoryExpandFor(path, intent) {
			return nil
		}
	} else {
		if intent.Epoch == 0 {
			intent = target.BeginAsyncDirectoryLoad(path)
			if intent.Epoch == 0 {
				return nil
			}
		} else if !target.BeginAsyncDirectoryLoadFor(path, intent) {
			return nil
		}
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
	files, remote := m.ws.(shellport.ClientFiles)

	return func() tea.Msg {
		state.mu.Lock()
		latest := state.latestDirectory[target]
		state.mu.Unlock()
		if generation != latest {
			return directoryAsyncResultMsg{
				target: target, generation: generation, intent: intent, expand: expand,
			}
		}
		type browseResult struct {
			listing workspace.DirectoryListing
			err     error
		}
		done := make(chan browseResult, 1)
		go func() {
			var listing workspace.DirectoryListing
			var err error
			if remote {
				ctx, stop := context.WithTimeout(context.Background(), directoryAsyncTimeout)
				defer stop()
				go func() {
					select {
					case <-cancel:
						stop()
					case <-ctx.Done():
					}
				}()
				entries, readErr := files.BrowseExternalDirectory(ctx, path)
				listing, err = clientDirectoryListing(files.CanonicalClientPath(path), entries), readErr
			} else {
				listing, err = browse(path)
			}
			done <- browseResult{listing: listing, err: err}
		}()
		timer := time.NewTimer(directoryAsyncTimeout)
		defer timer.Stop()
		select {
		case result := <-done:
			return directoryAsyncResultMsg{
				target: target, generation: generation, intent: intent,
				expand: expand, listing: result.listing, err: result.err,
			}
		case <-cancel:
			return directoryAsyncResultMsg{target: target, generation: generation, intent: intent, expand: expand}
		case <-timer.C:
			return directoryAsyncResultMsg{
				target:     target,
				generation: generation,
				intent:     intent,
				expand:     expand,
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
		if m.Feedback != nil {
			m.Feedback.NotifyError(result.err.Error())
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
		if frame, ok := fw.Content.(*ui.WindowFrameWidget); ok && frame != nil && frame.Host() != nil {
			// Directory/search result counts can change whether the scrollbar
			// occupies a column. Content invalidation alone does not rebuild the
			// Host hit/layout tree.
			frame.Host().MarkLayout()
		}
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

// StopAsyncIO releases app-owned background workers and editor/tree watchers
// after the Bubble Tea program exits. It is idempotent. Pending clipboard jobs are drained in FIFO order so commands
// already waiting for their result can finish without a goroutine leak.
func (m *App) StopAsyncIO() {
	if m == nil {
		return
	}
	m.asyncIOStopOnce.Do(m.stopAsyncIO)
}

func (m *App) stopAsyncIO() {
	m.stopRemoteWorkspaceSwitch()
	if m.projectReplace != nil && m.projectReplace.cancel != nil {
		m.projectReplace.cancel()
		if m.projectReplace.start != nil {
			m.projectReplace.start()
		}
		if m.projectReplace.done != nil {
			<-m.projectReplace.done
		}
	}
	if m.workspaceListCancel != nil {
		m.workspaceListCancel()
		m.workspaceListCancel = nil
	}
	if port, ok := m.ws.(shellport.CapabilityHost); ok {
		port.StopCapabilityHost()
	}
	if port, ok := m.ws.(shellport.ShellControl); ok {
		port.StopShellControl()
	}
	// Exclusive post-program barrier: never turn an unresolved flush into a
	// successful detach merely by closing its projection.
	retainedEditors := m.retainEditorRecovery(m.FlushLiveEditors())
	m.flushComposerDraft()
	m.resetMemoryBrowser()
	if m.overlayHost != nil {
		m.overlayHost.Close(ui.OverlayCapabilityPicker)
	}
	m.cancelCapabilityOperations()
	m.cancelLocalDiffs()
	// The UI, not Workspace, owns editor and tree watcher lifetimes. Closing
	// only the domain leaves fsnotify handles alive across another shell/test.
	// Reject new starts before joining accepted recursive watcher construction.
	m.treeWatcherLifeMu.Lock()
	m.asyncIOStopped.Store(true)
	m.workspaceSwitchGen.Add(1)
	m.treeWatcherLifeMu.Unlock()
	m.treeWatcherStarts.Wait()
	if m.Tree != nil && m.Tree.Model != nil {
		m.Tree.Model.SetOnFileChanged(nil)
		m.Tree.Model.SetOnGitChanged(nil)
		m.Tree.Model.StopWatcher()
	}
	if len(retainedEditors) == 0 {
		m.closeWorkspaceEditors()
	} else if m.EditorState != nil {
		seen := make(map[*editor.Editor]bool)
		closeEditor := func(ed *editor.Editor) {
			if ed == nil || seen[ed] || retainedEditors[ed] {
				return
			}
			seen[ed] = true
			ed.SetOnContentChanged(nil)
			ed.Close()
		}
		for _, tab := range m.EditorState.Tabs {
			if tab != nil {
				closeEditor(tab.Editor)
			}
		}
		closeEditor(m.EditorState.Editor)
	}
	state := m.asyncIO.Swap(nil)
	if state == nil {
		return
	}
	state.mu.Lock()
	state.clipboardClosed = true
	caches := state.desktopCaches
	state.desktopCaches = nil
	workerRunning := state.clipboardWorkerRun
	for target, cancel := range state.directoryCancel {
		close(cancel)
		delete(state.directoryCancel, target)
	}
	for target, cancel := range state.pathSearchCancel {
		cancel()
		delete(state.pathSearchCancel, target)
	}
	for target := range state.pathSearchRunning {
		delete(state.pathSearchRunning, target)
		delete(state.pathSearchCatalog, target)
		delete(state.pathSearchRoot, target)
		delete(state.pathSearchDirsOnly, target)
		delete(state.pathSearchHidden, target)
		delete(state.pathSearchQuery, target)
		delete(state.pathSearchIntent, target)
	}
	if state.treePathSearch != nil {
		for target, cancel := range state.treePathSearch.cancel {
			cancel()
			delete(state.treePathSearch.cancel, target)
		}
		state.treePathSearch.clear()
	}
	if state.clipboardCond != nil {
		state.clipboardCond.Broadcast()
	}
	state.mu.Unlock()
	for _, cache := range caches {
		_ = os.RemoveAll(cache)
	}
	if workerRunning {
		<-state.clipboardWorkerDone
	}
}
