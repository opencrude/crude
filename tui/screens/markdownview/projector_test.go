package markdownview

import (
	"fmt"
	"os"
	"strings"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"

	"crude/tui/chatprojection"
	"crude/tui/rendering"
	"crude/tui/theme"
	"crude/tui/ui"
)

// fixture holds every construct the renderer knows, with link definitions
// used before they are defined.
const fixture = "# Title\n\nSome *emphasis*, **strong**, ~~gone~~ and `code` with a [link][ref].\n" +
	"Second line of the same paragraph.\n\n" +
	"Heading two\n-----------\n\n" +
	"- one\n- two\n  - nested\n- [x] done\n\n" +
	"1. first\n2. second\n\n" +
	"> quoted\n> text\n\n" +
	"```go\nfunc main() {}\n```\n\n" +
	"    indented code\n\n" +
	"| Name | Value |\n|:-----|------:|\n| a | 1 |\n| b | 2 |\n\n" +
	"---\n\n" +
	"Term\n: Definition\n\n" +
	"![picture](img/a.png) and [file](guide/b.md#L3) and [heading](#title).\n\n" +
	"[ref]: https://example.com/\n"

// spans writes every painted attribute of rows. Blank layout rows are left
// out: the space between blocks is the document's own (one blank row), while
// a whole rendering keeps each block style's margins.
func spans(rows []chatprojection.PaintLine) string {
	var b strings.Builder
	for _, row := range rows {
		if layoutOnly(row) {
			continue
		}
		for _, span := range row.Spans {
			b.WriteString(span.Text)
			b.WriteString("\x01")
			b.WriteString(string(rune(span.Role)))
			fmt.Fprintf(&b, "%v%v%v%v%v%d", span.FGSet, span.FGColor, span.BGSet, span.BGColor, span.BgRole, span.Attr)
			b.WriteString(span.Copy.String())
			if span.Link != nil {
				b.WriteString(span.Link.Target())
			}
			if span.Action != nil {
				b.WriteString(span.Action.ID)
			}
			b.WriteString("\x02")
		}
		b.WriteString("\n")
	}
	return b.String()
}

func documentRows(doc *Document) []chatprojection.PaintLine {
	var rows []chatprojection.PaintLine
	for _, row := range doc.Rows {
		rows = append(rows, row.Native...)
	}
	return rows
}

// Rendering a document block by block must paint the same text, styles,
// links, copy sources and actions as rendering it whole, so a file and a chat
// reply never differ. The repository's own largest documents are the corpus.
func TestBlockRenderingEqualsWholeDocument(t *testing.T) {
	th := theme.DefaultDark()
	tokens := ui.NewTokenSet(th)
	docs := []string{fixture}
	for _, path := range []string{"../../../AGENTS.md", "../../../docs/reference/editor.md", "../../../docs/plans/autonomous-code-intelligence.md"} {
		data, err := os.ReadFile(path)
		if err != nil {
			t.Fatal(err)
		}
		docs = append(docs, string(data))
	}
	for i, source := range docs {
		for _, width := range []int{36, 100} {
			md := rendering.MakeMdRenderer(width, th)
			whole, err := md.RenderMapped(source)
			if err != nil {
				t.Fatal(err)
			}
			want := spans(chatprojection.BridgeSourceRows(whole, tokens, "", true))
			doc := NewProjector(nil).render(Request{Text: source, Width: width, Theme: th})
			if got := spans(documentRows(doc)); got != want {
				gotLines, wantLines := strings.Split(got, "\n"), strings.Split(want, "\n")
				for row := range min(len(gotLines), len(wantLines)) {
					if gotLines[row] != wantLines[row] {
						t.Fatalf("document %d width %d: row %d differs\nblocks: %q\nwhole:  %q", i, width, row, gotLines[row], wantLines[row])
					}
				}
				t.Fatalf("document %d width %d: %d rows, whole document has %d", i, width, len(gotLines), len(wantLines))
			}
			if doc.Height != len(documentRows(doc)) {
				t.Fatalf("document %d: height %d for %d rows", i, doc.Height, len(documentRows(doc)))
			}
			// Exactly one blank row separates two blocks; a block never
			// starts or ends with one.
			for b, row := range doc.Rows {
				lines := row.Native
				if b > 0 {
					if !layoutOnly(lines[0]) {
						t.Fatalf("document %d: block %d has no separator row", i, b)
					}
					lines = lines[1:]
				}
				if len(lines) == 0 || layoutOnly(lines[0]) || layoutOnly(lines[len(lines)-1]) {
					t.Fatalf("document %d: block %d starts or ends with blank rows", i, b)
				}
			}
		}
	}
}

// Each block knows the source line it starts on, including blocks that a
// paragraph turns into (tables, setext headings) and blocks without text.
func TestBlocksStartOnTheirSourceLines(t *testing.T) {
	doc := NewProjector(nil).render(Request{Text: fixture, Width: 80, Theme: theme.DefaultDark()})
	lines := strings.Split(fixture, "\n")
	want := []string{"# Title", "Some *emphasis*", "Heading two", "- one", "1. first", "> quoted", "```go", "    indented", "| Name", "---", "Term", "![picture]"}
	if len(doc.Blocks) != len(want) {
		t.Fatalf("got %d blocks, want %d", len(doc.Blocks), len(want))
	}
	for i, block := range doc.Blocks {
		if !strings.HasPrefix(lines[block.Line], want[i]) {
			t.Fatalf("block %d starts on line %d %q, want %q", i, block.Line, lines[block.Line], want[i])
		}
		if i > 0 && block.Row <= doc.Blocks[i-1].Row {
			t.Fatalf("block rows are not increasing: %+v", doc.Blocks)
		}
	}
	// Line and row mapping is monotonic and returns to where it started.
	for line := 0.0; line < float64(doc.Lines); line += .25 {
		row := doc.RowForLine(line)
		if back := doc.LineForRow(row); back-line > .01 || line-back > .01 {
			t.Fatalf("line %g maps to row %g and back to %g", line, row, back)
		}
		if line > 0 && row < doc.RowForLine(line-.25) {
			t.Fatalf("row for line %g moved backwards", line)
		}
	}
}

// Relative links open the file the author meant, and a heading link opens
// the heading's line.
func TestDocumentLinksResolveFromTheFile(t *testing.T) {
	doc := NewProjector(nil).render(Request{Text: fixture, Width: 100, Theme: theme.DefaultDark(),
		Links: rendering.MarkdownLinks{Base: "C:/work/notes", Self: "C:/work/notes/README.md"}})
	targets := map[string]bool{}
	for _, row := range documentRows(doc) {
		for _, span := range row.Spans {
			if span.Link != nil {
				targets[span.Link.Target()] = true
			}
		}
	}
	for _, want := range []string{"C:/work/notes/img/a.png", "C:/work/notes/guide/b.md#L3", "C:/work/notes/README.md#L1", "https://example.com/"} {
		if !targets[want] {
			t.Fatalf("no link to %q in %v", want, targets)
		}
	}
}

// An edit renders only the changed block, and a burst of requests renders the
// newest one, never every one in turn.
func TestProjectorRendersOnlyChangedBlocksAndTheNewestRequest(t *testing.T) {
	got := make(chan RenderedMsg, 16)
	p := NewProjector(func(msg tea.Msg) { got <- msg.(RenderedMsg) })
	th := theme.DefaultDark()
	p.Request(Request{Text: fixture, Revision: 1, Width: 80, Theme: th})
	first := wait(t, got)
	edited := strings.Replace(fixture, "quoted", "changed", 1)
	for revision := uint64(2); revision < 8; revision++ {
		p.Request(Request{Text: edited, Revision: revision, Width: 80, Theme: th})
	}
	var last RenderedMsg
	for last.doc == nil || last.doc.Revision != 7 {
		last = wait(t, got)
		if last.doc.Revision != 2 && last.doc.Revision != 7 {
			t.Fatalf("rendered stale revision %d", last.doc.Revision)
		}
	}
	changed := 0
	for i := range first.doc.Rows {
		if first.doc.Rows[i].Version != last.doc.Rows[i].Version {
			changed++
			if !strings.Contains(strings.Join(last.doc.Rows[i].Lines, "\n"), "changed") {
				t.Fatalf("an unchanged block rendered again: %q", last.doc.Rows[i].Lines)
			}
		}
		if first.doc.Rows[i].ID != last.doc.Rows[i].ID && first.doc.Rows[i].Version == last.doc.Rows[i].Version {
			t.Fatal("an unchanged block lost its identity")
		}
	}
	if changed != 1 {
		t.Fatalf("%d blocks rendered again for a one-block edit", changed)
	}
	p.Close()
	p.Request(Request{Text: fixture, Revision: 9, Width: 80, Theme: th})
	select {
	case msg := <-got:
		t.Fatalf("closed projector delivered revision %d", msg.doc.Revision)
	case <-time.After(50 * time.Millisecond):
	}
}

func wait(t *testing.T, got chan RenderedMsg) RenderedMsg {
	t.Helper()
	select {
	case msg := <-got:
		return msg
	case <-time.After(10 * time.Second):
		t.Fatal("no rendering arrived")
	}
	return RenderedMsg{}
}

func TestCachedHeadingLinkTracksMovedTarget(t *testing.T) {
	p := NewProjector(nil)
	r := Request{Text: "[Go](#target)\n\n# Target\n\nUnchanged text.\n", Width: 80, Theme: theme.DefaultDark(),
		Links: rendering.MarkdownLinks{Base: "/notes", Self: "/notes/guide.md"}}
	first := p.render(r)
	r.Text = "First paragraph.\n\n" + r.Text
	second := p.render(r)
	for i, doc := range []*Document{first, second} {
		want := fmt.Sprintf("/notes/guide.md#L%d", 3+2*i)
		found := false
		for _, row := range documentRows(doc) {
			for _, span := range row.Spans {
				found = found || span.Link != nil && span.Link.Target() == want
			}
		}
		if !found {
			t.Fatalf("render %d does not link to %s", i, want)
		}
	}
	if a, b := first.Rows[len(first.Rows)-1], second.Rows[len(second.Rows)-1]; a.Version != b.Version {
		t.Fatal("an unrelated block rendered again after a heading moved")
	}
}

func BenchmarkLargeDocument(b *testing.B) {
	data, err := os.ReadFile("../../../docs/plans/autonomous-code-intelligence.md")
	if err != nil {
		b.Fatal(err)
	}
	th := theme.DefaultDark()
	source := string(data)
	b.Run("cold", func(b *testing.B) {
		for range b.N {
			NewProjector(nil).render(Request{Text: source, Width: 100, Theme: th})
		}
	})
	b.Run("edit", func(b *testing.B) {
		p := NewProjector(nil)
		p.render(Request{Text: source, Width: 100, Theme: th})
		edits := []string{source + "\nA new paragraph.\n", source}
		b.ResetTimer()
		for i := range b.N {
			p.render(Request{Text: edits[i%2], Width: 100, Theme: th})
		}
	})
}
