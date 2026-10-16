package ui

import (
	"crude/tui/theme"
)

// CommandPaletteFrameIDs are the component IDs inside a command palette frame.
const (
	CPPaletteID    = "command-palette"
	CPSearchField  = "command-palette/search"
	CPList         = "command-palette/list"
	CPActivate     = "command-palette.activate"
	CPSubmitSearch = "command-palette.search"
)

// CommandPaletteSpec configures a WindowFrame-backed command palette.
type CommandPaletteSpec struct {
	Items    []ListItem
	Selected int
	Width    int
	Height   int
}

// BuildCommandPaletteFrame builds a WindowFrame + TextField + List command palette.
// Replaces the hand-drawn chrome/list in command_palette.go View().
// The viewmodel keeps the filter logic and updates List items via SetItems
// (mutate-in-place pattern, matching how LSP list popups work).
func BuildCommandPaletteFrame(th *theme.Theme, spec CommandPaletteSpec) *ComponentWidget {
	if th == nil {
		th = theme.DefaultDark()
	}
	tokens := NewTokenSet(th)

	w := spec.Width
	if w < 40 {
		w = 40
	}
	if w > 80 {
		w = 80
	}
	h := spec.Height
	if h < 6 {
		h = 6
	}

	search := NewTextField(CPSearchField, tokens)
	search.SetPrompt(SearchGlyphEnd(tokens))
	search.SetPromptPlacement(PromptEnd)
	search.SetPlaceholder("search commands")
	search.SetSubmitAction(CPSubmitSearch)
	// Opening the palette is input activity: keep the focused cursor visible
	// immediately instead of inheriting an arbitrary wall-clock blink phase.
	search.SetValue("")

	list := NewList(CPList, tokens)
	list.SetActivateAction(CPActivate)
	list.SetItems(spec.Items)
	if spec.Selected >= 0 {
		list.SelectIndex(spec.Selected)
	}

	// Stack: search field (1 row) + gap (1 row divider) + list (remaining)
	// The Stack lays out vertically; List takes remaining space.
	stack := NewStack(NewID(RootID, "command-palette/content"), Vertical, 1, search, list)

	frame := NewWindowFrame(NewID(RootID, "command-palette"), FramePopover, tokens)
	frame.SetTitle("Command Palette")
	frame.SetClosable(true)
	frame.SetContent(stack)

	cw := NewComponentWidget(th, frame, w, h)
	// Focus the search field so typing filters the list.
	cw.Focus(CPSearchField)
	return cw
}

// CommandPaletteList returns the list component from a framed command palette.
func CommandPaletteList(cw *ComponentWidget) *List {
	if cw == nil {
		return nil
	}
	return findList(cw.Root(), CPList)
}

// CommandPaletteSearch returns the search field from a framed command palette.
func CommandPaletteSearch(cw *ComponentWidget) *TextField {
	if cw == nil {
		return nil
	}
	return findTextField(cw.Root(), CPSearchField)
}
