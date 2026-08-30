package settings

import (
	"testing"

	"crude/tui/theme"
	"crude/tui/types"
)

// TestAppearanceFramePadFieldsPresent verifies the Appearance form exposes the
// four shell-frame padding rows with the configured values.
func TestAppearanceFramePadFieldsPresent(t *testing.T) {
	top, bottom, left, right := 2, 3, 1, 4
	m := New(theme.DefaultDark(), types.AppSettings{
		Theme: types.ThemeSettings{
			PadTop:    &top,
			PadBottom: &bottom,
			PadLeft:   &left,
			PadRight:  &right,
		},
	}, nil)
	for id, want := range map[string]string{
		"frame-top": "2", "frame-bottom": "3", "frame-left": "1", "frame-right": "4",
	} {
		if got := m.ValueForTest(catAppearance, id); got != want {
			t.Fatalf("field %q = %q want %q", id, got, want)
		}
	}
}

// TestAppearanceFramePadCollectRoundTrip types values into the rows and
// verifies AppSettings carries them as pointers.
func TestAppearanceFramePadCollectRoundTrip(t *testing.T) {
	m := New(theme.DefaultDark(), types.AppSettings{}, nil)
	m.SetValueForTest(catAppearance, "frame-top", "5")
	m.SetValueForTest(catAppearance, "frame-bottom", "0")
	m.SetValueForTest(catAppearance, "frame-left", "2")
	m.SetValueForTest(catAppearance, "frame-right", "12")

	got := m.AppSettings().Theme
	if got.PadTop == nil || *got.PadTop != 5 {
		t.Fatalf("PadTop=%v want 5", got.PadTop)
	}
	if got.PadBottom == nil || *got.PadBottom != 0 {
		t.Fatalf("PadBottom=%v want 0", got.PadBottom)
	}
	if got.PadLeft == nil || *got.PadLeft != 2 {
		t.Fatalf("PadLeft=%v want 2", got.PadLeft)
	}
	if got.PadRight == nil || *got.PadRight != 12 {
		t.Fatalf("PadRight=%v want 12", got.PadRight)
	}
}

// TestAppearanceFramePadInvalidRejected verifies the validator blocks garbage
// while blank (inherit) and zero stay legal.
func TestAppearanceFramePadInvalidRejected(t *testing.T) {
	m := New(theme.DefaultDark(), types.AppSettings{}, nil)
	m.SetValueForTest(catAppearance, "frame-top", "-3")
	if field := m.categories[catAppearance].Form.Field("frame-top"); field != nil && len(field.Errors) == 0 {
		t.Fatal("negative padding must fail validation")
	}
	m.SetValueForTest(catAppearance, "frame-top", "abc")
	if field := m.categories[catAppearance].Form.Field("frame-top"); field != nil && len(field.Errors) == 0 {
		t.Fatal("non-numeric padding must fail validation")
	}
	m.SetValueForTest(catAppearance, "frame-top", "0")
	if field := m.categories[catAppearance].Form.Field("frame-top"); field != nil && len(field.Errors) != 0 {
		t.Fatalf("zero is a valid padding, got errors %v", field.Errors)
	}

	// Invalid input must not persist a pointer.
	m.SetValueForTest(catAppearance, "frame-top", "abc")
	if got := m.AppSettings().Theme.PadTop; got != nil {
		t.Fatalf("invalid input must collect nil, got %v", *got)
	}
}

// TestAppearanceFramePadUnsetCollectsDefault verifies a fresh form (rows
// seeded with the effective values) saves zeros, which resolve identically to
// unset — the shell renders edge to edge.
func TestAppearanceFramePadUnsetCollectsDefault(t *testing.T) {
	m := New(theme.DefaultDark(), types.AppSettings{}, nil)
	got := m.AppSettings().Theme
	if got.PadTopOrDefault() != 0 || got.PadBottomOrDefault() != 0 || got.PadLeftOrDefault() != 0 || got.PadRightOrDefault() != 0 {
		t.Fatal("defaults must resolve to zero on every side")
	}
	if got.PadTop != nil && (*got.PadTop != 0) {
		t.Fatalf("fresh form pad_top=%v want 0 or nil", *got.PadTop)
	}
}
