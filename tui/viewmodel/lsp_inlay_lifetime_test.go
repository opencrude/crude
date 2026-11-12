package viewmodel

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
)

func TestInlayPeerFocusKeepsTheOtherFileHintsAndEditing(t *testing.T) {
	f := newInlayConnectedFixture(t, true, inlayConnectedAnswer)
	plain := filepath.Join(filepath.Dir(f.engine.main), "notes.txt")
	if err := os.WriteFile(plain, []byte("no provider here\n"), 0600); err != nil {
		t.Fatal(err)
	}
	liveTestOpen(t, f.shells[1].app, plain)
	remoteJourneyUntil(t, f.shells, func() bool {
		lsp := f.shells[0].app.ws.LSPMgr()
		return !lsp.ServerReady() && lsp.FileProducer(f.engine.main).InstanceID != ""
	})
	remoteJourneyUntil(t, f.shells, func() bool { return f.main.HasInlayProjection() })
	f.main.SetCursorPosition(2, 13)
	renameConnectedKey(t, f.shells[0], tea.KeyPressMsg{Code: 'X', Text: "X"})
	remoteJourneyUntil(t, f.shells, func() bool {
		return strings.Contains(f.main.Value(), "1X") && f.peer.Value() == f.main.Value() && f.main.HasInlayProjection()
	})
	renameConnectedKey(t, f.shells[0], tea.KeyPressMsg{Code: 'z', Mod: tea.ModCtrl})
	remoteJourneyUntil(t, f.shells, func() bool {
		return f.main.Value() == inlayConnectedSource && f.peer.Value() == inlayConnectedSource && f.main.HasInlayProjection()
	})
	if f.shells[0].app.Input.Value() != "private first inlay draft" || f.shells[1].app.EditorState.Editor.Value() != "no provider here\n" {
		t.Fatal("peer focus altered another draft or file")
	}
	if disk, err := os.ReadFile(f.engine.main); err != nil || string(disk) != inlayConnectedSource {
		t.Fatal("inlay interaction wrote disk", err)
	}
}

// Native Close Tab and a normal reopen must keep the peer's source and hints
// intact, then restore fresh hints without a second shared document owner.
func TestInlayCloseAndReopenWhilePeerKeepsFile(t *testing.T) {
	f := newInlayConnectedFixture(t, true, inlayConnectedAnswer)
	one := f.shells[0]
	renameConnectedKey(t, one, tea.KeyPressMsg{Code: 'w', Mod: tea.ModCtrl})
	runAppCommand(t, one.app, one.app.drainLiveEditorCommands())
	if one.app.EditorState.Tabs[f.engine.main] != nil {
		t.Fatal("native Close Tab left the file open")
	}
	if !f.peer.HasInlayProjection() || f.peer.Value() != inlayConnectedSource {
		t.Fatal("closing one view changed the peer")
	}
	reopened := liveTestOpen(t, one.app, f.engine.main)
	one.app.setFocusEditor()
	remoteJourneyUntil(t, f.shells, func() bool { return reopened.HasInlayProjection() && f.peer.HasInlayProjection() })
	reopened.SetCursorPosition(2, 13)
	renameConnectedKey(t, one, tea.KeyPressMsg{Code: 'X', Text: "X"})
	remoteJourneyUntil(t, f.shells, func() bool {
		return strings.Contains(reopened.Value(), "1X") && f.peer.Value() == reopened.Value() && reopened.HasInlayProjection()
	})
	renameConnectedKey(t, one, tea.KeyPressMsg{Code: 'z', Mod: tea.ModCtrl})
	remoteJourneyUntil(t, f.shells, func() bool {
		return reopened.Value() == inlayConnectedSource && f.peer.Value() == inlayConnectedSource && reopened.HasInlayProjection()
	})
	if one.app.Input.Value() != "private first inlay draft" || f.shells[1].app.Input.Value() != "private second inlay draft" {
		t.Fatal("close/reopen consumed a private draft")
	}
	if disk, err := os.ReadFile(f.engine.main); err != nil || string(disk) != inlayConnectedSource {
		t.Fatal("close/reopen wrote disk", err)
	}
}
