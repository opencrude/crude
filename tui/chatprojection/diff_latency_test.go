package chatprojection_test

import (
	"fmt"
	"reflect"
	"strings"
	"testing"

	"crude/tui/chatprojection"
)

func TestDiffPreviewMatchesExpandedPrefix(t *testing.T) {
	for _, count := range []int{23, 24, 25, 300} {
		t.Run(fmt.Sprint(count), func(t *testing.T) {
			raw := "--- a/main.go\n+++ b/main.go\n@@ -0,0 +1,300 @@\n" + strings.Repeat("+var number = 123456789 // wrapped source code\n", count)
			full := chatprojection.BuildBandedDiffRows(raw, 24, 30, true)
			preview := chatprojection.BuildBandedDiffRows(raw, 24, 30, false)
			if len(full) <= 24 {
				if !reflect.DeepEqual(full, preview) {
					t.Fatal("untruncated preview differs from expanded diff")
				}
				return
			}
			if len(preview) != 24 || !reflect.DeepEqual(preview[:23], full[:23]) {
				t.Fatal("preview lost source text, syntax colors, or diff bands")
			}
			want := fmt.Sprintf("+%d more changes", len(full)-23)
			if !strings.Contains(preview[23].PlainText(), want) {
				t.Fatalf("footer = %q, want %q", preview[23].PlainText(), want)
			}
		})
	}
}

func BenchmarkLargeDiffPreview(b *testing.B) {
	raw := "--- a/main.go\n+++ b/main.go\n@@ -0,0 +1,3000 @@\n" + strings.Repeat("+var number = 123456789 // wrapped source code\n", 3000)
	b.ReportAllocs()
	for b.Loop() {
		chatprojection.BuildBandedDiffRows(raw, 60, 80, false)
	}
}
