package workspace

import (
	"strings"
	"testing"
)

func TestReviewedDiffShowsLinesWithoutInternalEvidence(t *testing.T) {
	var text strings.Builder
	err := appendReviewedDiff("main.go", "package probe\n\nimport \"fmt\"\n\nvar value = 42\n",
		"package probe\n\nvar value = 42\n", func(value string) error {
			text.WriteString(value)
			return nil
		})
	if err != nil {
		t.Fatal(err)
	}
	got := text.String()
	for _, want := range []string{"main.go", "Line 2", "- import \"fmt\""} {
		if !strings.Contains(got, want) {
			t.Fatalf("the review hid the actual import removal %q:\n%s", want, got)
		}
	}
	for _, banned := range []string{"SHA-256", "UTF-16", "literal", "canonical", "Before bytes", "-0", "+0"} {
		if strings.Contains(got, banned) {
			t.Fatalf("review contains internal or empty metadata %q:\n%s", banned, got)
		}
	}
}

func TestReviewedDiffDoesNotHideByteChanges(t *testing.T) {
	for _, test := range []struct{ name, before, after, want string }{
		{"CRLF-to-LF", "old\r\n", "old\n", "- old\\r\n+ old\n"},
		{"mixed-endings-and-code", "one\r\ntwo\r\n", "new\n" + "two\n", "- two\\r\n"},
		{"final-newline", "old\n", "old", "(no final newline)"},
		{"escape", "old\n", "new\x1b[31m\u202e\n", "+ new\\x1b[31m\\u202e\n"},
	} {
		t.Run(test.name, func(t *testing.T) {
			var text strings.Builder
			if err := appendReviewedDiff("src/file.txt", test.before, test.after, func(value string) error {
				text.WriteString(value)
				return nil
			}); err != nil {
				t.Fatal(err)
			}
			if !strings.Contains(text.String(), test.want) || strings.ContainsRune(text.String(), '\x1b') {
				t.Fatalf("review hid or executed a source byte: %q", text.String())
			}
		})
	}
}

func TestReviewedDiffRefusesAnUnreviewableLongLine(t *testing.T) {
	err := appendReviewedDiff("source.txt", strings.Repeat("x", reviewedEditTextBytes+1), "short",
		func(string) error { return nil })
	if err == nil {
		t.Fatal("a line beyond the exact review bound became an approvable preview")
	}
}
