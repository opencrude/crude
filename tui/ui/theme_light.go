package ui

import "crude/tui/theme"

// LightTheme exposes the light reference theme from the UI foundation package.
// Product and gallery code can switch themes without depending on palette details.
func LightTheme() *theme.Theme {
	return theme.DefaultLight()
}
