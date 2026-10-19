package gitdiff

import (
	"context"
	"errors"
	"runtime"
	"strings"
	"testing"

	"crude/tui/screens/diffview"
	"crude/tui/surface"
	"crude/tui/theme"
	"crude/tui/ui"
	"crude/workspace"
)

func settleDocumentSearch(t *testing.T, doc *Document) {
	t.Helper()
	if command := doc.SearchCommand(); command != nil {
		command().(diffview.SearchReadyMsg).Apply()
	}
	t.Cleanup(doc.WaitSearch)
}

func BenchmarkGitDiffLongLinePaint(b *testing.B) {
	for _, size := range []int{128, 2_000_000} {
		name := "short"
		if size > 128 {
			name = "two_megabytes"
		}
		b.Run(name, func(b *testing.B) {
			doc := New("bench", ui.NewTokenSet(theme.DefaultDark()))
			doc.SetFiles(PrepareChanges([]workspace.GitChangeComparison{comparisonItem("large.txt", "old\n", "visible-prefix "+strings.Repeat("x", size)+"\n", workspace.GitAreaWorking)}))
			doc.Layout(ui.LayoutContext{}, ui.Rect{W: 120, H: 12})
			buf := surface.NewCellBuffer(120, 12)
			doc.Paint(buf, 0, 0, ui.PaintContext{})
			b.ReportAllocs()
			b.ResetTimer()
			for range b.N {
				doc.Paint(buf, 0, 0, ui.PaintContext{})
			}
		})
	}
}

func BenchmarkGitDiffLongLineFarRightPaint(b *testing.B) {
	doc := New("far-right", ui.NewTokenSet(theme.DefaultDark()))
	doc.SetFiles(PrepareChanges([]workspace.GitChangeComparison{comparisonItem("large.txt", "old\n", strings.Repeat("x", 2_000_000)+" tail-marker\n", workspace.GitAreaWorking)}))
	doc.Layout(ui.LayoutContext{}, ui.Rect{W: 120, H: 12})
	doc.hscroll = 1_999_950
	buf := surface.NewCellBuffer(120, 12)
	doc.Paint(buf, 0, 0, ui.PaintContext{})
	b.ReportAllocs()
	b.ResetTimer()
	for range b.N {
		doc.Paint(buf, 0, 0, ui.PaintContext{})
	}
}

func TestEmptyFilesDescribePresenceNotModeChange(t *testing.T) {
	files := PrepareChanges([]workspace.GitChangeComparison{
		{Change: workspace.GitChange{Path: "new.txt", Area: workspace.GitAreaUntracked}, Comparison: workspace.GitComparison{AfterPresent: true, AfterMode: 0100644, Untracked: true}},
		{Change: workspace.GitChange{Path: "removed.txt", Area: workspace.GitAreaWorking}, Comparison: workspace.GitComparison{BeforePresent: true, BeforeMode: 0100644, Deleted: true}},
	})
	if files[0].Note != "New empty file" || files[1].Note != "Deleted empty file" {
		t.Fatalf("empty notes=%q %q", files[0].Note, files[1].Note)
	}
}

func TestPreparationCancellationDoesNotPublishPartialFiles(t *testing.T) {
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	files, err := PrepareChangesContext(ctx, []workspace.GitChangeComparison{comparisonItem("a.txt", "old", "new", workspace.GitAreaWorking)})
	if !errors.Is(err, context.Canceled) || files != nil {
		t.Fatalf("cancelled preparation=%v, %v", files, err)
	}
}

func TestLargeDocumentUsesCompactPresentationIndex(t *testing.T) {
	doc := New("large", ui.NewTokenSet(theme.DefaultDark()))
	doc.SetFiles(PrepareChanges([]workspace.GitChangeComparison{comparisonItem("many.txt", "", strings.Repeat("complete source\n", 100_000), workspace.GitAreaUntracked)}))
	if doc.rows.Len() != 100_002 || len(doc.rows.spans) > 4 {
		t.Fatalf("rows=%d spans=%d", doc.rows.Len(), len(doc.rows.spans))
	}
	last := doc.rows.At(100_001)
	if last.new != 100_000 {
		t.Fatalf("last row=%+v", last)
	}
	doc.SetPresentation(Split)
	paint(doc, 140, 12)
	if len(doc.rows.spans) > 4 || doc.rows.At(100_001).new != 100_000 {
		t.Fatal("split materialized or lost large source")
	}
	doc.SetAllCollapsed(true)
	if doc.rows.Len() != 1 {
		t.Fatal("collapse lost file header")
	}
	doc.SetAllCollapsed(false)
	if doc.rows.Len() != 100_002 {
		t.Fatal("expand lost source")
	}
}

func TestGitDiffLongLinePaintIsViewportBounded(t *testing.T) {
	doc := New("long-line", ui.NewTokenSet(theme.DefaultDark()))
	doc.SetFiles(PrepareChanges([]workspace.GitChangeComparison{comparisonItem("large.txt", "old\n", "visible-prefix "+strings.Repeat("x", 2_000_000)+"\n", workspace.GitAreaWorking)}))
	if text := paint(doc, 120, 12); !strings.Contains(text, "visible-prefix") {
		t.Fatalf("long-line source missing:\n%s", text)
	}
	buf := surface.NewCellBuffer(120, 12)
	var before, after runtime.MemStats
	runtime.ReadMemStats(&before)
	doc.Paint(buf, 0, 0, ui.PaintContext{})
	runtime.ReadMemStats(&after)
	if bytes := after.TotalAlloc - before.TotalAlloc; bytes > 1<<20 {
		t.Fatalf("one visible line allocated %d bytes; offscreen source was materialized during paint", bytes)
	}
}
