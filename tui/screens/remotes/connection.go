// Package remotes supplies the shared native connection editor. Persistence and
// connection attempts belong to the client manager, never to this component.
package remotes

import (
	"fmt"
	"strconv"
	"strings"

	tea "charm.land/bubbletea/v2"
	"crude/remoteconn"
	"crude/shellport"
	"crude/tui/cell"
	"crude/tui/rendering"
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
	tokens             *ui.TokenSet
	headerRect         ui.Rect
	helpRect           ui.Rect
	statusText         string
	connection         remoteconn.Connection
	open, busy, remove bool
}

func NewConnection(th *theme.Theme, c remoteconn.Connection, open bool) *ConnectionEditor {
	if th == nil {
		th = theme.DefaultDark()
	}
	tokens := ui.NewTokenSet(th)
	s := &ConnectionEditor{Base: ui.NewBase("remotes/editor"), Model: ui.NewFormModel(), connection: c, open: open, tokens: tokens}
	add := func(id, label, value, description string, kind ui.FieldType) {
		s.Model.AddField(&ui.FieldState{ID: id, Label: label, Description: description, Type: kind, Value: ui.StringValue(value), Initial: ui.StringValue(value)})
	}
	if !open {
		add("name", "Name", c.Name, "Choose a name for this connection on this client.", ui.FieldText)
		s.Model.AddField(ui.SegmentedFieldState("kind", "Connection type", []ui.FormOption{{ID: "ssh", Label: "SSH"}, {ID: "direct", Label: "Direct"}}, c.Kind, "SSH uses your system SSH configuration; Direct connects to the host's remote listener."))
		add("host", "Host or alias", c.Host, "Enter a host name, IP address or SSH alias; SSH verifies known hosts strictly.", ui.FieldText)
		s.Model.Field("host").Placeholder = "example.com"
		port := ""
		if c.Port != 0 {
			port = strconv.Itoa(c.Port)
		}
		add("port", "Port", port, "Leave blank to use your SSH configuration; Direct requires the listener's port.", ui.FieldText)
		s.Model.Field("port").Numeric = true
		add("user", "SSH user", c.User, "Leave blank to use your SSH configuration; leave empty for Direct.", ui.FieldText)
		add("identity", "SSH identity file", c.IdentityFile, "Optional private-key path for SSH; leave empty for Direct. No password is stored.", ui.FieldText)
		s.Model.AddField(&ui.FieldState{ID: "security", Label: "Trust & pairing", Type: ui.FieldSeparator})
		add("carrier", "Transport", c.Carrier, "Automatic chooses an available transport; the SSH transport is only valid for SSH connections.", ui.FieldSelect)
		s.Model.Field("carrier").Options = []ui.FormOption{{ID: "auto", Label: "Automatic"}, {ID: "ssh", Label: "SSH"}, {ID: "quic", Label: "QUIC"}, {ID: "tls", Label: "TLS"}}
		add("fingerprint", "Certificate pin", c.Fingerprint, "Verify the host's 64 lowercase SHA-256 hex digits through a trusted channel; required for Direct.", ui.FieldText)
		add("code", "Pairing code", "", "Enter the host's one-time code, then use Test & save to pair this device.", ui.FieldPassword)
		s.Model.AddField(ui.BoolFieldState("fallback", "Credential file", false, "Allow a private 0600 credential file only if the system keychain is unavailable."))
	} else {
		add("root", "Workspace path", "", "Enter the full workspace path on the destination backend, not this client.", ui.FieldText)
	}
	s.Form = ui.NewForm(FormID, s.Model, "", "", tokens)
	s.Form.SetFieldGap(1)
	s.Form.SetHideDescriptions(true)
	s.scrollbar = ui.NewScrollbarControl("remotes/form-scroll", s.Form.Scroll(), tokens)
	s.scrollbar.SetSurfaceBackground(ui.ColorPopupBg)
	s.scrollbar.SetPointerTargetSize(1)
	s.Form.SetArrowExitsForm(true)
	s.status = ui.NewTextArea("remotes/status", tokens, true)
	s.status.SetReadOnly(true)
	if open {
		s.SetStatus("Destination attaches before the current workspace is released. Dirty editors and private drafts use the existing workspace-switch checks.")
	}
	s.Actions = ui.NewActionBar(ActionsID, tokens)
	s.Actions.SetShowRule(true)
	s.Actions.SetCancelAction("cancel")
	s.buttons()
	return s
}

func (s *ConnectionEditor) SetSSHHosts(hosts []string) {
	if len(hosts) == 0 || s.open {
		return
	}
	f := &ui.FieldState{ID: "ssh-config", Label: "Saved SSH host", Description: "Choose an alias from your system SSH configuration, or enter the host manually.", Type: ui.FieldSelect, Value: ui.StringValue(""), Options: []ui.FormOption{{ID: "", Label: "Enter host manually"}}}
	for _, host := range hosts {
		f.Options = append(f.Options, ui.FormOption{ID: host, Label: host})
	}
	// Keep discovered aliases beside the host, not after the security controls.
	model := ui.NewFormModel()
	for _, field := range s.Model.Fields() {
		if field.ID == "ssh-config" {
			continue
		}
		model.AddField(field)
		if field.ID == "kind" {
			model.AddField(f)
		}
	}
	s.Model = model
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
	s.SetStatus("Remove the saved connection and its device credential from this client? This does not revoke the device on the host. You can add and pair it again later.")
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
func (s *ConnectionEditor) SetStatus(text string) {
	s.statusText = text
	s.status.SetValue(text)
}
func (s *ConnectionEditor) Root() string { return s.Model.Field("root").Value.String() }
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
	return shellport.RemoteConnectionDraft{Connection: c, PairCode: v("code"), AllowFileFallback: ui.BoolFieldValue(s.Model.Field("fallback"))}, nil
}
func (s *ConnectionEditor) ClearPairCode() {
	if f := s.Model.Field("code"); f != nil {
		s.Model.SetValue("code", ui.StringValue(""))
		s.Form.SetForm(s.Model, "", "")
	}
}
func (s *ConnectionEditor) Children() []ui.Component {
	children := []ui.Component{s.status, s.Actions}
	if !s.remove {
		children = append(children, s.Form)
		if s.scrollbar.Needed() {
			children = append(children, s.scrollbar)
		}
	}
	return children
}
func (s *ConnectionEditor) Measure(_ ui.LayoutContext, c ui.Constraints) ui.Size {
	return c.ClampSize(ui.Size{W: c.MaxW, H: c.MaxH})
}
func (s *ConnectionEditor) Layout(ctx ui.LayoutContext, r ui.Rect) {
	s.SetBounds(r)
	// Footer decisions survive short grids. The shared settings rhythm is a
	// three-row header, scrollable body, field-help row, then rule + decisions.
	footerH := min(2, max(0, r.H))
	bodyEnd := max(0, r.H-footerH)
	headerH := min(3, bodyEnd)
	s.headerRect = ui.Rect{W: r.W, H: headerH}
	helpH := min(1, max(0, bodyEnd-headerH))
	statusH := 0
	if s.statusText != "" {
		statusH = min(3, max(0, bodyEnd-headerH-helpH-1))
	}
	formH := max(0, bodyEnd-headerH-helpH-statusH)
	if s.remove {
		helpH = 0
		statusH = max(0, bodyEnd-headerH)
		formH = 0
	}
	formW := r.W
	s.Form.Layout(ctx, ui.Rect{Y: headerH, W: formW, H: formH})
	if s.scrollbar.Needed() {
		formW = max(0, r.W-1)
		s.Form.Layout(ctx, ui.Rect{Y: headerH, W: formW, H: formH})
	}
	s.scrollbar.Layout(ctx, ui.Rect{X: formW, Y: headerH, W: min(1, max(0, r.W-formW)), H: formH})
	s.status.Layout(ctx, ui.Rect{Y: headerH + formH, W: r.W, H: statusH})
	s.helpRect = ui.Rect{Y: bodyEnd - helpH, W: r.W, H: helpH}
	s.Actions.SetShowRule(footerH > 1)
	s.Actions.Layout(ctx, ui.Rect{Y: bodyEnd, W: r.W, H: footerH})
}
func (s *ConnectionEditor) Paint(buf *surface.CellBuffer, ox, oy int, _ ui.PaintContext) {
	r := s.Bounds()
	bg := s.tokens.Color(ui.ColorPopupBg)
	ui.FillRect(buf, ox, oy, r, cell.BlankCell(bg))
	x, y := ox+r.X, oy+r.Y
	title, subtitle := "Connection details", "Save tests this exact connection before storing it on this client."
	if s.open {
		title, subtitle = "Workspace destination", "Open a workspace on the selected backend."
	}
	if s.remove {
		title, subtitle = "Remove connection", "The host and its workspaces are not deleted."
	}
	if s.headerRect.H > 0 {
		buf.WriteString(x, y, rendering.TruncateVisual(title, r.W, "…"), s.tokens.Color(ui.ColorAccent), bg, cell.AttrBold)
	}
	if s.headerRect.H > 1 {
		buf.WriteString(x, y+1, rendering.TruncateVisual(subtitle, r.W, "…"), s.tokens.Color(ui.ColorMuted), bg, 0)
	}
	if field := s.Form.HelpField(); field != nil && !s.helpRect.Empty() {
		text, ink := field.Description, ui.ColorMuted
		if len(field.Errors) > 0 {
			text, ink = strings.Join(field.Errors, " · "), ui.ColorError
		}
		text = strings.Join(strings.Fields(text), " ")
		buf.WriteString(x+s.helpRect.X, y+s.helpRect.Y, rendering.TruncateVisual(text, s.helpRect.W, "…"), s.tokens.Color(ink), bg, 0)
	}
}
func (s *ConnectionEditor) SetTokens(t *ui.TokenSet) {
	s.tokens = t
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
		if s.remove {
			return focus(ActionsID)
		}
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
