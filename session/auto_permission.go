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
		if settings.Enabled != "on" || settings.Model == "" || settings.AccountID == "" {
			return ask
		}
		unresolved := func(reason string) agent.PermissionReview {
			if settings.UnresolvedMode == "deny" {
				return agent.PermissionReview{Decision: "deny", Reason: reason}
			}
			return ask
		}
		// Existing human grants keep their normal path and incur no review
		// request. Returning ask lets the authoritative permission store
		// resolve the pending prompt, rather than inventing reviewer credit.
		if s.permissionAlreadyGrantedAtRoot(a.WorkDir, tc.Function.Name, params, risk) {
			return ask
		}
		payload, ok := permissionReviewPayload(a.WorkDir, tc.Function.Name, params, risk, settings.MaxInputBytes)
		if !ok {
			return unresolved("Operation is outside the configured automatic review boundary.")
		}
		if len(settings.Instructions) > 1024 {
			return unresolved("Automatic review restrictions exceed the supported input limit.")
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
			return unresolved("Automatic review exhausted its configured technical recovery; the operation was not executed.")
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
		if _, ok := permissionReviewPayload(a.WorkDir, tc.Function.Name, params, risk, settings.MaxInputBytes); !ok {
			return unresolved("Operation scope changed while automatic review was running.")
		}
		if review.Decision == "deny" && settings.DenyMode != "deny" {
			return ask
		}
		if review.Decision == "ask" && settings.UnresolvedMode == "deny" {
			reason := strings.TrimSpace(review.Reason)
			if reason == "" {
				reason = "Automatic reviewer was uncertain; the operation was not executed."
			}
			return agent.PermissionReview{Decision: "deny", Reason: reason}
		}
		return review
	}
}

func runPermissionReviewAttempts(ctx context.Context, cfg *Config, settings types.AutoPermissionSettings, prompt string, payload []byte, a *agent.Agent, emit func(agent.AgentEvent)) (agent.PermissionReview, bool) {
	for attempt := 0; attempt < settings.MaxAttempts; attempt++ {
		if ctx.Err() != nil {
			return agent.PermissionReview{}, false
		}
		p, err := cfg.providerForCategory(CategoryConfig{AccountID: settings.AccountID, Model: settings.Model, ThinkingEffort: settings.ThinkingEffort})
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

// Every exact, non-hard-blocked operation may be reviewed. Secrets and
// oversized payloads stay local and fail closed; never truncate an unseen tail.
func permissionReviewPayload(root, tool string, params map[string]any, risk safety.CommandRisk, limit int) ([]byte, bool) {
	if risk.IsAlwaysBlocked() {
		return nil, false
	}
	if tool == "execute_command" || tool == "async_execute_command" {
		command, _ := params["command"].(string)
		if command == "" || safety.CheckCommandSafety(command, nil).Risk.IsAlwaysBlocked() {
			return nil, false
		}
	}
	payload, err := json.Marshal(struct {
		Tool          string         `json:"tool"`
		Risk          string         `json:"risk"`
		WorkspaceRoot string         `json:"workspace_root,omitempty"`
		Args          map[string]any `json:"arguments"`
	}{tool, risk.String(), root, params})
	if err != nil || len(payload) > limit || reviewContainsSecret(string(payload)) {
		return nil, false
	}
	return payload, true
}

func reviewContainsSecret(value string) bool {
	lower := strings.ToLower(value)
	for _, marker := range []string{".env", ".ssh", ".aws", ".git/", "credentials", "password", "passwd", "api_key", "apikey", "api-key", "access_token", "auth_token", "authorization", "bearer ", "private key", "secret", "sk-", "ghp_", "gho_"} {
		if strings.Contains(lower, marker) {
			return true
		}
	}
	return false
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

func (s *Session) permissionAlreadyGrantedAtRoot(root, tool string, params map[string]any, risk safety.CommandRisk) bool {
	if tool == "apply_patch" {
		return s.patchAlwaysAllowedAtRoot(root, getStrParam(params, "input"))
	}
	if s.permStore == nil {
		return false
	}
	if tool == "execute_command" || tool == "async_execute_command" {
		command, _ := params["command"].(string)
		return s.permStore.IsAlwaysAllowed(safety.CRUDEOp(risk.String()), tool, command) || s.permStore.IsCommandAllowed(&safety.CommandSafetyResult{Risk: risk, BaseCommand: tool})
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
