package session

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"strings"
	"time"

	"crude/agent"
	rootprompt "crude/prompt"
	"crude/provider"
	"crude/safety"
	"crude/types"
	"crude/types/accounting"
)

func (s *Session) bindAgentPermissionPolicy(a *agent.Agent) {
	if s == nil || a == nil {
		return
	}
	a.Yolo = s.YoloEnabled()
	a.YoloMode = s.YoloEnabled
	a.PermissionReviewer = s.permissionReviewer(a)
	a.CommandApprovals = s.permStore
	a.PermissionPolicyChanged = s.permissionPolicyChanged
}

// permissionPolicyChanged returns a channel that closes at the next change of
// the Auto Review settings. Waiting permission requests are then reviewed
// again, so turning Auto Review on answers them.
func (s *Session) permissionPolicyChanged() <-chan struct{} {
	s.permissionPolicyMu.Lock()
	defer s.permissionPolicyMu.Unlock()
	if s.permissionPolicyChange == nil {
		s.permissionPolicyChange = make(chan struct{})
	}
	return s.permissionPolicyChange
}

// publishPermissionPolicyLocked wakes waiting permission requests when the
// effective Auto Review settings changed. Requires s.mu.
func (s *Session) publishPermissionPolicyLocked(before types.AutoPermissionSettings) {
	if s.cfg == nil || s.cfg.Safety.AutoPermission.Normalized() == before {
		return
	}
	s.permissionPolicyMu.Lock()
	defer s.permissionPolicyMu.Unlock()
	if s.permissionPolicyChange != nil {
		close(s.permissionPolicyChange)
		s.permissionPolicyChange = nil
	}
}

// Every root and child owns an independent reviewer instance. There is no
// lifetime call cap or result cache: each exact call receives a fresh review.
func (s *Session) permissionReviewer(a *agent.Agent) agent.PermissionReviewer {
	return func(ctx context.Context, tc provider.ToolCall, params map[string]any, risk safety.CommandRisk, emit func(agent.AgentEvent)) agent.PermissionReview {
		ask := agent.PermissionReview{Decision: "ask"}
		s.mu.Lock()
		cfg := copyConfig(s.cfg)
		settings := s.cfg.Safety.AutoPermission.Normalized()
		revision := s.permissionReviewRevision.Load()
		s.mu.Unlock()
		if settings.Enabled != "on" {
			return ask
		}
		if settings.Model == "" || settings.AccountID == "" {
			return agent.PermissionReview{Decision: "ask", Reason: "Auto Review is on, but it has no model. Choose its account and model in Settings."}
		}
		unresolved := func(reason string) agent.PermissionReview {
			if settings.UnresolvedMode == "deny" {
				return agent.PermissionReview{Decision: "deny", Reason: reason}
			}
			return agent.PermissionReview{Decision: "ask", Reason: reason}
		}
		// Existing human grants keep their normal path and incur no review
		// request. Returning ask lets the authoritative permission store
		// resolve the pending prompt, rather than inventing reviewer credit.
		if s.permissionAlreadyGrantedAtRoot(a.WorkDir, tc.Function.Name, params) {
			return ask
		}
		payload, boundary := permissionReviewPayload(a.WorkDir, tc.Function.Name, params, risk, settings.MaxInputBytes, cfg.credentialValues())
		if boundary != "" {
			return unresolved(boundary)
		}
		if len(settings.Instructions) > 1024 {
			return unresolved("Auto Review did not check this request: its restrictions are longer than 1024 characters. Shorten them in Settings.")
		}
		prompt := rootprompt.PermissionReviewSystem(settings.Instructions)
		review, ok := runPermissionReviewAttempts(ctx, cfg, settings, prompt, payload, a, emit)
		if !ok {
			if ctx.Err() != nil {
				return ask
			}
			s.mu.Lock()
			current := s.cfg.Safety.AutoPermission.Normalized()
			stillCurrent := s.permissionReviewRevision.Load() == revision && current == settings
			s.mu.Unlock()
			if !stillCurrent {
				return ask
			}
			return unresolved("Auto Review did not give a valid answer. Check its model and account in Settings.")
		}
		s.mu.Lock()
		current := s.cfg.Safety.AutoPermission.Normalized()
		stillCurrent := s.permissionReviewRevision.Load() == revision && current == settings
		s.mu.Unlock()
		if !stillCurrent || ctx.Err() != nil {
			return ask
		}
		// Revalidate filesystem scope after the remote wait; a path may have
		// been replaced with a symlink while the reviewer was responding.
		if _, boundary := permissionReviewPayload(a.WorkDir, tc.Function.Name, params, risk, settings.MaxInputBytes, cfg.credentialValues()); boundary != "" {
			return unresolved("The request changed while Auto Review checked it.")
		}
		reason := strings.TrimSpace(review.Reason)
		if review.Decision == "deny" && settings.DenyMode != "deny" {
			return agent.PermissionReview{Decision: "ask", Reason: "Auto Review recommends Deny: " + reason}
		}
		if review.Decision == "ask" && settings.UnresolvedMode == "deny" {
			if reason == "" {
				reason = "Automatic reviewer was uncertain; the operation was not executed."
			}
			return agent.PermissionReview{Decision: "deny", Reason: reason}
		}
		if review.Decision == "ask" {
			return agent.PermissionReview{Decision: "ask", Reason: "Auto Review was not sure: " + reason}
		}
		return review
	}
}

func runPermissionReviewAttempts(ctx context.Context, cfg *Config, settings types.AutoPermissionSettings, prompt string, payload []byte, a *agent.Agent, emit func(agent.AgentEvent)) (agent.PermissionReview, bool) {
	for attempt := 0; attempt < settings.MaxAttempts; attempt++ {
		if ctx.Err() != nil {
			return agent.PermissionReview{}, false
		}
		p, err := cfg.providerForCategory(CategoryConfig{AccountID: settings.AccountID, Model: settings.Model, ThinkingEffort: settings.ThinkingEffort, ServiceTier: settings.ServiceTier})
		if err != nil {
			return agent.PermissionReview{}, false
		}
		// Review requests never inherit arbitrary template extras. Every transport
		// control below comes from the persisted Auto Review Agent settings.
		p.SetExtraBody(nil)
		format := settings.ResponseFormat
		if format == types.AutoPermissionFormatAuto {
			format = types.AutoPermissionFormatPrompt
			if attempt == 0 && p.Protocol() != nil && p.Protocol().Info().ID == "openai" {
				format = types.AutoPermissionFormatJSONSchema
			}
		}
		extra, supported := permissionReviewExtraBody(p, format)
		if !supported {
			return agent.PermissionReview{}, false
		}
		p.SetExtraBody(extra)
		p.SetDialSettings(provider.DialSettings{MaxAttempts: 1, RequestTimeoutSeconds: settings.TimeoutSeconds, ConnectTimeoutSeconds: settings.TimeoutSeconds})
		client, err := p.Client(provider.WithResponseLimit(16 << 10))
		if err != nil {
			continue
		}

		attemptCtx, cancel := context.WithTimeout(ctx, time.Duration(settings.TimeoutSeconds)*time.Second)
		response, requestErr := client.Complete(attemptCtx, &provider.ChatRequest{
			Model:           settings.Model,
			MaxOutputTokens: settings.ReviewOutputTokens(attempt),
			Messages: []provider.ChatMessage{
				provider.NewTextMessage("system", prompt),
				provider.NewTextMessage("user", string(payload)),
			},
		})
		attemptErr := attemptCtx.Err()
		cancel()

		review, valid := permissionReviewResponse(response, requestErr)
		if response != nil && response.Usage != nil {
			emitPermissionReviewUsage(a, p, response.Usage, valid, emit)
		}
		if valid {
			return review, true
		}
		if ctx.Err() != nil || (attemptErr != nil && !errors.Is(attemptErr, context.DeadlineExceeded)) {
			return agent.PermissionReview{}, false
		}
	}
	return agent.PermissionReview{}, false
}

func permissionReviewExtraBody(p *provider.Provider, format string) (map[string]any, bool) {
	switch format {
	case types.AutoPermissionFormatPrompt:
		return nil, true
	case types.AutoPermissionFormatJSONSchema, types.AutoPermissionFormatJSONObject:
		if p == nil || p.Protocol() == nil || p.Protocol().Info().ID != "openai" {
			return nil, false
		}
	}
	if format == types.AutoPermissionFormatJSONObject {
		return map[string]any{"response_format": map[string]any{"type": "json_object"}}, true
	}
	return map[string]any{"response_format": map[string]any{
		"type": "json_schema",
		"json_schema": map[string]any{
			"name":   "permission_review",
			"strict": true,
			"schema": map[string]any{
				"type": "object",
				"properties": map[string]any{
					"decision": map[string]any{"type": "string", "enum": []string{"allow", "deny", "ask"}},
					"reason":   map[string]any{"type": "string"},
				},
				"required":             []string{"decision", "reason"},
				"additionalProperties": false,
			},
		},
	}}, true
}

func permissionReviewResponse(response *provider.ChatResponse, requestErr error) (agent.PermissionReview, bool) {
	if requestErr != nil || response == nil || response.Error != nil || len(response.Choices) != 1 {
		return agent.PermissionReview{}, false
	}
	choice := response.Choices[0]
	if len(choice.Message.ToolCalls) > 0 || !permissionReviewFinished(choice.FinishReason) {
		return agent.PermissionReview{}, false
	}
	return parsePermissionReview(choice.Message.ContentString())
}

func permissionReviewFinished(reason string) bool {
	switch strings.ToLower(strings.TrimSpace(reason)) {
	case "", "stop", "end_turn", "stop_sequence", "completed":
		return true
	default:
		return false
	}
}

// Every exact, non-hard-blocked operation may be reviewed. The reviewer sees
// the arguments exactly as the agent's model wrote them, so only credentials
// that Crude itself stores stay local; oversized payloads also stay local, as
// an unseen tail is never truncated. A refusal returns the reason shown to the
// user, and an empty reason means the payload may be sent.
func permissionReviewPayload(root, tool string, params map[string]any, risk safety.CommandRisk, limit int, credentials []string) ([]byte, string) {
	if risk.IsAlwaysBlocked() {
		return nil, "Auto Review cannot approve a blocked request."
	}
	payload, err := json.Marshal(struct {
		Tool          string         `json:"tool"`
		Risk          string         `json:"risk"`
		WorkspaceRoot string         `json:"workspace_root,omitempty"`
		Args          map[string]any `json:"arguments"`
	}{tool, risk.String(), root, params})
	if err != nil {
		return nil, "Auto Review did not check this request: Crude could not prepare it."
	}
	if len(payload) > limit {
		return nil, fmt.Sprintf("Auto Review did not check this request: it is larger than the %d KB input limit in Settings.", limit>>10)
	}
	for _, credential := range credentials {
		if strings.Contains(string(payload), credential) {
			return nil, "Auto Review did not check this request: it contains a credential that Crude saved for an account."
		}
	}
	return payload, ""
}

// minimumCredentialBytes ignores short placeholder values, which would match
// ordinary text and are not credentials.
const minimumCredentialBytes = 8

// credentialValues returns the account credentials Crude stores: API keys,
// OAuth tokens and connection header values. They never leave Crude in a
// review request.
func (c *Config) credentialValues() []string {
	if c == nil {
		return nil
	}
	accounts := c.Accounts
	if c.AccountManager != nil {
		accounts = c.AccountManager.Snapshot()
	}
	var values []string
	add := func(value string) {
		if len(value) >= minimumCredentialBytes {
			values = append(values, value)
		}
	}
	for _, account := range accounts {
		add(account.APIKey)
		if account.OAuth != nil {
			add(account.OAuth.AccessToken)
			add(account.OAuth.RefreshToken)
			add(account.OAuth.IDToken)
		}
		for _, value := range account.Connection.Headers {
			add(value)
		}
	}
	return values
}

func parsePermissionReview(text string) (agent.PermissionReview, bool) {
	text = strings.TrimSpace(text)
	if strings.HasPrefix(text, "```") {
		lineEnd := strings.IndexByte(text, '\n')
		closeAt := strings.LastIndex(text, "```")
		if lineEnd < 0 || closeAt <= lineEnd || strings.TrimSpace(text[closeAt+3:]) != "" {
			return agent.PermissionReview{}, false
		}
		label := strings.TrimSpace(text[3:lineEnd])
		if label != "" && !strings.EqualFold(label, "json") {
			return agent.PermissionReview{}, false
		}
		text = strings.TrimSpace(text[lineEnd+1 : closeAt])
	}
	var wire struct {
		Decision string `json:"decision"`
		Reason   string `json:"reason"`
	}
	decoder := json.NewDecoder(strings.NewReader(text))
	decoder.DisallowUnknownFields()
	if decoder.Decode(&wire) != nil || decoder.Decode(new(any)) != io.EOF {
		return agent.PermissionReview{}, false
	}
	if wire.Decision != "allow" && wire.Decision != "deny" && wire.Decision != "ask" {
		return agent.PermissionReview{}, false
	}
	if strings.TrimSpace(wire.Reason) == "" || len(wire.Reason) > 512 {
		return agent.PermissionReview{}, false
	}
	return agent.PermissionReview{Decision: wire.Decision, Reason: wire.Reason}, true
}

func emitPermissionReviewUsage(a *agent.Agent, p *provider.Provider, usage *provider.ChatUsage, succeeded bool, emit func(agent.AgentEvent)) {
	outcome := accounting.Failed
	if succeeded {
		outcome = accounting.Succeeded
	}
	normalized := usage.Normalized()
	cached := normalized.PromptTokensCached
	price, known, source := p.RequestPriceKnown(p.Model, normalized.ServiceTier)
	emit(agent.AgentEvent{Type: "usage", Data: map[string]any{
		"request_outcome": string(outcome),
		"request_id":      fmt.Sprintf("permission-review-%s", newMessageID()), "request_kind": "permission_review",
		"provider": p.ID, "account_id": p.AccountID, "model": p.Model, "turn_generation": a.TurnGeneration, "reported_at": time.Now().UnixNano(),
		"prompt": usage.PromptTokens, "completion": usage.CompletionTokens, "prompt_cached": cached,
		"cost": price.CalculateCost(usage.PromptTokens, usage.CompletionTokens, cached), "cost_known": known, "cost_source": source,
		"context_prompt": 0, "context_window": 0, "estimated": false,
		"prompt_written": normalized.PromptTokensWritten, "cache_read_known": normalized.CacheReadKnown, "cache_write_known": normalized.CacheWriteKnown,
		"reasoning": normalized.ReasoningTokens, "reasoning_known": normalized.ReasoningKnown, "reasoning_in_output": normalized.ReasoningInOutput,
		"cost_upper_bound": !normalized.CacheReadKnown,
	}})
}

func (s *Session) permissionAlreadyGrantedAtRoot(root, tool string, params map[string]any) bool {
	if tool == "apply_patch" {
		return s.patchAlwaysAllowedAtRoot(root, getStrParam(params, "input"))
	}
	// Command approvals are checked by the agent before it asks for review.
	if s.permStore == nil {
		return false
	}
	path, _ := params["path"].(string)
	op := safety.OpUpdate
	if tool == "write_file" || tool == "copy_file" {
		op = safety.OpCreate
	}
	if tool == "copy_file" {
		path, _ = params["destination"].(string)
	}
	return s.permStore.IsAlwaysAllowed(op, tool, path)
}
