package ui

// ContentInset computes a symmetric side inset and the columns left for a
// pane's content. Give up the inset before squeezing out the last body cell.
func ContentInset(width, preferred int) (padLR, innerW int) {
	width = max(0, width)
	padLR = min(max(0, preferred), max(0, width-1)/2)
	return padLR, width - 2*padLR
}

// ShellContentRail returns the shared text rail used by persistent header and
// footer chrome. The separator rules remain full-bleed inside the shell frame;
// text and hit targets use [left, right).
func ShellContentRail(tokens *TokenSet, width int) (left, right int) {
	pad := 0
	if tokens != nil {
		pad = tokens.PadLR()
	}
	left, inner := ContentInset(width, pad)
	return left, left + inner
}
