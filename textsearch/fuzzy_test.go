package textsearch

import (
	"strings"
	"testing"
)

func TestFuzzyRequiresCompleteQueryAndUsefulEvidence(t *testing.T) {
	for _, tc := range []struct {
		value, query string
		want         bool
	}{
		{"main.go", "mg", true}, {"HandleWorkspaceEvent", "hwe", true},
		{"workspace_registry.md", "wsreg", true}, {"tui/editor/render.go", "ter", true},
		{"Über.txt", "üb", true}, {"界文件.go", "界文", true},
		{"main.go", "mystery", false}, {"one n", "needle", false},
		{"nobody is ever expected to deliver a lovely example", "needle", false},
		{"abcdefghijklmnopqrstuvwxyzz", "acegkqz", false},
		{"ab", "aa", false}, {"ababab", "abab", true},
		{"foo", "foo bar", false}, {"alpha beta", "ab", false},
		{strings.Repeat("x", 40000) + "target", "target", true},
	} {
		pos, _, ok := Fuzzy(tc.value, tc.query, false)
		if ok != tc.want {
			t.Errorf("%q / %q matched=%v positions=%v", tc.value, tc.query, ok, pos)
		}
		if ok && len(pos) != len([]rune(tc.query)) {
			t.Fatal("partial query accepted")
		}
	}
}
