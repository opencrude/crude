package remotes

import (
	"fmt"
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
	"crude/remoteconn"
	"crude/tui/surface"
	"crude/tui/theme"
	"crude/tui/ui"
)

func remotesPaint(t *testing.T, s *ConnectionEditor, w, h int) (*ui.Host, string) {
	t.Helper()
	host := ui.NewHost(theme.DefaultDark(), s)
	host.RegisterTree(s)
	host.Resize(w, h)
	host.Layout()
	host.Focus(FormID)
	buf := surface.NewCellBuffer(w, h)
	host.Paint(buf)
	var text strings.Builder
	for y := 0; y < h; y++ {
		for x := 0; x < w; x++ {
			text.WriteRune(buf.Get(x, y).Char)
		}
		text.WriteByte('\n')
	}
	return host, text.String()
}
func TestRemotesNativeFormPaintKeyboardAndMaskAtBothSizes(t *testing.T) {
	for _, size := range [][2]int{{86, 28}, {50, 15}} {
		t.Run(fmt.Sprint(size), func(t *testing.T) {
			s := NewConnection(theme.DefaultDark(), remoteconn.Connection{ID: "one", Kind: "ssh", Carrier: "auto"}, false)
			s.Model.SetValue("code", ui.StringValue("NEVER_SHOW_THIS_CODE"))
			s.Form.SetForm(s.Model, "", "")
			host, text := remotesPaint(t, s, size[0], size[1])
			if !strings.Contains(text, "Name") || !strings.Contains(text, "Test & save") || strings.Contains(text, "NEVER_SHOW_THIS_CODE") {
				t.Fatalf("bad native form paint: %s", text)
			}
			result := host.Handle(tea.PasteMsg{Content: "My connection"})
			if !result.Handled || s.Model.Field("name").Value.String() != "My connection" {
				t.Fatal("real focused Form paste did not edit name")
			}
			host.Handle(tea.KeyPressMsg{Code: tea.KeyTab})
			if s.Form.HelpField() == nil || s.Form.HelpField().ID != "kind" {
				t.Fatal("Tab did not traverse native form")
			}
			cancel := host.Handle(tea.KeyPressMsg{Code: tea.KeyEsc})
			if cancel.Cmd == nil {
				t.Fatalf("Esc failed to produce cancel: %+v", cancel)
			}
			msg, ok := cancel.Cmd().(ActionMsg)
			if !ok || msg.Action != "cancel" || msg.Target != s {
				t.Fatal("cancel lost exact editor")
			}
		})
	}
}
func TestRemotesSSHConfigSelectionAndDirectPinValidation(t *testing.T) {
	s := NewConnection(theme.DefaultDark(), remoteconn.Connection{ID: "one", Name: "One", Kind: "ssh", Carrier: "auto"}, false)
	s.SetSSHHosts([]string{"work-alias"})
	s.Model.SetValue("ssh-config", ui.StringValue("work-alias"))
	draft, err := s.Draft()
	if err != nil || draft.Connection.Host != "work-alias" || draft.Connection.Kind != "ssh" {
		t.Fatalf("alias not applied: %v %v", draft, err)
	}
	s.Model.SetValue("kind", ui.StringValue("direct"))
	s.Model.SetValue("port", ui.StringValue("4444"))
	if _, err = s.Draft(); err == nil {
		t.Fatal("unknown certificate accepted without pin")
	}
	s.Model.SetValue("fingerprint", ui.StringValue(strings.Repeat("a", 64)))
	if _, err = s.Draft(); err != nil {
		t.Fatal(err)
	}
}
