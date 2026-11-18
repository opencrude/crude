// Adapted for Crude from fgn/applypatch 95ed66f6d36a575b00001f2e27aedc1ec7455812.
// See NOTICE for provenance and modification details.
package codexio

import (
	"strings"
	"testing"
)

// These cases document Codex mode. The parity corpus in parity_test.go
// checks it against the real Codex binary.
func TestCodexApply(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		name, input, diff, want, err string
	}{
		{"replace", "a\nb\n", "@@\n-a\n+c\n", "c\nb\n", ""},
		{"no marker for the first section", "a\nb\n", " a\n-b\n+c", "a\nc\n", ""},
		{"first match wins", "x\nx\n", "@@\n-x\n+y", "y\nx\n", ""},
		{"trailing whitespace ignored", "a  \n", "@@\n-a\n+b", "b\n", ""},
		{"surrounding whitespace ignored", "  a\t\n", "@@\n-a\n+b", "b\n", ""},
		{"punctuation normalized", "a – “b”\n", "@@\n-a - \"b\"\n+c", "c\n", ""},
		{"fuzzy context kept as in the file", "a  \nb\n", "@@\n a\n-b\n+c", "a  \nc\n", ""},
		{"anchor then bare marker", "a\nb\n", "@@ a\n@@\n-b\n+c", "", "Unexpected line"},
		{"anchor moves the search", "b\na\nb\n", "@@ a\n-b\n+c", "b\na\nc\n", ""},
		{"missing anchor", "a\n", "@@ z\n-a\n+b", "", "Failed to find context 'z' in input"},
		{"anchored insertion goes to the end", "a\nb\n", "@@ a\n+x", "a\nb\nx\n", ""},
		{"end of file pins the section", "a\nb\na\n", "@@\n-a\n+z\n*** End of File", "a\nb\nz\n", ""},
		{"end of file only at the end", "a\nb\n", "@@\n-a\n+z\n*** End of File", "", "Failed to find expected lines"},
		{"trailing empty context stands for the final newline", "a\nb", "@@\n a\n-b\n+c\n ", "a\nc\n", ""},
		{"CRLF kept and used for new lines", "a\r\nb\r\n", "@@\n a\n-b\n+c\n+d", "a\r\nc\r\nd\r\n", ""},
		{"CR kept", "a\rb\r", "@@\n-a\n+c", "c\rb\r", ""},
		{"first ending preferred", "a\r\nb\nc", "@@\n-c\n+d", "a\r\nb\nd\r\n", ""},
		{"final newline added", "a", "@@\n-a\n+b", "b\n", ""},
		{"empty input", "", "@@\n+a", "a\n", ""},
		{"empty diff", "a\n", "", "", "Update file hunk for path 'input' is empty"},
		{"empty section", "a\n", "@@\n", "", "Update hunk does not contain any lines"},
		{"stacked anchors", "a\n", "@@ a\n@@ a\n+b", "", "Unexpected line found in update hunk: '@@ a'"},
		{"only end of file", "a\n", "*** End of File", "", "Update file hunk for path 'input' is empty"},
		{"end of file in an empty section", "a\n", "@@\n*** End of File", "", "does not contain any lines"},
		{"text after end of file", "a\n", "@@\n-a\n+b\n*** End of File\n+c", "", "Expected update hunk to start"},
		{"blank lines after end of file", "a\n", "@@\n-a\n+b\n*** End of File\n\n  ", "b\n", ""},
		{"bad line after a change", "a\n", "@@\n-a\nbad", "", "Expected update hunk to start"},
		{"bad first line", "a\n", "bad", "", "Unexpected line found in update hunk: 'bad'"},
		{"patch header", "a\n", "*** Update File: b\n-a", "", "Unexpected line"},
		{"invalid UTF-8 input", "\xff\n", "@@\n+a", "", "not contain valid UTF-8"},
		{"invalid UTF-8 diff", "a\n", "@@\n+\xff", "", "not valid UTF-8"},
		{"carriage return kept in diff lines", "a\n", "@@\n-a\r\n+b\r", "b\r\n", ""},
		{"retry after context down to one line", "\n", " \n-", "", ""},
		{"exact anchor beats an earlier lenient one", "f \nx\nf\nx\n", "@@ f\n-x\n+y", "f \nx\nf\ny\n", ""},
		{"later exact match beats earlier lenient one", "a \na\n", "@@\n-a\n+x", "a \nx\n", ""},
		{"insertions at one position keep order", "a\n", "@@\n+1\n@@\n+2", "a\n1\n2\n", ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			got, err := PreserveLineEndings.Apply(tc.input, tc.diff)
			if tc.err != "" {
				if err == nil || !strings.Contains(err.Error(), tc.err) || got != "" {
					t.Fatalf("got %q, %v; want error containing %q", got, err, tc.err)
				}
				return
			}
			if err != nil || got != tc.want {
				t.Fatalf("got %q, %v; want %q", got, err, tc.want)
			}
		})
	}
}

func TestCodexApplyBatchIsSequential(t *testing.T) {
	t.Parallel()
	got, err := PreserveLineEndings.ApplyBatch("a\n", []string{"@@\n-a\n+b", "@@\n-b\n+c"})
	if err != nil || got != "c\n" {
		t.Fatalf("got %q, %v", got, err)
	}
	_, err = PreserveLineEndings.ApplyBatch("a\n", []string{"@@\n-a\n+b", "@@\n-a\n+c"})
	if err == nil {
		t.Fatal("second diff should not see the original")
	}
}

func TestUnknownMode(t *testing.T) {
	t.Parallel()
	mode := Mode(7)
	if mode.String() != "Mode(7)" || NormalizeToLf.String() != "NormalizeToLf" || PreserveLineEndings.String() != "PreserveLineEndings" {
		t.Fatalf("names: %v %v %v", mode, NormalizeToLf, PreserveLineEndings)
	}
	_, err := mode.Apply("a", "-a\n+b")
	if err == nil {
		t.Fatal("Apply accepted an unknown mode")
	}
	_, err = mode.ApplyBatch("a", nil)
	if err == nil {
		t.Fatal("ApplyBatch accepted an unknown mode")
	}
	_, err = mode.ApplyFiles(nil, "*** Begin Patch\n*** End Patch")
	if err == nil {
		t.Fatal("ApplyFiles accepted an unknown mode")
	}
}

// upstreamCodexCases are Codex's own update tests from
// codex-rs/apply-patch/tests/suite/tool.rs, with the expected bytes Codex
// wrote. They check Codex mode here and the oracle binary in checkOracle.
// The diffs end in a newline, and want is empty when Codex must fail.
var upstreamCodexCases = []struct{ name, input, diff, want, err string }{
	{
		"preserves_crlf_from_target_file", "one\r\ntwo\r\nthree\r\n",
		"@@\n-one\n+uno\n@@\n two\n+\n+between\n three\n", "uno\r\ntwo\r\n\r\nbetween\r\nthree\r\n", "",
	},
	{"appends_after_trailing_blank_crlf_line", "a\r\n\r\n", "@@\n+new\n", "a\r\n\r\nnew\r\n", ""},
	{
		"preserves_cr_from_target_file", "one\rtwo\rthree\r",
		"@@\n-one\n+uno\n@@\n two\n+\n+between\n three\n", "uno\rtwo\r\rbetween\rthree\r", "",
	},
	{"preserves_change_order_with_repeated_lines", "a\nb\n", "@@\n-a\n-b\n+b\n+b\n+a\n", "b\nb\na\n", ""},
	{"preserves_repeated_context_line_ending", "same\r\nsame\n", "@@\n-same\n same\n", "same\n", ""},
	{
		"preserves_untouched_mixed_line_endings", "one\r\ntwo\rthree\nfour\r\n",
		"@@\n one\n two\n-three\n+THREE\n four\n", "one\r\ntwo\rTHREE\r\nfour\r\n", "",
	},
	{"uses_crlf_for_new_trailing_newline", "one\r\ntwo", "@@\n-one\n+ONE\n", "ONE\r\ntwo\r\n", ""},
	{
		"updates_file_appends_trailing_newline", "no newline at end",
		"@@\n-no newline at end\n+first line\n+second line\n", "first line\nsecond line\n", "",
	},
	{
		"rejects_overlapping_end_of_file_chunks", "one\n",
		"@@\n-one\n+first\n@@\n-one\n+second\n*** End of File\n", "", "Failed to find expected lines",
	},
}

func TestCodexUpstreamCases(t *testing.T) {
	t.Parallel()
	for _, tc := range upstreamCodexCases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			got, err := PreserveLineEndings.Apply(tc.input, tc.diff)
			if tc.err != "" {
				if err == nil || !strings.Contains(err.Error(), tc.err) {
					t.Fatalf("got %q, %v; want error containing %q", got, err, tc.err)
				}
				return
			}
			if err != nil || got != tc.want {
				t.Fatalf("got %q, %v; want %q", got, err, tc.want)
			}
		})
	}
}
