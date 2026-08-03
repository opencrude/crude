package ui

import (
	"crude/tui/cell"
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

	// ── Collection states (Table, Menu) ──
	tableDefault := newDemoTable(tokens, 1)
	menuDefault := newDemoMenu(tokens, 1)

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
	secCollections := NewLabel(NewID(RootID, "sec-col"), "── Collections ──", th)
	if tokens != nil {
		secCollections.SetTokens(tokens)
		secCollections.SetColorRole(ColorMuted)
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
	secLayouts := NewLabel(NewID(RootID, "sec-layout"), "── Layouts ──", th)
	if tokens != nil {
		secLayouts.SetTokens(tokens)
		secLayouts.SetColorRole(ColorMuted)
	}

	// ── Layout container demos ──
	flexDemo := newDemoFlex(tokens)
	gridDemo := newDemoGrid(tokens)
	insetDemo := newDemoInset(tokens)
	splitDemo := newDemoSplit(tokens)
	viewportDemo := newDemoViewport(tokens)

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
		secCollections,
		newDemoRow(tokens, "table", tableDefault),
		newDemoRow(tokens, "menu", menuDefault),
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
		secLayouts,
		newDemoRow(tokens, "flex", flexDemo),
		newDemoRow(tokens, "grid", gridDemo),
		newDemoRow(tokens, "inset", insetDemo),
		newDemoRow(tokens, "split", splitDemo),
		newDemoRow(tokens, "viewport", viewportDemo),
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
		if p, ok := c.(Parent); ok {
			for _, ch := range p.Children() {
				registerAll(ch)
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

func newDemoTable(tokens *TokenSet, selectedIdx int) *Table {
	table := NewTable(NewID(RootID, "table"), tokens)
	table.SetShowHeader(true)
	table.SetColumns([]TableColumn{
		{ID: "name", Label: "File", Track: FixedGridTrack(12), Align: TableAlignLeft},
		{ID: "status", Label: "Status", Track: FractionGridTrack(1)},
		{ID: "lines", Label: "Lines", Track: FixedGridTrack(6), Align: TableAlignRight},
	})
	table.SetRows([]TableRow{
		{ID: "main.go", Cells: []TableCell{{Text: "main.go"}, {Text: "modified"}, {Text: "128"}}},
		{ID: "app.go", Cells: []TableCell{{Text: "app.go"}, {Text: "saved"}, {Text: "310"}}},
		{ID: "ui.go", Cells: []TableCell{{Text: "ui.go"}, {Text: "new"}, {Text: "42"}}},
		{ID: "doc.go", Cells: []TableCell{{Text: "doc.go"}, {Text: "saved"}, {Text: "7"}}},
	})
	if selectedIdx >= 0 && selectedIdx < 4 {
		table.SelectIndex(selectedIdx)
	}
	return table
}

func newDemoMenu(tokens *TokenSet, selectedIdx int) *Menu {
	menu := NewMenu(NewID(RootID, "menu"), tokens)
	menu.SetItems([]MenuItem{
		{Header: true, Label: "Actions"},
		{ID: "open", Label: "Open File…", Accel: "ctrl+o"},
		{ID: "save", Label: "Save", Accel: "ctrl+s"},
		{Separator: true},
		{ID: "goto", Label: "Go to Symbol", Submenu: true},
		{Disabled: true, Label: "Run (disabled)"},
	})
	if selectedIdx >= 0 {
		menu.SelectIndex(selectedIdx)
	}
	return menu
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

// demoBandLabel is a label that fills its whole bounds with a popup background
// so grow/stretch/padding effects are visible in the gallery.
func demoBandLabel(id ComponentID, text string, tokens *TokenSet, role SemanticColor) *Label {
	l := NewLabel(id, text, nil)
	if tokens != nil {
		l.SetStyle(tokens.Color(role), tokens.Color(ColorPopupBg), 0)
		l.SetTokens(tokens)
	}
	return l
}

func newDemoFlex(tokens *TokenSet) *Flex {
	f := NewFlex(NewID(RootID, "demo/flex"), FlexRow, 1,
		NewFlexChild(demoBandLabel(NewID(RootID, "demo/flex/a"), "grow", tokens, ColorAccent), 2, 1, 0),
		NewFlexChild(demoBandLabel(NewID(RootID, "demo/flex/b"), "mid", tokens, ColorMuted), 1, 1, 0),
		NewFlexChild(demoBandLabel(NewID(RootID, "demo/flex/c"), "auto", tokens, ColorSuccess), 0, 1, 0),
	)
	f.SetCrossAxisAlignment(CrossStretch)
	return f
}

func newDemoGrid(tokens *TokenSet) *Grid {
	return NewGrid(NewID(RootID, "demo/grid"), 3, 1,
		demoBandLabel(NewID(RootID, "demo/grid/a"), "A", tokens, ColorAccent),
		demoBandLabel(NewID(RootID, "demo/grid/b"), "B", tokens, ColorMuted),
		demoBandLabel(NewID(RootID, "demo/grid/c"), "C", tokens, ColorSuccess),
		demoBandLabel(NewID(RootID, "demo/grid/d"), "D", tokens, ColorError),
	)
}

func newDemoInset(tokens *TokenSet) *Inset {
	return NewInset(NewID(RootID, "demo/inset"), InsetsAll(1),
		demoBandLabel(NewID(RootID, "demo/inset/inner"), "padded", tokens, ColorAccent))
}

func newDemoSplit(tokens *TokenSet) *SplitPane {
	s := NewSplitPane(NewID(RootID, "demo/split"), Horizontal,
		demoBandLabel(NewID(RootID, "demo/split/first"), "left", tokens, ColorAccent),
		demoBandLabel(NewID(RootID, "demo/split/second"), "right", tokens, ColorMuted))
	s.SetFraction(0.4)
	return s
}

func newDemoViewport(tokens *TokenSet) *Viewport {
	content := NewLabel(NewID(RootID, "demo/viewport/content"), "long content scrolled through the viewport", nil)
	if tokens != nil {
		content.SetStyle(cell.NoColor, tokens.Color(ColorPopupBg), 0)
		content.SetTokens(tokens)
	}
	vp := NewViewport(NewID(RootID, "demo/viewport"), content)
	vp.SetScrollOffset(5, 0)
	return vp
}
