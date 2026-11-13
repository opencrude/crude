package editor

// Ordered Enter actions and inherited indentation follow VS Code's onEnter.ts
// and autoIndent.ts at e608ca420ca76f4bfc2cfbff02538d66242e8174 (MIT).
// Copyright (c) Microsoft Corporation. All rights reserved.
// Language facts remain in the catalog. This adapter owns no document/history.
import (
	"sort"
	"strings"

	"crude/insight/catalog"
	"crude/syntax"
	"crude/tui/rendering"
	"github.com/alecthomas/chroma/v2"
	"github.com/dlclark/regexp2"
)

// Regex work is line-local and bounded on the input path. An oversized line or
// missing exact syntax preserves ordinary editing, never a truncated match.
const maxIndentLineBytes = 16 << 10
const maxIndentLookback = 2000

type enterRule struct {
	before, after, previous *regexp2.Regexp
	action                  catalog.EnterAction
}
type editLanguage struct {
	known                            bool
	catalog                          *catalog.Catalog
	path                             string
	brackets                         [][2]string
	enter                            []enterRule
	increase, decrease, next, ignore *regexp2.Regexp
}

func (e *Editor) editingLanguage() *editLanguage {
	path := e.filePath
	if path == "" {
		path = e.title
	}
	c := catalog.Current()
	if e.editLanguage.catalog == c && e.editLanguage.path == path {
		return &e.editLanguage
	}
	rules := editLanguage{catalog: c, path: path, brackets: [][2]string{{"(", ")"}, {"{", "}"}, {"[", "]"}}}
	language, ok := c.LanguageForPath(path, nil)
	rules.known = ok
	if ok && language.Editing != nil {
		data := language.Editing
		rules.brackets = data.Brackets
		compile := func(p *catalog.EditorPattern) *regexp2.Regexp {
			if p == nil {
				return nil
			}
			re, _ := p.Compile()
			return re // catalog validated every pattern
		}
		for _, r := range data.OnEnterRules {
			rules.enter = append(rules.enter, enterRule{compile(&r.BeforeText), compile(r.AfterText), compile(r.PreviousLineText), r.Action})
		}
		if r := data.IndentationRules; r != nil {
			rules.increase, rules.decrease = compile(&r.IncreaseIndentPattern), compile(&r.DecreaseIndentPattern)
			rules.next, rules.ignore = compile(r.IndentNextLinePattern), compile(r.UnIndentedLinePattern)
		}
	}
	e.editLanguage = rules
	return &e.editLanguage
}

type indentMatcher struct{ failed bool }

func (m *indentMatcher) matches(re *regexp2.Regexp, text string) bool {
	if re == nil || m.failed {
		return false
	}
	if len(text) > maxIndentLineBytes {
		m.failed = true
		return false
	}
	matched, err := re.MatchString(text)
	if err != nil {
		m.failed = true
	}
	return err == nil && matched
}
func (m *indentMatcher) optional(re *regexp2.Regexp, text string) bool {
	return re == nil || m.matches(re, text)
}

// indentContext reads only current native/lexical spans. Provisional paint and
// stale worker results are never authority for source rewrites.
type indentContext struct {
	native   *syntax.Result
	lexical  *syntaxDocument
	plain    bool
	brackets [][2]string
}

func (e *Editor) indentationContext() indentContext {
	native, lexical := e.structuralSource()
	rules := e.editingLanguage()
	return indentContext{native: native, lexical: lexical, plain: e.syntaxPlain || !rules.known, brackets: rules.brackets}
}
func (c indentContext) ready() bool { return c.plain || c.native != nil || c.lexical != nil }
func (c indentContext) kind(offset int) chroma.TokenType {
	if c.native != nil {
		spans := c.native.Spans
		i := sort.Search(len(spans), func(i int) bool { return int(spans[i].End) > offset })
		if i < len(spans) {
			return rendering.SyntaxToken(spans[i].Kind)
		}
	} else if c.lexical != nil {
		spans := c.lexical.spans
		i := sort.Search(len(spans), func(i int) bool { return spans[i].end > offset })
		if i < len(spans) {
			return spans[i].kind
		}
	}
	return chroma.Text
}
func (c indentContext) processed(raw string, start int) string {
	// VS Code removes configured bracket tokens from strings/comments/regex,
	// not their entire text. Comment leaders and quoted tag attributes remain.
	var out strings.Builder
	copied := 0
	for i := 0; i < len(raw); {
		width := 0
		if structuralExcluded(c.kind(start + i)) {
			for _, pair := range c.brackets {
				for _, token := range pair {
					if len(token) > width && strings.HasPrefix(raw[i:], token) {
						width = len(token)
					}
				}
			}
		}
		if width == 0 {
			i++
			continue
		}
		if out.Cap() == 0 {
			out.Grow(len(raw))
		}
		out.WriteString(raw[copied:i])
		i += width
		copied = i
	}
	if copied == 0 {
		return raw
	}
	out.WriteString(raw[copied:])
	return out.String()
}
func (e *Editor) indentLine(c indentContext, line int) (raw, processed string, ok bool) {
	if line < 0 || line >= e.LineCount() {
		return "", "", false
	}
	raw = e.LineText(line)
	if len(raw) > maxIndentLineBytes {
		return "", "", false
	}
	start := e.buf.positionToOffset(Position{Line: line})
	return raw, c.processed(raw, start), true
}
func (e *Editor) unshiftIndent(indent string) string {
	width := LineVisualWidth(indent, e.TabWidth())
	size := e.indentStyle().size
	target := max(0, (width-1)/size*size)
	if width == 0 {
		return ""
	}
	return e.indentStyle().padding(0, target)
}

func (e *Editor) smartNewline(start, end int) (result caretEdit) {
	a, b := e.buf.positionAtOffset(start), e.buf.positionAtOffset(end)
	left, right := e.LineText(a.Line), e.LineText(b.Line)
	before := left[:VisualColToByteOffset(left, a.Col, e.TabWidth())]
	after := right[VisualColToByteOffset(right, b.Col, e.TabWidth()):]
	base := leadingWhitespace(before)
	p := replacement(start, end, e.lineEnding()+base)
	ordinary := p
	match := &indentMatcher{}
	defer func() {
		if match.failed {
			result = ordinary
		}
	}()
	c := e.indentationContext()
	if strings.TrimSpace(before) == "" || !c.ready() || len(left) > maxIndentLineBytes || len(right) > maxIndentLineBytes {
		return p
	}
	language := e.editingLanguage()
	beforeCode := c.processed(before, start-len(before))
	afterCode := c.processed(after, end)
	_, prevCode, prevOK := e.indentLine(c, a.Line-1)
	if a.Line > 0 && !prevOK {
		return p
	}
	// A colon or a word inside a string is not a code indentation trigger.
	if start > 0 && c.kind(start-1).InSubCategory(chroma.String) {
		return p
	}
	action := catalog.EnterAction{}
	matched := false
	for _, r := range language.enter {
		prev, lhs, rhs := prevCode, beforeCode, afterCode
		if match.matches(r.before, lhs) && match.optional(r.after, rhs) && match.optional(r.previous, prev) {
			action, matched = r.action, true
			break
		}
	}
	// OnEnterSupport checks explicit actions, then bracket pairs, then opening
	// brackets, before getIndentForEnter consults the indentation rules.
	if !matched {
		for _, pair := range language.brackets {
			if !indentBracketBefore(beforeCode, pair[0]) {
				continue
			}
			action.Indent = "indent"
			if indentBracketAfter(afterCode, pair[1]) {
				action.Indent = "indentOutdent"
			}
			matched = true
			break
		}
	}
	indent := base
	if matched {
		if action.RemoveText > 0 {
			indent = indent[:max(0, len(indent)-action.RemoveText)]
		}
		normalIndent := indent
		appendText := action.AppendText
		if appendText == "" && (action.Indent == "indent" || action.Indent == "indentOutdent") {
			appendText = "\t"
		} else if action.Indent == "indent" {
			appendText = "\t" + appendText
		}
		if action.Indent == "outdent" {
			indent = e.unshiftIndent(indent)
		}
		// A tab in appendText means one file-policy indentation step, not a
		// hard-coded tab byte. Literal comment padding remains literal.
		for i, r := range appendText {
			if r == '\t' && i < len(leadingWhitespace(appendText)) {
				indent += e.levelUnit(indent)
			} else {
				indent += string(r)
			}
		}
		p.Text = e.lineEnding() + indent
		p.anchor, p.cursor = len(p.Text), len(p.Text)
		if action.Indent == "indentOutdent" {
			// Indentation-only pairs consume the old padding. Explicit
			// continuation text retains the source padding before its closer.
			if strings.TrimSpace(action.AppendText) == "" {
				p.End += len(leadingWhitespace(after))
			}
			p.Text += e.lineEnding() + normalIndent
		}
		return p
	}
	if language.increase != nil {
		if inherited, increase, ok := e.inheritIndent(c, language, match, a.Line+1, &beforeCode, true); ok {
			indent = inherited
			if increase {
				indent += e.levelUnit(indent)
			}
			if match.matches(language.decrease, afterCode) {
				indent = e.unshiftIndent(indent)
			}
			p.Text = e.lineEnding() + indent
			p.anchor, p.cursor = len(p.Text), len(p.Text)
		}
	}
	return p
}

// inheritIndent is VS Code's preceding-valid-line and temporary-next-line
// algorithm. override is the current line before an Enter selection, not a
// mutation. The bounded search refuses an unknown answer instead of guessing.
func (e *Editor) inheritIndent(c indentContext, r *editLanguage, match *indentMatcher, line int, override *string, honor bool) (string, bool, bool) {
	floor := max(0, line-maxIndentLookback)
	get := func(n int) (string, string, bool) {
		if n == line-1 && override != nil {
			return *override, *override, true
		}
		return e.indentLine(c, n)
	}
	previous := line - 1
	var raw, code string
	for ; previous >= floor; previous-- {
		var ok bool
		raw, code, ok = get(previous)
		if !ok {
			return "", false, false
		}
		if strings.TrimSpace(code) != "" && !match.matches(r.ignore, code) {
			break
		}
	}
	if previous < floor {
		return "", false, floor == 0
	}
	if match.matches(r.increase, code) || match.matches(r.next, code) {
		return leadingWhitespace(raw), true, true
	}
	if match.matches(r.decrease, code) || previous == 0 {
		return leadingWhitespace(raw), false, true
	}
	// A next-line rule grants only one temporary level, including chained
	// unbraced statements. Preserve the first statement's base after its body.
	if previous > floor {
		priorRaw, prior, ok := get(previous - 1)
		if !ok {
			return "", false, false
		}
		if !match.matches(r.increase, prior) && !match.matches(r.decrease, prior) && match.matches(r.next, prior) {
			first := previous - 1
			for first > floor {
				upRaw, up, ok := get(first - 1)
				if !ok {
					return "", false, false
				}
				if !match.matches(r.next, up) {
					break
				}
				first--
				priorRaw = upRaw
			}
			return leadingWhitespace(priorRaw), false, true
		}
	}
	if honor {
		return leadingWhitespace(raw), false, true
	}
	for n := previous; n >= floor; n-- {
		raw, code, ok := get(n)
		if !ok {
			return "", false, false
		}
		if match.matches(r.increase, code) {
			return leadingWhitespace(raw), true, true
		}
		if match.matches(r.decrease, code) {
			return leadingWhitespace(raw), false, true
		}
		if match.matches(r.next, code) {
			return leadingWhitespace(raw), false, true
		}
		if n == 0 {
			return leadingWhitespace(raw), false, true
		}
	}
	return "", false, false
}

func (e *Editor) dedentTyped(p caretEdit, text string) (result caretEdit) {
	original := p
	match := &indentMatcher{}
	defer func() {
		if match.failed {
			result = original
		}
	}()
	a, b := e.buf.positionAtOffset(p.Start), e.buf.positionAtOffset(p.End)
	if a.Line != b.Line {
		return p
	}
	language := e.editingLanguage()
	closing := false
	for _, pair := range language.brackets {
		if strings.HasSuffix(pair[1], text) {
			closing = true
			break
		}
	}
	if !closing {
		return p
	}
	c := e.indentationContext()
	if !c.ready() {
		return p
	}
	raw, code, ok := e.indentLine(c, a.Line)
	if !ok {
		return p
	}
	lineStart := p.Start - VisualColToByteOffset(raw, a.Col, e.TabWidth())
	localStart, localEnd := p.Start-lineStart, p.End-lineStart
	indent := leadingWhitespace(raw)
	if localStart < len(indent) || indent == "" || match.matches(language.decrease, code) {
		return p
	}
	at := max(0, p.Start-1)
	if structuralExcluded(c.kind(at)) {
		return p
	}
	modified := c.processed(raw[:localStart], lineStart) + text + c.processed(raw[localEnd:], p.End)
	var desired string
	found := false
	if match.matches(language.decrease, modified) {
		inherited, increase, ok := e.inheritIndent(c, language, match, a.Line, nil, false)
		if !ok {
			return p
		}
		desired = inherited
		if !increase {
			desired = e.unshiftIndent(desired)
		}
	} else {
		// VS Code's BracketElectricCharacterSupport is the bracket fallback
		// when a configuration has no matching indentation rule. It aligns
		// only a first closing token with its exact, non-comment/string opener.
		prefix := strings.TrimLeft(raw[:localStart]+text, " \t")
		for _, pair := range language.brackets {
			if prefix != pair[1] || localEnd != localStart || strings.TrimSpace(raw[localEnd:]) != "" {
				continue
			}
			if value, ok := e.openBracketIndent(c, language, a.Line, pair[0]); ok {
				desired = value
				found = true
				break
			} else {
				return p
			}
		}
		if !found {
			return p
		}
	}
	if LineVisualWidth(desired, e.TabWidth()) >= LineVisualWidth(indent, e.TabWidth()) {
		return p
	}
	p.Start = lineStart
	p.Text = desired + raw[len(indent):localStart] + text
	p.anchor, p.cursor = len(p.Text), len(p.Text)
	return p
}

// The electric-bracket fallback searches exact-source tokens, skipping balanced
// nested pairs. Crossing or missing delimiters and the input-work bound refuse
// a match; mapped colors and a stale tree are never used as a bracket index.
func (e *Editor) openBracketIndent(c indentContext, rules *editLanguage, line int, want string) (string, bool) {
	var stack []int
	for n := line - 1; n >= max(0, line-maxIndentLookback); n-- {
		raw, _, ok := e.indentLine(c, n)
		if !ok {
			return "", false
		}
		offset := e.buf.positionToOffset(Position{Line: n})
		for end := len(raw); end > 0; {
			matched, size, opening := -1, 0, false
			for index, pair := range rules.brackets {
				for side, token := range pair {
					start := end - len(token)
					if start < 0 || len(token) <= size || !strings.EqualFold(raw[start:end], token) || structuralExcluded(c.kind(offset+start)) {
						continue
					}
					if indentWord(token[0]) && start > 0 && indentWord(raw[start-1]) {
						continue
					}
					if indentWord(token[len(token)-1]) && end < len(raw) && indentWord(raw[end]) {
						continue
					}
					matched, size, opening = index, len(token), side == 0
				}
			}
			if matched < 0 {
				end--
				continue
			}
			end -= size
			if !opening {
				stack = append(stack, matched)
				continue
			}
			if len(stack) > 0 {
				if stack[len(stack)-1] != matched {
					return "", false
				}
				stack = stack[:len(stack)-1]
				continue
			}
			if strings.EqualFold(rules.brackets[matched][0], want) {
				return leadingWhitespace(raw), true
			}
			return "", false
		}
	}
	return "", false
}

// OnEnterSupport adds a word boundary for word-shaped bracket tokens.
func indentWord(b byte) bool {
	return b >= 'a' && b <= 'z' || b >= 'A' && b <= 'Z' || b >= '0' && b <= '9' || b == '_'
}
func indentBracketBefore(text, token string) bool {
	text = strings.TrimRight(text, " \t")
	if !strings.HasSuffix(text, token) {
		return false
	}
	start := len(text) - len(token)
	return !indentWord(token[0]) || start == 0 || !indentWord(text[start-1])
}
func indentBracketAfter(text, token string) bool {
	text = strings.TrimLeft(text, " \t")
	if !strings.HasPrefix(text, token) {
		return false
	}
	return !indentWord(token[len(token)-1]) || len(text) == len(token) || !indentWord(text[len(token)])
}
