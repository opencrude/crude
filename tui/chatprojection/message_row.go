package chatprojection

// ChatToggleActionID is the typed action shellchat emits when the user clicks
// a message's disclosure row (no drag). The viewmodel routes it to the active
// renderer's ToggleDisclosure.
const ChatToggleActionID = "chat.toggle-details"

const ChatRecoveryRetryActionID = "chat.retry-recovery"

type ChatRecoveryRetryPayload struct {
	AgentID, IncidentID string
	Generation          uint64
}

// ChatTogglePayload carries the message whose disclosure row was clicked.
type ChatTogglePayload struct {
	MessageID string
}

// MessageRow is one message's rendered chat lines for shellchat ui.VirtualList items.
// Height = len(Lines); RowPainter paints Lines[localRow] after compose/selection.
// Native is parallel to Lines; shellchat paints those structured cells.
type MessageRow struct {
	ID                   string
	Lines                []string
	Native               []PaintLine
	SubPaneID            string
	ContextBoundary      bool
	ContextBoundaryStart int
	// Toggle distinguishes real disclosure rows from ToggleLine's zero value.
	// Manually-built and legacy rows therefore remain non-interactive.
	Toggle bool
	// ToggleAny collapses an expanded block from a click anywhere in that
	// block. Dragging still selects text and never toggles.
	ToggleAny bool
	// LeafToggles maps clickable tree-leaf rows to their own message IDs.
	// Shellchat checks these before the enclosing group's disclosure.
	LeafToggles map[int]string
	// ToggleLine is the 0-based local line within Lines (including any leading
	// spacer) that owns the disclosure affordance: clicking it with no
	// drag toggles expand/collapse. -1 means the message has no disclosure.
	ToggleLine int
}
