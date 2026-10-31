package navigation

import (
	"fmt"
	"strconv"
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"

	"crude/tui/surface"
	"crude/tui/theme"
	"crude/tui/ui"
	"crude/types"
)

func navigationFixture() types.LSPNavigation {
	r := types.LSPNavigationRange{
		Start: types.LSPNavigationPosition{Line: 3, Character: 2},
		End:   types.LSPNavigationPosition{Line: 6, Character: 18},
	}
	return types.LSPNavigation{
		Kind: types.LSPNavigationDefinition, Answer: types.LSPNavigationTargets,
		WireVersion: 17, Line: 2, Col: 4, IncludeDeclaration: true,
		Boundary: types.InsightBoundary{
			Source: types.InsightSource{Kind: "canonical", DocumentID: "source-document",
				WorkspaceGeneration: "workspace-generation", Path: "/workspace/source.go",
				Lifetime: "source-lifetime", Revision: 29},
			Context: types.InsightContext{ID: "source-context", Root: "/workspace", Generation: 31,
				UnknownInputs: []string{"unknown-build-input"}},
			Producer: types.InsightProducer{InstanceID: "provider-instance", Provider: "test-provider",
				ToolIdentity: "tool-identity"},
		},
		Targets: []types.LSPNavigationTarget{
			{URI: "file:///workspace/first.go", Path: "/workspace/first.go", Range: r,
				Selection: types.LSPNavigationRange{Start: r.Start, End: r.Start}, Origin: &r, Link: true},
			{URI: "custom://unavailable", UnavailableReason: "Unsupported URI scheme", Range: r, Selection: r},
			{URI: "file:///external/third.go", Path: "/external/third.go", External: true, Range: r, Selection: r},
		},
	}
}

func navigationHost(s *Screen, w, h int) *ui.Host {
	host := ui.NewHost(theme.DefaultDark(), s)
	host.Resize(w, h)
	host.Layout()
	host.Focus(s.DefaultFocus())
	return host
}

func navigationPaint(host *ui.Host, w, h int) string {
	buffer := surface.NewCellBuffer(w, h)
	host.Paint(buffer)
	var out strings.Builder
	for y := range h {
		for x := range w {
			char := buffer.Get(x, y).Char
			if char == 0 {
				char = ' '
			}
			out.WriteRune(char)
		}
		out.WriteByte('\n')
	}
	return out.String()
}

func navigationKey(host *ui.Host, code rune) ui.EventResult {
	return host.Handle(tea.KeyPressMsg{Code: code})
}

func navigationAssertClose(t *testing.T, result ui.EventResult, s *Screen) {
	t.Helper()
	if result.Cmd == nil {
		t.Fatal("Close did not emit its command")
	}
	if message, ok := result.Cmd().(CloseMsg); !ok || message.Target != s {
		t.Fatalf("expected exact-owner Close, got %#v", message)
	}
}

func navigationAssertBounds(t *testing.T, component ui.Component, w, h int) {
	t.Helper()
	if parent, ok := component.(ui.Parent); ok {
		for _, child := range parent.Children() {
			r := child.Bounds()
			if r.X < 0 || r.Y < 0 || r.W < 0 || r.H < 0 || r.X+r.W > w || r.Y+r.H > h {
				t.Fatalf("%s escaped parent-local %dx%d: %+v", child.ID(), w, h, r)
			}
			navigationAssertBounds(t, child, r.W, r.H)
		}
	}
}

func TestNavigationRenderedInspectDefaultCloseExplicitOpenAndBack(t *testing.T) {
	for _, size := range [][2]int{{160, 40}, {80, 24}} {
		t.Run(fmt.Sprintf("%dx%d", size[0], size[1]), func(t *testing.T) {
			s := New(nil)
			s.SetResult(navigationFixture())
			host := navigationHost(s, size[0], size[1])
			paint := navigationPaint(host, size[0], size[1])
			for _, label := range []string{"first.go", "Unavailable", "Outside workspace", "Close"} {
				if !strings.Contains(paint, label) {
					t.Fatalf("list omitted %q:\n%s", label, paint)
				}
			}
			// List Tab reaches Close without inspecting or implicitly opening.
			navigationKey(host, tea.KeyTab)
			if host.Focused() != ActionsID || !s.InList() {
				t.Fatal("list Tab did not visit actions")
			}
			navigationAssertClose(t, navigationKey(host, tea.KeyEnter), s)
			navigationKey(host, tea.KeyTab)
			if host.Focused() != ListID {
				t.Fatal("actions Tab did not return to list")
			}
			if got := navigationKey(host, tea.KeyEnter); got.Cmd != nil || s.InList() {
				t.Fatal("Enter must inspect, never emit Open")
			}
			// No test-side MarkLayout: inspect must invalidate the real action tree.
			paint = navigationPaint(host, size[0], size[1])
			for _, label := range []string{"URI (literal, complete)", "targetRange: [3:2, 6:18)",
				"targetSelectionRange: [3:2, 3:2)", "originSelectionRange: [3:2, 6:18)", "Open", "Back", "Close"} {
				if !strings.Contains(paint, label) {
					t.Fatalf("detail omitted %q or had stale action geometry:\n%s", label, paint)
				}
			}
			if host.Focused() != ActionsID {
				t.Fatal("detail did not default to actions")
			}
			navigationAssertClose(t, navigationKey(host, tea.KeyEnter), s)
			// Only choosing Open deliberately emits a selected-row request.
			navigationKey(host, tea.KeyLeft)
			navigationKey(host, tea.KeyLeft)
			got := navigationKey(host, tea.KeyEnter)
			if got.Cmd == nil {
				t.Fatal("explicit Open is missing")
			}
			message, ok := got.Cmd().(OpenMsg)
			if !ok || message.Target != s || message.Index != 0 {
				t.Fatalf("Open lost selected row or owner: %#v", message)
			}
			if got := s.HandleAction(ui.EventContext{}, ui.Action{ID: ActionOpen}); got.Cmd != nil {
				t.Fatal("Open could be issued twice while busy")
			}
			navigationKey(host, tea.KeyEsc) // Back remains usable while busy.
			if !s.InList() || host.Focused() != ListID || s.list.SelectedIndex() != 0 {
				t.Fatal("detail Esc did not return to retained list selection")
			}
			if got := navigationKey(host, tea.KeyEnter); got.Cmd != nil || !s.InList() {
				t.Fatal("busy list reactivated a target")
			}
			s.SetBusy(false)
			navigationKey(host, tea.KeyDown)
			navigationKey(host, tea.KeyEnter)
			navigationPaint(host, size[0], size[1])
			if got := s.HandleAction(ui.EventContext{}, ui.Action{ID: ActionOpen}); got.Cmd != nil {
				t.Fatal("unavailable target acquired Open authority")
			}
			if !strings.Contains(s.text.Value(), `"Unsupported URI scheme"`) {
				t.Fatal("unavailable reason is not inspectable")
			}
			// Close → Back via actual native action navigation, retaining row one.
			navigationKey(host, tea.KeyLeft)
			navigationKey(host, tea.KeyEnter)
			if !s.InList() || s.list.SelectedIndex() != 1 {
				t.Fatal("Back action lost unavailable row selection")
			}
			navigationKey(host, tea.KeyDown)
			navigationKey(host, tea.KeyEnter)
			paint = navigationPaint(host, size[0], size[1])
			for _, label := range []string{"outside workspace", "Open external file", "No target file contents have been read yet",
				"unversioned", "structural validation", "cannot guarantee semantic freshness"} {
				if !strings.Contains(strings.ReplaceAll(paint, "\n", " "), label) &&
					!strings.Contains(s.text.Value(), label) {
					t.Fatalf("external detail omitted %q:\n%s", label, paint)
				}
			}
			if !strings.Contains(paint, "[ Open external file ]") {
				t.Fatalf("external native Open footer not visible:\n%s", paint)
			}
			navigationAssertClose(t, navigationKey(host, tea.KeyEnter), s)
			navigationKey(host, tea.KeyEsc)
			navigationAssertClose(t, navigationKey(host, tea.KeyEsc), s)
		})
	}
}

func TestNavigationImmutableLiteralEvidenceAndCopy(t *testing.T) {
	s := New(nil)
	result := navigationFixture()
	uri := "file:///workspace/\"quoted\"\\tab\t.go"
	path := "/workspace/\"quoted\"\\tab\t.go"
	result.Targets[0].URI, result.Targets[0].Path = uri, path
	s.SetResult(result)
	result.Targets[0].URI = "forged"
	result.Targets[0].Origin.Start.Line = 999
	result.Boundary.Context.UnknownInputs[0] = "forged"
	host := navigationHost(s, 80, 24)
	navigationKey(host, tea.KeyEnter)
	value := s.text.Value()
	for _, literal := range []string{strconv.Quote(uri), strconv.Quote(path), "source-document", "source-lifetime",
		"workspace-generation", "source-context", "unknown-build-input", "provider-instance", "test-provider",
		"tool-identity", "originSelectionRange: [3:2, 6:18)", "Source wire version: 17",
		"Invocation (zero-based UTF-16): 2:4", "References includeDeclaration: true"} {
		if !strings.Contains(value, literal) {
			t.Fatalf("complete immutable evidence omitted %q: %s", literal, value)
		}
	}
	if strings.Contains(value, "forged") {
		t.Fatal("caller mutated retained presentation")
	}
	navigationKey(host, tea.KeyTab) // Close → reader
	if host.Focused() != TextID {
		t.Fatal("detail Tab did not reach reader")
	}
	var copied string
	s.text.SetClipboardCopy(func(text string) tea.Cmd {
		return func() tea.Msg { copied = text; return nil }
	})
	host.Handle(tea.KeyPressMsg{Code: 'a', Mod: tea.ModCtrl})
	copyResult := host.Handle(tea.KeyPressMsg{Code: 'c', Mod: tea.ModCtrl})
	if copyResult.Cmd == nil || copied != "" {
		t.Fatal("copy missing or performed synchronously")
	}
	copyResult.Cmd()
	if copied != value {
		t.Fatal("copy did not include complete literal metadata and provenance")
	}
	navigationKey(host, tea.KeyEsc)
	if s.InList() {
		t.Fatal("Esc skipped native text selection cleanup")
	}
	host.Handle(tea.PasteMsg{Content: "mutate"})
	if s.text.Value() != value {
		t.Fatal("read-only evidence was editable")
	}
	navigationKey(host, tea.KeyTab)
	if host.Focused() != ActionsID {
		t.Fatal("reader Tab failed to reach native actions")
	}
	navigationAssertClose(t, navigationKey(host, tea.KeyEnter), s)
}

func TestNavigationDistinctNonTargetLoadingFailureAndInvalidStates(t *testing.T) {
	for answer, description := range map[string]string{
		types.LSPNavigationNull:        "Provider returned null.",
		types.LSPNavigationEmpty:       "Provider returned an empty array [].",
		types.LSPNavigationUnsupported: "Navigation is unsupported for this source/provider.",
	} {
		t.Run(answer, func(t *testing.T) {
			s := New(nil)
			result := navigationFixture()
			result.Answer, result.Targets = answer, nil
			s.SetResult(result)
			host := navigationHost(s, 80, 24)
			paint := navigationPaint(host, 80, 24)
			if !strings.Contains(paint, description) || !strings.Contains(s.text.Value(), "unknown-build-input") ||
				!strings.Contains(paint, "not proof") || s.InList() {
				t.Fatalf("non-target answer lost literal distinction/provenance:\n%s", paint)
			}
			navigationAssertClose(t, navigationKey(host, tea.KeyEnter), s)
		})
	}
	s := New(nil)
	for _, state := range []string{"loading", "error"} {
		s.SetResult(navigationFixture())
		if state == "loading" {
			s.SetLoading(types.LSPNavigationReferences)
		} else {
			s.SetError("transport failed\nquoted failure")
		}
		host := navigationHost(s, 80, 24)
		paint := navigationPaint(host, 80, 24)
		if s.InList() || !strings.Contains(paint, "Close") ||
			s.HandleAction(ui.EventContext{}, ui.Action{ID: ActionOpen}).Cmd != nil ||
			s.HandleAction(ui.EventContext{}, ui.Action{ID: ActionInspect, Payload: ui.ListItem{Payload: 0}}).Cmd != nil {
			t.Fatalf("%s allowed a stale target activation:\n%s", state, paint)
		}
		navigationAssertClose(t, navigationKey(host, tea.KeyEnter), s)
	}
	for name, invalidate := range map[string]func(*types.LSPNavigation){
		"target count": func(r *types.LSPNavigation) { r.Targets = make([]types.LSPNavigationTarget, 1025) },
		"uri":          func(r *types.LSPNavigation) { r.Targets[0].URI = strings.Repeat("u", 4097) },
		"path":         func(r *types.LSPNavigation) { r.Targets[0].Path = strings.Repeat("p", 4097) },
		"aggregate": func(r *types.LSPNavigation) {
			r.Boundary.Context.UnknownInputs = make([]string, 40)
			for i := range r.Boundary.Context.UnknownInputs {
				r.Boundary.Context.UnknownInputs[i] = strings.Repeat("x", 4096)
			}
		},
		"unknown inputs": func(r *types.LSPNavigation) { r.Boundary.Context.UnknownInputs = make([]string, 1025) },
		"utf8":           func(r *types.LSPNavigation) { r.Targets[0].URI = "\xff" },
		"range":          func(r *types.LSPNavigation) { r.Targets[0].Range.End.Line = -1 },
		"selection":      func(r *types.LSPNavigation) { r.Targets[0].Selection.End.Character = -1 },
		"origin":         func(r *types.LSPNavigation) { r.Targets[0].Origin.End.Line = 0 },
		"answer":         func(r *types.LSPNavigation) { r.Answer = "future" },
		"kind":           func(r *types.LSPNavigation) { r.Kind = "typeDefinition" },
		"null targets":   func(r *types.LSPNavigation) { r.Answer = types.LSPNavigationNull },
	} {
		t.Run(name, func(t *testing.T) {
			s := New(nil)
			s.SetResult(navigationFixture())
			invalid := navigationFixture()
			invalidate(&invalid)
			s.SetResult(invalid)
			host := navigationHost(s, 80, 24)
			paint := navigationPaint(host, 80, 24)
			if s.InList() || !strings.Contains(paint, "refused") ||
				len(s.result.Targets) != 0 || s.HandleAction(ui.EventContext{}, ui.Action{ID: ActionOpen}).Cmd != nil {
				t.Fatalf("invalid data left active results:\n%s", paint)
			}
			navigationAssertClose(t, navigationKey(host, tea.KeyEnter), s)
		})
	}
}

func TestNavigationRenderedScrollingResizeAndTinyBounds(t *testing.T) {
	s := New(nil)
	result := navigationFixture()
	base := result.Targets[0]
	result.Targets = make([]types.LSPNavigationTarget, 1024)
	for i := range result.Targets {
		result.Targets[i] = base
		result.Targets[i].URI = fmt.Sprintf("file:///workspace/target-%04d.go", i)
		result.Targets[i].Path = fmt.Sprintf("/workspace/target-%04d.go", i)
	}
	result.Boundary.Context.UnknownInputs = make([]string, 100)
	for i := range result.Boundary.Context.UnknownInputs {
		result.Boundary.Context.UnknownInputs[i] = fmt.Sprintf("unknown-context-%03d", i)
	}
	s.SetResult(result)
	host := navigationHost(s, 80, 24)
	before := navigationPaint(host, 80, 24)
	navigationKey(host, tea.KeyEnd)
	after := navigationPaint(host, 80, 24)
	if !strings.Contains(before, "target-0000") || !strings.Contains(after, "target-1023") ||
		strings.Contains(after, "target-0000") || s.list.Scroll().Offset() == 0 {
		t.Fatal("native list navigation did not move rendered viewport")
	}
	navigationKey(host, tea.KeyEnter)
	navigationPaint(host, 80, 24)
	navigationKey(host, tea.KeyTab)
	before = navigationPaint(host, 80, 24)
	for range 12 {
		navigationKey(host, tea.KeyPgDown)
	}
	after = navigationPaint(host, 80, 24)
	if s.text.ScrollOffset() == 0 || before == after || !strings.Contains(after, "unknown-context-099") ||
		!strings.Contains(after, "Close") {
		t.Fatalf("long provenance was not genuinely scrollable with pinned actions:\n%s", after)
	}
	for _, size := range [][2]int{{160, 40}, {80, 24}, {9, 5}, {1, 1}, {80, 24}} {
		host.Resize(size[0], size[1])
		host.Layout()
		navigationAssertBounds(t, s, size[0], size[1])
		paint := navigationPaint(host, size[0], size[1])
		if size[0] >= 80 && !strings.Contains(paint, "Close") {
			t.Fatalf("resizing lost native actions:\n%s", paint)
		}
		if size[0] > 0 && size[1] > 0 {
			host.Focus(ActionsID)
			navigationAssertClose(t, navigationKey(host, tea.KeyEnter), s)
		}
	}
	navigationKey(host, tea.KeyEsc)
	host.Layout()
	if !s.InList() || s.list.SelectedIndex() != 1023 {
		t.Fatal("resizing/reading/back lost original row selection")
	}
	for _, th := range []*theme.Theme{theme.DefaultLight(), theme.DefaultDark()} {
		host.SetTheme(th)
		navigationPaint(host, 80, 24)
	}
	// Parent offsets never leak into child-local geometry.
	s.Layout(ui.LayoutContext{}, ui.Rect{X: 7, Y: 9, W: 9, H: 5})
	navigationAssertBounds(t, s, 9, 5)
	// Host deliberately skips zero-sized layouts; check the body's contract
	// directly instead of mistaking the Host's last geometry for new geometry.
	s.Layout(ui.LayoutContext{}, ui.Rect{})
	navigationAssertBounds(t, s, 0, 0)
}

func TestNavigationNarrowListKeepsDestinationNamesVisible(t *testing.T) {
	result := navigationFixture()
	root := "/workspace/" + strings.Repeat("a-long-directory/", 8)
	first, second := result.Targets[0], result.Targets[0]
	first.Path, second.Path = root+"lib.go", root+"main.go"
	first.URI, second.URI = "file://"+first.Path, "file://"+second.Path
	result.Targets = []types.LSPNavigationTarget{first, second}
	s := New(nil)
	s.SetResult(result)
	host := navigationHost(s, 54, 14) // Actual content width of the 60-column shell.
	paint := navigationPaint(host, 54, 14)
	if !strings.Contains(paint, "lib.go") || !strings.Contains(paint, "main.go") {
		t.Fatalf("narrow chooser clipped both destination names behind shared path prefixes:\n%s", paint)
	}
	navigationKey(host, tea.KeyDown)
	navigationKey(host, tea.KeyEnter)
	if !strings.Contains(s.text.Value(), strconv.Quote(second.URI)) || !strings.Contains(s.text.Value(), strconv.Quote(second.Path)) {
		t.Fatal("compact row label lost complete literal destination metadata")
	}
}

func TestNavigationUnavailableOpenGatesAndMaximumLiteral(t *testing.T) {
	for _, target := range []types.LSPNavigationTarget{
		{URI: "file:///missing-path"},
		{URI: "file:///blocked", Path: "/blocked", UnavailableReason: "not available"},
	} {
		s := New(nil)
		result := navigationFixture()
		result.Targets = []types.LSPNavigationTarget{target}
		s.SetResult(result)
		host := navigationHost(s, 80, 24)
		navigationKey(host, tea.KeyEnter)
		paint := navigationPaint(host, 80, 24)
		if !strings.Contains(paint, "Unavailable") || !strings.Contains(paint, "Close") {
			t.Fatalf("missing unavailable explanation or native Close:\n%s", paint)
		}
		navigationKey(host, tea.KeyLeft)
		navigationKey(host, tea.KeyLeft)
		if got := navigationKey(host, tea.KeyEnter); got.Cmd != nil {
			t.Fatal("unavailable Open button could activate")
		}
		navigationKey(host, tea.KeyRight)
		navigationKey(host, tea.KeyRight)
		navigationAssertClose(t, navigationKey(host, tea.KeyEnter), s)
	}
	s := New(nil)
	result := navigationFixture()
	result.Kind = types.LSPNavigationTypeDefinition
	result.Targets[0].URI, result.Targets[0].Path = strings.Repeat("u", 4096), strings.Repeat("p", 4096)
	s.SetResult(result)
	host := navigationHost(s, 80, 24)
	navigationKey(host, tea.KeyEnter)
	navigationPaint(host, 80, 24)
	if !strings.Contains(s.text.Value(), strconv.Quote(result.Targets[0].URI)) ||
		!strings.Contains(s.text.Value(), strconv.Quote(result.Targets[0].Path)) ||
		!strings.Contains(s.text.Value(), `"type-definition"`) {
		t.Fatal("maximum valid literal was truncated or closed kind changed")
	}
}
