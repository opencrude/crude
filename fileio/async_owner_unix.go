//go:build !windows

package fileio

import "os/exec"

func ownProcessTree(_ *exec.Cmd) (func(), error) { return func() {}, nil }
func resumeOwnedProcess(_ *exec.Cmd) error       { return nil }
