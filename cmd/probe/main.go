package main

import (
	"fmt"

	"github.com/charmbracelet/x/ansi"
	"crude/tui/rendering"
)

func main() {
	prev := ansi.SetWidthProfile(ansi.WidthProfileWindowsTerminal)
	defer ansi.SetWidthProfile(prev)

	r := '☰' // U+2630
	fmt.Printf("rune = ☰ (U+%04X)\n", r)
	fmt.Printf("runeVisualWidth(☰)  = %d   (via rendering.VisibleWidth)\n", rendering.VisibleWidth(string(r)))
	fmt.Printf("PlainWidth(☰)       = %d   (via WidthMethod.StringWidth)\n", rendering.PlainWidth(string(r)))
	fmt.Printf("ActiveMethod()      = %d   (0=WcWidth, 1=GraphemeWidth)\n", ansi.ActiveMethod())
	fmt.Printf("active profile      = %d   (0=Modern, 1=WT)\n", ansi.ActiveWidthProfile())
	fmt.Printf("ansi.GraphemeWidth.StringWidth(☰) = %d\n", ansi.GraphemeWidth.StringWidth("☰"))
	fmt.Printf("ansi.WcWidth.StringWidth(☰)       = %d\n", ansi.WcWidth.StringWidth("☰"))
	fmt.Printf("ansi.ActiveMethod().StringWidth(☰) = %d\n", ansi.ActiveMethod().StringWidth("☰"))
	c, w := rendering.FirstGrapheme("☰")
	fmt.Printf("FirstGrapheme(☰)      → cluster=%q width=%d\n", c, w)
}
