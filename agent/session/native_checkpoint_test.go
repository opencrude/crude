package session

import (
	"bytes"
	"encoding/json"
	"strings"
	"sync/atomic"
	"testing"

	"crude/agent/provider"
	"crude/agent/sessiondb"
	"crude/internal/providersim"
)

// Exercise the session operation, not just a codec: no Errand binding, exact
// archival, durable portable text plus opaque item, attribution and passive resume.
func TestManualNativeCheckpointWithoutErrand(t *testing.T) {
	const model = "claude-sonnet-4-6"
	var real, follow atomic.Int32
	sim := providersim.New(t)
	sim.Respond(func(request providersim.Request) providersim.Turn {
		if request.Model != model || request.Path != "/v1/messages" {
			t.Error("used another model or endpoint")
		}
		if simRawRequest(request)["compaction"] != nil {
			real.Add(1)
			if !bytes.Contains(request.Raw, []byte("build-evidence-27")) {
				t.Error("native operation missed the source")
			}
			return providersim.Text("build-evidence-27").WithUsage(providersim.Usage{Input: 11000, Output: 200})
		}
		follow.Add(1)
		if !bytes.Contains(request.Raw, []byte(`"signature":`)) || bytes.Contains(request.Raw, []byte("raw tool body")) {
			t.Error("verification did not use the signed block")
		}
		return providersim.Text("<<<intent>>>\nFix the build\n<<<work>>>\nRead the test\n<<<state>>>\nbuild-evidence-27\n<<<continuation>>>\nRun the focused test").
			WithUsage(providersim.Usage{Input: 299, Output: 60})
	})
	history := []provider.ChatMessage{provider.NewTextMessage("assistant", strings.Repeat("raw tool body ", 3000)+"build-evidence-27"), provider.NewTextMessage("user", "Fix the build")}
	s, path := newAdaptiveCompactionSession(t, sim.BaseURL(providersim.Messages), 20000, history)
	account := s.cfg.Accounts["account"]
	account.Connection.APIStandard = "anthropic"
	account.Connection.Protocol = &provider.ProtocolSettings{Compaction: provider.CompactionProviderOnly}
	s.cfg.Accounts["account"] = account
	profile := s.cfg.Agents.Profiles[s.cfg.Agents.ActiveProfile]
	main := profile.Categories[CategoryDefault]
	main.Model = model
	profile.Categories[CategoryDefault] = main
	profile.Categories[CategoryErrand] = CategoryConfig{AccountID: "deleted", Model: "removed"}
	s.cfg.Agents.Profiles[s.cfg.Agents.ActiveProfile] = profile
	r := s.HandleCompact()
	if r.Error != "" || real.Load() != 1 || follow.Load() != 1 {
		t.Fatalf("complete=%+v calls=%d/%d", r, real.Load(), follow.Load())
	}
	records := s.CompactionRecords(s.sessionID)
	if len(records) != 1 || records[0].ModelID != model || records[0].State != sessiondb.CompactionCompleted {
		t.Fatalf("wrong attribution: %+v", records)
	}
	if err := s.db.Close(); err != nil {
		t.Fatal(err)
	}
	db, err := sessiondb.OpenAndMigrate(path)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	state, ok, err := sessiondb.LoadContextState(db, s.sessionID, s.sessionID)
	if err != nil || !ok || len(state.Messages) < 3 || len(state.Messages[0].ProviderItems) != 1 || !strings.Contains(state.Messages[0].ContentString(), "build-evidence-27") || state.Messages[len(state.Messages)-1].Origin != provider.OriginRuntimeContext {
		t.Fatalf("passive resume lost usable checkpoint: %v", err)
	}
	journal := sim.Requests()
	if len(journal) != 3 || journal[0].Path != "/v1/models/"+model {
		t.Fatalf("expected model lookup, native write and export; got %d requests", len(journal))
	}
	var sealed struct{ Signature string }
	if err := json.Unmarshal(state.Messages[0].ProviderItems[0].Data, &sealed); err != nil || sealed.Signature == "" ||
		!bytes.Contains(journal[2].Raw, []byte(sealed.Signature)) {
		t.Fatalf("passive resume did not preserve the exact verified signature: %v", err)
	}
	var source []byte
	if err := db.QueryRow(`SELECT messages_json FROM agent_context_generations WHERE generation=7`).Scan(&source); err != nil || !bytes.Contains(source, []byte("raw tool body")) {
		t.Fatal("source not archived")
	}
}

// The user switched a session to another model on the same Responses account,
// stopped the turn and typed /compact. On 2026-11-25 Crude probed the new
// model, then sent it the whole history for native compaction at full price.
// The user's /compact is Errand's in every state and with every Compaction
// setting: neither a capability check nor the history reaches the serving
// model, whichever model wrote the context.
func TestManualCompactionUsesErrandAfterModelChange(t *testing.T) {
	const errand = "adaptive-errand-model"
	for _, test := range []struct {
		name, writtenBy, method string
	}{
		{name: "after a model change", writtenBy: "previous-model"},
		{name: "same model", writtenBy: "adaptive-main-model"},
		{name: "provider only", writtenBy: "adaptive-main-model", method: provider.CompactionProviderOnly},
	} {
		t.Run(test.name, func(t *testing.T) {
			var writes, native atomic.Int32
			sim := providersim.New(t)
			sim.Respond(func(request providersim.Request) providersim.Turn {
				if strings.HasSuffix(request.Path, "/compact") || bytes.Contains(request.Raw, []byte("compaction_trigger")) {
					native.Add(1)
					return providersim.HTTPError(providersim.InvalidRequest).WithFaultMessage("unsupported compaction trigger")
				}
				if request.Model != errand {
					t.Errorf("/compact sent %s to model %q", request.Path, request.Model)
					return providersim.HTTPError(providersim.InvalidRequest)
				}
				writes.Add(1)
				if !bytes.Contains(request.Raw, []byte("build-evidence-27")) {
					t.Error("Errand did not read the complete source")
				}
				return providersim.Text("<<<intent>>>\nFix the build\n<<<work>>>\nRead the test\n<<<state>>>\nbuild-evidence-27\n<<<continuation>>>\nRun the focused test")
			})
			responses := func(history []provider.ChatMessage) (*Session, *provider.Provider) {
				s, _ := newAdaptiveCompactionSession(t, sim.BaseURL(providersim.Responses), 200_000, history)
				account := s.cfg.Accounts["account"]
				account.Connection.APIStandard = "openai-responses"
				if test.method != "" {
					account.Connection.Protocol = &provider.ProtocolSettings{Compaction: test.method}
				}
				s.cfg.Accounts["account"] = account
				main, err := s.cfg.providerForCategory(s.cfg.ActiveDefaultCategory())
				if err != nil || !main.UsesNativeCompaction() || main.CompactionMethod() != test.method {
					t.Fatalf("fixture is not a native wire with Compaction %q: %v", test.method, err)
				}
				return s, main
			}
			_, main := responses(nil)
			answer := provider.NewTextMessage("assistant", strings.Repeat("raw tool body ", 3000)+"build-evidence-27")
			answer.ReplayScope, answer.ReplaySourceModel = main.ReplayScope(), test.writtenBy
			s, _ := responses([]provider.ChatMessage{provider.NewTextMessage("user", "Fix the build"), answer, provider.NewTextMessage("user", "Continue")})
			prepared, result := s.PrepareCompaction(s.sessionID)
			if prepared.Run == nil || prepared.ModelID != errand {
				t.Fatalf("prepared writer=%q result=%+v; want Errand", prepared.ModelID, result)
			}
			if r := prepared.Run(); r.Error != "" || r.Cancelled || writes.Load() == 0 {
				t.Fatalf("compaction=%+v writes=%d", r, writes.Load())
			}
			if native.Load() != 0 {
				t.Fatalf("/compact sent %d native compaction requests", native.Load())
			}
			records := s.CompactionRecords(s.sessionID)
			if len(records) != 1 || records[0].ModelID != errand || records[0].State != sessiondb.CompactionCompleted {
				t.Fatalf("wrong checkpoint writer: %+v", records)
			}
		})
	}
}
