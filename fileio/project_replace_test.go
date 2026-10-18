package fileio

import (
	"os"
	"path/filepath"
	"testing"
)

func TestReplaceableMatchRejectsFuzzyAndNameOnly(t *testing.T) {
	if ReplaceableMatch(ProjectSearchMatch{NameOnly: true, Line: 1, Column: 0, EndColumn: 3}) {
		t.Fatal("name-only must not be replaceable")
	}
	if ReplaceableMatch(ProjectSearchMatch{Line: 1, Column: 0, EndColumn: 3, TextMatches: []int{0, 2}}) {
		t.Fatal("scattered fuzzy match must not be replaceable")
	}
	if !ReplaceableMatch(ProjectSearchMatch{Line: 1, Column: 0, EndColumn: 3, TextMatches: []int{0, 1, 2}}) {
		t.Fatal("contiguous span should be replaceable")
	}
}

func TestPlanProjectReplaceLiteralAndRegex(t *testing.T) {
	root := t.TempDir()
	path := filepath.Join(root, "main.go")
	if err := os.WriteFile(path, []byte("alpha\nalpha beta\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	matches := []ProjectSearchMatch{
		{Path: path, RelativePath: "main.go", Line: 1, Column: 0, EndColumn: 5, LineText: "alpha", TextMatches: []int{0, 1, 2, 3, 4}},
		{Path: path, RelativePath: "main.go", Line: 2, Column: 0, EndColumn: 5, LineText: "alpha beta", TextMatches: []int{0, 1, 2, 3, 4}},
	}
	edits, skipped, err := PlanProjectReplace(matches, "alpha", "omega", ProjectSearchOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if skipped != 0 || len(edits) != 1 || edits[0].Count != 2 {
		t.Fatalf("edits=%+v skipped=%d", edits, skipped)
	}
	if edits[0].NewContent != "omega\nomega beta\n" {
		t.Fatalf("new=%q", edits[0].NewContent)
	}

	rePath := filepath.Join(root, "re.go")
	if err := os.WriteFile(rePath, []byte("Alpha42\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	reMatches := []ProjectSearchMatch{
		{Path: rePath, RelativePath: "re.go", Line: 1, Column: 0, EndColumn: 7, LineText: "Alpha42", TextMatches: []int{0, 1, 2, 3, 4, 5, 6}},
	}
	edits, _, err = PlanProjectReplace(reMatches, `Alpha(\d+)`, "N$1", ProjectSearchOptions{Regex: true, CaseSensitive: true})
	if err != nil {
		t.Fatal(err)
	}
	if len(edits) != 1 || edits[0].NewContent != "N42\n" {
		t.Fatalf("regex replace=%+v", edits)
	}
}

func TestPlanProjectReplaceSkipsNameOnly(t *testing.T) {
	root := t.TempDir()
	path := filepath.Join(root, "skip.go")
	if err := os.WriteFile(path, []byte("keep\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	edits, skipped, err := PlanProjectReplace([]ProjectSearchMatch{{
		Path: path, RelativePath: "skip.go", NameOnly: true, Line: 0,
	}}, "keep", "gone", ProjectSearchOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if skipped != 1 || len(edits) != 0 {
		t.Fatalf("edits=%d skipped=%d", len(edits), skipped)
	}
}

func TestProjectReplacementRejectsWrongQueryAndDeduplicates(t *testing.T) {
	root := t.TempDir()
	path := filepath.Join(root, "source.txt")
	if err := os.WriteFile(path, []byte("alpha alpha"), 0600); err != nil {
		t.Fatal(err)
	}
	hit := ProjectSearchMatch{Path: path, RelativePath: "source.txt", Line: 1, Column: 0, EndColumn: 5, LineText: "alpha alpha"}
	if edits, _, err := PlanProjectReplace([]ProjectSearchMatch{hit}, "bravo", "X", ProjectSearchOptions{}); err == nil || len(edits) != 0 {
		t.Fatal("same-sized unrelated query allowed")
	}
	edits, _, err := PlanProjectReplace([]ProjectSearchMatch{hit, hit}, "alpha", "X", ProjectSearchOptions{})
	if err != nil || len(edits) != 1 || edits[0].Count != 1 || edits[0].NewContent != "X alpha" {
		t.Fatalf("duplicate hit=%+v %v", edits, err)
	}
	hit.LineText = "alpha omega"
	if _, _, err = PlanProjectReplace([]ProjectSearchMatch{hit}, "alpha", "X", ProjectSearchOptions{}); err == nil {
		t.Fatal("stale source accepted")
	}
}
