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
	return host, remotesText(buf, w, h)
}

func remotesText(buf *surface.CellBuffer, w, h int) string {
	var text strings.Builder
	for y := 0; y < h; y++ {
		for x := 0; x < w; x++ {
			text.WriteRune(buf.Get(x, y).Char)
		}
		text.WriteByte('\n')
	}
	return text.String()
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

func TestConnectionSettingsAnatomyAndContextHelp(t *testing.T) {
	s := NewConnection(theme.DefaultDark(), remoteconn.Connection{ID: "one", Kind: "ssh", Carrier: "auto"}, false)
	host, text := remotesPaint(t, s, 86, 28)
	rows := strings.Split(text, "\n")
	if !strings.HasPrefix(rows[0], "Connection details") || !strings.Contains(rows[1], "Save tests this exact connection") {
		t.Fatalf("missing settings header:\n%s", text)
	}
	if strings.TrimSpace(rows[2]) != "" {
		t.Fatal("header has no breathing row")
	}
	for _, pair := range [][2]string{{"name", "kind"}, {"kind", "host"}, {"host", "port"}, {"fingerprint", "code"}, {"code", "fallback"}} {
		first, second := s.Form.Field(pair[0]).Bounds(), s.Form.Field(pair[1]).Bounds()
		if second.Y != first.Y+first.H+1 {
			t.Fatalf("%v fields lack exactly one breathing row: %v %v", pair, first, second)
		}
	}
	if !strings.Contains(rows[25], "Choose a name for this connection") || !strings.Contains(rows[26], "─") {
		t.Fatalf("field help must be directly above footer rule:\n%s", text)
	}
	if !strings.Contains(rows[27], "Test & save") || !strings.Contains(rows[27], "Test") || !strings.Contains(rows[27], "Cancel") {
		t.Fatalf("missing pinned decisions:\n%s", text)
	}
	if strings.Index(rows[27], "[") <= 0 || !strings.HasSuffix(strings.TrimSpace(rows[27]), "[ Cancel ]") {
		t.Fatal("floating decisions must align to the right")
	}
	if strings.Count(text, s.Model.Field("name").Description) != 1 || strings.Contains(text, s.Model.Field("fingerprint").Description) {
		t.Fatal("routine descriptions leaked into body rows")
	}
	if !s.Form.FocusField("fingerprint") {
		t.Fatal("certificate pin is unreachable")
	}
	buf := surface.NewCellBuffer(86, 28)
	host.Paint(buf)
	text = remotesText(buf, 86, 28)
	if !strings.Contains(strings.Split(text, "\n")[25], "64 lowercase SHA-256") || strings.Contains(text, "Choose a name for this connection") {
		t.Fatalf("help did not follow focused field:\n%s", text)
	}
}

func TestConnectionConstrainedNavigationRetainsDraftAndConsent(t *testing.T) {
	s := NewConnection(theme.DefaultDark(), remoteconn.Connection{ID: "one", Name: "Work", Kind: "direct", Host: "example.com", Port: 443, Carrier: "tls", Fingerprint: strings.Repeat("a", 64)}, false)
	host, _ := remotesPaint(t, s, 50, 15)
	s.Model.SetValue("code", ui.StringValue("PRIVATE_PAIRING_CODE"))
	s.Form.SetForm(s.Model, "", "")
	for range 9 {
		host.Handle(tea.KeyPressMsg{Code: tea.KeyTab})
	}
	if s.Form.HelpField() == nil || s.Form.HelpField().ID != "fallback" {
		t.Fatal("keyboard cannot reach consent at constrained height")
	}
	if s.Form.Scroll().Offset() == 0 {
		t.Fatal("keyboard did not reveal offscreen controls")
	}
	host.Handle(tea.KeyPressMsg{Code: tea.KeySpace})
	draft, err := s.Draft()
	if err != nil || !draft.AllowFileFallback || draft.PairCode != "PRIVATE_PAIRING_CODE" {
		t.Fatalf("explicit consent/draft lost: %+v %v", draft, err)
	}
	s.SetBusy(true)
	s.SetStatus("Connection not saved. Please verify the destination and try again.")
	host.MarkLayout()
	buf := surface.NewCellBuffer(50, 15)
	host.Paint(buf)
	text := remotesText(buf, 50, 15)
	if !strings.Contains(text, "Connection not saved.") || strings.Contains(text, "PRIVATE_PAIRING_CODE") {
		t.Fatalf("notice missing or secret exposed:\n%s", text)
	}
	if result := s.HandleAction(ui.EventContext{}, ui.Action{ID: "save"}); result.Handled || result.Cmd != nil {
		t.Fatal("busy editor admitted a duplicate save")
	}
	cancel := s.HandleAction(ui.EventContext{}, ui.Action{ID: "cancel"})
	if cancel.Cmd == nil || cancel.Cmd().(ActionMsg).Target != s {
		t.Fatal("busy operation lost exact-owner cancellation")
	}
	s.SetBusy(false)
	after, err := s.Draft()
	if err != nil || after.Connection != draft.Connection || after.PairCode != draft.PairCode || !after.AllowFileFallback {
		t.Fatal("notice/busy cycle consumed edited connection")
	}
	s.ClearPairCode()
	after, err = s.Draft()
	if err != nil || after.PairCode != "" || after.Connection != draft.Connection {
		t.Fatal("clearing secret changed the saved connection draft")
	}
}

func TestConnectionOpenAndRemoveJourneys(t *testing.T) {
	for _, size := range [][2]int{{86, 28}, {50, 15}} {
		t.Run(fmt.Sprint(size), func(t *testing.T) {
			s := NewConnection(theme.DefaultDark(), remoteconn.Connection{ID: "one"}, true)
			s.SetRoot("/srv/project")
			host, text := remotesPaint(t, s, size[0], size[1])
			if !strings.Contains(text, "/srv/project") || !strings.Contains(text, "Open workspace") || strings.Contains(text, "Certificate pin") || strings.Contains(text, "Test & save") {
				t.Fatalf("wrong open-workspace surface:\n%s", text)
			}
			host.Focus(ActionsID)
			s.Actions.FocusFirst()
			result := host.Handle(tea.KeyPressMsg{Code: tea.KeyEnter})
			if result.Cmd == nil || result.Cmd().(ActionMsg) != (ActionMsg{Target: s, Action: "open"}) || s.Root() != "/srv/project" {
				t.Fatal("open decision lost exact destination")
			}

			remove := NewConnection(theme.DefaultDark(), remoteconn.Connection{ID: "one", Name: "Work", Kind: "ssh"}, false)
			remove.ConfirmRemove()
			host, text = remotesPaint(t, remove, size[0], size[1])
			host.Focus(ActionsID)
			if !strings.Contains(strings.Join(strings.Fields(text), " "), "does not revoke") || !strings.Contains(text, "Remove from this client") || strings.Contains(text, "Certificate pin") {
				t.Fatalf("wrong remove confirmation:\n%s", text)
			}
			result = host.Handle(tea.KeyPressMsg{Code: tea.KeyEnter})
			if result.Cmd == nil || result.Cmd().(ActionMsg) != (ActionMsg{Target: remove, Action: "cancel"}) {
				t.Fatal("remove must default to Cancel")
			}
			host.Handle(tea.KeyPressMsg{Code: tea.KeyUp})
			if host.Focused() != ActionsID {
				t.Fatal("remove navigation entered the hidden editing form")
			}
			remove.Actions.FocusFirst()
			result = host.Handle(tea.KeyPressMsg{Code: tea.KeyEnter})
			if result.Cmd == nil || result.Cmd().(ActionMsg) != (ActionMsg{Target: remove, Action: "remove"}) {
				t.Fatal("explicit remove decision was not preserved")
			}
		})
	}
}

func TestConnectionSSHHostsStayBesideDestinationAndPreserveEdits(t *testing.T) {
	s := NewConnection(theme.DefaultDark(), remoteconn.Connection{ID: "one", Kind: "ssh", Carrier: "auto"}, false)
	s.Model.SetValue("name", ui.StringValue("Private draft"))
	s.Model.SetValue("code", ui.StringValue("secret"))
	s.SetSSHHosts([]string{"work"})
	s.SetSSHHosts([]string{"work", "backup"})
	if s.Model.Fields()[2].ID != "ssh-config" || s.Model.Fields()[3].ID != "host" {
		t.Fatal("SSH catalog is not adjacent to destination controls")
	}
	count := 0
	for _, field := range s.Model.Fields() {
		if field.ID == "ssh-config" {
			count++
		}
	}
	if count != 1 || s.Model.Field("name").Value.String() != "Private draft" || s.Model.Field("code").Value.String() != "secret" {
		t.Fatal("SSH discovery duplicated controls or replaced edited draft")
	}
}
