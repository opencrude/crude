package viewmodel

import (
	"testing"

	tea "charm.land/bubbletea/v2"

	filepickerscreen "crude/tui/screens/filepicker"
	"crude/tui/ui"
)

func resetAsyncIOStateForTest(m *App) {
	if m != nil {
		m.StopAsyncIO()
	}
}

func installAsyncClipboardBackendForTest(m *App, backend clipboardAsyncBackend) func() {
	state := asyncIOStateFor(m)
	state.mu.Lock()
	previous := state.clipboardBackend
	state.clipboardBackend = backend
	state.mu.Unlock()
	return func() {
		state.mu.Lock()
		state.clipboardBackend = previous
		state.mu.Unlock()
	}
}

func installAsyncDirectoryBrowserForTest(m *App, browser directoryAsyncBrowser) func() {
	state := asyncIOStateFor(m)
	state.mu.Lock()
	previous := state.directoryBrowser
	state.directoryBrowser = browser
	state.mu.Unlock()
	return func() {
		state.mu.Lock()
		state.directoryBrowser = previous
		state.mu.Unlock()
	}
}

func installDetachedMessageSinkForTest(m *App, sink func(tea.Msg)) func() {
	state := asyncIOStateFor(m)
	state.mu.Lock()
	previous := state.detachedMessageSink
	state.detachedMessageSink = sink
	state.mu.Unlock()
	return func() {
		state.mu.Lock()
		state.detachedMessageSink = previous
		state.mu.Unlock()
	}
}

func pickerNavigateMsgForTest(
	t *testing.T,
	target *filepickerscreen.Screen,
	path string,
) filepickerscreen.NavigateMsg {
	t.Helper()
	result := target.HandleAction(ui.EventContext{}, ui.Action{
		ID: "filepicker.entry.activate",
		Payload: ui.ListItem{Payload: filepickerscreen.Entry{
			Name:  path,
			Path:  path,
			IsDir: true,
		}},
	})
	if result.Cmd == nil {
		t.Fatal("file picker did not emit navigation intent")
	}
	raw := result.Cmd()
	msg, ok := raw.(filepickerscreen.NavigateMsg)
	if !ok {
		t.Fatalf("file picker navigation emitted %T", raw)
	}
	return msg
}
