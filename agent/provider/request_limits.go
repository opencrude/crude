package provider

import (
	"context"
	"database/sql"
	"errors"
	"net"
	"net/http"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"sync"
	"time"
)

// A model or route often accepts less than it advertises: a gateway caps the
// request body, an account caps tokens per request, a model's real context or
// output ceiling is smaller than its catalog entry. Crude reads the limit from
// the rejection, plans the next request within it, and remembers it for the
// connection with a lifetime, so the next run does not repeat the failure.

// RejectedLimit names the limit one rejected request exceeded.
type RejectedLimit int

const (
	// RejectedContext means the input, or input plus output allowance,
	// exceeds the model's context window or the account's per-request token
	// limit.
	RejectedContext RejectedLimit = iota + 1
	// RejectedOutput means the output token parameter exceeds the model's
	// ceiling.
	RejectedOutput
	// RejectedBody means the request body exceeds a provider or proxy byte
	// limit.
	RejectedBody
)

// RequestRejection is provider evidence that one request was too large.
type RequestRejection struct {
	Limit RejectedLimit
	// Maximum is the stated ceiling: tokens per request for RejectedContext,
	// output tokens for RejectedOutput, bytes for RejectedBody. Zero when the
	// provider did not state it.
	Maximum int
	// Counted is the provider's own count of the request's input tokens, when
	// stated. With Maximum it converts the ceiling into Crude's estimate.
	Counted int
}

// Stated reports whether the provider gave the exact ceiling.
func (r RequestRejection) Stated() bool { return r.Maximum > 0 }

var (
	statedContextLimits = []*regexp.Regexp{
		// OpenAI, vLLM, DeepSeek, OpenRouter: "maximum context length is N tokens".
		regexp.MustCompile(`maximum context length is (?P<max>\d+)`),
		// Anthropic: "prompt is too long: N tokens > M maximum".
		regexp.MustCompile(`(?P<counted>\d+) tokens > (?P<max>\d+) maximum`),
		// Gemini: "The input token count (N) exceeds the maximum number of tokens allowed (M)".
		regexp.MustCompile(`input token count \((?P<counted>\d+)\) exceeds the maximum number of tokens allowed \((?P<max>\d+)\)`),
		// Text Generation Inference: "must be <= M. Given: N `inputs` tokens".
		regexp.MustCompile("must be <= (?P<max>\\d+)\\. given: (?P<counted>\\d+) `?inputs`? tokens"),
		// Cerebras: "Current length is N while limit is M".
		regexp.MustCompile(`current length is (?P<counted>\d+) while limit is (?P<max>\d+)`),
		// LM Studio: "loaded with context length of only M tokens".
		regexp.MustCompile(`context length of only (?P<max>\d+)`),
		// Models that state only an input ceiling.
		regexp.MustCompile(`maximum input length is (?P<max>\d+)`),
		// OpenAI per-request token rate limit: "Limit M, Requested N".
		regexp.MustCompile(`limit (?P<max>\d+), requested (?P<counted>\d+)`),
	}
	statedInputCounts = []*regexp.Regexp{
		regexp.MustCompile(`(?P<counted>\d+) in the messages`),
		regexp.MustCompile(`messages resulted in (?P<counted>\d+) tokens`),
		regexp.MustCompile(`request has (?P<counted>\d+) input tokens`),
		regexp.MustCompile(`(?P<counted>\d+) of text input`),
	}
	statedBodyLimit = regexp.MustCompile(`(\d+) bytes`)
	numbers         = regexp.MustCompile(`\d+`)
)

// Phrases providers and runtimes use for a request beyond the model's
// context. Sources: OpenAI, Anthropic, Gemini, DeepSeek, Ollama, llama.cpp,
// vLLM, TGI, LM Studio, Cerebras and MiniMax error texts, as collected by
// LiteLLM's exception mapping and Roo Code's context error handling.
var contextMarkers = []string{
	"context_length_exceeded", "context_window_exceeded", "maximum context length",
	"context length exceeded", "exceeds the context window", "exceeds model context",
	"exceed context limit", "exceeds the available context size", "exceed_context_size",
	"is longer than the model's context length", "input length exceeds the context length",
	"prompt is too long", "model's maximum context limit", "requested token count exceeds",
	"input tokens exceed the configured limit", "`inputs` tokens + `max_new_tokens` must be",
	"maximum input length is", "context window exceeds limit", "context length of only",
	"reduce the length of the messages",
}

// Phrases for a body beyond a byte limit: HTTP 413 reason phrases of RFC 9110
// and common proxies, Anthropic's request_too_large and Gemini's payload limit.
var bodyMarkers = []string{
	"request entity too large", "payload too large", "content too large", "request_too_large",
	"request body too large", "body too large", "payload size exceeds", "request size exceeds",
	"exceeds the maximum request size", "maximum request body size", "maximum allowed number of bytes",
}

// Output token parameters of the supported wires and common runtimes.
var outputParameters = []string{
	"max_tokens", "max_completion_tokens", "max_output_tokens", "maxoutputtokens",
	"max_new_tokens", "num_predict", "output tokens", "completion tokens",
}

// RequestTooLarge classifies err as a request that exceeded a limit. sent is
// the output token allowance of the rejected request; an output ceiling is
// accepted only when the provider names that parameter.
func RequestTooLarge(err error, sent int) (RequestRejection, bool) {
	var local *ContextWindowError
	if errors.As(err, &local) {
		return RequestRejection{Limit: RejectedContext, Maximum: local.ContextWindow, Counted: local.InputTokens}, true
	}
	status, text, param, ok := rejectionText(err)
	if !ok {
		return RequestRejection{}, false
	}
	switch status {
	case 0, http.StatusBadRequest, http.StatusRequestEntityTooLarge, http.StatusUnprocessableEntity, http.StatusInternalServerError:
	case http.StatusTooManyRequests:
		// A request above the per-minute token limit is never accepted, however
		// long Crude waits: "Request too large for <model> ... tokens per min".
		if !strings.Contains(text, "request too large") {
			return RequestRejection{}, false
		}
		maximum, counted := statedContext(text)
		return RequestRejection{Limit: RejectedContext, Maximum: maximum, Counted: counted}, true
	default:
		return RequestRejection{}, false
	}
	// Gemini words its context rejection without a fixed marker.
	gemini := strings.Contains(text, "input token count") && strings.Contains(text, "exceeds the maximum number of tokens")
	if gemini || containsAny(text, contextMarkers) {
		maximum, counted := statedContext(text)
		return RequestRejection{Limit: RejectedContext, Maximum: maximum, Counted: counted}, true
	}
	if status != http.StatusRequestEntityTooLarge && (slices.Contains(outputParameters, strings.ToLower(param)) || containsAny(text, outputParameters)) &&
		(sent <= 0 || strings.Contains(text, strconv.Itoa(sent)) || param != "") {
		return RequestRejection{Limit: RejectedOutput, Maximum: statedOutput(text, sent)}, true
	}
	if status == http.StatusRequestEntityTooLarge || containsAny(text, bodyMarkers) {
		maximum := 0
		if match := statedBodyLimit.FindStringSubmatch(text); match != nil {
			maximum, _ = strconv.Atoi(match[1])
		}
		return RequestRejection{Limit: RejectedBody, Maximum: maximum}, true
	}
	return RequestRejection{}, false
}

// UnexplainedRejection reports a provider refusal of the request itself (400
// or 422) without a stated cause: no parameter, credential, permission, model,
// policy or quota code. A smaller request is worth one attempt.
func UnexplainedRejection(err error) bool {
	status, text, param, ok := rejectionText(err)
	if !ok || param != "" || (status != http.StatusBadRequest && status != http.StatusUnprocessableEntity) {
		return false
	}
	for _, code := range []string{"api_key", "authentication", "permission", "not_found", "unsupported", "content_policy", "content_filter", "safety", "quota", "billing"} {
		if strings.Contains(text, code) {
			return false
		}
	}
	return true
}

// TimedOut reports a request that stopped on a timeout before any output:
// HTTP 408, a gateway timeout, a transport deadline or an interrupted stream.
// A large request that keeps timing out may fit when it is smaller.
func TimedOut(err error) bool {
	if err == nil || errors.Is(err, context.Canceled) {
		return false
	}
	var status *StatusError
	if errors.As(err, &status) {
		// 524 is the de facto proxy status for an origin that did not answer in time.
		return status.StatusCode == http.StatusRequestTimeout || status.StatusCode == http.StatusGatewayTimeout || status.StatusCode == 524
	}
	var interrupted *StreamInterruptionError
	var transport net.Error
	return errors.Is(err, context.DeadlineExceeded) || errors.As(err, &interrupted) || errors.As(err, &transport) && transport.Timeout()
}

// IsContextWindowExceeded reports provider or local evidence that a request's
// tokens exceed the model's context. A byte limit is not context evidence.
func IsContextWindowExceeded(err error) bool {
	rejection, ok := RequestTooLarge(err, 0)
	return ok && rejection.Limit == RejectedContext
}

func rejectionText(err error) (status int, text, param string, ok bool) {
	var statusErr *StatusError
	var streamed *APIError
	switch {
	case errors.As(err, &statusErr):
		return statusErr.StatusCode, strings.ToLower(statusErr.Code + " " + statusErr.APIType + " " + statusErr.APICode + " " + statusErr.Message), statusErr.Param, true
	case errors.As(err, &streamed):
		code, _ := strconv.Atoi(streamed.Code)
		return code, strings.ToLower(streamed.codes() + " " + streamed.Message), streamed.Param, true
	}
	return 0, "", "", false
}

func statedContext(text string) (maximum, counted int) {
	for _, pattern := range statedContextLimits {
		if match := pattern.FindStringSubmatch(text); match != nil {
			maximum = namedNumber(pattern, match, "max")
			counted = namedNumber(pattern, match, "counted")
			break
		}
	}
	if counted == 0 {
		for _, pattern := range statedInputCounts {
			if match := pattern.FindStringSubmatch(text); match != nil {
				counted = namedNumber(pattern, match, "counted")
				break
			}
		}
	}
	return maximum, counted
}

// statedOutput is the largest number below the sent allowance, less one: a
// stated range can be exclusive ("from 1 (inclusive) to 65537 (exclusive)").
func statedOutput(text string, sent int) int {
	best := 0
	for _, raw := range numbers.FindAllString(text, -1) {
		if n, err := strconv.Atoi(raw); err == nil && n > 1 && (sent <= 0 || n < sent) && n > best {
			best = n
		}
	}
	if best <= 1 {
		return 0
	}
	return best - 1
}

func namedNumber(pattern *regexp.Regexp, match []string, name string) int {
	index := pattern.SubexpIndex(name)
	if index < 0 || index >= len(match) {
		return 0
	}
	n, _ := strconv.Atoi(match[index])
	return n
}

func containsAny(text string, markers []string) bool {
	for _, marker := range markers {
		if strings.Contains(text, marker) {
			return true
		}
	}
	return false
}

// LearnedLimit names one remembered request limit of a connection and model.
type LearnedLimit string

const (
	// LearnedWindow is the context window, in Crude's token estimate, derived
	// from a ceiling the provider stated.
	LearnedWindow LearnedLimit = "window"
	// LearnedInput is the largest estimated input a request may carry.
	LearnedInput LearnedLimit = "input"
	// LearnedOutput is the largest output allowance the model accepts.
	LearnedOutput LearnedLimit = "output"
	// LearnedBytes is the largest estimated request body the route accepts.
	LearnedBytes LearnedLimit = "bytes"
	// LearnedSectioned records that one request for all checkpoint sections
	// failed, so each section is extracted on its own.
	LearnedSectioned LearnedLimit = "sectioned"
)

const (
	// StatedLimitLifetime keeps a ceiling the provider stated or proved.
	StatedLimitLifetime = 7 * 24 * time.Hour
	// InferredLimitLifetime keeps a ceiling Crude inferred from a recovery.
	// It expires sooner, so a wrong guess costs at most one failure a day.
	InferredLimitLifetime = 24 * time.Hour
)

// LearnedLimits are the unexpired limits remembered for one connection and
// model. A zero value means no limit is known.
type LearnedLimits struct {
	Window, Input, Output, Bytes int
	Sectioned                    bool
}

type learnedLimitKey struct {
	profile, scope, account, model string
	kind                           LearnedLimit
}

type learnedLimitValue struct {
	value   int
	expires time.Time
}

// learnedLimitStore keeps learned limits in memory; the app's model catalog
// mirrors them to disk. Reads never touch the database.
type learnedLimitStore struct {
	mu      sync.Mutex
	entries map[learnedLimitKey]learnedLimitValue
	db      *sql.DB
}

var transientLearnedLimits = &learnedLimitStore{entries: make(map[learnedLimitKey]learnedLimitValue)}

func activeLearnedLimits() *learnedLimitStore {
	if catalog := activeModelCatalog.Load(); catalog != nil {
		return &catalog.limits
	}
	return transientLearnedLimits
}

func (s *learnedLimitStore) load(db *sql.DB) error {
	rows, err := db.Query(`SELECT agent_profile,provider_scope,account_id,model_id,kind,value,expires FROM learned_request_limits WHERE expires>?`, time.Now().Unix())
	if err != nil {
		return err
	}
	defer rows.Close()
	entries := make(map[learnedLimitKey]learnedLimitValue)
	for rows.Next() {
		var key learnedLimitKey
		var kind string
		var value, expires int64
		if err := rows.Scan(&key.profile, &key.scope, &key.account, &key.model, &kind, &value, &expires); err != nil {
			return err
		}
		key.kind = LearnedLimit(kind)
		entries[key] = learnedLimitValue{value: int(value), expires: time.Unix(expires, 0)}
	}
	if err := rows.Err(); err != nil {
		return err
	}
	s.mu.Lock()
	s.entries, s.db = entries, db
	s.mu.Unlock()
	return nil
}

// LearnedRequestLimits returns the unexpired limits Crude learned for this
// agent profile, connection (endpoint, account, protocol) and model.
func LearnedRequestLimits(agentProfile string, connection *Provider, model string) LearnedLimits {
	if connection == nil || model == "" {
		return LearnedLimits{}
	}
	store := activeLearnedLimits()
	now := time.Now()
	key := learnedLimitKey{profile: agentProfile, scope: connection.ReplayScope(), account: connection.AccountID, model: model}
	value := func(kind LearnedLimit) int {
		key.kind = kind
		entry, ok := store.entries[key]
		if !ok || !now.Before(entry.expires) {
			return 0
		}
		return entry.value
	}
	store.mu.Lock()
	defer store.mu.Unlock()
	return LearnedLimits{
		Window: value(LearnedWindow), Input: value(LearnedInput), Output: value(LearnedOutput),
		Bytes: value(LearnedBytes), Sectioned: value(LearnedSectioned) > 0,
	}
}

// RecordRequestLimit remembers an upper bound for lifetime. A later, higher
// observation never raises an unexpired bound; an expired one is replaced.
func RecordRequestLimit(agentProfile string, connection *Provider, model string, kind LearnedLimit, value int, lifetime time.Duration) error {
	if connection == nil || model == "" || value <= 0 || lifetime <= 0 {
		return nil
	}
	store := activeLearnedLimits()
	now := time.Now()
	key := learnedLimitKey{profile: agentProfile, scope: connection.ReplayScope(), account: connection.AccountID, model: model, kind: kind}
	store.mu.Lock()
	defer store.mu.Unlock()
	if current, ok := store.entries[key]; ok && now.Before(current.expires) && current.value <= value {
		return nil
	}
	entry := learnedLimitValue{value: value, expires: now.Add(lifetime)}
	if store.entries == nil {
		store.entries = make(map[learnedLimitKey]learnedLimitValue)
	}
	store.entries[key] = entry
	if store.db == nil {
		return nil
	}
	if _, err := store.db.Exec(`INSERT INTO learned_request_limits(agent_profile,provider_scope,account_id,model_id,kind,value,expires)
 VALUES(?,?,?,?,?,?,?) ON CONFLICT(agent_profile,provider_scope,account_id,model_id,kind) DO UPDATE SET value=excluded.value, expires=excluded.expires`,
		key.profile, key.scope, key.account, key.model, string(kind), value, entry.expires.Unix()); err != nil {
		return err
	}
	_, err := store.db.Exec(`DELETE FROM learned_request_limits WHERE expires<=?`, now.Unix())
	return err
}

// forget removes every limit learned for account, so the next request
// rediscovers them.
func (s *learnedLimitStore) forget(account string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	for key := range s.entries {
		if key.account == account {
			delete(s.entries, key)
		}
	}
	if s.db == nil {
		return nil
	}
	_, err := s.db.Exec(`DELETE FROM learned_request_limits WHERE account_id=?`, account)
	return err
}
