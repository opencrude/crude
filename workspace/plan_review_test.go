package workspace

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"crude/agent/session"
	textencoding "crude/text/encoding"
	"crude/types"
)

func TestPlanReviewDiskFallbackUsesEditorEncodingAndBounds(t *testing.T) {
	path := filepath.Join(t.TempDir(), "plan.md")
	want := strings.Repeat("x", types.PlanReviewMaxPlanText) + "\n"
	data, err := textencoding.EncodeText([]byte(want), textencoding.UTF16LE)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, data, 0600); err != nil {
		t.Fatal(err)
	}
	if got, err := readPlanReviewText(t.Context(), path); err != nil || got != want {
		t.Fatalf("disk source did not decode the maximum-size plan: len=%d err=%v", len(got), err)
	}
	if err := os.WriteFile(path, append(data, 'x', 0), 0600); err != nil {
		t.Fatal(err)
	}
	if _, err := readPlanReviewText(t.Context(), path); !errors.Is(err, session.ErrPlanReviewTooLong) {
		t.Fatalf("oversize disk source was not refused: %v", err)
	}
}
