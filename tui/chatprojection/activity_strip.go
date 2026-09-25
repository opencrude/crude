package chatprojection

import (
	"fmt"
	"math"
	"strings"
	"time"

	"charm.land/glamour/v2"
	"crude/tui/rendering"
	"crude/tui/theme"
	"crude/tui/ui"
	"crude/types"
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
	md                       *glamour.TermRenderer
	cycleKey                 string
	cycleStart               int
	resetCycle               bool
	waitStart                time.Time
	retryAt                  time.Time
	retryPrefix, retryReason string
	waitSecond               int
	waitLine                 PaintLine
}

func (s *ActivityStrip) Update(source types.AgentActivityView, width int, th *theme.Theme, tokens *ui.TokenSet) {
	if s.ready && s.source == source && s.width == width && s.theme == th && s.tokens == tokens {
		return
	}
	if s.width != width || s.theme != th || s.tokens != tokens {
		s.md = nil
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
	label := stripText(status.Detail)
	if label == "" {
		label = "Ready"
		if source.Active {
			label = "Working"
		}
	}
	role := ui.ColorMuted
	if status.NeedsAction {
		role = ui.ColorWarning
	}
	if status.Phase == types.AgentFailed {
		role = ui.ColorError
	}
	if status.NeedsAction || status.Phase == types.AgentFailed || status.Phase == types.AgentRetrying || status.Phase == types.AgentCancelling {
		if status.Phase == types.AgentRetrying {
			label = "Retrying request"
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
		line := PaintLine{Spans: []RowSpan{{Text: label, Role: ui.ColorMuted}}}
		for _, span := range preview.Spans {
			span.Role = ui.ColorMuted // Markdown emphasis survives without foreground-white ink.
			span.FGSet = false
			line.Spans = append(line.Spans, span)
		}
		s.add(line)
	} else if source.Active || source.Plan == nil {
		s.add(PaintLine{Spans: []RowSpan{{Text: label, Role: role}}})
	}
	if p := source.Plan; p != nil {
		progress := fmt.Sprintf("Plan %d/%d done", p.Completed, p.Total)
		if p.Status == "paused" {
			progress = "Plan paused · " + fmt.Sprintf("%d/%d done", p.Completed, p.Total)
		}
		if p.Skipped > 0 {
			progress += fmt.Sprintf(" · %d skipped", p.Skipped)
		}
		if p.Cancelled > 0 {
			progress += fmt.Sprintf(" · %d cancelled", p.Cancelled)
		}
		if len(p.Current) == 0 {
			progress += " · " + stripText(p.Objective)
		}
		for i, step := range p.Current {
			prefix := fmt.Sprintf("Step %d/%d", step.Number, p.Total)
			stepRole := ui.ColorMuted
			if i == 0 {
				prefix = progress + " · " + prefix
			}
			if step.Status == "blocked" || step.Status == "failed" {
				prefix += " " + step.Status
				stepRole = ui.ColorMuted
			}
			text := stripText(step.Text)
			if step.Reason != "" {
				text += " · " + stripText(step.Reason)
			}
			s.add(PaintLine{Spans: []RowSpan{{Text: prefix + ": ", Role: stepRole}, {Text: text, Role: ui.ColorMuted}}})
		}
		if len(p.Current) == 0 {
			s.add(PaintLine{Spans: []RowSpan{{Text: progress, Role: ui.ColorMuted}}})
		}
		if p.Objective != "" && len(p.Current) > 0 {
			s.add(PaintLine{Spans: []RowSpan{{Text: "Goal · " + stripText(p.Objective), Role: ui.ColorMuted}}})
		}
		if step := p.Next; step != nil {
			s.add(PaintLine{Spans: []RowSpan{{Text: fmt.Sprintf("Next %d/%d · %s", step.Number, p.Total, stripText(step.Text)), Role: ui.ColorMuted}}})
		}
	}
	// Counts never consume a page or displace useful activity. They occupy only
	// spare trailing space on the last eligible page.
	if source.SuccessfulCalls > 0 && len(s.pages) > 0 {
		count := fmt.Sprint(source.SuccessfulCalls)
		if !source.CallsKnown {
			count += "+"
		}
		label := " turns"
		if source.CallsKnown && source.SuccessfulCalls == 1 {
			label = " turn"
		}
		suffix := " · " + count + label
		last := &s.pages[len(s.pages)-1]
		if last.VisibleWidth()+rendering.VisibleWidth(suffix) <= width {
			last.Spans = append(last.Spans, RowSpan{Text: suffix, Role: ui.ColorMuted})
		}
	}
}

// add packs complete facts when space permits; otherwise it creates a bounded
// page. Higher-priority facts enter first and are never displaced by later ones.
func (s *ActivityStrip) add(line PaintLine) {
	if line.VisibleWidth() > s.width {
		line = TruncatePaintLine(line, max(0, s.width-1))
		line.Spans = append(line.Spans, RowSpan{Text: "…", Role: ui.ColorMuted})
	}
	if len(s.pages) > 0 {
		last := &s.pages[len(s.pages)-1]
		if last.VisibleWidth()+3+line.VisibleWidth() <= s.width {
			last.Spans = append(last.Spans, RowSpan{Text: " · ", Role: ui.ColorMuted})
			last.Spans = append(last.Spans, line.Spans...)
			last.TextTide = last.TextTide || line.TextTide
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
	index := max(0, (frame-s.cycleStart)/ActivityPageDuration) % len(s.pages)
	line := s.pages[index]
	if !s.retryAt.IsZero() {
		remaining := max(0, (int(s.retryAt.UnixMilli())-frame+999)/1000)
		if remaining != s.waitSecond {
			text := s.retryPrefix + fmt.Sprintf(" · in %ds", remaining)
			if s.retryReason != "" {
				text += " · " + s.retryReason
			}
			text = rendering.TruncateVisual(text, s.width, "…")
			s.waitLine = PaintLine{Spans: []RowSpan{{Text: text, Role: ui.ColorMuted}}}
			s.waitSecond = remaining
		}
		line = s.waitLine
		line.TextTide = s.source.Active
		line.TideForward = s.source.TideForward || s.source.ThinkingDisplay.Kind.IsSummary()
		line.TideFrame = frame
		return line
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
				s.waitLine.Spans = append(append([]RowSpan(nil), line.Spans...), RowSpan{Text: elapsed, Role: ui.ColorMuted})
			}
			s.waitSecond = seconds
		}
		line = s.waitLine
	}
	line.TextTide = s.source.Active
	line.TideForward = s.source.TideForward || s.source.ThinkingDisplay.Kind.IsSummary()
	line.TideFrame = frame
	return line
}

func stripText(text string) string {
	return strings.Join(strings.Fields(rendering.SanitizeForTerminal(text)), " ")
}
