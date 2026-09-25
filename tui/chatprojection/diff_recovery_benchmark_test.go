package chatprojection

import (
	"crude/tui/ui"
	"fmt"
	"strings"
	"testing"
)

func TestRepeatedDiffSyntaxKeepsIndependentBands(t *testing.T) {
	raw := "--- a/main.go\n+++ b/main.go\n@@ -1,1 +1,2 @@\n-var number = 1\n+var number = 1\n+var number = 1\n"
	rows := BuildBandedDiffRows(raw, 80, 90, true)
	if len(rows) != 3 {
		t.Fatalf("changed source rows omitted: %d", len(rows))
	}
	for i, row := range rows {
		want := ui.ColorDiffAdditionBg
		if i == 0 {
			want = ui.ColorDiffDeletionBg
		}
		for _, span := range row.Spans[1:] {
			if !span.BgSet || span.BgRole != want {
				t.Fatalf("cached syntax lost independent band on row %d: %+v", i, span)
			}
		}
	}
	before := rows[2].PlainText()
	rows[1].Spans[4].Text = "changed"
	if rows[2].PlainText() != before {
		t.Fatal("mutable paint rows alias cached source spans")
	}
}

func BenchmarkLargeOpenDiff(b *testing.B) {
	for _, unique := range []bool{false, true} {
		b.Run(fmt.Sprint("unique=", unique), func(b *testing.B) {
			var raw strings.Builder
			raw.WriteString("--- a/main.go\n+++ b/main.go\n@@ -0,0 +1,3000 @@\n")
			for i := range 3000 {
				n := 0
				if unique {
					n = i
				}
				fmt.Fprintf(&raw, "+var number = %d // wrapped source code\n", n)
			}
			text := raw.String()
			b.ReportAllocs()
			b.ResetTimer()
			for i := 0; i < b.N; i++ {
				rows := BuildBandedDiffRows(text, 100, 110, true)
				if len(rows) != 3000 {
					b.Fatalf("expanded content omitted: %d rows", len(rows))
				}
			}
		})
	}
}
