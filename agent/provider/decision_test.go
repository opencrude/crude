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
	_, err := p.DecideRelevance(t.Context(), "systemone", "state", []string{"q"})
	if err == nil || !strings.Contains(err.Error(), "retention") {
		t.Fatalf("expected local refusal: %v", err)
	}
}

func TestDecisionContract(t *testing.T) {
	for _, tc := range []struct {
		name, reply string
		ok          bool
	}{
		{"independent", `{"answers":{"m1":{"type":"noul","noul":0.8},"m0":{"type":"noul","noul":0.9}}}`, true},
		{"missing", `{"answers":{"m0":{"type":"noul","noul":1}}}`, false},
		{"range", `{"answers":{"m0":{"type":"noul","noul":1.1},"m1":{"type":"noul","noul":1}}}`, false},
		{"invented", `{"answers":{"m0":{"type":"noul","noul":0},"m5":{"type":"noul","noul":0}}}`, false},
		{"duplicate", `{"answers":{"m0":{"type":"noul","noul":0,"noul":1},"m1":{"type":"noul","noul":0}}}`, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.URL.Path != "/proxy/v1/systemone" || r.Method != "POST" || r.Header.Get("Authorization") != "Bearer fixture" {
					t.Errorf("wrong route or account headers: %s", r.URL.Path)
				}
				var req struct {
					Model, State string
					Questions    map[string]struct{ Type, Instructions string }
				}
				if err := json.NewDecoder(r.Body).Decode(&req); err != nil || req.Model != "decision-model" || req.State != "task" || len(req.Questions) != 2 || req.Questions["m1"].Type != "noul" {
					t.Errorf("wrong decision contract: %+v %v", req, err)
				}
				fmt.Fprint(w, tc.reply)
			}))
			defer srv.Close()
			p := FromPreset("openai-chat", "fixture", srv.URL+"/proxy/v1")
			p.Model = "decision-model"
			scores, err := p.DecideRelevance(context.Background(), "systemone", "task", []string{"a?", "b?"})
			if (err == nil) != tc.ok {
				t.Fatal(scores, err)
			}
			if tc.ok && (len(scores) != 2 || scores[0] != .9 || scores[1] != .8) {
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
