package chatprojection_test

import (
	"fmt"
	"strings"
	"testing"

	"crude/tui/cell"
	"crude/tui/chatprojection"
	"crude/tui/theme"
	"crude/types"
)

func TestBuildPlanViewRows_RendersStepCard(t *testing.T) {
	raw := `{"id":"plan-123","revision":4,"objective":"Fix persistence","status":"active","steps":[
		{"id":"audit","description":"Audit flow","status":"completed","result":"found bug"},
		{"id":"verify","description":"Add tests","status":"in_progress","depends_on":["audit"]},
		{"id":"migration","description":"Migrate","status":"skipped","reason":"not needed"}
	]}`
	rows := chatprojection.BuildPlanViewRows(raw, 80)
	if len(rows) < 4 {
		t.Fatalf("expected >=4 rows, got %d", len(rows))
	}
	header := rows[0].PlainText()
	if !strings.Contains(header, "Plan: Fix persistence") {
		t.Errorf("header = %q", header)
	}
	if !strings.Contains(header, "active") {
		t.Errorf("header missing status: %q", header)
	}
	body := rows[1].PlainText()
	if strings.Contains(body, "found bug") || !strings.Contains(body, "Audit flow") {
		t.Errorf("completed step rendered stale result narration: %q", body)
	}
}

func TestBuildPlanViewRows_RejectsNonPlanJSON(t *testing.T) {
	if rows := chatprojection.BuildPlanViewRows("just plain box text", 80); rows != nil {
		t.Fatalf("expected nil for non-JSON, got %d rows", len(rows))
	}
}

func TestBuildPlanViewRows_AcceptsHistoryEnvelope(t *testing.T) {
	raw := `{"plan":{"id":"plan-123","revision":2,"objective":"Fix persistence","status":"active","steps":[{"id":"a","description":"Audit","status":"pending"}]},"events":[]}`
	rows := chatprojection.BuildPlanViewRows(raw, 80)
	if len(rows) < 2 || !strings.Contains(rows[0].PlainText(), "Fix persistence") {
		t.Fatalf("history envelope rows = %+v", rows)
	}
}

func TestPlanProjectionCoalescesSamePlanAcrossTools(t *testing.T) {
	r := chatprojection.NewRenderer(theme.DefaultDark())
	r.SetSize(100, 30)
	r.SetChatAreaBounds(0, 20)
	first := `{"id":"plan-123","revision":1,"objective":"Fix persistence","status":"active","steps":[{"id":"a","description":"Old step body","status":"pending"}]}`
	latest := `{"id":"plan-123","revision":2,"objective":"Fix persistence","status":"active","steps":[{"id":"a","description":"Latest step body","status":"in_progress"}]}`
	r.AppendMessage(types.ChatMessage{InternalID: "p1", Role: types.RoleTool, ToolName: "update_plan", ToolStatus: "success", ToolPreviewPhase: "done", Content: first})
	r.AppendMessage(types.ChatMessage{InternalID: "p2", Role: types.RoleTool, ToolName: "get_plan", ToolStatus: "success", ToolPreviewPhase: "done", Content: latest})
	rows, _, _ := r.ExportMessageRows(100)
	if len(rows) != 2 {
		t.Fatalf("rows = %+v", rows)
	}
	if strings.Contains(strings.Join(rows[0].Lines, "\n"), "Old step body") {
		t.Fatalf("superseded plan body remained visible: %#v", rows[0].Lines)
	}
	if !strings.Contains(strings.Join(rows[1].Lines, "\n"), "Latest step body") {
		t.Fatalf("latest plan body missing: %#v", rows[1].Lines)
	}
}

func TestPlanViewExpandedWrapsStepTextWholeWhileCompactElides(t *testing.T) {
	raw := `{"id":"plan-123","revision":1,"objective":"A deliberately long plan objective","status":"active","explanation":"first explanation segment second explanation segment third explanation segment","steps":[{"id":"a","description":"first step segment second step segment third step segment","status":"in_progress"}]}`
	rows := chatprojection.BuildPlanViewRows(raw, 28)
	plain := plainRows(rows)
	for _, row := range rows {
		if row.VisibleWidth() > 28 {
			t.Fatalf("expanded row overflows: %q", row.PlainText())
		}
	}
	for _, want := range []string{"first explanation", "third explanation", "first step", "second step", "third step", "0/1"} {
		if !strings.Contains(plain, want) {
			t.Fatalf("expanded plan view lost %q:\n%s", want, plain)
		}
	}
	if strings.Contains(plain, "…") {
		t.Fatalf("expanded plan view truncated text:\n%s", plain)
	}
	if strings.Count(plain, "→") != 1 {
		t.Fatalf("step marker must lead exactly one row:\n%s", plain)
	}
	// Wrapped continuations hang under the step text, not under the marker.
	if !strings.Contains(plain, "│     second step segment") {
		t.Fatalf("continuation row not indent-aligned:\n%s", plain)
	}

	compact := plainRows(chatprojection.BuildPlanViewRowsCollapsed(raw, 28))
	if strings.Count(compact, "→") != 1 {
		t.Fatalf("compact card must keep one row per step:\n%s", compact)
	}
	if !strings.Contains(compact, "…") {
		t.Fatalf("compact card must elide the long step:\n%s", compact)
	}
}

func TestPlanViewUsesSingleHierarchyMarker(t *testing.T) {
	raw := `{"id":"plan-123","revision":1,"objective":"Search sessions","status":"active","steps":[{"id":"a","description":"Implement","status":"completed"}]}`
	rows := chatprojection.BuildPlanViewRows(raw, 80)
	if len(rows) == 0 {
		t.Fatal("missing plan rows")
	}
	header := rows[0].PlainText()
	if strings.Count(header, "●") != 0 || !strings.Contains(header, "Plan: Search sessions") {
		t.Fatalf("plan header hierarchy = %q", header)
	}
}

func TestPlanViewStrikesThroughCompletedSteps(t *testing.T) {
	raw := `{"id":"plan-123","revision":1,"objective":"Ship it","status":"active","steps":[
		{"id":"a","description":"Audit flow","status":"completed","result":"found bug"},
		{"id":"b","description":"Fix bug","status":"in_progress"}
	]}`
	rows := chatprojection.BuildPlanViewRows(raw, 80)
	striked := false
	for _, row := range rows {
		text := row.PlainText()
		for _, span := range row.Spans {
			if span.Text == "Audit flow" {
				if span.Attr&cell.AttrStrikethrough == 0 {
					t.Fatalf("completed step not struck through: %+v", span)
				}
				striked = true
			}
		}
		if strings.Contains(text, "Fix bug") {
			for _, span := range row.Spans {
				if span.Text == "Fix bug" && span.Attr&cell.AttrStrikethrough != 0 {
					t.Fatalf("in-progress step struck through: %+v", span)
				}
			}
		}
	}
	if !striked {
		t.Fatal("no span carried the completed step text")
	}
}

func TestBuildPlanViewRowsCollapsedWindowsAroundLiveStep(t *testing.T) {
	raw := `{"id":"plan-123","revision":1,"objective":"Ship it","status":"active","steps":[
		{"id":"one","description":"Done long ago","status":"completed"},
		{"id":"two","description":"Older context","status":"completed"},
		{"id":"three","description":"Previous step","status":"completed"},
		{"id":"four","description":"Live step now","status":"in_progress"},
		{"id":"five","description":"Coming up next","status":"pending"},
		{"id":"six","description":"Planned later","status":"pending"},
		{"id":"seven","description":"Far future work","status":"pending"},
		{"id":"eight","description":"Distant horizon","status":"pending"}
	]}`
	collapsed := chatprojection.BuildPlanViewRowsCollapsed(raw, 80)
	plain := make([]string, len(collapsed))
	for i, row := range collapsed {
		plain[i] = row.PlainText()
	}
	joined := strings.Join(plain, "\n")
	for _, want := range []string{
		"Plan: Ship it", "Older context", "Previous step", "Live step now",
		"Coming up next", "Planned later", "1 earlier step", "2 remaining steps",
	} {
		if !strings.Contains(joined, want) {
			t.Fatalf("collapsed card missing %q:\n%s", want, joined)
		}
	}
	for _, hidden := range []string{"Done long ago", "Far future work", "Distant horizon"} {
		if strings.Contains(joined, hidden) {
			t.Fatalf("collapsed card leaked %q:\n%s", hidden, joined)
		}
	}

	full := chatprojection.BuildPlanViewRows(raw, 80)
	var fullJoined strings.Builder
	for _, row := range full {
		fullJoined.WriteString(row.PlainText())
		fullJoined.WriteString("\n")
	}
	for _, want := range []string{"Done long ago", "Far future work"} {
		if !strings.Contains(fullJoined.String(), want) {
			t.Fatalf("expanded card lost %q:\n%s", want, fullJoined.String())
		}
	}
}

func TestPlanWindowShowsFiveStepsAtEveryPosition(t *testing.T) {
	step := func(index, live int) string {
		status := "pending"
		if index == live {
			status = "in_progress"
		}
		return fmt.Sprintf(`{"id":"s%d","description":"Step %d","status":"%s"}`, index, index, status)
	}
	cases := []struct {
		live      int
		wantShown int
		gotHidden []int
	}{
		{live: 0, wantShown: 5, gotHidden: []int{5, 6, 7}},
		{live: 3, wantShown: 5, gotHidden: []int{0, 6, 7}},
		{live: 7, wantShown: 5, gotHidden: []int{0, 1, 2}},
	}
	for _, c := range cases {
		parts := make([]string, 8)
		for i := range parts {
			parts[i] = step(i, c.live)
		}
		raw := `{"id":"plan-123","revision":1,"objective":"Window","status":"active","steps":[` + strings.Join(parts, ",") + `]}`
		rows := chatprojection.BuildPlanViewRowsCollapsed(raw, 80)
		shown := 0
		for _, row := range rows {
			if strings.Contains(row.PlainText(), "Step ") {
				shown++
			}
		}
		if shown != c.wantShown {
			t.Fatalf("live step %d showed %d steps, want %d", c.live, shown, c.wantShown)
		}
		joined := plainRows(rows)
		for _, hidden := range c.gotHidden {
			if strings.Contains(joined, fmt.Sprintf("Step %d", hidden)) {
				t.Fatalf("live step %d leaked hidden Step %d:\n%s", c.live, hidden, joined)
			}
		}
	}
}

func plainRows(rows []chatprojection.PaintLine) string {
	var joined strings.Builder
	for _, row := range rows {
		joined.WriteString(row.PlainText())
		joined.WriteString("\n")
	}
	return joined.String()
}

func TestBuildPlanViewHeaderShowsObjectiveInFull(t *testing.T) {
	objective := "Remove the stale objective length limit and render every plan step as one compact banner line"
	raw := fmt.Sprintf(`{"id":"plan-123","revision":1,"objective":%q,"status":"active","steps":[{"id":"a","description":"Audit","status":"pending"}]}`, objective)
	for _, width := range []int{40, 80} {
		rows := chatprojection.BuildPlanViewRows(raw, width)
		var joined strings.Builder
		for _, row := range rows {
			if row.VisibleWidth() > width {
				t.Fatalf("row overflows width %d: %q", width, row.PlainText())
			}
			joined.WriteString(row.PlainText())
			joined.WriteString("\n")
		}
		compact := strings.Join(strings.Fields(strings.ReplaceAll(joined.String(), "│", " ")), " ")
		want := "Plan: " + objective + " (active · 0/1)"
		if !strings.Contains(compact, want) {
			t.Fatalf("width %d header lost the objective:\n%s", width, joined.String())
		}
		if strings.Contains(joined.String(), "…") {
			t.Fatalf("width %d header was truncated:\n%s", width, joined.String())
		}
	}
}

func TestPlanViewFlattensNotepadTextInBothForms(t *testing.T) {
	raw := `{"id":"plan-123","revision":1,"objective":"Ship it","status":"active","steps":[
		{"id":"a","description":"Audit\n  the   renderer\nnow","status":"completed","result":"found 22 files\nplus notes"}
	]}`
	want := "Audit the renderer now"
	for _, form := range []struct {
		name string
		rows []chatprojection.PaintLine
	}{
		{name: "compact", rows: chatprojection.BuildPlanViewRowsCollapsed(raw, 80)},
		{name: "expanded", rows: chatprojection.BuildPlanViewRows(raw, 80)},
	} {
		if len(form.rows) != 2 {
			t.Fatalf("%s card rows = %d (%+v)", form.name, len(form.rows), form.rows)
		}
		step := form.rows[1].PlainText()
		if strings.Contains(step, "\n") || !strings.Contains(step, want) || strings.Contains(step, "found 22 files") {
			t.Fatalf("%s step row = %q", form.name, step)
		}
	}
}

// Expanded is the reading view: a long description and its reason wrap across
// rows in full — the ellipsis belongs to the compact banner alone. This is the
// contract a long step must not lose text behind.
func TestPlanViewExpandedRevealsLongStepAndReasonWhole(t *testing.T) {
	description := "Investigate the projection path and fix the truncation the user reported"
	reason := "registry unreachable from the sandbox network"
	raw := fmt.Sprintf(`{"id":"plan-123","revision":1,"objective":"Ship it","status":"active","steps":[{"id":"a","description":%q,"status":"failed","reason":%q}]}`, description, reason)
	rows := chatprojection.BuildPlanViewRows(raw, 40)
	var sb strings.Builder
	markerRows := 0
	for _, row := range rows {
		if row.VisibleWidth() > 40 {
			t.Fatalf("expanded row overflows width 40: %q", row.PlainText())
		}
		text := row.PlainText()
		if strings.Contains(text, "✗") {
			markerRows++
		}
		line := strings.TrimPrefix(text, "│   ")
		line = strings.TrimPrefix(line, "✗ ")
		line = strings.TrimPrefix(line, "  ")
		sb.WriteString(line)
		sb.WriteString(" ")
	}
	flat := strings.Join(strings.Fields(sb.String()), " ")
	if !strings.Contains(flat, description) {
		t.Fatalf("expanded card lost the step description:\n%s", flat)
	}
	if !strings.Contains(flat, "("+reason+")") {
		t.Fatalf("expanded card lost the step reason:\n%s", flat)
	}
	if strings.Contains(flat, "…") {
		t.Fatalf("expanded card truncated text:\n%s", flat)
	}
	if markerRows != 1 {
		t.Fatalf("step marker rows = %d, want 1", markerRows)
	}

	compact := plainRows(chatprojection.BuildPlanViewRowsCollapsed(raw, 40))
	if !strings.Contains(compact, "…") {
		t.Fatalf("compact card must elide the long step:\n%s", compact)
	}
	if strings.Contains(compact, reason) {
		t.Fatalf("compact card unexpectedly fits the whole reason:\n%s", compact)
	}
}

func TestPlanViewRendersDistinctLifecycleMarkersInCompactAndExpandedForms(t *testing.T) {
	raw := `{"id":"plan-123","revision":2,"objective":"Lifecycle","status":"cancelled","steps":[
		{"id":"done","description":"Completed work","status":"completed"},
		{"id":"skip","description":"Skipped work","status":"skipped","reason":"not needed"},
		{"id":"cancel","description":"Cancelled work","status":"cancelled","reason":"deferred"},
		{"id":"fail","description":"Failed work","status":"failed","reason":"boom"},
		{"id":"block","description":"Blocked work","status":"blocked","reason":"waiting"},
		{"id":"live","description":"Live work","status":"in_progress"},
		{"id":"todo","description":"Pending work","status":"pending"}
	]}`
	for _, form := range []struct {
		name string
		rows []chatprojection.PaintLine
	}{
		{name: "compact", rows: chatprojection.BuildPlanViewRowsCollapsed(raw, 120)},
		{name: "expanded", rows: chatprojection.BuildPlanViewRows(raw, 120)},
	} {
		plain := plainRows(form.rows)
		for _, want := range []string{
			"✓ Completed work", "− Skipped work (not needed)", "× Cancelled work (deferred)",
			"✗ Failed work (boom)", "! Blocked work (waiting)", "× Live work", "× Pending work",
			"(cancelled · 1/7)",
		} {
			if !strings.Contains(plain, want) {
				t.Fatalf("%s lifecycle card missing %q:\n%s", form.name, want, plain)
			}
		}
		for _, row := range form.rows {
			text := row.PlainText()
			if strings.Contains(text, "Completed work") || strings.Contains(text, "Skipped work") || strings.Contains(text, "Cancelled work") {
				struck := false
				for _, span := range row.Spans {
					if span.Attr&cell.AttrStrikethrough != 0 {
						struck = true
					}
				}
				if !struck {
					t.Fatalf("%s settled row is not struck through: %q", form.name, text)
				}
			}
		}
	}
}

func TestPlanHydrationEnvelopePreservesCancelledLifecycle(t *testing.T) {
	raw := `{"plan":{"id":"plan-123","revision":3,"objective":"Hydrate","status":"cancelled","explanation":"deferred","steps":[
		{"id":"a","description":"Already done","status":"completed"},
		{"id":"b","description":"Stopped work","status":"cancelled","reason":"deferred"}
	]},"events":[]}`
	for _, rows := range [][]chatprojection.PaintLine{
		chatprojection.BuildPlanViewRowsCollapsed(raw, 80),
		chatprojection.BuildPlanViewRows(raw, 80),
	} {
		plain := plainRows(rows)
		for _, want := range []string{"Hydrate", "cancelled", "deferred", "✓ Already done", "× Stopped work (deferred)"} {
			if !strings.Contains(plain, want) {
				t.Fatalf("hydrated plan missing %q:\n%s", want, plain)
			}
		}
	}
}

func TestCancelledPlanProjectionRepairsOlderSnapshotsWithLiveSteps(t *testing.T) {
	raw := `{"id":"plan-123","revision":2,"objective":"Legacy cancellation","status":"cancelled","explanation":"deferred","steps":[
		{"id":"done","description":"Done","status":"completed"},
		{"id":"live","description":"Live","status":"in_progress"},
		{"id":"todo","description":"Todo","status":"pending"},
		{"id":"failed","description":"Failed","status":"failed","reason":"boom"}
	]}`
	for _, rows := range [][]chatprojection.PaintLine{
		chatprojection.BuildPlanViewRowsCollapsed(raw, 80),
		chatprojection.BuildPlanViewRows(raw, 80),
	} {
		plain := plainRows(rows)
		for _, want := range []string{"✓ Done", "× Live (deferred)", "× Todo (deferred)", "✗ Failed (boom)"} {
			if !strings.Contains(plain, want) {
				t.Fatalf("cancelled projection missing %q:\n%s", want, plain)
			}
		}
		if strings.Contains(plain, "→ Live") || strings.Contains(plain, "☐ Todo") {
			t.Fatalf("cancelled plan leaked live markers:\n%s", plain)
		}
	}
}

func TestPlanRendererLiveAndHydratedMessagesMatch(t *testing.T) {
	content := `{"id":"plan-123","revision":3,"objective":"Parity","status":"cancelled","explanation":"deferred","steps":[
		{"id":"a","description":"Done","status":"completed"},
		{"id":"b","description":"Stopped","status":"cancelled","reason":"deferred"}
	]}`
	message := types.ChatMessage{InternalID: "plan", Role: types.RoleTool, ToolName: "update_plan", ToolStatus: "success", ToolPreviewPhase: "done", Content: content}
	live := chatprojection.NewRenderer(theme.DefaultDark())
	live.SetSize(100, 30)
	live.SetChatAreaBounds(0, 20)
	live.AppendMessage(message)
	liveRows, _, _ := live.ExportMessageRows(100)

	hydrated := chatprojection.NewRenderer(theme.DefaultDark())
	hydrated.SetSize(100, 30)
	hydrated.SetChatAreaBounds(0, 20)
	hydrated.SyncMessages([]types.ChatMessage{message}, 1)
	hydratedRows, _, _ := hydrated.ExportMessageRows(100)

	if fmt.Sprint(liveRows) != fmt.Sprint(hydratedRows) {
		t.Fatalf("live/hydrated plan mismatch:\nlive=%+v\nhydrated=%+v", liveRows, hydratedRows)
	}
	if joined := strings.Join(hydratedRows[0].Lines, "\n"); !strings.Contains(joined, "× Stopped") {
		t.Fatalf("hydrated renderer lost cancelled marker: %s", joined)
	}
}

func TestBuildPlanViewRowsCollapsedShowsAllWhenPlanFinished(t *testing.T) {
	raw := `{"id":"plan-123","revision":1,"objective":"Ship it","status":"completed","steps":[
		{"id":"one","description":"First done","status":"completed"},
		{"id":"two","description":"Second done","status":"completed"},
		{"id":"three","description":"Third done","status":"completed"}
	]}`
	rows := chatprojection.BuildPlanViewRowsCollapsed(raw, 80)
	var joined strings.Builder
	for _, row := range rows {
		joined.WriteString(row.PlainText())
		joined.WriteString("\n")
	}
	for _, want := range []string{"First done", "Second done", "Third done"} {
		if !strings.Contains(joined.String(), want) {
			t.Fatalf("finished plan should render whole list, missing %q:\n%s", want, joined.String())
		}
	}
}
