package chatprojection

import (
	"sort"
	"strings"

	"crude/tui/cell"
	"crude/tui/rendering"
	"crude/tui/tools"
	"crude/tui/ui"
	"crude/types/conversation"
	"crude/workspace"
)

// BuildSubPaneHeaderLine builds the title bar ("│ ● agent-name 12s").
func BuildSubPaneHeaderLine(header workspace.SubPaneHeaderView, width, spinFrame int) SubPaneLine {
	controls := agentPaneControls(header, width)
	width -= spansWidth(controls)
	var spans []RowSpan
	spans = append(spans, RowSpan{Text: "│ ", Role: ui.ColorBorder})

	if !header.Done {
		dot := ui.ActivityDot(spinFrame) + " "
		meta := rendering.FormatThinkingDuration(header.Elapsed)
		if meta == "" {
			meta = "<1s"
		}
		titleWidth := max(1, width-rendering.VisibleWidth("│ ")-rendering.VisibleWidth(dot)-rendering.VisibleWidth(" "+meta))
		title := rendering.TruncateMiddleVisual(header.Title, titleWidth, "…")
		switch header.Type {
		case workspace.PaneThinking:
			spans = append(spans, RowSpan{Text: dot, Role: ui.ColorAccent})
			spans = append(spans, RowSpan{Text: title, Role: ui.ColorAccent, Attr: cell.AttrBold})
		default:
			spans = append(spans, RowSpan{Text: dot + title, Role: ui.ColorAccent, Attr: cell.AttrBold})
		}
	} else if header.Type == workspace.PaneThinking {
		duration := rendering.FormatThinkingDuration(header.Elapsed)
		if duration == "" {
			duration = "<1s"
		}
		spans = append(spans, RowSpan{Text: "● Thought for " + duration, Role: ui.ColorMuted})
		spans = append(spans, RowSpan{Text: " ▾", Role: ui.ColorMuted})
	} else {
		status, failed := subAgentDisplayStatus(header.Status)
		role := ui.ColorMuted
		marker := "● "
		if failed {
			role, marker = ui.ColorError, "× "
		}
		if status != "" {
			status = " — " + status
		}
		titleWidth := max(1, width-rendering.VisibleWidth("│ "+marker)-rendering.VisibleWidth(status+" ▾"))
		title := rendering.TruncateMiddleVisual(header.Title, titleWidth, "…")
		spans = append(spans, RowSpan{Text: marker, Role: role}, RowSpan{Text: title, Role: ui.ColorMuted})
		if status != "" {
			spans = append(spans, RowSpan{Text: status, Role: role})
		}
		spans = append(spans, RowSpan{Text: " ▾", Role: ui.ColorMuted})
	}

	if !header.Done {
		meta := rendering.FormatThinkingDuration(header.Elapsed)
		if meta == "" {
			meta = "<1s"
		}
		spans = append(spans, RowSpan{Text: " " + meta, Role: ui.ColorMuted})
	}

	pl := padPaintLine(PaintLine{Spans: spans}, width)
	pl.Spans = append(pl.Spans, controls...)
	line := NativeLine(pl)
	line.FrameSensitive = !header.Done
	return line
}

// agentPaneControls are a sub-agent pane's explicit open buttons. Sub-agents
// start minimized, so the pane is where the user opens one as a main tab or
// as its own window. Thinking panes and panes too narrow to keep a readable
// title get none.
func agentPaneControls(header workspace.SubPaneHeaderView, width int) []RowSpan {
	if header.Type != workspace.PaneAgent || header.ID == "" {
		return nil
	}
	controls := []RowSpan{
		{Text: "  "},
		{Text: "⧉ tab", Role: ui.ColorAccent, Action: &ui.Action{ID: ChatAgentOpenActionID, Payload: ChatAgentOpenPayload{AgentID: header.ID}}},
		{Text: "  "},
		{Text: "⇱ window", Role: ui.ColorAccent, Action: &ui.Action{ID: ChatAgentOpenActionID, Payload: ChatAgentOpenPayload{AgentID: header.ID, Window: true}}},
		{Text: " "},
	}
	if width-spansWidth(controls) < 24 {
		return nil
	}
	return controls
}

func spansWidth(spans []RowSpan) int {
	total := 0
	for _, span := range spans {
		total += rendering.VisibleWidth(span.Text)
	}
	return total
}

// BuildSubPaneSummaryLine builds the collapsed summary ("│ ● agent for 5.0s").
func BuildSubPaneSummaryLine(view workspace.SubPaneView, width int) SubPaneLine {
	controls := agentPaneControls(view.Header, width)
	width -= spansWidth(controls)
	summary := ""
	if view.Summary != nil {
		summary = view.Summary.Text
	}
	status := ""
	statusRole := ui.ColorMuted
	marker := "● "
	if view.Header.Type == workspace.PaneAgent {
		label, failed := subAgentDisplayStatus(view.Header.Status)
		if label != "" {
			status = " — " + label
		}
		if failed {
			statusRole, marker = ui.ColorError, "× "
		}
	}
	summaryWidth := max(1, width-rendering.VisibleWidth("│ "+marker)-rendering.VisibleWidth(status+" ▸"))
	summary = rendering.TruncateMiddleVisual(summary, summaryWidth, "…")
	spans := []RowSpan{
		{Text: "│ ", Role: ui.ColorBorder},
		{Text: marker, Role: statusRole},
		{Text: summary, Role: ui.ColorMuted},
	}
	if status != "" {
		spans = append(spans, RowSpan{Text: status, Role: statusRole})
	}
	spans = append(spans, RowSpan{Text: " ▸", Role: ui.ColorMuted})
	pl := padPaintLine(PaintLine{Spans: spans}, width)
	pl.Spans = append(pl.Spans, controls...)
	return NativeLine(pl)
}

func subAgentDisplayStatus(status string) (string, bool) {
	switch status {
	case "", "completed":
		return "", false
	case "cancelled":
		return "cancelled", false
	case "failed_timeout":
		return "timed out", true
	case "failed_error", "error", "failed":
		return "failed", true
	case "failed_quota":
		return "quota exceeded", true
	case "failed_policy":
		return "policy blocked", true
	case "crashed":
		return "crashed", true
	default:
		return "failed", true
	}
}

// BuildSubPaneToolLines uses this renderer's workspace root and the same
// semantic titles as ordinary tool leaves.
func (r *Renderer) BuildSubPaneToolLines(messages []*conversation.ChatMessage, width, frame int) ([]SubPaneLine, bool) {
	var lines []SubPaneLine
	active := false
	for _, msg := range messages {
		if msg.Role != conversation.RoleTool || msg.ToolName == "" {
			continue
		}
		active = active || toolRowFrameSensitive(*msg)
		lines, _ = r.appendSubPaneToolLines(lines, msg, width, frame, r.disclosureState(msg))
	}
	return lines, active
}

// CompactToolRows retains the measured compact tool blocks of one renderer.
// Sync invalidates by message ID, even if several updates precede the paint.
// A completed or hidden pane does not prepare new rows until it is opened.
type CompactToolRows struct {
	width, height int
	byID          map[string]*compactToolBlock
	ordered       []*compactToolBlock
	dirty         []*compactToolBlock
	pendingSyntax map[*compactToolBlock]bool
	// Prefix offsets before layoutFrom are still valid.
	layoutFrom int
}

type compactToolBlock struct {
	id           string
	index, start int
	lines        []SubPaneLine
	alternate    map[int]SubPaneLine
	disclosure   DisclosureState
	planSource   bool // this row can affect another plan row's default disclosure
	dirty        bool
}

func compactTool(msg *conversation.ChatMessage) bool {
	return msg != nil && msg.Role == conversation.RoleTool && msg.ToolName != ""
}

func (c *CompactToolRows) appendBlock(msg *conversation.ChatMessage, block *compactToolBlock) {
	if block == nil {
		block = &compactToolBlock{id: msg.InternalID}
		c.invalidate(block)
	}
	block.index = len(c.ordered)
	c.ordered = append(c.ordered, block)
	c.byID[msg.InternalID] = block
}

func (c *CompactToolRows) invalidate(block *compactToolBlock) {
	if block != nil && !block.dirty {
		block.dirty = true
		c.dirty = append(c.dirty, block)
	}
}

// Reuse surviving identities on removals, reorder, or a change of role.
// Drop removed rows now, rather than waiting for another render.
func (c *CompactToolRows) reorder(messages []*conversation.ChatMessage) {
	previous := c.byID
	c.byID = make(map[string]*compactToolBlock, len(previous))
	clear(c.ordered)
	c.ordered = c.ordered[:0]
	for _, msg := range messages {
		if compactTool(msg) {
			c.appendBlock(msg, previous[msg.InternalID])
		}
	}
	kept := c.dirty[:0]
	for _, block := range c.dirty {
		if c.byID[block.id] == block {
			kept = append(kept, block)
		}
	}
	clear(c.dirty[len(kept):])
	c.dirty = kept
	for block := range c.pendingSyntax {
		if c.byID[block.id] != block {
			delete(c.pendingSyntax, block)
		}
	}
	c.layoutFrom = 0
}

func (r *Renderer) syncCompactTools(result SyncResult) {
	c := r.compact
	if c == nil {
		return
	}
	reorder := result.StructureChanged && !result.PureAppend
	if result.StructureChanged && result.PureAppend {
		for _, msg := range r.Messages()[result.PreviousLen:] {
			if compactTool(msg) {
				c.appendBlock(msg, nil)
			}
		}
	}
	checkPlans := reorder
	for _, id := range result.ChangedIDs {
		msg, block := r.MessageByID(id), c.byID[id]
		if compactTool(msg) != (block != nil) {
			reorder = true
		}
		if block != nil {
			c.invalidate(block)
			checkPlans = checkPlans || block.planSource
		}
		if compactTool(msg) && (tools.IsPlanTool(msg.ToolName) || msg.ToolName == "multi_tool_use.parallel") {
			checkPlans = true
		}
	}
	if reorder {
		c.reorder(r.Messages())
	}
	// A newer (possibly batched) plan can supersede an unchanged plan row.
	// Ordinary tool updates and streamed text never visit unrelated blocks.
	if checkPlans {
		for id, block := range c.byID {
			if !block.dirty && block.planSource {
				if msg := r.MessageByID(id); tools.IsPlanTool(msg.ToolName) && block.disclosure != r.disclosureState(msg) {
					c.invalidate(block)
				}
			}
		}
	}
}

// CompactRows returns measured rows at this width for a compact child viewport.
func (r *Renderer) CompactRows(width int) *CompactToolRows {
	c := r.compact
	if c == nil || c.width != width {
		c = &CompactToolRows{width: width}
		c.reorder(r.Messages())
		r.compact = c
	}
	if len(c.pendingSyntax) != 0 && !DiffSyntaxBusy() {
		for block := range c.pendingSyntax {
			c.invalidate(block)
		}
	}
	for _, block := range c.dirty {
		msg := r.MessageByID(block.id)
		block.disclosure = r.disclosureState(msg)
		block.planSource = tools.IsPlanTool(msg.ToolName) || msg.ToolName == "multi_tool_use.parallel"
		previousHeight := len(block.lines)
		var pending bool
		block.lines, pending = r.appendSubPaneToolLines(nil, msg, width, 0, block.disclosure)
		if pending {
			if c.pendingSyntax == nil {
				c.pendingSyntax = make(map[*compactToolBlock]bool)
			}
			c.pendingSyntax[block] = true
		} else {
			delete(c.pendingSyntax, block)
		}
		block.alternate = nil
		if toolRowFrameSensitive(*msg) {
			animated, _ := r.appendSubPaneToolLines(nil, msg, width, ui.ActivityBlinkPhaseMillis, block.disclosure)
			for i, other := range animated {
				if other.Text != block.lines[i].Text {
					if block.alternate == nil {
						block.alternate = make(map[int]SubPaneLine)
					}
					block.alternate[i] = other
				}
			}
		}
		if len(block.lines) != previousHeight {
			c.layoutFrom = min(c.layoutFrom, block.index)
		}
		block.dirty = false
	}
	clear(c.dirty)
	c.dirty = c.dirty[:0]
	if c.layoutFrom < len(c.ordered) || len(c.ordered) == 0 {
		c.height = 0
		if c.layoutFrom > 0 {
			prev := c.ordered[c.layoutFrom-1]
			c.height = prev.start + len(prev.lines)
		}
		for _, block := range c.ordered[c.layoutFrom:] {
			block.start = c.height
			c.height += len(block.lines)
		}
		c.layoutFrom = len(c.ordered)
	}
	return c
}

// Height returns the scroll extent in compact tool rows.
func (c *CompactToolRows) Height() int { return c.height }

// AppendVisible appends only the requested range, selecting alternate ink
// for visible animated rows without modifying previously returned frames.
func (c *CompactToolRows) AppendVisible(dst []SubPaneLine, start, height, frame int) []SubPaneLine {
	start = max(0, start)
	if height <= 0 || start >= c.height {
		return dst
	}
	index := sort.Search(len(c.ordered), func(i int) bool {
		block := c.ordered[i]
		return block.start+len(block.lines) > start
	})
	end := start + min(height, c.height-start)
	on := ui.BlinkOn(frame)
	for _, block := range c.ordered[index:] {
		if start >= end {
			break
		}
		for i := start - block.start; i < len(block.lines) && start < end; i++ {
			line := block.lines[i]
			if !on && block.alternate != nil {
				if other, ok := block.alternate[i]; ok {
					line = other
				}
			}
			dst = append(dst, line)
			start++
		}
	}
	return dst
}

func (r *Renderer) appendSubPaneToolLines(lines []SubPaneLine, msg *conversation.ChatMessage, width, frame int, disclosure DisclosureState) ([]SubPaneLine, bool) {
	body := renderProjectToolMessageLines(*msg, nil, width-toolBodyGutterWidth, width, width, frame, 12,
		msg.ToolPreviewPhase, disclosure, r.projectPaths)
	for _, line := range body.native {
		row := NativeLine(padPaintLine(line, width))
		row.FrameSensitive = body.syntaxPending
		lines = append(lines, row)
	}
	return lines, body.syntaxPending
}

// BuildCompactWorkingLine builds the placeholder when a compact pane has no tools yet.
func BuildCompactWorkingLine(thinking bool, width int) SubPaneLine {
	msg := "Working..."
	if thinking {
		msg = "Thinking..."
	}
	spans := []RowSpan{
		{Text: "│ ", Role: ui.ColorBorder},
		{Text: "● " + msg, Role: ui.ColorMuted},
	}
	pl := padPaintLine(PaintLine{Spans: spans}, width)
	return NativeLine(pl)
}

func padPaintLine(pl PaintLine, width int) PaintLine {
	visW := pl.VisibleWidth()
	padR := width - visW
	if padR < 0 {
		padR = 0
	}
	if padR > 0 {
		pl.Spans = append(pl.Spans, RowSpan{Text: strings.Repeat(" ", padR), Role: ui.ColorFg})
	}
	return pl
}
