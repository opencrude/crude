package widgets

import (
	"crude/tui/theme"
	"crude/tui/ui"
)

// FindReplaceFrameIDs are the component IDs inside a find/replace frame.
const (
	FRFrameID      = "find-replace"
	FRFindField    = "find-replace/find"
	FRReplaceField = "find-replace/replace"
	FRContent      = "find-replace/content"
)

// FindReplaceFrameSpec configures a WindowFrame-backed find/replace popover.
type FindReplaceFrameSpec struct {
	FindValue    string
	ReplaceValue string
	ReplaceMode  bool
	FocusedField  int // 0=find, 1=replace
	Width        int
	Height       int
}

// BuildFindReplaceFrame builds a WindowFrame popover for find/replace.
// The find and replace fields are ui.TextField components (backed by TextEdit).
// The toggle pills, status, and hints are rendered as Label lines in the Stack.
// The viewmodel keeps the toggle state and callback wiring.
func BuildFindReplaceFrame(th *theme.Theme, spec FindReplaceFrameSpec) *ui.ComponentWidget {
	if th == nil {
		th = theme.DefaultDark()
	}
	tokens := ui.NewTokenSet(th)

	w := spec.Width
	if w < 38 {
		w = 38
	}
	if w > 90 {
		w = 90
	}

	find := ui.NewTextField(FRFindField, tokens)
	find.SetPrompt("Find: ")
	find.SetPlaceholder("type to search")
	find.SetValue(spec.FindValue)

	var children []ui.Component
	children = append(children, find)

	if spec.ReplaceMode {
		replace := ui.NewTextField(FRReplaceField, tokens)
		replace.SetPrompt("Replace: ")
		replace.SetValue(spec.ReplaceValue)
		children = append(children, replace)
	}

	// Status/hint labels (placeholder rows — viewmodel can mutate)
	statusLabel := ui.NewLabel(ui.NewID(ui.RootID, "find-replace/status"), "", th)
	statusLabel.SetText("")
	children = append(children, statusLabel)

	hintLabel := ui.NewLabel(ui.NewID(ui.RootID, "find-replace/hints"), "", th)
	hintLabel.SetText("enter next  |  shift+enter prev  |  esc close")
	children = append(children, hintLabel)

	stack := ui.NewStack(ui.NewID(ui.RootID, FRContent), ui.Vertical, 1, children...)

	frame := ui.NewWindowFrame(ui.NewID(ui.RootID, FRFrameID), ui.FramePopover, tokens)
	frame.SetTitle("Find")
	frame.SetIcon("⌕")
	frame.SetClosable(false)
	frame.SetContent(stack)

	h := spec.Height
	if h < 7 {
		h = 7
	}
	if h > 12 {
		h = 12
	}

	cw := ui.NewComponentWidget(th, frame, w, h)
	// Focus the find field by default
	if spec.FocusedField == 1 && spec.ReplaceMode {
		cw.Focus(FRReplaceField)
	} else {
		cw.Focus(FRFindField)
	}
	return cw
}

// FindReplaceFrameFindField returns the find TextField from a framed find/replace.
func FindReplaceFrameFindField(cw *ui.ComponentWidget) *ui.TextField {
	if cw == nil {
		return nil
	}
	return findTextField(cw.Root(), FRFindField)
}

// FindReplaceFrameReplaceField returns the replace TextField from a framed find/replace.
func FindReplaceFrameReplaceField(cw *ui.ComponentWidget) *ui.TextField {
	if cw == nil {
		return nil
	}
	return findTextField(cw.Root(), FRReplaceField)
}