package buildinfo

import (
	"crypto/sha256"
	"encoding/hex"
	"io"
	"os"
	"sync"
)

// ExecutableID identifies a native image for immutable staging and owner handover.
// ProtocolBuild, not image bytes, is the cross-platform admission identity.
var ExecutableID = sync.OnceValue(func() string {
	exe, err := os.Executable()
	if err != nil {
		return "unknown"
	}
	f, err := os.Open(exe)
	if err != nil {
		return "unknown"
	}
	defer f.Close()
	h := sha256.New()
	if _, err = io.Copy(h, f); err != nil {
		return "unknown"
	}
	return hex.EncodeToString(h.Sum(nil))[:16]
})
