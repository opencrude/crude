package provider

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"net/http"
	"net/url"
	"slices"
	"strings"
	"sync"
	"time"
)

// Model types for the bounded judge requests (memory ranking and Auto
// Review). Automatic asks the account's model list; a model is a decision
// model only when that list says so.
const (
	ModelTypeAutomatic = ""
	ModelTypeChat      = "chat"
	ModelTypeDecision  = "decision"
)

// DecisionQuestion is one typed question about a shared state. Keys are local
// slots chosen by Crude, never IDs or instructions supplied by a model.
type DecisionQuestion struct {
	Type         string            `json:"type"`
	Instructions string            `json:"instructions"`
	Criteria     map[string]string `json:"criteria,omitempty"`
}

// DecisionAnswer is a validated typed answer. Refused is true when the model
// declined to answer; then no other field carries a value.
type DecisionAnswer struct {
	Noul          float64
	Choice        string
	Probabilities map[string]float64
	Refused       bool
}

// decisionURL resolves the protocol's decision path against the account's
// API base with RFC 3986 rules, so a proxy prefix is kept.
func decisionURL(base *url.URL, path string) *url.URL {
	dir := *base
	dir.Path = strings.TrimRight(dir.Path, "/") + "/"
	dir.RawPath = ""
	return dir.ResolveReference(&url.URL{Path: path})
}

// Decide sends typed questions to the protocol's decision endpoint. It never
// switches to chat or another endpoint when the request fails.
func (p *Provider) Decide(ctx context.Context, state string, questions map[string]DecisionQuestion) (map[string]DecisionAnswer, error) {
	if p == nil || strings.TrimSpace(p.Model) == "" || len(questions) < 1 || len(questions) > 64 || strings.TrimSpace(state) == "" || len(state) > 64<<10 {
		return nil, fmt.Errorf("invalid decision request")
	}
	for key, q := range questions {
		if key == "" || len(key) > 100 || q.Instructions == "" || len(q.Instructions) > 4096 {
			return nil, fmt.Errorf("invalid decision question")
		}
		switch q.Type {
		case "noul":
		case "choice":
			if len(q.Criteria) < 2 || len(q.Criteria) > 26 {
				return nil, fmt.Errorf("invalid decision question")
			}
		default:
			return nil, fmt.Errorf("invalid decision question")
		}
	}
	// Typed decisions have no standard retention field. Do not reinterpret a
	// request not to store content as permission to use endpoint defaults.
	if ps := p.protocolSettings; ps != nil && (ps.ZeroDataRetention || ps.Store != nil && !*ps.Store) {
		return nil, fmt.Errorf("decision endpoint cannot enforce the account retention setting")
	}
	if err := onceAuth(p); err != nil {
		return nil, err
	}
	c, err := p.Client(WithMaxAttempts(1))
	if err != nil {
		return nil, err
	}
	path := c.proto.Info().DecisionPath
	codec, typed := c.proto.(DecisionCodec)
	if path == "" || !typed {
		return nil, fmt.Errorf("this account cannot use decision models. Choose Chat as the model type")
	}
	wire := codec.EncodeDecision(p.Model, state, questions)
	// Do not silently discard account routing or privacy constraints. A gateway
	// that cannot honor them must refuse rather than run a weaker request.
	for key, v := range p.extraBody {
		if key != "provider" {
			return nil, fmt.Errorf("account has unsupported decision request fields")
		}
		wire[key] = v
	}
	body, err := json.Marshal(wire)
	if err != nil {
		return nil, err
	}
	hc := *c.hc
	hc.CheckRedirect = func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }
	c.hc = &hc
	ctx, cancel := c.withTimeout(ctx)
	defer cancel()
	resp, err := c.do(ctx, http.MethodPost, decisionURL(c.base, path), body)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode/100 != 2 {
		return nil, fmt.Errorf("decision endpoint returned HTTP %d", resp.StatusCode)
	}
	raw, err := readBounded(resp.Body, 64<<10)
	if err != nil {
		return nil, err
	}
	// onceJSON rejects duplicate keys and trailing data before accepting scores.
	obj, err := onceJSON(raw)
	if err != nil {
		return nil, err
	}
	return codec.DecodeDecision(obj, questions)
}

func probability(v any) (float64, error) {
	n, ok := v.(json.Number)
	if !ok {
		return 0, fmt.Errorf("invalid decision answer")
	}
	f, err := n.Float64()
	if err != nil || math.IsNaN(f) || f < 0 || f > 1 {
		return 0, fmt.Errorf("invalid decision probability")
	}
	return f, nil
}

// DecideRelevance asks one yes/no relevance question per candidate and
// returns the probability of yes in question order. A refused answer scores 0.
func (p *Provider) DecideRelevance(ctx context.Context, state string, questions []string) ([]float64, error) {
	if len(questions) < 1 || len(questions) > 64 {
		return nil, fmt.Errorf("invalid decision request")
	}
	typed := make(map[string]DecisionQuestion, len(questions))
	for i, text := range questions {
		typed[fmt.Sprintf("m%d", i)] = DecisionQuestion{Type: "noul", Instructions: text}
	}
	answers, err := p.Decide(ctx, state, typed)
	if err != nil {
		return nil, err
	}
	scores := make([]float64, len(questions))
	for i := range scores {
		scores[i] = answers[fmt.Sprintf("m%d", i)].Noul
	}
	return scores, nil
}

// ScoreChatRelevance is a no-tools completion on the account's configured
// protocol. Chat scores are estimates, not probabilities from a typed
// decision head. Never switch formats on a failed request.
func (p *Provider) ScoreChatRelevance(ctx context.Context, system, state string, count int) ([]float64, error) {
	if p == nil || strings.TrimSpace(p.Model) == "" || count < 1 || count > 64 || len(state) > 64<<10 {
		return nil, fmt.Errorf("invalid ranking request")
	}
	if err := onceAuth(p); err != nil {
		return nil, err
	}
	for key := range p.extraBody {
		if key != "provider" {
			return nil, fmt.Errorf("account has unsupported ranking request fields")
		}
	}
	c, err := p.Client(WithMaxAttempts(1), WithResponseLimit(64<<10))
	if err != nil {
		return nil, err
	}
	hc := *c.hc
	hc.CheckRedirect = func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }
	c.hc = &hc
	r, err := c.Complete(ctx, &ChatRequest{Model: p.Model, MaxOutputTokens: 384, Messages: []ChatMessage{NewTextMessage("system", system), NewTextMessage("user", state)}})
	if err != nil {
		return nil, err
	}
	if len(r.Choices) != 1 || r.Choices[0].FinishReason != "stop" || len(r.Choices[0].Message.ToolCalls) > 0 || r.Choices[0].Message.Refusal != "" {
		return nil, fmt.Errorf("incomplete ranking reply")
	}
	obj, err := onceJSON([]byte(r.Choices[0].Message.ContentString()))
	if err != nil || len(obj) != count {
		return nil, fmt.Errorf("invalid ranking scores")
	}
	scores := make([]float64, count)
	for i := range scores {
		n, ok := obj[fmt.Sprintf("m%d", i)].(json.Number)
		if !ok {
			return nil, fmt.Errorf("missing ranking score")
		}
		v, err := n.Float64()
		if err != nil || v < 0 || v > 1 {
			return nil, fmt.Errorf("invalid ranking score")
		}
		scores[i] = v
	}
	return scores, nil
}

// modelTypeCache keeps the account's decision-model set briefly, so judge
// requests do not list models every time. A failed listing is kept for a short
// time only, and is never treated as proof that a model is a decision model.
var modelTypeCache = struct {
	sync.Mutex
	entries map[string]modelTypeEntry
}{entries: map[string]modelTypeEntry{}}

type modelTypeEntry struct {
	decision []string
	expires  time.Time
}

const (
	modelTypeTTL       = 10 * time.Minute
	modelTypeFailedTTL = time.Minute
)

// ResolveModelType returns the model type to use. An explicit choice wins.
// Automatic uses the account's model list when its protocol marks decision
// models: only a marked model uses the decision endpoint. Every other model,
// and every protocol whose list cannot mark one, uses chat with no listing.
func (p *Provider) ResolveModelType(ctx context.Context, choice string) (string, error) {
	switch choice {
	case ModelTypeChat, ModelTypeDecision:
		return choice, nil
	case ModelTypeAutomatic:
	default:
		return "", fmt.Errorf("choose Automatic, Chat or Decision as the model type")
	}
	if p == nil || p.Model == "" {
		return "", fmt.Errorf("invalid model")
	}
	c, err := p.Client(WithMaxAttempts(1))
	if err != nil {
		return "", err
	}
	if info := c.proto.Info(); info.DecisionPath == "" || !info.ListsDecisionModels {
		return ModelTypeChat, nil
	}
	key := p.AccountID + "\x00" + c.base.String() + "\x00" + c.proto.Info().ID
	modelTypeCache.Lock()
	entry, ok := modelTypeCache.entries[key]
	modelTypeCache.Unlock()
	if !ok || time.Now().After(entry.expires) {
		entry = modelTypeEntry{expires: time.Now().Add(modelTypeTTL)}
		models, err := c.ListModels(ctx)
		if err != nil {
			if ctx.Err() != nil {
				return "", ctx.Err()
			}
			if !errors.Is(err, ErrModelsUnsupported) {
				entry.expires = time.Now().Add(modelTypeFailedTTL)
			}
		}
		for _, m := range models {
			if m.Decision {
				entry.decision = append(entry.decision, m.ID)
			}
		}
		modelTypeCache.Lock()
		modelTypeCache.entries[key] = entry
		modelTypeCache.Unlock()
	}
	if slices.Contains(entry.decision, p.Model) {
		return ModelTypeDecision, nil
	}
	return ModelTypeChat, nil
}
