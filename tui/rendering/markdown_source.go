package rendering

import (
	"strconv"
	"strings"
)

// CopyText is immutable semantic text from the Markdown renderer before layout.
// A paragraph and its inline code share one text; a code block has its own text.
// It retains source content only, not generated controls, screen cells or padding.
type CopyText struct {
	Block     int
	Separator string
	Value     string
	Language  string
	Code      bool
}

type CopyRange struct {
	Text       *CopyText
	Start, End int
}

func (r CopyRange) Valid() bool {
	return r.Text != nil && r.Start >= 0 && r.End >= r.Start && r.End <= len(r.Text.Value)
}

type copyAtom struct {
	CopyRange
	action bool
	atomic bool
}
type copyBuild struct {
	text  *CopyText
	value strings.Builder
}
type markdownTrace struct {
	blocks    []*copyBuild
	current   *copyBuild
	atoms     []copyAtom
	separator string
}

const sourcePrefix = "\x1b_"
const sourceEnd = "\x1b\\"

func copyMarker(id int) string                  { return sourcePrefix + strconv.FormatInt(int64(id), 36) + sourceEnd }
func (t *markdownTrace) Begin(separator string) { t.current = nil; t.separator = separator }
func (t *markdownTrace) Pause() string          { return copyMarker(-1) }
func (t *markdownTrace) Resume() string         { return copyMarker(-2) }
func (t *markdownTrace) block() *copyBuild {
	if t.current == nil {
		t.current = &copyBuild{text: &CopyText{Block: len(t.blocks), Separator: t.separator}}
		t.blocks = append(t.blocks, t.current)
	}
	return t.current
}
func (t *markdownTrace) atom(value copyAtom) string {
	t.atoms = append(t.atoms, value)
	return copyMarker(len(t.atoms))
}

func (t *markdownTrace) Mark(styled, text string) string {
	block := t.block()
	plain := text
	start := block.value.Len()
	block.value.WriteString(plain)
	type part struct {
		style, text string
		start, end  int
		atomic      bool
	}
	var parts []part
	offset, col := 0, 0
	var state byte
	for remaining := styled; len(remaining) > 0; {
		sequence, width, n, next := WidthMethod.DecodeSequenceInString(remaining, state, nil)
		if n == 0 {
			break
		}
		remaining, state = remaining[n:], next
		if width == 0 && sequence != "\n" && sequence != "\r" && sequence != "\t" {
			if _, ok := sgrParameters(sequence); ok {
				parts = append(parts, part{style: sequence})
			} else if _, ok := oscHyperlink(sequence); ok {
				parts = append(parts, part{style: sequence})
			}
			continue
		}
		// Source controls have no painted width. Skip their tokens with the
		// same decoder, but keep their bytes in the authoritative source.
		for offset < len(plain) {
			raw, w, n, _ := WidthMethod.DecodeSequenceInString(plain[offset:], 0, nil)
			if n == 0 || w > 0 || raw == "\n" || raw == "\r" || raw == "\t" {
				break
			}
			offset += n
		}
		from, to := offset, offset+len(sequence)
		if sequence == "\n" && strings.HasPrefix(plain[offset:], "\r\n") {
			to = offset + 2
		}
		if to > len(plain) {
			parts = append(parts, part{text: sequence})
			continue
		}
		// The formatter may normalize CRLF or add its required final newline.
		// No other rewrite is a source correspondence. Do not guess offsets.
		if plain[from:to] != sequence && (sequence != "\n" || plain[from:to] != "\r\n") {
			// Refuse a source correspondence if a formatter rewrites text.
			// Never let source controls impersonate private annotations.
			return SanitizeForTerminal(styled)
		}
		output := sequence
		atomic := false
		if sequence == "\t" {
			output = strings.Repeat(" ", 8-col%8)
			width = len(output)
			atomic = true
		}
		if sequence == "\n" || sequence == "\r" {
			col = 0
		} else {
			col += width
		}
		parts = append(parts, part{text: output, start: start + from, end: start + to, atomic: atomic})
		offset = to
	}
	var out strings.Builder
	// Attribute runs, not individual graphemes. Word/space boundaries retain
	// offsets when established wrapping drops a boundary space. SGR changes
	// stay inside the same range and do not multiply source objects.
	for i := 0; i < len(parts); {
		if parts[i].text == "" {
			out.WriteString(parts[i].style)
			i++
			continue
		}
		first := parts[i]
		if first.end == 0 {
			out.WriteString(first.text)
			i++
			continue
		}
		space := strings.TrimSpace(first.text) == ""
		j, end := i+1, first.end
		for !first.atomic && !strings.ContainsAny(first.text, "\r\n") && j < len(parts) {
			p := parts[j]
			if p.text == "" {
				j++
				continue
			}
			if p.atomic || p.start != end || (strings.TrimSpace(p.text) == "") != space || strings.ContainsAny(parts[j-1].text, "\r\n") {
				break
			}
			end = p.end
			j++
		}
		marker := copyAtom{CopyRange: CopyRange{Text: block.text, Start: first.start, End: end}, atomic: first.atomic}
		out.WriteString(t.atom(marker))
		for ; i < j; i++ {
			out.WriteString(parts[i].style)
			out.WriteString(parts[i].text)
		}
		out.WriteString(copyMarker(0))
	}
	return out.String()
}
func (t *markdownTrace) Code(styled, text, language string) string {
	block := t.block()
	block.text.Code = true
	block.text.Language = language
	marked := t.Mark(styled, text)
	action := t.atom(copyAtom{CopyRange: CopyRange{Text: block.text}, action: true})
	t.Begin("\n\n")
	// This is a source-qualified action from the actual parsed code node, not
	// a text marker found by a second fence scanner.
	return marked + "\n" + action + "\n"
}

// TextProjection is a private annotated layout plus its immutable sources.
// Use its Parser to consume the annotations; they never go to terminal output.
type TextProjection struct {
	ANSI  string
	trace *markdownTrace
}

func (r *MarkdownRenderer) RenderMapped(in string) (TextProjection, error) {
	trace := &markdownTrace{}
	value, err := r.render(in, trace)
	if err != nil {
		return TextProjection{}, err
	}
	return trace.projection(value), nil
}
func (p TextProjection) Parser() *AnsiRunParser {
	return &AnsiRunParser{copy: &copyParser{trace: p.trace}}
}

type copyCursor struct {
	source CopyRange
	atomic bool
}
type copyParser struct {
	trace  *markdownTrace
	active CopyRange
	atomic bool
	stack  []copyCursor
	action *CopyText
}

func (p *copyParser) control(sequence string) bool {
	if !strings.HasPrefix(sequence, sourcePrefix) || !strings.HasSuffix(sequence, sourceEnd) {
		return false
	}
	value, err := strconv.ParseInt(strings.TrimSuffix(strings.TrimPrefix(sequence, sourcePrefix), sourceEnd), 36, 32)
	if err != nil {
		p.active = CopyRange{}
		return true
	}
	switch value {
	case -1:
		p.stack = append(p.stack, copyCursor{p.active, p.atomic})
		p.active = CopyRange{}
	case -2:
		if n := len(p.stack); n > 0 {
			p.active, p.atomic = p.stack[n-1].source, p.stack[n-1].atomic
			p.stack = p.stack[:n-1]
		}
	case 0:
		p.active = CopyRange{}
	default:
		if value < 1 || p.trace == nil || value > int64(len(p.trace.atoms)) {
			p.active = CopyRange{}
			return true
		}
		atom := p.trace.atoms[int(value)-1]
		if atom.action {
			p.action = atom.Text
			p.active = CopyRange{}
		} else {
			p.active = atom.CopyRange
			p.atomic = atom.atomic
		}
	}
	return true
}
func (p *AnsiRunParser) CopyAction() *CopyText {
	if p.copy == nil {
		return nil
	}
	v := p.copy.action
	p.copy.action = nil
	return v
}
func (r CopyRange) String() string {
	if !r.Valid() {
		return ""
	}
	return r.Text.Value[r.Start:r.End]
}

// PlainProjection uses the same source/decoration boundary for an unfinished
// streaming line. It does not parse it as prematurely complete Markdown.
func PlainProjection(in string) TextProjection {
	trace := &markdownTrace{}
	return trace.projection(trace.Mark(in, in))
}
func (t *markdownTrace) projection(value string) TextProjection {
	for _, block := range t.blocks {
		block.text.Value = block.value.String()
	}
	return TextProjection{ANSI: value, trace: t}
}
