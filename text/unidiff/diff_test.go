package unidiff

import (
	"strings"
	"testing"
)

func TestGenerateDiffCountsLinesThatStartWithDoublePlusOrMinus(t *testing.T) {
	// go-udiff renders an added line whose text begins with "++" as "+++<text>",
	// which the previous prefix check mistook for the "+++" file header, so a
	// real change was reported as zero additions. The symmetric "--" case hid
	// removals. These are common line prefixes (++/-- operators, SQL comments).
	cases := []struct {
		name   string
		before string
		after  string
		add    int
		remove int
	}{
		{"add a ++i line", "", "++i\n", 1, 0},
		{"add a +++x line", "", "+++x\n", 1, 0},
		{"add two ++ lines", "", "++i\n++j\n", 2, 0},
		{"remove a --count line", "--count\n", "", 0, 1},
		{"remove two -- lines", "--a\n--b\n", "", 0, 2},
		{"replace line with ++i", "y = 0\n", "++i\n", 1, 1},
		{"replace --line with ++line", "--old\n", "++new\n", 1, 1},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			_, additions, removals := GenerateDiff(c.before, c.after, "f.go")
			if additions != c.add || removals != c.remove {
				t.Fatalf("before=%q after=%q: additions=%d removals=%d, want %d/%d",
					c.before, c.after, additions, removals, c.add, c.remove)
			}
		})
	}
}

func TestGenerateDiffCountsNormalEdits(t *testing.T) {
	// The regression fix must keep the ordinary cases working.
	cases := []struct {
		name   string
		before string
		after  string
		add    int
		remove int
	}{
		{"identical", "x\n", "x\n", 0, 0},
		{"empty to empty", "", "", 0, 0},
		{"add one line", "", "x = 1\n", 1, 0},
		{"add unterminated line", "", "hello", 1, 0},
		{"remove one line", "x\n", "", 0, 1},
		{"edit one line", "a\nb\nc\n", "a\nX\nc\n", 1, 1},
		{"replace block", "old1\nold2\n", "new1\nnew2\nnew3\n", 3, 2},
		{"add lines between context", "a\nb\nc\n", "a\nb\nb2\nc\n", 1, 0},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			_, additions, removals := GenerateDiff(c.before, c.after, "f.go")
			if additions != c.add || removals != c.remove {
				t.Fatalf("before=%q after=%q: additions=%d removals=%d, want %d/%d",
					c.before, c.after, additions, removals, c.add, c.remove)
			}
		})
	}
}

func TestIsEmptyDiffSeesDoublePlusOrMinusChanges(t *testing.T) {
	// A diff that only adds or removes lines beginning with "++"/"--" must not
	// be reported as empty.
	cases := []struct {
		name   string
		before string
		after  string
		empty  bool
	}{
		{"add ++i is a change", "", "++i\n", false},
		{"add +++x is a change", "", "+++x\n", false},
		{"remove --count is a change", "--count\n", "", false},
		{"remove ---y is a change", "---y\n", "", false},
		{"identical is empty", "x\n", "x\n", true},
		{"empty input is empty", "", "", true},
		{"normal add is a change", "", "x\n", false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			diff, _, _ := GenerateDiff(c.before, c.after, "f.go")
			// GenerateDiff emits "" for identical content, so exercise the
			// rendered-diff path with a non-empty file name and trim only the
			// leading slash like the production callers do.
			if got := IsEmptyDiff(strings.TrimSpace(diff)); got != c.empty {
				t.Fatalf("before=%q after=%q: IsEmptyDiff=%v, want %v (diff=%q)",
					c.before, c.after, got, c.empty, diff)
			}
		})
	}
}

func TestIsEmptyDiffSkipsHeadersAndCountsRenderedContent(t *testing.T) {
	// The two file headers ("--- ..." and "+++ ...") are present in every
	// non-empty diff and must never count as content lines.
	diff, additions, removals := GenerateDiff("a\nb\n", "a\nb\nc\n", "f.go")
	if additions != 1 || removals != 0 {
		t.Fatalf("setup: additions=%d removals=%d, want 1/0", additions, removals)
	}
	if IsEmptyDiff(strings.TrimSpace(diff)) {
		t.Fatalf("diff with one added line reported empty:\n%s", diff)
	}
	if !strings.Contains(diff, "--- a/f.go") || !strings.Contains(diff, "+++ b/f.go") {
		t.Fatalf("expected file headers in diff:\n%s", diff)
	}
}
