package provider

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"strings"
)

// compactExchange sends the two distinct requests in native compaction. The
// replay request takes only the window; it cannot rebase to the original input.
type compactExchange struct {
	provider *Provider
	client   *Client
	request  NativeCheckpointRequest
	codec    compactWire
	path     string
}

func (x *compactExchange) stage(label string) {
	if x.request.Stage != nil {
		x.request.Stage(label)
	}
}
func (x *compactExchange) report(label string, usage TokenUsage, ok bool) {
	if x.request.Usage != nil {
		x.request.Usage(label, usage, ok)
	}
}
func (x *compactExchange) send(ctx context.Context, path string, body []byte) ([]byte, error) {
	if err := x.provider.nativeReady(ctx, x.request.Model); err != nil {
		return nil, err
	}
	c := x.client
	ctx, cancel := c.withTimeout(ctx)
	defer cancel()
	resp, err := c.do(ctx, http.MethodPost, joinPath(c.base, path), body)
	if err != nil {
		return nil, err
	}
	if resp.StatusCode/100 != 2 {
		return nil, readStatusError(resp, c.dial.MaxResponseBytes)
	}
	defer resp.Body.Close()
	data, err := readBounded(resp.Body, c.dial.MaxResponseBytes)
	if err != nil || !strings.Contains(resp.Header.Get("Content-Type"), "text/event-stream") {
		return data, err
	}
	return responsesStreamBody(data)
}

// responsesStreamBody rebuilds the final Responses object from a streamed
// body, for a backend that only streams. Completed output items fill an
// empty final output; an error event is an uncertain provider failure.
func responsesStreamBody(data []byte) ([]byte, error) {
	var final map[string]any
	var items []any
	for _, line := range strings.Split(string(data), "\n") {
		payload, ok := strings.CutPrefix(strings.TrimRight(line, "\r"), "data:")
		if !ok || strings.TrimSpace(payload) == "[DONE]" {
			continue
		}
		var event struct {
			Type     string
			Item     any
			Response map[string]any
			Code     string
			Message  string
		}
		if json.Unmarshal([]byte(strings.TrimSpace(payload)), &event) != nil {
			return nil, errNoWindow
		}
		switch event.Type {
		case "response.output_item.done":
			items = append(items, event.Item)
		case "response.completed", "response.incomplete", "response.failed":
			final = event.Response
		case "error":
			return nil, &StatusError{StatusCode: http.StatusBadGateway, Message: event.Message, APICode: event.Code}
		}
	}
	if final == nil {
		return nil, errNoWindow
	}
	if output, _ := final["output"].([]any); len(output) == 0 {
		final["output"] = items
	}
	return json.Marshal(final)
}
func (x *compactExchange) post(ctx context.Context, q *ChatRequest, directive, label string) (nativeWindow, error) {
	var empty nativeWindow
	body, err := x.codec.compactBody(q, directive, x.client.requestExtras)
	if err != nil {
		return empty, err
	}
	raw, err := json.Marshal(body)
	if err != nil {
		return empty, err
	}
	data, err := x.send(ctx, x.path, raw)
	if err != nil {
		return empty, err
	}
	obj, err := onceJSON(data)
	if err != nil {
		return empty, errNoWindow
	}
	window, u, err := x.codec.compactResult(data)
	if errors.Is(err, errNoWindow) && (obj["status"] == "completed" || obj["stop_reason"] == "end_turn") {
		if plain, e := x.client.proto.DecodeResponse(data); e == nil && len(plain.Choices) == 1 && plain.Choices[0].Message.ContentString() != "" {
			err = errIgnoredCompaction
		}
	}
	x.report(label, u, err == nil)
	return window, err
}
func (x *compactExchange) follow(ctx context.Context, w nativeWindow, instruction string, limit int, label string) (string, error) {
	r := x.request
	msg := nativeMessage(x.provider, r.Model, w, "", NewTextMessage("system", ""))
	msg.nativeReplay = true
	c := *x.client
	c.requestExtras = cloneAnyMap(c.requestExtras)
	if c.proto.Info().Options.Store {
		if c.requestExtras == nil {
			c.requestExtras = map[string]any{}
		}
		c.requestExtras["store"] = false
	}
	// A long read-back must keep bytes moving: silence until a large reply is
	// complete looks like a stalled connection. send rebuilds the final
	// Responses object from the stream; other wires answer in one body.
	q := &ChatRequest{Model: r.Model, Messages: []ChatMessage{msg, NewTextMessage("user", instruction)}, MaxOutputTokens: limit, Stream: nativeStandard(c.proto) == "openai-responses"}
	raw, err := c.encodeChatRequest(q)
	if err != nil {
		return "", err
	}
	// A backend that requires top-level instructions continues under the
	// conversation's own system message rather than an invented one.
	if wire, e := decodeJSONObject(raw); e == nil {
		if v, ok := wire["instructions"].(string); ok && v == "" && len(r.Messages) > 0 && r.Messages[0].Role == "system" {
			wire["instructions"] = r.Messages[0].ContentString()
			if raw, err = json.Marshal(wire); err != nil {
				return "", err
			}
		}
	}
	data, err := x.send(ctx, c.proto.Info().ChatPath, raw)
	if err != nil {
		return "", err
	}
	obj, err := onceJSON(data)
	if err != nil {
		return "", errNoWindow
	}
	terminal := obj["status"] == "completed"
	if c.proto.Info().ID == "anthropic" {
		terminal = obj["stop_reason"] == "end_turn"
	}
	resp, err := c.proto.DecodeResponse(data)
	if err != nil {
		return "", errNoWindow
	}
	u := resp.Usage.Normalized()
	u.ServiceTier = resp.ServiceTier
	valid := terminal && len(resp.Choices) == 1 && len(resp.Choices[0].Message.ToolCalls) == 0 && resp.Choices[0].Message.Refusal == "" && strings.TrimSpace(resp.Choices[0].Message.ContentString()) != ""
	for _, choice := range resp.Choices {
		for _, item := range choice.Message.ProviderItems {
			if item.Type != "message" && item.Type != "reasoning" && item.Type != "text" && item.Type != "thinking" {
				valid = false
			}
		}
	}
	x.report(label, u, valid)
	if !valid {
		if outputLimitReached(obj) {
			return "", errCheckpointOutputLimit
		}
		return "", errors.New("the provider did not finish the checkpoint continuation")
	}
	return strings.TrimSpace(resp.Choices[0].Message.ContentString()), nil
}

// outputLimitReached reports a reply that stopped at its output allowance:
// Responses status incomplete for max_output_tokens, or Messages max_tokens.
func outputLimitReached(obj map[string]any) bool {
	if obj["stop_reason"] == "max_tokens" {
		return true
	}
	details, _ := obj["incomplete_details"].(map[string]any)
	return obj["status"] == "incomplete" && details["reason"] == "max_output_tokens"
}
