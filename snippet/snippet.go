// Package snippet parses a bounded, context-free numeric completion subset.
// It never reads files, environment, clipboard or provider state. Variables and
// transforms are explicitly unsupported, not expanded using ambient authority.
package snippet

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"strings"
	"unicode/utf8"

	"crude/types"
)

const (
	MaxSourceBytes   = 64 << 10
	MaxExpandedBytes = 64 << 10
	MaxOccurrences   = 256
	MaxChoices       = 64
	MaxDepth         = 16 // containment edges; a root occurrence has depth zero
	MaxStop          = 1_000_000
	maxNodes         = 2048
)

var (
	ErrInvalid     = errors.New("invalid numeric snippet")
	ErrUnsupported = errors.New("snippet variables and transforms are unsupported")
	ErrLimit       = errors.New("numeric snippet exceeds its bound")
)

type node struct {
	text       string
	stop       int // -1 for literal text
	definition bool
	parts      []node
	choices    []string
}

type parser struct {
	source string
	at     int
	nodes  int
	defs   map[int]node
}

// Parse resolves defaults before expanding any occurrence, so a reference before
// its definition gets the same value and subtree. Expansion is acyclic, bounded
// and detached. No unsupported item produces a partially usable plan.
func Parse(source string) (string, *types.CompletionSnippet, error) {
	if len(source) > MaxSourceBytes {
		return "", nil, ErrLimit
	}
	if !utf8.ValidString(source) {
		return "", nil, ErrInvalid
	}
	p := parser{source: source, defs: make(map[int]node)}
	tree, err := p.sequence(false, 0)
	if err != nil {
		return "", nil, err
	}
	e := expansion{defs: p.defs, visiting: make(map[int]bool)}
	if err := e.emit(tree, -1, 0); err != nil {
		return "", nil, err
	}
	if !e.zero {
		if len(e.occurrences) == MaxOccurrences {
			return "", nil, ErrLimit
		}
		e.occurrences = append(e.occurrences, types.CompletionOccurrence{
			Stop: 0, StartByte: e.text.Len(), EndByte: e.text.Len(), Parent: -1,
		})
	}
	text := e.text.String()
	plan := &types.CompletionSnippet{TextSHA256: textDigest(text), Occurrences: e.occurrences}
	if err := Validate(text, plan); err != nil {
		return "", nil, err
	}
	return text, plan, nil
}

func (p *parser) sequence(braced bool, depth int) ([]node, error) {
	var out []node
	var text strings.Builder
	flush := func() error {
		if text.Len() == 0 {
			return nil
		}
		p.nodes++
		if p.nodes > maxNodes {
			return ErrLimit
		}
		out = append(out, node{stop: -1, text: text.String()})
		text.Reset()
		return nil
	}
	for p.at < len(p.source) {
		c := p.source[p.at]
		if c == '}' && braced {
			if err := flush(); err != nil {
				return nil, err
			}
			p.at++
			return out, nil
		}
		if c == '\\' {
			if p.at+1 == len(p.source) || !strings.ContainsRune(`\$}`, rune(p.source[p.at+1])) {
				return nil, ErrInvalid
			}
			text.WriteByte(p.source[p.at+1])
			p.at += 2
			continue
		}
		if c != '$' {
			text.WriteByte(c)
			p.at++
			continue
		}
		if err := flush(); err != nil {
			return nil, err
		}
		n, err := p.placeholder(depth)
		if err != nil {
			return nil, err
		}
		out = append(out, n)
	}
	if braced {
		return nil, ErrInvalid
	}
	if err := flush(); err != nil {
		return nil, err
	}
	return out, nil
}

func (p *parser) placeholder(depth int) (node, error) {
	if depth > MaxDepth {
		return node{}, ErrLimit
	}
	p.nodes++
	if p.nodes > maxNodes {
		return node{}, ErrLimit
	}
	p.at++ // $
	braced := p.at < len(p.source) && p.source[p.at] == '{'
	if braced {
		p.at++
	}
	start, id := p.at, 0
	for p.at < len(p.source) && p.source[p.at] >= '0' && p.source[p.at] <= '9' {
		digit := int(p.source[p.at] - '0')
		if id > (MaxStop-digit)/10 {
			return node{}, ErrLimit
		}
		id = id*10 + digit
		p.at++
	}
	if start == p.at {
		return node{}, ErrUnsupported
	}
	n := node{stop: id}
	if !braced {
		return n, nil
	}
	if p.at == len(p.source) {
		return node{}, ErrInvalid
	}
	switch p.source[p.at] {
	case '}':
		p.at++
		return n, nil
	case ':':
		n.definition = true
		p.at++
		var err error
		n.parts, err = p.sequence(true, depth+1)
		if err != nil {
			return node{}, err
		}
	case '|':
		n.definition = true
		p.at++
		var err error
		n.choices, err = p.choiceList()
		if err != nil {
			return node{}, err
		}
	case '/':
		return node{}, ErrUnsupported
	default:
		return node{}, ErrInvalid
	}
	if old, exists := p.defs[id]; exists && !equalDefinition(old, n) {
		return node{}, ErrInvalid
	}
	p.defs[id] = n
	return n, nil
}

func (p *parser) choiceList() ([]string, error) {
	var values []string
	var value strings.Builder
	for p.at < len(p.source) {
		c := p.source[p.at]
		switch c {
		case '\\':
			if p.at+1 == len(p.source) || !strings.ContainsRune(`\,|`, rune(p.source[p.at+1])) {
				return nil, ErrInvalid
			}
			value.WriteByte(p.source[p.at+1])
			p.at += 2
		case ',', '|':
			values = append(values, value.String())
			if len(values) > MaxChoices {
				return nil, ErrLimit
			}
			value.Reset()
			p.at++
			if c == '|' {
				if p.at == len(p.source) || p.source[p.at] != '}' {
					return nil, ErrInvalid
				}
				p.at++
				return values, nil
			}
		default:
			value.WriteByte(c)
			p.at++
		}
	}
	return nil, ErrInvalid
}

func equalDefinition(a, b node) bool {
	if a.stop != b.stop || a.text != b.text || a.definition != b.definition ||
		len(a.parts) != len(b.parts) || len(a.choices) != len(b.choices) {
		return false
	}
	for i := range a.parts {
		if !equalDefinition(a.parts[i], b.parts[i]) {
			return false
		}
	}
	for i := range a.choices {
		if a.choices[i] != b.choices[i] {
			return false
		}
	}
	return true
}

type expansion struct {
	text        strings.Builder
	defs        map[int]node
	visiting    map[int]bool
	occurrences []types.CompletionOccurrence
	choiceBytes int
	zero        bool
}

func (e *expansion) write(text string) error {
	if len(text) > MaxExpandedBytes-e.text.Len() {
		return ErrLimit
	}
	e.text.WriteString(text)
	return nil
}

func (e *expansion) emit(nodes []node, parent, depth int) error {
	for _, n := range nodes {
		if n.stop < 0 {
			if err := e.write(n.text); err != nil {
				return err
			}
			continue
		}
		if depth > MaxDepth {
			return ErrLimit
		}
		if e.visiting[n.stop] {
			return ErrInvalid
		}
		if len(e.occurrences) == MaxOccurrences {
			return ErrLimit
		}
		def := e.defs[n.stop]
		choices := make([]string, len(def.choices))
		for i, value := range def.choices {
			if len(value) > MaxSourceBytes-e.choiceBytes {
				return ErrLimit
			}
			e.choiceBytes += len(value)
			choices[i] = value
		}
		index := len(e.occurrences)
		e.occurrences = append(e.occurrences, types.CompletionOccurrence{
			Stop: n.stop, StartByte: e.text.Len(), Parent: parent, Choices: choices,
		})
		e.zero = e.zero || n.stop == 0
		e.visiting[n.stop] = true
		if len(def.choices) > 0 {
			if err := e.write(def.choices[0]); err != nil {
				return err
			}
		} else if err := e.emit(def.parts, index, depth+1); err != nil {
			return err
		}
		e.visiting[n.stop] = false
		e.occurrences[index].EndByte = e.text.Len()
	}
	return nil
}

// Validate admits a detached plan against its one authoritative insertion text.
// Shape limits precede maps, copies or traversal. Parent edges must describe a
// laminar source-preorder tree with consistent numeric values and acyclic stop
// dependencies. Byte offsets must be UTF-8 boundaries; grapheme landing safety
// is the editor's separate responsibility after insertion joins adjacent text.
func Validate(text string, plan *types.CompletionSnippet) error {
	if plan == nil || len(plan.Occurrences) == 0 || len(plan.Occurrences) > MaxOccurrences ||
		len(text) > MaxExpandedBytes {
		return ErrLimit
	}
	if !utf8.ValidString(text) || len(plan.TextSHA256) != 64 || plan.TextSHA256 != textDigest(text) {
		return ErrInvalid
	}
	choiceBytes := 0
	for _, o := range plan.Occurrences {
		if len(o.Choices) > MaxChoices {
			return ErrLimit
		}
		for _, value := range o.Choices {
			if len(value) > MaxSourceBytes-choiceBytes {
				return ErrLimit
			}
			choiceBytes += len(value)
			if !utf8.ValidString(value) {
				return ErrInvalid
			}
		}
	}
	previous := make(map[int]int)
	values := make(map[int]string)
	choices := make(map[int][]string)
	edges := make(map[int][]int)
	stack := []int{-1}
	zero := false
	for i, o := range plan.Occurrences {
		if o.Stop < 0 || o.Stop > MaxStop || o.StartByte < 0 || o.EndByte < o.StartByte ||
			o.EndByte > len(text) || o.Parent < -1 || o.Parent >= i ||
			!byteBoundary(text, o.StartByte) || !byteBoundary(text, o.EndByte) {
			return ErrInvalid
		}
		for len(stack) > 0 && stack[len(stack)-1] != o.Parent {
			stack = stack[:len(stack)-1]
		}
		if len(stack) == 0 || len(stack) > MaxDepth+1 || o.StartByte < previous[o.Parent] {
			return ErrInvalid
		}
		if o.Parent >= 0 {
			parent := plan.Occurrences[o.Parent]
			if o.StartByte < parent.StartByte || o.EndByte > parent.EndByte || len(parent.Choices) > 0 {
				return ErrInvalid
			}
			edges[parent.Stop] = append(edges[parent.Stop], o.Stop)
		}
		value := text[o.StartByte:o.EndByte]
		if len(o.Choices) > 0 && o.Choices[0] != value {
			return ErrInvalid
		}
		if old, seen := values[o.Stop]; seen && (old != value || !equalChoices(choices[o.Stop], o.Choices)) {
			return ErrInvalid
		}
		values[o.Stop], choices[o.Stop] = value, o.Choices
		previous[o.Parent] = o.EndByte
		stack = append(stack, i)
		zero = zero || o.Stop == 0
	}
	if !zero {
		return ErrInvalid
	}
	// A repeated numeric value must carry the same relative descendant tree,
	// not just equal visible bytes. Otherwise editing one nested stop would
	// make different copies disagree about which text is a placeholder.
	ends := make([]int, len(plan.Occurrences))
	for i := len(ends) - 1; i >= 0; i-- {
		ends[i] = max(ends[i], i+1)
		if parent := plan.Occurrences[i].Parent; parent >= 0 {
			ends[parent] = max(ends[parent], ends[i])
		}
	}
	first := make(map[int]int)
	for i, o := range plan.Occurrences {
		if old, seen := first[o.Stop]; seen {
			if ends[i]-i != ends[old]-old {
				return ErrInvalid
			}
			base := plan.Occurrences[old].StartByte
			for j := 1; j < ends[i]-i; j++ {
				a, b := plan.Occurrences[old+j], plan.Occurrences[i+j]
				if a.Stop != b.Stop || a.StartByte-base != b.StartByte-o.StartByte ||
					a.EndByte-base != b.EndByte-o.StartByte || a.Parent-old != b.Parent-i {
					return ErrInvalid
				}
			}
		} else {
			first[o.Stop] = i
		}
	}
	visiting, heights := make(map[int]bool), make(map[int]int)
	var visit func(int) (int, bool)
	visit = func(id int) (int, bool) {
		if visiting[id] {
			return 0, false
		}
		if height := heights[id]; height > 0 {
			return height, true
		}
		height := 1
		visiting[id] = true
		for _, child := range edges[id] {
			childHeight, ok := visit(child)
			if !ok || childHeight >= MaxDepth+1 {
				return 0, false
			}
			height = max(height, childHeight+1)
		}
		visiting[id], heights[id] = false, height
		return height, true
	}
	for id := range values {
		if _, ok := visit(id); !ok {
			return ErrInvalid
		}
	}
	return nil
}

func byteBoundary(text string, at int) bool {
	return at == len(text) || utf8.RuneStart(text[at])
}

func textDigest(text string) string {
	sum := sha256.Sum256([]byte(text))
	return hex.EncodeToString(sum[:])
}

func equalChoices(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}
