package workspace

import (
	"fmt"
	"time"
)

// SubPaneHeaderView is presentation-neutral subpane header data. The TUI
// renders the ANSI header line from this struct.
type SubPaneHeaderView struct {
	Title      string
	Type       PaneType
	Done       bool
	Status     string
	Elapsed    time.Duration
	TokenCount int
	// Thinking is true when the pane has active thinking content (used by
	// the TUI to drive the spinner animation phase).
	Thinking bool
}

// SubPaneSummaryView is the collapsed-pane summary. Non-nil only when
// the pane is Done.
type SubPaneSummaryView struct {
	// Text is the fully-formatted summary line (e.g. "Thinked for 5.0s ↓42K").
	// The workspace computes this; the TUI only adds the border prefix + icon.
	Text       string
	TokenCount int
}

// SubPaneView is the presentation-neutral projection of a SubPane. The
// TUI reads this struct and the Messages pointer to render the pane;
// it never reaches back into SubPane methods for rendering.
type SubPaneView struct {
	Header  SubPaneHeaderView
	Summary *SubPaneSummaryView
	// Messages is the canonical Conversation pointer. The TUI projection
	// reads Messages()/Version() to synchronize its MessageList by stable
	// ID/version — same pattern as AgentState.Messages.
	Messages *Conversation
}

// View returns the current subpane projection. This method MUST NOT
// import any TUI package or emit ANSI. It reads sp.Messages and the
// compact scroll fields only.
func (sp *SubPane) View() SubPaneView {
	msgs := sp.Messages.Messages()

	header := SubPaneHeaderView{
		Title:      sp.Title,
		Type:       sp.Type,
		Done:       sp.Done,
		Status:     sp.Status,
		Elapsed:    sp.Elapsed(),
		TokenCount: sp.TokenCount,
	}
	if len(msgs) > 0 {
		last := msgs[len(msgs)-1]
		header.Thinking = last.ThinkingActive
	}

	view := SubPaneView{
		Header:   header,
		Messages: sp.Messages,
		Summary:  nil,
	}

	if sp.Done {
		view.Summary = sp.buildSummaryView()
	}

	return view
}

func (sp *SubPane) buildSummaryView() *SubPaneSummaryView {
	dur := sp.Elapsed()
	tokens := ""
	if sp.TokenCount > 0 {
		tokens = fmt.Sprintf(" ↓%s", formatTokenCount(sp.TokenCount))
	}
	verb := "Thinking"
	if sp.Done {
		verb = "Thought"
	}
	if sp.Type == PaneAgent {
		verb = sp.Title
	}
	text := fmt.Sprintf("%s for %s%s", verb, formatDurationHuman(dur), tokens)
	if sp.Type == PaneAgent && sp.Status != "" && sp.Status != "completed" {
		text = fmt.Sprintf("%s — %s", text, sp.Status)
	}
	return &SubPaneSummaryView{
		Text:       text,
		TokenCount: sp.TokenCount,
	}
}

// formatDurationHuman renders a compact human duration for the collapsed
// summary line: "42s", "2m 10s", "1h 02m 03s". It mirrors the TUI's
// rendering.FormatThinkingDuration so the promoted pane summary and the
// inline "thought for Xs" label read identically.
func formatDurationHuman(d time.Duration) string {
	if d <= 0 {
		return "<1s"
	}
	if d < time.Second {
		return "<1s"
	}
	secs := int(d.Seconds())
	hours := secs / 3600
	mins := (secs % 3600) / 60
	rem := secs % 60
	switch {
	case hours > 0:
		return fmt.Sprintf("%dh %02dm %02ds", hours, mins, rem)
	case mins > 0:
		return fmt.Sprintf("%dm %02ds", mins, rem)
	default:
		return fmt.Sprintf("%ds", rem)
	}
}

func formatTokenCount(n int) string {
	switch {
	case n >= 1000000:
		v := float64(n) / 1000000
		if v == float64(int(v)) {
			return fmt.Sprintf("%dM", int(v))
		}
		return fmt.Sprintf("%.1fM", v)
	case n >= 1000:
		v := float64(n) / 1000
		if v == float64(int(v)) {
			return fmt.Sprintf("%dK", int(v))
		}
		return fmt.Sprintf("%.1fK", v)
	default:
		return fmt.Sprintf("%d", n)
	}
}
