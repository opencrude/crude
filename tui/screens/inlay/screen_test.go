package inlay

import (
	"fmt"
	"strings"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"

	"crude/tui/surface"
	"crude/tui/theme"
	"crude/tui/ui"
	"crude/types"
	"crude/types/control"
)

func fixture() types.LSPInlaySet {
	r := types.LSPInlayRange{Start: types.LSPInlayPosition{Line: 2}, End: types.LSPInlayPosition{Line: 9}}
	return types.LSPInlaySet{
		ID: "original-set", Digest: "original-digest", ViewID: "private-view", Sequence: 18,
		WireVersion: 17, Range: r, Answer: types.LSPInlayHints, ResolveSupported: true,
		ExpiresAt: time.Date(2030, 1, 2, 3, 4, 5, 0, time.UTC), Detail: "range-scoped evidence",
		Boundary: types.InsightBoundary{
			Source: types.InsightSource{Kind: "canonical", DocumentID: "source-document",
				WorkspaceGeneration: "workspace-generation", Path: "/workspace/source.go",
				Lifetime: "source-lifetime", Revision: 29},
			Context: types.InsightContext{ID: "source-context", Root: "/workspace", Generation: 31,
				UnknownInputs: []string{"unknown-build-input"}},
			Producer: types.InsightProducer{InstanceID: "provider-instance", Provider: "test-provider", ToolIdentity: "tool-identity"},
		},
		Hints: []types.LSPInlayHint{
			{Index: 7, SourceByte: 27, Position: types.LSPInlayPosition{Line: 3, Character: 4},
				Label: "count:", Kind: 2, HasKind: true, LabelIsParts: true, PaddingRight: true,
				HasTextEdits: true, HasData: true, Parts: []types.LSPInlayLabelPart{
					{Value: "count:", HasCommand: true, Location: &types.LSPInlayLocation{
						URI: "command://never-open", Range: r}},
				}},
			{Index: 19, SourceByte: 27, Position: types.LSPInlayPosition{Line: 3, Character: 4},
				Label: "Widget", HasKind: true, Kind: 1},
		},
	}
}

func inspection(r types.LSPInlaySet, ordinal int) types.LSPInlayInspection {
	return types.LSPInlayInspection{
		SetID: r.ID, Digest: r.Digest, Index: r.Hints[ordinal].Index,
		Boundary: r.Boundary, WireVersion: r.WireVersion, Range: r.Range,
		Hint: r.Hints[ordinal], ResolveState: "not-requested",
		OriginalJSON: []byte(`{"label":"count:","opaque":9007199254740993,"decimal":1e+09,"command":{"command":"never.run","arguments":[{"x":2}]},"textEdits":[{"newText":"never insert"}]}`),
		Detail:       "read-only original evidence",
	}
}

func mount(s *Screen, w, h int) *ui.Host {
	host := ui.NewHost(theme.DefaultDark(), s)
	host.Resize(w, h)
	host.Layout()
	host.Focus(s.DefaultFocus())
	return host
}

func paint(host *ui.Host, w, h int) string {
	buf := surface.NewCellBuffer(w, h)
	host.Paint(buf)
	var out strings.Builder
	for y := range h {
		for x := range w {
			char := buf.Get(x, y).Char
			if char == 0 {
				char = ' '
			}
			out.WriteRune(char)
		}
		out.WriteByte('\n')
	}
	return out.String()
}

func key(host *ui.Host, code rune) ui.EventResult {
	return host.Handle(tea.KeyPressMsg{Code: code})
}

func assertInspect(t *testing.T, result ui.EventResult, s *Screen, index int, resolve bool) {
	t.Helper()
	if result.Cmd == nil {
		t.Fatal("missing selected-row inspection intent")
	}
	if got, ok := result.Cmd().(InspectMsg); !ok || got.Target != s || got.Index != index || got.Resolve != resolve {
		t.Fatalf("wrong inspection intent: %#v", got)
	}
}

func assertClose(t *testing.T, result ui.EventResult, s *Screen) {
	t.Helper()
	if result.Cmd == nil {
		t.Fatal("missing Close intent")
	}
	if got, ok := result.Cmd().(CloseMsg); !ok || got.Target != s {
		t.Fatalf("wrong Close owner: %#v", got)
	}
}

func operate(t *testing.T, host *ui.Host, id ui.ComponentID, op ui.ControlOperation) ui.EventResult {
	t.Helper()
	node, err := host.InspectControl(id, control.Target{})
	if err != nil {
		t.Fatal(err)
	}
	result, err := host.Control(id, node.OpenEpoch, &node.Revision, op)
	if err != nil {
		t.Fatal(err)
	}
	return result
}

func TestRenderedNativeJourney(t *testing.T) {
	for _, size := range [][2]int{{100, 24}, {60, 16}} {
		t.Run(fmt.Sprintf("%dx%d", size[0], size[1]), func(t *testing.T) {
			s, r := New(nil), fixture()
			s.SetResult(r)
			host := mount(s, size[0], size[1])
			shown := paint(host, size[0], size[1])
			for _, want := range []string{"count:", "Widget", "Line 4", "Parameter", "Type", "Show details", "Close"} {
				if !strings.Contains(shown, want) {
					t.Fatalf("narrow list lost distinct readable row %q:\n%s", want, shown)
				}
			}
			key(host, tea.KeyTab)
			assertClose(t, key(host, tea.KeyEnter), s)
			key(host, tea.KeyTab)
			key(host, tea.KeyDown)
			assertInspect(t, key(host, tea.KeyEnter), s, 19, false)
			if host.Focused() != ActionsID || s.InList() {
				t.Fatal("inspect did not enter safe-default detail")
			}
			shown = paint(host, size[0], size[1])
			if !strings.Contains(shown, "Widget") || !strings.Contains(shown, "Back") ||
				!strings.Contains(shown, "Load details") || !strings.Contains(shown, "Close") {
				t.Fatalf("real Host did not remount detail/footer:\n%s", shown)
			}
			if got := s.HandleAction(ui.EventContext{}, ui.Action{ID: ActionResolve}); got.Cmd != nil {
				t.Fatal("summary before exact inspection acquired Resolve")
			}
			s.SetInspection(inspection(r, 1))
			host.MarkLayout()
			host.Focus(s.DefaultFocus())
			assertClose(t, key(host, tea.KeyEnter), s)
			key(host, tea.KeyLeft)
			key(host, tea.KeyLeft)
			assertInspect(t, key(host, tea.KeyEnter), s, 19, true)
			if got := s.HandleAction(ui.EventContext{}, ui.Action{ID: ActionResolve}); got.Cmd != nil {
				t.Fatal("busy resolve repeated")
			}
			key(host, tea.KeyEsc)
			if !s.InList() || host.Focused() != ListID || s.list.SelectedIndex() != 1 {
				t.Fatal("Esc did not return to retained list row")
			}
			if got := key(host, tea.KeyEnter); got.Cmd != nil || !s.InList() {
				t.Fatal("busy list activated")
			}
			s.SetBusy(false)
			// Actual Show details button takes the same selected ORIGINAL index.
			key(host, tea.KeyTab)
			key(host, tea.KeyLeft)
			assertInspect(t, key(host, tea.KeyEnter), s, 19, false)
			s.SetInspection(inspection(r, 1))
			host.MarkLayout()
			host.Focus(s.DefaultFocus())
			key(host, tea.KeyLeft) // native Back
			key(host, tea.KeyEnter)
			assertClose(t, key(host, tea.KeyEsc), s)
		})
	}
}

func TestNativePointerAndSemanticPaths(t *testing.T) {
	for _, mode := range []string{"pointer", "semantic"} {
		t.Run(mode, func(t *testing.T) {
			s, r := New(nil), fixture()
			s.SetResult(r)
			host := mount(s, 60, 16)
			var result ui.EventResult
			if mode == "pointer" {
				// Native list's one click path, not a screen-side hit test.
				result = host.Handle(tea.MouseClickMsg{X: 4, Y: 1, Button: tea.MouseLeft})
			} else {
				result = operate(t, host, ListID, ui.ControlOperation{Verb: "activate", ItemID: s.list.Items()[1].ID})
			}
			assertInspect(t, result, s, 19, false)
			s.SetInspection(inspection(r, 1))
			host.MarkLayout()
			paint(host, 60, 16)
			if mode == "pointer" {
				// Shared ActionBar supplies the exact current button bounds.
				node, err := host.InspectControl("inlay/actions/resolve", control.Target{})
				if err != nil {
					t.Fatal(err)
				}
				result = host.Handle(tea.MouseClickMsg{X: node.Bounds.X + 1, Y: node.Bounds.Y, Button: tea.MouseLeft})
			} else {
				result = operate(t, host, "inlay/actions/resolve", ui.ControlOperation{Verb: "activate"})
			}
			assertInspect(t, result, s, 19, true)
			// Unknown executable-looking action IDs remain unhandled.
			for _, id := range []string{"never.run", "command://never-open", "inlay.apply", "inlay.open"} {
				if got := s.HandleAction(ui.EventContext{}, ui.Action{ID: id}); got.Cmd != nil || got.Handled {
					t.Fatalf("inert metadata became an executable action: %s", id)
				}
			}
		})
	}
}

func TestBackEmitsTheExactReaderCancellationIntent(t *testing.T) {
	s := New(nil)
	s.SetResult(fixture())
	host := mount(s, 60, 16)
	assertInspect(t, key(host, tea.KeyEnter), s, 7, false)
	if index, ok := s.SelectedIndex(); !ok || index != 7 || !s.InDetail() {
		t.Fatal("native row did not retain its original selection identity")
	}
	result := key(host, tea.KeyEsc)
	if result.Cmd == nil {
		t.Fatal("Back abandoned the parent reader without a cancellation intent")
	}
	if msg, ok := result.Cmd().(BackMsg); !ok || msg.Target != s || !s.InList() {
		t.Fatal("Back did not identify the exact former reader")
	}
	if _, ok := s.SelectedIndex(); ok || s.InDetail() {
		t.Fatal("Back retained an actionable old detail row")
	}
}

func TestDetachedLiteralEvidenceAndNativeCopy(t *testing.T) {
	r := fixture()
	unsafe := "\x1b]52;c;injected\a\t\r\n\u2028\u2029\"\\"
	r.Hints[0].Tooltip = types.LSPInlayMarkup{Present: true, Form: "markup", Kind: "markdown", Value: unsafe}
	r.Hints[0].Parts[0].Tooltip = types.LSPInlayMarkup{Present: true, Form: "string", Value: unsafe}
	r.Hints[0].Parts[0].Location.URI = unsafe
	s := New(nil)
	s.SetResult(r)
	r.Hints[0].Parts[0].Location.URI = "forged"
	r.Hints[0].Parts[0].Value = "forged"
	r.Boundary.Context.UnknownInputs[0] = "forged"
	host := mount(s, 100, 24)
	assertInspect(t, key(host, tea.KeyEnter), s, 7, false)
	in := inspection(s.result, 0)
	in.ResolvedJSON = []byte("{\r\n\"tooltip\":\"literal\u2028\u2029\",\"opaque\":9007199254740993}")
	in.ResolveState = "resolved-tooltips"
	s.SetInspection(in)
	value := s.text.Value()
	// Mutating any caller-owned pointer/slice after acceptance cannot repaint it.
	in.Hint.Parts[0].Location.URI = "forged-inspection"
	in.Hint.Parts[0].Tooltip.Value = "forged-inspection"
	in.Boundary.Context.UnknownInputs[0] = "forged-inspection"
	in.OriginalJSON[2] = 'X'
	in.ResolvedJSON[2] = 'X'
	for _, want := range []string{"count:", `\x1b]52;c;injected\a`, `\u2028\u2029`, "\t\n"} {
		if !strings.Contains(value, want) {
			t.Fatalf("useful literal content missing %q: %s", want, value)
		}
	}
	for _, forbidden := range []string{"\x1b", "\a", "\r", "\u2028", "\u2029", "forged", "9007199254740993", "never.run", "Source revision:", "unknown-build-input", "provider-instance", "Original raw JSON", "Resolved raw JSON"} {
		if strings.Contains(value, forbidden) {
			t.Fatalf("unsafe, aliased or internal data survived: %q", forbidden)
		}
	}

	host.MarkLayout()
	host.Focus(s.DefaultFocus())
	key(host, tea.KeyTab)
	if host.Focused() != TextID {
		t.Fatal("Tab did not reach native copyable reader")
	}
	var copied string
	s.text.SetClipboardCopy(func(text string) tea.Cmd {
		return func() tea.Msg { copied = text; return nil }
	})
	host.Handle(tea.KeyPressMsg{Code: 'a', Mod: tea.ModCtrl})
	got := host.Handle(tea.KeyPressMsg{Code: 'c', Mod: tea.ModCtrl})
	if got.Cmd == nil || copied != "" {
		t.Fatal("native copy was absent or synchronous")
	}
	got.Cmd()
	if copied != value {
		t.Fatal("copy did not preserve complete accepted literal evidence")
	}
	key(host, tea.KeyEsc) // shared selection-first lifecycle
	if s.InList() {
		t.Fatal("Esc skipped native text-selection cleanup")
	}
	host.Handle(tea.PasteMsg{Content: "mutate"})
	host.Handle(tea.KeyPressMsg{Code: 'x', Text: "x"})
	if s.text.Value() != value {
		t.Fatal("read-only evidence changed")
	}
	key(host, tea.KeyEsc)
	if !s.InList() {
		t.Fatal("detail Esc failed to go Back")
	}
	assertClose(t, key(host, tea.KeyEsc), s)
}

func TestDistinctNonHintStatesAndClearedActions(t *testing.T) {
	for answer, want := range map[string]string{
		types.LSPInlayNull:        "No inlay hints in the visible lines.",
		types.LSPInlayEmpty:       "No inlay hints in the visible lines.",
		types.LSPInlayUnsupported: "does not support inlay hints.",
	} {
		t.Run(answer, func(t *testing.T) {
			s, r := New(nil), fixture()
			r.Answer, r.Hints = answer, nil
			s.SetResult(r)
			host := mount(s, 60, 16)
			shown := paint(host, 60, 16)
			if !strings.Contains(shown, want) || strings.Contains(shown, "unknown-build-input") || s.InList() {
				t.Fatalf("empty or unsupported answer was not readable:\n%s", shown)
			}
			assertClose(t, key(host, tea.KeyEsc), s)
		})
	}
	s := New(nil)
	for _, state := range []string{"loading", "error"} {
		s.SetResult(fixture())
		old := s.list.Items()[0]
		if state == "loading" {
			s.SetLoading()
		} else {
			s.SetError()
		}
		host := mount(s, 60, 16)
		if !strings.Contains(paint(host, 60, 16), "Close") || s.InList() ||
			s.HandleAction(ui.EventContext{}, ui.Action{ID: ActionResolve}).Cmd != nil ||
			s.HandleAction(ui.EventContext{}, ui.Action{ID: ActionInspect, Payload: old}).Cmd != nil {
			t.Fatalf("%s left old actions enabled", state)
		}
		assertClose(t, key(host, tea.KeyEsc), s)
	}
	// An exact source result that does not support resolve is still inspectable.
	r := fixture()
	r.ResolveSupported = false
	s.SetResult(r)
	host := mount(s, 60, 16)
	assertInspect(t, key(host, tea.KeyEnter), s, 7, false)
	s.SetInspection(inspection(r, 0))
	if s.canResolve() || s.HandleAction(ui.EventContext{}, ui.Action{ID: ActionResolve}).Cmd != nil {
		t.Fatal("unsupported provider acquired Resolve")
	}
}

func TestRefuseForgedInspectionAndMalformedDisplay(t *testing.T) {
	for name, change := range map[string]func(*types.LSPInlayInspection){
		"set":       func(in *types.LSPInlayInspection) { in.SetID = "other" },
		"digest":    func(in *types.LSPInlayInspection) { in.Digest = "other" },
		"row":       func(in *types.LSPInlayInspection) { in.Index = 19 },
		"hint row":  func(in *types.LSPInlayInspection) { in.Hint.Index = 19 },
		"source":    func(in *types.LSPInlayInspection) { in.Boundary.Source.Revision++ },
		"provider":  func(in *types.LSPInlayInspection) { in.Boundary.Producer.InstanceID = "other" },
		"context":   func(in *types.LSPInlayInspection) { in.Boundary.Context.Generation++ },
		"unknown":   func(in *types.LSPInlayInspection) { in.Boundary.Context.UnknownInputs = []string{"other"} },
		"wire":      func(in *types.LSPInlayInspection) { in.WireVersion++ },
		"range":     func(in *types.LSPInlayInspection) { in.Range.End.Line++ },
		"geometry":  func(in *types.LSPInlayInspection) { in.Hint.Position.Character++ },
		"command":   func(in *types.LSPInlayInspection) { in.Hint.Parts[0].HasCommand = false },
		"malformed": func(in *types.LSPInlayInspection) { in.OriginalJSON = []byte(`{"bad":`) },
		"raw null":  func(in *types.LSPInlayInspection) { in.OriginalJSON = []byte(`null`) },
		"raw utf8":  func(in *types.LSPInlayInspection) { in.OriginalJSON = []byte("{\"x\":\"\xff\"}") },
		"raw size":  func(in *types.LSPInlayInspection) { in.ResolvedJSON = make([]byte, (1<<20)+1) },
	} {
		t.Run(name, func(t *testing.T) {
			s, r := New(nil), fixture()
			s.SetResult(r)
			host := mount(s, 60, 16)
			key(host, tea.KeyEnter)
			in := inspection(r, 0)
			change(&in)
			s.SetInspection(in)
			if s.hasResult || s.canResolve() || !strings.Contains(s.text.Value(), "Could not load inlay hints") ||
				s.HandleAction(ui.EventContext{}, ui.Action{ID: ActionResolve}).Cmd != nil {
				t.Fatal("forged inspection retained actionable old result")
			}
		})
	}
	for name, change := range map[string]func(*types.LSPInlaySet){
		"count":     func(r *types.LSPInlaySet) { r.Hints = make([]types.LSPInlayHint, 1025) },
		"duplicate": func(r *types.LSPInlaySet) { r.Hints[1].Index = 7 },
		"utf8":      func(r *types.LSPInlaySet) { r.Hints[0].Label = "\xff" },
		"label":     func(r *types.LSPInlaySet) { r.Hints[0].Label = strings.Repeat("x", 4097) },
		"field":     func(r *types.LSPInlaySet) { r.Detail = strings.Repeat("x", (64<<10)+1) },
		"range":     func(r *types.LSPInlaySet) { r.Range.End.Line = -1 },
		"answer":    func(r *types.LSPInlaySet) { r.Answer = "unknown" },
		"null rows": func(r *types.LSPInlaySet) { r.Answer = types.LSPInlayNull },
		"parts":     func(r *types.LSPInlaySet) { r.Hints[0].Parts = make([]types.LSPInlayLabelPart, 33) },
		"markup": func(r *types.LSPInlaySet) {
			r.Hints[0].Tooltip = types.LSPInlayMarkup{Present: true, Form: "html", Value: "invalid"}
		},
		"aggregate": func(r *types.LSPInlaySet) {
			r.Boundary.Context.UnknownInputs = []string{strings.Repeat("\x00", 64000)}
		},
	} {
		t.Run(name, func(t *testing.T) {
			s, r := New(nil), fixture()
			s.SetResult(r)
			old := s.list.Items()[0]
			change(&r)
			s.SetResult(r)
			if s.hasResult || !strings.Contains(s.text.Value(), "Could not load inlay hints") ||
				s.HandleAction(ui.EventContext{}, ui.Action{ID: ActionInspect, Payload: old}).Cmd != nil {
				t.Fatal("malformed result retained old action")
			}
		})
	}
	s := New(nil)
	s.SetResult(fixture())
	old := s.list.Items()[0]
	s.SetResult(fixture())
	for _, payload := range []any{old, ui.ListItem{Payload: 0}, ui.ListItem{Payload: []int{0}}} {
		if s.HandleAction(ui.EventContext{}, ui.Action{ID: ActionInspect, Payload: payload}).Cmd != nil {
			t.Fatal("forged/stale row was accepted")
		}
	}
}

func assertBounds(t *testing.T, component ui.Component, w, h int) {
	t.Helper()
	if parent, ok := component.(ui.Parent); ok {
		for _, child := range parent.Children() {
			r := child.Bounds()
			if r.X < 0 || r.Y < 0 || r.W < 0 || r.H < 0 || r.X+r.W > w || r.Y+r.H > h {
				t.Fatalf("%s escaped %dx%d parent: %+v", child.ID(), w, h, r)
			}
			assertBounds(t, child, r.W, r.H)
		}
	}
}

func TestBoundedRawScrollingGeometryAndThemes(t *testing.T) {
	s, r := New(nil), fixture()
	r.Hints = make([]types.LSPInlayHint, 1024)
	for i := range r.Hints {
		r.Hints[i] = types.LSPInlayHint{Index: i, Label: fmt.Sprintf("hint-%04d", i),
			Position: types.LSPInlayPosition{Line: i}}
	}
	s.SetResult(r)
	host := mount(s, 60, 16)
	before := paint(host, 60, 16)
	key(host, tea.KeyEnd)
	after := paint(host, 60, 16)
	if !strings.Contains(before, "hint-0000") || !strings.Contains(after, "hint-1023") || before == after {
		t.Fatal("native list did not scroll the actual rendered viewport")
	}
	key(host, tea.KeyEnter)
	in := inspection(r, 1023)
	// Exactly 1 MiB each, accepted without retaining the raw backing arrays.
	raw := `{"opaque":"` + strings.Repeat("x", (1<<20)-13) + `"}`
	if len(raw) != 1<<20 {
		t.Fatalf("test input size %d", len(raw))
	}
	in.OriginalJSON, in.ResolvedJSON = []byte(raw), []byte(raw)
	in.Hint.Tooltip = types.LSPInlayMarkup{Present: true, Form: "string", Value: strings.Repeat("Useful explanation for this hint.\n", 50) + "Final useful detail."}
	s.SetInspection(in)
	if !s.inspected || len(s.text.Value()) > 256<<10 || strings.Contains(s.text.Value(), "opaque") || !strings.Contains(s.text.Value(), "Final useful detail.") {
		t.Fatal("long hint details or bounded raw validation were lost")
	}
	host.MarkLayout()
	host.Focus(TextID)
	before = paint(host, 60, 16)
	for range 40 {
		key(host, tea.KeyPgDown)
	}
	after = paint(host, 60, 16)
	if s.text.ScrollOffset() == 0 || before == after || !strings.Contains(after, "Close") || !strings.Contains(after, "Final useful detail.") || s.detailScrollbar.Bounds().W != 1 {
		t.Fatal("native reader did not scroll beneath pinned actions")
	}
	for _, size := range [][2]int{{100, 24}, {60, 16}, {9, 5}, {1, 1}, {60, 16}} {
		host.Resize(size[0], size[1])
		host.Layout()
		assertBounds(t, s, size[0], size[1])
		paint(host, size[0], size[1])
		host.Focus(ActionsID)
		assertClose(t, key(host, tea.KeyEnter), s)
	}
	for _, th := range []*theme.Theme{theme.DefaultLight(), theme.DefaultDark()} {
		s.SetTheme(th)
		host.SetTheme(th)
		buf := surface.NewCellBuffer(60, 16)
		host.Paint(buf)
		if got := buf.Get(0, 0).BG; got != ui.NewTokenSet(th).Color(ui.ColorPopupBg) {
			t.Fatalf("reader did not receive theme tokens: %+v", got)
		}
	}
	s.Layout(ui.LayoutContext{}, ui.Rect{X: 7, Y: 9, W: 9, H: 5})
	assertBounds(t, s, 9, 5)
	s.Layout(ui.LayoutContext{}, ui.Rect{})
	assertBounds(t, s, 0, 0)
	// Direct output cap has an explicit marker and remains valid UTF-8.
	if got := limitText(strings.Repeat("界", 100000), 256<<10); len(got) > 256<<10 ||
		!strings.Contains(got, "Some details are not shown") {
		t.Fatal("aggregate output limit was silent or exceeded")
	}
}
