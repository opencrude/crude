package editor

import (
	"strings"

	"github.com/alecthomas/chroma/v2"
	"github.com/alecthomas/chroma/v2/lexers"

	"crude/tui/rendering"
)

// chromaBackscanCap / chromaForwardCap bound how far we may look above or
// below the viewport when tokenising so multi-line constructs (block
// comments, raw strings) whose opener or closer is off-screen still
// highlight correctly. Whole-file tokenisation is forbidden on the UI thread.
const (
	chromaBackscanCap = 256
	chromaForwardCap  = 256
)

func (e *Editor) initChroma() {
	e.chromaStyle = rendering.SourceStyle(e.th)
	path := e.filePath
	if path == "" {
		path = e.title
	}
	l := lexers.Match(path)
	if l == nil || l.Config().Name == "fallback" {
		l = lexers.Fallback
	}
	e.chromaLexer = chroma.Coalesce(l)
	e.chromaLineCache = nil
	e.chromaDirtyFrom = 0
}

// ensureChromaColors tokenises a bounded window covering [fromLine, toLine]
// (inclusive-exclusive), backscanning up to chromaBackscanCap lines so
// constructs that open above the viewport are still recognised. Results are
// cached per line; an edit on line N drops caches from N onward.
func (e *Editor) ensureChromaColors(fromLine, toLine int) {
	if e.chromaLexer == nil || e.chromaStyle == nil {
		return
	}
	lc := e.buf.LineCount()
	if lc <= 0 {
		return
	}
	if fromLine < 0 {
		fromLine = 0
	}
	if toLine > lc {
		toLine = lc
	}
	if fromLine >= toLine {
		return
	}

	need := false
	for i := fromLine; i < toLine; i++ {
		raw, err := e.buf.Line(i)
		if err != nil {
			continue
		}
		rawStr := string(raw)
		if e.chromaLineCache != nil {
			if ent := e.chromaLineCache[i]; ent != nil && ent.raw == rawStr {
				continue
			}
		}
		need = true
		break
	}
	if !need {
		return
	}

	start := fromLine - chromaBackscanCap
	if start < 0 {
		start = 0
	}
	if e.chromaDirtyFrom >= 0 && e.chromaDirtyFrom < fromLine {
		if fromLine-e.chromaDirtyFrom <= chromaBackscanCap && e.chromaDirtyFrom < start {
			start = e.chromaDirtyFrom
		}
	}
	// Extend forward so closers (*/ , closing backticks) that sit just
	// below the viewport are still visible to chroma's single-pass regex.
	end := toLine + chromaForwardCap
	if end > lc {
		end = lc
	}

	var b strings.Builder
	for i := start; i < end; i++ {
		raw, err := e.buf.Line(i)
		if err != nil {
			b.WriteByte('\n')
			continue
		}
		b.Write(raw)
		b.WriteByte('\n')
	}
	text := b.String()
	it, err := e.chromaLexer.Tokenise(nil, text)
	if err != nil {
		return
	}

	if e.chromaLineCache == nil {
		e.chromaLineCache = make(map[int]*chromaLineEntry, end-start+8)
	}

	type lineAcc struct {
		raw string
		cm  []string
	}
	accs := make([]*lineAcc, end-start)
	for i := start; i < end; i++ {
		raw, _ := e.buf.Line(i)
		rawStr := string(raw)
		visW := LineVisualWidth(rawStr, tabWidth)
		var cm []string
		if visW > 0 {
			cm = make([]string, visW)
		}
		accs[i-start] = &lineAcc{raw: rawStr, cm: cm}
	}

	absCol := 0
	lineIdx := start
	for tk := it(); tk != chroma.EOF; tk = it() {
		clr := e.chromaTokenColor(tk.Type)
		val := tk.Value
		for len(val) > 0 {
			if val[0] == '\n' {
				lineIdx++
				absCol = 0
				val = val[1:]
				continue
			}
			if lineIdx < start || lineIdx >= end {
				nl := strings.IndexByte(val, '\n')
				if nl < 0 {
					val = ""
				} else {
					val = val[nl:]
				}
				continue
			}
			acc := accs[lineIdx-start]
			if val[0] == '\t' {
				adv := TabStopAdvance(absCol, tabWidth)
				if clr != "" && acc.cm != nil {
					for c := absCol; c < absCol+adv && c < len(acc.cm); c++ {
						acc.cm[c] = clr
					}
				}
				absCol += adv
				val = val[1:]
				continue
			}
			cluster, w := rendering.FirstGrapheme(val)
			if len(cluster) == 0 {
				break
			}
			if clr != "" && acc.cm != nil {
				for c := absCol; c < absCol+w && c < len(acc.cm); c++ {
					acc.cm[c] = clr
				}
			}
			absCol += w
			val = val[len(cluster):]
		}
	}

	for i, acc := range accs {
		e.chromaLineCache[start+i] = &chromaLineEntry{raw: acc.raw, colorMap: acc.cm}
	}
	if e.chromaDirtyFrom >= 0 && e.chromaDirtyFrom < end {
		if e.chromaDirtyFrom < start {
			// Still dirty above the window; leave the watermark.
		} else {
			e.chromaDirtyFrom = end
			if e.chromaDirtyFrom >= e.buf.LineCount() {
				e.chromaDirtyFrom = -1
			}
		}
	}
}

func (e *Editor) lineColorMap(lineIdx int) []string {
	if e.chromaLineCache == nil {
		return nil
	}
	ent := e.chromaLineCache[lineIdx]
	if ent == nil {
		return nil
	}
	raw, err := e.buf.Line(lineIdx)
	if err != nil || ent.raw != string(raw) {
		return nil
	}
	return ent.colorMap
}

// buildLineColorMap tokenises a single standalone line for tests and
// simple callers. Production rendering uses ensureChromaColors so
// multi-line constructs keep lexer context across lines.
func (e *Editor) buildLineColorMap(lineStr string, lineVisW int) []string {
	if e.chromaLexer == nil || e.chromaStyle == nil || lineStr == "" || lineVisW <= 0 {
		return nil
	}
	e.buf.ReplaceAll(lineStr)
	e.chromaLineCache = nil
	e.chromaDirtyFrom = 0
	e.ensureChromaColors(0, 1)
	cm := e.lineColorMap(0)
	if cm == nil {
		cm = make([]string, lineVisW)
	}
	return cm
}

// buildLineColorMapWithRunes is kept for call-site compatibility; geometry
// is derived from the line string via the coords module.
func (e *Editor) buildLineColorMapWithRunes(lineStr string, lineVisW int, _ []rune, _ []int) []string {
	return e.buildLineColorMap(lineStr, lineVisW)
}

func (e *Editor) chromaTokenColor(tt chroma.TokenType) string {
	entry := e.chromaStyle.Get(tt)
	if entry.Colour.IsSet() {
		return entry.Colour.String()
	}
	return ""
}
