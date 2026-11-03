//go:build !unix && !windows

package fileio

import (
	"errors"
	"os"
)

func openRegularNoFollow(string) (*os.File, error) {
	return nil, errors.New("fileio: no-follow regular reads are not supported on this platform")
}
