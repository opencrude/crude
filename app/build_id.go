package app

import "crude/buildinfo"

// BuildID identifies the exact executable, so a shell never talks to a core
// built from different code (every `go run .` produces a new build).
func BuildID() string { return buildinfo.ExecutableID() }
