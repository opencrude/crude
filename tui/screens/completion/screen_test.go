package completion

import (
	"fmt"
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"

	"crude/tui/cell"
	"crude/tui/surface"
	"crude/tui/theme"
	"crude/tui/ui"
	"crude/types/control"
)

func fixture() []Entry {
	return []Entry{
		{ID: "original-A", Label: "same", Kind: 3, Detail: "first signature", Documentation: "**literal A** [link](command:never)", DocumentationState: "ready"},
		{ID: "original-B", Label: "same", Kind: 6, Detail: "second signature", Documentation: "independent B documentation", DocumentationState: "ready"},
	}
}

func mount(s *Screen, w, h int) *ui.Host {
	host := ui.NewHost(theme.DefaultDark(), s)
	host.Resize(w, h)
	host.Layout()
	host.Focus(s.DefaultFocus())
	return host
}

func painted(host *ui.Host, w, h int) (*surface.CellBuffer, string) {
	buf := surface.NewCellBuffer(w, h)
	host.Paint(buf)
	var out strings.Builder
	for y := range h {
		for x := range w {
			c := buf.Get(x, y)
			if c.Content != "" {
				out.WriteString(c.Content)
			} else if c.Width == 0 && c.Char == 0 {
				out.WriteByte(' ')
			} else if c.Char != 0 {
				out.WriteRune(c.Char)
			}
		}
		out.WriteByte('\n')
	}
	return buf, out.String()
}

func key(host *ui.Host, code rune) ui.EventResult {
	return host.Handle(tea.KeyPressMsg{Code: code})
}

func expectAction(t *testing.T, result ui.EventResult, kind, item string) {
	t.Helper()
	if !result.Handled || result.Cmd != nil || result.Action == nil ||
		result.Action.ID != kind || result.Action.Payload != (Action{"invocation-original", item}) {
		t.Fatalf("wanted one %s exact-token action for %s, got %+v", kind, item, result)
	}
}

func expectNoIntent(t *testing.T, result ui.EventResult) {
	t.Helper()
	if result.Action != nil || result.Cmd != nil {
		t.Fatalf("unexpected parent intent or side effect: %+v", result)
	}
}

func operate(t *testing.T, host *ui.Host, id ui.ComponentID, op ui.ControlOperation) ui.EventResult {
	t.Helper()
	node, err := host.InspectControl(id, control.Target{})
	if err != nil {
		t.Fatal(err)
	}
	r, err := host.Control(id, node.OpenEpoch, &node.Revision, op)
	if err != nil {
		t.Fatal(err)
	}
	return r
}

func TestPaintGoldenCellBufferContract(t *testing.T) {
	for _, size := range [][2]int{{140, 24}, {60, 22}} {
		t.Run(fmt.Sprint(size), func(t *testing.T) {
			s := New(nil, "invocation-original", fixture(), "source.go · provider-X · context-Y")
			host := mount(s, size[0], size[1])
			_, output := painted(host, size[0], size[1])
			for _, want := range []string{"source.go", "provider-X", "context-Y", "first signature",
				"ƒ same", "v same", "**literal A**", "[link](command:never)", "Ctrl+PgUp/PgDn: docs"} {
				if !strings.Contains(output, want) {
					t.Fatalf("missing literal display %q:\n%s", want, output)
				}
			}
			if strings.Contains(output, "independent B documentation") {
				t.Fatal("nonselected documentation painted")
			}
			expectAction(t, key(host, tea.KeyDown), ActionSelection, "original-B")
			_, output = painted(host, size[0], size[1])
			if !strings.Contains(output, "independent B documentation") || strings.Contains(output, "**literal A**") {
				t.Fatalf("duplicate labels conflated documentation:\n%s", output)
			}
			if size[0] >= 80 {
				if s.text.Bounds().X <= s.list.Bounds().X+s.list.Bounds().W {
					t.Fatal("wide documentation is not adjacent")
				}
			} else if s.text.Bounds().Y < s.list.Bounds().Y+s.list.Bounds().H {
				t.Fatal("narrow documentation overlaps rather than stacks below list")
			}
			if host.Focused() != ListID {
				t.Fatal("selection moved chooser focus")
			}
		})
	}
}

func TestNativeKeyPointerSemanticActivationExactIdentity(t *testing.T) {
	for _, route := range []string{"enter", "tab", "pointer", "semantic", "docs-enter", "root-tab"} {
		t.Run(route, func(t *testing.T) {
			s := New(nil, "invocation-original", fixture(), "inert provenance")
			host := mount(s, 100, 18)
			var r ui.EventResult
			switch route {
			case "enter", "tab":
				expectAction(t, key(host, tea.KeyDown), ActionSelection, "original-B")
				code := rune(tea.KeyEnter)
				if route == "tab" {
					code = tea.KeyTab
				}
				r = key(host, code)
			case "pointer":
				b := s.list.Bounds()
				r = host.Handle(tea.MouseClickMsg{X: b.X + 2, Y: b.Y + 1, Button: tea.MouseLeft})
				expectNoIntent(t, host.Handle(tea.MouseReleaseMsg{X: b.X + 2, Y: b.Y + 1, Button: tea.MouseLeft}))
			case "semantic":
				expectAction(t, operate(t, host, ListID, ui.ControlOperation{Verb: "select", ItemID: "original-B"}),
					ActionSelection, "original-B")
				r = operate(t, host, ListID, ui.ControlOperation{Verb: "activate", ItemID: "original-B"})
			case "docs-enter":
				host.Focus(TextID)
				expectAction(t, key(host, tea.KeyDown), ActionSelection, "original-B")
				r = key(host, tea.KeyEnter)
			case "root-tab":
				host.Focus(ScreenID)
				expectAction(t, key(host, tea.KeyDown), ActionSelection, "original-B")
				r = key(host, tea.KeyTab)
			}
			expectAction(t, r, ActionAccept, "original-B")
			_, output := painted(host, 100, 18)
			if s.SelectedID() != "original-B" || !strings.Contains(output, "independent B documentation") ||
				strings.Contains(output, "**literal A**") {
				t.Fatalf("%s activation painted another row's docs:\n%s", route, output)
			}
			expectAction(t, key(host, tea.KeyEsc), ActionClose, "original-B")
		})
	}
	// Native semantic activation can select and activate an unselected original
	// row in one operation; it must not emit Selection plus Accept.
	s := New(nil, "invocation-original", fixture(), "")
	host := mount(s, 100, 18)
	expectAction(t, operate(t, host, ListID, ui.ControlOperation{Verb: "activate", ItemID: "original-B"}),
		ActionAccept, "original-B")
	_, output := painted(host, 100, 18)
	if !strings.Contains(output, "independent B documentation") {
		t.Fatal("semantic activation left the previous row's documentation")
	}
}

func TestDetachedDocumentationDeliveryAndHonestStates(t *testing.T) {
	entries := fixture()
	s := New(nil, "invocation-original", entries, "source-original")
	entries[0] = Entry{ID: "mutated", Documentation: "caller changed"}
	host := mount(s, 110, 20)
	if s.SelectedID() != "original-A" || !strings.Contains(s.text.Value(), "**literal A**") {
		t.Fatal("caller changed retained entries")
	}
	original := s.text.Value()
	for _, bad := range [][2]string{{"foreign", "original-A"}, {"invocation-original", "missing"}} {
		if s.SetDocumentation(bad[0], bad[1], "forged detail", "forged docs", "ready") {
			t.Fatal("stale/foreign identity accepted")
		}
		if s.text.Value() != original {
			t.Fatal("rejected result changed displayed documentation")
		}
	}
	expectAction(t, key(host, tea.KeyDown), ActionSelection, "original-B")
	current := s.text.Value()
	if !s.SetDocumentation("invocation-original", "original-A", "cached signature", "cached original-A", "ready") ||
		s.text.Value() != current {
		t.Fatal("nonselected delivery replaced selected docs or was not retained")
	}
	expectAction(t, key(host, tea.KeyUp), ActionSelection, "original-A")
	_, output := painted(host, 110, 20)
	if !strings.Contains(output, "cached original-A") || !strings.Contains(output, "cached signature") {
		t.Fatalf("original-row cached result missing:\n%s", output)
	}
	for _, state := range []string{"loading", "unavailable", "failed", "ready", "custom state: no authority"} {
		if !s.SetDocumentation("invocation-original", "original-A", "", "", state) {
			t.Fatal("current identity refused")
		}
		host.MarkPaint() // external deliveries are parent-owned invalidations
		_, output = painted(host, 110, 20)
		if !strings.Contains(output, "Documentation: "+state) || !strings.Contains(output, "No documentation supplied.") ||
			strings.Contains(output, "cached original-A") {
			t.Fatalf("state fabricated evidence or retained obsolete docs:\n%s", output)
		}
	}
	empty := New(nil, "invocation-original", nil, "empty source")
	emptyHost := mount(empty, 100, 14)
	_, output = painted(emptyHost, 100, 14)
	if !strings.Contains(output, "No completion selected.") || !strings.Contains(output, "empty source") {
		t.Fatal("empty state hidden")
	}
	expectNoIntent(t, key(emptyHost, tea.KeyEnter))
	expectNoIntent(t, key(emptyHost, tea.KeyTab))
	expectAction(t, key(emptyHost, tea.KeyEsc), ActionClose, "")
}

func TestDocumentationNativeScrollingSelectionCopyAndTyping(t *testing.T) {
	rows := fixture()
	rows[0].Documentation = strings.Repeat("literal long documentation row\n", 80) + "final sentinel"
	s := New(nil, "invocation-original", rows, "source")
	host := mount(s, 100, 14)
	_, before := painted(host, 100, 14)
	expectNoIntent(t, host.Handle(tea.KeyPressMsg{Code: tea.KeyPgDown, Mod: tea.ModCtrl}))
	expectNoIntent(t, host.Handle(tea.KeyPressMsg{Code: tea.KeyPgDown, Mod: tea.ModCtrl}))
	if s.text.ScrollOffset() <= 0 || host.Focused() != ListID || s.SelectedID() != "original-A" {
		t.Fatal("Ctrl+PgDown failed native docs scroll or stole chooser selection/focus")
	}
	_, after := painted(host, 100, 14)
	if before == after {
		t.Fatal("docs keyboard scrolling did not change rendered output")
	}
	oldOffset := s.text.ScrollOffset()
	expectNoIntent(t, host.Handle(tea.KeyPressMsg{Code: tea.KeyPgUp, Mod: tea.ModCtrl}))
	expectNoIntent(t, host.Handle(tea.KeyPressMsg{Code: tea.KeyPgUp, Mod: tea.ModCtrl}))
	if s.text.ScrollOffset() >= oldOffset {
		t.Fatal("Ctrl+PgUp did not scroll documentation back")
	}
	b := s.text.Bounds()
	expectNoIntent(t, host.Handle(tea.MouseWheelMsg{X: b.X + 2, Y: b.Y + 2, Button: tea.MouseWheelDown}))
	if s.text.ScrollOffset() <= 0 || s.SelectedID() != "original-A" || host.Focused() != ListID {
		t.Fatal("docs wheel activated list or failed to scroll independently")
	}
	scroll := s.text.ScrollOffset()
	for range 3 {
		host.SetFrame(900)
		host.MarkPaint()
		painted(host, 100, 14)
		host.Handle(tea.WindowSizeMsg{Width: 100, Height: 14})
	}
	if s.text.ScrollOffset() != scroll {
		t.Fatal("paint/tick rebuilt documentation or snapped native scrolling")
	}
	// Real docs pointer capture may traverse list cells, but must never activate.
	var copied string
	s.text.SetClipboardCopy(func(value string) tea.Cmd {
		return func() tea.Msg { copied = value; return nil }
	})
	s.text.SetScrollOffset(0)
	host.MarkPaint()
	painted(host, 100, 14)
	expectNoIntent(t, host.Handle(tea.MouseClickMsg{X: b.X, Y: b.Y, Button: tea.MouseLeft}))
	if !host.HasPointerCapture() {
		t.Fatal("native TextArea did not retain selection capture")
	}
	expectNoIntent(t, host.Handle(tea.MouseMotionMsg{X: b.X + 3, Y: b.Y, Button: tea.MouseLeft}))
	release := host.Handle(tea.MouseReleaseMsg{X: b.X + 3, Y: b.Y, Button: tea.MouseLeft})
	if release.Action != nil || release.Cmd == nil || host.HasPointerCapture() {
		t.Fatal("docs selection release activated a row, lost copy or retained capture")
	}
	if copied != "" {
		t.Fatal("copy ran synchronously")
	}
	release.Cmd()
	if copied == "" {
		t.Fatal("native read-only drag did not copy")
	}
	value := s.text.Value()
	for _, focus := range []ui.ComponentID{ListID, TextID, ScreenID} {
		host.Focus(focus)
		for _, k := range []tea.KeyPressMsg{{Code: 'x', Text: "x"}, {Code: '界', Text: "界"}, {Code: 'A', Text: "A", Mod: tea.ModShift}} {
			r := host.Handle(k)
			expectNoIntent(t, r)
			if r.Handled {
				t.Fatalf("normal typing must remain unhandled for parent close+forward: %+v", k)
			}
		}
	}
	host.Focus(TextID)
	host.Handle(tea.PasteMsg{Content: "must not mutate"})
	host.Handle(tea.KeyPressMsg{Code: tea.KeyBackspace})
	host.Handle(tea.KeyPressMsg{Code: 'z', Mod: tea.ModCtrl})
	if s.text.Value() != value {
		t.Fatal("native docs were editable")
	}
	host.Handle(tea.KeyPressMsg{Code: 'a', Mod: tea.ModCtrl})
	copyResult := host.Handle(tea.KeyPressMsg{Code: 'c', Mod: tea.ModCtrl})
	if copyResult.Cmd == nil || copyResult.Action != nil {
		t.Fatal("native keyboard copy missing")
	}
	copyResult.Cmd()
	if copied != value {
		t.Fatal("native keyboard copy omitted complete docs")
	}
	expectAction(t, key(host, tea.KeyEsc), ActionClose, "original-A")
}

func TestNativeListViewportAndInvalidIdentity(t *testing.T) {
	rows := make([]Entry, 80)
	for i := range rows {
		rows[i] = Entry{ID: fmt.Sprintf("row-%02d", i), Label: fmt.Sprintf("choice-%02d", i),
			Documentation: fmt.Sprintf("documentation-%02d", i)}
	}
	s := New(nil, "invocation-original", rows, "source")
	host := mount(s, 100, 12)
	_, output := painted(host, 100, 12)
	if strings.Contains(output, "choice-79") || s.scrollbar.Bounds().W != 1 {
		t.Fatal("list did not use bounded native viewport/scrollbar")
	}
	expectAction(t, key(host, tea.KeyEnd), ActionSelection, "row-79")
	_, output = painted(host, 100, 12)
	if !strings.Contains(output, "choice-79") || !strings.Contains(output, "documentation-79") ||
		s.list.Scroll().Offset() == 0 {
		t.Fatal("native End did not reveal final row and its own documentation")
	}
	expectAction(t, key(host, tea.KeyPgUp), ActionSelection, "row-69")
	expectAction(t, key(host, tea.KeyHome), ActionSelection, "row-00")
	old := s.list.Scroll().Offset()
	b := s.list.Bounds()
	expectNoIntent(t, host.Handle(tea.MouseWheelMsg{X: b.X + 2, Y: b.Y + 2, Button: tea.MouseWheelDown}))
	if s.list.Scroll().Offset() <= old || s.SelectedID() != "row-00" {
		t.Fatal("list wheel did not retain native independent scroll/selection")
	}
	for _, op := range []ui.ControlOperation{{Verb: "activate", ItemID: "foreign"}, {Verb: "select", ItemID: "foreign"}} {
		node, err := host.InspectControl(ListID, control.Target{})
		if err != nil {
			t.Fatal(err)
		}
		r, err := host.Control(ListID, node.OpenEpoch, &node.Revision, op)
		if err == nil || r.Action != nil {
			t.Fatal("native semantic operation accepted a missing original ID")
		}
	}
	invalid := New(nil, "invocation-original", []Entry{
		{ID: "", Label: "empty"}, {ID: "duplicate", Label: "one"}, {ID: "duplicate", Label: "two"},
	}, "")
	invalidHost := mount(invalid, 100, 14)
	if invalid.SelectedID() != "" || invalid.SetDocumentation("invocation-original", "duplicate", "", "forged", "ready") {
		t.Fatal("ambiguous identity acquired selection or result authority")
	}
	expectNoIntent(t, key(invalidHost, tea.KeyEnter))
	foreign := ui.ListItem{ID: "row-00", Payload: Action{"foreign", "row-00"}}
	expectNoIntent(t, s.HandleAction(ui.EventContext{}, ui.Action{ID: rowActivate, Payload: foreign}))
}

type insetParent struct {
	ui.Base
	child *Screen
}

func (p *insetParent) Children() []ui.Component { return []ui.Component{p.child} }
func (p *insetParent) Layout(ctx ui.LayoutContext, r ui.Rect) {
	p.SetBounds(r)
	p.child.Layout(ctx, ui.Rect{X: 3, Y: 2, W: max(0, r.W-6), H: max(0, r.H-4)})
}

func assertBounds(t *testing.T, c ui.Component) {
	t.Helper()
	if p, ok := c.(ui.Parent); ok {
		for _, child := range p.Children() {
			r := child.Bounds()
			b := c.Bounds()
			if r.X < 0 || r.Y < 0 || r.W < 0 || r.H < 0 || r.X+r.W > b.W || r.Y+r.H > b.H {
				t.Fatalf("%s bounds %+v escape parent %s %+v", child.ID(), r, c.ID(), b)
			}
			assertBounds(t, child)
		}
	}
}

func TestParentLocalTinyGeometryThemeUnicodeAndLiteralControls(t *testing.T) {
	for _, size := range [][2]int{{160, 40}, {80, 24}, {60, 18}, {12, 8}, {7, 5}, {6, 4}, {1, 1}, {0, 0}} {
		t.Run(fmt.Sprint(size), func(t *testing.T) {
			s := New(nil, "invocation-original", fixture(), "source")
			host := mount(s, size[0], size[1])
			assertBounds(t, s)
			painted(host, size[0], size[1])
		})
	}
	rows := []Entry{{ID: "unicode-id", Label: "界 é 👩‍💻", Documentation: "Unicode 界 é 👩‍💻\n[execute](command:inert)\n```sh\nnever-run\n```\n\u001b[31m"}}
	s := New(nil, "invocation-original", rows, "source\nevidence")
	parent := &insetParent{Base: ui.NewBase("container"), child: s}
	host := ui.NewHost(theme.DefaultDark(), parent)
	host.Resize(112, 24)
	host.Layout()
	host.Focus(ListID)
	assertBounds(t, parent)
	buf, output := painted(host, 112, 24)
	if !strings.Contains(output, "Unicode") || !strings.Contains(output, "[execute](command:inert)") ||
		!strings.Contains(output, "\\x1b[31m") || !strings.Contains(output, "source\\nevidence") {
		t.Fatalf("literal Unicode/control display lost:\n%s", output)
	}
	if !strings.Contains(s.text.Value(), "界 é 👩‍💻") || strings.Contains(s.text.Value(), "\u001b") {
		t.Fatal("Unicode graphemes lost or terminal control retained")
	}
	// Parent-local paint must not touch even a cell outside the actual slot.
	for y := range 24 {
		for x := range 112 {
			if x < 3 || x >= 109 || y < 2 || y >= 22 {
				c := buf.Get(x, y)
				if c.Char != 0 && c.Char != ' ' {
					t.Fatalf("paint escaped parent at %d,%d: %+v", x, y, c)
				}
			}
		}
	}
	beforeID, beforeValue := s.SelectedID(), s.text.Value()
	for _, th := range []*theme.Theme{theme.DefaultLight(), theme.DefaultHighContrast(), theme.DefaultDark()} {
		host.SetTheme(th)
		buf, _ = painted(host, 112, 24)
		if s.SelectedID() != beforeID || s.text.Value() != beforeValue {
			t.Fatal("theme changed selection or original documentation")
		}
		want := ui.NewTokenSet(th).Color(ui.ColorPopupBg)
		if got := buf.Get(3, 2).BG; got != want {
			t.Fatalf("theme popup fill not propagated: got %+v want %+v", got, want)
		}
		_, selectedBg := ui.NewTokenSet(th).ListRowColors(ui.StateFocused)
		found := false
		for x := 3; x < 40; x++ {
			if buf.Get(x, 3).BG == selectedBg {
				found = true
			}
		}
		if !found || selectedBg == cell.NoColor {
			t.Fatal("native selected row lost theme ink")
		}
	}
}
