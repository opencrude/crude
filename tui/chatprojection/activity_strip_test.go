package chatprojection

import (
	"strings"
	"testing"
	"time"

	"crude/tui/theme"
	"crude/tui/ui"
	"crude/types"
	"crude/types/activity"
	"crude/types/reasoning"
)

func TestActivityStripPinsUploadWaitAndActionsAheadOfOptionalFacts(t *testing.T) {
	th := theme.DefaultDark()
	tokens := ui.NewTokenSet(th)
	var strip ActivityStrip
	view := types.AgentActivityView{Summary: "do not echo my prompt", Active: true, SuccessfulCalls: 25, CallsKnown: true,
		Plan:   &activity.Plan{ID: "plan", Total: 3, Current: []activity.Step{{Number: 1, Text: "implement activity"}}},
		Status: types.AgentStatusSnapshot{Phase: types.AgentUploading, UploadedBytes: 43008, TotalBytes: 102400, PhaseStartedAt: time.UnixMilli(1000)}}
	for _, phase := range []types.AgentPhase{types.AgentPreparing, types.AgentUploading, types.AgentWaiting} {
		view.Status.Phase = phase
		if phase == types.AgentWaiting {
			view.Status.Detail = "Waiting for OpenAI"
			view.Status.UploadedBytes = view.Status.TotalBytes
		}
		strip.Update(view, 90, th, tokens)
		for _, frame := range []int{1000, 3000, 9000, 13000} {
			line := strip.Line(frame)
			text := line.PlainText()
			if !strings.Contains(text, "↑") || !strings.Contains(text, "/ 100 KB") || !strings.Contains(text, "%") || strings.Contains(text, "Plan") || strings.Contains(text, "turn") || strings.Contains(text, "prompt") || !line.TextTide {
				t.Fatalf("phase=%s frame=%d: %+v", phase, frame, line)
			}
			if phase == types.AgentWaiting && (!strings.Contains(text, "Waiting for OpenAI") || !strings.Contains(text, "100%")) {
				t.Fatalf("lost waiting/upload parity: %s", text)
			}
		}
	}
	view.Status.Phase, view.Status.NeedsAction, view.Status.Detail = types.AgentApproval, true, "Approval required"
	strip.Update(view, 90, th, tokens)
	for _, frame := range []int{20000, 60000} {
		if got := strip.Line(frame).PlainText(); got != "Approval required" {
			t.Fatalf("action hidden by secondary content: %s", got)
		}
	}
	view.Status.Phase, view.Status.NeedsAction, view.Status.RetryAt = types.AgentRetrying, false, time.UnixMilli(65000)
	view.RetryAttempt, view.RetryLimit, view.RetryReason = 2, 3, "Connection stalled"
	strip.Update(view, 90, th, tokens)
	if text := strip.Line(61000).PlainText(); !strings.Contains(text, "attempt 2/3") || !strings.Contains(text, "in 4s") || !strings.Contains(text, "Connection stalled") {
		t.Fatalf("retry parity=%s", text)
	}
}

func TestActivityStripPacksThenRotatesPlanFactsInOrder(t *testing.T) {
	th := theme.DefaultDark()
	tokens := ui.NewTokenSet(th)
	var strip ActivityStrip
	view := types.AgentActivityView{Active: true, Thinking: "**Inspecting the renderer and state ownership**", ThinkingDisplay: reasoning.Display{Kind: reasoning.Summary, BlockID: "one"}, SuccessfulCalls: 8, CallsKnown: true,
		Status: types.AgentStatusSnapshot{Ref: types.ResourceRef{ID: "owner"}, Phase: types.AgentThinking},
		Plan:   &activity.Plan{ID: "plan", Status: "active", Total: 4, Completed: 1, Objective: "Repair activity ownership", Current: []activity.Step{{Number: 2, Text: "Implement the projector"}}, Next: &activity.Step{Number: 3, Text: "Run tests"}}}
	strip.Update(view, 48, th, tokens)
	first := strip.Line(1000).PlainText()
	if !strings.Contains(first, "Inspecting the renderer") || strings.HasPrefix(first, "8") {
		t.Fatalf("wrong priority: %s", first)
	}
	if next := strip.Line(7000).PlainText(); !strings.Contains(next, "Plan 1/4 done") || !strings.Contains(next, "Step 2/4") {
		t.Fatalf("active step lost: %s", next)
	}
	if next := strip.Line(13000).PlainText(); !strings.Contains(next, "Goal") {
		t.Fatalf("goal order: %s", next)
	}
	if next := strip.Line(19000).PlainText(); !strings.Contains(next, "Next 3/4") || !strings.HasSuffix(next, "8 turns") {
		t.Fatalf("next step/count priority: %s", next)
	}
	if next := strip.Line(25000).PlainText(); next != first {
		t.Fatalf("cycle did not wrap: %s", next)
	}
	// Transport deltas must not continually restart rotation and starve plans.
	view.Thinking += "."
	strip.Update(view, 48, th, tokens)
	if line := strip.Line(31000).PlainText(); !strings.Contains(line, "Step 2/4") {
		t.Fatalf("token update starved plan: %s", line)
	}
	// New semantic blocks preempt secondary information immediately.
	view.ThinkingDisplay.BlockID = "two"
	strip.Update(view, 48, th, tokens)
	if line := strip.Line(31500); !line.TextTide {
		t.Fatalf("new thought did not regain priority: %+v", line)
	}
	strip.Update(view, 260, th, tokens)
	if len(strip.pages) != 1 {
		t.Fatalf("wide line wasted space on %d pages", len(strip.pages))
	}
	if text := strip.Line(32000).PlainText(); !strings.Contains(text, "Step 2/4") || !strings.HasSuffix(text, "8 turns") {
		t.Fatalf("wide packing=%s", text)
	}
}

func TestActivityStripNeverAppliesSummaryTreatmentToRawOrUnknown(t *testing.T) {
	th := theme.DefaultDark()
	tokens := ui.NewTokenSet(th)
	for _, kind := range []reasoning.Kind{reasoning.Raw, reasoning.Unknown, reasoning.Encrypted} {
		var strip ActivityStrip
		view := types.AgentActivityView{Active: true, Thinking: "must stay in the pane", ThinkingDisplay: reasoning.Display{Kind: kind}, Status: types.AgentStatusSnapshot{Phase: types.AgentThinking, Detail: "Reasoning"}}
		strip.Update(view, 100, th, tokens)
		line := strip.Line(100)
		if !line.TextTide || strings.Contains(line.PlainText(), "must stay") {
			t.Fatalf("classified %q as summary: %+v", kind, line)
		}
	}
	var strip ActivityStrip
	view := types.AgentActivityView{Active: true, Thinking: "**Measured summary**", ThinkingDisplay: reasoning.Display{Kind: reasoning.InferredSummary}, Status: types.AgentStatusSnapshot{Phase: types.AgentThinking}}
	strip.Update(view, 100, th, tokens)
	if line := strip.Line(100); !line.TextTide || !strings.Contains(line.PlainText(), "Measured summary") {
		t.Fatalf("measured summary remained in a pane: %+v", line)
	}
	view.ThinkingDisplay.Kind = reasoning.Provisional
	strip.Update(view, 100, th, tokens)
	if line := strip.Line(200); !line.TextTide || line.TideForward || !strings.Contains(line.PlainText(), "Thinking ▸ · Measured summary") {
		t.Fatalf("unclassified first request was not safely previewed: %+v", line)
	}
	view.ThinkingDisplay.Kind = reasoning.InferredSummary
	view.TideForward = true
	strip.Update(view, 100, th, tokens)
	line := strip.Line(300)
	if !line.TideForward || !strings.Contains(line.PlainText(), "Reasoning ▸ · Measured summary") {
		t.Fatalf("settling the same thought did not switch direction: %+v", line)
	}
	for _, span := range line.Spans {
		if span.Text != "" && (span.Role != ui.ColorMuted || span.FGSet) {
			t.Fatalf("thinking preview used foreground-white Markdown ink: %+v", span)
		}
	}
	view.Thinking = ""
	view.ThinkingDisplay = reasoning.Display{}
	view.Status.Phase, view.Status.Detail = types.AgentTool, "Running tools"
	strip.Update(view, 100, th, tokens)
	if after := strip.Line(400); !after.TideForward || !after.TextTide {
		t.Fatalf("direction reset before the active turn settled: %+v", after)
	}
}

func BenchmarkActivityStripRetainedFrame(b *testing.B) {
	th := theme.DefaultDark()
	tokens := ui.NewTokenSet(th)
	var strip ActivityStrip
	view := types.AgentActivityView{Active: true, Thinking: "**Inspecting** `shared` state", ThinkingDisplay: reasoning.Display{Kind: reasoning.Summary}, Status: types.AgentStatusSnapshot{Phase: types.AgentThinking}}
	strip.Update(view, 100, th, tokens)
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		strip.Update(view, 100, th, tokens)
		_ = strip.Line(i * 16)
	}
}
