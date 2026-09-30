package rendering

import (
	"strings"

	"crude/tui/cell"
)

// AnsiRun is one styled text run produced by parsing an ANSI string: the
// plain text plus the active SGR foreground, background, and attributes at
// the point the run was emitted. Used by the chatprojection ANSI->PaintLine
// bridge so it can reuse the canonical SGR parser instead of duplicating it.
type AnsiRun struct {
	Text string
	FG   cell.Color
	BG   cell.Color
	Attr cell.Attrs
}

// ParseAnsiRuns parses an independent styled line. Use AnsiRunParser for a
// block whose SGR state can span line breaks.
func ParseAnsiRuns(line string) []AnsiRun {
	var parser AnsiRunParser
	return parser.ParseLine(line)
}

// AnsiRunParser retains terminal style and escape-sequence state across lines.
// The zero value starts with default colours and attributes. OSC/DCS/APC data
// is metadata, never text. Tokenization uses the calibrated shared width path.
type AnsiRunParser struct {
	fg, bg cell.Color
	attr   cell.Attrs
	state  byte
}

func (p *AnsiRunParser) ParseLine(line string) []AnsiRun {
	var runs []AnsiRun
	var b strings.Builder
	flush := func() {
		if b.Len() == 0 {
			return
		}
		runs = append(runs, AnsiRun{Text: b.String(), FG: p.fg, BG: p.bg, Attr: p.attr})
		b.Reset()
	}

	for len(line) > 0 {
		sequence, width, n, state := WidthMethod.DecodeSequenceInString(line, p.state, nil)
		if n == 0 {
			break
		}
		line, p.state = line[n:], state
		if width == 0 {
			if params, ok := sgrParameters(sequence); ok {
				flush()
				parseSGR(params, &p.fg, &p.bg, &p.attr)
			} else if sequence == "\n" || sequence == "\r" {
				flush()
			}
			continue
		}
		b.WriteString(sequence)
	}
	flush()
	return runs
}
