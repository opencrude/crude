// Package remotes supplies the shared native connection editor. Persistence and
// connection attempts belong to the client manager, never to this component.
package remotes

import (
	"fmt"
	"strconv"

	tea "charm.land/bubbletea/v2"
	"crude/remoteconn"
	"crude/shellport"
	"crude/tui/surface"
	"crude/tui/theme"
	"crude/tui/ui"
)

const FormID ui.ComponentID = "remotes/form"
const ActionsID ui.ComponentID = "remotes/actions"

type ActionMsg struct {
	Target *ConnectionEditor
	Action string
}

type ConnectionEditor struct {
	ui.Base
	Model              *ui.FormModel
	Form               *ui.Form
	Actions            *ui.ActionBar
	status             *ui.TextArea
	scrollbar          *ui.ScrollbarControl
	connection         remoteconn.Connection
	open, busy, remove bool
}

func NewConnection(th *theme.Theme, c remoteconn.Connection, open bool) *ConnectionEditor {
	tokens := ui.NewTokenSet(th)
	s := &ConnectionEditor{Base: ui.NewBase("remotes/editor"), Model: ui.NewFormModel(), connection: c, open: open}
	add := func(id, label, value string, kind ui.FieldType) {
		s.Model.AddField(&ui.FieldState{ID: id, Label: label, Type: kind, Value: ui.StringValue(value), Initial: ui.StringValue(value)})
	}
	if !open {
		add("name", "Name", c.Name, ui.FieldText)
		add("kind", "Connection type", c.Kind, ui.FieldCycle)
		s.Model.Field("kind").Options = []ui.FormOption{{ID: "ssh", Label: "SSH"}, {ID: "direct", Label: "Direct"}}
		add("host", "Host / SSH alias", c.Host, ui.FieldText)
		port := ""
		if c.Port != 0 {
			port = strconv.Itoa(c.Port)
		}
		add("port", "Port", port, ui.FieldText)
		add("user", "SSH user", c.User, ui.FieldText)
		add("identity", "SSH identity file", c.IdentityFile, ui.FieldText)
		add("carrier", "Carrier", c.Carrier, ui.FieldCycle)
		s.Model.Field("carrier").Options = []ui.FormOption{{ID: "auto", Label: "Automatic"}, {ID: "ssh", Label: "SSH"}, {ID: "quic", Label: "QUIC"}, {ID: "tls", Label: "TLS"}}
		add("fingerprint", "Trusted SHA-256 fingerprint", c.Fingerprint, ui.FieldText)
		s.Model.Field("fingerprint").Description = "Copy the 64 lower-case hexadecimal fingerprint from the host through a trusted channel. Unknown certificates are never accepted."
		add("code", "One-time pairing code", "", ui.FieldPassword)
		s.Model.AddField(&ui.FieldState{ID: "fallback", Label: "Allow private 0600 credential file if keychain unavailable", Type: ui.FieldCheckbox, Value: ui.BoolValue(false)})
	} else {
		add("root", "Workspace path on destination", "", ui.FieldText)
	}
	s.Form = ui.NewForm(FormID, s.Model, "", "", tokens)
	s.scrollbar = ui.NewScrollbarControl("remotes/form-scroll", s.Form.Scroll(), tokens)
	s.Form.SetArrowExitsForm(true)
	s.status = ui.NewTextArea("remotes/status", tokens, true)
	s.status.SetReadOnly(true)
	if open {
		s.status.SetValue("Destination attaches before the current workspace is released. Dirty editors and private drafts use the existing workspace-switch checks.")
	} else {
		s.status.SetValue("SSH uses system config aliases and strict known-host checks; no passwords are stored. Direct pairing requires a fingerprint verified on the host. Save tests the exact connection first.")
	}
	s.Actions = ui.NewActionBar(ActionsID, tokens)
	s.Actions.SetAlignEnd(false)
	s.Actions.SetCancelAction("cancel")
	s.buttons()
	return s
}

func (s *ConnectionEditor) SetSSHHosts(hosts []string) {
	if len(hosts) == 0 || s.open {
		return
	}
	f := &ui.FieldState{ID: "ssh-config", Label: "SSH config hosts", Type: ui.FieldCycle, Value: ui.StringValue(""), Options: []ui.FormOption{{ID: "", Label: "Enter host manually"}}}
	for _, host := range hosts {
		f.Options = append(f.Options, ui.FormOption{ID: host, Label: host})
	}
	s.Model.AddField(f)
	s.Model.OnChanged(func(id string) {
		if id == "ssh-config" {
			if host := s.Model.Field(id).Value.String(); host != "" {
				s.Model.SetValue("host", ui.StringValue(host))
				s.Model.SetValue("kind", ui.StringValue("ssh"))
				s.Form.SetForm(s.Model, "", "")
			}
		}
	})
	s.Form.SetForm(s.Model, "", "")
}

func (s *ConnectionEditor) buttons() {
	primary := "Test & save"
	action := "save"
	if s.open {
		primary = "Open workspace"
		action = "open"
	}
	if s.remove {
		s.Actions.SetButtons(ui.ActionBarButton{Label: "Remove from this client", Action: "remove", Enabled: !s.busy, Variant: ui.VariantDanger}, ui.ActionBarButton{Label: "Cancel", Action: "cancel", Enabled: true, Variant: ui.VariantSecondary})
		s.Actions.FocusLast()
		return
	}
	buttons := []ui.ActionBarButton{{Label: primary, Action: action, Enabled: !s.busy, Variant: ui.VariantPrimary}}
	if !s.open {
		buttons = append(buttons, ui.ActionBarButton{Label: "Test", Action: "test", Enabled: !s.busy, Variant: ui.VariantSecondary})
	}
	buttons = append(buttons, ui.ActionBarButton{Label: "Cancel", Action: "cancel", Enabled: true, Variant: ui.VariantSecondary})
	s.Actions.SetButtons(buttons...)
}
func (s *ConnectionEditor) ConfirmRemove() {
	s.remove = true
	s.status.SetValue("Remove the saved connection and its device credential from this client? This does not revoke the device on the host. You can add and pair it again later.")
	s.buttons()
}
func (s *ConnectionEditor) SetBusy(b bool) {
	s.busy = b
	for _, f := range s.Model.Fields() {
		f.Disabled = b
	}
	s.Form.SetForm(s.Model, "", "")
	s.buttons()
}
func (s *ConnectionEditor) SetStatus(text string) { s.status.SetValue(text) }
func (s *ConnectionEditor) Root() string          { return s.Model.Field("root").Value.String() }
func (s *ConnectionEditor) SetRoot(root string) {
	s.Model.SetValue("root", ui.StringValue(root))
	s.Form.SetForm(s.Model, "", "")
}
func (s *ConnectionEditor) Draft() (shellport.RemoteConnectionDraft, error) {
	c := s.connection
	v := func(id string) string { return s.Model.Field(id).Value.String() }
	c.Name, c.Kind, c.Host, c.User, c.IdentityFile, c.Fingerprint, c.Carrier = v("name"), v("kind"), v("host"), v("user"), v("identity"), v("fingerprint"), v("carrier")
	c.Port = 0
	if port := v("port"); port != "" {
		n, err := strconv.Atoi(port)
		if err != nil {
			return shellport.RemoteConnectionDraft{}, fmt.Errorf("port must be a number")
		}
		c.Port = n
	}
	if err := c.Validate(); err != nil {
		return shellport.RemoteConnectionDraft{}, err
	}
	return shellport.RemoteConnectionDraft{Connection: c, PairCode: v("code"), AllowFileFallback: s.Model.Field("fallback").Value.Bool()}, nil
}
func (s *ConnectionEditor) ClearPairCode() {
	if f := s.Model.Field("code"); f != nil {
		s.Model.SetValue("code", ui.StringValue(""))
		s.Form.SetForm(s.Model, "", "")
	}
}
func (s *ConnectionEditor) Children() []ui.Component {
	return []ui.Component{s.Form, s.scrollbar, s.status, s.Actions}
}
func (s *ConnectionEditor) Measure(_ ui.LayoutContext, c ui.Constraints) ui.Size {
	return c.ClampSize(ui.Size{W: c.MaxW, H: c.MaxH})
}
func (s *ConnectionEditor) Layout(ctx ui.LayoutContext, r ui.Rect) {
	s.SetBounds(r)
	statusH := min(3, max(0, r.H-2))
	formH := max(0, r.H-statusH-2)
	if s.remove {
		statusH = max(0, r.H-2)
		formH = 0
	}
	s.Form.Layout(ctx, ui.Rect{W: max(0, r.W-1), H: formH})
	s.scrollbar.Layout(ctx, ui.Rect{X: max(0, r.W-1), W: min(1, r.W), H: formH})
	s.status.Layout(ctx, ui.Rect{Y: formH, W: r.W, H: statusH})
	s.Actions.Layout(ctx, ui.Rect{Y: max(0, r.H-1), W: r.W, H: min(1, r.H)})
}
func (s *ConnectionEditor) Paint(*surface.CellBuffer, int, int, ui.PaintContext) {}
func (s *ConnectionEditor) SetTokens(t *ui.TokenSet) {
	s.Form.SetTokens(t)
	s.scrollbar.SetTokens(t)
	s.status.SetTokens(t)
	s.Actions.SetTokens(t)
}
func (s *ConnectionEditor) Handle(ctx ui.EventContext, e ui.Event) ui.EventResult {
	if e.IsKey() && e.Key.Code == tea.KeyEsc {
		return s.HandleAction(ctx, ui.Action{ID: "cancel"})
	}
	return ui.EventResult{}
}
func (s *ConnectionEditor) HandleAction(_ ui.EventContext, a ui.Action) ui.EventResult {
	focus := func(id ui.ComponentID) ui.EventResult {
		return ui.EventResult{Handled: true, Action: &ui.Action{ID: "ui.focus", Payload: id}, Invalidate: ui.InvalidateFocus | ui.InvalidatePaint}
	}
	switch a.ID {
	case "form.exit.forward", "form.exit.backward":
		s.Actions.FocusLast()
		return focus(ActionsID)
	case "actionbar.exit.forward", "actionbar.exit.backward":
		s.Form.FocusFirst()
		return focus(FormID)
	case "form.cancel":
		a.ID = "cancel"
	case "form.changed":
		return ui.EventResult{Handled: true, Invalidate: ui.InvalidatePaint}
	}
	if a.ID == "cancel" || (!s.busy && (a.ID == "save" || a.ID == "test" || a.ID == "open" || a.ID == "remove")) {
		return ui.EventResult{Handled: true, Cmd: func() tea.Msg { return ActionMsg{Target: s, Action: a.ID} }}
	}
	return ui.EventResult{}
}
func (s *ConnectionEditor) Semantics() ui.Semantics {
	return ui.Semantics{Role: ui.RoleDialog, Label: "Remote connection"}
}
