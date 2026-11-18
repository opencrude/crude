package skills

import (
	"crypto/rand"
	"encoding/hex"
)

func stagingName() (string, error) {
	var token [16]byte
	if _, err := rand.Read(token[:]); err != nil {
		return "", err
	}
	return ".crude-skill-" + hex.EncodeToString(token[:]), nil
}
