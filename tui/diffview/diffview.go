package diffview

import (
	"fmt"
	"hash/fnv"
	"image/color"
	"strconv"
	"strings"

	"github.com/alecthomas/chroma/v2"
	"github.com/alecthomas/chroma/v2/lexers"
	"github.com/aymanbagabas/go-udiff"
	"charm.land/lipgloss/v2"
	"github.com/charmbracelet/x/ansi"
	"crude/tui/rendering"
	"crude/tui/xchroma"
)

const (
	leadingSymbolsSize = 2
	lineNumPadding     = 1
)

type file struct {
	path    string
	content string
}

type layout int

const (
	layoutUnified layout = iota + 1
	layoutSplit
)

type DiffView struct {
	layout          layout
	before          file
	after           file
	fileName        string
	contextLines    int
	lineNumbers     bool
	height          int
	width           int
	xOffset         int
	yOffset         int
	infiniteYScroll bool
	style           Style
	tabWidth        int
	chromaStyle     *chroma.Style

	isComputed bool
	err        error
	unified    udiff.UnifiedDiff
	edits      []udiff.Edit

	splitHunks      []splitHunk
	totalLines      int
	codeWidth       int
	fullCodeWidth   int
	extraColOnAfter bool
	beforeNumDigits int
	afterNumDigits  int

	cachedLexer chroma.Lexer
	syntaxCache map[string]string
}

func New() *DiffView {
	dv := &DiffView{
		layout:       layoutUnified,
		contextLines: udiff.DefaultContextLines,
		lineNumbers:  true,
		tabWidth:     8,
		syntaxCache:  make(map[string]string),
	}
	dv.style = DefaultDarkStyle()
	return dv
}

func (dv *DiffView) Unified() *DiffView {
	dv.layout = layoutUnified
	return dv
}

func (dv *DiffView) Split() *DiffView {
	dv.layout = layoutSplit
	return dv
}

func (dv *DiffView) Before(path, content string) *DiffView {
	dv.before = file{path: path, content: content}
	dv.clearCaches()
	return dv
}

func (dv *DiffView) After(path, content string) *DiffView {
	dv.after = file{path: path, content: content}
	dv.clearCaches()
	return dv
}

func (dv *DiffView) FileName(name string) *DiffView {
	dv.fileName = name
	return dv
}

func (dv *DiffView) clearCaches() {
	dv.cachedLexer = nil
	dv.clearSyntaxCache()
	dv.isComputed = false
}

func (dv *DiffView) ContextLines(contextLines int) *DiffView {
	dv.contextLines = contextLines
	return dv
}

func (dv *DiffView) Style(style Style) *DiffView {
	dv.style = style
	return dv
}

func (dv *DiffView) LineNumbers(lineNumbers bool) *DiffView {
	dv.lineNumbers = lineNumbers
	return dv
}

func (dv *DiffView) Height(height int) *DiffView {
	dv.height = height
	return dv
}

func (dv *DiffView) Width(width int) *DiffView {
	dv.width = width
	return dv
}

func (dv *DiffView) XOffset(xOffset int) *DiffView {
	dv.xOffset = xOffset
	return dv
}

func (dv *DiffView) YOffset(yOffset int) *DiffView {
	dv.yOffset = yOffset
	return dv
}

func (dv *DiffView) InfiniteYScroll(infiniteYScroll bool) *DiffView {
	dv.infiniteYScroll = infiniteYScroll
	return dv
}

func (dv *DiffView) TabWidth(tabWidth int) *DiffView {
	dv.tabWidth = tabWidth
	return dv
}

func (dv *DiffView) ChromaStyle(style *chroma.Style) *DiffView {
	dv.chromaStyle = style
	dv.clearSyntaxCache()
	return dv
}

func (dv *DiffView) clearSyntaxCache() {
	for k := range dv.syntaxCache {
		delete(dv.syntaxCache, k)
	}
}

func (dv *DiffView) String() string {
	dv.normalizeLineEndings()
	dv.replaceTabs()
	if err := dv.computeDiff(); err != nil {
		return err.Error()
	}
	dv.convertDiffToSplit()
	dv.adjustStyles()
	dv.detectNumDigits()
	dv.detectTotalLines()
	dv.preventInfiniteYScroll()

	if dv.width <= 0 {
		dv.detectCodeWidth()
	} else {
		dv.resizeCodeWidth()
	}

	style := lipgloss.NewStyle()
	if dv.width > 0 {
		style = style.MaxWidth(dv.width)
	}
	if dv.height > 0 {
		style = style.MaxHeight(dv.height)
	}

	switch dv.layout {
	case layoutUnified:
		return style.Render(strings.TrimSuffix(dv.renderUnified(), "\n"))
	case layoutSplit:
		return style.Render(strings.TrimSuffix(dv.renderSplit(), "\n"))
	default:
		panic("unknown diffview layout")
	}
}

func (dv *DiffView) normalizeLineEndings() {
	dv.before.content = strings.ReplaceAll(dv.before.content, "\r\n", "\n")
	dv.after.content = strings.ReplaceAll(dv.after.content, "\r\n", "\n")
}

func (dv *DiffView) replaceTabs() {
	spaces := strings.Repeat(" ", dv.tabWidth)
	dv.before.content = strings.ReplaceAll(dv.before.content, "\t", spaces)
	dv.after.content = strings.ReplaceAll(dv.after.content, "\t", spaces)
}

func (dv *DiffView) computeDiff() error {
	if dv.isComputed {
		return dv.err
	}
	dv.isComputed = true
	dv.edits = udiff.Lines(dv.before.content, dv.after.content)
	dv.unified, dv.err = udiff.ToUnifiedDiff(
		dv.before.path, dv.after.path, dv.before.content, dv.edits, dv.contextLines,
	)
	return dv.err
}

func (dv *DiffView) convertDiffToSplit() {
	if dv.layout != layoutSplit {
		return
	}
	dv.splitHunks = make([]splitHunk, len(dv.unified.Hunks))
	for i, h := range dv.unified.Hunks {
		dv.splitHunks[i] = hunkToSplit(h)
	}
}

func (dv *DiffView) adjustStyles() {
	setPadding := func(s lipgloss.Style) lipgloss.Style {
		return s.Padding(0, lineNumPadding).Align(lipgloss.Right)
	}
	dv.style.MissingLine.LineNumber = setPadding(dv.style.MissingLine.LineNumber)
	dv.style.DividerLine.LineNumber = setPadding(dv.style.DividerLine.LineNumber)
	dv.style.EqualLine.LineNumber = setPadding(dv.style.EqualLine.LineNumber)
	dv.style.InsertLine.LineNumber = setPadding(dv.style.InsertLine.LineNumber)
	dv.style.DeleteLine.LineNumber = setPadding(dv.style.DeleteLine.LineNumber)
	dv.style.Filename.LineNumber = setPadding(dv.style.Filename.LineNumber)
}

func (dv *DiffView) detectNumDigits() {
	dv.beforeNumDigits = 0
	dv.afterNumDigits = 0
	for _, h := range dv.unified.Hunks {
		dv.beforeNumDigits = max(dv.beforeNumDigits, len(strconv.Itoa(h.FromLine+len(h.Lines))))
		dv.afterNumDigits = max(dv.afterNumDigits, len(strconv.Itoa(h.ToLine+len(h.Lines))))
	}
}

func (dv *DiffView) detectTotalLines() {
	dv.totalLines = 0
	if dv.fileName != "" {
		dv.totalLines++
	}
	switch dv.layout {
	case layoutUnified:
		for _, h := range dv.unified.Hunks {
			dv.totalLines += 1 + len(h.Lines)
		}
	case layoutSplit:
		for _, h := range dv.splitHunks {
			dv.totalLines += 1 + len(h.lines)
		}
	}
}

func (dv *DiffView) preventInfiniteYScroll() {
	if dv.infiniteYScroll {
		return
	}
	if dv.height > 0 {
		maxYOffset := max(0, dv.totalLines-dv.height)
		dv.yOffset = min(dv.yOffset, maxYOffset)
	} else {
		dv.yOffset = min(dv.yOffset, max(0, dv.totalLines-1))
	}
	dv.yOffset = max(0, dv.yOffset)
}

func (dv *DiffView) detectCodeWidth() {
	switch dv.layout {
	case layoutUnified:
		dv.detectUnifiedCodeWidth()
	case layoutSplit:
		dv.detectSplitCodeWidth()
	}
	dv.fullCodeWidth = dv.codeWidth + leadingSymbolsSize
}

func (dv *DiffView) detectUnifiedCodeWidth() {
	dv.codeWidth = 0
	for _, h := range dv.unified.Hunks {
		shownLines := rendering.VisibleWidth(dv.hunkLineFor(h))
		for _, l := range h.Lines {
			lineWidth := rendering.VisibleWidth(strings.TrimSuffix(l.Content, "\n")) + 1
			dv.codeWidth = max(dv.codeWidth, lineWidth, shownLines)
		}
	}
}

func (dv *DiffView) detectSplitCodeWidth() {
	dv.codeWidth = 0
	for i, h := range dv.splitHunks {
		shownLines := rendering.VisibleWidth(dv.hunkLineFor(dv.unified.Hunks[i]))
		for _, l := range h.lines {
			if l.before != nil {
				codeWidth := rendering.VisibleWidth(strings.TrimSuffix(l.before.Content, "\n")) + 1
				dv.codeWidth = max(dv.codeWidth, codeWidth, shownLines)
			}
			if l.after != nil {
				codeWidth := rendering.VisibleWidth(strings.TrimSuffix(l.after.Content, "\n")) + 1
				dv.codeWidth = max(dv.codeWidth, codeWidth, shownLines)
			}
		}
	}
}

func (dv *DiffView) resizeCodeWidth() {
	fullNumWidth := dv.beforeNumDigits + dv.afterNumDigits
	fullNumWidth += lineNumPadding * 4

	switch dv.layout {
	case layoutUnified:
		dv.codeWidth = dv.width - fullNumWidth - leadingSymbolsSize
	case layoutSplit:
		remainingWidth := dv.width - fullNumWidth - leadingSymbolsSize*2
		dv.codeWidth = remainingWidth / 2
		dv.extraColOnAfter = isOdd(remainingWidth)
	}
	dv.fullCodeWidth = dv.codeWidth + leadingSymbolsSize
}

func (dv *DiffView) renderUnified() string {
	var b strings.Builder

	fullContentStyle := lipgloss.NewStyle().MaxWidth(dv.fullCodeWidth)
	printedLines := -dv.yOffset
	shouldWrite := func() bool { return printedLines >= 0 }

	getContent := func(in string, ls LineStyle) (content string, leadingEllipsis bool) {
		content = strings.TrimSuffix(in, "\n")
		content = dv.hightlightCode(content, ls.Code.GetBackground())
		content = ansi.Cut(content, dv.xOffset, len(content))
		content = ansi.Truncate(content, dv.codeWidth, "…")
		leadingEllipsis = dv.xOffset > 0 && strings.TrimSpace(content) != ""
		return content, leadingEllipsis
	}

outer:
	for i, h := range dv.unified.Hunks {
		if i == 0 && dv.fileName != "" {
			if shouldWrite() {
				ls := dv.style.Filename
				if dv.lineNumbers {
					b.WriteString(ls.LineNumber.Render(pad("…", dv.beforeNumDigits)))
					b.WriteString(ls.LineNumber.Render(pad("…", dv.afterNumDigits)))
				}
				content := ansi.Truncate("  "+dv.fileName, dv.fullCodeWidth, "…")
				b.WriteString(ls.Code.Width(dv.fullCodeWidth).Render(content))
				b.WriteString("\n")
			}
			printedLines++
		}
		if shouldWrite() {
			ls := dv.style.DividerLine
			if dv.lineNumbers {
				b.WriteString(ls.LineNumber.Render(pad("…", dv.beforeNumDigits)))
				b.WriteString(ls.LineNumber.Render(pad("…", dv.afterNumDigits)))
			}
			content := ansi.Truncate(dv.hunkLineFor(h), dv.fullCodeWidth, "…")
			b.WriteString(ls.Code.Width(dv.fullCodeWidth).Render(content))
			b.WriteString("\n")
		}
		printedLines++

		beforeLine := h.FromLine
		afterLine := h.ToLine

		for j, l := range h.Lines {
			hasReachedHeight := dv.height > 0 && printedLines+1 == dv.height
			isLastHunk := i+1 == len(dv.unified.Hunks)
			isLastLine := j+1 == len(h.Lines)
			if hasReachedHeight && (!isLastHunk || !isLastLine) {
				if shouldWrite() {
					ls := dv.lineStyleForType(l.Kind)
					if dv.lineNumbers {
						b.WriteString(ls.LineNumber.Render(pad("…", dv.beforeNumDigits)))
						b.WriteString(ls.LineNumber.Render(pad("…", dv.afterNumDigits)))
					}
					b.WriteString(fullContentStyle.Render(
						ls.Code.Width(dv.fullCodeWidth).Render("  …"),
					))
					b.WriteRune('\n')
				}
				break outer
			}

			switch l.Kind {
			case udiff.Equal:
				if shouldWrite() {
					ls := dv.style.EqualLine
					content, leadingEllipsis := getContent(l.Content, ls)
					if dv.lineNumbers {
						b.WriteString(ls.LineNumber.Render(pad(beforeLine, dv.beforeNumDigits)))
						b.WriteString(ls.LineNumber.Render(pad(afterLine, dv.afterNumDigits)))
					}
					b.WriteString(fullContentStyle.Render(
						ls.Code.Width(dv.fullCodeWidth).Render(ternary(leadingEllipsis, " …", "  ") + content),
					))
				}
				beforeLine++
				afterLine++
			case udiff.Insert:
				if shouldWrite() {
					ls := dv.style.InsertLine
					content, leadingEllipsis := getContent(l.Content, ls)
					if dv.lineNumbers {
						b.WriteString(ls.LineNumber.Render(pad(" ", dv.beforeNumDigits)))
						b.WriteString(ls.LineNumber.Render(pad(afterLine, dv.afterNumDigits)))
					}
					b.WriteString(fullContentStyle.Render(
						ls.Symbol.Render(ternary(leadingEllipsis, "+…", "+ ")) +
							ls.Code.Width(dv.codeWidth).Render(content),
					))
				}
				afterLine++
			case udiff.Delete:
				if shouldWrite() {
					ls := dv.style.DeleteLine
					content, leadingEllipsis := getContent(l.Content, ls)
					if dv.lineNumbers {
						b.WriteString(ls.LineNumber.Render(pad(beforeLine, dv.beforeNumDigits)))
						b.WriteString(ls.LineNumber.Render(pad(" ", dv.afterNumDigits)))
					}
					b.WriteString(fullContentStyle.Render(
						ls.Symbol.Render(ternary(leadingEllipsis, "-…", "- ")) +
							ls.Code.Width(dv.codeWidth).Render(content),
					))
				}
				beforeLine++
			}
			if shouldWrite() {
				b.WriteRune('\n')
			}
			printedLines++
		}
	}
	return b.String()
}

func (dv *DiffView) renderSplit() string {
	var b strings.Builder

	beforeFullContentStyle := lipgloss.NewStyle().MaxWidth(dv.fullCodeWidth)
	afterFullContentStyle := lipgloss.NewStyle().MaxWidth(dv.fullCodeWidth + btoi(dv.extraColOnAfter))
	printedLines := -dv.yOffset
	shouldWrite := func() bool { return printedLines >= 0 }

	getContent := func(in string, ls LineStyle) (content string, leadingEllipsis bool) {
		content = strings.TrimSuffix(in, "\n")
		content = dv.hightlightCode(content, ls.Code.GetBackground())
		content = ansi.Cut(content, dv.xOffset, len(content))
		content = ansi.Truncate(content, dv.codeWidth, "…")
		leadingEllipsis = dv.xOffset > 0 && strings.TrimSpace(content) != ""
		return content, leadingEllipsis
	}

outer:
	for i, h := range dv.splitHunks {
		if i == 0 && dv.fileName != "" {
			if shouldWrite() {
				ls := dv.style.Filename
				if dv.lineNumbers {
					b.WriteString(ls.LineNumber.Render(pad("…", dv.beforeNumDigits)))
				}
				content := ansi.Truncate("  "+dv.fileName, dv.fullCodeWidth, "…")
				b.WriteString(ls.Code.Width(dv.fullCodeWidth).Render(content))
				if dv.lineNumbers {
					b.WriteString(ls.LineNumber.Render(pad("…", dv.afterNumDigits)))
				}
				b.WriteString(ls.Code.Width(dv.fullCodeWidth + btoi(dv.extraColOnAfter)).Render(" "))
				b.WriteRune('\n')
			}
			printedLines++
		}
		if shouldWrite() {
			ls := dv.style.DividerLine
			if dv.lineNumbers {
				b.WriteString(ls.LineNumber.Render(pad("…", dv.beforeNumDigits)))
			}
			content := ansi.Truncate(dv.hunkLineFor(dv.unified.Hunks[i]), dv.fullCodeWidth, "…")
			b.WriteString(ls.Code.Width(dv.fullCodeWidth).Render(content))
			if dv.lineNumbers {
				b.WriteString(ls.LineNumber.Render(pad("…", dv.afterNumDigits)))
			}
			b.WriteString(ls.Code.Width(dv.fullCodeWidth + btoi(dv.extraColOnAfter)).Render(" "))
			b.WriteRune('\n')
		}
		printedLines++

		beforeLine := h.fromLine
		afterLine := h.toLine

		for j, l := range h.lines {
			hasReachedHeight := dv.height > 0 && printedLines+1 == dv.height
			isLastHunk := i+1 == len(dv.unified.Hunks)
			isLastLine := j+1 == len(h.lines)
			if hasReachedHeight && (!isLastHunk || !isLastLine) {
				if shouldWrite() {
					ls := dv.style.MissingLine
					if l.before != nil {
						ls = dv.lineStyleForType(l.before.Kind)
					}
					if dv.lineNumbers {
						b.WriteString(ls.LineNumber.Render(pad("…", dv.beforeNumDigits)))
					}
					b.WriteString(beforeFullContentStyle.Render(
						ls.Code.Width(dv.fullCodeWidth).Render("  …"),
					))
					ls = dv.style.MissingLine
					if l.after != nil {
						ls = dv.lineStyleForType(l.after.Kind)
					}
					if dv.lineNumbers {
						b.WriteString(ls.LineNumber.Render(pad("…", dv.afterNumDigits)))
					}
					b.WriteString(afterFullContentStyle.Render(
						ls.Code.Width(dv.fullCodeWidth).Render("  …"),
					))
					b.WriteRune('\n')
				}
				break outer
			}

			switch {
			case l.before == nil:
				if shouldWrite() {
					ls := dv.style.MissingLine
					if dv.lineNumbers {
						b.WriteString(ls.LineNumber.Render(pad(" ", dv.beforeNumDigits)))
					}
					b.WriteString(beforeFullContentStyle.Render(
						ls.Code.Width(dv.fullCodeWidth).Render("  "),
					))
				}
			case l.before.Kind == udiff.Equal:
				if shouldWrite() {
					ls := dv.style.EqualLine
					content, leadingEllipsis := getContent(l.before.Content, ls)
					if dv.lineNumbers {
						b.WriteString(ls.LineNumber.Render(pad(beforeLine, dv.beforeNumDigits)))
					}
					b.WriteString(beforeFullContentStyle.Render(
						ls.Code.Width(dv.fullCodeWidth).Render(ternary(leadingEllipsis, " …", "  ") + content),
					))
				}
				beforeLine++
			case l.before.Kind == udiff.Delete:
				if shouldWrite() {
					ls := dv.style.DeleteLine
					content, leadingEllipsis := getContent(l.before.Content, ls)
					if dv.lineNumbers {
						b.WriteString(ls.LineNumber.Render(pad(beforeLine, dv.beforeNumDigits)))
					}
					b.WriteString(beforeFullContentStyle.Render(
						ls.Symbol.Render(ternary(leadingEllipsis, "-…", "- ")) +
							ls.Code.Width(dv.codeWidth).Render(content),
					))
				}
				beforeLine++
			}

			switch {
			case l.after == nil:
				if shouldWrite() {
					ls := dv.style.MissingLine
					if dv.lineNumbers {
						b.WriteString(ls.LineNumber.Render(pad(" ", dv.afterNumDigits)))
					}
					b.WriteString(afterFullContentStyle.Render(
						ls.Code.Width(dv.fullCodeWidth + btoi(dv.extraColOnAfter)).Render("  "),
					))
				}
			case l.after.Kind == udiff.Equal:
				if shouldWrite() {
					ls := dv.style.EqualLine
					content, leadingEllipsis := getContent(l.after.Content, ls)
					if dv.lineNumbers {
						b.WriteString(ls.LineNumber.Render(pad(afterLine, dv.afterNumDigits)))
					}
					b.WriteString(afterFullContentStyle.Render(
						ls.Code.Width(dv.fullCodeWidth + btoi(dv.extraColOnAfter)).Render(ternary(leadingEllipsis, " …", "  ") + content),
					))
				}
				afterLine++
			case l.after.Kind == udiff.Insert:
				if shouldWrite() {
					ls := dv.style.InsertLine
					content, leadingEllipsis := getContent(l.after.Content, ls)
					if dv.lineNumbers {
						b.WriteString(ls.LineNumber.Render(pad(afterLine, dv.afterNumDigits)))
					}
					b.WriteString(afterFullContentStyle.Render(
						ls.Symbol.Render(ternary(leadingEllipsis, "+…", "+ ")) +
							ls.Code.Width(dv.codeWidth+btoi(dv.extraColOnAfter)).Render(content),
					))
				}
				afterLine++
			}

			if shouldWrite() {
				b.WriteRune('\n')
			}
			printedLines++
		}
	}
	return b.String()
}

func (dv *DiffView) hunkLineFor(h *udiff.Hunk) string {
	beforeShownLines, afterShownLines := dv.hunkShownLines(h)
	return fmt.Sprintf("  @@ -%d,%d +%d,%d @@ ", h.FromLine, beforeShownLines, h.ToLine, afterShownLines)
}

func (dv *DiffView) hunkShownLines(h *udiff.Hunk) (before, after int) {
	for _, l := range h.Lines {
		switch l.Kind {
		case udiff.Equal:
			before++
			after++
		case udiff.Insert:
			after++
		case udiff.Delete:
			before++
		}
	}
	return before, after
}

func (dv *DiffView) lineStyleForType(t udiff.OpKind) LineStyle {
	switch t {
	case udiff.Equal:
		return dv.style.EqualLine
	case udiff.Insert:
		return dv.style.InsertLine
	case udiff.Delete:
		return dv.style.DeleteLine
	default:
		return dv.style.MissingLine
	}
}

func (dv *DiffView) hightlightCode(source string, bgColor color.Color) string {
	if dv.chromaStyle == nil {
		return source
	}

	cacheKey := dv.createSyntaxCacheKey(source, bgColor)
	if cached, exists := dv.syntaxCache[cacheKey]; exists {
		return cached
	}

	l := dv.getChromaLexer()
	f := dv.getChromaFormatter(bgColor)

	it, err := l.Tokenise(nil, source)
	if err != nil {
		return source
	}

	var b strings.Builder
	if err := f.Format(&b, dv.chromaStyle, it); err != nil {
		return source
	}

	result := b.String()
	dv.syntaxCache[cacheKey] = result
	return result
}

func (dv *DiffView) createSyntaxCacheKey(source string, bgColor color.Color) string {
	r, g, b, a := bgColor.RGBA()
	colorStr := fmt.Sprintf("%d,%d,%d,%d", r, g, b, a)
	h := fnv.New128a()
	h.Write([]byte(source))
	h.Write([]byte(colorStr))
	return fmt.Sprintf("%x", h.Sum(nil))
}

func (dv *DiffView) getChromaLexer() chroma.Lexer {
	if dv.cachedLexer != nil {
		return dv.cachedLexer
	}
	l := lexers.Match(dv.before.path)
	if l == nil {
		l = lexers.Analyse(dv.before.content)
	}
	if l == nil {
		l = lexers.Fallback
	}
	dv.cachedLexer = chroma.Coalesce(l)
	return dv.cachedLexer
}

func (dv *DiffView) getChromaFormatter(bgColor color.Color) chroma.Formatter {
	return xchroma.Formatter(bgColor, processChromaValue)
}

func processChromaValue(value string) string {
	value = strings.TrimRight(value, "\n")
	return value
}
