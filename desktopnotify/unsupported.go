//go:build !windows && !darwin && !linux

package desktopnotify

func openBackend(Identity, func(string)) (backend, error) { return nil, ErrUnsupported }
