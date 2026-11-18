package provider

import (
	"io"
	"os"
)

// readFileHead fills head with the first bytes of path and returns the
// number of bytes read. It exists so the provider package can sniff media
// content without importing fileio (which would create an import cycle).
func readFileHead(path string, head []byte) (int, error) {
	file, err := os.Open(path)
	if err != nil {
		return 0, err
	}
	defer file.Close()
	n, err := io.ReadFull(file, head)
	if err == io.ErrUnexpectedEOF || err == io.EOF {
		return n, nil
	}
	return n, err
}
