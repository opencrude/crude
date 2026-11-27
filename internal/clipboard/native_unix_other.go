//go:build (!linux || android) && !windows

package clipboard

func nativeRead(Format) ([]byte, error) { return nil, errUnavailable }
func nativeCopy([]byte) error           { return errUnavailable }
