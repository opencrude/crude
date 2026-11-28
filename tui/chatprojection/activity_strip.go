package chatprojection

import (
	"fmt"
	"math"
	"strconv"
	"strings"
	"time"

	"crude/tui/rendering"
	"crude/tui/theme"
	"crude/tui/ui"
	"crude/types/activity"
	"crude/types/reasoning"
	"crude/types/statuscenter"
)

const ActivityPageDuration = 6000 // milliseconds on the shared presentation clock

// ActivityStrip derives retained, bounded rows from workspace facts. It owns
// presentation cadence only: no domain mutation, protocol parsing, timers or I/O.
type ActivityStrip struct {
	source                   statuscenter.AgentActivityView
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
	retryMark                PaintLine
	waitSecond               int
	waitLine                 PaintLine
	tide                     ui.TextTideMotion
}

// sameActivitySource compares two projections by value. Connected shells
// copy the plan on every read, so its pointer is not its identity: the
// committed revision is.
func sameActivitySource(a, b statuscenter.AgentActivityView) bool {
	if !activity.SameRevision(a.Plan, b.Plan) {
		return false
	}
	b.Plan = a.Plan
	return a == b
}

// Update rebuilds the strip when the activity, the width or the theme changed.
func (s *ActivityStrip) Update(source statuscenter.AgentActivityView, width int, th *theme.Theme, tokens *ui.TokenSet) {
	if s.ready && sameActivitySource(s.source, source) && s.width == width && s.theme == th && s.tokens == tokens {
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
		label, role, mark := "Compacting context", ui.ColorActivityText, PaintLine{}
		switch source.CompactionPhase {
		case "compaction_cancelling":
			label = "Stopping compaction"
		case "compaction_retrying":
			label, role, mark = "Recovering compaction", ui.ColorWarning, problemMark(ui.ColorWarning)
		case "compaction_committing":
			label = "Saving checkpoint"
		case "compaction_completed":
			label = "Context compacted · Ready to continue"
		case "compaction_cancelled":
			label = "Compaction cancelled"
		case "compaction_failed":
			label, role, mark = "Compaction failed · details in transcript", ui.ColorError, problemMark(ui.ColorError)
		case "compaction_interrupted":
			label, role, mark = "Compaction interrupted · original context kept", ui.ColorWarning, problemMark(ui.ColorWarning)
		}
		s.add(markedLine(mark, RowSpan{Text: label, Role: role}))
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
	if status.Phase == statuscenter.AgentFailed {
		role = ui.ColorError
		label = "Turn failed · details in transcript"
	}
	if !s.animating() {
		s.tide = ui.TextTideMotion{}
	}
	if status.Phase == statuscenter.AgentCancelled {
		s.add(PaintLine{Spans: []RowSpan{{Text: "Stopped", Role: ui.ColorActivityText}}})
		return
	}
	if status.Phase == statuscenter.AgentNeedsAttention {
		label, role = "Needs attention · work kept · /retry to continue", ui.ColorWarning
	}
	if status.Phase == statuscenter.AgentInterrupted {
		label, role = "Interrupted · work kept · /retry to continue", ui.ColorWarning
	}
	if status.NeedsAction || status.Phase == statuscenter.AgentNeedsAttention || status.Phase == statuscenter.AgentInterrupted || status.Phase == statuscenter.AgentFailed || status.Phase == statuscenter.AgentRetrying || status.Phase == statuscenter.AgentRecovering || status.Phase == statuscenter.AgentCancelling {
		// Something went wrong: the strip leads with the same sign the
		// transcript uses, so a retry wait never reads as ordinary work.
		var mark PaintLine
		switch status.Phase {
		case statuscenter.AgentFailed:
			mark = problemMark(ui.ColorError)
		case statuscenter.AgentNeedsAttention, statuscenter.AgentInterrupted, statuscenter.AgentRetrying, statuscenter.AgentRecovering:
			mark = problemMark(ui.ColorWarning)
		}
		if status.Phase == statuscenter.AgentRetrying || status.Phase == statuscenter.AgentRecovering {
			label = "Retrying request"
			if status.Phase == statuscenter.AgentRecovering {
				label = "Recovering"
				source.RetryAttempt, source.RetryLimit, source.RetryReason = source.Recovery.Attempt, 0, source.Recovery.Summary
			}
			if source.RetryAttempt > 0 {
				label += fmt.Sprintf(" · attempt %d", source.RetryAttempt)
			}
			if source.RetryLimit > 0 {
				label += fmt.Sprintf("/%d", source.RetryLimit)
			}
			s.retryAt, s.retryPrefix, s.retryReason, s.retryMark = status.RetryAt, label, stripText(source.RetryReason), mark
			s.add(s.retryLine(label))
			return
		}
		s.add(markedLine(mark, RowSpan{Text: label, Role: role}))
		return
	}
	if status.Phase == statuscenter.AgentSending || status.Phase == statuscenter.AgentPreparing || status.Phase == statuscenter.AgentUploading || status.Phase == statuscenter.AgentWaiting {
		if status.Phase == statuscenter.AgentSending {
			label = "Sending"
		}
		if status.Phase == statuscenter.AgentPreparing {
			label = "Preparing"
		}
		if status.Phase == statuscenter.AgentUploading {
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
		if status.Phase == statuscenter.AgentWaiting {
			s.waitStart = status.PhaseStartedAt
		}
		s.add(PaintLine{Spans: []RowSpan{{Text: label, Role: role}}})
		return // uploads, waits and action states are never hidden by rotation
	}

	if status.Phase == statuscenter.AgentThinking && source.Thinking != "" && source.ThinkingDisplay.Kind.HasCompactPreview() {
		if s.md == nil {
			s.md = rendering.MakeMdRenderer(max(20, width), th)
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
	} else if source.Active && livePlan(source.Plan) {
		s.add(compactPlanLine(source.Plan))
		return
	} else {
		s.add(PaintLine{Spans: []RowSpan{{Text: label, Role: role}}})
	}
	if source.Active && livePlan(source.Plan) {
		s.add(compactPlanLine(source.Plan))
	}
}

// problemMark is the sign that leads a strip row about a problem: the warning
// sign the transcript's recovery rows use, or the failed turn's cross.
func problemMark(role ui.SemanticColor) PaintLine {
	glyph := "⚠"
	if role == ui.ColorError {
		glyph = "✕"
	}
	return PaintLine{Spans: []RowSpan{{Text: glyph + " ", Role: role}}}
}

// markedLine prefixes spans with an optional problem sign.
func markedLine(mark PaintLine, spans ...RowSpan) PaintLine {
	return PaintLine{Spans: append(append([]RowSpan(nil), mark.Spans...), spans...)}
}

// retryLine is a retry row: the sign and heading in the warning color and
// the provider's reason in supporting ink.
func (s *ActivityStrip) retryLine(heading string) PaintLine {
	spans := []RowSpan{{Text: heading, Role: ui.ColorWarning}}
	if s.retryReason != "" {
		spans = append(spans, RowSpan{Text: " · " + s.retryReason, Role: ui.ColorActivityText})
	}
	return fitStripLine(markedLine(s.retryMark, spans...), s.width)
}

// fitStripLine truncates a row wider than the strip with an ellipsis.
func fitStripLine(line PaintLine, width int) PaintLine {
	if line.VisibleWidth() > width {
		line = TruncatePaintLine(line, max(0, width-1))
		line.Spans = append(line.Spans, RowSpan{Text: "…", Role: ui.ColorActivityText})
	}
	return line
}

// livePlan reports whether the plan describes the work of the running agent.
// A paused plan waits for the user; a later run that works on something else
// must not show it as its activity. The plan popover still shows it.
func livePlan(p *activity.Plan) bool { return p != nil && p.Status != "paused" }

// compactPlanLine keeps full plan detail out of the fixed status band.
func compactPlanLine(p *activity.Plan) PaintLine {
	var numbers []string
	var task string
	for _, step := range p.Current {
		if step.Status == "in_progress" || step.Status == "" {
			if len(numbers) == 0 {
				task = step.Text
			}
			numbers = append(numbers, strconv.Itoa(step.Number))
		}
	}
	if len(numbers) == 0 && p.Next != nil {
		numbers = append(numbers, strconv.Itoa(p.Next.Number))
		task = p.Next.Text
	}
	label := "Plan ready"
	if len(numbers) != 0 {
		label = fmt.Sprintf("%s/%d %s", strings.Join(numbers, ","), p.Total, stripText(task))
	}
	return PaintLine{Spans: []RowSpan{{Text: label, Role: ui.ColorActivityText}}}
}

// add packs complete facts when space permits; otherwise it creates a bounded
// page. Higher-priority facts enter first and are never displaced by later ones.
func (s *ActivityStrip) add(line PaintLine) {
	line = fitStripLine(line, s.width)
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
			s.waitLine = s.retryLine(s.retryPrefix + fmt.Sprintf(" · in %ds", remaining))
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
	case statuscenter.AgentIdle, statuscenter.AgentCancelling, statuscenter.AgentCancelled, statuscenter.AgentCompleted, statuscenter.AgentFailed, statuscenter.AgentCompactionResult, statuscenter.AgentNeedsAttention, statuscenter.AgentInterrupted:
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
