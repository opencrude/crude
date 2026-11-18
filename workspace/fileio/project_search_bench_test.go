package fileio

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// Exercise the real walker, decoder, exact matcher, fuzzy discovery and preview
// extraction. A rare exact hit is in the last file, after suggestion capacity.
func BenchmarkSearchProjectDense(b *testing.B) {
	root := b.TempDir()
	body := strings.Repeat("const inputDirection = isRTL ? rtlValue : ltrValue;\nApply direction at each screen root; descendants use logical start/end layout.\n", 64)
	for i := range 512 {
		path := filepath.Join(root, fmt.Sprintf("part%02d", i/32), fmt.Sprintf("source%03d.txt", i))
		if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
			b.Fatal(err)
		}
		text := body
		if i == 511 {
			text += "direction is exact\nrareNeedle state\n"
		}
		if err := os.WriteFile(path, []byte(text), 0o600); err != nil {
			b.Fatal(err)
		}
	}
	for _, tc := range []struct {
		name, query string
		want        int
	}{
		{"absent", "missingIdentifier", 0},
		{"rare", "rareNeedle state", 1},
		{"suggestions", "direction is", maxProjectSearchResults},
	} {
		b.Run(tc.name, func(b *testing.B) {
			b.ReportAllocs()
			for b.Loop() {
				results, err := SearchProject(b.Context(), root, tc.query, ProjectSearchOptions{})
				if err != nil || len(results) != tc.want {
					b.Fatalf("results=%d, want %d: %v", len(results), tc.want, err)
				}
				if tc.want > 0 && (results[0].FuzzyOnly || results[0].NameOnly || results[0].RelativePath != "part15/source511.txt") {
					b.Fatalf("late exact result did not lead: %+v", results[0])
				}
			}
		})
	}
}

// Opt-in evidence for a real checkout, distinct from reproducible fixture gates.
func BenchmarkSearchProjectRepository(b *testing.B) {
	root := os.Getenv("CRUDE_SEARCH_BENCH_ROOT")
	if root == "" {
		b.Skip("set CRUDE_SEARCH_BENCH_ROOT to measure a real checkout")
	}
	for _, query := range []string{"direction is", "ProjectSearch", "noSuchStateMachine"} {
		b.Run(query, func(b *testing.B) {
			b.ReportAllocs()
			for b.Loop() {
				if _, err := SearchProject(b.Context(), root, query, ProjectSearchOptions{}); err != nil {
					b.Fatal(err)
				}
			}
		})
	}
}
