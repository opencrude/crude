package provider

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestDecisionsRespectAccountRetention(t *testing.T) {
	p := FromPreset("openai-chat", "fixture", "https://unused.invalid")
	p.Model = "rank"
	p.SetProtocolSettings(&ProtocolSettings{ZeroDataRetention: true})
	_, err := p.DecideRelevance(t.Context(), "state", []string{"q"})
	if err == nil || !strings.Contains(err.Error(), "retention") {
		t.Fatalf("expected local refusal: %v", err)
	}
}

// OpenAI-style accounts speak the documented Decisions API: shared input, an
// array of named predicate questions, and an array of named answers.
func TestDecisionContract(t *testing.T) {
	for _, tc := range []struct {
		name, reply string
		ok          bool
	}{
		{"independent", `{"answers":[{"type":"predicate","name":"m1","probability":0.8},{"type":"predicate","name":"m0","probability":0.9}]}`, true},
		{"refused", `{"answers":[{"type":"refusal","name":"m1"},{"type":"predicate","name":"m0","probability":0.9}]}`, true},
		{"missing", `{"answers":[{"type":"predicate","name":"m0","probability":1}]}`, false},
		{"range", `{"answers":[{"type":"predicate","name":"m0","probability":1.1},{"type":"predicate","name":"m1","probability":1}]}`, false},
		{"invented", `{"answers":[{"type":"predicate","name":"m0","probability":0},{"type":"predicate","name":"m5","probability":0}]}`, false},
		{"repeated", `{"answers":[{"type":"predicate","name":"m0","probability":0},{"type":"predicate","name":"m0","probability":0}]}`, false},
		{"duplicate key", `{"answers":[{"type":"predicate","name":"m0","probability":0,"probability":1},{"type":"predicate","name":"m1","probability":0}]}`, false},
		{"systemone shape", `{"answers":{"m0":{"type":"noul","noul":0.9},"m1":{"type":"noul","noul":0.8}}}`, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.URL.Path != "/proxy/v1/decisions" || r.Method != "POST" || r.Header.Get("Authorization") != "Bearer fixture" {
					t.Errorf("wrong route or account headers: %s", r.URL.Path)
				}
				var req struct {
					Model, Input string
					Questions    []struct{ Type, Name, Instructions string }
				}
				if err := json.NewDecoder(r.Body).Decode(&req); err != nil || req.Model != "decision-model" || req.Input != "task" ||
					len(req.Questions) != 2 || req.Questions[0] != (struct{ Type, Name, Instructions string }{"predicate", "m0", "a?"}) || req.Questions[1].Name != "m1" {
					t.Errorf("wrong decision contract: %+v %v", req, err)
				}
				fmt.Fprint(w, tc.reply)
			}))
			defer srv.Close()
			p := FromPreset("openai-chat", "fixture", srv.URL+"/proxy/v1")
			p.Model = "decision-model"
			scores, err := p.DecideRelevance(context.Background(), "task", []string{"a?", "b?"})
			if (err == nil) != tc.ok {
				t.Fatal(scores, err)
			}
			if tc.ok && (len(scores) != 2 || scores[0] != .9 || (tc.name == "independent" && scores[1] != .8) || (tc.name == "refused" && scores[1] != 0)) {
				t.Fatal(scores)
			}
		})
	}
}

func TestExplicitChatRankerValidatesEveryScore(t *testing.T) {
	for _, body := range []string{`{"m1":0,"m0":1}`, `{"m0":1}`, `{"m0":1,"m2":0}`, `{"m0":1,"m1":0,"m1":1}`, `{"m0":1,"m1":-1}`, `{"m0":1,"m1":"0"}`} {
		t.Run(body, func(t *testing.T) {
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.URL.Path != "/v1/chat/completions" {
					t.Error(r.URL.Path)
				}
				var req ChatRequest
				if err := json.NewDecoder(r.Body).Decode(&req); err != nil || req.Model != "rank" || len(req.Tools) > 0 || len(req.Messages) != 2 {
					t.Error("invalid ranking request")
					return
				}
				fmt.Fprintf(w, `{"choices":[{"message":{"role":"assistant","content":%q},"finish_reason":"stop"}]}`, body)
			}))
			defer srv.Close()
			p := FromPreset("openai-chat", "test", srv.URL)
			p.Model = "rank"
			scores, err := p.ScoreChatRelevance(t.Context(), "rate", "data", 2)
			if body == `{"m1":0,"m0":1}` {
				if err != nil || len(scores) != 2 || scores[0] != 1 || scores[1] != 0 {
					t.Fatal(scores, err)
				}
			} else if err == nil {
				t.Fatal("accepted invalid response")
			}
		})
	}
}

// An account made from the Ollama template needs no manual setup: its model
// list marks decision models, and System One is found beside the native /api
// root, also behind a proxy prefix.
func TestOllamaTemplateFindsDecisionModelsAndSystemOne(t *testing.T) {
	var lists int
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/proxy/api/tags":
			lists++
			fmt.Fprint(w, `{"models":[{"name":"clef:27b","capabilities":["decision","vision"]},{"name":"beam","capabilities":["completion","tools"]}]}`)
		case "/proxy/v1/systemone":
			var req struct {
				Questions map[string]DecisionQuestion
			}
			if err := json.NewDecoder(r.Body).Decode(&req); err != nil || req.Questions["d"].Type != "choice" || len(req.Questions["d"].Criteria) != 3 {
				t.Errorf("wrong choice question: %+v %v", req, err)
			}
			fmt.Fprint(w, `{"answers":{"d":{"type":"choice","choice":"deny","confidence":0.96,"probabilities":{"allow":0.01,"deny":0.98,"ask":0.01}}}}`)
		default:
			t.Errorf("unexpected path %s", r.URL.Path)
			http.NotFound(w, r)
		}
	}))
	defer srv.Close()
	p := FromPreset("ollama-cloud", "key", srv.URL+"/proxy/api")
	p.AccountID = "ollama-template-" + t.Name()
	for model, want := range map[string]string{"clef:27b": ModelTypeDecision, "beam": ModelTypeChat} {
		p.Model = model
		got, err := p.ResolveModelType(t.Context(), ModelTypeAutomatic)
		if err != nil || got != want {
			t.Fatalf("%s: type %q, %v; want %q", model, got, err, want)
		}
	}
	if lists != 1 {
		t.Fatalf("model list fetched %d times; want one cached listing", lists)
	}
	p.Model = "beam"
	if got, _ := p.ResolveModelType(t.Context(), ModelTypeDecision); got != ModelTypeDecision {
		t.Fatal("explicit model type was not kept")
	}
	p.Model = "clef:27b"
	answers, err := p.Decide(t.Context(), `{"tool":"execute_command"}`, map[string]DecisionQuestion{"d": {Type: "choice", Instructions: "May it run?", Criteria: map[string]string{"allow": "a", "deny": "d", "ask": "q"}}})
	if err != nil || answers["d"].Choice != "deny" || answers["d"].Probabilities["deny"] != 0.98 {
		t.Fatal(answers, err)
	}
}

// A choice answer must name one of the offered labels with a probability for
// each; anything else is rejected rather than read as a verdict.
// A choice answer must pick one of the supplied values and give exactly one
// probability for each, in either protocol's wire shape.
func TestDecisionChoiceRejectsInventedLabels(t *testing.T) {
	question := map[string]DecisionQuestion{"d": {Type: "choice", Instructions: "?", Criteria: map[string]string{"allow": "a", "deny": "d"}}}
	for _, tc := range []struct {
		preset, path, reply string
		ok                  bool
	}{
		{"openai-chat", "/v1", `{"answers":[{"type":"choice","name":"d","choice":"deny","probabilities":[{"value":"allow","probability":0.1},{"value":"deny","probability":0.9}],"confidence":0.8}]}`, true},
		{"openai-chat", "/v1", `{"answers":[{"type":"choice","name":"d","choice":"run","probabilities":[{"value":"allow","probability":0.5},{"value":"deny","probability":0.5}]}]}`, false},
		{"openai-chat", "/v1", `{"answers":[{"type":"choice","name":"d","choice":"allow","probabilities":[{"value":"allow","probability":0.9},{"value":"allow","probability":0.1}]}]}`, false},
		{"openai-chat", "/v1", `{"answers":[{"type":"predicate","name":"d","probability":0.9}]}`, false},
		{"ollama", "/api", `{"answers":{"d":{"type":"choice","choice":"deny","probabilities":{"allow":0.1,"deny":0.9}}}}`, true},
		{"ollama", "/api", `{"answers":{"d":{"type":"choice","choice":"run","probabilities":{"allow":0.5,"deny":0.5}}}}`, false},
		{"ollama", "/api", `{"answers":{"d":{"type":"choice","choice":"allow","probabilities":{"allow":0.9}}}}`, false},
		{"ollama", "/api", `{"answers":{"d":{"type":"noul","noul":0.9}}}`, false},
	} {
		var sent map[string]any
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			_ = json.NewDecoder(r.Body).Decode(&sent)
			fmt.Fprint(w, tc.reply)
		}))
		p := FromPreset(tc.preset, "key", srv.URL+tc.path)
		p.Model = "judge"
		answers, err := p.Decide(t.Context(), "state", question)
		srv.Close()
		if (err == nil) != tc.ok {
			t.Errorf("%s %s: err=%v", tc.preset, tc.reply, err)
			continue
		}
		if tc.ok && (answers["d"].Choice != "deny" || answers["d"].Probabilities["deny"] != 0.9 || answers["d"].Probabilities["allow"] != 0.1) {
			t.Errorf("%s answer=%+v", tc.preset, answers)
		}
		if tc.preset == "openai-chat" {
			questions, _ := sent["questions"].([]any)
			q, _ := questions[0].(map[string]any)
			choices, _ := q["choices"].([]any)
			if q["type"] != "choice" || q["name"] != "d" || len(choices) != 2 {
				t.Errorf("choice question not sent in the Decisions shape: %v", sent)
			}
		}
	}
}
