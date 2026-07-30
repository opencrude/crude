package editor

import (
	"fmt"
	"image/color"
	"strings"

	"github.com/alecthomas/chroma/v2"
	"github.com/alecthomas/chroma/v2/lexers"

	"crude/tui/rendering"
	"crude/tui/theme"
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
	e.chromaStyle = buildChromaStyle(e.th)
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

func buildChromaStyle(th *theme.Theme) *chroma.Style {
	hex := func(c color.Color) string {
		if c == nil {
			return ""
		}
		r, g, b, _ := c.RGBA()
		return fmt.Sprintf("#%02x%02x%02x", r>>8, g>>8, b>>8)
	}

	b := chroma.NewStyleBuilder("editor")
	cc := th.Colors.Code
	co := th.Colors
	add := func(tt chroma.TokenType, c color.Color) {
		if h := hex(c); h != "" {
			b.Add(tt, h)
		}
	}

	// Core language tokens
	add(chroma.Keyword, cc.Keyword)
	add(chroma.KeywordType, cc.Keyword)
	add(chroma.KeywordDeclaration, cc.Keyword)
	add(chroma.KeywordNamespace, cc.Keyword)
	add(chroma.KeywordPseudo, cc.Keyword)
	add(chroma.KeywordReserved, cc.Keyword)
	add(chroma.KeywordConstant, cc.Keyword)

	add(chroma.NameFunction, cc.Function)
	add(chroma.NameFunctionMagic, cc.Function)
	add(chroma.NameDecorator, cc.Function)
	add(chroma.NameClass, cc.Function)
	add(chroma.NameNamespace, cc.Function)
	add(chroma.NameException, cc.Keyword)
	add(chroma.NameBuiltin, cc.Type)
	add(chroma.NameBuiltinPseudo, cc.Type)
	add(chroma.NameVariable, cc.Property)
	add(chroma.NameVariableMagic, cc.Property)
	add(chroma.NameLabel, cc.Property)
	add(chroma.NameProperty, cc.Property)
	add(chroma.NameConstant, cc.Number)
	add(chroma.NameOther, co.Fg)
	add(chroma.NameEntity, cc.Property)
	add(chroma.NameTag, cc.Keyword)
	add(chroma.NameAttribute, cc.Function)

	add(chroma.String, cc.String)
	add(chroma.StringDouble, cc.String)
	add(chroma.StringSingle, cc.String)
	add(chroma.StringBacktick, cc.String)
	add(chroma.LiteralStringBacktick, cc.String)
	add(chroma.StringDelimiter, cc.String)
	add(chroma.StringInterpol, cc.String)
	add(chroma.StringOther, cc.String)
	add(chroma.LiteralString, cc.String)

	add(chroma.Number, cc.Number)
	add(chroma.NumberInteger, cc.Number)
	add(chroma.NumberFloat, cc.Number)
	add(chroma.NumberHex, cc.Number)
	add(chroma.NumberOct, cc.Number)
	add(chroma.LiteralDate, cc.Number)

	add(chroma.Comment, cc.Comment)
	add(chroma.CommentSingle, cc.Comment)
	add(chroma.CommentMultiline, cc.Comment)
	add(chroma.CommentSpecial, cc.Comment)
	add(chroma.CommentPreproc, cc.Comment)
	add(chroma.CommentHashbang, cc.Comment)

	add(chroma.Operator, cc.Operator)
	add(chroma.Punctuation, co.Muted)

	// Markdown-specific tokens
	add(chroma.GenericHeading, co.Accent)
	add(chroma.GenericSubheading, co.Accent2)
	add(chroma.GenericEmph, cc.Function)
	add(chroma.GenericStrong, co.Accent)
	add(chroma.GenericDeleted, co.Error)
	add(chroma.GenericInserted, co.Success)
	add(chroma.GenericTraceback, co.Error)
	add(chroma.GenericPrompt, co.Muted)
	add(chroma.GenericOutput, co.Muted)
	add(chroma.GenericError, co.Error)

	s, _ := b.Build()
	return s
}
