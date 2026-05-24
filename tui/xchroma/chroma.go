package xchroma

import (
	"fmt"
	"image/color"
	"io"

	"github.com/alecthomas/chroma/v2"
	"charm.land/lipgloss/v2"
)

func Formatter(bgColor color.Color, processValue func(string) string) chroma.Formatter {
	bg := lipgloss.Color(colorToHex(bgColor))
	return chroma.FormatterFunc(func(w io.Writer, style *chroma.Style, it chroma.Iterator) error {
		for token := it(); token != chroma.EOF; token = it() {
			value := token.Value
			if processValue != nil {
				value = processValue(value)
			}

			entry := style.Get(token.Type)
			if entry.IsZero() {
				if _, err := fmt.Fprint(w, value); err != nil {
					return err
				}
				continue
			}

			s := lipgloss.NewStyle().Background(bg)

			if entry.Bold == chroma.Yes {
				s = s.Bold(true)
			}
			if entry.Underline == chroma.Yes {
				s = s.Underline(true)
			}
			if entry.Italic == chroma.Yes {
				s = s.Italic(true)
			}
			if entry.Colour.IsSet() {
				s = s.Foreground(lipgloss.Color(entry.Colour.String()))
			}

			if _, err := fmt.Fprint(w, s.Render(value)); err != nil {
				return err
			}
		}
		return nil
	})
}

func colorToHex(c color.Color) string {
	r, g, b, _ := c.RGBA()
	return fmt.Sprintf("#%02x%02x%02x", r>>8, g>>8, b>>8)
}
