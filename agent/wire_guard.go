package agent

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"sync"

	"crude/provider"
)

// WireGuard enforces append-only provider context inside one explicit cache
// epoch. Check never mutates accepted state; Accept commits only after the
// provider returns a successful HTTP response.
type WireGuard struct {
	mu           sync.Mutex
	epoch        uint64
	reason       string
	envelopeHash [32]byte
	hasEnvelope  bool
	accepted     [][]byte
}

type WireCandidate struct {
	epoch        uint64
	envelopeHash [32]byte
	messages     [][]byte
	digest       string
}

func NewWireGuard() *WireGuard { return &WireGuard{epoch: 1, reason: "session_start"} }

func NewWireGuardAt(epoch uint64, reason string) *WireGuard {
	if epoch == 0 {
		epoch = 1
	}
	return &WireGuard{epoch: epoch, reason: reason}
}

func (g *WireGuard) Epoch() uint64 {
	if g == nil {
		return 0
	}
	g.mu.Lock()
	defer g.mu.Unlock()
	return g.epoch
}

func (g *WireGuard) Reason() string {
	if g == nil {
		return ""
	}
	g.mu.Lock()
	defer g.mu.Unlock()
	return g.reason
}

func (g *WireGuard) Rotate(reason string) {
	if g == nil {
		return
	}
	g.mu.Lock()
	g.epoch++
	g.reason = reason
	g.hasEnvelope = false
	g.envelopeHash = [32]byte{}
	g.accepted = nil
	g.mu.Unlock()
}

func (g *WireGuard) EnsureEnvelope(model, systemPrompt string, tools []provider.ToolDef, options ...any) (bool, error) {
	if g == nil {
		return false, nil
	}
	payload, err := json.Marshal(struct {
		Model   string             `json:"model"`
		System  string             `json:"system"`
		Tools   []provider.ToolDef `json:"tools"`
		Options []any              `json:"options,omitempty"`
	}{Model: model, System: systemPrompt, Tools: tools, Options: options})
	if err != nil {
		return false, err
	}
	hash := sha256.Sum256(payload)
	g.mu.Lock()
	defer g.mu.Unlock()
	if !g.hasEnvelope {
		g.envelopeHash = hash
		g.hasEnvelope = true
		return false, nil
	}
	if g.envelopeHash == hash {
		return false, nil
	}
	g.epoch++
	g.reason = "envelope_changed"
	g.envelopeHash = hash
	g.accepted = nil
	return true, nil
}

func (g *WireGuard) Check(messages []provider.ChatMessage) (WireCandidate, error) {
	if g == nil {
		return WireCandidate{}, nil
	}
	encoded := make([][]byte, len(messages))
	hasher := sha256.New()
	for i, message := range messages {
		data, err := json.Marshal(message)
		if err != nil {
			return WireCandidate{}, fmt.Errorf("encode wire message %d: %w", i, err)
		}
		encoded[i] = data
		_, _ = hasher.Write(data)
	}
	g.mu.Lock()
	defer g.mu.Unlock()
	if len(encoded) < len(g.accepted) {
		return WireCandidate{}, fmt.Errorf("wire prefix shrank inside epoch %d: accepted=%d request=%d", g.epoch, len(g.accepted), len(encoded))
	}
	for i := range g.accepted {
		if string(g.accepted[i]) != string(encoded[i]) {
			return WireCandidate{}, fmt.Errorf("wire prefix mutation inside epoch %d at message %d", g.epoch, i)
		}
	}
	return WireCandidate{
		epoch: g.epoch, envelopeHash: g.envelopeHash, messages: encoded,
		digest: hex.EncodeToString(hasher.Sum(nil)[:8]),
	}, nil
}

func (g *WireGuard) Accept(candidate WireCandidate) error {
	if g == nil || candidate.messages == nil {
		return nil
	}
	g.mu.Lock()
	defer g.mu.Unlock()
	if candidate.epoch != g.epoch || candidate.envelopeHash != g.envelopeHash {
		return fmt.Errorf("wire epoch changed before request acceptance")
	}
	g.accepted = make([][]byte, len(candidate.messages))
	for i := range candidate.messages {
		g.accepted[i] = append([]byte(nil), candidate.messages[i]...)
	}
	return nil
}

func (c WireCandidate) Digest() string { return c.digest }
func (c WireCandidate) Epoch() uint64  { return c.epoch }
