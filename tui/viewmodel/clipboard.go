package viewmodel

import (
	"encoding/base64"
	"fmt"
	"net/http"
	"strings"

	tea "charm.land/bubbletea/v2"

	"crude/clipboard"
	"crude/sessiondb"
)

func (m *App) copyEditorSelection() {
	if m.EditorState.Editor == nil || !m.EditorState.Editor.HasSelection() {
		return
	}
	m.copyToClipboard(m.EditorState.Editor.SelectedText())
}

func (m *App) cutEditorSelection() {
	if m.EditorState.Editor == nil || !m.EditorState.Editor.HasSelection() {
		return
	}
	m.copyToClipboard(m.EditorState.Editor.SelectedText())
	m.EditorState.Editor.DeleteSelection()
}

func (m *App) pasteIntoEditor() {
	if m.EditorState.Editor == nil {
		return
	}
	data := clipboard.Read(clipboard.FmtText)
	if len(data) == 0 {
		m.StatusBar.SetError("clipboard is empty or unavailable")
		return
	}
	content := string(data)
	m.EditorState.Editor.HandleEvent(tea.PasteMsg{Content: content})
	m.StatusBar.SetInfo(fmt.Sprintf("Pasted %d chars", len(content)))
}

func (m *App) pasteIntoInput() {
	if m.pasteClipboardImage() {
		return
	}
	data := clipboard.Read(clipboard.FmtText)
	if len(data) == 0 {
		m.StatusBar.SetError("clipboard is empty or unavailable")
		return
	}
	content := string(data)
	canType := !m.OverlayState.IsBlocking()
	updated, _, _ := m.Input.Update(tea.PasteMsg{Content: content},
		canType, m.OverlayState.IsBlocking())
	m.Input = updated
	m.StatusBar.SetInfo(fmt.Sprintf("Pasted %d chars into input", len(content)))
}

func (m *App) pasteClipboardImage() bool {
	if m.ActiveSession == nil || m.ActiveSession.RefStore == nil {
		return false
	}
	data := clipboard.Read(clipboard.FmtImage)
	if len(data) == 0 {
		return false
	}
	mime := strings.SplitN(http.DetectContentType(data), ";", 2)[0]
	switch mime {
	case "image/png", "image/jpeg", "image/gif", "image/webp":
	default:
		m.StatusBar.SetError("clipboard image format is not supported")
		return true
	}
	ref, err := m.ActiveSession.RefStore.CreateReference(sessiondb.CreateRequest{
		Kind: sessiondb.KindPastedImage, Title: "Clipboard image",
		Mime: mime, Content: base64.StdEncoding.EncodeToString(data),
	})
	if err != nil {
		m.StatusBar.SetError("image paste failed: " + err.Error())
		return true
	}
	m.Input.InsertReference(ref)
	m.StatusBar.SetInfo(fmt.Sprintf("Attached clipboard image (%d KB)", (len(data)+1023)/1024))
	return true
}

func (m *App) copyToClipboard(s string) {
	if s == "" {
		return
	}
	if err := clipboard.WriteText(s); err != nil {
		m.StatusBar.SetError("clipboard error: " + err.Error())
		return
	}
	m.StatusBar.SetInfo(fmt.Sprintf("Copied %d chars", len(s)))
}
