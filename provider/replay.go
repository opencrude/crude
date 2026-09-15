package provider

import (
	"crypto/sha256"
	"encoding/binary"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"hash"
	"io"
	"strings"
)

type prefixHasher struct{ state [32]byte }

func newPrefixHasher(tools []ToolDef) prefixHasher {
	encoded, _ := json.Marshal(tools)
	return prefixHasher{state: sha256.Sum256(append([]byte("crude-provider-prefix-v1\x00"), encoded...))}
}

func (h *prefixHasher) add(message ChatMessage) {
	digest := sha256.New()
	_, _ = digest.Write(h.state[:])
	writeHashString(digest, message.Role)
	writeHashString(digest, message.Content.String())
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

func RequestPrefixHash(messages []ChatMessage, tools []ToolDef) string {
	hasher := newPrefixHasher(tools)
	for _, message := range messages {
		hasher.add(message)
	}
	return hasher.value()
}

// FilterReplayMessages returns a request-owned copy. Opaque replay metadata is
// retained only when both its provider scope and immutable request prefix match.
// The canonical transcript is never modified.
func FilterReplayMessages(messages []ChatMessage, tools []ToolDef, current *Provider) []ChatMessage {
	result := make([]ChatMessage, 0, len(messages))
	scope := current.ReplayScope()
	hasher := newPrefixHasher(tools)
	for _, message := range messages {
		copy := message
		copy.ProviderItems = nil
		prefix := hasher.value()
		eligible := message.ReplayScope == scope && message.ReplayPrefixHash != "" && message.ReplayPrefixHash == prefix
		if eligible {
			for _, item := range message.ProviderItems {
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
	return result
}

func StampProviderItems(items []ProviderItem, current *Provider, model, prefixHash string) []ProviderItem {
	result := make([]ProviderItem, len(items))
	for index, item := range items {
		result[index] = cloneProviderItem(item)
		result[index].ReplayScope = current.ReplayScope()
		result[index].SourceModel = model
		result[index].PrefixHash = prefixHash
	}
	return result
}

func cloneProviderItem(item ProviderItem) ProviderItem {
	copy := item
	copy.Data = append(json.RawMessage(nil), item.Data...)
	return copy
}

type replayTool struct {
	ID, Type, Name, Arguments string
}

type streamTranscript struct {
	Content       string
	Reasoning     string
	Tools         map[int]replayTool
	ProviderItems bool
}

func (t *streamTranscript) apply(update StreamUpdate) {
	t.Content += update.ContentDelta
	t.Reasoning += update.ReasoningDelta
	if len(update.ProviderItems) > 0 {
		t.ProviderItems = true
	}
	if len(update.ToolCalls) > 0 && t.Tools == nil {
		t.Tools = make(map[int]replayTool)
	}
	for _, delta := range update.ToolCalls {
		tool := t.Tools[delta.Index]
		if delta.ID != "" {
			tool.ID = delta.ID
		}
		if delta.Type != "" {
			tool.Type = delta.Type
		}
		if delta.Name != "" {
			tool.Name = delta.Name
		}
		if delta.ReplaceArguments {
			tool.Arguments = delta.Arguments
		} else {
			tool.Arguments += delta.Arguments
		}
		t.Tools[delta.Index] = tool
	}
}

func reconcileReplay(emitted streamTranscript, replay *streamTranscript, update StreamUpdate) (StreamUpdate, bool, error) {
	if len(update.ProviderItems) > 0 {
		return StreamUpdate{}, false, fmt.Errorf("stream replay contains opaque provider items")
	}
	replay.apply(update)
	adjusted := update
	adjusted.ContentDelta = ""
	adjusted.ReasoningDelta = ""
	adjusted.ToolCalls = nil
	adjusted.ProviderItems = nil

	content, ahead, err := replaySuffix(emitted.Content, replay.Content, "content")
	if err != nil {
		return StreamUpdate{}, false, err
	}
	adjusted.ContentDelta = content
	reasoning, reasoningAhead, err := replaySuffix(emitted.Reasoning, replay.Reasoning, "reasoning")
	if err != nil {
		return StreamUpdate{}, false, err
	}
	adjusted.ReasoningDelta = reasoning
	ahead = ahead || reasoningAhead

	for index, candidate := range replay.Tools {
		prior := emitted.Tools[index]
		if prior.ID != "" && candidate.ID != "" && prior.ID != candidate.ID {
			return StreamUpdate{}, false, fmt.Errorf("stream replay diverged at tool %d id", index)
		}
		if prior.Name != "" && candidate.Name != "" && prior.Name != candidate.Name {
			return StreamUpdate{}, false, fmt.Errorf("stream replay diverged at tool %d name", index)
		}
		suffix, toolAhead, suffixErr := replaySuffix(prior.Arguments, candidate.Arguments, fmt.Sprintf("tool %d arguments", index))
		if suffixErr != nil {
			return StreamUpdate{}, false, suffixErr
		}
		ahead = ahead || toolAhead || (prior.ID == "" && candidate.ID != "") || (prior.Name == "" && candidate.Name != "")
		if suffix != "" || toolAhead || (prior.ID == "" && candidate.ID != "") || (prior.Name == "" && candidate.Name != "") {
			adjusted.ToolCalls = append(adjusted.ToolCalls, ToolCallDelta{
				Index: index, ID: candidate.ID, Type: candidate.Type, Name: candidate.Name,
				Arguments: suffix,
			})
		}
	}
	for index := range emitted.Tools {
		if _, exists := replay.Tools[index]; !exists {
			ahead = false
		}
	}

	caughtUp := replayMatches(emitted, *replay)
	if !caughtUp && !ahead {
		adjusted.Done = false
		adjusted.FinishReason = ""
		adjusted.Usage = nil
		return adjusted, false, nil
	}
	emit := adjusted.ContentDelta != "" || adjusted.ReasoningDelta != "" || len(adjusted.ToolCalls) > 0 || adjusted.Done
	return adjusted, emit, nil
}

func replaySuffix(emitted, candidate, label string) (suffix string, ahead bool, err error) {
	switch {
	case strings.HasPrefix(emitted, candidate):
		return "", false, nil
	case strings.HasPrefix(candidate, emitted):
		return candidate[len(emitted):], len(candidate) > len(emitted), nil
	default:
		return "", false, fmt.Errorf("stream replay diverged in %s", label)
	}
}

func replayMatches(left, right streamTranscript) bool {
	if left.Content != right.Content || left.Reasoning != right.Reasoning || len(left.Tools) != len(right.Tools) {
		return false
	}
	for index, a := range left.Tools {
		b, ok := right.Tools[index]
		if !ok || a != b {
			return false
		}
	}
	return true
}
