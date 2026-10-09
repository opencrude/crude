package app

import (
	"crypto/sha256"
	"encoding/hex"
	"io"
	"os"
	"sync"
)

var buildID = sync.OnceValue(func() string {
	exe, err := os.Executable()
	if err != nil {
		return "unknown"
	}
	file, err := os.Open(exe)
	if err != nil {
		return "unknown"
	}
	defer func() { _ = file.Close() }()
	hash := sha256.New()
	if _, err := io.Copy(hash, file); err != nil {
		return "unknown"
	}
	return hex.EncodeToString(hash.Sum(nil))[:16]
})

// BuildID identifies the exact executable, so a shell never talks to a core
// built from different code (every `go run .` produces a new build).
func BuildID() string { return buildID() }
