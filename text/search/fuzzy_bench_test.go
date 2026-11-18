package textsearch

import "testing"

func BenchmarkFuzzyProjectLines(b *testing.B) {
	for _, tc := range []struct {
		name, value, query string
		want               bool
	}{
		{"absent", "const inputDirection = isRTL ? rtlValue : ltrValue;", "missingIdentifier", false},
		{"phrase", "const inputDirection = isRTL ? rtlValue : ltrValue;", "direction is", true},
		{"unrelated", "Apply direction at each screen root; descendants use logical start/end layout.", "direction is", false},
	} {
		b.Run(tc.name, func(b *testing.B) {
			b.ReportAllocs()
			for b.Loop() {
				if _, _, ok := Fuzzy(tc.value, tc.query, false); ok != tc.want {
					b.Fatalf("match=%v, want %v", ok, tc.want)
				}
			}
		})
	}
}
