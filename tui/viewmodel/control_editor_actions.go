package viewmodel

import (
	tea "charm.land/bubbletea/v2"
	"crude/indentation"
	"crude/shellport"
	"crude/tui/editor"
	"crude/tui/ui"
	"crude/types/control"
	"encoding/json"
)

func (m *App) handleEditorActionControl(msg shellport.ControlRequestMsg) (tea.Cmd, bool) {
	r := msg.Envelope.Request
	fail := func(code control.FaultCode, text string) (tea.Cmd, bool) {
		return m.failShellControl(msg, code, text), true
	}
	if m.EditorState == nil || m.EditorState.Editor == nil {
		return fail(control.Unavailable, "Open an editable document first.")
	}
	ed := m.EditorState.Editor
	if !ed.IsOpen() || ed.DocumentState().DocumentID != r.Target.ResourceID || m.focusTarget() != FocusEditor {
		return fail(control.Stale, "The focused editor changed. Inspect it again.")
	}
	result := control.Result{State: control.Applied}
	if r.CommandID == "editor.indentation" {
		var input struct {
			Token   string               `json:"view_token"`
			Choices *indentation.Choices `json:"choices"`
		}
		if err := decodeShellInput(r.Input, &input); err != nil {
			return fail(control.Invalid, err.Error())
		}
		if input.Choices == nil {
			return fail(control.Invalid, "Supply indentation choices. Use an empty object to inherit.")
		}
		if ed.ActionToken() != input.Token {
			return fail(control.Stale, "The editor changed. Inspect it again.")
		}
		if err := ed.SetIndentationChoices(*input.Choices); err != nil {
			return fail(control.Invalid, err.Error())
		}
		result.Output, _ = json.Marshal(ed.Indentation())
		m.forceFrame()
		return m.replyShellControl(msg, result), true
	}

	if r.CommandID == "editor.actions" {
		if err := decodeShellInput(r.Input, &struct{}{}); err != nil {
			return fail(control.Invalid, err.Error())
		}
		type action struct {
			ID       ui.ActionID `json:"id"`
			Label    string      `json:"label"`
			Shortcut string      `json:"shortcut,omitempty"`
			Mutates  bool        `json:"mutates_text"`
			Enabled  bool        `json:"enabled"`
		}
		out := struct {
			Token              string                   `json:"view_token"`
			Selections         []editor.SourceSelection `json:"selections"`
			Actions            []action                 `json:"actions"`
			Indentation        indentation.Choices      `json:"indentation"`
			IndentationChoices indentation.Choices      `json:"indentation_choices"`
			IndentationSource  string                   `json:"indentation_source"`
		}{Selections: ed.SourceSelections(), Token: ed.ActionToken(), Indentation: ed.Indentation(), IndentationChoices: ed.IndentationChoices(), IndentationSource: ed.IndentationSource()}
		for _, a := range ui.EditorActions() {
			out.Actions = append(out.Actions, action{ID: a.ID, Label: a.Label, Shortcut: ui.FormatShortcut(m.actionRegistry.HintFor(a.ID)), Mutates: a.MutatesText, Enabled: !ed.ReadOnly() || !a.MutatesText})
		}
		result.Output, _ = json.Marshal(out)
		return m.replyShellControl(msg, result), true
	}
	var in struct {
		ID    ui.ActionID `json:"action_id"`
		Token string      `json:"view_token"`
	}
	if err := decodeShellInput(r.Input, &in); err != nil {
		return fail(control.Invalid, err.Error())
	}
	a, ok := ui.FindEditorAction(in.ID)
	if !ok {
		return fail(control.Invalid, "Unknown editor action. Inspect the available actions.")
	}
	if ed.ActionToken() != in.Token {
		return fail(control.Stale, "The editor changed. Inspect it again.")
	}
	if ed.ReadOnly() && a.MutatesText {
		return fail(control.Denied, "This document is read-only.")
	}
	cmd := m.runEditorAction(ed, in.Token, in.ID)
	if a.MutatesText || ed.DocumentState().HistoryQueued {
		result.State = control.Admitted
	}
	result.ResourceIDs = []string{r.Target.ResourceID}
	m.forceFrame()
	return tea.Batch(cmd, m.replyShellControl(msg, result)), true
}
