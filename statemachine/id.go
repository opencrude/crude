package statemachine

import (
	"crypto/rand"
	"encoding/hex"
)

func newID() string {
	b := make([]byte, 16)
	_, _ = rand.Read(b)
	return hex.EncodeToString(b)
}
