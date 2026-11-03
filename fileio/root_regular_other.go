//go:build !unix && !windows

package fileio

import (
	"errors"
	"os"
)

func openRootRegular(_ *os.Root, _ string) (*os.File, error) {
	return nil, errors.New("fileio: confined regular-file reads are unavailable on this system")
}
