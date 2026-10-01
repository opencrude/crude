package chatprojection

import (
	"fmt"
	"reflect"
	"strings"
	"testing"
	"time"

	"crude/tui/cell"
	"crude/tui/surface"
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
	if line := strip.Line(200); !line.TextTide || !strings.Contains(line.PlainText(), "Thinking ▸ · Measured summary") {
		t.Fatalf("unclassified first request was not safely previewed: %+v", line)
	}
	view.ThinkingDisplay.Kind = reasoning.InferredSummary
	view.TideForward = true
	strip.Update(view, 100, th, tokens)
	line := strip.Line(300)
	if !line.TextTide || !strings.Contains(line.PlainText(), "Reasoning ▸ · Measured summary") {
		t.Fatalf("settling the same thought did not switch direction: %+v", line)
	}
	for _, span := range line.Spans {
		if span.Text != "" && (span.Role != ui.ColorActivityText || span.FGSet) {
			t.Fatalf("thinking preview used foreground-white Markdown ink: %+v", span)
		}
	}
	view.Thinking = ""
	view.ThinkingDisplay = reasoning.Display{}
	view.Status.Phase, view.Status.Detail = types.AgentTool, "Running tools"
	strip.Update(view, 100, th, tokens)
	line = strip.Line(400)
	if after := strip.Line(416); !after.TextTide || after.TideCenter <= line.TideCenter {
		t.Fatalf("direction reset before the active turn settled: %+v", after)
	}
}

func TestActivityStripStoppedPlansStayStill(t *testing.T) {
	th := theme.DefaultDark()
	tokens := ui.NewTokenSet(th)
	for _, width := range []int{40, 120} {
		for _, phase := range []types.AgentPhase{types.AgentCancelling, types.AgentCancelled, types.AgentIdle, types.AgentCompleted, types.AgentFailed, types.AgentRetrying} {
			t.Run(fmt.Sprintf("%s/%d", phase, width), func(t *testing.T) {
				var strip ActivityStrip
				p := &activity.Plan{ID: "plan", Status: "active", Total: 3, Objective: "Polish the activity presentation",
					Current: []activity.Step{{Number: 1, Status: "in_progress", Text: "Smooth the animation"}},
					Next:    &activity.Step{Number: 2, Text: "Verify the stopped plan and goal strip"}}
				view := types.AgentActivityView{Active: true, TideForward: true, Plan: p,
					Status: types.AgentStatusSnapshot{Phase: types.AgentTool, Detail: "Working"}}
				strip.Update(view, width, th, tokens)
				first := strip.Line(0)
				if rotated := strip.Line(6100); !rotated.TextTide || rotated.PlainText() == first.PlainText() {
					t.Fatal("fixture must rotate multiple active plan pages")
				}
				view.Active, view.Status.Phase, view.Status.Detail = false, phase, string(phase)
				view.Status.RetryAt = time.UnixMilli(12000)
				strip.Update(view, width, th, tokens)
				paint := func(frame int) []cell.Cell {
					line := strip.Line(frame)
					if line.TextTide {
						t.Fatal("stopped strip still has a light sweep")
					}
					if phase == types.AgentCancelled && !strings.HasPrefix(line.PlainText(), "Stopped") {
						t.Fatalf("stopped plan lacks its lifecycle label: %s", line.PlainText())
					}
					buf := surface.NewCellBuffer(width, 1)
					PaintLineAt(buf, 0, 0, width, line, tokens, 0, 0, false)
					cells := make([]cell.Cell, width)
					for x := range cells {
						cells[x] = buf.Get(x, 0)
					}
					return cells
				}
				stopped := paint(7000)
				for _, frame := range []int{7016, 13100, 20100, 100000} {
					if !reflect.DeepEqual(stopped, paint(frame)) {
						t.Fatalf("stopped plan changed its painted cells at %d", frame)
					}
				}
				if p.Status != "active" || p.Current[0].Status != "in_progress" {
					t.Fatal("stopping presentation changed the persisted plan lifecycle")
				}
				// The same strip resumes from a quiet entry and rotates again.
				view.Active, view.Status.Phase, view.Status.Detail = true, types.AgentTool, "Working"
				strip.Update(view, width, th, tokens)
				restarted := strip.Line(110000)
				if !restarted.TextTide || restarted.TideCenter >= 0 {
					t.Fatal("resumed tide did not enter from outside the text")
				}
				if strip.Line(116100).PlainText() == restarted.PlainText() {
					t.Fatal("plan pages did not resume rotation")
				}
			})
		}
	}
}

func TestCompactionActivityOwnsStripUntilNextTurn(t *testing.T) {
	th := theme.DefaultDark()
	tokens := ui.NewTokenSet(th)
	var strip ActivityStrip
	view := types.AgentActivityView{
		Active: true,
		Status: types.AgentStatusSnapshot{Phase: types.AgentCompacting, Detail: "Compacting context"},
		Plan: &activity.Plan{ID: "unrelated-plan", Status: "active", Total: 4, Completed: 3,
			Current: []activity.Step{{Number: 4, Text: "Run build, tests, and whole-repo lint"}}},
	}
	for _, phase := range []struct {
		name, label string
		active      bool
	}{
		{"compaction_preparing", "Compacting context", true},
		{"compaction_extracting", "Compacting context", true},
		{"compaction_retrying", "Recovering compaction", true},
		{"compaction_committing", "Saving checkpoint", true},
		{"compaction_cancelling", "Stopping compaction", false},
		{"compaction_completed", "Context compacted · Ready to continue", false},
		{"compaction_cancelled", "Compaction cancelled", false},
		{"compaction_failed", "Compaction failed · details in transcript", false},
		{"compaction_interrupted", "Compaction interrupted · original context retained", false},
	} {
		view.Active, view.CompactionPhase = phase.active, phase.name
		strip.Update(view, 100, th, tokens)
		for _, frame := range []int{0, 6500, 14000} {
			line := strip.Line(frame)
			if got := line.PlainText(); got != phase.label || line.TextTide != phase.active {
				t.Fatalf("phase=%s frame=%d: strip=%q active=%v", phase.name, frame, got, line.TextTide)
			}
		}
	}
	view.CompactionPhase = ""
	view.Active = true
	view.Status.Phase, view.Status.Detail = types.AgentTool, "Working"
	strip.Update(view, 100, th, tokens)
	if got := strip.Line(15000).PlainText(); !strings.Contains(got, "Working") || !strings.Contains(got, "Plan 3/4") {
		t.Fatalf("next turn did not restore the plan: %q", got)
	}
}

func TestActivityStripLiveTextKeepsCrestPosition(t *testing.T) {
	th := theme.DefaultDark()
	tokens := ui.NewTokenSet(th)
	var strip ActivityStrip
	view := types.AgentActivityView{Active: true, TideForward: true, Thinking: "Inspecting state",
		ThinkingDisplay: reasoning.Display{Kind: reasoning.Summary}, Status: types.AgentStatusSnapshot{Phase: types.AgentThinking}}
	strip.Update(view, 100, th, tokens)
	strip.Line(1700000000013)
	before := strip.Line(1700000001613)
	view.Thinking += " while more words arrive from the provider"
	strip.Update(view, 100, th, tokens)
	after := strip.Line(1700000001613)
	if after.VisibleWidth() <= before.VisibleWidth() || after.TideCenter != before.TideCenter {
		t.Fatalf("streamed words moved the crest: %f -> %f", before.TideCenter, after.TideCenter)
	}
	strip.Update(view, 45, th, tokens)
	if resized := strip.Line(1700000001613); resized.TideCenter != before.TideCenter {
		t.Fatal("resize rephased a visible crest")
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
