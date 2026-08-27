package chatprojection_test

import (
	"strings"
	"testing"

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

func TestBuildPlanViewRowsPreservesWrappedExplanationAndStep(t *testing.T) {
	raw := `{"id":"plan-123","revision":1,"objective":"A deliberately long plan objective","status":"active","explanation":"first explanation segment second explanation segment third explanation segment","steps":[{"id":"a","description":"first step segment second step segment third step segment","status":"in_progress"}]}`
	rows := chatprojection.BuildPlanViewRows(raw, 28)
	plain := ""
	for _, row := range rows {
		plain += row.PlainText() + "\n"
	}
	for _, want := range []string{"first explanation", "third explanation", "first step", "third step", "0/1"} {
		if !strings.Contains(plain, want) {
			t.Fatalf("plan view lost %q:\n%s", want, plain)
		}
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
