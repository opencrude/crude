package tea

import (
	"bytes"
	"strings"
	"testing"

	"github.com/charmbracelet/x/ansi"
)

func hostBidiRenderer(t *testing.T) (*cursedRenderer, *bytes.Buffer) {
	t.Helper()
	var output bytes.Buffer
	r := newCursedRenderer(&output, nil, 8, 2)
	v := NewView("x")
	v.AltScreen = true
	r.render(v)
	if err := r.flush(false); err != nil {
		t.Fatal(err)
	}
	output.Reset()
	return r, &output
}

func TestHostBidiExplicitModeIsWrittenAndRestored(t *testing.T) {
	r, out := hostBidiRenderer(t)
	r.setHostBidi(true)
	if err := r.flush(false); err != nil {
		t.Fatal(err)
	}
	if got := out.String(); !strings.Contains(got, ansi.ResetModeBiDirectionalSupport+selectCharacterPathLTR) {
		t.Fatalf("explicit mode not written: %q", got)
	}
	out.Reset()
	if err := r.close(); err != nil {
		t.Fatal(err)
	}
	if got := out.String(); !strings.Contains(got, selectCharacterPathDefault+ansi.SetModeBiDirectionalSupport) {
		t.Fatalf("implicit mode not restored on exit: %q", got)
	}
}

func TestHostBidiKeepsTerminalThatStartedExplicit(t *testing.T) {
	r, out := hostBidiRenderer(t)
	r.hostBidiReport(ansi.ModeReset)
	r.setHostBidi(true)
	out.Reset()
	if err := r.close(); err != nil {
		t.Fatal(err)
	}
	if got := out.String(); strings.Contains(got, ansi.SetModeBiDirectionalSupport) {
		t.Fatalf("a terminal that reported explicit mode was switched to implicit: %q", got)
	}
}

func TestHostBidiNeverWritesToPermanentlyImplicitTerminal(t *testing.T) {
	r, out := hostBidiRenderer(t)
	r.hostBidiReport(ansi.ModePermanentlySet)
	r.setHostBidi(true)
	if err := r.flush(false); err != nil {
		t.Fatal(err)
	}
	if err := r.close(); err != nil {
		t.Fatal(err)
	}
	if got := out.String(); strings.Contains(got, "\x1b[8") || strings.Contains(got, " k") {
		t.Fatalf("wrote BiDi modes to a terminal that cannot change them: %q", got)
	}
}

func TestHostBidiGivesModeBack(t *testing.T) {
	r, out := hostBidiRenderer(t)
	r.setHostBidi(true)
	r.setHostBidi(false)
	if err := r.flush(false); err != nil {
		t.Fatal(err)
	}
	got := out.String()
	if !strings.Contains(got, ansi.ResetModeBiDirectionalSupport) || !strings.HasSuffix(strings.TrimRight(got, "\x1b[?25h"), ansi.SetModeBiDirectionalSupport) && !strings.Contains(got, selectCharacterPathDefault+ansi.SetModeBiDirectionalSupport) {
		t.Fatalf("mode not given back: %q", got)
	}
}
