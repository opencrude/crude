package viewmodel

import (
	"fmt"
	"os"
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"

	"crude/tui/screens/termdock"
	"crude/types/statuscenter"
)

// Exercise the real dock and footer in a dedicated tuishot terminal. F2 opens
// Notices; F3 closes it and adds a notice; F4 opens it again; F9 verifies exit.
func TestDenseNoticesVisualAcceptance(t *testing.T) {
	if os.Getenv("CRUDE_VISUAL_DENSE_NOTICES") != "1" {
		t.Skip("requires a dedicated capture terminal")
	}
	app := newFullTestApp(t)
	wsOf(app).Logger = stubLogger{}
	for i := range 153 {
		app.ws.Status().Notify(statuscenter.NotificationRecord{Summary: fmt.Sprintf("Workspace notice %03d", i+1)})
	}
	model := &noticeHistoryVisual{App: app}
	p := tea.NewProgram(model, tea.WithWidthMethod(ansi.GraphemeWidth))
	app.SetProgram(p)
	_, err := p.Run()
	app.StopAsyncIO()
	if err != nil || !model.opened || !model.reopened {
		t.Fatalf("open=%v reopen=%v run=%v", model.opened, model.reopened, err)
	}
}

type noticeHistoryVisual struct {
	*App
	opened, reopened bool
}

func (*noticeHistoryVisual) Init() tea.Cmd { return nil }
func (m *noticeHistoryVisual) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	if key, ok := msg.(tea.KeyPressMsg); ok {
		switch key.Code {
		case tea.KeyF2, tea.KeyF4:
			m.toggleWorkspacePanel(termdock.PanelNotices)
			return m, nil
		case tea.KeyF3:
			m.opened = strings.Contains(bufferPlain(m.mainSurf.Buffer()), "Workspace notice 153") && m.ws.Status().UnreadCount() == 0
			m.toggleWorkspacePanel(termdock.PanelNotices)
			m.ws.Status().Notify(statuscenter.NotificationRecord{Summary: "New while hidden"})
			return m, nil
		case tea.KeyF9:
			m.reopened = strings.Contains(bufferPlain(m.mainSurf.Buffer()), "New while hidden") && m.ws.Status().UnreadCount() == 0
			return m, tea.Quit
		}
	}
	_, cmd := m.App.Update(msg)
	return m, cmd
}
