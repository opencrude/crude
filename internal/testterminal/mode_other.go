//go:build !windows

package testterminal

func enableVirtualInput(int) (func() error, error) { return func() error { return nil }, nil }
