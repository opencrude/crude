//go:build windows

package toolstore

import "os"

// On Windows os.Chmod sets or clears only the read-only attribute. The store
// sets it on files only; directories stay as they are.

func sealFile(name string, _ bool) error { return os.Chmod(name, 0o444) }

func sealDir(string) error { return nil }

func unsealFile(name string) error { return os.Chmod(name, 0o666) }

func unsealDir(string) error { return nil }

// syncDir is a no-op: os.Open cannot open a Windows directory for sync. The
// store accepts that a rename is durable only when the file system commits it.
func syncDir(string) error { return nil }
