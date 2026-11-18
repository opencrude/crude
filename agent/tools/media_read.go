package tools

import (
	"fmt"
	"os"
)

// readMediaBounded reads a media file, refusing when it exceeds maxBytes.
func readMediaBounded(path string, maxBytes int64) ([]byte, error) {
	info, err := os.Lstat(path)
	if err != nil {
		return nil, err
	}
	if info.Mode()&os.ModeSymlink != 0 {
		return nil, fmt.Errorf("refusing to follow symlink %s — resolve the actual file path instead", path)
	}
	if !info.Mode().IsRegular() {
		return nil, fmt.Errorf("refusing to read non-regular media file %s", path)
	}
	if info.Size() > maxBytes {
		return nil, fmt.Errorf("file too large: %s (%d bytes, max %d)", path, info.Size(), maxBytes)
	}
	return os.ReadFile(path)
}

func fileSizeOf(path string) int64 {
	info, err := os.Stat(path)
	if err != nil {
		return 0
	}
	return info.Size()
}
