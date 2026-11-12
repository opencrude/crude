package inlay

import (
	"fmt"
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
	"crude/tui/ui"
	"crude/types"
	"github.com/charmbracelet/x/ansi"
)

func TestInlayReaderShowsUsefulDetailsWithoutInternalEvidence(t *testing.T) {
	for _, size := range [][2]int{{100, 24}, {60, 16}} {
		t.Run(fmt.Sprintf("%dx%d", size[0], size[1]), func(t *testing.T) {
			r := fixture()
			r.Hints[0].Tooltip = types.LSPInlayMarkup{Present: true, Form: "string", Value: "Number of items to process."}
			r.Hints[0].Parts[0].Tooltip = types.LSPInlayMarkup{Present: true, Form: "markup", Kind: "plaintext", Value: "The item count."}
			s := New(nil)
			s.SetResult(r)
			host := mount(s, size[0], size[1])
			list := paint(host, size[0], size[1])
			for _, want := range []string{"count:", "Widget", "Line 4"} {
				if !strings.Contains(list, want) {
					t.Fatalf("list missing %q:\n%s", want, list)
				}
			}
			for _, bad := range []string{"#7", "#19", "3:4"} {
				if strings.Contains(list, bad) {
					t.Fatalf("list exposed %q:\n%s", bad, list)
				}
			}
			assertInspect(t, key(host, tea.KeyEnter), s, 7, false)
			s.SetInspection(inspection(r, 0))
			host.MarkLayout()
			detail := s.text.Value()
			for _, want := range []string{"count:", "Number of items to process.", "The item count."} {
				if !strings.Contains(detail, want) {
					t.Fatalf("hint detail missing %q: %s", want, detail)
				}
			}
			for _, bad := range []string{"Source byte:", "zero-based UTF-16", "Original raw JSON", "Resolved raw JSON", "UnknownInputs", "provider-instance", "opaque data", "Set ID:"} {
				if strings.Contains(detail, bad) {
					t.Fatalf("hint detail exposed %q: %s", bad, detail)
				}
			}
			if !strings.Contains(paint(host, size[0], size[1]), "Number of items to process.") {
				t.Fatal("helpful text was not painted")
			}
			if got := key(host, tea.KeyEsc); got.Cmd == nil || !s.InList() {
				t.Fatal("Escape did not return to the list")
			}
		})
	}
}

func TestInlayReaderEmptyAndFailureStatesHaveUsefulMessages(t *testing.T) {
	for _, answer := range []string{types.LSPInlayNull, types.LSPInlayEmpty, types.LSPInlayUnsupported} {
		t.Run(answer, func(t *testing.T) {
			r := fixture()
			r.Answer = answer
			r.Hints = nil
			s := New(nil)
			s.SetResult(r)
			host := mount(s, 60, 16)
			shown := paint(host, 60, 16)
			if answer == types.LSPInlayUnsupported {
				if !strings.Contains(shown, "does not support inlay hints") {
					t.Fatal(shown)
				}
			} else if !strings.Contains(shown, "No inlay hints") {
				t.Fatal(shown)
			}
			for _, bad := range []string{"Provider returned", "UnknownInputs", "Set ID:", "[]"} {
				if strings.Contains(shown, bad) {
					t.Fatalf("empty result exposed %q: %s", bad, shown)
				}
			}
			assertClose(t, key(host, tea.KeyEsc), s)
		})
	}
	s := New(nil)
	s.SetError()
	host := mount(s, 60, 16)
	shown := paint(host, 60, 16)
	if !strings.Contains(shown, "Could not load inlay hints") || !strings.Contains(shown, "Refresh Inlay Hints") || strings.Contains(shown, "private transport token") {
		t.Fatal("failure must give a useful next step without internal details", shown)
	}
}

func TestInlayReaderSizesForContentAndMakesOverflowReachable(t *testing.T) {
	s := New(nil)
	r := fixture()
	r.Hints = r.Hints[:1]
	s.SetResult(r)
	small := s.Measure(ui.LayoutContext{}, ui.Constraints{MaxW: 70, MaxH: 18})
	if small.W >= 70 || small.H >= 18 {
		t.Fatalf("one short hint opened a maximum-sized panel: %+v", small)
	}
	host := mount(s, 60, 8)
	assertInspect(t, key(host, tea.KeyEnter), s, 7, false)
	in := inspection(r, 0)
	in.Hint.Tooltip = types.LSPInlayMarkup{Present: true, Form: "string", Value: strings.Repeat("More useful information.\n", 30) + "Final useful detail."}
	s.SetInspection(in)
	host.MarkLayout()
	host.Focus(TextID)
	before := paint(host, 60, 8)
	for range 12 {
		key(host, tea.KeyPgDown)
	}
	after := paint(host, 60, 8)
	if before == after || !strings.Contains(after, "Final useful detail.") || !strings.Contains(after, "Close") {
		t.Fatalf("reader did not scroll to the end below pinned actions: %s", after)
	}
}

func TestInlayReaderScrollbarAndWheelShareNativeViewport(t *testing.T) {
	r := fixture()
	r.Hints = r.Hints[:1]
	s := New(nil)
	s.SetResult(r)
	host := mount(s, 60, 8)
	assertInspect(t, key(host, tea.KeyEnter), s, 7, false)
	in := inspection(r, 0)
	in.Hint.Tooltip = types.LSPInlayMarkup{Present: true, Form: "string", Value: strings.Repeat("Explanatory line.\n", 50) + "END OF HINT"}
	s.SetInspection(in)
	host.MarkLayout()
	host.Layout()
	host.Focus(TextID)
	bar, ok := host.ScreenRect("inlay/detail-scrollbar")
	if !ok || bar.W != 1 || bar.H < 2 {
		t.Fatal("overflow did not mount a visible native scrollbar", bar)
	}
	host.Handle(tea.MouseWheelMsg{X: 1, Y: 1, Button: tea.MouseWheelDown})
	if s.text.ScrollOffset() == 0 {
		t.Fatal("wheel did not move the shared reader")
	}
	s.text.SetScrollOffset(0)
	host.MarkLayout()
	host.Layout()
	host.Handle(tea.MouseClickMsg{X: bar.X, Y: bar.Y, Button: tea.MouseLeft})
	host.Handle(tea.MouseMotionMsg{X: bar.X, Y: bar.Y + bar.H - 1, Button: tea.MouseLeft})
	host.Handle(tea.MouseReleaseMsg{X: bar.X, Y: bar.Y + bar.H - 1, Button: tea.MouseLeft})
	if host.HasPointerCapture() || !strings.Contains(paint(host, 60, 8), "END OF HINT") {
		t.Fatal("scrollbar drag did not reach the final hint text")
	}
}

func TestInlayLabelsEscapeControlsAndUnsupportedResolveStaysAbsent(t *testing.T) {
	r := fixture()
	r.Hints = r.Hints[:1]
	r.ResolveSupported = false
	r.Hints[0].Label = "safe\x1b]52;c;bad\a\nlabel"
	r.Hints[0].LabelIsParts = false
	r.Hints[0].Parts = nil
	s := New(nil)
	s.SetResult(r)
	host := mount(s, 60, 16)
	shown := paint(host, 60, 16)
	if strings.Contains(shown, "\x1b") || strings.Contains(shown, "\a") || !strings.Contains(shown, `\x1b]52;c;bad\a\nlabel`) {
		t.Fatal("unsafe label was not rendered as literal text", shown)
	}
	assertInspect(t, key(host, tea.KeyEnter), s, 7, false)
	s.SetInspection(inspection(r, 0))
	host.MarkLayout()
	shown = paint(host, 60, 16)
	if strings.Contains(shown, "Load details") || !strings.Contains(shown, "No details are available.") {
		t.Fatal("unsupported resolve was offered or not explained", shown)
	}
}

// Warm measure and layout follow the real popup's own chosen dimensions. This
// measures the reader only; it does not claim terminal presentation latency.
func BenchmarkInlayReaderStableLayout(b *testing.B) {
	for _, tc := range []struct {
		name  string
		lines int
	}{{"short", 1}, {"long", 1200}} {
		b.Run(tc.name, func(b *testing.B) {
			r := fixture()
			r.Hints = r.Hints[:1]
			s := New(nil)
			s.SetResult(r)
			host := mount(s, 60, 8)
			key(host, tea.KeyEnter)
			in := inspection(r, 0)
			in.Hint.Tooltip = types.LSPInlayMarkup{Present: true, Form: "string", Value: strings.Repeat("Explanatory line for the item.\n", tc.lines)}
			s.SetInspection(in)
			host.MarkLayout()
			host.Layout()
			cs := ui.Constraints{MaxW: 60, MaxH: 8}
			size := s.Measure(ui.LayoutContext{}, cs)
			rect := ui.Rect{W: size.W, H: size.H}
			s.Layout(ui.LayoutContext{}, rect)
			if !s.InDetail() || !s.inspected || (tc.lines > 1 && s.detailScrollbar.Bounds().W != 1) {
				b.Fatal("benchmark did not mount the intended detail and overflow")
			}
			b.ReportAllocs()
			b.ResetTimer()
			for range b.N {
				s.Measure(ui.LayoutContext{}, cs)
				s.Layout(ui.LayoutContext{}, rect)
			}
		})
	}
}

// Calibration may arrive after the popup has opened. Its list and detail must
// both grow for a wider measured glyph without changing selected evidence.
func TestInlayReaderFollowsTerminalWidthProfile(t *testing.T) {
	ansi.SetTerminalWidthProfile(ansi.TerminalWidthProfile{Exact: map[string]int{"☰": 1}})
	t.Cleanup(func() { ansi.SetTerminalWidthProfile(ansi.TerminalWidthProfile{}) })
	r := fixture()
	r.Hints = r.Hints[:1]
	r.Hints[0].Label = strings.Repeat("☰", 24)
	r.Hints[0].LabelIsParts = false
	r.Hints[0].Parts = nil
	s := New(nil)
	s.SetResult(r)
	cs := ui.Constraints{MaxW: 70, MaxH: 18}
	beforeList := s.Measure(ui.LayoutContext{}, cs)
	host := mount(s, 70, 18)
	assertInspect(t, key(host, tea.KeyEnter), s, 7, false)
	s.SetInspection(inspection(r, 0))
	beforeDetail := s.Measure(ui.LayoutContext{}, cs)
	ansi.SetTerminalWidthProfile(ansi.TerminalWidthProfile{Exact: map[string]int{"☰": 2}})
	afterDetail := s.Measure(ui.LayoutContext{}, cs)
	if afterDetail.W <= beforeDetail.W {
		t.Fatalf("detail width did not follow terminal: before=%+v after=%+v", beforeDetail, afterDetail)
	}
	key(host, tea.KeyEsc)
	afterList := s.Measure(ui.LayoutContext{}, cs)
	if afterList.W <= beforeList.W {
		t.Fatalf("list width did not follow terminal: before=%+v after=%+v", beforeList, afterList)
	}
	if got, ok := s.list.Selected(); !ok || got.Payload != 0 {
		t.Fatal("calibration changed the selected original hint")
	}
}

// Provider text is untrusted: directional controls must not reorder the label
// or tooltip, while normal complex graphemes must remain useful and copyable.
func TestInlayReaderEscapesBidiControlsWithoutBreakingEmoji(t *testing.T) {
	r := fixture()
	r.Hints = r.Hints[:1]
	r.Hints[0].Label = "name\u202eX"
	r.Hints[0].LabelIsParts = false
	r.Hints[0].Parts = nil
	r.Hints[0].Tooltip = types.LSPInlayMarkup{Present: true, Form: "string", Value: "help \u2066safe\u2069 👩‍💻"}
	s := New(nil)
	s.SetResult(r)
	host := mount(s, 80, 20)
	if got := s.list.Items()[0].Label; got != `name\u202eX` {
		t.Fatalf("label retained a directional control: %q", got)
	}
	assertInspect(t, key(host, tea.KeyEnter), s, 7, false)
	s.SetInspection(inspection(r, 0))
	if got := s.text.Value(); !strings.Contains(got, `name\u202eX`) || !strings.Contains(got, `help \u2066safe\u2069 👩‍💻`) || strings.ContainsAny(got, "\u202e\u2066\u2069") {
		t.Fatalf("unsafe or damaged literal detail: %q", got)
	}
}
