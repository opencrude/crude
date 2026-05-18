package session

import (
	"os"
	"path/filepath"
)

func SessionsRoot() (string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(home, ".crude", "sessions"), nil
}
