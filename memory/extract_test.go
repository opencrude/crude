package memory

import (
	"context"
	"strings"
	"testing"
	"time"
)

func TestParseExtractionCleanJSON(t *testing.T) {
	raw := `{"memories":[{"kind":"convention","title":"Test gate","content":"Run go test.","importance":60,"paths":["pkg/a.go"]}],"summary":{"title":"Fixing parser","summary":"Fixed the parser bug in pkg/a.go.","outcome":"completed","key_files":["pkg/a.go"]}}`
	result, err := ParseExtraction(raw)
	if err != nil {
		t.Fatalf("ParseExtraction: %v", err)
	}
	if len(result.Memories) != 1 || result.Memories[0].Kind != KindConvention {
		t.Fatalf("memories = %+v", result.Memories)
	}
	if result.Summary == nil || result.Summary.Outcome != "completed" {
		t.Fatalf("summary = %+v", result.Summary)
	}
}

func TestParseExtractionToleratesFencesAndProse(t *testing.T) {
	raw := "Here is the result:\n```json\n" +
		`{"memories":[],"summary":{"title":"t","summary":"Explored the codebase.","outcome":"in-progress","key_files":[]}}` +
		"\n```\nHope this helps!"
	result, err := ParseExtraction(raw)
	if err != nil {
		t.Fatalf("ParseExtraction: %v", err)
	}
	if len(result.Memories) != 0 || result.Summary == nil {
		t.Fatalf("result = %+v", result)
	}
}

func TestParseExtractionRejectsGarbageAndClampsInvalid(t *testing.T) {
	if _, err := ParseExtraction("I could not produce JSON, sorry."); err == nil {
		t.Fatal("garbage should error")
	}
	// Invalid kind falls back to fact; blank entries dropped; >5 truncated;
	// out-of-range importance reset.
	raw := `{"memories":[
		{"kind":"nonsense","title":"A","content":"a","importance":900},
		{"kind":"fact","title":"","content":"missing title"},
		{"kind":"fact","title":"B","content":"b"},
		{"kind":"fact","title":"C","content":"c"},
		{"kind":"fact","title":"D","content":"d"},
		{"kind":"fact","title":"E","content":"e"},
		{"kind":"fact","title":"F","content":"f"}
	]}`
	result, err := ParseExtraction(raw)
	if err != nil {
		t.Fatalf("ParseExtraction: %v", err)
	}
	if len(result.Memories) != 5 {
		t.Fatalf("expected clamp to 5 valid memories, got %d", len(result.Memories))
	}
	if result.Memories[0].Kind != KindFact || result.Memories[0].Importance != DefaultImportance {
		t.Fatalf("invalid kind/importance not normalized: %+v", result.Memories[0])
	}
	if result.Summary != nil {
		t.Fatalf("summary should be nil, got %+v", result.Summary)
	}
}

func TestParseConsolidationOps(t *testing.T) {
	raw := "```json\n" + `[
		{"op":"merge","ids":["mem-1","mem-2"],"kind":"convention","title":"T","content":"C","importance":50,"reason":"dup"},
		{"op":"supersede","id":"mem-3","by":"mem-4","reason":"contradicted"}
	]` + "\n```"
	ops, err := ParseConsolidation(raw)
	if err != nil {
		t.Fatalf("ParseConsolidation: %v", err)
	}
	if len(ops) != 2 || ops[0].Op != "merge" || ops[1].By != "mem-4" {
		t.Fatalf("ops = %+v", ops)
	}
	if _, err := ParseConsolidation("no array here"); err == nil {
		t.Fatal("garbage should error")
	}
}

func TestApplyConsolidationValidatesAndJournals(t *testing.T) {
	st := openTestStore(t)
	ctx := context.Background()
	first, err := st.Remember(ctx, Record{Kind: KindFact, Title: "One", Content: "The build uses Go."})
	if err != nil {
		t.Fatalf("Remember: %v", err)
	}
	second, err := st.Remember(ctx, Record{Kind: KindFact, Title: "Two", Content: "Go is used for the build."})
	if err != nil {
		t.Fatalf("Remember: %v", err)
	}
	pinned, err := st.Remember(ctx, Record{Kind: KindConvention, Title: "Keep", Content: "Never touch.", Pinned: true})
	if err != nil {
		t.Fatalf("Remember pinned: %v", err)
	}

	applied, err := st.ApplyConsolidation(ctx, []ConsolidationOp{
		{Op: "merge", IDs: []string{first.ID, second.ID}, Kind: KindFact,
			Title: "Build language", Content: "The build is written in Go.", Importance: 50},
		{Op: "rewrite", ID: pinned.ID, Content: "should be rejected (pinned)"},
		{Op: "supersede", ID: "mem-missing", By: first.ID},
	}, "s9")
	if err != nil {
		t.Fatalf("ApplyConsolidation: %v", err)
	}
	if applied != 1 {
		t.Fatalf("applied = %d, want 1 (merge only)", applied)
	}
	for _, id := range []string{first.ID, second.ID} {
		got, err := st.Get(ctx, id)
		if err != nil {
			t.Fatalf("Get: %v", err)
		}
		if got.Status != StatusSuperseded || got.SupersededBy == "" {
			t.Fatalf("merge source %s = %+v", id, got)
		}
	}
	keep, err := st.Get(ctx, pinned.ID)
	if err != nil {
		t.Fatalf("Get pinned: %v", err)
	}
	if keep.Content != "Never touch." {
		t.Fatalf("pinned record was rewritten: %+v", keep)
	}
}

func TestDedupeExactSupersedesDuplicates(t *testing.T) {
	st := openTestStore(t)
	ctx := context.Background()
	if _, err := st.Remember(ctx, Record{Kind: KindFact, Title: "A", Content: "Use WAL journal mode."}); err != nil {
		t.Fatalf("Remember: %v", err)
	}
	if _, err := st.Remember(ctx, Record{Kind: KindFact, Title: "B", Content: "use wal Journal MODE"}); err != nil {
		t.Fatalf("Remember: %v", err)
	}
	superseded, err := st.DedupeExact(ctx)
	if err != nil {
		t.Fatalf("DedupeExact: %v", err)
	}
	if superseded != 1 {
		t.Fatalf("superseded = %d, want 1", superseded)
	}
	active, err := st.CountByStatus(ctx, StatusActive)
	if err != nil {
		t.Fatalf("CountByStatus: %v", err)
	}
	if active != 1 {
		t.Fatalf("active = %d, want 1", active)
	}
}

func TestEnforceCapArchivesLowestValue(t *testing.T) {
	st := openTestStore(t)
	ctx := context.Background()
	titles := []string{"One", "Two", "Three", "Four"}
	for i, title := range titles {
		if _, err := st.Remember(ctx, Record{
			Kind: KindFact, Title: title, Content: "Content " + title,
			Importance: 10 + i*20,
		}); err != nil {
			t.Fatalf("Remember %s: %v", title, err)
		}
	}
	archived, err := st.EnforceCap(ctx, 2)
	if err != nil {
		t.Fatalf("EnforceCap: %v", err)
	}
	if archived != 2 {
		t.Fatalf("archived = %d, want 2", archived)
	}
	active, err := st.CountByStatus(ctx, StatusActive)
	if err != nil {
		t.Fatalf("CountByStatus: %v", err)
	}
	if active != 2 {
		t.Fatalf("active = %d, want 2", active)
	}
}

func TestDecayArchiveOnlyTouchesUnusedAutoRecords(t *testing.T) {
	st := openTestStore(t)
	ctx := context.Background()
	old := time.Now().Add(-90 * 24 * time.Hour).UnixNano()
	stale, err := st.Remember(ctx, Record{
		Kind: KindFact, Title: "Stale", Content: "Old auto fact.",
		Source: SourceAuto, CreatedAt: old,
	})
	if err != nil {
		t.Fatalf("Remember: %v", err)
	}
	// Force timestamps into the past (Remember sets updated_at=now).
	if _, err := st.db.ExecContext(ctx,
		`UPDATE workspace_memories SET updated_at = ?, created_at = ? WHERE id = ?`,
		old, old, stale.ID); err != nil {
		t.Fatalf("age record: %v", err)
	}
	if _, err := st.Remember(ctx, Record{
		Kind: KindFact, Title: "Fresh", Content: "Recent tool fact.", Source: SourceTool,
	}); err != nil {
		t.Fatalf("Remember fresh: %v", err)
	}
	archived, err := st.DecayArchive(ctx, 45*24*time.Hour)
	if err != nil {
		t.Fatalf("DecayArchive: %v", err)
	}
	if archived != 1 {
		t.Fatalf("archived = %d, want 1", archived)
	}
	got, err := st.Get(ctx, stale.ID)
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if got.Status != StatusArchived {
		t.Fatalf("stale record status = %q", got.Status)
	}
}

func TestBuildExtractionUserAssemblesSections(t *testing.T) {
	out := BuildExtractionUser("prior summary", "- Title (fact)", "--- turn 1 ---\nUSER: hi")
	for _, want := range []string{"PREVIOUS SUMMARY:", "EXISTING MEMORIES", "NEW TURNS:", "turn 1"} {
		if !strings.Contains(out, want) {
			t.Fatalf("missing %q in:\n%s", want, out)
		}
	}
}

func TestNormalizeAndMatchQueryHelpers(t *testing.T) {
	norm := NormalizeText("Editor.moveWordLeft in tui/editor/movement.go")
	for _, want := range []string{"move", "word", "left", "movement", "editor", "movewordleft"} {
		if !strings.Contains(norm, want) {
			t.Fatalf("norm %q missing %q", norm, want)
		}
	}
	if q := BuildMatchQuery("the and of"); q != "" {
		t.Fatalf("stopword-only query should be empty, got %q", q)
	}
	q := BuildMatchQuery(`weird "quoted" term`)
	if !strings.Contains(q, `"quoted"`) || strings.Contains(q, `"""`+" OR") {
		// Every token must be individually quoted; embedded quotes doubled.
		if !strings.Contains(q, `""`) && strings.Contains(`weird "quoted" term`, `"`) {
			t.Fatalf("quotes not escaped: %q", q)
		}
	}
	entities := ExtractEntities("Fix moveWordLeft in tui/editor/movement.go and run go_test now")
	joined := strings.Join(entities, " ")
	for _, want := range []string{"moveWordLeft", "tui/editor/movement.go", "go_test"} {
		if !strings.Contains(joined, want) {
			t.Fatalf("entities %v missing %q", entities, want)
		}
	}
}
