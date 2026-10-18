package provider

import (
	"crypto/rand"
	"encoding/hex"
	"fmt"
)

// NewMessageID mints the immutable identity every durable context message
// carries, whichever layer creates it.
func NewMessageID() string {
	var value [16]byte
	if _, err := rand.Read(value[:]); err != nil {
		panic(fmt.Sprintf("provider: generate message ID: %v", err))
	}
	return "msg-" + hex.EncodeToString(value[:])
}
