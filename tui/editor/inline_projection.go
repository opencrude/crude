package editor

import (
	"errors"
	"fmt"
	"sort"
	"strings"
	"unicode"
	"unicode/utf8"

	"crude/tui/rendering"
)

// InlineInsertion is display-only text at an exact source grapheme boundary.
// Index is a nonnegative identity unique within one build, not a sort key.
// Labels must already concatenate any provider label parts before this call.
type InlineInsertion struct {
	ByteOffset, Index         int
	Label                     string
	PaddingLeft, PaddingRight bool
}

// ErrInlinePlacement means valid text cannot be represented at an unambiguous
// source caret or as positive-width cells. Callers must not floor that placement.
// In particular, a line containing a standalone zero-cell source unit is refused
// entirely: its two byte boundaries share a source column.
var ErrInlinePlacement = errors.New("inline placement is not renderable")

const (
	inlineMaxInsertions = 1024
	inlineMaxSource     = 2 << 20
	inlineMaxLabel      = 4 << 10
	inlineMaxLabels     = 64 << 10
	inlineMaxRuns       = 131072
)

// inlineRun is one complete paintable grapheme, or one expanded tab space.
// SourceByte:SourceEnd retains the whole source unit (empty for insertions).
// SourceCol is the source-only paint/highlight column; AnchorCol is the source
// caret for pointer hits, including every space belonging to a tab. HintIndex
// is -1 for source, otherwise the insertion's identity, also on padding runs.
// Consumers must clip Cluster atomically using DisplayStart and Width, never
// emit a partial wide cluster or resegment adjacent source and label runs.
type inlineRun struct {
	DisplayStart, Width                         int
	SourceCol, SourceByte, SourceEnd, AnchorCol int
	Cluster                                     string
	HintIndex                                   int
	Padding                                     bool
	Ghost                                       bool
}

type inlineHit struct {
	SourceByte, SourceCol, HintIndex int
	Padding                          bool
}

type inlineBoundary struct {
	sourceCol, displayCol int
}

// inlineProjection is an immutable, per-line derived value. Rebuild it when
// source, insertions or the shared terminal-width profile changes. There is no
// editor/history state here. Runs are exposed to the package for read-only paint.
type inlineProjection struct {
	Runs                      []inlineRun
	SourceWidth, DisplayWidth int
	sourceBytes               int
	boundaries                []inlineBoundary
}

func validGhostText(text string) bool {
	if len(text) == 0 || len(text) > inlineMaxLabel || !utf8.ValidString(text) {
		return false
	}
	for _, r := range text {
		if unicode.IsControl(r) || r == '\u2028' || r == '\u2029' ||
			r == '\u061c' || r == '\u200e' || r == '\u200f' ||
			r >= '\u202a' && r <= '\u202e' || r >= '\u2066' && r <= '\u2069' {
			return false
		}
	}
	return true
}

func buildInlineProjection(raw string, insertions []InlineInsertion) (*inlineProjection, error) {
	return buildInlineProjectionWithGhost(raw, insertions, nil)
}

// Both display-only kinds share source segmentation, paint runs and hit geometry.
// Hints have after affinity; a candidate is last at its anchor, before the caret's
// source glyph, and gives the caret before affinity for its entire literal text.
func buildInlineProjectionWithGhost(raw string, insertions []InlineInsertion, ghost *InlineInsertion) (*inlineProjection, error) {
	if len(raw) > inlineMaxSource || !utf8.ValidString(raw) || strings.ContainsAny(raw, "\r\n") {
		return nil, errors.New("inline source must be a UTF-8 line of at most 2 MiB")
	}
	count := len(insertions)
	if ghost != nil {
		count++
	}
	if count > inlineMaxInsertions {
		return nil, errors.New("too many inline insertions")
	}
	type insertion struct {
		InlineInsertion
		ghost bool
	}
	ordered := make([]insertion, 0, count)
	for _, in := range insertions {
		ordered = append(ordered, insertion{InlineInsertion: in})
	}
	if ghost != nil {
		if !validGhostText(ghost.Label) || ghost.PaddingLeft || ghost.PaddingRight {
			return nil, errors.New("prediction must be bounded literal single-line text")
		}
		ordered = append(ordered, insertion{InlineInsertion: *ghost, ghost: true})
	}
	labelBytes := 0
	ids := make(map[int]struct{}, len(insertions))
	for _, in := range ordered {
		if in.ByteOffset < 0 || in.ByteOffset > len(raw) ||
			(in.ByteOffset < len(raw) && !utf8.RuneStart(raw[in.ByteOffset])) {
			return nil, fmt.Errorf("inline index %d has invalid source byte %d", in.Index, in.ByteOffset)
		}
		if in.Index < 0 && !in.ghost {
			return nil, errors.New("inline index must be nonnegative")
		}
		if !in.ghost {
			if _, exists := ids[in.Index]; exists {
				return nil, fmt.Errorf("duplicate inline index %d", in.Index)
			}
			ids[in.Index] = struct{}{}
		}
		if len(in.Label) > inlineMaxLabel || !utf8.ValidString(in.Label) {
			return nil, errors.New("inline label must be UTF-8 of at most 4 KiB")
		}
		labelBytes += len(in.Label)
		if labelBytes > inlineMaxLabels {
			return nil, errors.New("inline labels exceed 64 KiB")
		}
	}
	sort.SliceStable(ordered, func(i, j int) bool {
		if ordered[i].ByteOffset == ordered[j].ByteOffset {
			return !ordered[i].ghost && ordered[j].ghost
		}
		return ordered[i].ByteOffset < ordered[j].ByteOffset
	})
	p := &inlineProjection{sourceBytes: len(raw)}
	// Each source byte can produce at most DefaultTabWidth space runs. An
	// escaped label byte produces at most six ASCII runs (\uXXXX), plus padding.
	// The independent run ceiling also bounds source boundaries (one per source
	// unit plus EOF). A 2 MiB byte limit alone would allow millions of runs.
	// Neither bound is an eagerly allocated maximum-size buffer.
	runLimit := min(inlineMaxRuns, DefaultTabWidth*len(raw)+6*labelBytes+2*len(ordered))
	appendRun := func(run inlineRun) error {
		if len(p.Runs) >= runLimit {
			return fmt.Errorf("%w: inline projection run bound exceeded", ErrInlinePlacement)
		}
		run.DisplayStart = p.DisplayWidth
		p.Runs = append(p.Runs, run)
		p.DisplayWidth += run.Width
		return nil
	}
	next := 0
	for off, col := 0, 0; ; {
		caret := -1
		for next < len(ordered) && ordered[next].ByteOffset == off {
			in := ordered[next]
			if in.ghost {
				caret = p.DisplayWidth
			}
			run := inlineRun{SourceByte: off, SourceEnd: off, SourceCol: col, AnchorCol: col, HintIndex: in.Index, Ghost: in.ghost}
			if in.PaddingLeft {
				run.Cluster, run.Width, run.Padding = " ", 1, true
				if err := appendRun(run); err != nil {
					return nil, err
				}
			}
			label := safeInlineLabel(in.Label)
			if in.ghost {
				label = in.Label // no escaping, stripping or normalization of code
			}
			for len(label) > 0 {
				cluster, width := rendering.FirstGrapheme(label)
				if len(cluster) == 0 || width <= 0 {
					return nil, fmt.Errorf("%w: zero-cell label at index %d", ErrInlinePlacement, in.Index)
				}
				run.Cluster, run.Width, run.Padding = cluster, width, false
				if err := appendRun(run); err != nil {
					return nil, err
				}
				label = label[len(cluster):]
			}
			if in.PaddingRight {
				run.Cluster, run.Width, run.Padding = " ", 1, true
				if err := appendRun(run); err != nil {
					return nil, err
				}
			}
			next++
		}
		if caret < 0 {
			caret = p.DisplayWidth
		}
		p.boundaries = append(p.boundaries, inlineBoundary{col, caret})
		if off == len(raw) {
			p.SourceWidth = col
			break
		}
		cluster, width := rendering.FirstGrapheme(raw[off:])
		size := len(cluster)
		isTab := raw[off] == '\t'
		if isTab {
			size, width = 1, TabStopAdvance(col, DefaultTabWidth)
		}
		if size == 0 || width <= 0 {
			return nil, fmt.Errorf("%w: zero-cell source unit at byte %d", ErrInlinePlacement, off)
		}
		if next < len(ordered) && ordered[next].ByteOffset < off+size {
			return nil, fmt.Errorf("%w: byte %d is inside a source grapheme", ErrInlinePlacement, ordered[next].ByteOffset)
		}
		run := inlineRun{SourceByte: off, SourceEnd: off + size, SourceCol: col, AnchorCol: col, Cluster: cluster, Width: width, HintIndex: -1}
		if isTab {
			run.Cluster, run.Width = " ", 1
			for step := 0; step < width; step++ {
				run.SourceCol = col + step
				if err := appendRun(run); err != nil {
					return nil, err
				}
			}
		} else if err := appendRun(run); err != nil {
			return nil, err
		}
		off += size
		col += width
	}
	return p, nil
}

func (p *inlineProjection) insertionAt(displayCell int) bool {
	if displayCell < 0 || displayCell >= p.DisplayWidth {
		return false
	}
	i := sort.Search(len(p.Runs), func(i int) bool { return p.Runs[i].DisplayStart+p.Runs[i].Width > displayCell })
	return p.Runs[i].HintIndex >= 0 || p.Runs[i].Ghost
}

// sourceToDisplay floors an interior source column to its full unit's caret.
// Out-of-range columns clamp to source head/EOF, retaining AFTER affinity.
func (p *inlineProjection) sourceToDisplay(sourceCol int) int {
	sourceCol = min(p.SourceWidth, max(0, sourceCol))
	i := sort.Search(len(p.boundaries), func(i int) bool { return p.boundaries[i].sourceCol > sourceCol }) - 1
	if i < 0 {
		return 0
	}
	return p.boundaries[i].displayCol
}

// displayHit preserves insertion identity only on its actual painted cells.
// Beyond either edge it returns source head/EOF without hint identity.
func (p *inlineProjection) displayHit(displayCell int) inlineHit {
	if displayCell < 0 {
		return inlineHit{HintIndex: -1}
	}
	if displayCell >= p.DisplayWidth {
		return inlineHit{SourceByte: p.sourceBytes, SourceCol: p.SourceWidth, HintIndex: -1}
	}
	i := sort.Search(len(p.Runs), func(i int) bool { return p.Runs[i].DisplayStart+p.Runs[i].Width > displayCell })
	run := p.Runs[i]
	return inlineHit{SourceByte: run.SourceByte, SourceCol: run.AnchorCol, HintIndex: run.HintIndex, Padding: run.Padding}
}

// safeInlineLabel makes terminal controls visible without quoting ordinary text,
// backslashes or quotes. Segmentation happens AFTER escaping the whole label,
// never across source/label or provider-part boundaries. Standalone zero-cell
// graphemes are refused by the builder instead of becoming invalid paint cells.
// Invalid UTF-8 bytes are visibly escaped for standalone callers; builds reject
// invalid UTF-8 input rather than silently changing provider text.
func safeInlineLabel(raw string) string {
	const hex = "0123456789abcdef"
	var out strings.Builder
	start := 0
	for off := 0; off < len(raw); {
		r, size := utf8.DecodeRuneInString(raw[off:])
		invalid := r == utf8.RuneError && size == 1
		escape := invalid || r < 0x20 || (r >= 0x7f && r <= 0x9f) || r == '\u2028' || r == '\u2029'
		if escape {
			out.WriteString(raw[start:off])
			switch r {
			case '\n':
				out.WriteString(`\n`)
			case '\r':
				out.WriteString(`\r`)
			case '\t':
				out.WriteString(`\t`)
			default:
				if invalid || r < 0x80 {
					out.WriteString(`\x`)
					b := raw[off]
					out.WriteByte(hex[b>>4])
					out.WriteByte(hex[b&15])
				} else {
					out.WriteString(`\u`)
					for shift := 12; shift >= 0; shift -= 4 {
						out.WriteByte(hex[(r>>shift)&15])
					}
				}
			}
			start = off + size
		}
		off += size
	}
	if start == 0 {
		return raw
	}
	out.WriteString(raw[start:])
	return out.String()
}
