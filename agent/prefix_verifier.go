package agent

import (
	"encoding/json"
	"fmt"

	"crude/provider"
)

type prefixVerifier struct {
	accepted [][]byte
}

func newPrefixVerifier(messages []provider.ChatMessage) (*prefixVerifier, error) {
	verifier := &prefixVerifier{}
	if err := verifier.accept(messages); err != nil {
		return nil, err
	}
	return verifier, nil
}

func (v *prefixVerifier) check(messages []provider.ChatMessage) error {
	encoded, err := encodePrefixMessages(messages)
	if err != nil {
		return err
	}
	if len(encoded) < len(v.accepted) {
		return fmt.Errorf("accepted context prefix shrank: accepted=%d request=%d", len(v.accepted), len(encoded))
	}
	for i := range v.accepted {
		if string(v.accepted[i]) != string(encoded[i]) {
			return fmt.Errorf("accepted context prefix changed at message %d", i)
		}
	}
	return nil
}

func (v *prefixVerifier) accept(messages []provider.ChatMessage) error {
	encoded, err := encodePrefixMessages(messages)
	if err != nil {
		return err
	}
	v.accepted = encoded
	return nil
}

func (v *prefixVerifier) reset() { v.accepted = nil }

func encodePrefixMessages(messages []provider.ChatMessage) ([][]byte, error) {
	encoded := make([][]byte, len(messages))
	for i, message := range messages {
		data, err := json.Marshal(message)
		if err != nil {
			return nil, fmt.Errorf("encode context message %d: %w", i, err)
		}
		encoded[i] = data
	}
	return encoded, nil
}
