package viewmodel

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"
	"crude/textencoding"
	"crude/types"
)

func TestConnectedEncodingMenuSaveReloadAndUndo(t *testing.T) {
	root := t.TempDir()
	path := filepath.Join(root, "encoding.txt")
	text := "café 😀\r\n"
	if err := os.WriteFile(path, []byte(text), 0o600); err != nil {
		t.Fatal(err)
	}
	shell := newRemoteJourneyShell(t, remoteJourneyRuntime(t, "http://unused.invalid"), root)
	app := shell.app
	ed := liveTestOpen(t, app, path)
	remoteJourneyUntil(t, []remoteJourneyShell{shell}, func() bool { return app.liveEditors[ed].topicReady })
	runAppCommand(t, app, app.openEncodingMenu(ed))
	key := func(k tea.KeyPressMsg) {
		t.Helper()
		_, cmd := app.Update(k)
		runAppCommand(t, app, cmd)
	}
	key(tea.KeyPressMsg{Code: tea.KeyDown})
	key(tea.KeyPressMsg{Code: tea.KeyDown})
	key(tea.KeyPressMsg{Code: tea.KeyEnter})
	remoteJourneyUntil(t, []remoteJourneyShell{shell}, func() bool { return ed.DocumentState().Encoding == textencoding.UTF16LE && !app.liveEditors[ed].busy })
	want, _ := textencoding.EncodeText([]byte(text), textencoding.UTF16LE)
	actual, err := os.ReadFile(path)
	if err != nil || !bytes.Equal(actual, want) || ed.Value() != text || ed.Modified() {
		t.Fatalf("menu conversion = %x, %v, text=%q dirty=%v", actual, err, ed.Value(), ed.Modified())
	}
	var labels []string
	for _, item := range app.statusHeaderRightSegments() {
		labels = append(labels, item.Text)
	}
	if !strings.Contains(strings.Join(labels, " "), "UTF-16 LE") {
		t.Fatal("current encoding is not exposed")
	}
	_, cmd := app.Update(tea.PasteMsg{Content: "draft "})
	runAppCommand(t, app, cmd)
	remoteJourneyUntil(t, []remoteJourneyShell{shell}, func() bool {
		return ed.DocumentState().PendingEdits == 0 && !app.liveEditors[ed].busy && !app.liveEditors[ed].sync
	})
	draft := ed.Value()
	app.applyEditorSettings(types.EditorSettings{AutoSave: types.AutoSaveAfterDelay, AutoSaveDelay: 100000})
	app.liveEditors[ed].due = time.Now().Add(-time.Second)
	app.confirmEditorReload(ed)
	if app.editorReload == nil {
		t.Fatal("reload did not ask before replacing unsaved text")
	}
	runAppCommand(t, app, app.drainLiveEditorCommands())
	if app.liveEditors[ed].save || app.liveEditors[ed].busy {
		t.Fatal("autosave wrote the draft while the reload decision was open")
	}
	key(tea.KeyPressMsg{Code: tea.KeyLeft})
	key(tea.KeyPressMsg{Code: tea.KeyEnter})
	remoteJourneyUntil(t, []remoteJourneyShell{shell}, func() bool { return ed.Value() == text && !ed.Modified() })
	actual, err = os.ReadFile(path)
	if err != nil || !bytes.Equal(actual, want) {
		t.Fatal("reload wrote disk bytes")
	}
	key(tea.KeyPressMsg{Code: 'z', Mod: tea.ModCtrl})
	remoteJourneyUntil(t, []remoteJourneyShell{shell}, func() bool { return ed.Value() == draft && !app.liveEditors[ed].busy })
	if !ed.Modified() || ed.DocumentState().Encoding != textencoding.UTF16LE {
		t.Fatal("reload lost the undoable draft or disk encoding")
	}
}
