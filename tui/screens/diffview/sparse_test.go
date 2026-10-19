package diffview

import (
	"context"
	"errors"
	"reflect"
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"

	"crude/tui/surface"
)

func TestSparseCheckpointsPreserveSourceBoundaries(t *testing.T) {
	// Expected columns/bytes come from an explicit fixture oracle, not the
	// traversal implementation being tested. Repetition crosses every checkpoint.
	clusters := []struct {
		raw, display string
		width        int
	}{{"a", "a", 1}, {"界", "界", 2}, {"e\u0301", "e\u0301", 1}, {"👩‍💻", "👩‍💻", 2}, {"🇺🇸", "🇺🇸", 2}, {"\t", "", 0}, {"\x1b", "�", 1}, {"\r", "�", 1}}
	var input strings.Builder
	var expected []SourceUnit
	column := 0
	for range 1000 {
		for _, cluster := range clusters {
			width, display := cluster.width, cluster.display
			if cluster.raw == "\t" {
				width, display = 4-column%4, "    "[:4-column%4]
			}
			expected = append(expected, SourceUnit{Raw: cluster.raw, Display: display, Offset: input.Len(), Column: column, Width: width})
			input.WriteString(cluster.raw)
			column += width
		}
	}
	input.WriteString("\r\n")
	source, err := PrepareSourceLinesContext(t.Context(), []string{input.String()})
	if err != nil {
		t.Fatal(err)
	}
	if source.Width() != column || len(source.owner.geometry.indexes[1].points) < 10 {
		t.Fatalf("width=%d checkpoints=%d", source.Width(), len(source.owner.geometry.indexes[1].points))
	}
	for from := 0; from <= column+1; from += 37 {
		to := from + 23
		var want, got []SourceUnit
		for _, unit := range expected {
			if unit.Column+unit.Width > from && unit.Column < to {
				want = append(want, unit)
			}
		}
		for unit := range source.Range(1, from, to) {
			got = append(got, unit)
		}
		if !reflect.DeepEqual(got, want) {
			t.Fatalf("columns %d..%d: got=%+v want=%+v", from, to, got, want)
		}
	}
}

func TestSparseFarRightFindSelectionAndCopy(t *testing.T) {
	prefix := strings.Repeat("x", 8192)
	s := New(nil)
	s.SetPrepared(Prepare(Comparison{After: prefix + "\t界é👩‍💻\x1b tail-marker\r\n"}))
	h := host(s, 120, 12)
	s.SetQuery("tail-marker")
	settleSearch(t, s)
	buf := surface.NewCellBuffer(120, 12)
	h.Paint(buf)
	if !strings.Contains(text(buf), "tail-marker") || s.after.horizontal.Offset() < 8000 {
		t.Fatalf("far-right Find did not reveal source:\n%s", text(buf))
	}
	s.after.selected = true
	s.after.start, s.after.end = point{column: 8192}, point{column: 8202}
	if got := s.after.SelectedText(); got != "\t界é👩‍💻\x1b" {
		t.Fatalf("far-right exact source copy=%q", got)
	}
	if got := s.after.snap(point{column: 8197}); got.column != 8196 {
		t.Fatalf("wide grapheme landing=%+v", got)
	}
}

func TestGeometryReflowRejectsSupersededAndCancelledResults(t *testing.T) {
	source, err := PrepareSourceLinesContext(t.Context(), []string{strings.Repeat("☰", 4096)})
	if err != nil {
		t.Fatal(err)
	}
	old := source.ReflowCommand(t.Context())()
	ansi.SetTerminalWidthProfile(ansi.TerminalWidthProfile{Exact: map[string]int{"☰": 1}})
	t.Cleanup(func() { ansi.SetTerminalWidthProfile(ansi.TerminalWidthProfile{}) })
	current := source.ReflowCommand(t.Context())().(SourceGeometryReadyMsg)
	if applied, err := old.(SourceGeometryReadyMsg).Apply(); applied || err != nil {
		t.Fatal("superseded profile published")
	}
	if applied, err := current.Apply(); !applied || err != nil || source.Width() != 4096 {
		t.Fatalf("current geometry: applied=%t err=%v width=%d", applied, err, source.Width())
	}
	if applied, _ := current.Apply(); applied {
		t.Fatal("completion applied twice")
	}
	ctx, cancel := context.WithCancel(t.Context())
	command := source.ReflowCommand(ctx)
	cancel()
	if applied, err := command().(SourceGeometryReadyMsg).Apply(); applied || !errors.Is(err, context.Canceled) || source.Width() != 4096 || !strings.Contains(source.GeometryStatus(), "unavailable") {
		t.Fatalf("cancelled geometry: applied=%t err=%v status=%q", applied, err, source.GeometryStatus())
	}
}

func TestCalibrationDoesNotPaintMixedCheckpointColumns(t *testing.T) {
	s := New(nil)
	s.SetPrepared(Prepare(Comparison{After: strings.Repeat("☰", 4096) + " END"}))
	h := host(s, 100, 12)
	ansi.SetTerminalWidthProfile(ansi.TerminalWidthProfile{Exact: map[string]int{"☰": 1}})
	t.Cleanup(func() { ansi.SetTerminalWidthProfile(ansi.TerminalWidthProfile{}) })
	s.BeginGeometryReflow()
	var commands []tea.Cmd
	// Admit both before executing either, matching the application command owner.
	for _, source := range s.SourceGeometry() {
		commands = append(commands, source.ReflowCommand(t.Context()))
	}
	buf := surface.NewCellBuffer(100, 12)
	h.Paint(buf)
	if !strings.Contains(text(buf), "Calibrating comparison") || strings.Contains(text(buf), "☰") {
		t.Fatalf("mixed geometry painted while pending:\n%s", text(buf))
	}
	for _, command := range commands {
		if applied, err := command().(SourceGeometryReadyMsg).Apply(); !applied || err != nil {
			t.Fatalf("geometry: applied=%t err=%v", applied, err)
		}
	}
	s.InvalidateWidthProfile()
	h.MarkLayout()
	h.Layout()
	s.SetQuery("END")
	settleSearch(t, s)
	h.Paint(buf)
	if s.after.horizontal.Content() != 4100 || !strings.Contains(text(buf), "END") || strings.Contains(text(buf), "Calibrating") {
		t.Fatalf("calibrated far-right source not painted:\n%s", text(buf))
	}
}
