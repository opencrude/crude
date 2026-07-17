package widgets

// stripAnsi removes ANSI escape codes from s for assertion-level
// checks in widget unit tests. Shared across widget test files.
func stripAnsi(s string) string {
	var b []byte
	inEsc := false
	for i := 0; i < len(s); i++ {
		c := s[i]
		if c == 0x1b {
			inEsc = true
			continue
		}
		if inEsc {
			// ESC sequences end at the first letter (e.g. 'm', 'K', 'H').
			if (c >= 'a' && c <= 'z') || (c >= 'A' && c <= 'Z') {
				inEsc = false
			}
			continue
		}
		b = append(b, c)
	}
	return string(b)
}