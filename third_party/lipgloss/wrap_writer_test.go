package lipgloss

import (
	"bytes"
	"errors"
	"io"
	"strings"
	"testing"

	"github.com/charmbracelet/x/ansi"
)

type countedWrites struct {
	bytes.Buffer
	calls int
}

func (b *countedWrites) Write(p []byte) (int, error) {
	b.calls++
	return b.Buffer.Write(p)
}

type shortWriter struct{}

func (shortWriter) Write(p []byte) (int, error) { return len(p) / 2, nil }

func TestWrapWriterForwardsTextRuns(t *testing.T) {
	var out countedWrites
	w := NewWrapWriter(&out)
	line := strings.Repeat("نوشته API ", 100)
	if _, err := w.Write([]byte(line)); err != nil || out.calls != 1 || out.String() != line {
		t.Fatalf("plain write: calls=%d, err=%v", out.calls, err)
	}
	if err := w.Close(); err != nil {
		t.Fatal(err)
	}
	w = NewWrapWriter(shortWriter{})
	if n, err := w.Write([]byte("abcd")); n != 2 || !errors.Is(err, io.ErrShortWrite) {
		t.Errorf("short write: n=%d, err=%v", n, err)
	}
	_ = w.Close()
}

func TestWrapWriterSplitPenAcrossCalls(t *testing.T) {
	const start = "\x1b[1m"
	open := ansi.SetHyperlink("https://example.org", "")
	closeLink := ansi.ResetHyperlink()
	input := start + open + "אב\n文\n" + closeLink + ansi.ResetStyle
	want := start + open + "אב" + ansi.ResetStyle + closeLink + "\n" + open + start +
		"文" + ansi.ResetStyle + closeLink + "\n" + open + start + closeLink + ansi.ResetStyle
	for split := 0; split <= len(input); split++ {
		var out bytes.Buffer
		w := NewWrapWriter(&out)
		for _, part := range []string{input[:split], input[split:]} {
			if _, err := w.Write([]byte(part)); err != nil {
				t.Fatal(err)
			}
		}
		if err := w.Close(); err != nil || out.String() != want {
			t.Fatalf("split %d: got %q, err=%v; want %q", split, out.String(), err, want)
		}
	}
}
