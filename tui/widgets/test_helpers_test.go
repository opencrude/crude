package widgets

import (
	tea "charm.land/bubbletea/v2"

	"crude/tui/theme"
)

func testTheme() *theme.Theme { return theme.DefaultDark() }

func keyMsg(code rune) tea.KeyPressMsg { return tea.KeyPressMsg{Code: code} }

func keyStr(text string) tea.KeyPressMsg { return tea.KeyPressMsg{Text: text} }

// stripAnsi removes ANSI escape codes for assertion-level widget tests.
func stripAnsi(s string) string {
	var out []byte
	inEscape := false
	for index := 0; index < len(s); index++ {
		char := s[index]
		if char == 0x1b {
			inEscape = true
			continue
		}
		if inEscape {
			if (char >= 'a' && char <= 'z') || (char >= 'A' && char <= 'Z') {
				inEscape = false
			}
			continue
		}
		out = append(out, char)
	}
	return string(out)
}
