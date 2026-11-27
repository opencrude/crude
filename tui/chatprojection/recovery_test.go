package chatprojection

import (
	"encoding/json"
	"reflect"
	"strings"
	"testing"
	"time"

	"crude/tui/cell"
	"crude/tui/theme"
	"crude/tui/ui"
	"crude/types/conversation"
	"crude/types/recovery"
	"crude/types/statuscenter"
)

func TestExpandedRecoveryKeepsDiagnosticsReadableAndHydrationIdentical(t *testing.T) {
	for _, th := range []*theme.Theme{theme.DefaultDark(), theme.DefaultLight()} {
		for _, width := range []int{1, 12, 40, 80, 160} {
			for _, state := range []recovery.State{recovery.Recovering, recovery.NeedsAttention, recovery.Recovered, recovery.Interrupted, recovery.Failed} {
				status := recovery.Status{
					ID: "incident", AgentID: "owner", Generation: 4, State: state,
					StartedAt: time.Unix(1000, 0), UpdatedAt: time.Unix(1001, 0),
					Summary: "Provider authentication needs attention",
					Detail:  strings.Repeat("Check the configured account and endpoint. ", 20) + "Diagnostic-tail-保持-👨‍👩‍👧‍👦",
					Action:  "Update Accounts, then use Continue run or /retry.",
				}
				if state == recovery.Recovering {
					status.RetryAt = time.UnixMilli(30000)
				}
				if width == 1 {
					// A two-column grapheme cannot be shown atomically in one
					// physical column; its complete evidence remains copyable.
					status.Detail = strings.Repeat("Full diagnostic content. ", 20) + "Diagnostic-tail"
				}
				live := conversation.ChatMessage{InternalID: status.ID, Role: conversation.RoleStatus, Time: status.StartedAt, Recovery: &status}
				encoded, err := json.Marshal(live)
				if err != nil {
					t.Fatal(err)
				}
				var hydrated conversation.ChatMessage
				if err := json.Unmarshal(encoded, &hydrated); err != nil {
					t.Fatal(err)
				}
				ctx := renderContext{th: th, tokens: ui.NewTokenSet(th), innerW: width, contentWidth: max(1, width-16), spinFrame: 1000, disclosure: DisclosureExpanded}
				rows := renderMessageContent(live, ctx, false)
				resumed := renderMessageContent(hydrated, ctx, false)
				if !reflect.DeepEqual(rows.native, resumed.native) || rows.frameSensitive != (state == recovery.Recovering) || len(rows.native) < 2 {
					t.Fatalf("state=%s width=%d: recovery drifted on hydration/tick", state, width)
				}
				body := strings.Join(rows.lines[1:], "")
				compact := func(text string) string {
					return strings.Join(strings.Fields(strings.NewReplacer("│", "", "▎", "").Replace(text)), "")
				}
				if !strings.Contains(compact(body), compact(status.Detail)) || !strings.Contains(compact(body), compact(status.Action)) {
					t.Fatalf("diagnostic or action was truncated at width %d", width)
				}
				for index, line := range rows.native {
					if line.VisibleWidth() > width {
						t.Fatalf("notice exceeded actual viewport: %+v", line)
					}
					if index > 0 {
						for _, span := range line.Spans {
							if span.Role != ui.ColorFg && span.Role != ui.ColorMuted && strings.TrimSpace(span.Text) != "▎" {
								t.Fatalf("whole diagnostic was severity-tinted: %+v", span)
							}
						}
					}
				}
			}
		}
	}
}

func TestRecoveryAcknowledgedAttemptIsStaticBoundary(t *testing.T) {
	status := recovery.Status{
		ID: "incident", AgentID: "owner", Generation: 1, State: recovery.Recovering,
		Summary: "Retrying unfinished run", Detail: "Original connection diagnostic",
	}
	renderer := NewRenderer(theme.DefaultDark())
	renderer.SetSize(100, 30)
	renderer.SetChatAreaBounds(0, 30)
	status.RetryAt = time.UnixMilli(30000)
	renderer.SyncMessages([]conversation.ChatMessage{{InternalID: status.ID, Role: conversation.RoleStatus, Recovery: &status}}, 1, 0)
	renderer.ToggleDisclosure(status.ID)
	renderer.SetSpinFrame(800)
	before := renderer.ExportMessageRowsSnapshot(100)
	if !strings.Contains(before.Rows[0].Lines[1], "Recovering") {
		t.Fatal("fixture did not begin with a recovering cooldown")
	}
	resumed := status
	resumed.RetryAt = time.Time{}
	renderer.SyncMessages([]conversation.ChatMessage{{InternalID: status.ID, Role: conversation.RoleStatus, Recovery: &resumed}}, 2, 0)
	first := renderer.ExportMessageRowsSnapshot(100)
	if len(first.Rows) != 1 || !strings.Contains(first.Rows[0].Lines[1], "Run resumed") {
		t.Fatalf("acknowledged attempt still claims recovery: %+v", first.Rows)
	}
	for _, frame := range []int{1200, 1600, 9000} {
		renderer.SetSpinFrame(frame)
		later := renderer.ExportMessageRowsSnapshot(100)
		if later.Generation != first.Generation || !reflect.DeepEqual(later.Rows[0].Native, first.Rows[0].Native) {
			t.Fatal("resumed incident still blinks or invalidates transcript on ticks")
		}
	}
	for _, line := range first.Rows[0].Native {
		for _, span := range line.Spans {
			if span.Action != nil || span.Role == ui.ColorError {
				t.Fatalf("resumed boundary kept recovery action/red indicator: %+v", span)
			}
		}
	}
	if !strings.Contains(strings.Join(first.Rows[0].Lines, "\n"), status.Detail) {
		t.Fatal("resuming lost the retained diagnostic")
	}
	data, err := json.Marshal(conversation.ChatMessage{InternalID: resumed.ID, Role: conversation.RoleStatus, Recovery: &resumed})
	if err != nil {
		t.Fatal(err)
	}
	var hydrated conversation.ChatMessage
	if err := json.Unmarshal(data, &hydrated); err != nil {
		t.Fatal(err)
	}
	ctx := renderContext{th: theme.DefaultDark(), tokens: ui.NewTokenSet(theme.DefaultDark()), innerW: 100, disclosure: DisclosureExpanded}
	projection := renderRecoveryMessage(hydrated, ctx)
	if projection.frameSensitive || strings.Contains(projection.lines[0], "Recovering") || !strings.Contains(projection.lines[0], "Run resumed") {
		t.Fatalf("persisted acknowledged incident drifted: %+v", projection)
	}
}

func TestRecoveryDefaultsCompactWithCountdownBlinkAndExpandableEvidence(t *testing.T) {
	th := theme.DefaultDark()
	renderer := NewRenderer(th)
	renderer.SetSize(100, 30)
	renderer.SetChatAreaBounds(0, 30)
	status := recovery.Status{
		ID: "incident", AgentID: "owner", Generation: 4, State: recovery.Recovering,
		RetryAt: time.UnixMilli(62000), Summary: "Connection failed",
		Detail: "the actual DNS failure and retained diagnostic tail",
	}
	renderer.SyncMessages([]conversation.ChatMessage{{InternalID: status.ID, Role: conversation.RoleStatus, Recovery: &status}}, 1, 0)
	renderer.SetSpinFrame(1000)
	rows, _, _ := renderer.ExportMessageRows(100)
	if len(rows) != 1 || len(rows[0].Lines) != 2 || !rows[0].Toggle ||
		!strings.Contains(rows[0].Lines[1], "retry in 61s") ||
		!strings.Contains(rows[0].Lines[1], "(Retry now)") ||
		strings.Contains(strings.Join(rows[0].Lines, ""), "DNS") {
		t.Fatalf("compact recovery contract = %+v", rows)
	}
	renderer.SetSpinFrame(1400)
	off, _, _ := renderer.ExportMessageRows(100)
	offText := off[0].Lines[1]
	renderer.SetSpinFrame(2400)
	on, _, _ := renderer.ExportMessageRows(100)
	if strings.Contains(offText, "■") || !strings.Contains(on[0].Lines[1], "■") ||
		!strings.Contains(on[0].Lines[1], "retry in 60s") {
		t.Fatalf("clock did not update square/countdown: off=%v on=%v", off[0].Lines, on[0].Lines)
	}
	renderer.ToggleLastDisclosure()
	expanded, _, _ := renderer.ExportMessageRows(100)
	if !strings.Contains(strings.Join(expanded[0].Lines, "\n"), status.Detail) {
		t.Fatal("disclosure lost actual reason")
	}
	renderer.SetSpinFrame(2800)
	later, _, _ := renderer.ExportMessageRows(100)
	if !reflect.DeepEqual(expanded[0].Native[2:], later[0].Native[2:]) {
		t.Fatal("animation reflowed expanded evidence")
	}
	renderer.ToggleLastDisclosure()
	collapsed, _, _ := renderer.ExportMessageRows(100)
	if len(collapsed[0].Lines) != 2 {
		t.Fatal("recovery did not collapse back to one content row")
	}
}

func TestRecoveryStripDoesNotAnimateAttentionOrEchoDiagnostics(t *testing.T) {
	th := theme.DefaultDark()
	for _, phase := range []statuscenter.AgentPhase{statuscenter.AgentNeedsAttention, statuscenter.AgentInterrupted} {
		var strip ActivityStrip
		strip.Update(statuscenter.AgentActivityView{
			Active: true, Status: statuscenter.AgentStatusSnapshot{Phase: phase, NeedsAction: true},
			Recovery: recovery.Status{ID: "incident", State: recovery.NeedsAttention, Detail: "private verbose diagnostic"},
		}, 80, th, ui.NewTokenSet(th))
		first, later := strip.Line(1000), strip.Line(9000)
		if first.TextTide || !reflect.DeepEqual(first, later) || strings.Contains(first.PlainText(), "private") || !strings.Contains(first.PlainText(), "/retry") {
			t.Fatalf("attention strip is noisy/nonactionable: %+v", first)
		}
	}
}

func TestRecoverySummaryAndActionChangesInvalidateRetainedRows(t *testing.T) {
	syncer := NewMessageSync()
	first := conversation.ChatMessage{InternalID: "incident", Role: conversation.RoleStatus, Recovery: &recovery.Status{ID: "incident", Summary: "old"}}
	_, _ = syncer.SyncMessages([]conversation.ChatMessage{first}, 1, 0)
	changed := first
	status := *first.Recovery
	status.Action = "Continue run"
	changed.Recovery = &status
	result, _ := syncer.SyncMessages([]conversation.ChatMessage{changed}, 2, 0)
	if len(result.ChangedIDs) != 1 || result.ChangedIDs[0] != "incident" {
		t.Fatalf("recovery action-only delta failed to invalidate painted rows: %+v", result)
	}
}

func BenchmarkExpandedRecoveryClockShort(b *testing.B) {
	benchmarkExpandedRecoveryClock(b, 1)
}

func BenchmarkExpandedRecoveryClockDense(b *testing.B) {
	benchmarkExpandedRecoveryClock(b, 2000)
}

func benchmarkExpandedRecoveryClock(b *testing.B, paragraphs int) {
	renderer := NewRenderer(theme.DefaultDark())
	renderer.SetSize(100, 30)
	renderer.SetChatAreaBounds(0, 30)
	status := recovery.Status{ID: "incident", AgentID: "owner", Generation: 1,
		State: recovery.Recovering, RetryAt: time.UnixMilli(300000),
		Detail: strings.Repeat("Accepted diagnostic evidence stays cached across clock updates.\n", paragraphs)}
	renderer.SyncMessages([]conversation.ChatMessage{{InternalID: status.ID, Role: conversation.RoleStatus, Recovery: &status}}, 1, 0)
	renderer.ToggleDisclosure(status.ID)
	renderer.SetSpinFrame(1000)
	_, _, _ = renderer.ExportMessageRows(100)
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		renderer.SetSpinFrame(1000 + (i%500)*400)
		_, _, _ = renderer.ExportMessageRows(100)
	}
}

// A turn that stopped on an error must stand apart from tool rows and
// assistant text: before this card its heading painted in muted ink and its
// expanded body repeated "Turn failed" with no next step.
func TestFailedTurnIsOneDistinctCard(t *testing.T) {
	failed := recovery.Status{
		ID: "incident", AgentID: "owner", Generation: 2, State: recovery.Failed,
		Summary: "Turn failed",
		Detail:  "Provider quota or billing limit reached: API 429: insufficient_quota: The account has no remaining credit.",
	}
	for _, th := range []*theme.Theme{theme.DefaultDark(), theme.DefaultLight()} {
		for _, message := range []conversation.ChatMessage{
			{InternalID: "incident", Role: conversation.RoleStatus, Recovery: &failed},
			{InternalID: "error", Role: conversation.RoleError, Content: failed.Detail},
		} {
			ctx := renderContext{th: th, tokens: ui.NewTokenSet(th), innerW: 100, contentWidth: 84}
			collapsed := renderMessageContent(message, ctx, false)
			header := collapsed.native[len(collapsed.native)-1]
			if header.VisibleWidth() != ctx.innerW {
				t.Fatalf("the error band does not fill the row: %q", header.PlainText())
			}
			want := []struct {
				text string
				role ui.SemanticColor
			}{{"✕ ", ui.ColorError}, {"Turn failed", ui.ColorError}, {" · ", ui.ColorMuted}}
			for i, w := range want {
				span := header.Spans[i+1]
				if span.Text != w.text || span.Role != w.role {
					t.Fatalf("heading span %d = %+v, want %q", i, span, w.text)
				}
			}
			if header.Spans[1].Attr != cell.AttrBold || header.Spans[2].Attr != cell.AttrBold || header.Spans[4].Role != ui.ColorFg ||
				!strings.HasPrefix(header.Spans[4].Text, "Provider quota or billing limit reached") {
				t.Fatalf("heading is not a bold severity title with a readable reason: %+v", header.Spans)
			}
			for i, span := range header.Spans {
				if tinted := span.BgSet && span.BgRole == ui.ColorIncompleteBg; tinted != (i > 0) {
					t.Fatalf("only the transcript gutter stays off the error band: %d %+v", i, span)
				}
			}
			ctx.disclosure = DisclosureExpanded
			expanded := renderMessageContent(message, ctx, false)
			body := expanded.native[1:]
			text := strings.Join(expanded.lines, "\n")
			if strings.Count(text, "Turn failed") != 1 || !strings.Contains(text, "remaining credit.") || !strings.Contains(text, failedTurnGuidance) {
				t.Fatalf("expanded card repeats the heading, loses evidence or has no next step:\n%s", text)
			}
			for _, line := range body {
				if len(line.Spans) < 3 || line.Spans[0].Text != "│ " || line.Spans[0].BgSet || line.Spans[1].Text != "▎ " || line.Spans[1].Role != ui.ColorError ||
					line.Spans[2].Role == ui.ColorError || !line.Spans[2].BgSet || line.VisibleWidth() != ctx.innerW {
					t.Fatalf("expanded line is not inside the card: %+v", line.Spans)
				}
			}
		}
	}
	attention := failed
	attention.State, attention.Summary, attention.Action = recovery.NeedsAttention, "Run needs attention", "Resolve the issue, then Continue"
	ctx := renderContext{th: theme.DefaultDark(), tokens: ui.NewTokenSet(theme.DefaultDark()), innerW: 100}
	line := recoveryHeader(conversation.ChatMessage{Recovery: &attention}, ctx)
	if line.Spans[1].Text != "⚠ " || line.Spans[1].Role != ui.ColorWarning || line.Spans[2].Role != ui.ColorWarning || line.Spans[2].BgSet {
		t.Fatalf("attention is a warning without the error band: %+v", line.Spans)
	}
}
