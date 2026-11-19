package provider

import (
	"context"
	"encoding/json"
	"fmt"
	"math"
	"net/http"
	"strings"
)

// DecideRelevance uses the System One typed yes/no contract. The native
// decisions endpoint accepts the same request. Keys are local candidate slots,
// never persistent IDs or instructions supplied by the model.
func (p *Provider) DecideRelevance(ctx context.Context, endpoint, state string, questions []string) ([]float64, error) {
	if p == nil || strings.TrimSpace(p.Model) == "" || (endpoint != "systemone" && endpoint != "decisions") || len(questions) < 1 || len(questions) > 64 || len(state) > 64<<10 {
		return nil, fmt.Errorf("invalid decision request")
	}
	// Typed decisions have no standard retention field. Do not reinterpret a
	// request not to store content as permission to use endpoint defaults.
	if ps := p.protocolSettings; ps != nil && (ps.ZeroDataRetention || ps.Store != nil && !*ps.Store) {
		return nil, fmt.Errorf("decision endpoint cannot enforce the account retention setting")
	}
	if err := onceAuth(p); err != nil {
		return nil, err
	}
	type question struct {
		Type         string `json:"type"`
		Instructions string `json:"instructions"`
	}
	qs := make(map[string]question, len(questions))
	for i, text := range questions {
		if len(text) > 4096 || text == "" {
			return nil, fmt.Errorf("invalid decision question")
		}
		qs[fmt.Sprintf("m%d", i)] = question{"noul", text}
	}
	wire := map[string]any{"model": p.Model, "state": state, "questions": qs}
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
	c, err := p.Client(WithMaxAttempts(1))
	if err != nil {
		return nil, err
	}
	hc := *c.hc
	hc.CheckRedirect = func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }
	c.hc = &hc
	ctx, cancel := c.withTimeout(ctx)
	defer cancel()
	resp, err := c.do(ctx, http.MethodPost, joinPath(c.base, endpoint), body)
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
	answers, ok := obj["answers"].(map[string]any)
	if !ok || len(answers) != len(qs) {
		return nil, fmt.Errorf("incomplete decision answers")
	}
	scores := make([]float64, len(qs))
	for i := range scores {
		a, _ := answers[fmt.Sprintf("m%d", i)].(map[string]any)
		if a["type"] == "refusal" && len(a) == 1 {
			continue
		}
		n, ok := a["noul"].(json.Number)
		if !ok || a["type"] != "noul" {
			return nil, fmt.Errorf("invalid decision answer")
		}
		v, err := n.Float64()
		if err != nil || math.IsNaN(v) || v < 0 || v > 1 {
			return nil, fmt.Errorf("invalid decision probability")
		}
		scores[i] = v
	}
	return scores, nil
}

// ScoreChatRelevance is an explicitly selected no-tools completion on the
// account's configured protocol. Chat scores are estimates, not probabilities
// from a typed decision head. Never switch formats on a failed request.
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
