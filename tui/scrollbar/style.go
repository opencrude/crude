package scrollbar

const (
	defaultTrackStyle    = "\x1b[38;5;236m"
	defaultThumbStyle    = "\x1b[38;5;243m"
	defaultFadeThumbStyle = "\x1b[38;5;239m"
	resetStyle           = "\x1b[0m"
)

func styleChar(ch, style string) string {
	return style + ch + resetStyle
}
