package navigation

import (
	"fmt"
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
		Kind: types.LSPNavigationReferences, Answer: types.LSPNavigationTargets,
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
			{URI: "file:///workspace/pkg/first.go", Path: "/workspace/pkg/first.go", Range: r,
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

// A list of places reads like an IDE: file and position, nothing else. Debug
// words never appear.
func TestResultsListFilesAndPositionsInPlainWords(t *testing.T) {
	for _, size := range [][2]int{{160, 40}, {80, 24}} {
		t.Run(fmt.Sprintf("%dx%d", size[0], size[1]), func(t *testing.T) {
			s := New(nil)
			s.SetResult(navigationFixture())
			host := navigationHost(s, size[0], size[1])
			paint := navigationPaint(host, size[0], size[1])
			for _, want := range []string{"first.go:4:3", "pkg", "cannot be opened", "outside the project"} {
				if !strings.Contains(paint, want) {
					t.Fatalf("list omitted %q:\n%s", want, paint)
				}
			}
			for _, banned := range []string{"Close", "Inspect", "literal", "provenance", "Provider", "wire", "UTF-16", "file:///"} {
				if strings.Contains(paint, banned) {
					t.Fatalf("list shows internal word %q:\n%s", banned, paint)
				}
			}
		})
	}
}

// Choosing a row is the whole decision: one Enter or click emits the jump.
// There is no inspect page and no Open button between the choice and the code.
func TestChoosingARowGoesStraightToTheCode(t *testing.T) {
	s := New(nil)
	s.SetResult(navigationFixture())
	host := navigationHost(s, 100, 24)
	result := navigationKey(host, tea.KeyEnter)
	if result.Cmd == nil {
		t.Fatal("Enter on a row did not open it")
	}
	if open, ok := result.Cmd().(OpenMsg); !ok || open.Target != s || open.Index != 0 {
		t.Fatalf("Enter emitted %#v, want Open of row 0", open)
	}
	if !s.InList() {
		t.Fatal("choosing a row replaced the list with another page")
	}
	// While the file opens, a second Enter changes nothing.
	if again := navigationKey(host, tea.KeyEnter); again.Cmd != nil {
		t.Fatal("a second Enter opened the row twice")
	}
}

func TestRowsThatCannotBeOpenedDoNothing(t *testing.T) {
	s := New(nil)
	s.SetResult(navigationFixture())
	host := navigationHost(s, 100, 24)
	if got := s.HandleAction(ui.EventContext{}, ui.Action{ID: ActionOpen, Payload: ui.ListItem{Payload: 1}}); got.Cmd != nil {
		t.Fatal("a row without a file emitted Open")
	}
	navigationKey(host, tea.KeyDown)
	got := navigationKey(host, tea.KeyEnter)
	if got.Cmd == nil {
		t.Fatal("an outside-the-project row must still open when chosen")
	}
	if open, ok := got.Cmd().(OpenMsg); !ok || open.Index != 2 {
		t.Fatalf("emitted %#v", open)
	}
}

func TestEscClosesWithoutFooterAndTabKeepsTheList(t *testing.T) {
	s := New(nil)
	s.SetResult(navigationFixture())
	host := navigationHost(s, 100, 24)
	navigationKey(host, tea.KeyTab)
	if host.Focused() != ListID {
		t.Fatal("Tab left the list for a redundant footer")
	}
	navigationAssertClose(t, navigationKey(host, tea.KeyEsc), s)
}

func TestEmptyAnswersSayWhatWasNotFound(t *testing.T) {
	cases := []struct{ kind, answer, want string }{
		{types.LSPNavigationReferences, types.LSPNavigationEmpty, "No references found."},
		{types.LSPNavigationDefinition, types.LSPNavigationNull, "No definition found."},
		{types.LSPNavigationImplementation, types.LSPNavigationEmpty, "No implementation found."},
		{types.LSPNavigationTypeDefinition, types.LSPNavigationNull, "No type definition found."},
		{types.LSPNavigationReferences, types.LSPNavigationUnsupported, "This language server cannot do that for this file."},
	}
	for _, c := range cases {
		t.Run(c.kind+"/"+c.answer, func(t *testing.T) {
			s := New(nil)
			result := navigationFixture()
			result.Kind, result.Answer, result.Targets = c.kind, c.answer, nil
			s.SetResult(result)
			host := navigationHost(s, 80, 24)
			paint := navigationPaint(host, 80, 24)
			if !strings.Contains(paint, c.want) || s.InList() {
				t.Fatalf("want %q:\n%s", c.want, paint)
			}
			for _, banned := range []string{"unknown-build-input", "not proof", "null", "[]", "literal"} {
				if strings.Contains(paint, banned) {
					t.Fatalf("empty answer shows %q:\n%s", banned, paint)
				}
			}
			navigationAssertClose(t, navigationKey(host, tea.KeyEsc), s)
		})
	}
}

func TestLoadingAndFailureShowOnlyPlainText(t *testing.T) {
	s := New(nil)
	s.SetResult(navigationFixture())
	s.SetLoading(types.LSPNavigationReferences)
	host := navigationHost(s, 80, 24)
	paint := navigationPaint(host, 80, 24)
	if s.InList() || !strings.Contains(paint, "Searching") || strings.Contains(paint, "Close") {
		t.Fatalf("loading state:\n%s", paint)
	}
	if got := s.HandleAction(ui.EventContext{}, ui.Action{ID: ActionOpen, Payload: ui.ListItem{Payload: 0}}); got.Cmd != nil {
		t.Fatal("a stale row opened while searching")
	}
	s.SetError("The language server did not answer in time.")
	host = navigationHost(s, 80, 24)
	paint = navigationPaint(host, 80, 24)
	if !strings.Contains(paint, "did not answer in time") {
		t.Fatalf("failure lost its reason:\n%s", paint)
	}
	for _, banned := range []string{"Failure (literal)", "No target was opened", "\"The language", "Navigation failed"} {
		if strings.Contains(paint, banned) {
			t.Fatalf("failure shows %q:\n%s", banned, paint)
		}
	}
	navigationAssertClose(t, navigationKey(host, tea.KeyEsc), s)
}

func TestInvalidAnswersAreRefusedWithoutActiveRows(t *testing.T) {
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
			if s.InList() || !strings.Contains(paint, "cannot be shown") || len(s.result.Targets) != 0 ||
				s.HandleAction(ui.EventContext{}, ui.Action{ID: ActionOpen, Payload: ui.ListItem{Payload: 0}}).Cmd != nil {
				t.Fatalf("invalid data left active results:\n%s", paint)
			}
			navigationAssertClose(t, navigationKey(host, tea.KeyEsc), s)
		})
	}
}

func TestLongListScrollsAndSurvivesResizeAndTinyBounds(t *testing.T) {
	s := New(nil)
	result := navigationFixture()
	base := result.Targets[0]
	result.Targets = make([]types.LSPNavigationTarget, 1024)
	for i := range result.Targets {
		result.Targets[i] = base
		result.Targets[i].URI = fmt.Sprintf("file:///workspace/target-%04d.go", i)
		result.Targets[i].Path = fmt.Sprintf("/workspace/target-%04d.go", i)
	}
	s.SetResult(result)
	host := navigationHost(s, 80, 24)
	before := navigationPaint(host, 80, 24)
	navigationKey(host, tea.KeyEnd)
	after := navigationPaint(host, 80, 24)
	if !strings.Contains(before, "target-0000") || !strings.Contains(after, "target-1023") ||
		strings.Contains(after, "target-0000") || s.list.Scroll().Offset() == 0 {
		t.Fatal("list navigation did not move the viewport")
	}
	for _, size := range [][2]int{{160, 40}, {80, 24}, {9, 5}, {1, 1}, {80, 24}} {
		host.Resize(size[0], size[1])
		host.Layout()
		navigationAssertBounds(t, s, size[0], size[1])
		paint := navigationPaint(host, size[0], size[1])
		if size[0] >= 80 && strings.Contains(paint, "Close") {
			t.Fatalf("resizing added a redundant footer:\n%s", paint)
		}
	}
	for _, th := range []*theme.Theme{theme.DefaultLight(), theme.DefaultDark()} {
		host.SetTheme(th)
		navigationPaint(host, 80, 24)
	}
	// Parent offsets never leak into child-local geometry.
	s.Layout(ui.LayoutContext{}, ui.Rect{X: 7, Y: 9, W: 9, H: 5})
	navigationAssertBounds(t, s, 9, 5)
	s.Layout(ui.LayoutContext{}, ui.Rect{})
	navigationAssertBounds(t, s, 0, 0)
}

// The file name and position stay visible however long the folders are.
func TestNarrowListKeepsFileNamesVisible(t *testing.T) {
	result := navigationFixture()
	root := "/workspace/" + strings.Repeat("a-long-directory/", 8)
	first, second := result.Targets[0], result.Targets[0]
	first.Path, second.Path = root+"lib.go", root+"main.go"
	result.Boundary.Context.Root = "/workspace"
	result.Targets = []types.LSPNavigationTarget{first, second}
	s := New(nil)
	s.SetResult(result)
	host := navigationHost(s, 54, 14)
	paint := navigationPaint(host, 54, 14)
	if !strings.Contains(paint, "lib.go") || !strings.Contains(paint, "main.go") {
		t.Fatalf("narrow list clipped the file names behind shared folders:\n%s", paint)
	}
}
