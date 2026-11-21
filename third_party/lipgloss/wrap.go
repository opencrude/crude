package lipgloss

import (
	"bytes"
	"io"

	uv "github.com/charmbracelet/ultraviolet"
	"github.com/charmbracelet/x/ansi"
)

// Wrap wraps the given string to the given width, preserving ANSI styles and links.
func Wrap(s string, width int, breakpoints string) string {
	var buf bytes.Buffer
	s = ansi.Wrap(s, width, breakpoints)
	w := NewWrapWriter(&buf)
	defer w.Close() //nolint:errcheck
	_, _ = io.WriteString(w, s)
	return buf.String()
}

// WrapWriter is a writer that writes to a buffer and keeps track of the
// current pen style and link state for the purpose of wrapping with newlines.
//
// When it encounters a newline, it resets the style and link, writes the
// newline, and then reapplies the style and link to the next line.
type WrapWriter struct {
	w     io.Writer
	p     *ansi.Parser
	style uv.Style
	link  uv.Link
}

// NewWrapWriter returns a new [WrapWriter].
func NewWrapWriter(w io.Writer) *WrapWriter {
	pw := &WrapWriter{w: w}
	pw.p = ansi.GetParser()
	handleCsi := func(cmd ansi.Cmd, params ansi.Params) {
		if cmd == 'm' {
			uv.ReadStyle(params, &pw.style)
		}
	}
	handleOsc := func(cmd int, data []byte) {
		if cmd == 8 {
			uv.ReadLink(data, &pw.link)
		}
	}
	pw.p.SetHandler(ansi.Handler{
		HandleCsi: handleCsi,
		HandleOsc: handleOsc,
	})
	return pw
}

// Style returns the current pen style.
func (w *WrapWriter) Style() uv.Style {
	return w.style
}

// Link returns the current pen link.
func (w *WrapWriter) Link() uv.Link {
	return w.link
}

// Write writes to the buffer.
func (w *WrapWriter) Write(p []byte) (int, error) {
	for start := 0; start < len(p); {
		end := len(p)
		if cut := bytes.IndexByte(p[start:], '\n'); cut >= 0 {
			end = start + cut
		}
		if end > start {
			n, err := w.w.Write(p[start:end])
			for _, b := range p[start : start+n] {
				w.p.Advance(b)
			}
			if err != nil {
				return start + n, err
			}
			if start+n != end {
				return start + n, io.ErrShortWrite
			}
		}
		if end == len(p) {
			break
		}
		w.p.Advance('\n')
		if !w.style.IsZero() {
			if _, err := io.WriteString(w.w, ansi.ResetStyle); err != nil {
				return end, err
			}
		}
		if !w.link.IsZero() {
			if _, err := io.WriteString(w.w, ansi.ResetHyperlink()); err != nil {
				return end, err
			}
		}
		if n, err := w.w.Write(p[end : end+1]); err != nil {
			return end + n, err
		} else if n == 0 {
			return end, io.ErrShortWrite
		}
		start = end + 1
		if !w.link.IsZero() {
			if _, err := io.WriteString(w.w, ansi.SetHyperlink(w.link.URL, w.link.Params)); err != nil {
				return start, err
			}
		}
		if !w.style.IsZero() {
			if _, err := io.WriteString(w.w, w.style.String()); err != nil {
				return start, err
			}
		}
	}
	return len(p), nil
}

// Close closes the writer, resets the style and link if necessary, and releases
// its parser. Calling it is performance critical, but forgetting it does not
// cause safety issues or leaks.
func (w *WrapWriter) Close() error {
	if !w.style.IsZero() {
		_, _ = w.w.Write([]byte(ansi.ResetStyle))
	}
	if !w.link.IsZero() {
		_, _ = w.w.Write([]byte(ansi.ResetHyperlink()))
	}
	if w.p != nil {
		ansi.PutParser(w.p)
		w.p = nil
	}
	return nil
}
