package session

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"image"
	"image/png"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"crude/agent"
	"crude/provider"
	"crude/safety"
	"crude/tools"
)

func TestAgentMediaPolicyRetainsExactRootSnapshot(t *testing.T) {
	home := t.TempDir()
	s, err := NewWithSessionDir("", modelAttributionConfig("http://127.0.0.1:9", "root-snapshot"), home, home)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	p := provider.FromPreset("openai-chat", "fixture", "http://127.0.0.1:9")
	p.Model = "root-snapshot"
	modalities := []string{provider.ModalityImage}
	live := s.newAgent(p, false, modalities)
	modalities[0] = provider.ModalityAudio
	if live.MediaReadPolicy == nil || live.MediaReadPolicy.Model != "root-snapshot" || live.MediaReadPolicy.Standard != "openai" || !reflect.DeepEqual(live.MediaReadPolicy.Modalities, []string{"image"}) {
		t.Fatalf("root media policy did not retain detached role snapshot: %#v", live.MediaReadPolicy)
	}
	for _, modalities := range [][]string{nil, {}} {
		live := s.newAgent(p, false, modalities)
		if live.MediaReadPolicy == nil || !reflect.DeepEqual(live.MediaReadPolicy.Modalities, modalities) {
			t.Fatalf("root inheritance/explicit denial changed: input=%#v policy=%#v", modalities, live.MediaReadPolicy)
		}
	}
}

func TestChildNativeMediaDispatchPreservesExplicitDisabledSnapshot(t *testing.T) {
	home := t.TempDir()
	s, err := NewWithSessionDir("", modelAttributionConfig("http://127.0.0.1:9", "parent-proxy"), home, home)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	root := t.TempDir()
	s.SetWorkDir(root)
	var imageBytes bytes.Buffer
	if err := png.Encode(&imageBytes, image.NewRGBA(image.Rect(0, 0, 1, 1))); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "child.png"), imageBytes.Bytes(), 0600); err != nil {
		t.Fatal(err)
	}
	runner := NewSubAgentRunner(s, 1)
	defer runner.SessionCancel()
	parentCalls := 0
	runner.SetParentToolFn(func(_ context.Context, _ string, _ map[string]any) (tools.ToolResult, error) {
		parentCalls++
		return tools.ToolResult{Content: "foreign parent result"}, nil
	})
	p := provider.FromPreset("openai-chat", "fixture", "http://127.0.0.1:9")
	p.Model = "child-proxy"
	handle := &agent.SubAgentHandle{Agent: agent.NewAgent(p, p.Model, s.logger)}
	// The parent's image-capable result proves the fixture is real and admissible;
	// the child must still use its own immutable, explicit-empty role snapshot.
	parent, err := s.buildToolFnAtRoot(false, p, []string{provider.ModalityImage}, root)(t.Context(), "read_file", map[string]any{"path": "child.png"})
	if err != nil || len(parent.Attachments) != 1 {
		t.Fatalf("image-capable native parent read=%#v err=%v", parent, err)
	}
	for _, modalities := range [][]string{{provider.ModalityImage}, {}} {
		spec := agent.SubAgentSpec{ID: "child", Policy: safety.ReadOnlyPolicy(), ModelModalities: modalities}
		result, err := runner.buildProxiedToolFn(spec, nil, handle)(t.Context(), "read_file", map[string]any{"path": "child.png"})
		if len(modalities) == 0 {
			if err == nil || len(result.Attachments) != 0 || !strings.Contains(result.Content, `model "child-proxy" does not advertise image input`) {
				t.Fatalf("explicit-empty child media selection inherited parent: result=%#v err=%v", result, err)
			}
		} else if err != nil || len(result.Attachments) != 1 || result.Attachments[0].ImageURL == nil || result.Attachments[0].ImageURL.URL != "data:image/png;base64,"+base64.StdEncoding.EncodeToString(imageBytes.Bytes()) {
			t.Fatalf("enabled child lost exact native image bytes: result=%#v err=%v", result, err)
		}
	}
	if parentCalls != 0 {
		t.Fatalf("native child dispatch used parent policy/tools %d times", parentCalls)
	}
}

func TestDelegatedAgentMediaPolicyUsesChildSnapshot(t *testing.T) {
	for _, modalities := range [][]string{{provider.ModalityImage}, {}} {
		name := "enabled"
		if len(modalities) == 0 {
			name = "explicitly-disabled"
		}
		t.Run(name, func(t *testing.T) {
			entered, release := make(chan struct{}), make(chan struct{})
			var releaseOnce sync.Once
			releaseProvider := func() { releaseOnce.Do(func() { close(release) }) }
			defer releaseProvider()
			var requests atomic.Int32
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, request *http.Request) {
				var body struct {
					Model string `json:"model"`
				}
				if err := json.NewDecoder(request.Body).Decode(&body); err != nil {
					t.Error(err)
					return
				}
				if body.Model != "delegated-media-model" {
					t.Errorf("child used foreign model %q", body.Model)
				}
				if requests.Add(1) == 1 {
					close(entered)
				}
				select {
				case <-release:
				case <-request.Context().Done():
					return
				}
				w.Header().Set("Content-Type", "text/event-stream")
				_, _ = fmt.Fprint(w, "data: {\"choices\":[{\"delta\":{\"content\":\"observed\"},\"finish_reason\":\"stop\"}]}\n\ndata: [DONE]\n\n")
			}))
			t.Cleanup(server.Close)
			home := t.TempDir()
			cfg := modelAttributionConfig(server.URL, "parent-media-model")
			profile := cfg.Agents.Profiles[cfg.Agents.ActiveProfile]
			main := profile.Categories[CategoryDefault]
			main.ModelModalities = []string{provider.ModalityAudio}
			profile.Categories[CategoryDefault] = main
			profile.Categories[CategoryResearcher] = CategoryConfig{AccountID: "account", Model: "delegated-media-model", ModelModalities: modalities}
			cfg.Agents.Profiles[cfg.Agents.ActiveProfile] = profile
			s, err := NewWithSessionDir("", cfg, home, filepath.Join(home, "sessions"))
			if err != nil {
				t.Fatal(err)
			}
			t.Cleanup(s.Close)
			s.SetWorkDir(t.TempDir())
			attachDelegationTestCollaboration(t, s)
			runner := NewSubAgentRunner(s, 1)
			s.subAgentSup = runner
			t.Cleanup(func() {
				runner.SessionCancel()
				releaseProvider()
				runner.Wait()
			})
			result, err := runner.StartAgent(t.Context(), map[string]any{"category": CategoryResearcher, "description": "retain exact role media capabilities", "isolation": "shared"})
			if err != nil {
				t.Fatal(err)
			}
			var identity struct {
				AgentID string `json:"agent_id"`
			}
			if err := json.Unmarshal([]byte(result.Content), &identity); err != nil || identity.AgentID == "" {
				t.Fatalf("child identity=%s err=%v", result.Content, err)
			}
			select {
			case <-entered:
			case <-time.After(5 * time.Second):
				t.Fatal("real child request did not start")
			}
			handle, exists := runner.Manager().GetActive(agent.AgentID(identity.AgentID))
			if !exists || handle.Agent == nil || handle.Agent.MediaReadPolicy == nil {
				t.Fatal("actual launched child has no media snapshot")
			}
			policy := handle.Agent.MediaReadPolicy
			if policy.Model != "delegated-media-model" || policy.Standard != "openai" || !reflect.DeepEqual(policy.Modalities, modalities) {
				t.Fatalf("child inherited parent/catalog capabilities: %#v want=%#v", policy, modalities)
			}
			// Stop before allowing a natural finish that requires another report
			// request; this test observes construction through the actual launch.
			handle.Cancel()
			releaseProvider()
			runner.Wait()
		})
	}
}
