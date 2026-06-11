package input

import (
	"testing"

	"crude/sessiondb"
)

func TestFindRefRanges_SingleLabel(t *testing.T) {
	refs := []sessiondb.Reference{
		{ID: "r1", Kind: sessiondb.KindPastedText, Label: "[Pasted Text1]", DeletedAt: 0},
	}
	text := "Hello [Pasted Text1] world"
	ranges := FindRefRanges(text, refs)
	if len(ranges) != 1 {
		t.Fatalf("expected 1 range, got %d", len(ranges))
	}
	if ranges[0].ID != "r1" {
		t.Errorf("ID: got %q, want r1", ranges[0].ID)
	}
	if ranges[0].Start != 6 {
		t.Errorf("Start: got %d, want 6", ranges[0].Start)
	}
	// "[Pasted Text1]" is 15 chars: [Pasted Text1], starting at pos 6, ending at 6+14=20 (exclusive)
	if ranges[0].End != 20 {
		t.Errorf("End: got %d, want 20", ranges[0].End)
	}
}

func TestFindRefRanges_MultipleLabels(t *testing.T) {
	refs := []sessiondb.Reference{
		{ID: "r1", Kind: sessiondb.KindPastedText, Label: "[Pasted Text1]", DeletedAt: 0},
		{ID: "r2", Kind: sessiondb.KindPastedImage, Label: "[Pasted IMG1]", DeletedAt: 0},
		{ID: "r3", Kind: sessiondb.KindSelectedFile, Label: "[File1]", DeletedAt: 0},
	}
	text := "See [Pasted Text1] and [Pasted IMG1] also [File1]"
	ranges := FindRefRanges(text, refs)
	if len(ranges) != 3 {
		t.Fatalf("expected 3 ranges, got %d: %+v", len(ranges), ranges)
	}
	ids := make(map[string]bool)
	for _, r := range ranges {
		ids[r.ID] = true
	}
	if !ids["r1"] || !ids["r2"] || !ids["r3"] {
		t.Errorf("expected all 3 IDs, got %v", ids)
	}
	// Ranges should be sorted by Start
	for i := 1; i < len(ranges); i++ {
		if ranges[i].Start <= ranges[i-1].Start {
			t.Errorf("ranges not sorted: [%d]=%d, [%d]=%d", i-1, ranges[i-1].Start, i, ranges[i].Start)
		}
	}
}

func TestFindRefRanges_DeletedExcluded(t *testing.T) {
	refs := []sessiondb.Reference{
		{ID: "r1", Kind: sessiondb.KindPastedText, Label: "[Pasted Text1]", DeletedAt: 0},
		{ID: "r2", Kind: sessiondb.KindPastedText, Label: "[Pasted Text2]", DeletedAt: 12345}, // deleted
	}
	text := "[Pasted Text1] [Pasted Text2]"
	ranges := FindRefRanges(text, refs)
	if len(ranges) != 1 {
		t.Fatalf("expected 1 range (deleted excluded), got %d", len(ranges))
	}
	if ranges[0].ID != "r1" {
		t.Errorf("ID: got %q, want r1", ranges[0].ID)
	}
}

func TestFindRefRanges_NonMatchingBracketsIgnored(t *testing.T) {
	refs := []sessiondb.Reference{
		{ID: "r1", Kind: sessiondb.KindPastedText, Label: "[Pasted Text1]", DeletedAt: 0},
	}
	text := "Hello [some random text] and [Pasted Text1]"
	ranges := FindRefRanges(text, refs)
	if len(ranges) != 1 {
		t.Fatalf("expected 1 range, got %d", len(ranges))
	}
	if ranges[0].ID != "r1" {
		t.Errorf("ID: got %q, want r1", ranges[0].ID)
	}
}

func TestFindRefRanges_MultipleOccurrences(t *testing.T) {
	refs := []sessiondb.Reference{
		{ID: "r1", Kind: sessiondb.KindPastedText, Label: "[Pasted Text1]", DeletedAt: 0},
	}
	text := "[Pasted Text1] first [Pasted Text1] second"
	ranges := FindRefRanges(text, refs)
	if len(ranges) != 2 {
		t.Fatalf("expected 2 ranges, got %d", len(ranges))
	}
	if ranges[0].ID != "r1" || ranges[1].ID != "r1" {
		t.Errorf("both should be r1, got %v", ranges)
	}
	if ranges[0].Start == ranges[1].Start {
		t.Error("ranges should be at different positions")
	}
}

func TestFindRefRanges_EmptyText(t *testing.T) {
	refs := []sessiondb.Reference{
		{ID: "r1", Kind: sessiondb.KindPastedText, Label: "[Pasted Text1]", DeletedAt: 0},
	}
	ranges := FindRefRanges("", refs)
	if len(ranges) != 0 {
		t.Errorf("empty text should have no ranges, got %d", len(ranges))
	}
}

func TestFindRefRanges_UnclosedBracketIgnored(t *testing.T) {
	refs := []sessiondb.Reference{
		{ID: "r1", Kind: sessiondb.KindPastedText, Label: "[Pasted Text1]", DeletedAt: 0},
	}
	text := "[Pasted Text1 is here without closing bracket"
	ranges := FindRefRanges(text, refs)
	if len(ranges) != 0 {
		t.Errorf("unclosed bracket should be ignored, got %d ranges", len(ranges))
	}
}

func TestFindRefRanges_NoRefs(t *testing.T) {
	text := "Hello [something] world"
	ranges := FindRefRanges(text, nil)
	if len(ranges) != 0 {
		t.Errorf("no refs should produce no ranges, got %d", len(ranges))
	}
}

func TestFindRefRanges_AllDeleted(t *testing.T) {
	refs := []sessiondb.Reference{
		{ID: "r1", Kind: sessiondb.KindPastedText, Label: "[Pasted Text1]", DeletedAt: 999},
		{ID: "r2", Kind: sessiondb.KindPastedImage, Label: "[Pasted IMG1]", DeletedAt: 999},
	}
	text := "[Pasted Text1] [Pasted IMG1]"
	ranges := FindRefRanges(text, refs)
	if len(ranges) != 0 {
		t.Errorf("all deleted should produce no ranges, got %d", len(ranges))
	}
}

func TestFindRefRanges_LabelWithoutBrackets(t *testing.T) {
	// A ref with a label that doesn't use brackets won't be found
	// because FindRefRanges scans for [....] tokens
	refs := []sessiondb.Reference{
		{ID: "r1", Kind: sessiondb.KindPastedText, Label: "PastedText1", DeletedAt: 0},
	}
	text := "Hello PastedText1 world"
	ranges := FindRefRanges(text, refs)
	if len(ranges) != 0 {
		t.Errorf("label without brackets should not be found, got %d", len(ranges))
	}
}

func TestFindRefRanges_EmptyLabel(t *testing.T) {
	refs := []sessiondb.Reference{
		{ID: "r1", Kind: sessiondb.KindPastedText, Label: "", DeletedAt: 0},
	}
	text := "Hello world"
	ranges := FindRefRanges(text, refs)
	if len(ranges) != 0 {
		t.Errorf("empty label should produce no ranges, got %d", len(ranges))
	}
}
