package provider

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"
	"unicode"
	"unicode/utf8"
)

const (
	webSearchBodyLimit        = 1 << 20
	webSearchQueryLimit       = 4 << 10
	webSearchObservationLimit = 512
)

// webSearchError is a complete user-facing message, also shown in the
// latest outcome. It never includes provider-supplied diagnostics.
type webSearchError string

func (e webSearchError) Error() string { return string(e) }

const (
	errWebSearchUnsupported = webSearchError("This account cannot search the web. Choose another account.")
	errWebSearchConnection  = webSearchError("Could not open the search connection. Check the account and Network settings.")
	errWebSearchAuth        = webSearchError("Could not use the account key. Check the account in Settings.")
	errWebSearchReplay      = webSearchError("A request header permits repeat searches. Remove the idempotency header in Settings.")
	errWebSearchRead        = webSearchError("Could not read the search response. Try again.")
	errWebSearchNoSources   = webSearchError("The account did not return search sources. Choose a model that can search the web.")
)

type webSearchTimeoutError struct{}

func (webSearchTimeoutError) Error() string { return "The search did not finish in time." }
func (webSearchTimeoutError) Unwrap() error { return context.DeadlineExceeded }

// WebSearchSource is source evidence from native search, not generated prose.
// Snippet is empty when the protocol does not report an excerpt.
type WebSearchSource struct{ URL, Title, Snippet string }

// WebSearchSupport reads only the connection's protocol declaration. Support
// does not prove that the account or a selected model can complete a search.
func WebSearchSupport(connection Template) (supported, needsModel bool) {
	id, _ := canonicalProtocolID(connection.APIStandard)
	if protocol, ok := ProtocolByID(id); ok {
		switch protocol.Info().WebSearch {
		case WebSearchEndpoint:
			return true, false
		case WebSearchTool:
			return true, true
		}
	}
	return false, false
}

// WebSearchObservation describes the latest search outcome for an account and,
// for hosted tools, the specific model. It contains no query or result content.
type WebSearchObservation struct {
	Working bool
	Detail  string
	Results int
	Latency time.Duration
	At      time.Time
}

type webSearchObservationKey struct{ scope, account, model string }

type webSearchRecord struct {
	observation WebSearchObservation
	sequence    uint64
}

type webSearchObservationStore struct {
	sync.Mutex
	entries  map[webSearchObservationKey]webSearchRecord
	sequence uint64
}

var webSearchObservations = webSearchObservationStore{}

func (p *Provider) webSearchObservationKey(model string) webSearchObservationKey {
	if protocol := p.Protocol(); protocol == nil || protocol.Info().WebSearch != WebSearchTool {
		model = ""
	}
	p.authMu.RLock()
	account := p.AccountID
	p.authMu.RUnlock()
	return webSearchObservationKey{scope: p.ReplayScope(), account: account, model: model}
}

// WebSearchObservation returns the last outcome in this process. Endpoint
// searches are scoped to the account and do not distinguish models.
func (p *Provider) WebSearchObservation(model string) (WebSearchObservation, bool) {
	if p == nil {
		return WebSearchObservation{}, false
	}
	return webSearchObservations.get(p.webSearchObservationKey(model))
}

func (s *webSearchObservationStore) get(key webSearchObservationKey) (WebSearchObservation, bool) {
	s.Lock()
	defer s.Unlock()
	record, ok := s.entries[key]
	return record.observation, ok
}

func (s *webSearchObservationStore) record(key webSearchObservationKey, started time.Time, count int, err error) {
	s.Lock()
	defer s.Unlock()
	at := time.Now()
	observation := WebSearchObservation{Working: err == nil, Results: count, Latency: at.Sub(started), At: at}
	if err != nil {
		observation.Detail = err.Error()
	}
	if s.entries == nil {
		s.entries = make(map[webSearchObservationKey]webSearchRecord)
	}
	if _, exists := s.entries[key]; !exists && len(s.entries) >= webSearchObservationLimit {
		// The time is for display. On a coarse clock many results share it.
		var oldest webSearchObservationKey
		var oldestSequence uint64
		for k, v := range s.entries {
			if oldestSequence == 0 || v.sequence < oldestSequence {
				oldest, oldestSequence = k, v.sequence
			}
		}
		delete(s.entries, oldest)
	}
	s.sequence++
	s.entries[key] = webSearchRecord{observation, s.sequence}
}

// SearchWeb sends one fixed, stateless request through the account's native
// search. The model is required for hosted search tools and ignored for an
// account search endpoint. It does not inherit chat context or request extras.
func (p *Provider) SearchWeb(ctx context.Context, model, query string, limit int) (sources []WebSearchSource, err error) {
	started := time.Now()
	searchCtx, cancel := context.WithTimeout(ctx, 24*time.Second)
	defer cancel()
	var key webSearchObservationKey
	if p != nil {
		key = p.webSearchObservationKey(model)
	}
	defer func() {
		if ctx.Err() == context.Canceled {
			sources, err = nil, context.Canceled
			return
		}
		if searchCtx.Err() == context.DeadlineExceeded || errors.Is(err, context.DeadlineExceeded) {
			sources, err = nil, webSearchTimeoutError{}
		}
		if p != nil {
			webSearchObservations.record(key, started, len(sources), err)
		}
	}()
	if err := searchCtx.Err(); err != nil {
		return nil, err
	}
	if p == nil {
		return nil, errWebSearchUnsupported
	}
	proto := p.Protocol()
	if proto == nil {
		return nil, errWebSearchUnsupported
	}
	info := proto.Info()
	codec, ok := proto.(WebSearchCodec)
	if !ok || (info.WebSearch != WebSearchEndpoint && info.WebSearch != WebSearchTool) ||
		(info.WebSearch == WebSearchEndpoint && info.WebSearchPath == "") {
		return nil, errWebSearchUnsupported
	}
	if info.WebSearch == WebSearchEndpoint {
		model = ""
	} else if len(model) > 1024 || strings.TrimSpace(model) != model || model == "" ||
		!utf8.ValidString(model) || strings.ContainsFunc(model, unicode.IsControl) {
		return nil, webSearchError("The search model is not valid. Choose a model that can search the web.")
	}
	if len(query) > webSearchQueryLimit || strings.TrimSpace(query) == "" ||
		!utf8.ValidString(query) || strings.ContainsRune(query, 0) {
		return nil, webSearchError("Use a non-empty search query of at most 4 KiB of text.")
	}
	if limit < 1 || limit > 10 {
		return nil, webSearchError("Request 1 to 10 search results.")
	}
	if err := onceAuth(p); err != nil {
		return nil, errWebSearchAuth
	}
	if onceReplayHeader(p.authHeader) {
		return nil, errWebSearchReplay
	}
	if err := p.Validate(); err != nil {
		return nil, errWebSearchConnection
	}
	c, err := p.Client(WithMaxAttempts(1), WithResponseLimit(webSearchBodyLimit))
	if err != nil {
		return nil, errWebSearchConnection
	}
	defer c.hc.CloseIdleConnections()
	hc := *c.hc
	hc.CheckRedirect = func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }
	c.hc = &hc
	source := c.headerSource
	c.headerSource = func(ctx context.Context) (http.Header, error) {
		if err := onceAuth(p); err != nil {
			return nil, errWebSearchAuth
		}
		headers, err := source(ctx)
		if err != nil {
			return nil, errWebSearchAuth
		}
		// These headers can make an inference POST replayable by the transport.
		for key := range headers {
			if onceReplayHeader(key) {
				return nil, errWebSearchReplay
			}
		}
		return headers, nil
	}
	var endpoint *url.URL
	if info.WebSearch == WebSearchEndpoint {
		endpoint = decisionURL(c.base, info.WebSearchPath)
	} else {
		endpoint = c.chatURL(&ChatRequest{Model: model, Stream: false})
	}
	body, err := json.Marshal(codec.EncodeWebSearch(model, query, limit))
	if err != nil {
		return nil, webSearchError("Could not prepare the search request. Check the account in Settings.")
	}
	requestCtx, cancelRequest := c.withTimeout(searchCtx)
	defer cancelRequest()
	defer func() {
		if requestCtx.Err() != nil {
			sources, err = nil, requestCtx.Err()
		}
	}()
	resp, err := c.modelPost(requestCtx, endpoint, body)
	if err != nil {
		if errors.Is(err, errWebSearchAuth) {
			return nil, errWebSearchAuth
		}
		if errors.Is(err, errWebSearchReplay) {
			return nil, errWebSearchReplay
		}
		return nil, errWebSearchConnection
	}
	defer resp.Body.Close()
	if resp.StatusCode/100 != 2 {
		// Error bodies can echo the query or account secrets. Do not read them.
		return nil, webSearchStatusError(resp.StatusCode, info.WebSearch == WebSearchTool)
	}
	reply, err := readBounded(resp.Body, c.dial.MaxResponseBytes)
	if err != nil {
		return nil, errWebSearchRead
	}
	sources, err = codec.DecodeWebSearch(reply, limit)
	if info.WebSearch == WebSearchEndpoint && errors.Is(err, errWebSearchNoSources) {
		err = webSearchError("The account did not return usable search sources. Try a different account.")
	}
	return sources, err
}

func webSearchStatusError(status int, needsModel bool) error {
	switch status {
	case http.StatusBadRequest, http.StatusNotFound, http.StatusUnprocessableEntity:
		if needsModel {
			return webSearchError(fmt.Sprintf("The account rejected the search request (HTTP %d). Choose a model that can search the web.", status))
		}
		return webSearchError(fmt.Sprintf("The account rejected the search request (HTTP %d). Check the account in Settings.", status))
	case http.StatusUnauthorized, http.StatusForbidden:
		return webSearchError(fmt.Sprintf("The account refused the key (HTTP %d). Check the account in Settings.", status))
	case http.StatusTooManyRequests:
		return webSearchError("Rate limit or quota reached (HTTP 429).")
	default:
		return webSearchError(fmt.Sprintf("The search failed (HTTP %d). Try again later.", status))
	}
}

// checkedWebSearchSources applies the same source safety and size limits to
// every wire format. An actual empty search is distinct from an unsafe reply.
func checkedWebSearchSources(sources []WebSearchSource, limit int) ([]WebSearchSource, error) {
	if len(sources) == 0 || limit <= 0 {
		return nil, nil
	}
	var results []WebSearchSource
	seen := make(map[string]bool)
	for _, source := range sources {
		if len(source.URL) > 8<<10 || !utf8.ValidString(source.URL) || strings.ContainsFunc(source.URL, unicode.IsControl) {
			continue
		}
		destination, err := url.Parse(source.URL)
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
		source.Title = strings.Join(strings.Fields(source.Title), " ")
		if source.Title == "" || len(source.Title) > 512 || strings.ContainsFunc(source.Title, unicode.IsControl) {
			source.Title = clipSearchText(destination.Hostname(), 512)
		}
		source.Snippet = clipSearchText(strings.Join(strings.Fields(source.Snippet), " "), 600)
		results = append(results, source)
		if len(results) == limit {
			break
		}
	}
	if len(results) == 0 {
		return nil, errWebSearchNoSources
	}
	return results, nil
}

func clipSearchText(s string, limit int) string {
	if len(s) <= limit {
		return s
	}
	for !utf8.RuneStart(s[limit]) {
		limit--
	}
	return s[:limit]
}
