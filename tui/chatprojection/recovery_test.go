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

func TestRecoveryNoticeKeepsDiagnosticsStaticReadableAndHydrationIdentical(t *testing.T) {
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
				ctx := renderContext{th: th, tokens: ui.NewTokenSet(th), innerW: width, contentWidth: max(1, width-16), spinFrame: 1000}
				rows := renderMessageContent(live, ctx, false)
				ctx.spinFrame = 2000
				resumed := renderMessageContent(hydrated, ctx, false)
				if !reflect.DeepEqual(rows.native, resumed.native) || rows.frameSensitive || len(rows.native) < 2 {
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
	_, _ = syncer.SyncMessages([]types.ChatMessage{first}, 1)
	changed := first
	status := *first.Recovery
	status.Action = "Continue run"
	changed.Recovery = &status
	result, _ := syncer.SyncMessages([]types.ChatMessage{changed}, 2)
	if len(result.ChangedIDs) != 1 || result.ChangedIDs[0] != "incident" {
		t.Fatalf("recovery action-only delta failed to invalidate painted rows: %+v", result)
	}
}
