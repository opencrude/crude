package session

import (
	"context"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"sync"
	"sync/atomic"
	"testing"

	"crude/provider"
	"crude/types/jsondoc"
)

func predictionConfig(endpoint, protocol string) *Config {
	account := provider.Account{
		ID: "prediction-account", ProviderID: "openai", APIKey: "local-fixture-secret",
		Connection: provider.Template{
			Name: "Local only", ProviderURL: endpoint, APIStandard: protocol,
			AuthType: provider.AuthBearer, Headers: map[string]string{"X-Fixture": "kept"},
			Dialer: &provider.DialSettings{MaxAttempts: 6},
		},
	}
	accounts := map[string]provider.Account{account.ID: account}
	return &Config{
		Accounts: accounts, AccountManager: provider.NewAccountManager(accounts, nil),
		Agents: AgentsConfig{ActiveProfile: "prediction", Profiles: map[string]ProfileConfig{
			"prediction": {Categories: map[string]CategoryConfig{
				CategoryAeron: {AccountID: account.ID, Model: "literal-model", ModelMaxOutput: 32},
			}},
		}},
	}
}

func predictionPrepare(t *testing.T, cfg *Config) EditorPredictionCompleter {
	t.Helper()
	binding, err := cfg.AeronPredictionCompleter()
	if err != nil {
		t.Fatal(err)
	}
	return binding
}

func predictionTextRequest() provider.TextOnceRequest {
	return provider.TextOnceRequest{
		Model: "literal-model", System: " system \r\n", Input: " input\t\n",
		MaxOutputTokens: 32, MaxOutputBytes: 1024,
	}
}

func predictionFIMRequest(dialect provider.FIMDialect) provider.FIMOnceRequest {
	return provider.FIMOnceRequest{
		Model: "literal-model", Dialect: dialect, EndpointPath: "/explicit/fim",
		Prefix: " \tα\r\n", Suffix: "\n終  ", MaxOutputTokens: 32, MaxOutputBytes: 1024,
	}
}

const predictionDone = "data: [DONE]\n\n"

func predictionWire(mode, usage string, broken bool) string {
	if mode == "openai-responses" {
		usage = strings.ReplaceAll(strings.ReplaceAll(usage, "prompt_tokens", "input_tokens"), "completion_tokens", "output_tokens")
		if broken {
			return `data: {"type":"response.output_text.delta","delta":"literal"}` + "\n\n" +
				`data: {"type":"response.in_progress","response":{"status":"in_progress","output":[]` + usage + "}}\n\n" +
				"data: {broken}\n\n"
		}
		return `data: {"type":"response.completed","response":{"status":"completed","output":[{"id":"m","type":"message","role":"assistant","content":[{"type":"output_text","text":"literal"}]}]` + usage + "}}\n\n"
	}
	content := `"delta":{"content":"literal"}`
	if mode == string(provider.FIMOpenAI) || mode == string(provider.FIMDeepSeek) {
		content = `"text":"literal"`
	}
	wire := `data: {"choices":[{"index":0,` + content + `,"finish_reason":"stop"}]` + usage + "}\n\n"
	if broken {
		return wire + "data: {broken}\n\n"
	}
	return wire + predictionDone
}

// Independent literal wire oracle.
func predictionWantWire(mode string) string {
	switch mode {
	case "openai":
		return `{"model":"literal-model","stream":true,"max_tokens":32,"tool_choice":"none","stream_options":{"include_usage":true},"messages":[{"role":"system","content":" system \r\n"},{"role":"user","content":" input\t\n"}]}`
	case "openai-responses":
		return `{"model":"literal-model","stream":true,"max_output_tokens":32,"tool_choice":"none","store":true,"input":[{"role":"system","content":[{"type":"input_text","text":" system \r\n"}]},{"role":"user","content":[{"type":"input_text","text":" input\t\n"}]}]}`
	case string(provider.FIMCodestral):
		return `{"model":"literal-model","prompt":" \tα\r\n","suffix":"\n終  ","max_tokens":32,"stream":true}`
	default:
		return `{"model":"literal-model","prompt":" \tα\r\n","suffix":"\n終  ","max_tokens":32,"stream":true,"stream_options":{"include_usage":true}}`
	}
}

func TestEditorPredictionOriginalWireAndUsage(t *testing.T) {
	for _, mode := range []string{"openai", "openai-responses", string(provider.FIMOpenAI), string(provider.FIMDeepSeek), string(provider.FIMCodestral)} {
		for _, usage := range []struct {
			name, raw     string
			input, output int
			present       bool
		}{
			{"absent", "", 0, 0, false},
			{"zero", `,"usage":{"prompt_tokens":0,"completion_tokens":0}`, 0, 0, true},
			{"nonzero", `,"usage":{"prompt_tokens":7,"completion_tokens":2}`, 7, 2, true},
		} {
			for _, broken := range []bool{false, true} {
				t.Run(fmt.Sprintf("%s/%s/broken=%v", mode, usage.name, broken), func(t *testing.T) {
					var calls atomic.Int32
					server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
						calls.Add(1)
						path := "/mount/explicit/fim"
						switch mode {
						case "openai":
							path = "/mount/chat/completions"
						case "openai-responses":
							path = "/mount/responses"
						}
						if r.Method != http.MethodPost || r.URL.RequestURI() != path ||
							r.Header.Get("Authorization") != "Bearer local-fixture-secret" ||
							r.Header.Get("X-Fixture") != "kept" || r.Header.Get("Idempotency-Key") != "" {
							t.Error("request did not retain exact method/path/auth/header contract")
						}
						var got, want map[string]any
						if err := json.NewDecoder(r.Body).Decode(&got); err != nil {
							t.Error(err)
						}
						if err := json.Unmarshal([]byte(predictionWantWire(mode)), &want); err != nil {
							t.Error(err)
						}
						if !reflect.DeepEqual(got, want) {
							t.Errorf("wire = %#v; want %#v", got, want)
						}
						w.Header().Set("Content-Type", "text/event-stream")
						if _, err := io.WriteString(w, predictionWire(mode, usage.raw, broken)); err != nil {
							t.Error(err)
						}
					}))
					defer server.Close()
					protocol := "openai"
					if mode == "openai-responses" {
						protocol = mode
					}
					binding := predictionPrepare(t, predictionConfig(server.URL+"/mount", protocol))
					var text, finish string
					var observed *provider.TokenUsage
					var accepted, complete bool
					var err error
					if mode == "openai" || mode == "openai-responses" {
						var result provider.TextOnceResult
						result, err = binding.CompleteText(t.Context(), predictionTextRequest())
						text, finish, observed, accepted, complete = result.Text, result.FinishReason, result.Usage, result.Accepted, result.UsageComplete
					} else {
						var result provider.FIMOnceResult
						result, err = binding.CompleteFIM(t.Context(), predictionFIMRequest(provider.FIMDialect(mode)))
						text, finish, observed, accepted, complete = result.Text, result.FinishReason, result.Usage, result.Accepted, result.UsageComplete
					}
					wantFinish := "stop"
					if mode == "openai-responses" && broken {
						wantFinish = ""
					}
					if (err != nil) != broken || text != "literal" || finish != wantFinish || !accepted ||
						complete != (usage.present && !broken) || (observed != nil) != usage.present || calls.Load() != 1 {
						t.Fatalf("lost original observations: text=%q finish=%q usage=%+v accepted=%v complete=%v err=%v calls=%d",
							text, finish, observed, accepted, complete, err, calls.Load())
					}
					if observed != nil && (observed.PromptTokens != usage.input || observed.CompletionTokens != usage.output) {
						t.Fatalf("usage changed: %+v", observed)
					}
				})
			}
		}
	}
}

func TestEditorPredictionPreparationRefusals(t *testing.T) {
	off := false
	for _, name := range []string{"nil", "disabled", "disabled inherited", "model", "account", "account ID", "OAuth"} {
		t.Run(name, func(t *testing.T) {
			c := predictionConfig("http://127.0.0.1:1", "openai")
			roles := c.Agents.Profiles["prediction"].Categories
			role := roles[CategoryAeron]
			switch name {
			case "disabled":
				role.Enabled = &off
			case "disabled inherited":
				role = CategoryConfig{Enabled: &off, Inherit: InheritMain}
				roles[CategoryDefault] = CategoryConfig{AccountID: "prediction-account", Model: "literal-model"}
			case "model":
				role.Model = ""
			case "account":
				c.AccountManager.Replace(nil)
			case "account ID":
				role.AccountID = ""
			case "OAuth":
				a, _ := c.AccountManager.Account(role.AccountID)
				a.OAuth = &provider.OAuthCredentials{RefreshToken: "must-not-refresh"}
				c.AccountManager.Replace(map[string]provider.Account{a.ID: a})
			}
			roles[CategoryAeron] = role
			if name == "nil" {
				c = nil
			}
			if _, err := c.AeronPredictionCompleter(); err == nil {
				t.Fatal("unavailable configuration was accepted")
			}
		})
	}
}

func TestEditorPredictionRevokesExactLiveBinding(t *testing.T) {
	var calls atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) { calls.Add(1) }))
	defer server.Close()
	for _, change := range []string{"remove", "reconnect-identical", "credentials", "provider", "url", "auth", "header", "dialer", "protocol", "protocol-settings", "extra", "oauth"} {
		t.Run(change, func(t *testing.T) {
			cfg := predictionConfig(server.URL, "openai")
			binding := predictionPrepare(t, cfg)
			a, _ := cfg.AccountManager.Account(binding.AccountID)
			switch change {
			case "remove":
				cfg.AccountManager.Replace(nil)
			case "reconnect-identical":
				cfg.AccountManager.Replace(nil)
				cfg.AccountManager.Replace(map[string]provider.Account{a.ID: a})
			default:
				switch change {
				case "credentials":
					a.APIKey = "rotated"
				case "provider":
					a.ProviderID = "different"
				case "url":
					a.Connection.ProviderURL += "/different"
				case "auth":
					a.Connection.AuthType = provider.AuthNone
				case "header":
					a.Connection.Headers["X-Fixture"] = "changed"
				case "dialer":
					a.Connection.Dialer.ConnectTimeoutSeconds = 3
				case "protocol":
					a.Connection.APIStandard = "openai-responses"
				case "protocol-settings":
					a.Connection.Protocol = &provider.ProtocolSettings{ZeroDataRetention: true}
				case "extra":
					a.Connection.ExtraBody = jsondoc.Object{"store": false}
				case "oauth":
					a.OAuth = &provider.OAuthCredentials{RefreshToken: "never-refresh"}
				}
				cfg.AccountManager.Replace(map[string]provider.Account{a.ID: a})
			}
			if err := binding.Current(); err == nil {
				t.Fatal("stale binding remained current")
			}
			text, err := binding.CompleteText(t.Context(), predictionTextRequest())
			if err == nil || text.Accepted {
				t.Fatal("text used stale binding")
			}
			fim, err := binding.CompleteFIM(t.Context(), predictionFIMRequest(provider.FIMOpenAI))
			if err == nil || fim.Accepted || calls.Load() != 0 {
				t.Fatal("FIM used stale binding or preflight performed I/O")
			}
		})
	}
}

func TestEditorPredictionImmutableRoleAndManager(t *testing.T) {
	var calls atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		var body map[string]any
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Error(err)
		}
		if body["temperature"] != 0.25 || body["top_p"] != 0.75 || body["reasoning_effort"] != "low" || body["service_tier"] != "priority" {
			t.Errorf("captured request settings changed: %#v", body)
		}
		w.Header().Set("Content-Type", "text/event-stream")
		if _, err := io.WriteString(w, predictionWire("openai", "", false)); err != nil {
			t.Error(err)
		}
	}))
	defer server.Close()
	cfg := predictionConfig(server.URL, "openai")
	temperature, topP := 0.25, 0.75
	role := cfg.Agents.Profiles["prediction"].Categories[CategoryAeron]
	role.ModelTemperature, role.ModelTopP = &temperature, &topP
	role.ThinkingEffort, role.ServiceTier, role.ModelContextWindow = "low", "priority", 8192
	role.AllowedTools, role.SkillModes = []string{"original"}, map[string]string{"original": "disabled"}
	cfg.Agents.Profiles["prediction"].Categories[CategoryAeron] = role
	binding := predictionPrepare(t, cfg)
	originalManager := cfg.AccountManager
	cfg.AccountManager = provider.NewAccountManager(nil, nil)
	temperature, topP = 0.9, 0.1
	role.AllowedTools[0], role.SkillModes["original"] = "mutated", "automatic"
	cfg.Agents.Profiles["prediction"].Categories[CategoryAeron] = CategoryConfig{Model: "other"}
	if binding.role.AllowedTools[0] != "original" || binding.role.SkillModes["original"] != "disabled" ||
		binding.provider.ContextWindow(binding.Model) != 8192 {
		t.Fatal("role snapshot aliases live configuration")
	}
	got, err := binding.CompleteText(t.Context(), predictionTextRequest())
	if err != nil || !got.Accepted || calls.Load() != 1 {
		t.Fatalf("request retargeted to replacement manager: %+v %v", got, err)
	}
	if _, err := binding.CompleteFIM(t.Context(), predictionFIMRequest(provider.FIMOpenAI)); err == nil || calls.Load() != 1 {
		t.Fatal("FIM cleared unsupported role settings")
	}
	originalManager.Replace(nil)
	if err := binding.Current(); err == nil {
		t.Fatal("original manager no longer owns revocation")
	}
}

func TestEditorPredictionIdentityAndSnapshotManager(t *testing.T) {
	cfg := predictionConfig("http://127.0.0.1:1", "openai")
	first := predictionPrepare(t, cfg)
	if decoded, err := hex.DecodeString(first.ConnectionIdentity); err != nil || len(decoded) != 32 {
		t.Fatal("identity is not SHA256 hex")
	}
	a, _ := cfg.AccountManager.Account(first.AccountID)
	a.Label, a.Connection.Name, a.Connection.HelpText = "renamed", "renamed", "new help"
	cfg.AccountManager.Replace(map[string]provider.Account{a.ID: a})
	if err := first.Current(); err != nil {
		t.Fatalf("cosmetics revoked binding: %v", err)
	}
	if second := predictionPrepare(t, cfg); second.ConnectionIdentity != first.ConnectionIdentity {
		t.Fatal("cosmetics changed identity")
	}
	role := cfg.Agents.Profiles["prediction"].Categories[CategoryAeron]
	zero := 0.0
	role.ModelTemperature = &zero
	cfg.Agents.Profiles["prediction"].Categories[CategoryAeron] = role
	if second := predictionPrepare(t, cfg); second.ConnectionIdentity == first.ConnectionIdentity {
		t.Fatal("explicit zero sampling absent from identity")
	}
	cfg.AccountManager = nil
	snapshot := predictionPrepare(t, cfg)
	delete(cfg.Accounts, snapshot.AccountID)
	if err := snapshot.Current(); err != nil {
		t.Fatalf("private snapshot manager read mutable Config: %v", err)
	}
}

func TestEditorPredictionRefusesRequestBeforePOST(t *testing.T) {
	var calls atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) { calls.Add(1) }))
	defer server.Close()
	binding := predictionPrepare(t, predictionConfig(server.URL, "openai"))
	for _, test := range []string{"model", "limit", "canceled", "nil-context", "zero"} {
		t.Run(test, func(t *testing.T) {
			b := binding
			text, fim := predictionTextRequest(), predictionFIMRequest(provider.FIMOpenAI)
			ctx := t.Context()
			switch test {
			case "model":
				text.Model, fim.Model = "different", "different"
			case "limit":
				text.MaxOutputTokens, fim.MaxOutputTokens = 33, 33
			case "canceled":
				canceled, cancel := context.WithCancel(ctx)
				cancel()
				ctx = canceled
			case "nil-context":
				ctx = nil
			case "zero":
				b = EditorPredictionCompleter{}
			}
			gotText, err := b.CompleteText(ctx, text)
			if err == nil || gotText.Accepted {
				t.Fatal("invalid text request accepted")
			}
			if test == "canceled" && !errors.Is(err, context.Canceled) {
				t.Fatalf("cancellation lost: %v", err)
			}
			gotFIM, err := b.CompleteFIM(ctx, fim)
			if err == nil || gotFIM.Accepted || calls.Load() != 0 {
				t.Fatal("invalid FIM request accepted or made a POST")
			}
		})
	}
}

func TestEditorPredictionNoRetryRedirectOrFallback(t *testing.T) {
	for _, fim := range []bool{false, true} {
		for _, status := range []int{429, 503, 404, 307, 308} {
			t.Run(fmt.Sprintf("fim=%v/status=%d", fim, status), func(t *testing.T) {
				var calls, redirected atomic.Int32
				target := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) { redirected.Add(1) }))
				defer target.Close()
				server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
					calls.Add(1)
					path := "/v1/chat/completions"
					if fim {
						path = "/v1/explicit/fim"
					}
					if r.URL.Path != path {
						t.Error("route changed")
					}
					w.Header().Set("Location", target.URL)
					w.Header().Set("Retry-After", "300")
					w.WriteHeader(status)
				}))
				defer server.Close()
				binding := predictionPrepare(t, predictionConfig(server.URL, "openai"))
				var err error
				if fim {
					var result provider.FIMOnceResult
					result, err = binding.CompleteFIM(t.Context(), predictionFIMRequest(provider.FIMDeepSeek))
					if result.Accepted {
						t.Fatal("rejected HTTP response reported accepted")
					}
				} else {
					var result provider.TextOnceResult
					result, err = binding.CompleteText(t.Context(), predictionTextRequest())
					if result.Accepted {
						t.Fatal("rejected HTTP response reported accepted")
					}
				}
				var statusErr *provider.StatusError
				if !errors.As(err, &statusErr) || statusErr.StatusCode != status || calls.Load() != 1 || redirected.Load() != 0 {
					t.Fatalf("single-attempt contract failed: %v calls=%d redirected=%d", err, calls.Load(), redirected.Load())
				}
			})
		}
	}
}

func TestEditorPredictionConcurrentCurrentAndAccountSnapshots(t *testing.T) {
	cfg := predictionConfig("http://127.0.0.1:1", "openai")
	binding := predictionPrepare(t, cfg)
	manager := cfg.AccountManager
	var wg sync.WaitGroup
	wg.Add(2)
	go func() {
		defer wg.Done()
		for range 50 {
			a, _ := manager.Account(binding.AccountID)
			a.Label += "."
			manager.Replace(map[string]provider.Account{a.ID: a})
		}
	}()
	go func() {
		defer wg.Done()
		for range 50 {
			if err := binding.Current(); err != nil {
				t.Errorf("cosmetic snapshot race: %v", err)
			}
		}
	}()
	cfg.Accounts = nil
	cfg.AccountManager = nil
	cfg.Agents.Profiles = nil
	wg.Wait()
}
