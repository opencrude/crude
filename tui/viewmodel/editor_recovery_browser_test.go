package viewmodel

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"crude/clientstate"
	"crude/document"
	"crude/tui/surface"
	"crude/tui/ui"

	tea "charm.land/bubbletea/v2"
)

func TestEditorRecoveryBrowserExplicitRestorePreservesShared(t *testing.T) {
	runtime := remoteJourneyRuntime(t, "http://127.0.0.1:1")
	root := t.TempDir()
	path := filepath.Join(root, "draft.txt")
	if err := os.WriteFile(path, []byte("base"), 0600); err != nil {
		t.Fatal(err)
	}
	one := newRemoteJourneyShell(t, runtime, root)
	ed := liveTestOpen(t, one.app, path)
	ed.InsertRune('X')
	draft, _ := ed.ExportDocumentRecovery()
	raw, err := json.Marshal(draft)
	if err != nil {
		t.Fatal(err)
	}
	store, err := clientstate.Open(filepath.Join(t.TempDir(), "private.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = store.Close() })
	a, err := store.Acquire(context.Background(), "browser-profile", clientstate.AcquireOptions{})
	if err != nil {
		t.Fatal(err)
	}
	backup, err := store.SaveEditorRecovery(context.Background(), a, clientstate.EditorRecoveryKey{ClientID: a.ClientID, WorkspaceID: "workspace", Path: path + "#discard-backup-1"}, 0, raw)
	if err != nil {
		t.Fatal(err)
	}
	two := newRemoteJourneyShell(t, runtime, root)
	m := two.app
	if err = m.ConfigureComposerDrafts(store, a, "workspace"); err != nil {
		t.Fatal(err)
	}
	load := m.OpenEditorRecoveryBrowser()
	listed := load().(editorRecoveryBrowserMsg)
	if len(listed.items) != 1 {
		t.Fatalf("list: %+v", listed)
	}
	_, _ = m.handleEditorRecoveryBrowserMessage(listed)
	p := listed.p
	if len(p.record.Payload) != 0 || m.EditorState.Tabs[path] != nil {
		t.Fatal("listing eagerly restored")
	}
	frame := m.overlayHost.Window(editorRecoveryBrowserOverlay).Content.(*ui.WindowFrameWidget)
	host := frame.Host()
	host.Resize(100, 24)
	host.Layout()
	buf := surface.NewCellBuffer(100, 24)
	host.Paint(buf)
	var painted strings.Builder
	for y := 0; y < buf.Height(); y++ {
		for x := 0; x < buf.Width(); x++ {
			c := buf.Get(x, y)
			if c.Content != "" {
				painted.WriteString(c.Content)
			} else {
				painted.WriteRune(c.Char)
			}
		}
		painted.WriteByte('\n')
	}
	if !strings.Contains(painted.String(), "draft.txt#disc") || !strings.Contains(painted.String(), "Open draft") {
		t.Fatalf("browser did not paint records/actions: %s", painted.String())
	}
	host.Focus(p.list.ID())
	activation := host.Handle(tea.KeyPressMsg{Code: tea.KeyEnter})
	if activation.Cmd == nil {
		t.Fatal("keyboard list activation did not emit preview")
	}
	if item, ok := p.list.Selected(); !ok || item.ID != backup.Key.Path {
		t.Fatalf("missing rendered list item: %+v", item)
	}
	_, cmd := m.handleEditorRecoveryBrowserMessage(editorRecoveryBrowserMsg{p: p, action: "preview"})
	preview := cmd().(editorRecoveryBrowserMsg)
	_, _ = m.handleEditorRecoveryBrowserMessage(preview)
	if !strings.Contains(p.detail.Value(), "Xbase") || p.draft.Path != path || m.EditorState.Tabs[path] != nil {
		t.Fatal("preview did not retain original draft/path")
	}
	_, cmd = m.handleEditorRecoveryBrowserMessage(editorRecoveryBrowserMsg{p: p, action: "open"})
	staged := cmd()
	_, _ = m.handleEditorRecoveryBrowserMessage(staged)
	if len(m.editorCommands) == 0 {
		t.Fatal("no queued asynchronous editor open")
	}
	open := m.editorCommands[len(m.editorCommands)-1]
	m.editorCommands = nil
	_, _ = m.Update(open())
	restored := m.EditorState.Tabs[path].Editor
	if restored.Value() != "Xbase" || restored.DocumentState().Conflict == nil {
		t.Fatalf("not restored privately: %q %+v", restored.Value(), restored.DocumentState())
	}
	if _, err = restored.CaptureDocumentEdits(document.Owner{ClientID: "other", Epoch: 99}, "new", "new"); err == nil {
		t.Fatal("recovery auto-publishable")
	}
	bytes, err := os.ReadFile(path)
	if err != nil || string(bytes) != "base" {
		t.Fatalf("disk changed: %q %v", bytes, err)
	}
	retained, err := store.LoadEditorRecovery(context.Background(), a, backup.Key)
	if err != nil || retained.Revision != backup.Revision {
		t.Fatal("backup consumed")
	}
	// Opening another browser and then accepting an old result cannot target it.
	oldLoad := m.OpenEditorRecoveryBrowser()
	oldMsg := oldLoad().(editorRecoveryBrowserMsg)
	newLoad := m.OpenEditorRecoveryBrowser()
	newMsg := newLoad().(editorRecoveryBrowserMsg)
	_, _ = m.handleEditorRecoveryBrowserMessage(oldMsg)
	if !newMsg.p.busy {
		t.Fatal("old browser completion changed reopened browser")
	}
	_, _ = m.handleEditorRecoveryBrowserMessage(newMsg)
	_, cmd = m.handleEditorRecoveryBrowserMessage(editorRecoveryBrowserMsg{p: newMsg.p, action: "preview", info: listed.items[0]})
	_, _ = m.handleEditorRecoveryBrowserMessage(cmd())
	before := restored.Value()
	_, cmd = m.handleEditorRecoveryBrowserMessage(editorRecoveryBrowserMsg{p: newMsg.p, action: "open"})
	compared := cmd().(editorRecoveryBrowserMsg)
	if compared.action != "compared" || restored.Value() != before {
		t.Fatal("existing draft was replaced")
	}
	_, _ = m.handleEditorRecoveryBrowserMessage(compared)
	if restored.Value() != before || m.activeLocalDiff() == nil {
		t.Fatal("existing editor recovery did not open immutable comparison")
	}
	closeLoad := m.OpenEditorRecoveryBrowser()
	closeMsg := closeLoad().(editorRecoveryBrowserMsg)
	_, _ = m.handleEditorRecoveryBrowserMessage(closeMsg)
	closeFrame := m.overlayHost.Window(editorRecoveryBrowserOverlay).Content.(*ui.WindowFrameWidget)
	closeFrame.Host().Resize(100, 24)
	closeFrame.Host().Layout()
	esc := closeFrame.Host().Handle(tea.KeyPressMsg{Code: tea.KeyEscape})
	if esc.Cmd == nil {
		t.Fatal("Esc did not close browser")
	}
	_, _ = m.handleEditorRecoveryBrowserMessage(esc.Cmd())
	if m.overlayHost.IsOpen(editorRecoveryBrowserOverlay) || restored.Value() != before {
		t.Fatal("Esc changed draft or retained modal")
	}
}
