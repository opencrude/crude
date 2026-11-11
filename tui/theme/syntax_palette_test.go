package theme

import (
	"crude/tui/cell"
	"image/color"
	"reflect"
	"testing"
)

func TestSyntaxPalettesAreDistinctReadableAndKeepChrome(t *testing.T) {
	for _, base := range []*Theme{DefaultDark(), DefaultLight(), DefaultHighContrast(), DefaultWarm()} {
		original := base.Colors
		distinct := map[[3]uint32]bool{}
		for _, name := range []string{"catppuccin", "dracula", "github", "monokai", "solarized"} {
			next := SyntaxPalette(base, name)
			if next == base || !reflect.DeepEqual(base.Colors, original) || next.Colors.Bg != base.Colors.Bg || next.Colors.TextSelection != base.Colors.TextSelection || next.Colors.Gutter != base.Colors.Gutter {
				t.Fatal("syntax palette changed shared chrome", name)
			}
			r, g, b, _ := next.Colors.Code.Keyword.RGBA()
			distinct[[3]uint32{r, g, b}] = true
			for _, ink := range []color.Color{next.Colors.Code.Keyword, next.Colors.Code.String, next.Colors.Code.Number, next.Colors.Code.Comment, next.Colors.Code.Function, next.Colors.Code.Type, next.Colors.Code.Operator, next.Colors.Code.Property} {
				if contrast := cell.ContrastRatio(syntaxCellColor(ink), syntaxCellColor(base.Colors.Bg)); contrast < 4.5 {
					t.Fatal("unreadable source ink", base.Name, name, contrast)
				}
			}
		}
		if len(distinct) < 4 {
			t.Fatal("syntax palette preferences are inert", base.Name, len(distinct))
		}
	}
}
