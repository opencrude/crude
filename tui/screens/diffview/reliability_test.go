package diffview

import (
	"context"
	"errors"
	"strings"
	"testing"

	"crude/textsearch"
	"crude/tui/surface"
)

func settleSearch(t *testing.T, s *Screen) {
	t.Helper()
	if command := s.SearchCommand(); command != nil {
		command().(SearchReadyMsg).Apply()
	}
	t.Cleanup(s.WaitSearch)
}

func TestSearchSupersededQueryRejectsLateResults(t *testing.T) {
	s := New(nil)
	s.SetPrepared(Prepare(Comparison{After: "old target\nlatest target\n"}))
	host(s, 100, 12)
	s.SetQuery("old")
	old := s.SearchCommand()()
	s.SetQuery("latest")
	settleSearch(t, s)
	if old.(SearchReadyMsg).Apply() {
		t.Fatal("old query installed after current query")
	}
	if s.query != "latest" {
		t.Fatal("query changed")
	}
	if len(s.search.Matches) != 1 || s.search.Matches[0].Row != 1 {
		t.Fatalf("current hits=%+v", s.search.Matches)
	}
}

func TestSourceRangeKeepsGraphemesTabsAndControlBoundaries(t *testing.T) {
	input := "\t界é👩‍💻\x1bX\r\n"
	var raw string
	for unit := range SourceRange(input, 4, 11) {
		raw += unit.Raw
	}
	if raw != "界é👩‍💻\x1bX" {
		t.Fatalf("range bytes=%q", raw)
	}
	sequence := SourceRange("abc", 0, 2)
	for range 2 {
		got := ""
		for unit := range sequence {
			got += unit.Raw
		}
		if got != "ab" {
			t.Fatalf("iterator not reusable: %q", got)
		}
	}
}

func TestSourceMatchesUseOriginalUnicodeAndWholeGraphemes(t *testing.T) {
	matcher, _ := textsearch.Compile("k", textsearch.Options{})
	hits, err := sourceMatches(t.Context(), "K k K", matcher, 0)
	if err != nil {
		t.Fatal(err)
	}
	if len(hits) != 3 || hits[0].Start != 0 || hits[1].Start != 2 || hits[2].Start != 4 {
		t.Fatalf("casefold offsets=%+v", hits)
	}
	matcher, _ = textsearch.Compile("e", textsearch.Options{})
	if got, _ := sourceMatches(t.Context(), "é", matcher, 0); len(got) != 0 {
		t.Fatalf("partial grapheme matched: %+v", got)
	}
}

func TestCancelledPreparationAndFullLongLineCopy(t *testing.T) {
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	if _, err := PrepareContext(ctx, Comparison{After: "new"}); !errors.Is(err, context.Canceled) {
		t.Fatalf("cancelled error=%v", err)
	}
	s := New(nil)
	source := "visible-prefix " + strings.Repeat("x", 2_000_000) + "\r\n"
	s.SetPrepared(Prepare(Comparison{Path: "large.txt", After: source}))
	h := host(s, 100, 12)
	buf := surface.NewCellBuffer(100, 12)
	h.Paint(buf)
	if !strings.Contains(text(buf), "visible-prefix") {
		t.Fatal("long source not painted")
	}
	s.after.selected, s.after.all = true, true
	if s.after.SelectedText() != source {
		t.Fatal("viewport optimization truncated copied source")
	}
}

func TestPreparationQueueIsCancellable(t *testing.T) {
	one, err := AcquirePreparation(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	defer one()
	two, err := AcquirePreparation(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	defer two()
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	if _, err := AcquirePreparation(ctx); !errors.Is(err, context.Canceled) {
		t.Fatalf("queue error=%v", err)
	}
}

func TestSearchAdmissionDoesNotWaitForWorkerAndRejectsCancelledResult(t *testing.T) {
	one, _ := AcquirePreparation(t.Context())
	defer one()
	two, _ := AcquirePreparation(t.Context())
	defer two()
	s := New(nil)
	s.SetPrepared(Prepared{})
	s.SetQuery("queued")
	if s.SearchStatus() != "Searching comparison…" {
		t.Fatal("search was not admitted asynchronously")
	}
	command := s.SearchCommand()
	s.SetQuery("")
	if command().(SearchReadyMsg).Apply() {
		t.Fatal("cancelled search published")
	}
	s.WaitSearch()
}

func TestComparisonLineLimitFailsExplicitly(t *testing.T) {
	_, err := PrepareContext(t.Context(), Comparison{After: strings.Repeat("\n", ComparisonLineLimit+1)})
	if err == nil || !strings.Contains(err.Error(), "line per-version") {
		t.Fatalf("oversize error=%v", err)
	}
}
