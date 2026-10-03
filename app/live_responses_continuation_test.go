package app

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"crude/provider"
	"crude/sessiondb"
	"golang.org/x/net/websocket"
)

// Run explicitly with a configured account; ordinary tests never use credentials
// or make network requests.
func TestLiveResponsesStoredContinuation(t *testing.T) {
	accountID := os.Getenv("CRUDE_LIVE_RESPONSES_ACCOUNT")
	model := os.Getenv("CRUDE_LIVE_RESPONSES_MODEL")
	if accountID == "" || model == "" {
		t.Skip("set CRUDE_LIVE_RESPONSES_ACCOUNT and CRUDE_LIVE_RESPONSES_MODEL")
	}
	cfg := FileConfigToSession(LoadFileConfig())
	p, err := cfg.ProviderForAccount(accountID, model)
	if err != nil {
		t.Fatal(err)
	}
	if os.Getenv("CRUDE_LIVE_RESPONSES_FORCE_PROTOCOL") == "1" {
		if err := p.SetProtocolID("openai-responses"); err != nil {
			t.Fatal(err)
		}
	}
	if p.ProtocolID() != "openai-responses" {
		t.Fatalf("selected account uses %q, want openai-responses", p.ProtocolID())
	}
	scope := p.ResponseContinuationScope(model)
	if scope == "" {
		t.Fatal("selected account does not permit stored Responses continuation")
	}

	ctx, cancel := context.WithTimeout(t.Context(), 2*time.Minute)
	defer cancel()
	marker := fmt.Sprintf("CRUDE-CONTINUATION-%d", time.Now().UnixNano())
	stable := strings.Repeat("Stable transport acceptance context.\n", 2048)
	messages := []provider.ChatMessage{
		provider.NewTextMessage("system", "Follow the user's output-format instruction exactly.\n"+stable),
		provider.NewTextMessage("user", "Remember this exact token for the next response: "+marker+". Reply only READY."),
	}
	firstBody, first := liveResponsesRequest(t, ctx, p, provider.ChatRequest{
		Model: model, Messages: messages, Stream: true, ResponseContinuationScope: scope,
	})
	var firstWire struct {
		Store *bool `json:"store"`
	}
	if err := json.Unmarshal(firstBody, &firstWire); err != nil {
		t.Fatal(err)
	}
	if firstWire.Store != nil && !*firstWire.Store {
		t.Fatal("Crude requested store:false")
	}
	if first.reference == nil {
		wsErr := probeResponsesWebSocket(ctx, p)
		conversationErr := probeResponsesConversation(ctx, p)
		t.Fatalf("gateway rejected requested HTTP storage (request_store=true response_reason=%s); WebSocket Responses probe: %v; Conversations probe: %v", first.reason, wsErr, conversationErr)
	}
	assistant := provider.NewTextMessage("assistant", first.text)
	provider.BindResponseReference(&assistant, first.reference)
	messages = append(messages, assistant, provider.NewTextMessage("user", "Reply with only the exact token from my previous message."))

	secondBody, second := liveResponsesRequest(t, ctx, p, provider.ChatRequest{
		Model: model, Messages: messages, Stream: true, ResponseContinuationScope: scope,
	})
	var wire struct {
		PreviousResponseID string            `json:"previous_response_id"`
		Input              []json.RawMessage `json:"input"`
	}
	if err := json.Unmarshal(secondBody, &wire); err != nil {
		t.Fatal(err)
	}
	if wire.PreviousResponseID != first.reference.ID || len(wire.Input) != 1 {
		t.Fatalf("second request was not a one-message delta: previous=%t input_items=%d", wire.PreviousResponseID != "", len(wire.Input))
	}
	if len(secondBody)*4 >= len(firstBody) {
		t.Fatalf("live delta did not materially reduce upload: initial=%d delta=%d", len(firstBody), len(secondBody))
	}
	if strings.TrimSpace(second.text) != marker {
		t.Fatalf("server did not preserve inherited context: got %q want %q", strings.TrimSpace(second.text), marker)
	}
	t.Logf("live Responses continuation verified: initial=%d bytes delta=%d bytes", len(firstBody), len(secondBody))
}

func TestLiveResponsesHistoricalSessionReplay(t *testing.T) {
	path, accountID, model := os.Getenv("CRUDE_LIVE_REPLAY_SESSION"), os.Getenv("CRUDE_LIVE_RESPONSES_ACCOUNT"), os.Getenv("CRUDE_LIVE_RESPONSES_MODEL")
	if path == "" || accountID == "" || model == "" {
		t.Skip("set CRUDE_LIVE_REPLAY_SESSION and the Responses account/model explicitly")
	}
	// Inspect the running session read-only. No session resume, tool execution,
	// persistence mutation, account change, or synthetic historical ID is needed.
	u := url.URL{Scheme: "file", Path: path, RawQuery: "mode=ro"}
	db, err := sql.Open("sqlite", u.String())
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	id := strings.TrimSuffix(filepath.Base(path), ".db")
	state, found, err := sessiondb.LoadContextState(db, id, id)
	if err != nil || !found {
		t.Fatalf("load exact durable context: found=%v err=%v", found, err)
	}
	before, err := json.Marshal(state.Messages)
	if err != nil {
		t.Fatal(err)
	}
	cfg := FileConfigToSession(LoadFileConfig())
	p, err := cfg.ProviderForAccount(accountID, model)
	if err != nil {
		t.Fatal(err)
	}
	if p.ProtocolID() != "openai-responses" {
		t.Fatalf("account uses %s, need Responses", p.ProtocolID())
	}
	messages := provider.FilterReplayMessages(state.Messages, nil, p)
	messages = append(messages, provider.NewTextMessage("user", "This request only verifies historical wire replay. Do not perform earlier tasks or call any tool. Reply exactly REPLAY_OK."))
	ctx, cancel := context.WithTimeout(t.Context(), 2*time.Minute)
	defer cancel()
	body, result := liveResponsesRequest(t, ctx, p, provider.ChatRequest{Model: model, Messages: messages, ToolChoice: "none", MaxOutputTokens: 4096})
	if strings.TrimSpace(result.text) != "REPLAY_OK" {
		t.Fatalf("historical replay did not continue cleanly: %q", result.text)
	}
	after, err := json.Marshal(state.Messages)
	if err != nil || string(before) != string(after) {
		t.Fatal("replay altered the local canonical context")
	}
	t.Logf("live historical session accepted: messages=%d upload=%d bytes; local session unchanged", len(state.Messages), len(body))
}

func probeResponsesConversation(ctx context.Context, p *provider.Provider) error {
	endpoint := strings.TrimRight(p.BaseURL, "/") + "/conversations"
	request, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, strings.NewReader(`{}`))
	if err != nil {
		return err
	}
	headers, err := p.RequestHeaders(ctx)
	if err != nil {
		return err
	}
	request.Header = headers
	request.Header.Set("Content-Type", "application/json")
	response, err := http.DefaultClient.Do(request)
	if err != nil {
		return err
	}
	defer response.Body.Close()
	body, _ := io.ReadAll(io.LimitReader(response.Body, 4096))
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		return fmt.Errorf("HTTP %d", response.StatusCode)
	}
	var created struct {
		ID string `json:"id"`
	}
	if err := json.Unmarshal(body, &created); err != nil || created.ID == "" {
		return fmt.Errorf("created conversation omitted an ID")
	}
	deleteRequest, err := http.NewRequestWithContext(ctx, http.MethodDelete, endpoint+"/"+url.PathEscape(created.ID), nil)
	if err != nil {
		return err
	}
	deleteRequest.Header = headers
	deleted, err := http.DefaultClient.Do(deleteRequest)
	if err != nil {
		return fmt.Errorf("created %s but cleanup failed: %w", created.ID, err)
	}
	defer deleted.Body.Close()
	if deleted.StatusCode < 200 || deleted.StatusCode >= 300 {
		return fmt.Errorf("created conversation but cleanup returned HTTP %d", deleted.StatusCode)
	}
	return nil
}

func probeResponsesWebSocket(ctx context.Context, p *provider.Provider) error {
	endpoint, err := url.Parse(strings.TrimRight(p.BaseURL, "/") + "/responses")
	if err != nil {
		return err
	}
	if endpoint.Scheme == "https" {
		endpoint.Scheme = "wss"
	} else {
		endpoint.Scheme = "ws"
	}
	config, err := websocket.NewConfig(endpoint.String(), p.BaseURL)
	if err != nil {
		return err
	}
	headers, err := p.RequestHeaders(ctx)
	if err != nil {
		return err
	}
	config.Header = headers
	connection, err := websocket.DialConfig(config)
	if err != nil {
		return err
	}
	return connection.Close()
}

type liveResponsesResult struct {
	text      string
	reason    string
	reference *provider.ResponseReference
}

func liveResponsesRequest(t *testing.T, ctx context.Context, p *provider.Provider, request provider.ChatRequest) ([]byte, liveResponsesResult) {
	t.Helper()
	var body []byte
	client, err := p.Client(provider.WithMaxAttempts(1), provider.OnRequestEncoded(func(encoded []byte) {
		body = append([]byte(nil), encoded...)
	}))
	if err != nil {
		t.Fatal(err)
	}
	stream, err := client.Stream(ctx, &request)
	if err != nil {
		t.Fatal(err)
	}
	defer stream.Close()
	var text strings.Builder
	var continuationID, reason string
	for {
		update, err := stream.Next()
		if err != nil {
			t.Fatal(err)
		}
		text.WriteString(update.ContentDelta)
		if update.ContinuationID != "" {
			continuationID = update.ContinuationID
		}
		if update.ContinuationReason != "" {
			reason = update.ContinuationReason
		}
		if update.Done {
			break
		}
	}
	result := liveResponsesResult{text: text.String(), reason: reason}
	if continuationID != "" {
		result.reference = &provider.ResponseReference{ID: continuationID, Scope: request.ResponseContinuationScope,
			InputHash: provider.ResponseInputHash(request.Messages, request.Tools)}
	}
	return body, result
}
