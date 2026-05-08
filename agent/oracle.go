package agent

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"time"

	"crude/provider"
)

type OracleQuery struct {
	Task          string
	Context       string
	DecisionPoint string
	Constraints   string
}

type OracleResponse struct {
	Decision     string
	Reasoning    string
	Plan         string
	Confidence   string
	Alternatives string
}

const OracleSystemPrompt = `You are an elite strategic reasoning engine — an architect-level AI specialized in high-stakes technical decisions. You have no tools, no ability to execute code, and no direct access to a filesystem. Your sole purpose is pure first-principles reasoning.

## Your Role
You serve as the architectural decision-maker in a dual-model agent system. A worker model handles all tool execution (reading/writing files, running commands, searching). When the worker encounters a complex problem that exceeds its reasoning depth, it consults you.

## Input Format
You receive a structured query:
TASK: <high-level goal the worker is trying to achieve>
CONTEXT: <relevant codebase information gathered by the worker>
DECISION_POINT: <the specific architectural or strategic decision needed>
CONSTRAINTS: <hard constraints that must be respected>

## Output Format
Respond EXACTLY in this structure:

<DECISION>
Your clear, definitive answer to the decision point. Be specific. No hedging.
</DECISION>

<REASONING>
First-principles reasoning chain. WHY this decision is correct. What tradeoffs you considered. Reference relevant software engineering principles (SOLID, coupling/cohesion, algorithmic complexity, Conway's Law, etc.).
</REASONING>

<PLAN>
Concrete, ordered, actionable steps the worker should execute. Each step should be specific enough that the worker can translate it directly into tool calls. Number them.
</PLAN>

<CONFIDENCE>
high | medium | low — your confidence in this decision
</CONFIDENCE>

<ALTERNATIVES>
What other approaches you considered and WHY you rejected them. This builds audit trail and gives the worker fallback options if needed.
</ALTERNATIVES>

## Principles
- Reason from first principles, not convention
- Surface hidden assumptions
- Prefer simplicity over cleverness (Occam's Razor)
- Consider both immediate needs and future extensibility
- When the decision impacts multiple subsystems, analyze coupling explicitly
- If the task has significant ambiguity, surface it in reasoning rather than guessing`

func buildOracleMessages(query OracleQuery) []provider.ChatMessage {
	userContent := fmt.Sprintf(`TASK: %s

CONTEXT: %s

DECISION_POINT: %s

CONSTRAINTS: %s`,
		query.Task,
		query.Context,
		query.DecisionPoint,
		query.Constraints)

	return []provider.ChatMessage{
		{Role: "system", Content: OracleSystemPrompt},
		{Role: "user", Content: userContent},
	}
}

func ConsultOracle(ctx context.Context, p *provider.Provider, model string, query OracleQuery) (OracleResponse, error) {
	msgs := buildOracleMessages(query)

	req := provider.ChatRequest{
		Model:    model,
		Messages: msgs,
		Stream:   false,
	}

	std := p.Standard()

	body, err := std.EncodeRequest(&req)
	if err != nil {
		return OracleResponse{}, fmt.Errorf("marshal oracle request: %w", err)
	}

	if extra := p.BuildExtraBody(); len(extra) > 0 {
		var merged map[string]any
		if err := json.Unmarshal(body, &merged); err != nil {
			return OracleResponse{}, fmt.Errorf("merge oracle extras: %w", err)
		}
		for k, v := range extra {
			merged[k] = v
		}
		body, err = json.Marshal(merged)
		if err != nil {
			return OracleResponse{}, fmt.Errorf("marshal merged oracle: %w", err)
		}
	}

	client := &http.Client{Timeout: 180 * time.Second}
	endpoint := p.ChatEndpoint()

	for attempt := 1; ; attempt++ {
		select {
		case <-ctx.Done():
			return OracleResponse{}, fmt.Errorf("oracle request cancelled: %w", ctx.Err())
		default:
		}

		httpReq, err := http.NewRequestWithContext(ctx, "POST", endpoint, bytes.NewReader(body))
		if err != nil {
			return OracleResponse{}, fmt.Errorf("create oracle request: %w", err)
		}
		hdr, val := p.Auth()
		httpReq.Header.Set(hdr, val)
		httpReq.Header.Set("Content-Type", "application/json")

		resp, err := client.Do(httpReq)
		if err != nil {
			select {
			case <-time.After(retryBackoff(attempt)):
			case <-ctx.Done():
				return OracleResponse{}, fmt.Errorf("oracle request cancelled during retry: %w", ctx.Err())
			}
			continue
		}

		respBody, readErr := io.ReadAll(resp.Body)
		resp.Body.Close()
		if readErr != nil {
			return OracleResponse{}, fmt.Errorf("read oracle response: %w", readErr)
		}

		if resp.StatusCode != 200 {
			if isRetryable(resp.StatusCode) {
				select {
				case <-time.After(retryBackoff(attempt)):
				case <-ctx.Done():
					return OracleResponse{}, fmt.Errorf("oracle request cancelled during retry: %w", ctx.Err())
				}
				continue
			}
			return OracleResponse{}, fmt.Errorf("oracle API %d: %s", resp.StatusCode, string(respBody))
		}

		chatResp, err := std.DecodeResponse(respBody)
		if err != nil {
			return OracleResponse{}, fmt.Errorf("decode oracle response: %w", err)
		}
		if chatResp.Error != nil {
			return OracleResponse{}, fmt.Errorf("oracle API error: %s", chatResp.Error.Message)
		}
		if len(chatResp.Choices) == 0 {
			return OracleResponse{}, fmt.Errorf("empty oracle response")
		}

		content := chatResp.Choices[0].Message.Content
		return parseOracleResponse(content), nil
	}
}

func FormatOracleResponse(resp OracleResponse) string {
	if resp.Decision == "" && resp.Reasoning == "" {
		return fmt.Sprintf("⚠ Oracle consultation failed. Proceed with your own best judgment.\nResponse: %v", resp)
	}
	var out string
	if resp.Decision != "" {
		out += fmt.Sprintf("ORACLE DECISION:\n%s\n\n", resp.Decision)
	}
	if resp.Reasoning != "" {
		out += fmt.Sprintf("REASONING:\n%s\n\n", resp.Reasoning)
	}
	if resp.Plan != "" {
		out += fmt.Sprintf("PLAN:\n%s\n\n", resp.Plan)
	}
	if resp.Confidence != "" {
		out += fmt.Sprintf("CONFIDENCE: %s\n\n", resp.Confidence)
	}
	if resp.Alternatives != "" {
		out += fmt.Sprintf("ALTERNATIVES CONSIDERED:\n%s", resp.Alternatives)
	}
	return out
}

func parseOracleResponse(content string) OracleResponse {
	return OracleResponse{
		Decision:     extractTag(content, "DECISION"),
		Reasoning:    extractTag(content, "REASONING"),
		Plan:         extractTag(content, "PLAN"),
		Confidence:   extractTag(content, "CONFIDENCE"),
		Alternatives: extractTag(content, "ALTERNATIVES"),
	}
}

func extractTag(content, tag string) string {
	openTag := "<" + tag + ">"
	closeTag := "</" + tag + ">"
	start := indexOf(content, openTag)
	if start < 0 {
		return ""
	}
	start += len(openTag)
	end := indexOf(content, closeTag)
	if end < 0 || end <= start {
		return ""
	}
	s := content[start:end]
	return trimLines(s)
}

func indexOf(s, sub string) int {
	n := len(sub)
	for i := 0; i+n <= len(s); i++ {
		if s[i:i+n] == sub {
			return i
		}
	}
	return -1
}

func trimLines(s string) string {
	for len(s) > 0 && s[0] == '\n' {
		s = s[1:]
	}
	for len(s) > 0 && s[len(s)-1] == '\n' {
		s = s[:len(s)-1]
	}
	return s
}
