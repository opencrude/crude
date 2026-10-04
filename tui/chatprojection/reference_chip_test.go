package chatprojection

import (
	"strings"
	"testing"

	"crude/tui/cell"
	"crude/tui/types"
	"crude/tui/ui"
)

func TestRenderUserMessageStylesReferenceLabel(t *testing.T) {
	const label = "[Pasted IMG1]"
	message := types.ChatMessage{
		Role:            types.RoleUser,
		Content:         label + " what is this?",
		ReferenceLabels: []string{label},
	}
	rendered := renderUserMessage(message, renderContext{contentWidth: 80}, true)
	if len(rendered.native) != 1 {
		t.Fatalf("native rows = %+v", rendered.native)
	}
	assertReferenceChipSpans(t, rendered.native[0], label)
}

func TestRenderUserMessageInfersLegacyReferenceLabel(t *testing.T) {
	const label = "[Pasted Text File3]"
	message := types.ChatMessage{Role: types.RoleUser, Content: "review " + label}
	rendered := renderUserMessage(message, renderContext{contentWidth: 80}, true)
	if len(rendered.native) != 1 {
		t.Fatalf("native rows = %+v", rendered.native)
	}
	assertReferenceChipSpans(t, rendered.native[0], label)
}

func TestRenderPendingSteeringUsesCanonicalActivityDot(t *testing.T) {
	message := types.ChatMessage{Role: types.RoleUser, Content: "include tests", PendingSteering: true}
	for _, spinFrame := range []int{0, 400} {
		rendered := renderUserMessage(message, renderContext{contentWidth: 80, spinFrame: spinFrame}, true)
		if len(rendered.native) != 2 {
			t.Fatalf("pending human steering rows = %+v", rendered.native)
		}
		status := rendered.native[1]
		if !strings.Contains(status.PlainText(), ui.ActivityDot(spinFrame)) || !strings.Contains(status.PlainText(), "Waiting to send") {
			t.Fatalf("pending human steering status = %q", status.PlainText())
		}
	}
}

func TestRenderAssignmentIdentifiesMainAgentAndKeepsPrompt(t *testing.T) {
	message := types.ChatMessage{
		Role: types.RoleAssignment, Content: "# Assignment\n- Description: inspect lifecycle",
	}
	rendered := renderAssignmentMessage(message, renderContext{contentWidth: 80, innerW: 80}, false)
	plain := strings.Join(rendered.lines, "\n")
	if !strings.Contains(plain, "Main Agent Assignment") || !strings.Contains(plain, "inspect lifecycle") {
		t.Fatalf("assignment render = %q", plain)
	}
	if messageParty(types.RoleAssignment) == messageParty(types.RoleAssistant) {
		t.Fatal("assignment incorrectly suppresses the sub-agent's own response header")
	}
}

func TestStyleReferenceLabelAcrossExistingSpans(t *testing.T) {
	const label = "[Pasted IMG1]"
	line := PaintLine{Spans: []RowSpan{
		{Text: "before [Pasted", Role: ui.ColorFg},
		{Text: " IMG1] after", Role: ui.ColorMuted, Attr: cell.AttrItalic},
	}}
	styled := styleReferenceLabels(line, []string{label})
	if styled.PlainText() != line.PlainText() {
		t.Fatalf("styling changed text: %q", styled.PlainText())
	}
	assertReferenceChipSpans(t, styled, label)
}

func assertReferenceChipSpans(t *testing.T, line PaintLine, label string) {
	t.Helper()
	var chip strings.Builder
	for _, span := range line.Spans {
		if !span.BgSet || span.BgRole != ui.ColorSelection {
			continue
		}
		if span.Role != ui.ColorAccent2 || span.Attr&cell.AttrBold == 0 {
			t.Fatalf("chip span has wrong style: %+v", span)
		}
		chip.WriteString(span.Text)
	}
	if chip.String() != label {
		t.Fatalf("styled chip = %q, want %q; spans=%+v", chip.String(), label, line.Spans)
	}
}
