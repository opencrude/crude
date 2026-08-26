package chatprojection

// ChatToggleActionID is the typed action shellchat emits when the user clicks
// a message's disclosure row (no drag). The viewmodel routes it to the active
// renderer's ToggleDisclosure.
const ChatToggleActionID = "chat.toggle-details"

// ChatTogglePayload carries the message whose disclosure row was clicked.
type ChatTogglePayload struct {
	MessageID string
}

// MessageRow is one message's rendered chat lines for shellchat ui.VirtualList items.
// Height = len(Lines); RowPainter paints Lines[localRow] after compose/selection.
// When Native is parallel to Lines and Native[i] is not PreferANSI(), shellchat
// paints structured cells directly instead of the ANSI/string path.
type MessageRow struct {
	ID        string
	Lines     []string
	Native    []PaintLine
	SubPaneID string
	// Toggle distinguishes real disclosure rows from ToggleLine's zero value.
	// Manually-built and legacy rows therefore remain non-interactive.
	Toggle bool
	// ToggleLine is the 0-based local line within Lines (including any leading
	// spacer) that owns the disclosure affordance: clicking it with no
	// drag toggles expand/collapse. -1 means the message has no disclosure.
	ToggleLine int
}
