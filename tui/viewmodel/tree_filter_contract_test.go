package viewmodel

import (
	"testing"

	tea "charm.land/bubbletea/v2"
)

// TestTreeFilterTypingContractViewmodel verifies viewmodel routes filter keys
// through sidebarHost exactly once per keystroke; model owns edit state.
func TestTreeFilterTypingContractViewmodel(t *testing.T) {
	app, model, _ := setupTreeRoutingApp(t)
	app.FocusStack.Clear()
	app.FocusStack.Push(FocusTree)

	type step struct {
		key    tea.KeyPressMsg
		want   string
		active bool
	}
	steps := []step{
		{key: tea.KeyPressMsg{Code: '/', Text: "/"}, active: true},
		{key: tea.KeyPressMsg{Code: 'm', Text: "m"}, want: "m", active: true},
		{key: tea.KeyPressMsg{Code: 'a', Text: "a"}, want: "ma", active: true},
		{key: tea.KeyPressMsg{Code: 'i', Text: "i"}, want: "mai", active: true},
		{key: tea.KeyPressMsg{Code: tea.KeyBackspace}, want: "ma", active: true},
	}

	for i, st := range steps {
		app.dispatchKeyPress(st.key)
		if model.FilterActive() != st.active {
			t.Fatalf("step %d FilterActive=%v want %v", i, model.FilterActive(), st.active)
		}
		if got := model.Filter(); got != st.want {
			t.Fatalf("step %d filter=%q want %q", i, got, st.want)
		}
	}

	// Filtering moves the cursor to the first match. The workspace root is
	// not a painted row, so a tight filter may leave a single match; keys
	// must still route to the model without moving off the list.
	before := model.CursorIndex()
	switch {
	case len(model.VisibleNodes()) < 2:
		app.dispatchKeyPress(tea.KeyPressMsg{Code: tea.KeyDown})
		if model.CursorIndex() != before {
			t.Fatalf("Down on a single match cursor=%d want %d", model.CursorIndex(), before)
		}
	case before > 0:
		app.dispatchKeyPress(tea.KeyPressMsg{Code: tea.KeyUp})
		if model.CursorIndex() != before-1 {
			t.Fatalf("Up while filtering cursor=%d want %d", model.CursorIndex(), before-1)
		}
		app.dispatchKeyPress(tea.KeyPressMsg{Code: tea.KeyDown})
		if model.CursorIndex() != before {
			t.Fatalf("Down while filtering cursor=%d want %d", model.CursorIndex(), before)
		}
	default:
		app.dispatchKeyPress(tea.KeyPressMsg{Code: tea.KeyDown})
		if model.CursorIndex() != 1 {
			t.Fatalf("Down while filtering cursor=%d want 1", model.CursorIndex())
		}
		app.dispatchKeyPress(tea.KeyPressMsg{Code: tea.KeyUp})
		if model.CursorIndex() != 0 {
			t.Fatalf("Up while filtering cursor=%d want 0", model.CursorIndex())
		}
	}

	app.dispatchKeyPress(tea.KeyPressMsg{Code: tea.KeyEsc})
	if model.Filter() != "" || model.FilterActive() {
		t.Fatal("first Esc should clear filter")
	}
	if app.FocusStack.Top() != FocusTree {
		t.Fatal("first Esc should keep tree focus")
	}

	app.dispatchKeyPress(tea.KeyPressMsg{Code: tea.KeyEsc})
	if app.FocusStack.Top() == FocusTree {
		t.Fatal("second Esc should defocus tree")
	}
}
