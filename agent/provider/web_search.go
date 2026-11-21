package provider

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"
)

const (
	webSearchBodyLimit  = 1 << 20
	webSearchQueryLimit = 4 << 10
)

// WebSearchSource is evidence from a completed, hosted search call. The title
// can be empty; no answer prose is parsed into results.
type WebSearchSource struct {
	URL   string
	Title string
}

// SupportsWebSearchFallback qualifies the connection, not a model name. Only
// the official Responses endpoint and API-key authentication are supported.
// The selected model still has to complete the provider's native search tool.
func SupportsWebSearchFallback(connection Template) bool {
	protocol, _ := canonicalProtocolID(connection.APIStandard)
	return protocol == "openai-responses" && officialWebSearchEndpoint(connection.ProviderURL, connection.AuthType)
}

func officialWebSearchEndpoint(base, auth string) bool {
	endpoint, err := url.Parse(base)
	return err == nil && auth != AuthChatGPT &&
		(auth == "" || auth == AuthProtocol || auth == AuthBearer) &&
		endpoint.Scheme == "https" && strings.EqualFold(endpoint.Host, "api.openai.com") &&
		endpoint.User == nil && (endpoint.Path == "/v1" || endpoint.Path == "/v1/") &&
		endpoint.RawPath == "" && endpoint.RawQuery == "" && !endpoint.ForceQuery &&
		endpoint.Fragment == "" && !strings.Contains(base, "#")
}

// SearchWebOnce posts only the selected model, literal search query, and
// bounded native-tool options. It does not inherit a chat request, custom
// body, continuation, redirects, or retry policy.
func (p *Provider) SearchWebOnce(ctx context.Context, model, query string, limit int) ([]WebSearchSource, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if p == nil || p.ProtocolID() != "openai-responses" || !officialWebSearchEndpoint(p.BaseURL, p.authType) {
		return nil, errors.New("choose an OpenAI Responses account for model fallback")
	}
	if err := onceAuth(p); err != nil {
		return nil, errors.New("choose an OpenAI API key account for model fallback")
	}
	client, err := p.Client(WithMaxAttempts(1), WithResponseLimit(webSearchBodyLimit))
	if err != nil {
		return nil, errors.New("could not open the model fallback connection. Check Accounts and Network settings")
	}
	defer client.hc.CloseIdleConnections()
	// Preserve the bound account's auth, including revocation checks. Only
	// pass headers this operation needs. In particular, do not permit an
	// idempotency key to make an inference POST replayable by the transport.
	headerSource := client.headerSource
	client.headerSource = func(ctx context.Context) (http.Header, error) {
		if err := onceAuth(p); err != nil {
			return nil, err
		}
		headers, err := headerSource(ctx)
		if err != nil {
			return nil, err
		}
		filtered := make(http.Header)
		for _, key := range []string{"Authorization", "OpenAI-Organization", "OpenAI-Project"} {
			for _, value := range headers.Values(key) {
				filtered.Add(key, value)
			}
		}
		if !strings.HasPrefix(filtered.Get("Authorization"), "Bearer ") {
			return nil, errors.New("API key is missing")
		}
		return filtered, nil
	}
	return client.searchWebOnce(ctx, model, query, limit)
}

func (c *Client) searchWebOnce(ctx context.Context, model, query string, limit int) ([]WebSearchSource, error) {
	if len(model) > 1024 || strings.TrimSpace(model) != model || model == "" ||
		!utf8.ValidString(model) || strings.ContainsFunc(model, unicode.IsControl) {
		return nil, errors.New("choose a valid model for web search")
	}
	if len(query) > webSearchQueryLimit || strings.TrimSpace(query) == "" ||
		!utf8.ValidString(query) || strings.ContainsRune(query, 0) {
		return nil, errors.New("use a shorter, non-empty search query")
	}
	if limit < 1 || limit > 10 {
		return nil, errors.New("model fallback accepts 1 to 10 results")
	}
	// Use a fixed, stateless provider tool request. Do not use ChatRequest:
	// it can bring agent settings, arbitrary tools, or conversation state.
	body, err := json.Marshal(map[string]any{
		"model": model, "input": query, "store": false,
		"tools":             []map[string]string{{"type": "web_search", "search_context_size": "low"}},
		"tool_choice":       map[string]string{"type": "web_search"},
		"include":           []string{"web_search_call.action.sources"},
		"max_tool_calls":    1,
		"max_output_tokens": 1024,
	})
	if err != nil {
		return nil, err
	}
	// Bound standalone use too; the tool's caller already imposes a shared
	// deadline over all primary providers plus this one attempt.
	ctx, cancel := context.WithTimeout(ctx, 24*time.Second)
	defer cancel()
	ctx, cancelRequest := c.withTimeout(ctx)
	defer cancelRequest()
	client := *c
	httpClient := *c.hc
	httpClient.CheckRedirect = func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }
	client.hc = &httpClient
	resp, err := client.do(ctx, http.MethodPost, client.chatURL(nil), body)
	if err != nil {
		if ctx.Err() != nil {
			return nil, ctx.Err()
		}
		return nil, errors.New("could not reach OpenAI for model fallback. Check Accounts and Network settings")
	}
	defer resp.Body.Close()
	if resp.StatusCode/100 != 2 {
		// Provider bodies can echo the query or account secrets. Only retain
		// the status and a useful, local explanation.
		switch resp.StatusCode {
		case http.StatusBadRequest, http.StatusNotFound:
			return nil, fmt.Errorf("OpenAI rejected the web search request (HTTP %d). Choose a model that supports web search", resp.StatusCode)
		case http.StatusUnauthorized, http.StatusForbidden:
			return nil, fmt.Errorf("OpenAI refused the account (HTTP %d). Check the account in Settings", resp.StatusCode)
		case http.StatusTooManyRequests:
			return nil, errors.New("OpenAI rate limit or quota reached (HTTP 429)")
		default:
			return nil, fmt.Errorf("OpenAI web search returned HTTP %d", resp.StatusCode)
		}
	}
	reply, err := readBounded(resp.Body, min64(c.dial.MaxResponseBytes, webSearchBodyLimit))
	if ctx.Err() != nil {
		return nil, ctx.Err()
	}
	if err != nil {
		return nil, errors.New("could not read the OpenAI web search response")
	}
	return decodeWebSearchSources(reply, limit)
}

func decodeWebSearchSources(reply []byte, limit int) ([]WebSearchSource, error) {
	var response struct {
		Status            string    `json:"status"`
		Error             *struct{} `json:"error"`
		IncompleteDetails struct {
			Reason string `json:"reason"`
		} `json:"incomplete_details"`
		Output []struct {
			Type   string `json:"type"`
			Status string `json:"status"`
			Action struct {
				Type    string `json:"type"`
				Sources []struct {
					Type string `json:"type"`
					URL  string `json:"url"`
				} `json:"sources"`
			} `json:"action"`
			Content []struct {
				Type        string `json:"type"`
				Annotations []struct {
					Type  string `json:"type"`
					URL   string `json:"url"`
					Title string `json:"title"`
				} `json:"annotations"`
			} `json:"content"`
		} `json:"output"`
	}
	if err := json.Unmarshal(reply, &response); err != nil {
		return nil, errors.New("OpenAI returned an unreadable web search response")
	}
	// A capped generated answer does not invalidate already completed actual
	// search evidence. Other incomplete, failed or unknown responses do.
	if response.Error != nil || (response.Status != "completed" &&
		(response.Status != "incomplete" || response.IncompleteDetails.Reason != "max_output_tokens")) {
		return nil, errors.New("OpenAI did not complete web search")
	}
	titles := make(map[string]string)
	for _, output := range response.Output {
		if output.Type != "message" {
			continue
		}
		for _, part := range output.Content {
			if part.Type != "output_text" {
				continue
			}
			for _, annotation := range part.Annotations {
				if annotation.Type == "url_citation" {
					titles[annotation.URL] = strings.Join(strings.Fields(annotation.Title), " ")
				}
			}
		}
	}
	var sources []WebSearchSource
	observed, hadEntries := false, false
	seen := make(map[string]bool)
	for _, output := range response.Output {
		if output.Type != "web_search_call" || output.Status != "completed" ||
			output.Action.Type != "search" || output.Action.Sources == nil {
			continue
		}
		observed, hadEntries = true, hadEntries || len(output.Action.Sources) != 0
		for _, source := range output.Action.Sources {
			urlText := source.URL
			if source.Type != "url" || len(urlText) > 8<<10 || strings.ContainsFunc(urlText, unicode.IsControl) {
				continue
			}
			destination, err := url.Parse(urlText)
			if err != nil || destination.User != nil || destination.Hostname() == "" ||
				(destination.Scheme != "http" && destination.Scheme != "https") {
				continue
			}
			destination.Fragment, destination.RawFragment = "", ""
			destination.Host = strings.ToLower(destination.Host)
			key := destination.String()
			if seen[key] {
				continue
			}
			seen[key] = true
			title := titles[urlText]
			if title == "" || len(title) > 512 || strings.ContainsFunc(title, unicode.IsControl) {
				title = destination.Hostname()
			}
			sources = append(sources, WebSearchSource{URL: urlText, Title: title})
			if len(sources) == limit {
				return sources, nil
			}
		}
	}
	if !observed || (hadEntries && len(sources) == 0) {
		return nil, errors.New("OpenAI did not return verified search sources. Choose a model that supports web search")
	}
	return sources, nil
}
