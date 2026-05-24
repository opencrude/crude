package session

import (
	"os"
	"path/filepath"

	"crude/buildinfo"
)

func SessionsRoot() (string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(home, buildinfo.DataDir(), "sessions"), nil
}
