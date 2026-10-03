//go:build !linux && !darwin && !windows

package fileio

import "errors"

func moveDirectoryNoReplace(string, string) error {
	return errors.New("safe directory moves are unavailable on this platform")
}
