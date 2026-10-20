package rendering

import "testing"

func TestCommandOutputConsumesTerminalSequencesWithoutLosingEvidence(t *testing.T) {
	raw := "\x1b[31mFAIL\x1b[0m\r\n\tindented\rprogress 50%\rprogress 100%\n\x1b]8;;https://example.com\x1b\\link\x1b]8;;\x1b\\\n\x1b]52;c;c2VjcmV0\aafter\x1b[2K\n"
	want := "FAIL\n\tindented\nprogress 50%\nprogress 100%\nlink\nafter\n"
	if got := CommandOutputText(raw); got != want {
		t.Fatalf("terminal audit text=%q, want %q", got, want)
	}
	if got := CommandOutputText("{\"output\":\"a\\nb\"}\n    literal"); got != "{\"output\":\"a\\nb\"}\n    literal" {
		t.Fatalf("ordinary command text was interpreted: %q", got)
	}
}
