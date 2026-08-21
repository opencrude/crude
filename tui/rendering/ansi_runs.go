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

// ParseAnsiRuns splits an ANSI-styled string into plain-text runs with their
// active SGR style. Newlines terminate a run (the caller splits the input on
// newlines first and calls this per line). SGR state carries across runs
// within a single line, matching terminal semantics. This is the structural
// half of the ANSI bridge; semantic colour->role mapping lives in
// tui/chatprojection.
func ParseAnsiRuns(line string) []AnsiRun {
	var runs []AnsiRun

	curFG := cell.NoColor
	curBG := cell.NoColor
	curAttr := cell.Attrs(0)

	var b strings.Builder
	flush := func() {
		if b.Len() == 0 {
			return
		}
		runs = append(runs, AnsiRun{Text: b.String(), FG: curFG, BG: curBG, Attr: curAttr})
		b.Reset()
	}

	i := 0
	for i < len(line) {
		if line[i] == '\x1b' && i+1 < len(line) && line[i+1] == '[' {
			j := i + 2
			for j < len(line) && (line[j] >= '0' && line[j] <= '9' || line[j] == ';' || line[j] == ':') {
				j++
			}
			if j < len(line) {
				switch line[j] {
				case 'm':
					flush()
					parseSGR(line[i+2:j], &curFG, &curBG, &curAttr)
				}
				i = j + 1
				continue
			}
		}
		cluster, dw := FirstGrapheme(line[i:])
		if len(cluster) == 0 {
			i++
			continue
		}
		i += len(cluster)
		if dw == 0 {
			continue
		}
		b.WriteString(cluster)
	}
	flush()
	return runs
}
