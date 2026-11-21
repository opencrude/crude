package ansi

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"strings"

	"charm.land/lipgloss/v2"
	"github.com/charmbracelet/x/ansi"
)

// MarginWriter is a Writer that applies indentation and padding around
// whatever you write to it.
type MarginWriter struct {
	iw *IndentWriter
}

// NewMarginWriter returns a new MarginWriter.
func NewMarginWriter(ctx RenderContext, w io.Writer, rules StyleBlock) *MarginWriter {
	bs := ctx.blockStack

	var indentation uint
	var margin uint
	if rules.Indent != nil {
		indentation = *rules.Indent
	}
	if rules.Margin != nil {
		margin = *rules.Margin
	}

	pw := NewPaddingWriter(w, int(bs.Width(ctx)), nil, ctx.options.Source) //nolint:gosec
	if ctx.options.Source != nil {
		pw.padRun = func(n int) error {
			_, err := renderText(w, rules.StylePrimitive, strings.Repeat(" ", n))
			return err
		}
	} else {
		// The unmapped renderer retains upstream ANSI bytes.
		pw.PadFunc = func(_ io.Writer) { _, _ = renderText(w, rules.StylePrimitive, " ") }
	}

	ic := " "
	if rules.IndentToken != nil {
		ic = *rules.IndentToken
	}
	iw := NewIndentWriter(pw, int(indentation+margin), func(_ io.Writer) { //nolint:gosec
		_, _ = renderText(w, bs.Parent().Style.StylePrimitive, ic)
	}, ctx.options.Source)

	return &MarginWriter{iw: iw}
}

// Write writes to the margin writer and implements [io.Writer].
func (w *MarginWriter) Write(b []byte) (int, error) {
	n, err := w.iw.Write(b)
	if err != nil {
		return 0, fmt.Errorf("glamour: error writing bytes: %w", err)
	}
	return n, nil
}

// Close closes the [MarginWriter].
func (w *MarginWriter) Close() error {
	return w.iw.Close()
}

// PaddingFunc is a function that applies padding around whatever you write to it.
type PaddingFunc = func(w io.Writer)

// PaddingWriter is a writer that applies padding around whatever you write to
// it.
type PaddingWriter struct {
	source  SourceTrace
	Padding int
	PadFunc PaddingFunc
	padRun  func(int) error
	w       *lipgloss.WrapWriter
	cache   bytes.Buffer
}

// NewPaddingWriter returns a new PaddingWriter.
func NewPaddingWriter(w io.Writer, padding int, padFunc PaddingFunc, source ...SourceTrace) *PaddingWriter {
	var trace SourceTrace
	if len(source) > 0 {
		trace = source[0]
	}
	return &PaddingWriter{source: trace,
		Padding: padding,
		PadFunc: padFunc,
		w:       lipgloss.NewWrapWriter(w),
	}
}

// Write writes to the padding writer.
func (w *PaddingWriter) Write(p []byte) (int, error) {
	if w.Padding <= 0 {
		return w.w.Write(p) //nolint:wrapcheck
	}
	for start := 0; start < len(p); {
		cut := bytes.IndexByte(p[start:], '\n')
		if cut < 0 {
			w.cache.Write(p[start:])
			n, err := w.w.Write(p[start:])
			return start + n, err //nolint:wrapcheck
		}
		end := start + cut
		if end > start {
			w.cache.Write(p[start:end])
			if n, err := w.w.Write(p[start:end]); err != nil {
				return start + n, err //nolint:wrapcheck
			}
		}
		if pad := w.Padding - ansi.StringWidth(w.cache.String()); pad > 0 {
			if err := w.writePadding(pad); err != nil {
				return end, err
			}
		}
		w.cache.Reset()
		if n, err := w.w.Write(p[end : end+1]); err != nil {
			return end + n, err //nolint:wrapcheck
		}
		start = end + 1
	}
	return len(p), nil
}

func (w *PaddingWriter) writePadding(pad int) error {
	if w.source != nil {
		_, _ = io.WriteString(w.w, w.source.Pause())
	}
	var err error
	switch {
	case w.padRun != nil:
		err = w.padRun(pad)
	case w.PadFunc != nil:
		for i := 0; i < pad; i++ {
			w.PadFunc(w.w)
		}
	default:
		_, err = io.WriteString(w.w, strings.Repeat(" ", pad))
	}
	if err != nil {
		return fmt.Errorf("glamour: error writing padding: %w", err)
	}
	if w.source != nil {
		_, _ = io.WriteString(w.w, w.source.Resume())
	}
	return nil
}

// Close closes the [PaddingWriter].
func (w *PaddingWriter) Close() error {
	return w.w.Close() //nolint:wrapcheck
}

// IndentFunc is a function that applies indentation around whatever you write to
// it.
type IndentFunc = func(w io.Writer)

// IndentWriter is a writer that applies indentation around whatever you write to
// it.
type IndentWriter struct {
	source     SourceTrace
	Indent     int
	IndentFunc PaddingFunc
	w          io.Writer
	pw         *lipgloss.WrapWriter
	skipIndent bool
	direction  blockDirection
	first      string
}

// NewIndentWriter returns a new IndentWriter.
func NewIndentWriter(w io.Writer, indent int, indentFunc IndentFunc, source ...SourceTrace) *IndentWriter {
	var trace SourceTrace
	if len(source) > 0 {
		trace = source[0]
	}
	return &IndentWriter{source: trace,
		Indent:     indent,
		IndentFunc: indentFunc,
		pw:         lipgloss.NewWrapWriter(w),
		w:          w,
	}
}

func (w *IndentWriter) resetPen() {
	style := w.pw.Style()
	link := w.pw.Link()
	if !style.IsZero() {
		_, _ = io.WriteString(w.w, ansi.ResetStyle)
	}
	if !link.IsZero() {
		_, _ = io.WriteString(w.w, ansi.ResetHyperlink())
	}
}

func (w *IndentWriter) restorePen() {
	style := w.pw.Style()
	link := w.pw.Link()
	if !style.IsZero() {
		_, _ = io.WriteString(w.w, style.String())
	}
	if !link.IsZero() {
		_, _ = io.WriteString(w.w, ansi.SetHyperlink(link.URL, link.Params))
	}
}

func (w *IndentWriter) writeIndent() error {
	if w.source != nil {
		_, _ = io.WriteString(w.w, w.source.Pause())
		if w.direction != noBlockDirection {
			_, _ = io.WriteString(w.w, w.source.BoxPrefix(true, w.direction == rightToLeft))
		}
	}
	w.resetPen()
	switch {
	case w.first != "":
		if _, err := io.WriteString(w.pw, w.first); err != nil {
			return fmt.Errorf("glamour: error writing marker: %w", err)
		}
		w.first = ""
	case w.IndentFunc != nil:
		for j := 0; j < w.Indent; j++ {
			w.IndentFunc(w.pw)
		}
	default:
		if _, err := io.WriteString(w.pw, strings.Repeat(" ", w.Indent)); err != nil {
			return fmt.Errorf("glamour: error writing indentation: %w", err)
		}
	}
	w.skipIndent = true
	w.restorePen()
	if w.source != nil {
		if w.direction != noBlockDirection {
			_, _ = io.WriteString(w.w, w.source.BoxPrefix(false, false))
		}
		_, _ = io.WriteString(w.w, w.source.Resume())
	}
	return nil
}

// Write writes to the indentation writer.
func (w *IndentWriter) Write(p []byte) (int, error) {
	for start := 0; start < len(p); {
		if !w.skipIndent {
			if err := w.writeIndent(); err != nil {
				return start, err
			}
		}
		end := len(p)
		// A newline cannot split a UTF-8 codepoint. Forward each line as
		// a slice and let WrapWriter retain the ANSI pen across writes.
		if cut := bytes.IndexByte(p[start:], '\n'); cut >= 0 {
			end = start + cut + 1
			w.skipIndent = false
		}
		n, err := w.pw.Write(p[start:end])
		if err != nil {
			return start + n, fmt.Errorf("glamour: error writing bytes: %w", err)
		}
		start = end
	}
	return len(p), nil
}

// Close closes the [IndentWriter].
func (w *IndentWriter) Close() error {
	err := w.pw.Close()
	if out, ok := w.w.(io.WriteCloser); ok {
		err = errors.Join(err, out.Close())
	}
	return err
}
