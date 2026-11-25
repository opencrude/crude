// Command providerfixtures records and replays one fixed synthetic text turn.
package main

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"regexp"
	"strings"
)

const prompt = "Reply with exactly OK."
const maxBytes = 1 << 20
const sse = "text/event-stream"
const ndjson = "application/x-ndjson"
const chatBody = `{"model":%q,"messages":[{"role":"user","content":"` + prompt + `"}],"stream":true}`
const fimBody = `{"model":%q,"prompt":"The missing two letters are OK: <","suffix":">","max_tokens":8,"stream":true}`

type scenario struct{ name, base, operation, fixtureModel, body, media string }

var scenarios = []scenario{
	{"chat", "https://api.openai.com/v1", "/chat/completions", "gpt-4o", chatBody, sse},
	{"responses", "https://api.openai.com/v1", "/responses", "gpt-4o", `{"model":%q,"input":"` + prompt + `","max_output_tokens":32,"stream":true,"store":false}`, sse},
	{"codex", "https://chatgpt.com/backend-api/codex", "/responses", "gpt-5-codex", `{"model":%q,"instructions":"` + prompt + `","input":[{"type":"message","role":"user","content":[{"type":"input_text","text":"` + prompt + `"}]}],"store":false,"stream":true}`, sse},
	{"messages", "https://api.anthropic.com/v1", "/messages", "claude-sonnet-4-6", `{"model":%q,"max_tokens":16,"messages":[{"role":"user","content":"` + prompt + `"}],"stream":true}`, sse},
	{"gemini", "https://generativelanguage.googleapis.com/v1beta", "/models/{model}:streamGenerateContent?alt=sse", "gemini-2.5-flash", `{"contents":[{"role":"user","parts":[{"text":"` + prompt + `"}]}],"generationConfig":{"maxOutputTokens":32}}`, sse},
	{"interactions", "https://generativelanguage.googleapis.com/v1beta", "/interactions?alt=sse", "gemini-3.8-flash", `{"model":%q,"input":"` + prompt + `","stream":true,"store":false,"generation_config":{"max_output_tokens":32}}`, sse},
	{"ollama", "http://127.0.0.1:11434/api", "/chat", "fixture:latest", `{"model":%q,"messages":[{"role":"user","content":"` + prompt + `"}],"stream":true,"options":{"num_predict":16}}`, ndjson},
	{"openai-completions", "https://api.openai.com/v1", "/completions", "gpt-3.5-turbo-instruct", fimBody, sse},
	{"deepseek-fim", "https://api.deepseek.com/beta", "/completions", "deepseek-flash", fimBody, sse},
	{"codestral-fim", "https://codestral.mistral.ai/v1", "/fim/completions", "codestral-latest", fimBody, sse},
}
var modelID = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._:-]{0,100}$`)

func lookup(name string) (scenario, error) {
	for _, s := range scenarios {
		if s.name == name {
			return s, nil
		}
	}
	return scenario{}, errors.New("select a supported wire")
}
func (s scenario) request(ctx context.Context, base, model, key string) (*http.Request, error) {
	if !modelID.MatchString(model) {
		return nil, errors.New("use a plain model ID")
	}
	body := s.body
	if strings.Contains(body, "%q") {
		body = fmt.Sprintf(body, model)
	}
	r, err := http.NewRequestWithContext(ctx, "POST", base+strings.ReplaceAll(s.operation, "{model}", model), strings.NewReader(body))
	if err != nil {
		return nil, err
	}
	r.Header.Set("Content-Type", "application/json")
	r.Header.Set("Accept", s.media)
	switch s.name {
	case "messages":
		r.Header.Set("Anthropic-Version", "2023-06-01")
	case "interactions":
		r.Header.Set("Api-Revision", "2026-05-20")
	case "codex":
		r.Header.Set("OpenAI-Beta", "responses=experimental")
		r.Header.Set("originator", "crude")
	}
	if key != "" {
		switch s.name {
		case "messages":
			r.Header.Set("x-api-key", key)
		case "gemini", "interactions":
			r.Header.Set("x-goog-api-key", key)
		default:
			r.Header.Set("Authorization", "Bearer "+key)
		}
	}
	return r, nil
}
func (s scenario) canonical(ctx context.Context) *http.Request {
	r, err := s.request(ctx, "https://fixture.invalid"+s.basePath(), s.fixtureModel, "")
	if err != nil {
		panic(err)
	}
	return r
}

func (s scenario) basePath() string {
	u, err := url.Parse(s.base)
	if err != nil {
		panic(err)
	}
	return u.Path
}
