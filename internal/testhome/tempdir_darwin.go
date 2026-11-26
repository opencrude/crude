package testhome

import "os"

// The default /var/folders path can exceed the Unix socket limit. macOS
// aliases /tmp too; use its short physical directory for receipt fixtures.
func normalizeTempDir() error { return os.Setenv("TMPDIR", "/private/tmp") }
