package session

import (
	"path/filepath"

	"crude/internal/buildinfo"
)

func SessionsRoot() (string, error) {
	home, err := buildinfo.Home()
	if err != nil {
		return "", err
	}
	return filepath.Join(home, buildinfo.DataDir(), "sessions"), nil
}
