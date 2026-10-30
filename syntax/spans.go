package syntax

import (
	"cmp"
	"context"
	"slices"
	"strings"
)

type capture struct {
	start, end uint32
	pattern    uint
	kind       Kind
}

type boundary struct {
	offset uint32
	index  int
	end    bool
}

// flatten follows nested highlight scopes: an inner capture overrides its
// ancestor; the later query pattern wins for identical ranges. An explicit
// Plain scope (embedded expressions) masks the enclosing string.
func flatten(ctx context.Context, captures []capture, length uint32) ([]Span, error) {
	events := make([]boundary, 0, 2*len(captures))
	for i, c := range captures {
		events = append(events, boundary{c.start, i, false}, boundary{c.end, i, true})
	}
	slices.SortFunc(events, func(a, b boundary) int { return cmp.Compare(a.offset, b.offset) })
	active := make([]int, 0, 16)
	ended := make([]bool, len(captures))
	precedes := func(i, j int) bool {
		a, b := captures[i], captures[j]
		if a.end-a.start != b.end-b.start {
			return a.end-a.start < b.end-b.start
		}
		if a.pattern != b.pattern {
			return a.pattern > b.pattern
		}
		return i > j
	}
	push := func(index int) {
		active = append(active, index)
		for i := len(active) - 1; i > 0; {
			parent := (i - 1) / 2
			if !precedes(active[i], active[parent]) {
				break
			}
			active[parent], active[i] = active[i], active[parent]
			i = parent
		}
	}
	pop := func() {
		active[0] = active[len(active)-1]
		active = active[:len(active)-1]
		for i := 0; 2*i+1 < len(active); {
			child := 2*i + 1
			if child+1 < len(active) && precedes(active[child+1], active[child]) {
				child++
			}
			if !precedes(active[child], active[i]) {
				break
			}
			active[child], active[i] = active[i], active[child]
			i = child
		}
	}
	// A thousand adjacent captures can collapse into one scope. Avoid retaining
	// a worst-case backing array with that tiny immutable result.
	spans := make([]Span, 0, min(len(captures), 4096))
	appendSpan := func(start, end uint32, kind Kind) {
		if start == end {
			return
		}
		if n := len(spans); n > 0 && spans[n-1].Kind == kind {
			spans[n-1].End = end
			return
		}
		spans = append(spans, Span{Start: start, End: end, Kind: kind})
	}
	var offset uint32
	for i := 0; i < len(events); {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		for len(active) > 0 && ended[active[0]] {
			pop()
		}
		kind := Plain
		if len(active) > 0 {
			kind = captures[active[0]].kind
		}
		next := events[i].offset
		appendSpan(offset, next, kind)
		offset = next
		for i < len(events) && events[i].offset == next {
			event := events[i]
			if event.end {
				ended[event.index] = true
			} else {
				push(event.index)
			}
			i++
		}
	}
	appendSpan(offset, length, Plain)
	return spans, ctx.Err()
}

func captureKind(name string) (Kind, bool) {
	if strings.HasPrefix(name, "_") {
		return Plain, false
	}
	base, _, _ := strings.Cut(name, ".")
	switch base {
	case "keyword":
		return Keyword, true
	case "type", "constructor":
		return Type, true
	case "function":
		if strings.HasSuffix(name, ".builtin") {
			return Builtin, true
		}
		return Function, true
	case "property":
		return Property, true
	case "variable":
		if strings.HasSuffix(name, ".builtin") {
			return Builtin, true
		}
		return Variable, true
	case "string", "escape":
		return String, true
	case "number":
		return Number, true
	case "comment":
		return Comment, true
	case "operator":
		return Operator, true
	case "punctuation":
		return Punctuation, true
	case "constant", "boolean":
		return Constant, true
	case "tag":
		return Tag, true
	case "attribute":
		return Attribute, true
	case "embedded":
		return Plain, true
	default:
		return Plain, false
	}
}
