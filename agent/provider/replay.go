package provider

import (
	"crypto/sha256"
	"encoding/binary"
	"encoding/hex"
	"encoding/json"
	"hash"
	"io"
	"strings"
)

type prefixHasher struct{ state [32]byte }

func newPrefixHasher() prefixHasher {
	// Keep the original empty-tool seed. A tool catalog belongs to the next
	// generation request, not to the message history the endpoint already has.
	return prefixHasher{state: sha256.Sum256([]byte("crude-provider-prefix-v1\x00null"))}
}

func (h *prefixHasher) add(message ChatMessage) {

	digest := sha256.New()
	_, _ = digest.Write(h.state[:])
	writeHashString(digest, message.Role)
	writeHashString(digest, string(message.Content))
	writeHashString(digest, message.ReasoningContent)
	writeHashString(digest, message.Reasoning)
	writeHashString(digest, message.Refusal)
	writeHashString(digest, message.ToolCallID)
	if message.CacheControl != nil {
		writeHashString(digest, message.CacheControl.Type)
	} else {
		writeHashString(digest, "")
	}
	for _, tool := range message.ToolCalls {
		writeHashUint64(digest, uint64(tool.Index))
		writeHashString(digest, tool.ID)
		writeHashString(digest, tool.Type)
		writeHashString(digest, tool.Function.Name)
		writeHashString(digest, tool.Function.Arguments)
	}
	writeHashUint64(digest, uint64(len(message.ToolCalls)))
	for _, detail := range message.ReasoningDetails {
		writeHashBytes(digest, detail)
	}
	writeHashUint64(digest, uint64(len(message.ReasoningDetails)))
	for _, item := range message.ProviderItems {
		writeHashString(digest, item.Standard)
		writeHashString(digest, item.Type)
		writeHashBytes(digest, item.Data)
	}
	writeHashUint64(digest, uint64(len(message.ProviderItems)))
	if message.ToolError {
		writeHashUint64(digest, 1)
	} else {
		writeHashUint64(digest, 0)
	}
	_ = digest.Sum(h.state[:0])
}

func writeHashString(digest hash.Hash, value string) {
	writeHashUint64(digest, uint64(len(value)))
	_, _ = io.WriteString(digest, value)
}

func writeHashBytes(digest hash.Hash, value []byte) {
	writeHashUint64(digest, uint64(len(value)))
	_, _ = digest.Write(value)
}

func writeHashUint64(digest hash.Hash, value uint64) {
	var encoded [8]byte
	binary.LittleEndian.PutUint64(encoded[:], value)
	_, _ = digest.Write(encoded[:])
}

func (h prefixHasher) value() string { return hex.EncodeToString(h.state[:]) }

// ReplayScope binds opaque provider items to one configured provider endpoint
// and wire protocol. It prevents encrypted or signed state from crossing into
// another provider that happens to use the same JSON format.
func (p *Provider) ReplayScope() string {
	if p == nil {
		return ""
	}
	identity := strings.TrimSpace(p.ID) + "\x00" + strings.TrimRight(strings.TrimSpace(p.BaseURL), "/") + "\x00" + p.ProtocolID()
	sum := sha256.Sum256([]byte(identity))
	return hex.EncodeToString(sum[:])
}

// MessagePrefixHash covers accepted message bytes, including attachments and provider replay.
func MessagePrefixHash(messages []ChatMessage) string {
	hasher := newPrefixHasher()
	for _, message := range messages {
		hasher.add(message)
	}
	return hasher.value()
}

// FilterReplayMessages returns a request-owned copy. Opaque replay metadata is
// retained only when both its provider scope and immutable request prefix match.
// The canonical transcript is never modified.
func FilterReplayMessages(messages []ChatMessage, current *Provider) []ChatMessage {
	result, _ := ProjectReplayMessages(messages, current)
	return result
}

// ProjectReplayMessages returns request-owned history and its exact digest in
// one pass. The same proof stamps completed output and stored response IDs.
func ProjectReplayMessages(messages []ChatMessage, current *Provider) ([]ChatMessage, string) {
	result := make([]ChatMessage, 0, len(messages))
	scope := current.ReplayScope()
	hasher := newPrefixHasher()
	for _, message := range messages {
		copy := message
		copy.ProviderItems = nil
		copy.nativeReplay = false
		prefix := hasher.value()
		eligible := message.ReplayScope == scope && message.ReplayPrefixHash != "" && message.ReplayPrefixHash == prefix
		if eligible {
			for _, item := range message.ProviderItems {
				if item.Type == nativeWindowItem {
					if len(result) == 1 && result[0].Role == "system" && message.Origin == OriginCheckpoint && current != nil && item.PrefixHash == prefix && item.SourceModel == current.Model && item.ReplayScope == current.compactionScope(current.Model) && !current.rejectedCheckpoint(message, item) {
						copy.nativeReplay = true
						copy.ProviderItems = append(copy.ProviderItems, cloneProviderItem(item))
					}
					continue
				}
				if item.ReplayScope == scope && item.PrefixHash == prefix {
					copy.ProviderItems = append(copy.ProviderItems, cloneProviderItem(item))
				}
			}
		} else {
			copy.ReasoningContent = ""
			copy.Reasoning = ""
			copy.ReasoningDetails = nil
			copy.ReplayScope = ""
			copy.ReplaySourceModel = ""
			copy.ReplayPrefixHash = ""
		}
		result = append(result, copy)
		hasher.add(copy)
	}
	return result, hasher.value()
}

// StampProviderItems copies items and stamps the current replay scope, model and prefix hash.
func StampProviderItems(items []Item, current *Provider, model, prefixHash string) []Item {
	result := make([]Item, len(items))
	for index, item := range items {
		result[index] = cloneProviderItem(item)
		result[index].ReplayScope = current.ReplayScope()
		result[index].SourceModel = model
		result[index].PrefixHash = prefixHash
	}
	return result
}

func cloneProviderItem(item Item) Item {
	copy := item
	copy.Data = append(json.RawMessage(nil), item.Data...)
	return copy
}
