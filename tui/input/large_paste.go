package input

import (
	"strings"

	"crude/tui/rendering"
)

const (
	pasteReferenceThreshold = 4 * 1024
	pasteInlineThreshold    = 16 * 1024
	pasteLineThreshold      = 8
	pasteTallMinBytes       = 512
	pasteWrappedThreshold   = 10
	pasteWrappedMinBytes    = 1024
)

// PasteAttachmentEvent asks the owning session to store a large paste off the
// UI thread. The input never owns attachment persistence or silently inserts a
// wall of text if storage is unavailable.
type PasteAttachmentEvent struct {
	ID, Epoch uint64
	Content   string
}

func (PasteAttachmentEvent) event() {}

type PastePendingEvent struct{}

func (PastePendingEvent) event() {}

func (m Model) promotePastedText(content string) bool {
	if len(content) >= pasteReferenceThreshold {
		return true
	}
	substantial := len(strings.TrimSpace(content))
	if substantial < pasteTallMinBytes {
		return false // a small checklist or short paragraph is still prose
	}
	logical := strings.TrimRight(content, "\n")
	if strings.Count(logical, "\n")+1 >= pasteLineThreshold {
		return true
	}
	if substantial < pasteWrappedMinBytes {
		return false
	}
	width := m.width
	if width <= 0 {
		width = 80
	}
	// Use the displayed text column, but don't promote ordinary prose merely
	// because a very narrow terminal/pane makes a few words wrap many times.
	textWidth := max(40, m.textWidthFor(width))
	rows := 0
	for _, line := range strings.Split(logical, "\n") {
		rows += len(rendering.WordWrap(line, textWidth))
		if rows >= pasteWrappedThreshold {
			return true
		}
	}
	return false
}

func PastedTextInline(content string) bool { return len(content) <= pasteInlineThreshold }

func (m *Model) invalidatePendingPastes() {
	m.draftEpoch++
	m.pendingPastes = nil
}

// CompletePaste consumes one exact asynchronous request. Typing stays usable
// while storage runs; clearing/sending/replacing the draft invalidates it.
func (m *Model) CompletePaste(event PasteAttachmentEvent) bool {
	if !m.PasteCurrent(event) {
		return false
	}
	delete(m.pendingPastes, event.ID)
	return true
}

func (m Model) PasteCurrent(event PasteAttachmentEvent) bool {
	return event.Epoch == m.draftEpoch && m.pendingPastes[event.ID]
}

func (m Model) DraftEpoch() uint64 { return m.draftEpoch }
