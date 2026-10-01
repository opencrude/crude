package reasoning

import "testing"

func TestTraceKeepsBlocksAndRefusesSummaryInference(t *testing.T) {
	var display Display
	text := display.Append("", Delta{Text: "first", Kind: Summary, BlockID: "one"})
	text = display.Append(text, Delta{Text: "second", Kind: Summary, BlockID: "two"})
	if text != "first\n\nsecond" || display.Kind != Summary || text[display.Start:] != "second" {
		t.Fatalf("trace=%q metadata=%+v", text, display)
	}
	before := display
	if got := display.Append(text, Delta{Text: "ciphertext", Kind: Encrypted}); got != text || display != before {
		t.Fatal("opaque reasoning changed the visible trace")
	}
	text = display.Append(text, Delta{Text: " unlabelled"})
	text = display.Append(text, Delta{Text: " summary", Kind: Summary})
	if display.Kind != Unknown {
		t.Fatalf("mixed content was promoted to summary: %q %+v", text, display)
	}
}

func TestCondensedByUsageRequiresStrongReportedEvidence(t *testing.T) {
	for _, tc := range []struct {
		visible, billed int
		known, want     bool
	}{
		{249, 736, true, true}, // first request group from a condensed gateway
		{317, 1002, true, true},
		{1000, 300, true, false}, // plausible raw trace
		{249, 736, false, false},
		{5, 900, true, false}, // tiny or empty traces do not classify a gateway
		{249, 100, true, false},
	} {
		if got := CondensedByUsage(tc.visible, tc.billed, tc.known); got != tc.want {
			t.Fatalf("visible=%d billed=%d known=%t: %t want %t", tc.visible, tc.billed, tc.known, got, tc.want)
		}
	}
	if !InferredSummary.IsSummary() || !Summary.IsSummary() || Raw.IsSummary() || Unknown.IsSummary() {
		t.Fatal("reasoning presentation classification changed")
	}
	if !Provisional.HasCompactPreview() || Provisional.IsSummary() || Raw.HasCompactPreview() {
		t.Fatal("provisional thinking was confused with a confirmed summary or raw trace")
	}
	if !InferredRaw.IsRaw() || !Raw.IsRaw() || InferredRaw.IsSummary() || InferredRaw.HasCompactPreview() {
		t.Fatal("measured raw thinking was confused with a compact summary")
	}
}
