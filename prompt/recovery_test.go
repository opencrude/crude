package prompt

import (
	"strings"
	"testing"
)

func TestRecoveryContinuationsAreCanonicalAndReplaySafe(t *testing.T) {
	tests := []struct {
		kind RecoveryKind
		want string
	}{
		{RecoveryTransport, "Do not repeat accepted output or completed tool effects"},
		{RecoveryPartialResponse, "Continue from the exact interruption point"},
		{RecoveryPartialTool, "No incomplete tool call from that response was executed"},
		{RecoveryOutputTruncated, "Continue exactly where it stopped"},
		{RecoveryRepeatedTools, "choose a materially different next step"},
		{RecoveryMissingReport, "Publish exactly one final report"},
		{RecoveryInterrupted, "restored passively after interruption"},
		{RecoveryUncertain, "Never replay an effectful operation"},
	}
	for _, test := range tests {
		t.Run(string(test.kind), func(t *testing.T) {
			got := RecoveryContinuation(test.kind)
			if !strings.Contains(got, `kind="`+string(test.kind)+`"`) ||
				!strings.Contains(got, test.want) ||
				!strings.HasPrefix(got, "<runtime_recovery") ||
				!strings.HasSuffix(got, "</runtime_recovery>") {
				t.Fatalf("continuation = %q", got)
			}
		})
	}
}
