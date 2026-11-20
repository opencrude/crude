package buildinfo

import (
	"crypto/sha256"
	"encoding/hex"
	"io"
	"os"
	"sync"
)

// ExecutableDigest identifies this native image for staging and owner handover.
// ProtocolBuild, not image bytes, is the cross-platform admission identity.
var ExecutableDigest = sync.OnceValue(func() string {
	exe, err := os.Executable()
	if err != nil {
		return ""
	}
	f, err := os.Open(exe)
	if err != nil {
		return ""
	}
	defer f.Close()
	h := sha256.New()
	if _, err = io.Copy(h, f); err != nil {
		return ""
	}
	return hex.EncodeToString(h.Sum(nil))
})

// ExecutableID is the display and filename prefix of the same native digest.
func ExecutableID() string {
	if digest := ExecutableDigest(); digest != "" {
		return digest[:16]
	}
	return "unknown"
}
