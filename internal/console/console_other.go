//go:build !windows

package console

// Init does nothing because other platforms need no console setup.
func Init() {}
