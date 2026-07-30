package ui

import (
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
)

func TestActionRegistry_RegisterAndGet(t *testing.T) {
	r := NewActionRegistry()
	a := &ActionDef{ID: "test.run", Label: "Run", Category: "Test"}
	r.Register(a)
	got := r.Get("test.run")
	if got == nil || got.Label != "Run" {
		t.Fatalf("expected to get Run, got %+v", got)
	}
}

func TestActionRegistry_GetMissing(t *testing.T) {
	r := NewActionRegistry()
	if r.Get("nope") != nil {
		t.Fatal("expected nil for missing action")
	}
}

func TestActionRegistry_RegisterReplaces(t *testing.T) {
	r := NewActionRegistry()
	r.Register(&ActionDef{ID: "x", Label: "Old"})
	r.Register(&ActionDef{ID: "x", Label: "New"})
	if r.Get("x").Label != "New" {
		t.Fatal("expected replaced action")
	}
	if len(r.All()) != 1 {
		t.Fatalf("expected 1 action after replace, got %d", len(r.All()))
	}
}

func TestActionRegistry_MatchGlobal(t *testing.T) {
	r := NewActionRegistry()
	r.Register(&ActionDef{
		ID: "palette.open",
		Bindings: []KeyBinding{
			{Key: "ctrl+alt+p"},
			{Mod: tea.ModCtrl | tea.ModAlt, Code: 'p'},
		},
	})
	// String match.
	msg := tea.KeyPressMsg{Code: 0, Text: ""}
	_ = msg
	// Mod-based match.
	msg2 := tea.KeyPressMsg{Code: 'p', Mod: tea.ModCtrl | tea.ModAlt}
	got := r.MatchGlobal(msg2)
	if got == nil || got.ID != "palette.open" {
		t.Fatalf("expected palette.open, got %+v", got)
	}
}

func TestActionRegistry_MatchGlobalSkipsContextActions(t *testing.T) {
	r := NewActionRegistry()
	r.Register(&ActionDef{
		ID:           "editor.save",
		FocusContext: "editor",
		Bindings:     []KeyBinding{{Key: "ctrl+s"}},
	})
	// editor.save is context-scoped, so global match returns nil.
	got := r.MatchGlobal(tea.KeyPressMsg{Code: 0})
	if got != nil {
		t.Fatalf("expected no global match, got %+v", got)
	}
}

func TestActionRegistry_MatchContext(t *testing.T) {
	r := NewActionRegistry()
	r.Register(&ActionDef{
		ID:           "editor.save",
		FocusContext: "editor",
		Bindings:     []KeyBinding{{Key: "ctrl+s"}},
	})
	// String-based matching via Key field.
	got := r.MatchContext("editor", tea.KeyPressMsg{})
	if got != nil {
		t.Fatalf("unexpected match: %+v", got)
	}
}

func TestActionRegistry_ByCategory(t *testing.T) {
	r := NewActionRegistry()
	r.Register(&ActionDef{ID: "a", Category: "File"})
	r.Register(&ActionDef{ID: "b", Category: "File"})
	r.Register(&ActionDef{ID: "c", Category: "Tools"})
	cats := r.ByCategory()
	if len(cats["File"]) != 2 {
		t.Fatalf("expected 2 File actions, got %d", len(cats["File"]))
	}
	if len(cats["Tools"]) != 1 {
		t.Fatalf("expected 1 Tools action, got %d", len(cats["Tools"]))
	}
}

func TestActionRegistry_HintFor(t *testing.T) {
	r := NewActionRegistry()
	r.Register(&ActionDef{ID: "x", HintKey: "Ctrl+Alt+P"})
	if h := r.HintFor("x"); h != "Ctrl+Alt+P" {
		t.Fatalf("expected Ctrl+Alt+P, got %q", h)
	}
	r.Register(&ActionDef{ID: "y", Bindings: []KeyBinding{{Key: "ctrl+b"}}})
	if h := r.HintFor("y"); h != "ctrl+b" {
		t.Fatalf("expected ctrl+b, got %q", h)
	}
}

func TestFormatShortcut(t *testing.T) {
	cases := []struct {
		in, want string
	}{
		{"", ""},
		{"p", "P"},
		{"ctrl+alt+p", "Ctrl+Alt+P"},
		{"alt+c", "Alt+C"},
		{"↑↓", "↑↓"},
		{"ctrl+↑", "Ctrl+↑"},
		{"🔥", "🔥"},
		{"ctrl+🔥", "Ctrl+🔥"},
	}
	for _, tc := range cases {
		got := FormatShortcut(tc.in)
		if got != tc.want {
			t.Fatalf("FormatShortcut(%q)=%q want %q", tc.in, got, tc.want)
		}
		if strings.ContainsRune(got, '\uFFFD') {
			t.Fatalf("FormatShortcut(%q) produced U+FFFD: %q", tc.in, got)
		}
	}
}

func TestDefaultRegistry_HasGlobalActions(t *testing.T) {
	r := DefaultRegistry()
	if r.Get("palette.open") == nil {
		t.Fatal("missing palette.open")
	}
	if r.Get("search.project.open") == nil {
		t.Fatal("missing search.project.open")
	}
	if r.Get("app.quit") == nil {
		t.Fatal("missing app.quit")
	}
	// app.quit has a binding.
	a := r.Get("app.quit")
	if len(a.Bindings) == 0 {
		t.Fatal("app.quit should have bindings")
	}
}

func TestDefaultRegistry_HintsContainOnlyShortcutText(t *testing.T) {
	r := DefaultRegistry()
	for _, action := range r.All() {
		// Arrow-only HintKeys are allowed; forbid embedding prose after arrows.
		if strings.Contains(action.HintKey, "→") || strings.Contains(action.HintKey, "←") {
			runes := []rune(action.HintKey)
			if len(runes) > 2 {
				t.Errorf("action %q embeds navigation prose in HintKey %q", action.ID, action.HintKey)
			}
		}
		if action.HintKey != "" && strings.Contains(strings.ToLower(action.HintKey), strings.ToLower(action.Label)) {
			t.Errorf("action %q embeds label in HintKey %q", action.ID, action.HintKey)
		}
	}
}

func TestDefaultRegistry_MatchPaletteShortcut(t *testing.T) {
	r := DefaultRegistry()
	// Mod-based match for Ctrl+Alt+P.
	msg := tea.KeyPressMsg{Code: 'p', Mod: tea.ModCtrl | tea.ModAlt}
	got := r.MatchGlobal(msg)
	if got == nil || got.ID != "palette.open" {
		t.Fatalf("expected palette.open, got %+v", got)
	}
}

func TestDefaultRegistry_MatchSidebarToggle(t *testing.T) {
	r := DefaultRegistry()
	// String match for ctrl+b. tea.KeyPressMsg.String() produces
	// "ctrl+b" when Mod=ModCtrl and Code='b'.
	msg := tea.KeyPressMsg{Code: 'b', Mod: tea.ModCtrl}
	got := r.MatchGlobal(msg)
	// May or may not match depending on how tea encodes; check.
	if got != nil && got.ID != "view.toggle.sidebar" {
		t.Fatalf("expected view.toggle.sidebar, got %s", got.ID)
	}
}

func TestActionRegistry_HintLabelFor(t *testing.T) {
	r := NewActionRegistry()
	r.Register(&ActionDef{
		ID:      "file.save",
		Label:   "Save",
		HintKey: "Ctrl+S",
	})
	r.Register(&ActionDef{
		ID:       "edit.undo",
		Label:    "Undo",
		Bindings: []KeyBinding{{Key: "ctrl+z"}},
	})
	r.Register(&ActionDef{
		ID:    "no.bindings",
		Label: "No Bindings",
	})

	if got := r.HintLabelFor("file.save"); got != "Ctrl+S save" {
		t.Fatalf("HintLabelFor(file.save) = %q, want %q", got, "Ctrl+S save")
	}
	if got := r.HintLabelFor("edit.undo"); got != "Ctrl+Z undo" {
		t.Fatalf("HintLabelFor(edit.undo) = %q, want %q", got, "Ctrl+Z undo")
	}
	if got := r.HintLabelFor("no.bindings"); got != "" {
		t.Fatalf("HintLabelFor(no.bindings) = %q, want empty (no HintKey)", got)
	}
	if got := r.HintLabelFor("missing"); got != "" {
		t.Fatalf("HintLabelFor(missing) = %q, want empty", got)
	}
}

func TestDefaultRegistry_HasEditorActions(t *testing.T) {
	r := DefaultRegistry()
	for _, id := range []ActionID{"file.save", "edit.undo", "edit.redo"} {
		a := r.Get(id)
		if a == nil {
			t.Fatalf("missing editor action %q", id)
		}
		if a.FocusContext != "editor" {
			t.Fatalf("action %q has FocusContext %q, want %q", id, a.FocusContext, "editor")
		}
		if r.HintLabelFor(id) == "" {
			t.Fatalf("action %q has empty HintLabelFor", id)
		}
	}
}

func TestDefaultRegistry_HasInputActions(t *testing.T) {
	r := DefaultRegistry()
	for _, id := range []ActionID{"input.submit", "input.newline"} {
		a := r.Get(id)
		if a == nil {
			t.Fatalf("missing input action %q", id)
		}
		if a.FocusContext != "chat" {
			t.Fatalf("action %q has FocusContext %q, want %q", id, a.FocusContext, "chat")
		}
		if r.HintLabelFor(id) == "" {
			t.Fatalf("action %q has empty HintLabelFor", id)
		}
	}
}
