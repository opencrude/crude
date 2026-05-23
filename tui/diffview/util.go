package diffview

import (
	"fmt"
	"strings"

	"crude/tui/rendering"
)

func pad(v any, width int) string {
	s := fmt.Sprintf("%v", v)
	w := rendering.VisibleWidth(s)
	if w >= width {
		return s
	}
	return strings.Repeat(" ", width-w) + s
}

func isEven(n int) bool {
	return n%2 == 0
}

func isOdd(n int) bool {
	return !isEven(n)
}

func btoi(b bool) int {
	if b {
		return 1
	}
	return 0
}

func ternary[T any](cond bool, t, f T) T {
	if cond {
		return t
	}
	return f
}
