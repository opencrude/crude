package chatprojection

import (
	"encoding/json"
	"reflect"
	"strings"
	"testing"
	"time"

	"crude/tui/theme"
	"crude/tui/ui"
	"crude/types"
	"crude/types/recovery"
)

func TestExpandedRecoveryKeepsDiagnosticsReadableAndHydrationIdentical(t *testing.T) {
	for _, th := range []*theme.Theme{theme.DefaultDark(), theme.DefaultLight()} {
		for _, width := range []int{1, 12, 40, 80, 160} {
			for _, state := range []recovery.State{recovery.Recovering, recovery.NeedsAttention, recovery.Recovered, recovery.Interrupted} {
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
				live := types.ChatMessage{InternalID: status.ID, Role: types.RoleStatus, Time: status.StartedAt, Recovery: &status}
				encoded, err := json.Marshal(live)
				if err != nil {
					t.Fatal(err)
				}
				var hydrated types.ChatMessage
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
				compact := func(text string) string { return strings.Join(strings.Fields(text), "") }
				if !strings.Contains(compact(body), compact(status.Detail)) || !strings.Contains(compact(body), compact(status.Action)) {
					t.Fatalf("diagnostic or action was truncated at width %d", width)
				}
				for index, line := range rows.native {
					if line.VisibleWidth() > width {
						t.Fatalf("notice exceeded actual viewport: %+v", line)
					}
					if index > 0 {
						for _, span := range line.Spans {
							if span.Role != ui.ColorFg && span.Role != ui.ColorMuted {
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
	renderer.SyncMessages([]types.ChatMessage{{InternalID: status.ID, Role: types.RoleStatus, Recovery: &status}}, 1, 0)
	renderer.ToggleDisclosure(status.ID)
	renderer.SetSpinFrame(800)
	before := renderer.ExportMessageRowsSnapshot(100)
	if !strings.Contains(before.Rows[0].Lines[1], "Recovering") {
		t.Fatal("fixture did not begin with a recovering cooldown")
	}
	resumed := status
	resumed.RetryAt = time.Time{}
	renderer.SyncMessages([]types.ChatMessage{{InternalID: status.ID, Role: types.RoleStatus, Recovery: &resumed}}, 2, 0)
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
	data, err := json.Marshal(types.ChatMessage{InternalID: resumed.ID, Role: types.RoleStatus, Recovery: &resumed})
	if err != nil {
		t.Fatal(err)
	}
	var hydrated types.ChatMessage
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
	renderer.SyncMessages([]types.ChatMessage{{InternalID: status.ID, Role: types.RoleStatus, Recovery: &status}}, 1, 0)
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
	for _, phase := range []types.AgentPhase{types.AgentNeedsAttention, types.AgentInterrupted} {
		var strip ActivityStrip
		strip.Update(types.AgentActivityView{
			Active: true, Status: types.AgentStatusSnapshot{Phase: phase, NeedsAction: true},
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
	first := types.ChatMessage{InternalID: "incident", Role: types.RoleStatus, Recovery: &recovery.Status{ID: "incident", Summary: "old"}}
	_, _ = syncer.SyncMessages([]types.ChatMessage{first}, 1, 0)
	changed := first
	status := *first.Recovery
	status.Action = "Continue run"
	changed.Recovery = &status
	result, _ := syncer.SyncMessages([]types.ChatMessage{changed}, 2, 0)
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
	renderer.SyncMessages([]types.ChatMessage{{InternalID: status.ID, Role: types.RoleStatus, Recovery: &status}}, 1, 0)
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
