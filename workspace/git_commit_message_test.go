package workspace

import (
	"strings"
	"testing"
)

func TestCommitMessageDiffIsBoundedAndNamesBinaryFiles(t *testing.T) {
	diff := commitMessageDiff([]GitComparison{
		{Path: "a.go", Before: "one\n", After: "two\n", BeforePresent: true, AfterPresent: true},
		{Path: "logo.png", Binary: true, BeforePresent: true, AfterPresent: true},
		{Path: "big.txt", After: strings.Repeat("x\n", 20000), AfterPresent: true},
	})
	for _, want := range []string{"-one", "+two", "Binary file logo.png changed", "New file big.txt", "diff truncated"} {
		if !strings.Contains(diff, want) {
			t.Fatalf("missing %q in:\n%.400s", want, diff)
		}
	}
	if len(diff) > commitMessageDiffLimit+commitMessageFileLimit {
		t.Fatalf("diff is %d bytes", len(diff))
	}
}

func TestCleanCommitMessageStripsModelWrapping(t *testing.T) {
	for input, want := range map[string]string{
		"```\nFix parser\n\nBody\n```": "Fix parser\n\nBody",
		"```text\nAdd docs\n```":       "Add docs",
		"\"Rename service\"\n":         "Rename service",
		"  Plain subject  ":            "Plain subject",
	} {
		if got := CleanCommitMessage(input); got != want {
			t.Fatalf("CleanCommitMessage(%q)=%q want %q", input, got, want)
		}
	}
}
