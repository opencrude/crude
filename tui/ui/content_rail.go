package ui

// ShellContentRail returns the shared text rail used by persistent header and
// footer chrome. The separator rules remain full-bleed inside the shell frame;
// text and hit targets use [left, right).
func ShellContentRail(tokens *TokenSet, width int) (left, right int) {
	if width <= 0 {
		return 0, 0
	}
	pad := 0
	if tokens != nil {
		pad = tokens.PadLR()
	}
	pad = min(max(0, pad), width/2)
	left, right = pad, width-pad
	if right < left {
		right = left
	}
	return left, right
}
