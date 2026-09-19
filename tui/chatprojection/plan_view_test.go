package chatprojection_test

import (
	"fmt"
	"strings"
	"testing"

	"crude/tui/cell"
	"crude/tui/chatprojection"
	"crude/tui/theme"
	"crude/tui/types"
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
	for i, r := range rows {
		if r.PreferANSI() {
			t.Fatalf("row %d PreferANSI — plan view must be native", i)
		}
	}
	header := rows[0].PlainText()
	if !strings.Contains(header, "Plan: Fix persistence") {
		t.Errorf("header = %q", header)
	}
	if !strings.Contains(header, "active") {
		t.Errorf("header missing status: %q", header)
	}
	body := rows[1].PlainText()
	if !strings.Contains(body, "found bug") {
		t.Errorf("completed step missing result: %q", body)
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

func TestBuildPlanViewRowsWrapsExplanationAndKeepsStepsOneLine(t *testing.T) {
	raw := `{"id":"plan-123","revision":1,"objective":"A deliberately long plan objective","status":"active","explanation":"first explanation segment second explanation segment third explanation segment","steps":[{"id":"a","description":"first step segment second step segment third step segment","status":"in_progress"}]}`
	rows := chatprojection.BuildPlanViewRows(raw, 28)
	plain := ""
	stepRows := 0
	for _, row := range rows {
		text := row.PlainText()
		plain += text + "\n"
		if strings.Contains(text, "→") {
			stepRows++
		}
	}
	for _, want := range []string{"first explanation", "third explanation", "first step", "…", "0/1"} {
		if !strings.Contains(plain, want) {
			t.Fatalf("plan view lost %q:\n%s", want, plain)
		}
	}
	if stepRows != 1 {
		t.Fatalf("step must stay one row, got %d:\n%s", stepRows, plain)
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
			if span.Text == "Audit flow — found bug" {
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

func TestPlanViewStepStaysOneLineDespiteNotepadText(t *testing.T) {
	raw := `{"id":"plan-123","revision":1,"objective":"Ship it","status":"active","steps":[
		{"id":"a","description":"Audit\n  the   renderer\nnow","status":"completed","result":"found 22 files\nplus notes"}
	]}`
	rows := chatprojection.BuildPlanViewRows(raw, 80)
	if len(rows) != 2 {
		t.Fatalf("card rows = %d (%+v)", len(rows), rows)
	}
	step := rows[1].PlainText()
	if strings.Contains(step, "\n") || !strings.Contains(step, "Audit the renderer now — found 22 files plus notes") {
		t.Fatalf("step row = %q", step)
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
