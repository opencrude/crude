package chatprojection

import (
	"fmt"
	"strings"
	"testing"

	"crude/tui/cell"
	"crude/tui/compositor"
	"crude/tui/rendering"
	"crude/tui/surface"
	"crude/tui/theme"
	"crude/tui/ui"
	uv "github.com/charmbracelet/ultraviolet"
)

// Check the browser target after the actual Markdown -> native rows -> clipped
// surface -> compositor -> terminal-cell path, not just upstream ANSI output.
func TestMarkdownShortenedLinksKeepFullTerminalTarget(t *testing.T) {
	for _, width := range []int{80, 160} {
		t.Run(fmt.Sprint(width), func(t *testing.T) {
			th := theme.DefaultDark()
			tokens := ui.NewTokenSet(th)
			target := "https://example.test/" + strings.Repeat("long-path/", 40) + "target?keep=all#anchor"
			text := "| Source | State |\n|---|---|\n| [Read more](" + target + ") | Ready |"
			raw, err := rendering.MakeMdRenderer(width, th).Render(text)
			if err != nil {
				t.Fatal(err)
			}
			if !strings.Contains(raw, "…") {
				t.Fatal("fixture did not shorten its displayed URL")
			}
			rows := BridgeAnsiLines(strings.Split(raw, "\n"), tokens)
			c := compositor.NewCompositor(width, len(rows)+2)
			c.BeginFrame()
			child := surface.NewSurface("markdown", width-4, len(rows), cell.TransparentBlank())
			child.SetPosition(2, 1)
			for y, row := range rows {
				PaintLineAt(child.Buffer(), 0, y, width-4, row, tokens, 0, 0, false)
			}
			c.Root().AddChild(child)
			c.Composite()
			screen := uv.NewScreenBuffer(width, len(rows)+2)
			c.PresentSurface().Draw(screen, screen.Bounds())
			linked := 0
			for y := 0; y < screen.Height(); y++ {
				for x := 0; x < screen.Width(); x++ {
					got := screen.CellAt(x, y)
					if got.Link.URL != "" {
						if got.Link.URL != target {
							t.Fatalf("browser target was shortened/changed: %q", got.Link.URL)
						}
						linked++
					}
				}
			}
			if linked == 0 {
				t.Fatal("Markdown destination was lost before reaching terminal cells")
			}
			// Occlusion and reuse must remove old targets, not leave an
			// invisible clickable link on unrelated text in the next frame.
			c.BeginFrame()
			c.Root().Buffer().WriteString(0, 0, "plain replacement", cell.White, cell.NoColor, 0)
			c.Composite()
			c.PresentSurface().Draw(screen, screen.Bounds())
			for y := 0; y < screen.Height(); y++ {
				for x := 0; x < screen.Width(); x++ {
					if screen.CellAt(x, y).Link.URL != "" {
						t.Fatal("stale hyperlink survived repaint")
					}
				}
			}
		})
	}
}
