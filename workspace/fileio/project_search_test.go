package fileio

import (
	"context"
	"os"
	"path/filepath"
	"testing"
)

func TestSearchProjectFindsLiteralContentsAndFuzzyNames(t *testing.T) {
	root := t.TempDir()
	contentPath := filepath.Join(root, "handlers.go")
	if err := os.WriteFile(contentPath, []byte("package sample\n\nfunc HandleWorkspaceEvent() {}\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	namePath := filepath.Join(root, "workspace_registry.md")
	if err := os.WriteFile(namePath, []byte("nothing relevant\n"), 0o600); err != nil {
		t.Fatal(err)
	}

	content, err := SearchProject(context.Background(), root, "HandleWorkspaceEvent", ProjectSearchOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if len(content) == 0 || content[0].Line != 3 || content[0].NameOnly {
		t.Fatalf("expected literal content match on line 3, got %+v", content)
	}
	if content[0].Context == "" || content[0].ContextStart != 1 {
		t.Fatalf("expected preview context, got %+v", content[0])
	}

	byName, err := SearchProject(context.Background(), root, "wsreg", ProjectSearchOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if len(byName) == 0 || !byName[0].NameOnly || byName[0].RelativePath != "workspace_registry.md" {
		t.Fatalf("expected filename-only fuzzy match, got %+v", byName)
	}
}

func TestSearchProjectRegexAndCase(t *testing.T) {
	root := t.TempDir()
	path := filepath.Join(root, "main.go")
	if err := os.WriteFile(path, []byte("Alpha42\nalpha42\n"), 0o600); err != nil {
		t.Fatal(err)
	}

	matches, err := SearchProject(context.Background(), root, `Alpha\d+`, ProjectSearchOptions{Regex: true, CaseSensitive: true})
	if err != nil {
		t.Fatal(err)
	}
	if len(matches) != 1 || matches[0].Line != 1 {
		t.Fatalf("case-sensitive regex matches=%+v", matches)
	}
	if _, err := SearchProject(context.Background(), root, "[", ProjectSearchOptions{Regex: true}); err == nil {
		t.Fatal("expected invalid regex error")
	}
}

func TestSearchProjectHonorsCancellation(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	_, err := SearchProject(ctx, t.TempDir(), "query", ProjectSearchOptions{})
	if err == nil {
		t.Fatal("expected cancellation error")
	}
}

func TestSearchProjectRejectsAccidentalSubsequence(t *testing.T) {
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "main.txt"), []byte("nobody is ever expected to deliver a lovely example"), 0o600); err != nil {
		t.Fatal(err)
	}
	matches, err := SearchProject(context.Background(), root, "needle", ProjectSearchOptions{})
	if err != nil || len(matches) != 0 {
		t.Fatalf("junk query matches=%+v err=%v", matches, err)
	}
}

func TestSearchRelevanceRetainsUsefulAbbreviations(t *testing.T) {
	for _, tc := range []struct{ text, query string }{{"HandleWorkspaceEvent", "hwe"}, {"workspace_registry.md", "wsreg"}, {"tui/editor/render.go", "ter"}, {"needle here", "needle"}, {"界文件.go", "界文件"}} {
		if _, _, ok := fuzzyRuneMatch(tc.text, tc.query, false); !ok {
			t.Fatalf("useful abbreviation rejected: %q / %q", tc.text, tc.query)
		}
	}
	for _, tc := range []struct{ text, query string }{{"nobody is ever expected to deliver a lovely example", "needle"}, {"abcdefghijklmnopqrstuvwxyzz", "acegkqz"}} {
		if _, _, ok := fuzzyRuneMatch(tc.text, tc.query, false); ok {
			t.Fatalf("junk subsequence accepted: %q / %q", tc.text, tc.query)
		}
	}
}

func TestProjectSearchExactOccurrencesAndFuzzySuggestionsAreDistinct(t *testing.T) {
	root := t.TempDir()
	path := filepath.Join(root, "source.txt")
	if err := os.WriteFile(path, []byte("hwe hwe\nHandleWorkspaceEvent\nnobody is ever expected to deliver a lovely example"), 0600); err != nil {
		t.Fatal(err)
	}
	hits, err := SearchProject(t.Context(), root, "hwe", ProjectSearchOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if len(hits) != 3 || hits[0].Column != 0 || hits[1].Column != 4 || !hits[2].FuzzyOnly {
		t.Fatalf("hits=%+v", hits)
	}
	if ReplaceableMatch(hits[2]) {
		t.Fatal("discovery suggestion became replaceable")
	}
	edits, skipped, err := PlanProjectReplace(hits, "hwe", "X", ProjectSearchOptions{})
	if err != nil || skipped != 1 || len(edits) != 1 || edits[0].Count != 2 || edits[0].NewContent != "X X\nHandleWorkspaceEvent\nnobody is ever expected to deliver a lovely example" {
		t.Fatalf("edits=%+v skipped=%d err=%v", edits, skipped, err)
	}
}

func TestProjectSearchRegexZeroWidthMultilineAndWhitespace(t *testing.T) {
	root := t.TempDir()
	path := filepath.Join(root, "source.txt")
	if err := os.WriteFile(path, []byte(" foo \nfoo12\nfoo34"), 0600); err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		query, replacement, want string
		regex                    bool
		count                    int
	}{
		{" foo ", "Q", "Q\nfoo12\nfoo34", false, 1},
		{`^`, "> ", ">  foo \n> foo12\n> foo34", true, 3},
		{`foo(\d+)\nfoo(\d+)`, "$2/$1", " foo \n34/12", true, 1},
	} {
		hits, err := SearchProject(t.Context(), root, tc.query, ProjectSearchOptions{Regex: tc.regex})
		if err != nil {
			t.Fatal(err)
		}
		edits, _, err := PlanProjectReplace(hits, tc.query, tc.replacement, ProjectSearchOptions{Regex: tc.regex})
		if err != nil || len(edits) != 1 || edits[0].Count != tc.count || edits[0].NewContent != tc.want {
			t.Fatalf("%q: %+v err=%v", tc.query, edits, err)
		}
	}
}
