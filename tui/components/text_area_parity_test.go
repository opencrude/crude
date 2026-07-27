package components

import (
	"testing"

	"crude/tui/rendering"
	"crude/tui/theme"
	"crude/tui/ui"
)

func TestTextAreaANSIViewMatchesLegacyTextInput(t *testing.T) {
	th := theme.DefaultDark()
	cases := []struct {
		name  string
		value string
		width int
	}{
		{name: "single line", value: "hello world", width: 30},
		{name: "wrapped", value: "one two three four five six", width: 18},
		{name: "multiline", value: "first line\nsecond line", width: 30},
		{name: "unicode", value: "日本語 👩‍💻", width: 30},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			legacy := NewTextInput(th)
			modern := ui.NewTextArea("test/parity", ui.NewTokenSet(th), true)
			legacy.SetValue(tc.value)
			modern.SetValue(tc.value)
			legacy.SetPrompt(">")
			modern.SetPrompt(">")

			legacyRaw := legacy.View(tc.width, 0, true)
			modernRaw := modern.View(tc.width, 0, true)
			legacyView := rendering.StripAnsi(legacyRaw)
			modernView := rendering.StripAnsi(modernRaw)
			if legacyView != modernView {
				t.Fatalf("legacy=%q modern=%q", legacyView, modernView)
			}
			if tc.name != "unicode" && legacyRaw != modernRaw {
				t.Fatalf("ANSI drift: legacy=%q modern=%q", legacyRaw, modernRaw)
			}
		})
	}
}

func TestTextAreaANSIViewMatchesLegacySelectionAndAtomicRange(t *testing.T) {
	th := theme.DefaultDark()
	legacy := NewTextInput(th)
	modern := ui.NewTextArea("test/parity", ui.NewTokenSet(th), true)
	value := "before [@ref] after"
	legacy.SetValue(value)
	modern.SetValue(value)
	legacy.SetPrompt("")
	modern.SetPrompt("")
	legacy.SetAtomicRanges([]AtomicRange{{ID: "ref", Start: 7, End: 13}})
	modern.SetAtomicRanges([]ui.AtomicRange{{ID: "ref", Start: 7, End: 13}})
	legacy.SetSelection(2, 14)
	modern.SetSelection(2, 14)

	legacyRaw := legacy.View(40, 0, true)
	modernRaw := modern.View(40, 0, true)
	legacyView := rendering.StripAnsi(legacyRaw)
	modernView := rendering.StripAnsi(modernRaw)
	if legacyView != modernView {
		t.Fatalf("legacy=%q modern=%q", legacyView, modernView)
	}
	if legacyRaw != modernRaw {
		t.Fatalf("ANSI drift: legacy=%q modern=%q", legacyRaw, modernRaw)
	}
}
