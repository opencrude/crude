//go:build !windows

package app

import "syscall"

// detachedProcAttr starts the core in its own session so closing the
// originating terminal (SIGHUP to its process group) cannot stop it.
func detachedProcAttr() *syscall.SysProcAttr { return &syscall.SysProcAttr{Setsid: true} }

func initializeCoreSignals() error { return nil }
