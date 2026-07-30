package tools

// This file exports the colour-free structural diff/plan parsing API so the
// semantic row builders in tui/chatprojection can consume typed records
// without importing any colour/styling code (avoiding an import cycle, since
// chatprojection imports tools but PaintLine lives in chatprojection).
//
// The parsers themselves remain in diff.go/render.go; here we only expose the
// already-colour-free data types and a thin classifier for plan-box rows.

// DiffLineKind classifies a unified-diff content line with no colour info.
type DiffLineKind int

const (
	DiffLineContext DiffLineKind = iota
	DiffLineAddition
	DiffLineDeletion
)

// DiffContentLine is one content line of a unified diff: the body (without
// the +/- prefix) plus its new-file line number (0 for deletions).
type DiffContentLine struct {
	Kind      DiffLineKind
	NewLine   int
	Content   string
}

// DiffHunkStructure is one hunk: the new-file start line and its content lines.
type DiffHunkStructure struct {
	StartLine int
	Lines     []DiffContentLine
}

// DiffFileStructure is one file's worth of a unified diff: path, change
// counts, and hunks. No colours, no styling.
type DiffFileStructure struct {
	Path     string
	AddCount int
	DelCount int
	Hunks    []DiffHunkStructure
}

// DiffRow is one flattened renderable diff row (a content line or an
// ellipsis marker inserted by compact-mode context trimming).
type DiffRow struct {
	LineNum  int
	Kind     DiffLineKind
	Content  string
	Ellipsis bool
}

// EllipsisRow returns a DiffRow marking a trimmed-context gap.
func EllipsisRow() DiffRow { return DiffRow{Ellipsis: true} }

// ParseUnifiedDiffStruct extracts the colour-free structure from a raw unified
// diff string. It strips markdown fences, file headers, @@ markers, and git
// metadata, returning typed records with new-file line numbers. Empty input
// returns a nil slice. This is the structural half of the diff pipeline; the
// semantic mapping to RowSpan roles lives in tui/chatprojection.
func ParseUnifiedDiffStruct(raw string) []DiffFileStructure {
	files := parseUnifiedDiff(raw)
	if len(files) == 0 {
		return nil
	}
	out := make([]DiffFileStructure, len(files))
	for fi, f := range files {
		hunks := make([]DiffHunkStructure, len(f.Hunks))
		for hi, h := range f.Hunks {
			lines := make([]DiffContentLine, len(h.Lines))
			for li, l := range h.Lines {
				lines[li] = DiffContentLine{
					Kind:    diffKindFromInternal(l.Type),
					NewLine:  l.NewLineNum,
					Content: l.Content,
				}
			}
			hunks[hi] = DiffHunkStructure{StartLine: h.StartLine, Lines: lines}
		}
		out[fi] = DiffFileStructure{
			Path:     f.Path,
			AddCount: f.AddCount,
			DelCount: f.DelCount,
			Hunks:    hunks,
		}
	}
	return out
}

// BuildDiffRows flattens a file's hunks into ordered renderable rows.
func BuildDiffRows(file DiffFileStructure) []DiffRow {
	hunks := make([]diffHunk, len(file.Hunks))
	for hi, h := range file.Hunks {
		lines := make([]diffLine, len(h.Lines))
		for li, l := range h.Lines {
			lines[li] = diffLine{
				Type:       internalKindFromDiff(l.Kind),
				NewLineNum: l.NewLine,
				Content:    l.Content,
			}
		}
		hunks[hi] = diffHunk{StartLine: h.StartLine, Lines: lines}
	}
	rl := buildRenderLines(hunks)
	out := make([]DiffRow, len(rl))
	for i, r := range rl {
		out[i] = DiffRow{LineNum: r.lineNum, Kind: diffKindFromInternal(r.lineType), Content: r.content, Ellipsis: r.ellipsis}
	}
	return out
}

// TrimDiffContext reduces context lines around change groups to `keep` lines
// before and after each change, inserting ellipsis markers between separated
// sections. This is the colour-free structural equivalent of the compact-mode
// trimming used by the ANSI renderer.
func TrimDiffContext(rows []DiffRow, keep int) []DiffRow {
	rl := make([]renderLine, len(rows))
	for i, r := range rows {
		rl[i] = renderLine{lineNum: r.LineNum, lineType: internalKindFromDiff(r.Kind), content: r.Content, ellipsis: r.Ellipsis}
	}
	trimmed := trimContext(rl, keep)
	out := make([]DiffRow, len(trimmed))
	for i, r := range trimmed {
		out[i] = DiffRow{LineNum: r.lineNum, Kind: diffKindFromInternal(r.lineType), Content: r.content, Ellipsis: r.ellipsis}
	}
	return out
}

func diffKindFromInternal(t diffLineType) DiffLineKind {
	switch t {
	case lineAdd:
		return DiffLineAddition
	case lineDelete:
		return DiffLineDeletion
	default:
		return DiffLineContext
	}
}

func internalKindFromDiff(k DiffLineKind) diffLineType {
	switch k {
	case DiffLineAddition:
		return lineAdd
	case DiffLineDeletion:
		return lineDelete
	default:
		return lineContext
	}
}