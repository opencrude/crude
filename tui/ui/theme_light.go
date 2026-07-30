package ui

import "crude/tui/theme"

// LightTheme exposes the light reference theme from the UI foundation package.
// Product and gallery code can switch themes without depending on palette details.
func LightTheme() *theme.Theme {
	return theme.DefaultLight()
}

// HighContrastTheme exposes the high-contrast reference theme.
func HighContrastTheme() *theme.Theme {
	return theme.DefaultHighContrast()
}

// WarmTheme exposes the warm low-contrast dark reference theme.
func WarmTheme() *theme.Theme {
	return theme.DefaultWarm()
}

// MidnightTheme exposes the cool deep midnight reference theme.
func MidnightTheme() *theme.Theme {
	return theme.DefaultMidnight()
}

// ThemeFromPreset resolves a named reference theme (dark/light/high-contrast/warm/midnight).
func ThemeFromPreset(name string) *theme.Theme {
	return theme.FromPreset(name)
}
