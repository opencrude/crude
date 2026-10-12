package chatprojection

import (
	"fmt"
	"math"
	"strings"
	"time"

	"crude/tui/rendering"
	"crude/tui/theme"
	"crude/tui/ui"
	"crude/types"
	"crude/types/activity"
	"crude/types/reasoning"
)

const ActivityPageDuration = 6000 // milliseconds on the shared presentation clock

// ActivityStrip derives retained, bounded rows from workspace facts. It owns
// presentation cadence only: no domain mutation, protocol parsing, timers or I/O.
type ActivityStrip struct {
	source                   types.AgentActivityView
	width                    int
	theme                    *theme.Theme
	tokens                   *ui.TokenSet
	ready                    bool
	pages                    []PaintLine
	md                       *rendering.MarkdownRenderer
	cycleKey                 string
	cycleStart               int
	resetCycle               bool
	waitStart                time.Time
	retryAt                  time.Time
	retryPrefix, retryReason string
	waitSecond               int
	waitLine                 PaintLine
	tide                     ui.TextTideMotion
}

func (s *ActivityStrip) Update(source types.AgentActivityView, width int, th *theme.Theme, tokens *ui.TokenSet) {
	if s.ready && s.source == source && s.width == width && s.theme == th && s.tokens == tokens {
		return
	}
	if s.width != width || s.theme != th || s.tokens != tokens {
		s.md = nil
	}
	if source.Status.Ref != s.source.Status.Ref || source.Status.TurnGeneration != s.source.Status.TurnGeneration ||
		source.Status.ProviderID != s.source.Status.ProviderID || source.Status.AccountID != s.source.Status.AccountID || source.Status.ModelID != s.source.Status.ModelID {
		s.tide = ui.TextTideMotion{}
	}
	if source.Active != s.source.Active {
		s.resetCycle = true
	}
	s.source, s.width, s.theme, s.tokens, s.ready = source, width, th, tokens, true
	key := source.Status.Ref.ID + ":" + source.Status.RequestID + ":" + string(source.Status.Phase) + ":" + source.ThinkingDisplay.BlockID
	if source.Plan != nil {
		key += ":" + source.Plan.ID
	}
	if key != s.cycleKey {
		s.cycleKey, s.resetCycle = key, true
	}
	s.pages = s.pages[:0]
	s.waitStart, s.waitSecond = time.Time{}, -1
	s.retryAt = time.Time{}
	if width <= 0 {
		return
	}

	status := source.Status
	if source.CompactionPhase != "" {
		label, role := "Compacting context", ui.ColorActivityText
		switch source.CompactionPhase {
		case "compaction_cancelling":
			label = "Stopping compaction"
		case "compaction_retrying":
			label = "Recovering compaction"
		case "compaction_committing":
			label = "Saving checkpoint"
		case "compaction_completed":
			label = "Context compacted · Ready to continue"
		case "compaction_cancelled":
			label = "Compaction cancelled"
		case "compaction_failed":
			label, role = "Compaction failed · details in transcript", ui.ColorError
		case "compaction_interrupted":
			label = "Compaction interrupted · original context retained"
		}
		s.add(PaintLine{Spans: []RowSpan{{Text: label, Role: role}}})
		return
	}
	label := stripText(status.Detail)
	if label == "" {
		label = "Ready"
		if source.Active {
			label = "Working"
		}
	}
	role := ui.ColorActivityText
	if status.NeedsAction {
		role = ui.ColorWarning
	}
	if status.Phase == types.AgentFailed {
		role = ui.ColorError
		label = "Turn failed · details in transcript"
	}
	if !s.animating() {
		s.tide = ui.TextTideMotion{}
	}
	if status.Phase == types.AgentCancelled {
		s.add(PaintLine{Spans: []RowSpan{{Text: "Stopped", Role: ui.ColorActivityText}}})
		return
	}
	if status.Phase == types.AgentNeedsAttention {
		label, role = "Needs attention · work kept · /retry to continue", ui.ColorWarning
	}
	if status.Phase == types.AgentInterrupted {
		label, role = "Interrupted · work kept · /retry to continue", ui.ColorWarning
	}
	if status.NeedsAction || status.Phase == types.AgentNeedsAttention || status.Phase == types.AgentInterrupted || status.Phase == types.AgentFailed || status.Phase == types.AgentRetrying || status.Phase == types.AgentRecovering || status.Phase == types.AgentCancelling {
		if status.Phase == types.AgentRetrying || status.Phase == types.AgentRecovering {
			label = "Retrying request"
			if status.Phase == types.AgentRecovering {
				label = "Recovering"
				source.RetryAttempt, source.RetryLimit, source.RetryReason = source.Recovery.Attempt, 0, source.Recovery.Summary
			}
			if source.RetryAttempt > 0 {
				label += fmt.Sprintf(" · attempt %d", source.RetryAttempt)
			}
			if source.RetryLimit > 0 {
				label += fmt.Sprintf("/%d", source.RetryLimit)
			}
			s.retryAt, s.retryPrefix, s.retryReason = status.RetryAt, label, stripText(source.RetryReason)
			if s.retryReason != "" {
				label += " · " + s.retryReason
			}
		}
		s.add(PaintLine{Spans: []RowSpan{{Text: label, Role: role}}})
		return
	}
	if status.Phase == types.AgentPreparing || status.Phase == types.AgentUploading || status.Phase == types.AgentWaiting {
		if status.Phase == types.AgentPreparing {
			label = "Preparing"
		}
		if status.Phase == types.AgentUploading {
			label = "Uploading"
		}
		metrics := ""
		if status.TotalBytes > 0 {
			percent := int(math.Min(1, math.Max(0, float64(status.UploadedBytes)/float64(status.TotalBytes))) * 100)
			metrics = fmt.Sprintf("↑ %s / %s · %d%%", rendering.FormatBytes(status.UploadedBytes), rendering.FormatBytes(status.TotalBytes), percent)
		} else if status.ProgressKnown {
			metrics = fmt.Sprintf("↑ %d%%", max(0, min(100, status.Progress)))
		}
		if metrics != "" {
			available := width - rendering.VisibleWidth(metrics) - 3
			if available >= 7 {
				label = rendering.TruncateVisual(label, available, "…") + " · " + metrics
			} else {
				label = metrics
			}
		}
		if status.Phase == types.AgentWaiting {
			s.waitStart = status.PhaseStartedAt
		}
		s.add(PaintLine{Spans: []RowSpan{{Text: label, Role: role}}})
		return // uploads, waits and action states are never hidden by rotation
	}

	if status.Phase == types.AgentThinking && source.Thinking != "" && source.ThinkingDisplay.Kind.HasCompactPreview() {
		if s.md == nil {
			s.md = rendering.MakeMdRenderer(max(40, width), th)
		}
		preview := ThinkingPreviewLine(source.Thinking, source.ThinkingDisplay, s.md, tokens)
		label := "Reasoning ▸ · "
		if source.ThinkingDisplay.Kind == reasoning.Provisional {
			label = "Thinking ▸ · "
		}
		if source.ThinkingExpanded {
			label = strings.Replace(label, "▸", "▾", 1)
		}
		line := PaintLine{Spans: []RowSpan{{Text: label, Role: ui.ColorActivityText}}}
		for _, span := range preview.Spans {
			span.Role = ui.ColorActivityText // Keep Markdown emphasis with the strip's supporting ink.
			span.FGSet = false
			line.Spans = append(line.Spans, span)
		}
		s.add(line)
	} else if source.Active && source.Plan != nil {
		s.add(compactPlanLine(source.Plan))
		return
	} else {
		s.add(PaintLine{Spans: []RowSpan{{Text: label, Role: role}}})
	}
	if source.Active && source.Plan != nil {
		s.add(compactPlanLine(source.Plan))
	}
}

// compactPlanLine keeps full plan detail out of the fixed status band.
func compactPlanLine(p *activity.Plan) PaintLine {
	var step *activity.Step
	for i := range p.Current {
		if p.Current[i].Status == "in_progress" || p.Current[i].Status == "" {
			step = &p.Current[i]
			break
		}
	}
	if step == nil {
		step = p.Next
	}
	label := "Plan ready"
	if step != nil {
		label = fmt.Sprintf("%d/%d %s", step.Number, p.Total, stripText(step.Text))
	} else {
		switch p.Status {
		case "paused":
			label = "Plan paused"
		case "completed":
			label = "Plan completed"
		case "cancelled":
			label = "Plan cancelled"
		}
	}
	return PaintLine{Spans: []RowSpan{{Text: label, Role: ui.ColorActivityText}}}
}

// add packs complete facts when space permits; otherwise it creates a bounded
// page. Higher-priority facts enter first and are never displaced by later ones.
func (s *ActivityStrip) add(line PaintLine) {
	if line.VisibleWidth() > s.width {
		line = TruncatePaintLine(line, max(0, s.width-1))
		line.Spans = append(line.Spans, RowSpan{Text: "…", Role: ui.ColorActivityText})
	}
	if len(s.pages) > 0 {
		last := &s.pages[len(s.pages)-1]
		if last.VisibleWidth()+3+line.VisibleWidth() <= s.width {
			last.Spans = append(last.Spans, RowSpan{Text: " · ", Role: ui.ColorActivityText})
			last.Spans = append(last.Spans, line.Spans...)
			return
		}
	}
	s.pages = append(s.pages, line)
}

func (s *ActivityStrip) Line(frame int) PaintLine {
	if len(s.pages) == 0 {
		return PaintLine{}
	}
	if s.resetCycle || frame < s.cycleStart {
		s.cycleStart, s.resetCycle = frame, false
	}
	index := 0
	if s.animating() {
		index = max(0, (frame-s.cycleStart)/ActivityPageDuration) % len(s.pages)
	}
	line := s.pages[index]
	if !s.animating() {
		return line
	}
	if !s.retryAt.IsZero() {
		remaining := max(0, (int(s.retryAt.UnixMilli())-frame+999)/1000)
		if remaining != s.waitSecond {
			text := s.retryPrefix + fmt.Sprintf(" · in %ds", remaining)
			if s.retryReason != "" {
				text += " · " + s.retryReason
			}
			text = rendering.TruncateVisual(text, s.width, "…")
			s.waitLine = PaintLine{Spans: []RowSpan{{Text: text, Role: ui.ColorActivityText}}}
			s.waitSecond = remaining
		}
		line = s.waitLine
		return s.animate(line, frame)
	}
	if !s.waitStart.IsZero() {
		seconds := max(0, (frame-int(s.waitStart.UnixMilli()))/1000)
		if seconds != s.waitSecond {
			s.waitLine = line
			elapsed := " · <1s"
			if seconds > 0 {
				elapsed = " · " + rendering.FormatThinkingDuration(time.Duration(seconds)*time.Second)
			}
			if line.VisibleWidth()+rendering.VisibleWidth(elapsed) <= s.width {
				s.waitLine.Spans = append(append([]RowSpan(nil), line.Spans...), RowSpan{Text: elapsed, Role: ui.ColorActivityText})
			}
			s.waitSecond = seconds
		}
		line = s.waitLine
	}
	return s.animate(line, frame)
}

func (s *ActivityStrip) animating() bool {
	switch s.source.CompactionPhase {
	case "compaction_completed", "compaction_cancelled", "compaction_failed", "compaction_interrupted", "compaction_cancelling":
		return false
	case "compaction_preparing", "compaction_extracting", "compaction_retrying", "compaction_committing":
		return s.source.Active
	}
	switch s.source.Status.Phase {
	case types.AgentIdle, types.AgentCancelling, types.AgentCancelled, types.AgentCompleted, types.AgentFailed, types.AgentCompactionResult, types.AgentNeedsAttention, types.AgentInterrupted:
		return false
	default:
		return s.source.Active
	}
}

func (s *ActivityStrip) animate(line PaintLine, frame int) PaintLine {
	line.TextTide = s.animating()
	if line.TextTide {
		forward := s.source.TideForward || s.source.ThinkingDisplay.Kind.IsSummary()
		line.TideCenter = s.tide.Advance(frame, line.VisibleWidth(), forward)
	}
	return line
}

func stripText(text string) string {
	return strings.Join(strings.Fields(rendering.SanitizeForTerminal(text)), " ")
}
