//go:build !linux || android

package clipboard

// RunHelper reports no desktop helper on platforms that do not use it.
func RunHelper([]string) (int, bool) { return 0, false }
