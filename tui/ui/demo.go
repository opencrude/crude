package ui

import (
	"crude/tui/surface"
	"crude/tui/theme"
)

// DemoGallery builds a non-product Host tree that exercises every
// control type in every visual state at 80x30 under any reference theme.
// This is the Phase 3 state gallery: it proves that all controls
// render correctly and that the theme token system covers all states.
//
// Layout: two-column grid. Left column = control label, right column
// = rendered state variants. Each row is one control type.
func DemoGallery(th *theme.Theme) *Host {
	tokens := NewTokenSet(th)

	// Root stack: title + grid of control rows.
	title := NewLabel(NewID(RootID, "title"), "Crude UI — State Gallery", th)
	if tokens != nil {
		title.SetTokens(tokens)
		title.SetColorRole(ColorAccent)
	}

	// ── TextField states ──
	tfDefault := newDemoTextField(tokens, "default", "")
	tfFocused := newDemoTextField(tokens, "focused", "")
	tfPlaceholder := newDemoTextField(tokens, "", "placeholder…")
	tfDisabled := newDemoTextField(tokens, "disabled", "")
	tfDisabled.SetDisabled(true)

	// ── Button states ──
	btnDefault := newDemoButton(tokens, "Default")
	btnDisabled := newDemoButton(tokens, "Disabled")
	btnDisabled.SetDisabled(true)

	// ── List states ──
	listDefault := newDemoList(tokens, 0, false)
	listSelected := newDemoList(tokens, 2, false)

	// ── Label variants ──
	labelDefault := NewLabel(NewID(RootID, "lbl-default"), "Default label", th)
	labelAccent := NewLabel(NewID(RootID, "lbl-accent"), "Accent label", th)
	if tokens != nil {
		labelAccent.SetTokens(tokens)
		labelAccent.SetColorRole(ColorAccent)
	}
	labelMuted := NewLabel(NewID(RootID, "lbl-muted"), "Muted label", th)
	if tokens != nil {
		labelMuted.SetTokens(tokens)
		labelMuted.SetColorRole(ColorMuted)
	}
	labelError := NewLabel(NewID(RootID, "lbl-error"), "Error label", th)
	if tokens != nil {
		labelError.SetTokens(tokens)
		labelError.SetColorRole(ColorError)
	}
	labelSuccess := NewLabel(NewID(RootID, "lbl-success"), "Success label", th)
	if tokens != nil {
		labelSuccess.SetTokens(tokens)
		labelSuccess.SetColorRole(ColorSuccess)
	}

	// ── WindowFrame variants ──
	frameDialog := newDemoFrame(tokens, FrameDialog, "Dialog Frame", true)
	framePopover := newDemoFrame(tokens, FramePopover, "Popover Frame", false)
	frameWindow := newDemoFrame(tokens, FrameWindow, "Window Frame", true)

	// ── Section headers ──
	secTextFields := NewLabel(NewID(RootID, "sec-tf"), "── TextFields ──", th)
	if tokens != nil {
		secTextFields.SetTokens(tokens)
		secTextFields.SetColorRole(ColorMuted)
	}
	secButtons := NewLabel(NewID(RootID, "sec-btn"), "── Buttons ──", th)
	if tokens != nil {
		secButtons.SetTokens(tokens)
		secButtons.SetColorRole(ColorMuted)
	}
	secLists := NewLabel(NewID(RootID, "sec-list"), "── Lists ──", th)
	if tokens != nil {
		secLists.SetTokens(tokens)
		secLists.SetColorRole(ColorMuted)
	}
	secLabels := NewLabel(NewID(RootID, "sec-lbl"), "── Labels ──", th)
	if tokens != nil {
		secLabels.SetTokens(tokens)
		secLabels.SetColorRole(ColorMuted)
	}
	secFrames := NewLabel(NewID(RootID, "sec-frame"), "── WindowFrames ──", th)
	if tokens != nil {
		secFrames.SetTokens(tokens)
		secFrames.SetColorRole(ColorMuted)
	}

	// Build the grid: each row is a horizontal stack [label, control].
	rows := []Component{
		title,
		secTextFields,
		newDemoRow(tokens, "default", tfDefault),
		newDemoRow(tokens, "focused", tfFocused),
		newDemoRow(tokens, "placeholder", tfPlaceholder),
		newDemoRow(tokens, "disabled", tfDisabled),
		secButtons,
		newDemoRow(tokens, "default", btnDefault),
		newDemoRow(tokens, "disabled", btnDisabled),
		secLists,
		newDemoRow(tokens, "cursor=0", listDefault),
		newDemoRow(tokens, "selected=2", listSelected),
		secLabels,
		newDemoRow(tokens, "default", labelDefault),
		newDemoRow(tokens, "accent", labelAccent),
		newDemoRow(tokens, "muted", labelMuted),
		newDemoRow(tokens, "error", labelError),
		newDemoRow(tokens, "success", labelSuccess),
		secFrames,
		newDemoRow(tokens, "dialog", frameDialog),
		newDemoRow(tokens, "popover", framePopover),
		newDemoRow(tokens, "window", frameWindow),
	}

	stack := NewStack(NewID(RootID, "gallery"), Vertical, 0, rows...)

	h := NewHost(th, stack)
	// Register all rows and their child components.
	var registerAll func(c Component)
	registerAll = func(c Component) {
		if c == nil {
			return
		}
		h.Register(c)
		if s, ok := c.(*Stack); ok {
			for _, ch := range s.Children() {
				registerAll(ch)
			}
		}
		if f, ok := c.(*WindowFrame); ok {
			if f.Content() != nil {
				registerAll(f.Content())
			}
		}
	}
	for _, c := range rows {
		registerAll(c)
	}
	// Focus the first text field.
	h.Focus(tfDefault.ID())
	return h
}

// DemoPaint renders the gallery into a buffer of the given size.
func DemoPaint(th *theme.Theme, w, h int) *surface.CellBuffer {
	host := DemoGallery(th)
	host.Resize(w, h)
	buf := surface.NewCellBuffer(w, h)
	host.Paint(buf)
	return buf
}

// ── helpers ──

func newDemoTextField(tokens *TokenSet, value, placeholder string) *TextField {
	id := NewID(RootID, "tf-"+value)
	if value == "" && placeholder != "" {
		id = NewID(RootID, "tf-ph")
	}
	if value == "disabled" {
		id = NewID(RootID, "tf-dis")
	}
	tf := NewTextField(id, tokens)
	tf.SetPrompt("❯")
	tf.SetPlaceholder(placeholder)
	if value != "" {
		tf.SetValue(value)
	}
	return tf
}

func newDemoButton(tokens *TokenSet, label string) *Button {
	id := NewID(RootID, "btn-"+label)
	btn := NewButton(id, label, "demo."+label)
	btn.SetTokens(tokens)
	return btn
}

func newDemoList(tokens *TokenSet, selectedIdx int, hover bool) *List {
	id := NewID(RootID, "list")
	list := NewList(id, tokens)
	list.SetItems([]ListItem{
		{ID: "a", Label: "Alpha", Detail: "first"},
		{ID: "b", Label: "Beta", Detail: "second"},
		{ID: "c", Label: "Gamma", Detail: "third"},
		{ID: "d", Label: "Delta", Detail: "fourth"},
	})
	if selectedIdx >= 0 && selectedIdx < 4 {
		list.SelectIndex(selectedIdx)
	}
	return list
}

func newDemoFrame(tokens *TokenSet, role FrameRole, title string, closable bool) *WindowFrame {
	id := NewID(RootID, "frame-"+title)
	frame := NewWindowFrame(id, role, tokens)
	frame.SetTitle(title)
	frame.SetClosable(closable)
	body := NewLabel(NewID(id, "body"), "Content area", nil)
	body.SetTokens(tokens)
	body.SetColorRole(ColorFg)
	frame.SetContent(body)
	return frame
}

func newDemoRow(tokens *TokenSet, label string, control Component) *Stack {
	lbl := NewLabel(NewID(RootID, "row-lbl-"+label), label, nil)
	if tokens != nil {
		lbl.SetTokens(tokens)
		lbl.SetColorRole(ColorMuted)
	}
	return NewStack(NewID(RootID, "row-"+label), Horizontal, 1, lbl, control)
}
