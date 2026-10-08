package diffview

import (
	"crude/tui/rendering"
	"strings"
	"unicode"
)

type sourceUnit struct {
	raw, display          string
	offset, column, width int
}

// One source-byte/display-column mapping for paint, copy, find and selection.
// Terminators are not cells; other controls are safe replacement cells.
func sourceUnits(text string) []sourceUnit {
	if strings.HasSuffix(text, "\r\n") {
		text = strings.TrimSuffix(text, "\r\n")
	} else {
		text = strings.TrimSuffix(text, "\n")
	}
	var units []sourceUnit
	column, offset := 0, 0
	for text != "" {
		cluster, width := rendering.FirstGrapheme(text)
		if cluster == "" {
			break
		}
		display := cluster
		if cluster == "\t" {
			width = 4 - column%4
			display = strings.Repeat(" ", width)
		} else {
			for _, r := range cluster {
				if unicode.IsControl(r) {
					display = "�"
					width = 1
					break
				}
			}
		}
		units = append(units, sourceUnit{cluster, display, offset, column, width})
		column += width
		offset += len(cluster)
		text = text[len(cluster):]
	}
	return units
}

func sourceWidth(text string) int {
	text = strings.TrimSuffix(strings.TrimSuffix(text, "\n"), "\r")
	column := 0
	for text != "" {
		cluster, width := rendering.FirstGrapheme(text)
		if cluster == "" {
			break
		}
		if cluster == "\t" {
			column += 4 - column%4
			text = text[len(cluster):]
			continue
		}
		for _, r := range cluster {
			if unicode.IsControl(r) {
				width = 1
				break
			}
		}
		column += width
		text = text[len(cluster):]
	}
	return column
}
