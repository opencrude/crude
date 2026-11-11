package theme

import (
	"crude/tui/cell"
	"github.com/alecthomas/chroma/v2"
	"github.com/alecthomas/chroma/v2/styles"
	"image/color"
)

// SyntaxPalette derives only source token colors. Chrome, backgrounds and
// selection retain the application theme. Pinned Chroma data supplies the named
// palettes; colors are adjusted toward readable ink on the actual editor fill.
func SyntaxPalette(base *Theme, name string) *Theme {
	if base == nil || name == "" {
		return base
	}
	bg := syntaxCellColor(base.Colors.Bg)
	light := cell.ContrastInk(bg).R == 0
	id := ""
	switch name {
	case "catppuccin":
		id = "catppuccin-mocha"
		if light {
			id = "catppuccin-latte"
		}
	case "dracula", "monokai":
		id = name
	case "github":
		id = "github-dark"
		if light {
			id = "github"
		}
	case "solarized":
		id = "solarized-dark"
		if light {
			id = "solarized-light"
		}
	default:
		return base
	}
	style := styles.Get(id)
	if style == nil {
		return base
	}
	next := *base
	roles := []struct {
		target *color.Color
		kind   chroma.TokenType
	}{
		{&next.Colors.Code.Keyword, chroma.Keyword}, {&next.Colors.Code.String, chroma.String},
		{&next.Colors.Code.Number, chroma.Number}, {&next.Colors.Code.Comment, chroma.Comment},
		{&next.Colors.Code.Function, chroma.NameFunction}, {&next.Colors.Code.Type, chroma.NameBuiltin},
		{&next.Colors.Code.Operator, chroma.Operator}, {&next.Colors.Code.Property, chroma.NameProperty},
	}
	for _, role := range roles {
		value := style.Get(role.kind).Colour
		if !value.IsSet() {
			continue
		}
		ink := color.RGBA{R: value.Red(), G: value.Green(), B: value.Blue(), A: 255}
		*role.target = readableSyntaxColor(ink, bg)
	}
	return &next
}
func syntaxCellColor(c color.Color) cell.Color {
	r, g, b, _ := c.RGBA()
	return cell.NewTrueColor(uint8(r>>8), uint8(g>>8), uint8(b>>8))
}
func readableSyntaxColor(source color.RGBA, bg cell.Color) color.Color {
	if cell.ContrastRatio(syntaxCellColor(source), bg) >= 4.5 {
		return source
	}
	target := cell.ContrastInk(bg).R
	// Keep the palette hue until enough lightness contrast is available. This
	// work runs only when the palette changes, never per token or painted cell.
	for step := 1; step <= 20; step++ {
		mix := func(value uint8) uint8 { return uint8((int(value)*(20-step) + int(target)*step) / 20) }
		next := color.RGBA{R: mix(source.R), G: mix(source.G), B: mix(source.B), A: 255}
		if cell.ContrastRatio(syntaxCellColor(next), bg) >= 4.5 {
			return next
		}
	}
	return color.RGBA{R: target, G: target, B: target, A: 255}
}
