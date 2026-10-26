package viewmodel

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"strings"
	"time"

	tea "charm.land/bubbletea/v2"
	"crude/coreprotocol"
	"crude/shellport"
	"crude/tui/editor"
	"crude/tui/screens/boardbrowser"
	"crude/tui/screens/gitpanel"
	"crude/tui/screens/memorybrowser"
	"crude/tui/screens/question"
	"crude/tui/screens/sessionsettings"
	"crude/tui/screens/termdock"
	settingsui "crude/tui/settings"
	"crude/tui/ui"
	"crude/tui/window"
	"crude/types/control"
)

// ShellCommandDescriptor is the semantic effect owner contract used by gateway
// discovery. Registration does not authorize any of these operations.
func ShellCommandDescriptor(id string) (control.Descriptor, bool) {
	switch id {
	case "shell.inspect", "shell.controls", "shell.describe_control", "performance.shell", "connection.inspect", "tree.set_visible", "tree.set_width", "tree.reveal", "tree.filter", "pane.split", "pane.merge", "pane.focus", "pane.resize", "tab.select", "tab.close", "file.open", "editor.navigate", "editor.select", "terminal.open", "terminal.show", "terminal.hide", "terminal.resize_dock", "panel.show", "panel.hide", "settings.open":
		return control.Lookup(id)
	default:
		return productDescriptor(id)
	}
}

func decodeShellInput(raw json.RawMessage, dst any) error {
	if len(raw) == 0 {
		raw = json.RawMessage(`{}`)
	}
	d := json.NewDecoder(bytes.NewReader(raw))
	d.DisallowUnknownFields()
	if err := d.Decode(dst); err != nil {
		return err
	}
	if err := d.Decode(new(any)); err != io.EOF {
		return errors.New("input must contain exactly one object")
	}
	return nil
}

type shellControlDoneMsg struct {
	request       shellport.ControlRequestMsg
	completion    tea.Msg
	generation    uint64
	settingsOwner *settingsui.WorkspaceSettingsSurface
	settingsModel *settingsui.Model
	deckOwner     ui.Component
	memoryOwner   *memorybrowser.Screen
	memoryEpoch   uint64
	boardOwner    *boardbrowser.Screen
	pickerOwner   *capabilityPickerOwner
}

type shellControlReplyMsg struct{ err error }

// shellInspection deliberately omits composer values, transcript, credentials,
// arbitrary component values and all peer private state.
type shellInspection struct {
	TreeVisible bool                 `json:"tree_visible"`
	TreeWidth   int                  `json:"tree_width"`
	DockOpen    bool                 `json:"dock_open"`
	DockPanel   string               `json:"dock_panel"`
	DockRows    int                  `json:"dock_rows"`
	ActiveGroup window.GroupID       `json:"active_group"`
	Groups      []window.GroupID     `json:"groups"`
	Tabs        []shellTabInspection `json:"tabs"`
	Width       int                  `json:"width"`
	Height      int                  `json:"height"`
	Revision    uint64               `json:"revision"`
	OpenEpoch   uint64               `json:"open_epoch"`
}
type shellTabInspection struct {
	ID       window.TabID   `json:"id"`
	Group    window.GroupID `json:"group_id"`
	Selected bool           `json:"selected"`
	Path     string         `json:"path,omitempty"`
	Dirty    bool           `json:"dirty,omitempty"`
	Revision uint64         `json:"revision,omitempty"`
}

func (m *App) inspectShell() shellInspection {
	s := shellInspection{Width: m.width, Height: m.height, DockRows: m.termDockHeight, OpenEpoch: m.workspaceSwitchGen.Load() + 1}
	if m.Tree != nil {
		s.TreeVisible = m.Tree.Visible
		s.TreeWidth = m.Tree.Width
	}
	if m.termDock != nil {
		s.DockOpen = m.termDock.IsOpen()
		s.DockPanel = m.termDock.ActivePanel()
	}
	if m.Win != nil {
		s.ActiveGroup = m.Win.ActiveGroup()
		s.Groups = m.Win.GroupIDs()
		for _, tab := range m.Win.Tabs() {
			if len(s.Tabs) >= control.MaxPageLimit {
				break
			}
			t := shellTabInspection{ID: tab.ID, Group: tab.Group}
			if active := m.Win.GroupActiveTab(tab.Group); active != nil {
				t.Selected = active.ID == tab.ID
			}
			if ed := m.editorForWindowTab(tab.Title); ed != nil {
				t.Path = ed.FilePath()
				t.Dirty = ed.Modified()
				t.Revision = ed.Revision()
			}
			s.Tabs = append(s.Tabs, t)
		}
	}
	raw, _ := json.Marshal(s)
	stamp := sha256.Sum256(raw)
	if m.shellControlRevision == 0 || stamp != m.shellControlStamp {
		m.shellControlRevision++
		m.shellControlStamp = stamp
	}
	s.Revision = m.shellControlRevision
	return s
}

func (m *App) replyShellControl(msg shellport.ControlRequestMsg, result control.Result) tea.Cmd {
	result.RequestID = msg.Envelope.Request.ID
	if result.OperationID == "" {
		result.OperationID = result.RequestID
	}
	result = coreprotocol.BoundControlResult(msg.Envelope.Request, result, msg.Envelope.Descriptor.Effect != control.Read)
	return func() tea.Msg {
		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()
		if msg.Reply != nil {
			return shellControlReplyMsg{err: msg.Reply(ctx, result)}
		}
		return shellControlReplyMsg{err: msg.Owner.ReplyShellControl(ctx, result)}
	}
}
func (m *App) failShellControl(msg shellport.ControlRequestMsg, code control.FaultCode, message string) tea.Cmd {
	return m.replyShellControl(msg, control.Result{State: control.Failed, Fault: &control.Fault{Code: code, Message: message}})
}

func (m *App) handleShellControl(msg shellport.ControlRequestMsg) tea.Cmd {
	port, ok := m.ws.(shellport.ShellControl)
	if !ok || port != msg.Owner || m.asyncIOStopped.Load() {
		return m.failShellControl(msg, control.Unavailable, "shell owner is detached")
	}
	if cmd, handled := m.handleDiagnosticControl(msg); handled {
		return cmd
	}
	if cmd, handled := m.handleProductControl(msg); handled {
		return cmd
	}
	r := msg.Envelope.Request
	supplied := msg.Envelope.Descriptor
	d, known := ShellCommandDescriptor(r.CommandID)
	var generic *shellControlIntent
	if r.CommandID == "shell.control" || r.CommandID == "shell.describe_control" {
		in := new(shellControlIntent)
		if err := decodeShellInput(r.Input, in); err != nil {
			return m.failShellControl(msg, control.Invalid, err.Error())
		}
		generic = in
		resolved, node, err := m.describeShellControl(*in, port.ControlIdentity())
		if err != nil {
			return m.failShellControl(msg, control.Denied, err.Error())
		}
		if r.CommandID == "shell.describe_control" {
			// Discovery is read-only and does not carry operation authority.
			d, known = ShellCommandDescriptor(r.CommandID)
		} else {
			if in.ExpectedAction != resolved.ID {
				return m.failShellControl(msg, control.Stale, "resolved owner action changed")
			}
			d, known = resolved, true
			d.ID = r.CommandID
			if r.Target.ResourceID != in.ComponentID || r.Preconditions.OpenEpoch == nil || *r.Preconditions.OpenEpoch != node.OpenEpoch || r.Preconditions.Revision == nil || *r.Preconditions.Revision != node.Revision {
				return m.failShellControl(msg, control.Stale, "exact component lifetime and revision required")
			}
		}
	}
	if !known {
		return m.failShellControl(msg, control.Unavailable, "unsupported shell command")
	}
	if r.CommandID == "shell.control" {
		if d.HumanOnly || (d.Effect != control.Read && d.Effect != control.Write && d.Effect != control.Delete) {
			return m.failShellControl(msg, control.Denied, "human/global authority or unsupported host effect cannot be invoked here")
		}
	}
	if supplied.Effect != d.Effect || supplied.HumanOnly != d.HumanOnly || supplied.Scope != d.Scope || supplied.Requires != d.Requires {
		return m.failShellControl(msg, control.Stale, "resolved command effect or requirements changed")
	}
	if r.CommandID != "shell.control" && (supplied.ID != d.ID || supplied.Input.ID != d.Input.ID || supplied.Input.Revision != d.Input.Revision || !bytes.Equal(supplied.Input.Document, d.Input.Document) || supplied.Output.ID != d.Output.ID || supplied.Output.Revision != d.Output.Revision || !bytes.Equal(supplied.Output.Document, d.Output.Document)) {
		return m.failShellControl(msg, control.Stale, "catalog schema changed")
	}
	s := m.inspectShell()
	current := control.Current{Target: port.ControlIdentity(), Preconditions: control.Preconditions{Revision: &s.Revision, OpenEpoch: &s.OpenEpoch}}
	if r.CommandID == "shell.control" {
		_, node, err := m.describeShellControl(*generic, current.Target)
		if err != nil {
			return m.failShellControl(msg, control.Denied, err.Error())
		}
		current.Target.ResourceID = generic.ComponentID
		if node.Target.SessionID != "" {
			if r.Target.SessionID != node.Target.SessionID {
				return m.failShellControl(msg, control.Denied, "exact component session required")
			}
			current.Target.SessionID = node.Target.SessionID
		}
		current.Preconditions = control.Preconditions{Revision: &node.Revision, OpenEpoch: &node.OpenEpoch}
	}
	if r.Target.SessionID != "" {
		if m.ws.SessionsByID()[r.Target.SessionID] == nil {
			return m.failShellControl(msg, control.Unavailable, "bound session unavailable")
		}
		current.Target.SessionID = r.Target.SessionID
	}
	if r.CommandID == "editor.navigate" || r.CommandID == "editor.select" {
		if ed := m.EditorState.Editor; ed != nil && ed.IsOpen() {
			current.Target.ResourceID = ed.DocumentState().DocumentID
			revision := ed.Revision()
			current.Preconditions.Revision = &revision
		}
	}
	if fault := r.ValidateAgainst(supplied, current); fault != nil {
		return m.replyShellControl(msg, control.Result{State: control.Failed, Fault: fault})
	}
	if d.Effect != control.Read && (m.workspaceAttachPending || (r.CommandID != "shell.control" && (m.sessionDeckBlocking() || (m.windowManager != nil && m.windowManager.Focused() != nil)))) {
		return m.failShellControl(msg, control.Denied, "a modal, floating owner or blocking decision owns input")
	}
	result := control.Result{State: control.Applied}
	invalid := func(err error) tea.Cmd { return m.failShellControl(msg, control.Invalid, err.Error()) }
	empty := func() error { return decodeShellInput(r.Input, &struct{}{}) }
	switch r.CommandID {
	case "shell.describe_control":
		resolved, node, err := m.describeShellControl(*generic, current.Target)
		if err != nil {
			return invalid(err)
		}
		result.Output, _ = json.Marshal(struct {
			Descriptor control.Descriptor `json:"descriptor"`
			Node       control.Node       `json:"node"`
		}{resolved, node})
	case "shell.control":
		if m.shellControlPending >= 16 {
			return m.failShellControl(msg, control.Unavailable, "shell operation capacity exceeded before effect")
		}
		owner, localID, err := m.resolveControlHost(generic.ComponentID, port.ControlIdentity())
		if err != nil {
			return m.failShellControl(msg, control.Stale, err.Error())
		}
		if owner.target.SessionID != "" && r.Target.SessionID != owner.target.SessionID {
			return m.failShellControl(msg, control.Stale, "exact session deck owner required")
		}
		// Capture the live owner before an action intentionally replaces its
		// button/page or closes a picker to stage context in the composer.
		done := m.captureShellControl(msg)
		res, err := owner.host.Control(localID, *r.Preconditions.OpenEpoch, r.Preconditions.Revision, generic.Operation)
		if err != nil {
			return m.failShellControl(msg, control.Stale, err.Error())
		}
		if !res.Handled {
			return m.failShellControl(msg, control.Unavailable, "semantic operation was not handled")
		}
		if surface := m.settingsSurface(owner.window); surface != nil {
			if outcome, ok := any(surface).(interface{ ControlActionOutcome(string) error }); ok {
				if err := outcome.ControlActionOutcome(generic.ExpectedAction); err != nil {
					m.forceFrame()
					return tea.Batch(res.Cmd, m.failShellControl(msg, control.OperationFailed, err.Error()))
				}
			}
		}
		actionCmd := m.applyHostPointerActions(res)
		cmd := tea.Batch(res.Cmd, actionCmd)
		if owner.window != nil {
			owner.window.MarkContentDirty()
		}
		if (res.Cmd != nil || actionCmd != nil) && (res.Cmd == nil || actionCmd == nil) && (done.memoryOwner != nil || done.boardOwner != nil || done.pickerOwner != nil || owner.body != nil || m.settingsSurface(owner.window) != nil) {
			work := res.Cmd
			if work == nil {
				work = actionCmd
			}
			return m.awaitCapturedShellControl(done, work, nil)
		} else if _, ordinaryQuestion := owner.body.(*question.Screen); ordinaryQuestion && res.Cmd == nil && actionCmd == nil {
			// Editing an ordinary question is a local draft mutation, not a
			// permission decision merely because its deck blocks input.
		} else if m.DirtyPromptPath != "" || m.sessionDeckBlocking() {
			result.State = control.PendingDecision
		} else if res.Cmd != nil || actionCmd != nil {
			result.State = control.Partial
			result.Fault = &control.Fault{Code: control.OperationFailed, Message: "local action handled; asynchronous owner outcome not yet verified"}
		}
		// Approved generic operations are local controls or synchronous owner
		// navigation. Unknown async owner effects never enter this allowlist.
		m.forceFrame()
		return tea.Batch(cmd, m.replyShellControl(msg, result))
	case "shell.inspect":
		if err := empty(); err != nil {
			return invalid(err)
		}
		result.Output, _ = json.Marshal(s)
		result.Revision = &s.Revision
	case "shell.controls":
		var in control.PageRequest
		if err := decodeShellInput(r.Input, &in); err != nil {
			return invalid(err)
		}
		page, err := m.inspectShellControls(current.Target, in)
		if err != nil {
			return invalid(err)
		}
		// Values are released only by the owning contextual metadata provider.
		result.Output, _ = json.Marshal(page)
	case "tree.set_visible":
		var in struct {
			Visible *bool `json:"visible"`
		}
		if err := decodeShellInput(r.Input, &in); err != nil {
			return invalid(err)
		}
		if in.Visible == nil || m.Tree == nil {
			return invalid(errors.New("visible is required and tree must exist"))
		}
		m.Tree.Visible = *in.Visible
	case "tree.set_width":
		var in struct {
			Columns int `json:"columns"`
		}
		if err := decodeShellInput(r.Input, &in); err != nil {
			return invalid(err)
		}
		if m.Tree == nil || in.Columns < 8 || in.Columns > max(8, m.width-24) {
			return invalid(errors.New("columns outside available tree width"))
		}
		m.Tree.Width = in.Columns
	case "tree.reveal", "tree.filter":
		return m.beginTreeControl(msg)
	case "pane.split":
		var in struct {
			Direction string `json:"direction"`
		}
		if err := decodeShellInput(r.Input, &in); err != nil {
			return invalid(err)
		}
		dir := ui.Horizontal
		if in.Direction == "down" {
			dir = ui.Vertical
		} else if in.Direction != "right" {
			return invalid(errors.New("direction must be right or down"))
		}
		m.configureShell()
		m.rootHost.MarkLayout()
		m.rootHost.Layout()
		before := len(m.Win.GroupIDs())
		m.splitMainGroup(dir, false, 0)
		if len(m.Win.GroupIDs()) == before {
			bounds := m.ensureMainPanes().group(m.Win.ActiveGroup()).Bounds()
			return m.failShellControl(msg, control.Unavailable, fmt.Sprintf("pane is too small to split (%dx%d cells)", bounds.W, bounds.H))
		}
	case "pane.merge":
		if err := empty(); err != nil {
			return invalid(err)
		}
		m.Win.MergeAllGroups()
		m.focusMainGroup(m.Win.ActiveGroup())
	case "pane.focus":
		var in struct {
			Group window.GroupID `json:"group_id"`
		}
		if err := decodeShellInput(r.Input, &in); err != nil {
			return invalid(err)
		}
		if !paneHasGroup(m.Win.PaneRoot(), in.Group) {
			return m.failShellControl(msg, control.Unavailable, "group is missing")
		}
		m.focusMainGroup(in.Group)
	case "pane.resize":
		var in struct {
			Delta int `json:"delta"`
		}
		if err := decodeShellInput(r.Input, &in); err != nil {
			return invalid(err)
		}
		if in.Delta == 0 || in.Delta < -1000 || in.Delta > 1000 {
			return invalid(errors.New("delta must be nonzero within +/-1000"))
		}
		if len(m.Win.GroupIDs()) < 2 {
			return m.failShellControl(msg, control.Unavailable, "no containing split")
		}
		m.resizeMainGroup(in.Delta)
	case "tab.select", "tab.close":
		var in struct {
			Tab window.TabID `json:"tab_id"`
		}
		if err := decodeShellInput(r.Input, &in); err != nil {
			return invalid(err)
		}
		index := m.Win.IndexOf(in.Tab)
		if index < 0 {
			return m.failShellControl(msg, control.Unavailable, "tab is missing")
		}
		if r.CommandID == "tab.select" {
			m.Win.SwitchTo(index)
			m.handleTabSwitch()
			m.releaseTabBarFocus()
		} else {
			cmd := m.handleAgentTabClose(index)
			if m.DirtyPromptPath != "" {
				result.State = control.PendingDecision
			} else if m.Win.IndexOf(in.Tab) >= 0 {
				return m.failShellControl(msg, control.Unavailable, "tab cannot be closed")
			}
			return tea.Batch(cmd, m.replyShellControl(msg, result))
		}
	case "file.open":
		var in struct {
			Path   string `json:"path"`
			Line   int    `json:"line"`
			Column int    `json:"column"`
		}
		if err := decodeShellInput(r.Input, &in); err != nil {
			return invalid(err)
		}
		if in.Path == "" || in.Line < 0 || in.Column < 0 {
			return invalid(errors.New("path required; line and column are zero-based nonnegative"))
		}
		cmd := m.openPaneFiles([]string{in.Path}, m.Win.ActiveGroup())
		if cmd == nil {
			return m.failShellControl(msg, control.Unavailable, "file owner unavailable")
		}
		return m.awaitShellControl(msg, cmd, func(completion tea.Msg) tea.Msg {
			p := completion.(paneFilesOpenedMsg)
			p.position = &editor.LSPDiagnostic{Line: in.Line, Col: in.Column, EndLine: in.Line, EndCol: in.Column}
			return p
		})
	case "editor.navigate", "editor.select":
		var in struct {
			Line      int `json:"line"`
			Column    int `json:"column"`
			EndLine   int `json:"end_line"`
			EndColumn int `json:"end_column"`
		}
		if err := decodeShellInput(r.Input, &in); err != nil {
			return invalid(err)
		}
		ed := m.EditorState.Editor
		if ed == nil || !ed.IsOpen() {
			return m.failShellControl(msg, control.Unavailable, "editor is unavailable")
		}
		if in.Line < 0 || in.Column < 0 || in.EndLine < 0 || in.EndColumn < 0 {
			return invalid(errors.New("positions must be nonnegative"))
		}
		ed.SetCursorPosition(in.Line, in.Column)
		if r.CommandID == "editor.select" {
			ed.SetSelection(in.Line, in.Column, in.EndLine, in.EndColumn)
		}
		ed.ScrollToCursor()
	case "terminal.open":
		if err := empty(); err != nil {
			return invalid(err)
		}
		cmd := m.openNewTerminal()
		if cmd == nil {
			return m.failShellControl(msg, control.Unavailable, "terminal service unavailable")
		}
		return m.awaitShellControl(msg, cmd, nil)
	case "terminal.show":
		var in struct {
			ID string `json:"id"`
		}
		if err := decodeShellInput(r.Input, &in); err != nil {
			return invalid(err)
		}
		if m.termMgr() == nil {
			return m.failShellControl(msg, control.Unavailable, "terminal service unavailable")
		}
		if _, ok := m.termMgr().Get(in.ID); !ok {
			return m.failShellControl(msg, control.Unavailable, "terminal missing")
		}
		m.focusTerminal(in.ID)
		result.ResourceIDs = []string{in.ID}
	case "terminal.hide", "panel.hide":
		if err := empty(); err != nil {
			return invalid(err)
		}
		m.hideWorkspacePanel()
	case "terminal.resize_dock":
		var in struct {
			Rows int `json:"rows"`
		}
		if err := decodeShellInput(r.Input, &in); err != nil {
			return invalid(err)
		}
		if in.Rows < minTermDockHeight || in.Rows > m.height-8 {
			return invalid(errors.New("rows outside available dock height"))
		}
		m.setTermDockHeight(in.Rows)
	case "panel.show":
		var in struct {
			ID string `json:"id"`
		}
		if err := decodeShellInput(r.Input, &in); err != nil {
			return invalid(err)
		}
		if m.termDock == nil {
			return m.failShellControl(msg, control.Unavailable, "workspace dock unavailable")
		}
		if m.termDock.IsOpen() && m.termDock.ActivePanel() == in.ID {
			break
		}
		switch in.ID {
		case termdock.PanelMemory:
			_, cmd := m.toggleMemoryBrowser()
			if cmd != nil {
				return m.awaitShellControl(msg, cmd, nil)
			}
		case termdock.PanelBoard:
			cmd := m.openBoardBrowser()
			if cmd == nil {
				return m.failShellControl(msg, control.Unavailable, "board unavailable")
			}
			return m.awaitShellControl(msg, cmd, nil)
		case termdock.PanelGit:
			cmd := m.openGitPanel(gitpanel.ModeChanges)
			if cmd == nil {
				return m.failShellControl(msg, control.Unavailable, "git unavailable")
			}
			return m.awaitShellControl(msg, cmd, nil)
		case termdock.PanelTerminal:
			if m.termDock.Empty() {
				return m.failShellControl(msg, control.Unavailable, "no docked terminal; use terminal.open to create one with execute authority, or terminal.show for an existing terminal")
			}
			m.toggleWorkspacePanel(in.ID)
		case termdock.PanelPerformance:
			// Reuse the product's lazy mount, placement and refresh lifecycle.
			cmd := m.togglePerformancePanel()
			m.forceFrame()
			return tea.Batch(cmd, m.replyShellControl(msg, result))
		case termdock.PanelProblems, termdock.PanelNotices, termdock.PanelWorkspace:
			m.toggleWorkspacePanel(in.ID)
		default:
			return m.failShellControl(msg, control.Unavailable, "panel is unsupported by this command")
		}
	case "settings.open":
		var in struct {
			Scope string `json:"scope"`
		}
		if err := decodeShellInput(r.Input, &in); err != nil {
			return invalid(err)
		}
		switch in.Scope {
		case "application":
			m.openApplicationSettings()
		case "workspace":
			m.openWorkspaceSettings()
		case "session":
			if m.ActiveSession == nil {
				return m.failShellControl(msg, control.Unavailable, "session unavailable")
			}
			m.openSessionSettings()
		default:
			return invalid(errors.New("scope must be application, workspace or session"))
		}
	default:
		return m.failShellControl(msg, control.Unavailable, "unsupported shell command")
	}
	m.forceFrame()
	return m.replyShellControl(msg, result)
}

type shellControlIntent struct {
	ComponentID    string              `json:"component_id"`
	Operation      ui.ControlOperation `json:"operation"`
	ExpectedAction string              `json:"expected_action,omitempty"`
}

func (m *App) describeShellControl(in shellControlIntent, target control.Target) (control.Descriptor, control.Node, error) {
	if in.ComponentID == "" || m.rootHost == nil {
		return control.Descriptor{}, control.Node{}, errors.New("component owner unavailable")
	}
	owner, localID, err := m.resolveControlHost(in.ComponentID, target)
	if err != nil {
		return control.Descriptor{}, control.Node{}, err
	}
	d, err := owner.host.DescribeControl(localID, in.Operation)
	if err != nil {
		return d, control.Node{}, err
	}
	if surface := m.settingsSurface(owner.window); surface != nil {
		if metadata, ok := any(surface).(interface {
			ControlMetadata(ui.ComponentID, string) (control.Effect, bool, error)
		}); ok {
			effect, human, err := metadata.ControlMetadata(localID, d.ID)
			if err != nil {
				return d, control.Node{}, err
			}
			if effect != "" {
				d.Effect, d.HumanOnly = effect, human
			}
		}
	}
	if metadata, ok := owner.body.(interface {
		ControlMetadata(ui.ComponentID, string) (control.Effect, bool, error)
	}); ok {
		effect, human, err := metadata.ControlMetadata(localID, d.ID)
		if err != nil {
			return d, control.Node{}, err
		}
		if effect != "" {
			d.Effect, d.HumanOnly = effect, human
		}
	}
	if effect, human, err := m.describeLocalControl(owner, localID, d.ID, in.Operation); err != nil {
		return d, control.Node{}, err
	} else if effect != "" {
		d.Effect, d.HumanOnly = effect, human
	}
	// Explicit actual action IDs, never text labels or registry membership as a
	// grant. Forms in settings/permission/trust surfaces are not in this Host.
	if d.Effect == "" {
		switch d.ID {
		case "tabs.activate", "tabs.close", "tree.select", ui.ActionSplitResize,
			"workspace.panel.hide", "workspace.panel.tab", "workspace.panel.window", "terminal.picker":
			d.Effect = control.Write
		default:
			return d, control.Node{}, errors.New("owner action requires contextual authority not offered by this shell")
		}
	}
	if strings.HasPrefix(d.ID, "ui.control.") {
		switch in.Operation.Verb {
		case "scroll", "set_offset", "select", "select_range", "set_value", "replace", "undo", "redo", "dismiss":
		default:
			return d, control.Node{}, errors.New("local operation is not resolved")
		}
	}
	node, err := owner.host.InspectControl(localID, owner.target)
	if err != nil {
		return d, control.Node{}, err
	}
	node = qualifyControlNode(node, owner)
	node.Actions = nil
	node.Target.ResourceID = in.ComponentID
	return d, node, nil
}

func (m *App) awaitShellControl(msg shellport.ControlRequestMsg, cmd tea.Cmd, transform func(tea.Msg) tea.Msg) tea.Cmd {
	return m.awaitCapturedShellControl(m.captureShellControl(msg), cmd, transform)
}

func (m *App) captureShellControl(msg shellport.ControlRequestMsg) shellControlDoneMsg {
	done := shellControlDoneMsg{request: msg, generation: m.workspaceSwitchGen.Load()}
	if msg.Envelope.Request.CommandID == "shell.control" {
		var intent shellControlIntent
		if decodeShellInput(msg.Envelope.Request.Input, &intent) == nil {
			if owner, localID, err := m.resolveControlHost(intent.ComponentID, msg.Envelope.Request.Target); err == nil {
				done.deckOwner = owner.body
				done.settingsOwner = m.settingsSurface(owner.window)
				if done.settingsOwner != nil {
					done.settingsModel = done.settingsOwner.Model()
				}
				if owner.window == nil && strings.HasPrefix(string(localID), "memorybrowser/") && m.memoryBrowserVisible() {
					done.memoryOwner, done.memoryEpoch = m.memoryBrowser, m.memoryBrowser.Epoch()
				}
				if owner.window == nil && strings.HasPrefix(string(localID), "boardbrowser/") && m.termDock != nil && m.termDock.IsOpen() && m.termDock.ActivePanel() == termdock.PanelBoard {
					done.boardOwner = m.boardBrowser
				}
				if owner.window != nil && m.capabilityPicker != nil {
					if frame, ok := owner.window.Content.(*ui.WindowFrameWidget); ok && frame.Body() == m.capabilityPicker.screen {
						done.pickerOwner = m.capabilityPicker
					}
				}
			}
		}
	}
	return done
}

func (m *App) awaitCapturedShellControl(done shellControlDoneMsg, cmd tea.Cmd, transform func(tea.Msg) tea.Msg) tea.Cmd {
	if m.shellControlPending >= 16 {
		return m.failShellControl(done.request, control.Unavailable, "shell operation capacity exceeded")
	}
	m.shellControlPending++
	return func() tea.Msg {
		done.completion = cmd()
		if transform != nil {
			done.completion = transform(done.completion)
		}
		return done
	}
}

func (m *App) finishShellControl(done shellControlDoneMsg) tea.Cmd {
	if m.shellControlPending > 0 {
		m.shellControlPending--
	}
	msg := done.request
	valid := m.shellControlStillOwned(msg, done.generation)
	if msg.Envelope.Request.CommandID == "shell.control" {
		// Component fences are not shell inspection fences. Source revision changes
		// after the admitted edit are expected; retain the exact mounted lifetime.
		probe := msg
		probe.Envelope = msg.Envelope.Clone()
		probe.Envelope.Request.Preconditions = control.Preconditions{}
		valid = m.shellControlStillOwned(probe, done.generation)
		if done.settingsOwner != nil {
			// Save freezes/rebuilds controls; Close can remove its own button.
			// Completion belongs to the captured surface/model, not that button.
			body := m.settingsSurface(m.overlayHost.Window(ui.OverlayWorkspaceSettings))
			valid = valid && body == done.settingsOwner && body.Model() == done.settingsModel
		} else if done.memoryOwner != nil {
			valid = valid && m.memoryBrowserVisible() && m.memoryBrowser == done.memoryOwner && m.memoryBrowser.Epoch() == done.memoryEpoch
		} else if done.boardOwner != nil {
			valid = valid && m.boardBrowser == done.boardOwner && m.termDock != nil && m.termDock.IsOpen() && m.termDock.ActivePanel() == termdock.PanelBoard
		} else if done.pickerOwner != nil {
			valid = valid && m.capabilityPicker == done.pickerOwner
		} else if done.deckOwner != nil {
			var intent shellControlIntent
			if decodeShellInput(msg.Envelope.Request.Input, &intent) != nil {
				valid = false
			} else if owner, _, err := m.resolveControlHost(intent.ComponentID, msg.Envelope.Request.Target); err != nil || owner.body != done.deckOwner {
				valid = false
			}
		} else {
			var intent shellControlIntent
			if decodeShellInput(msg.Envelope.Request.Input, &intent) != nil {
				valid = false
			} else if owner, localID, err := m.resolveControlHost(intent.ComponentID, msg.Envelope.Request.Target); err != nil {
				valid = false
			} else {
				node, err := owner.host.InspectControl(localID, owner.target)
				valid = valid && err == nil && msg.Envelope.Request.Preconditions.OpenEpoch != nil && node.OpenEpoch == *msg.Envelope.Request.Preconditions.OpenEpoch
			}
		}
	}
	result := control.Result{State: control.Applied}
	var follow tea.Cmd
	switch completion := done.completion.(type) {
	case sessionsettings.SaveMsg:
		if !valid || completion.Target != done.deckOwner {
			return m.failShellControl(msg, control.Stale, "session settings owner changed")
		}
		cmd, err := m.prepareSessionSettingsSave(completion)
		if err != nil {
			return m.failShellControl(msg, control.OperationFailed, err.Error())
		}
		return m.awaitShellControl(msg, cmd, nil)
	case sessionsettings.ResetMsg, sessionsettings.SaveAsWorkspaceMsg, settingsui.ResetSectionMsg:
		if !valid {
			return m.failShellControl(msg, control.Stale, "settings owner changed")
		}
		_, cmd := m.Update(completion)
		if cmd == nil {
			return m.failShellControl(msg, control.Unavailable, "settings operation unavailable")
		}
		return m.awaitShellControl(msg, cmd, nil)
	case settingsShortcutResultMsg:
		if !valid || completion.owner != m.ws || completion.generation != m.workspaceSwitchGen.Load() {
			return m.failShellControl(msg, control.Stale, "settings owner changed during save")
		}
		m.applySettingsShortcut(completion)
		if completion.result.Err != nil {
			return m.failShellControl(msg, control.OperationFailed, completion.result.Err.Error())
		}
	case sessionSettingsSaveResultMsg:
		if !valid || completion.target != done.deckOwner || completion.workspace != m.ws || completion.generation != done.generation {
			return m.failShellControl(msg, control.Stale, "session settings owner changed during save")
		}
		if completion.result.Err != nil {
			return m.failShellControl(msg, control.OperationFailed, completion.result.Err.Error())
		}
		if completion.result.ResultOwner() == nil || m.ws.SessionsByID()[completion.result.ResultOwner().SessionID] != completion.result.ResultOwner() {
			return m.failShellControl(msg, control.Stale, "session receipt owner changed")
		}
		_, follow = m.Update(completion)
		result.Revision = &completion.result.Snapshot.Revision
	case sessionsettings.FetchModelsMsg:
		if !valid {
			return m.failShellControl(msg, control.Stale, "session picker changed")
		}
		_, follow = m.Update(completion)
		// Discovery is pending; the model picker itself is already mounted.
		result.State = control.Admitted
	case sessionsettings.CancelMsg:
		if !valid {
			return m.failShellControl(msg, control.Stale, "session settings changed")
		}
		_, follow = m.Update(completion)
	case question.ResultMsg:
		if !valid {
			return m.failShellControl(msg, control.Stale, "question owner changed")
		}
		cmd, err := m.prepareQuestionReply(completion.Target, completion.Result, false, msg.Envelope.Request.ID)
		if err != nil {
			return m.failShellControl(msg, control.Denied, err.Error())
		}
		return m.awaitShellControl(msg, cmd, nil)
	case question.CancelMsg:
		if !valid {
			return m.failShellControl(msg, control.Stale, "question owner changed")
		}
		cmd, err := m.prepareQuestionReply(completion.Target, question.Result{}, true, msg.Envelope.Request.ID)
		if err != nil {
			return m.failShellControl(msg, control.Denied, err.Error())
		}
		return m.awaitShellControl(msg, cmd, nil)
	case questionReplyMsg:
		if completion.err != nil || !completion.receipt.Accepted || completion.receipt.BlockID != completion.blockID {
			return m.failShellControl(msg, control.OperationFailed, "question was not acknowledged")
		}
		m.applyQuestionReply(completion)
	case settingsui.SaveMsg:
		if !valid {
			return m.failShellControl(msg, control.Stale, "settings owner changed before save")
		}
		cmd := m.savePreferences(completion)
		if cmd == nil {
			return m.failShellControl(msg, control.Stale, "settings draft is no longer saving")
		}
		return m.awaitShellControl(msg, cmd, nil)
	case preferencesSavedMsg:
		if !valid || completion.owner != m.ws || completion.generation != m.workspaceSwitchGen.Load() {
			return m.failShellControl(msg, control.Stale, "settings owner changed during save")
		}
		body := m.settingsSurface(m.overlayHost.Window(ui.OverlayWorkspaceSettings))
		if body == nil || body.Model() != completion.target || !completion.target.Saving() {
			return m.failShellControl(msg, control.Stale, "settings lifetime changed during save")
		}
		m.applyPreferencesSaved(completion)
		if completion.result.Err != nil {
			return m.failShellControl(msg, control.OperationFailed, completion.result.Err.Error())
		}
	case settingsui.CancelMsg:
		if !valid {
			return m.failShellControl(msg, control.Stale, "settings owner changed")
		}
		_, follow = m.Update(completion)
	case settingsui.FetchModelsMsg:
		if !valid {
			return m.failShellControl(msg, control.Stale, "settings model selector owner changed")
		}
		_, follow = m.Update(completion)
		// Opening the retained catalog editor succeeded. Optional model discovery
		// continues through its owner-fenced background result, not a false save.
		result.State = control.Admitted
	case settingsui.ThemePreviewMsg, settingsui.FramePreviewMsg, settingsui.PromptMsg:
		if !valid {
			return m.failShellControl(msg, control.Stale, "settings owner changed")
		}
		_, follow = m.Update(completion)
		if _, decision := completion.(settingsui.PromptMsg); decision {
			result.State = control.PendingDecision
		}
	case memorybrowser.SearchMsg:
		if !valid {
			return m.failShellControl(msg, control.Stale, "memory owner changed")
		}
		cmd := m.handleMemoryBrowserSearch(completion)
		if cmd == nil {
			return m.failShellControl(msg, control.Unavailable, "memory search unavailable")
		}
		return m.awaitCapturedShellControl(done, cmd, nil)
	case memorybrowser.DetailMsg:
		if !valid {
			return m.failShellControl(msg, control.Stale, "memory detail owner changed")
		}
		cmd := m.handleMemoryBrowserDetail(completion)
		if cmd == nil {
			return m.failShellControl(msg, control.Unavailable, "memory detail unavailable")
		}
		return m.awaitCapturedShellControl(done, cmd, nil)
	case memoryBrowserDetailResultMsg:
		if !valid || completion.Err != nil || !m.ownsMemoryBrowserResult(completion.Owner, completion.WorkspaceGeneration, completion.Request.Target) || !completion.Request.Target.OwnsDetail(completion.Request) {
			return m.failShellControl(msg, control.OperationFailed, "memory detail failed or owner changed")
		}
		m.applyMemoryBrowserDetail(completion)
	case memorybrowser.MutationMsg:
		if !valid {
			return m.failShellControl(msg, control.Stale, "memory mutation owner changed")
		}
		cmd := m.handleMemoryBrowserMutation(completion)
		if cmd == nil {
			return m.failShellControl(msg, control.Unavailable, "memory mutation unavailable")
		}
		return m.awaitCapturedShellControl(done, cmd, nil)
	case memoryBrowserMutationResultMsg:
		if completion.Err != nil {
			// Restore the exact editor's error state; failure never clears its draft.
			if valid {
				m.applyMemoryBrowserMutation(completion)
			}
			return m.failShellControl(msg, control.OperationFailed, completion.Err.Error())
		}
		result.ResourceIDs = []string{completion.Request.ID}
		if !valid || !m.ownsMemoryBrowserResult(completion.Owner, completion.WorkspaceGeneration, completion.Request.Target) || !completion.Request.Target.OwnsMutation(completion.Request) {
			result.State, result.Fault = control.Partial, &control.Fault{Code: control.Stale, Message: "memory mutation committed; original panel can no longer present it"}
			break
		}
		follow = m.applyMemoryBrowserMutation(completion)
	case memorybrowser.CancelMsg:
		if !valid || completion.Target != m.memoryBrowser || completion.Epoch != m.memoryBrowser.Epoch() {
			return m.failShellControl(msg, control.Stale, "memory panel owner changed")
		}
		m.handleMemoryBrowserCancel(completion)
	case memoryBrowserSearchResultMsg:
		if !valid || completion.Err != nil || m.memoryBrowser == nil || !m.ownsMemoryBrowserResult(completion.Owner, completion.WorkspaceGeneration, completion.Request.Target) || !m.memoryBrowser.OwnsSearch(completion.Request) {
			return m.failShellControl(msg, control.OperationFailed, "memory panel load failed or owner changed")
		}
		m.applyMemoryBrowserSearch(completion)
		if !m.memoryBrowserVisible() {
			return m.failShellControl(msg, control.Stale, "memory panel hidden")
		}
	case boardResultMsg:
		if !valid || completion.err != nil || completion.generation != m.workspaceSwitchGen.Load() || completion.request.Target != m.boardBrowser || !m.boardBrowser.OwnsRequest(completion.request) {
			if completion.committed {
				result.State, result.Fault = control.Partial, &control.Fault{Code: control.OperationFailed, Message: "Board read marker committed; original panel load failed or owner changed"}
				break
			}
			return m.failShellControl(msg, control.OperationFailed, "board panel load failed or owner changed")
		}
		m.applyBoardResult(completion)
		if !m.termDock.IsOpen() || m.termDock.ActivePanel() != termdock.PanelBoard {
			return m.failShellControl(msg, control.Stale, "board panel hidden")
		}
	case capabilityPickerResultMsg:
		// Toggles are session commits, never superseded by a later refresh.
		if !valid || completion.owner != m.capabilityPicker || (done.pickerOwner != nil && completion.owner != done.pickerOwner) || completion.toggle == nil && completion.sequence != completion.owner.sequence {
			return m.failShellControl(msg, control.Stale, "capability modal owner changed")
		}
		m.applyCapabilityPickerResult(completion)
		if completion.err != nil {
			return m.failShellControl(msg, control.OperationFailed, "capabilities could not be refreshed or updated")
		}
	case capabilityContextMsg:
		if completion.result.Err != nil {
			return m.failShellControl(msg, control.OperationFailed, completion.result.Err.Error())
		}
		if !valid || completion.generation != done.generation || completion.owner != m.ActiveSession || completion.agentID != m.activeAgentTabID() || completion.draft != m.Input.Value() {
			result.State, result.Fault = control.Partial, &control.Fault{Code: control.Stale, Message: "capability reference created; changed composer was not overwritten"}
			if completion.reference.ID != "" {
				result.ResourceIDs = []string{completion.reference.ID}
			}
			break
		}
		_, follow = m.Update(completion)
		result.ResourceIDs = []string{completion.reference.ID}
	case sourceControlResultMsg:
		if !valid || completion.err != nil || completion.gen == 0 || completion.gen != m.gitRefreshGen.Load() {
			return m.failShellControl(msg, control.OperationFailed, "git panel load failed or owner changed")
		}
		follow = m.applySourceControlResult(completion)
		if !m.gitPanelOpen() {
			return m.failShellControl(msg, control.Stale, "git panel hidden")
		}
	case treeControlCompletion:
		return m.finishTreeControl(done, completion, valid)
	case paneFilesOpenedMsg:
		valid = valid && completion.seq == m.paneFileOpenSeq && completion.manager == m.BufferMgr && completion.win == m.Win && m.Win.HasGroup(completion.group)
		if !valid {
			follow = releasePaneDocuments(completion)
			result.State = control.Failed
			result.Fault = &control.Fault{Code: control.Stale, Message: "file owner changed during preparation"}
			break
		}
		follow = m.applyPaneFilesOpened(completion)
		if len(completion.files) != 1 {
			result.State = control.Failed
			result.Fault = &control.Fault{Code: control.OperationFailed, Message: "expected one prepared file"}
		} else {
			file := completion.files[0]
			tab := m.Win.TabByTitle(m.editorTabTitle(file.path))
			if file.err != nil || tab == nil {
				result.State = control.Failed
				result.Fault = &control.Fault{Code: control.OperationFailed, Message: "file was not installed"}
			} else {
				result.ResourceIDs = []string{fmt.Sprint(tab.ID)}
				if file.snapshot != nil {
					result.ResourceIDs = append(result.ResourceIDs, file.snapshot.ID)
				}
			}
		}
	case terminalCreatedMsg:
		if completion.info.ID != "" {
			result.ResourceIDs = []string{completion.info.ID}
		}
		if !valid || completion.err != nil {
			result.State = control.Failed
			if completion.info.ID != "" {
				result.State = control.Partial
			}
			result.Fault = &control.Fault{Code: control.OperationFailed, Message: "terminal was not mounted; any returned resource is retained, not recreated"}
			break
		}
		follow = m.applyTerminalCreated(completion)
		if m.termScreens[completion.info.ID] == nil {
			result.State = control.Partial
			result.Fault = &control.Fault{Code: control.OperationFailed, Message: "created terminal was not mounted"}
		}
	default:
		if cmd, handled := m.finishProductControl(done); handled {
			return cmd
		}
		result.State = control.Failed
		result.Fault = &control.Fault{Code: control.OperationFailed, Message: "unexpected shell completion"}
	}
	m.forceFrame()
	return tea.Batch(follow, m.replyShellControl(msg, result))
}

func (m *App) prepareSessionSettingsSave(msg sessionsettings.SaveMsg) (tea.Cmd, error) {
	session, _ := m.sessionProjections.projectionForSettings(msg.Target)
	if session == nil {
		return nil, errors.New("session settings owner unavailable")
	}
	owner, generation := m.ws, m.workspaceSwitchGen.Load()
	operation, err := owner.PrepareSessionSettingsSave(session.SessionID, msg.Settings)
	if err != nil {
		return nil, err
	}
	return func() tea.Msg {
		return sessionSettingsSaveResultMsg{target: msg.Target, workspace: owner, generation: generation, result: operation.Run()}
	}, nil
}

type questionReplyMsg struct {
	target           *question.Screen
	workspace        shellport.Workspace
	generation       uint64
	agentID, blockID string
	receipt          coreprotocol.QuestionControlResult
	err              error
}

func (m *App) prepareQuestionReply(target *question.Screen, result question.Result, cancelled bool, controlID string) (tea.Cmd, error) {
	state := m.promptForScreen(target)
	if state == nil {
		return nil, errors.New("question prompt unavailable")
	}
	owner, generation := m.ws, m.workspaceSwitchGen.Load()
	req := state.request
	if controlID != "" {
		port, ok := owner.(shellport.QuestionControl)
		if !ok {
			return nil, errors.New("receipt-aware question control unavailable")
		}
		session := owner.SessionForAgent(req.agentID)
		if session == nil {
			return nil, errors.New("question session unavailable")
		}
		input := coreprotocol.QuestionControlRequest{ControlRequestID: controlID, SessionID: session.SessionID, AgentID: req.agentID, BlockID: req.blockID, Answers: result.Answers, Questions: result.Questions, Cancelled: cancelled}
		return func() tea.Msg {
			ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
			defer cancel()
			receipt, err := port.ResolveQuestionControl(ctx, input)
			return questionReplyMsg{target: target, workspace: owner, generation: generation, agentID: req.agentID, blockID: req.blockID, receipt: receipt, err: err}
		}, nil
	}
	port, ok := owner.(shellport.QuestionReceipt)
	if !ok {
		return nil, errors.New("question receipt unavailable")
	}
	return func() tea.Msg {
		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()
		receipt, err := port.ResolveQuestionReceipt(ctx, req.agentID, req.blockID, result.Answers, result.Questions, cancelled)
		return questionReplyMsg{target: target, workspace: owner, generation: generation, agentID: req.agentID, blockID: req.blockID, receipt: receipt, err: err}
	}, nil
}
func (m *App) applyQuestionReply(msg questionReplyMsg) {
	if msg.workspace != m.ws || msg.generation != m.workspaceSwitchGen.Load() {
		return
	}
	if msg.err != nil || !msg.receipt.Accepted || msg.receipt.BlockID != msg.blockID {
		m.Feedback.NotifyError("Question response was not acknowledged; the draft is retained")
		return
	}
	state := m.promptForScreen(msg.target)
	if state == nil || state.request.agentID != msg.agentID || state.request.blockID != msg.blockID {
		return
	}
	m.removeAgentPrompt(state)
	m.openNextAgentPrompt()
}
