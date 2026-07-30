package chatprojection

// MessageRow is one message's rendered chat lines for shellchat ui.VirtualList items.
// Height = len(Lines); RowPainter paints Lines[localRow] after compose/selection.
// When Native is parallel to Lines and Native[i] is not PreferANSI(), shellchat
// paints structured cells directly instead of the ANSI/string path.
type MessageRow struct {
	ID        string
	Lines     []string
	Native    []PaintLine
	SubPaneID string
}
