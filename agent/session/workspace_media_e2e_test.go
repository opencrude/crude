package session

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"crude/agent/provider"
	"crude/agent/sessiondb"
	"crude/agent/tools"
	"crude/internal/providersim"
)

// ─── media fixture helpers ───────────────────────────────────────────────────

// mediaFixture describes one binary media file the model is asked to read.
type mediaFixture struct {
	name    string
	content []byte
}

var mediaPNG = mediaFixture{name: "fun-screenshot.png", content: []byte{
	0x89, 'P', 'N', 'G', '\r', '\n', 0x1a, '\n', 0x00, 0x00, 0x00, 0x0d,
	'I', 'H', 'D', 'R', 0x00, 0x00, 0x00, 0x01, 0x00, 0x00, 0x00, 0x01, 0x08, 0x06,
}}

var mediaJPEG = mediaFixture{name: "photo.jpg", content: []byte{0xFF, 0xD8, 0xFF, 0xE0, 0x00, 0x10, 'J', 'F', 'I', 'F'}}

var mediaPDF = mediaFixture{name: "report.pdf", content: []byte("%PDF-1.4\n1 0 obj\n<</Type/Catalog>>\nendobj\ntrailer\n<<>>\n%%EOF\n")}

var mediaWAV = mediaFixture{name: "voice.wav", content: []byte{'R', 'I', 'F', 'F', 0x24, 0x00, 0x00, 0x00, 'W', 'A', 'V', 'E', 'f', 'm', 't', ' '}}

var mediaMP4 = mediaFixture{name: "clip.mp4", content: []byte{0x00, 0x00, 0x00, 0x18, 'f', 't', 'y', 'p', 'i', 's', 'o', 'm'}}

var mediaZIP = mediaFixture{name: "archive.zip", content: []byte{'P', 'K', 0x03, 0x04, 0x14, 0x00, 0x00, 0x00, 0x00, 0x00}}

// mediaMatrix covers every (modality, capability) combination the E2E matrix
// needs. Capability values are set per test through config overrides.
func mediaFixtures(t *testing.T, dir string) map[string]string {
	t.Helper()
	paths := make(map[string]string, 6)
	for _, fixture := range []mediaFixture{mediaPNG, mediaJPEG, mediaPDF, mediaWAV, mediaMP4, mediaZIP} {
		path := filepath.Join(dir, fixture.name)
		if err := os.WriteFile(path, fixture.content, 0o600); err != nil {
			t.Fatalf("write %s: %v", fixture.name, err)
		}
		paths[fixture.name] = path
	}
	return paths
}

// ─── request capture harness ─────────────────────────────────────────────────

func newRequestCapture(t *testing.T, reply string) *providersim.Server {
	t.Helper()
	server := providersim.New(t)
	server.Script(providersim.Text(reply))
	return server
}

// flattenParts extracts every content part across messages, so assertions can
// be protocol-shaped without re-implementing each encoder in the test.
func flattenParts(body map[string]any) []map[string]any {
	var parts []map[string]any
	rawMessages, _ := body["messages"].([]any)
	inputItems, _ := body["input"].([]any)
	for _, msg := range append(append([]any{}, rawMessages...), inputItems...) {
		message, ok := msg.(map[string]any)
		if !ok {
			continue
		}
		switch content := message["content"].(type) {
		case string:
			parts = append(parts, map[string]any{"type": "text", "text": content})
		case []any:
			for _, rawPart := range content {
				if part, ok := rawPart.(map[string]any); ok {
					parts = append(parts, part)
				}
			}
		}
	}
	return parts
}

// ─── config helpers ──────────────────────────────────────────────────────────

func mediaAttributionConfig(baseURL, model, standard string, modalities ...string) *Config {
	off := false
	return &Config{

		Accounts: map[string]provider.Account{"account": {ID: "account", ProviderID: "custom", APIKey: "test-key",
			Connection: provider.Template{
				Name: "Custom", ProviderURL: baseURL, APIStandard: standard, AuthType: provider.AuthProtocol,
			},
		}},
		Agents: AgentsConfig{
			Enabled: false, ActiveProfile: "default",
			Profiles: map[string]ProfileConfig{
				"default": {
					MultiAgent: &off,
					Categories: map[string]CategoryConfig{
						CategoryDefault: {
							AccountID: "account", Model: model,
							ModelModalities: modalities,
						},
					},
				},
			},
		},
	}
}

// ─── E2E: capability-aware binary reads (image modality) ─────────────────────

// TestE2EReadImageAttachesWhenCapabilityEnabled drives the real session tool
// pipeline: a model with image capability enabled reads a PNG through
// read_file, and the next provider request must carry the image as a
// multimodal part instead of an error string.
func TestE2EReadImageAttachesWhenCapabilityEnabled(t *testing.T) {
	project := t.TempDir()
	fixtures := mediaFixtures(t, project)
	requests := make(chan map[string]any, 2)
	server := providersim.New(t)
	server.Respond(func(request providersim.Request) providersim.Turn {
		requests <- simRawRequest(request)
		if request.Index == 1 {
			return providersim.CallTools(simToolArgs("call_image", "read_file", map[string]any{"path": fixtures["fun-screenshot.png"]}))
		}
		return providersim.Text("image inspected")
	})
	cfg := mediaAttributionConfig(server.BaseURL(providersim.Chat), "vision-model", "openai", provider.ModalityImage)
	home := t.TempDir()
	sessionDir := filepath.Join(t.TempDir(), "sessions")
	sess, err := NewWithSessionDir("media-e2e", cfg, home, sessionDir)
	if err != nil {
		t.Fatal(err)
	}
	defer sess.Close()

	sender := newMockEventSender()
	orchestrator, err := sess.NewOrchestrator(sender)
	if err != nil {
		t.Fatal(err)
	}
	if !sess.SubmitInput(orchestrator.ID, provider.PlainInput("describe it")) {
		t.Fatal("submit input was rejected")
	}
	first := waitCapturedRequest(t, requests)
	second := waitCapturedRequest(t, requests)
	if hasImagePart(first) {
		t.Fatal("image appeared before read_file executed")
	}
	url := capturedImageURL(second)
	if !strings.HasPrefix(url, "data:image/png;base64,") {
		t.Fatalf("second request image URL = %q", url)
	}
	if _, err := base64.StdEncoding.DecodeString(strings.TrimPrefix(url, "data:image/png;base64,")); err != nil {
		t.Fatalf("image payload is not valid base64: %v", err)
	}
	assertOpenAIToolMediaOrder(t, second)
	waitTurnDone(t, sender, orchestrator.ID)
	waitAssistantCount(t, sess, 1)
	if !toolMessageContainsImage(t, sess.MessagesWithMeta()) {
		t.Fatal("completed turn did not persist its media content")
	}
	dbPath := filepath.Join(sessionDir, sess.SessionID()+".db")
	sess.Close()
	resumed, err := ResumeSessionWithDir(dbPath, cfg, home, sessionDir)
	if err != nil {
		t.Fatalf("resume media session: %v", err)
	}
	defer resumed.Close()
	if !toolMessageContainsImage(t, resumed.MessagesWithMeta()) {
		t.Fatal("resumed turn lost its media content")
	}
}

func TestE2EOpenAIReadFileDeliversEveryEnabledModalitySameTurn(t *testing.T) {
	cases := []struct {
		name       string
		fixture    mediaFixture
		modality   string
		wireType   string
		payloadKey string
		prefix     string
	}{
		{"image", mediaPNG, provider.ModalityImage, "image_url", "url", "data:image/png;base64,"},
		{"PDF", mediaPDF, provider.ModalityPDF, "file", "file_data", "data:application/pdf;base64,"},
		{"audio", mediaWAV, provider.ModalityAudio, "input_audio", "data", ""},
		{"video", mediaMP4, provider.ModalityVideo, "video_url", "url", "data:video/mp4;base64,"},
	}
	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			project := t.TempDir()
			path := filepath.Join(project, testCase.fixture.name)
			if err := os.WriteFile(path, testCase.fixture.content, 0o600); err != nil {
				t.Fatal(err)
			}
			requests := make(chan map[string]any, 2)
			var baseURL string
			if testCase.modality == provider.ModalityVideo {
				baseURL = newChatVideoGateway(t, testCase.fixture.name, requests)
			} else {
				server := providersim.New(t)
				server.Respond(func(request providersim.Request) providersim.Turn {
					requests <- simRawRequest(request)
					if request.Index == 1 {
						return providersim.CallTools(simToolArgs("call_media", "read_file", map[string]any{"path": testCase.fixture.name}))
					}
					return providersim.Text("media inspected")
				})
				baseURL = server.BaseURL(providersim.Chat)
			}

			cfg := mediaAttributionConfig(baseURL, "e2e-"+strings.ToLower(testCase.name), "openai", testCase.modality)
			sess, err := NewWithSessionDir("all-media", cfg, t.TempDir(), filepath.Join(t.TempDir(), "sessions"))
			if err != nil {
				t.Fatal(err)
			}
			defer sess.Close()
			sess.SetWorkDir(project)
			sender := newMockEventSender()
			orchestrator, err := sess.NewOrchestrator(sender)
			if err != nil {
				t.Fatal(err)
			}
			if !sess.SubmitInput(orchestrator.ID, provider.PlainInput("inspect media")) {
				t.Fatal("submit rejected")
			}
			first := waitCapturedRequest(t, requests)
			assertMultimodalReadContractOffered(t, first)
			second := waitCapturedRequest(t, requests)
			part := findTypedMap(second, testCase.wireType)
			if part == nil {
				t.Fatalf("second request has no %s part: %#v", testCase.wireType, second)
			}
			value := nestedWireString(part, testCase.payloadKey)
			if testCase.prefix != "" && !strings.HasPrefix(value, testCase.prefix) {
				t.Fatalf("payload = %q", value)
			}
			validateWirePayload(t, value, testCase.prefix)
			assertOpenAIToolMediaOrder(t, second)
		})
	}
}

func assertOpenAIToolMediaOrder(t *testing.T, request map[string]any) {
	t.Helper()
	messages, _ := request["messages"].([]any)
	toolIndex, mediaIndex := -1, -1
	for index, raw := range messages {
		message, _ := raw.(map[string]any)
		role, _ := message["role"].(string)
		if role == "tool" {
			if _, ok := message["content"].(string); !ok {
				t.Fatalf("OpenAI Chat tool content must be text: %#v", message["content"])
			}
			toolIndex = index
		}
		if role == "user" {
			if content, ok := message["content"].([]any); ok {
				for _, rawPart := range content {
					part, _ := rawPart.(map[string]any)
					if kind, _ := part["type"].(string); kind == "image_url" || kind == "file" || kind == "input_audio" || kind == "video_url" {
						mediaIndex = index
					}
				}
			}
		}
	}
	if toolIndex < 0 || mediaIndex != toolIndex+1 {
		t.Fatalf("tool/media order invalid: tool=%d media=%d messages=%#v", toolIndex, mediaIndex, messages)
	}
}

func assertMultimodalReadContractOffered(t *testing.T, request map[string]any) {
	t.Helper()
	definitions, _ := request["tools"].([]any)
	for _, raw := range definitions {
		definition, _ := raw.(map[string]any)
		function, _ := definition["function"].(map[string]any)
		if function["name"] != "read_file" {
			continue
		}
		description, _ := function["description"].(string)
		for _, capability := range []string{"Images/screenshots", "PDF", "audio", "video", "native attachments", "provider protocol", "explicit refusal"} {
			if !strings.Contains(description, capability) {
				t.Fatalf("actual read_file schema omits %q: %s", capability, description)
			}
		}
		return
	}
	t.Fatal("actual model request did not offer read_file")
}

func toolMessageContainsImage(t *testing.T, messages []sessiondb.MessageWithMeta) bool {
	t.Helper()
	found := false
	for _, message := range messages {
		if message.Msg.Content.HasImage() {
			if message.Msg.Role != "tool" {
				t.Fatalf("canonical media was persisted as fake %s message", message.Msg.Role)
			}
			found = true
		}
	}
	return found
}

func waitCapturedRequest(t *testing.T, requests <-chan map[string]any) map[string]any {
	t.Helper()
	select {
	case request := <-requests:
		return request
	case <-time.After(5 * time.Second):
		t.Fatal("timed out waiting for provider request")
		return nil
	}
}

func hasImagePart(request map[string]any) bool { return capturedImageURL(request) != "" }

func capturedImageURL(request map[string]any) string {
	messages, _ := request["messages"].([]any)
	for _, rawMessage := range messages {
		message, _ := rawMessage.(map[string]any)
		content, _ := message["content"].([]any)
		for _, rawPart := range content {
			part, _ := rawPart.(map[string]any)
			if part["type"] != "image_url" {
				continue
			}
			image, _ := part["image_url"].(map[string]any)
			url, _ := image["url"].(string)
			return url
		}
	}
	return ""
}

// TestE2EReadImageRefusedWithoutCapability verifies the honest-refusal path:
// a text-only model reading an image gets an explicit, actionable error.
func TestE2EReadImageRefusedWithoutCapability(t *testing.T) {
	project := t.TempDir()
	fixtures := mediaFixtures(t, project)

	capture := newRequestCapture(t, "done")
	cfg := mediaAttributionConfig(capture.BaseURL(providersim.Chat), "text-model", "openai") // no modalities
	home := t.TempDir()
	sessionDir := filepath.Join(t.TempDir(), "sessions")
	sess, err := NewWithSessionDir("media-e2e-text", cfg, home, sessionDir)
	if err != nil {
		t.Fatal(err)
	}
	defer sess.Close()

	toolFn := captureToolFn(sess)
	result, _ := toolFn(nil, "read_file", map[string]any{"path": fixtures["fun-screenshot.png"]})
	if !strings.Contains(result.Content, "binary") {
		t.Fatalf("text model should get an explicit binary refusal, got: %s", result.Content)
	}
	if !strings.Contains(result.Content, "image") {
		t.Fatalf("refusal should name the rejected modality, got: %s", result.Content)
	}
}

// TestE2EPDFReadUsesDocumentWhenCapabilityEnabled covers the PDF path through
// the real tool boundary and the next request's document part.
func TestE2EPDFReadUsesDocumentWhenCapabilityEnabled(t *testing.T) {
	project := t.TempDir()
	fixtures := mediaFixtures(t, project)
	requests := make(chan map[string]any, 2)
	server := providersim.New(t)
	server.Respond(func(request providersim.Request) providersim.Turn {
		requests <- simRawRequest(request)
		if request.Index == 1 {
			return providersim.CallTools(simToolArgs("toolu_pdf", "read_file", map[string]any{"path": fixtures["report.pdf"]})).
				WithUsage(providersim.Usage{Input: 10, Output: 7})
		}
		return providersim.Text("pdf inspected").WithUsage(providersim.Usage{Input: 20, Output: 3})
	})
	cfg := mediaAttributionConfig(server.BaseURL(providersim.Messages), "pdf-model", "anthropic", provider.ModalityImage, provider.ModalityPDF)
	home := t.TempDir()
	sessionDir := filepath.Join(t.TempDir(), "sessions")
	sess, err := NewWithSessionDir("media-e2e-pdf", cfg, home, sessionDir)
	if err != nil {
		t.Fatal(err)
	}
	defer sess.Close()

	sender := newMockEventSender()
	orchestrator, err := sess.NewOrchestrator(sender)
	if err != nil {
		t.Fatal(err)
	}
	if !sess.SubmitInput(orchestrator.ID, provider.PlainInput("summarize the PDF")) {
		t.Fatal("submit input was rejected")
	}
	first := waitCapturedRequest(t, requests)
	second := waitCapturedRequest(t, requests)
	if findTypedMap(first, "document") != nil {
		t.Fatal("PDF appeared before read_file executed")
	}
	document := findTypedMap(second, "document")
	if document == nil {
		t.Fatalf("second Anthropic request has no document block: %#v", second)
	}
	source, _ := document["source"].(map[string]any)
	if source["media_type"] != "application/pdf" || source["type"] != "base64" {
		t.Fatalf("document source = %#v", source)
	}
	payload, _ := source["data"].(string)
	decoded, err := base64.StdEncoding.DecodeString(payload)
	if err != nil || string(decoded) != string(mediaPDF.content) {
		t.Fatalf("document payload did not round-trip: err=%v bytes=%d", err, len(decoded))
	}
}

func findTypedMap(value any, want string) map[string]any {
	switch typed := value.(type) {
	case map[string]any:
		if typed["type"] == want {
			return typed
		}
		for _, child := range typed {
			if found := findTypedMap(child, want); found != nil {
				return found
			}
		}
	case []any:
		for _, child := range typed {
			if found := findTypedMap(child, want); found != nil {
				return found
			}
		}
	}
	return nil
}

// TestE2EUnsupportedMediaGetsExplicitRefusal covers audio/video/unknown
// binaries for a model that has none of those capabilities: each read must
// fail loudly and name what happened, never hex-dump into the context.
func TestE2EUnsupportedMediaGetsExplicitRefusal(t *testing.T) {
	project := t.TempDir()
	fixtures := mediaFixtures(t, project)

	capture := newRequestCapture(t, "done")
	cfg := mediaAttributionConfig(capture.BaseURL(providersim.Chat), "image-only-model", "openai", provider.ModalityImage)
	home := t.TempDir()
	sessionDir := filepath.Join(t.TempDir(), "sessions")
	sess, err := NewWithSessionDir("media-e2e-mixed", cfg, home, sessionDir)
	if err != nil {
		t.Fatal(err)
	}
	defer sess.Close()

	toolFn := captureToolFn(sess)
	for _, name := range []string{"voice.wav", "clip.mp4", "archive.zip"} {
		result, _ := toolFn(nil, "read_file", map[string]any{"path": fixtures[name]})
		if !strings.Contains(result.Content, "binary") && !strings.Contains(result.Content, "not supported") &&
			!strings.Contains(result.Content, "cannot") {
			t.Fatalf("%s read should be explicitly refused, got: %s", name, result.Content)
		}
		if len(result.Content) > 2000 {
			t.Fatalf("%s refusal leaked file content (%d bytes)", name, len(result.Content))
		}
	}
}

func TestCapabilityEnabledMediaReturnsStructuredAttachment(t *testing.T) {
	cases := []struct {
		name     string
		fixture  mediaFixture
		modality string
		standard string
		partType string
		wantMIME string
	}{
		{"PNG", mediaPNG, provider.ModalityImage, "openai", "image_url", "image/png"},
		{"JPEG", mediaJPEG, provider.ModalityImage, "anthropic", "image_url", "image/jpeg"},
		{"PDF", mediaPDF, provider.ModalityPDF, "anthropic", "document", "application/pdf"},
		{"WAV", mediaWAV, provider.ModalityAudio, "openai", "audio", "audio/wav"},
		{"MP4", mediaMP4, provider.ModalityVideo, "openai", "video", "video/mp4"},
	}
	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			project := t.TempDir()
			path := filepath.Join(project, testCase.fixture.name)
			if err := os.WriteFile(path, testCase.fixture.content, 0o600); err != nil {
				t.Fatal(err)
			}
			cfg := mediaAttributionConfig("http://127.0.0.1", "structured-"+strings.ToLower(testCase.name), testCase.standard, testCase.modality)
			sess, err := NewWithSessionDir("structured", cfg, t.TempDir(), filepath.Join(t.TempDir(), "sessions"))
			if err != nil {
				t.Fatal(err)
			}
			defer sess.Close()
			sess.SetWorkDir(project)
			result, err := sess.ExecuteToolForTest(context.Background(), "read_file", map[string]any{"path": testCase.fixture.name})
			if err != nil {
				t.Fatalf("read_file: %v", err)
			}
			if len(result.Attachments) != 1 {
				t.Fatalf("attachments = %#v", result.Attachments)
			}
			part := result.Attachments[0]
			if part.Type != testCase.partType || part.MimeType != testCase.wantMIME || part.Filename != testCase.fixture.name {
				t.Fatalf("attachment = %#v", part)
			}
			if part.Type == "image_url" {
				if part.ImageURL == nil || !strings.HasPrefix(part.ImageURL.URL, "data:"+testCase.wantMIME+";base64,") {
					t.Fatalf("image wire source = %#v", part.ImageURL)
				}
			} else if string(part.Data) != string(testCase.fixture.content) {
				t.Fatalf("attachment bytes changed: got=%d want=%d", len(part.Data), len(testCase.fixture.content))
			}
		})
	}
}

func TestEnabledModalityRejectedWhenProtocolCannotCarryIt(t *testing.T) {
	project := t.TempDir()
	path := filepath.Join(project, mediaWAV.name)
	if err := os.WriteFile(path, mediaWAV.content, 0o600); err != nil {
		t.Fatal(err)
	}
	cfg := mediaAttributionConfig("http://127.0.0.1", "anthropic-audio", "anthropic", provider.ModalityAudio)
	sess, err := NewWithSessionDir("protocol-refusal", cfg, t.TempDir(), filepath.Join(t.TempDir(), "sessions"))
	if err != nil {
		t.Fatal(err)
	}
	defer sess.Close()
	result, err := sess.ExecuteToolForTest(context.Background(), "read_file", map[string]any{"path": path})
	if err == nil || !strings.Contains(result.Content, `protocol "anthropic" cannot carry audio attachments`) {
		t.Fatalf("protocol refusal = %q, err=%v", result.Content, err)
	}
	if len(result.Attachments) != 0 {
		t.Fatalf("unsupported protocol leaked attachment: %#v", result.Attachments)
	}
}

func TestBinaryEditSafetyUnaffectedByReadCapability(t *testing.T) {
	project := t.TempDir()
	path := filepath.Join(project, mediaPNG.name)
	if err := os.WriteFile(path, mediaPNG.content, 0o600); err != nil {
		t.Fatal(err)
	}
	cfg := mediaAttributionConfig("http://127.0.0.1", "vision-edit", "openai", provider.ModalityImage)
	sess, err := NewWithSessionDir("binary-edit", cfg, t.TempDir(), filepath.Join(t.TempDir(), "sessions"))
	if err != nil {
		t.Fatal(err)
	}
	defer sess.Close()
	sess.SetWorkDir(project)
	result, err := sess.ExecuteToolForTest(context.Background(), "edit_file", map[string]any{
		"path": mediaPNG.name, "old_string": "PNG", "new_string": "BAD",
	})
	if err == nil || !strings.Contains(result.Content, "binary file") {
		t.Fatalf("binary edit was not rejected: content=%q err=%v", result.Content, err)
	}
	got, readErr := os.ReadFile(path)
	if readErr != nil || string(got) != string(mediaPNG.content) {
		t.Fatalf("binary changed despite refusal: err=%v", readErr)
	}
}

func TestMediaReadPreservesSymlinkAndSizeSafety(t *testing.T) {
	project := t.TempDir()
	target := filepath.Join(project, mediaPNG.name)
	if err := os.WriteFile(target, mediaPNG.content, 0o600); err != nil {
		t.Fatal(err)
	}
	link := filepath.Join(project, "linked.png")
	if err := os.Symlink(target, link); err != nil {
		t.Fatal(err)
	}
	cfg := mediaAttributionConfig("http://127.0.0.1", "vision-safety", "openai", provider.ModalityImage)
	sess, err := NewWithSessionDir("media-safety", cfg, t.TempDir(), filepath.Join(t.TempDir(), "sessions"))
	if err != nil {
		t.Fatal(err)
	}
	defer sess.Close()
	sess.SetWorkDir(project)
	result, err := sess.ExecuteToolForTest(context.Background(), "read_file", map[string]any{"path": link})
	if err == nil || !strings.Contains(result.Content, "refusing to follow symlink") {
		t.Fatalf("media symlink was followed: content=%q err=%v", result.Content, err)
	}

	large := filepath.Join(project, "oversized.png")
	limit := provider.MaxImageFileSizeFor("vision-safety")
	file, err := os.Create(large)
	if err != nil {
		t.Fatal(err)
	}
	if err := file.Truncate(limit + 1); err != nil {
		file.Close()
		t.Fatal(err)
	}
	if err := file.Close(); err != nil {
		t.Fatal(err)
	}
	result, err = sess.ExecuteToolForTest(context.Background(), "read_file", map[string]any{"path": large})
	if err == nil || !strings.Contains(result.Content, "file too large") {
		t.Fatalf("oversized media was accepted: content=%q err=%v", result.Content, err)
	}
}

func TestSubAgentMediaUsesChildModelCapabilities(t *testing.T) {
	project := t.TempDir()
	path := filepath.Join(project, mediaPNG.name)
	if err := os.WriteFile(path, mediaPNG.content, 0o600); err != nil {
		t.Fatal(err)
	}
	provider.SetModelModalities("parent-text-model", nil)
	provider.SetModelModalities("child-vision-model", []string{provider.ModalityImage})
	cfg := mediaAttributionConfig("http://127.0.0.1", "parent-text-model", "openai")
	sess, err := NewWithSessionDir("child-media", cfg, t.TempDir(), filepath.Join(t.TempDir(), "sessions"))
	if err != nil {
		t.Fatal(err)
	}
	defer sess.Close()
	sess.SetWorkDir(project)
	params := map[string]any{
		"path":              mediaPNG.name,
		"_media_model":      "child-vision-model",
		"_media_standard":   "openai",
		"_media_modalities": []string{provider.ModalityImage},
	}
	result, err := sess.buildToolFn(false, sess.provider, nil)(nil, "read_file", params)
	if err != nil || len(result.Attachments) != 1 {
		t.Fatalf("child vision read = %#v, err=%v", result, err)
	}
	for _, internal := range []string{"_media_model", "_media_standard", "_media_modalities"} {
		if _, exists := params[internal]; exists {
			t.Fatalf("internal media override leaked past dispatch: %s", internal)
		}
	}
}

// ─── E2E: workspace-scoped execution root ────────────────────────────────────

// TestE2EWorkspaceSwitchReRootsToolExecution is the real lifecycle: create a
// session in workspace A, run a tool, switch the workspace to B, create a
// session there, run the same tool; the second run must execute inside B.
func TestE2EWorkspaceSwitchReRootsToolExecution(t *testing.T) {
	dirA := t.TempDir()
	dirB := t.TempDir()
	for _, dir := range []string{dirA, dirB} {
		if err := os.WriteFile(filepath.Join(dir, "marker.txt"), []byte(dir), 0o600); err != nil {
			t.Fatal(err)
		}
	}

	capture := newRequestCapture(t, "done")
	cfg := mediaAttributionConfig(capture.BaseURL(providersim.Chat), "cwd-model", "openai")
	if runtime.GOOS == "windows" {
		cfg.Agents.Shell = "cmd" // the test commands are cmd.exe command lines
	}
	home := t.TempDir()

	sessA, err := NewWithSessionDir("cwd-e2e-a", cfg, home, filepath.Join(t.TempDir(), "sessions-a"))
	if err != nil {
		t.Fatal(err)
	}
	defer sessA.Close()
	// Product contract: workspace.CreateSession re-roots the backend session
	// at the project root (see workspace/bootstrap.go SetWorkDir).
	sessA.SetWorkDir(dirA)

	toolFnA := captureToolFn(sessA)
	command := "pwd"
	if runtime.GOOS == "windows" {
		command = "cd"
	}
	assertExecutionRoot := func(content, want string) {
		t.Helper()
		var result struct {
			CWD      string `json:"cwd"`
			Output   string `json:"output"`
			ExitCode int    `json:"exit_code"`
		}
		if err := json.Unmarshal([]byte(content), &result); err != nil {
			t.Fatalf("decode command result: %v: %q", err, content)
		}
		if result.ExitCode != 0 {
			t.Fatalf("command failed: %+v", result)
		}
		wantInfo, err := os.Stat(want)
		if err != nil {
			t.Fatal(err)
		}
		for name, path := range map[string]string{"cwd": result.CWD, "output": strings.TrimSpace(result.Output)} {
			info, err := os.Stat(filepath.Clean(path))
			if err != nil || !os.SameFile(wantInfo, info) {
				t.Fatalf("%s = %q, want directory %q: %v", name, path, want, err)
			}
		}
	}
	result, err := toolFnA(nil, "execute_command", map[string]any{"command": command})
	if err != nil {
		t.Fatalf("execute_command in workspace A: %v", err)
	}
	assertExecutionRoot(result.Content, dirA)

	// Simulate a workspace switch: the product path re-roots all domain
	// managers and creates a new session in the new root. The session's
	// execution context must follow, not stay at the process cwd.
	sessB, err := NewWithSessionDir("cwd-e2e-b", cfg, home, filepath.Join(t.TempDir(), "sessions-b"))
	if err != nil {
		t.Fatal(err)
	}
	defer sessB.Close()
	sessB.SetWorkDir(dirB)

	toolFnB := captureToolFn(sessB)
	resultB, err := toolFnB(nil, "execute_command", map[string]any{"command": command})
	if err != nil {
		t.Fatalf("execute_command in workspace B: %v", err)
	}
	assertExecutionRoot(resultB.Content, dirB)

	// The model-visible prompt must advertise the new working directory.
	prompt := sessB.systemPrompt(false)
	if !strings.Contains(prompt, dirB) {
		t.Fatalf("system prompt after switch advertises stale cwd: want %q in prompt", dirB)
	}
}

// TestE2ERelativePathsResolveAgainstWorkspaceRoot verifies that relative
// tool paths resolve against the session's workspace root, not the process
// cwd, the exact failure mode reported when the agent cwd does not change.
func TestE2ERelativePathsResolveAgainstWorkspaceRoot(t *testing.T) {
	dirA := t.TempDir()
	dirB := t.TempDir()
	markerB := filepath.Join(dirB, "only-in-b.txt")
	if err := os.WriteFile(markerB, []byte("unique marker content"), 0o600); err != nil {
		t.Fatal(err)
	}

	capture := newRequestCapture(t, "ok")
	cfg := mediaAttributionConfig(capture.BaseURL(providersim.Chat), "cwd-model", "openai")
	if runtime.GOOS == "windows" {
		cfg.Agents.Shell = "cmd" // the test commands are cmd.exe command lines
	}
	home := t.TempDir()

	sessA, err := NewWithSessionDir("rel-a", cfg, home, filepath.Join(t.TempDir(), "sessions-a"))
	if err != nil {
		t.Fatal(err)
	}
	defer sessA.Close()
	sessA.SetWorkDir(dirA)

	// chdir away to prove resolution does not depend on the process cwd.
	restore := chdirTemp(t, dirA)
	sessB, err := NewWithSessionDir("rel-b", cfg, home, filepath.Join(t.TempDir(), "sessions-b"))
	if err != nil {
		t.Fatal(err)
	}
	defer sessB.Close()
	sessB.SetWorkDir(dirB)
	defer restore()

	toolFnB := captureToolFn(sessB)
	result, err := toolFnB(nil, "read_file", map[string]any{"path": "only-in-b.txt"})
	if err != nil {
		t.Fatalf("relative read after switch failed: %v", err)
	}
	if strings.Contains(result.Content, "Error") || strings.Contains(result.Content, "no such file") {
		t.Fatalf("relative path resolved against wrong root: %s", result.Content)
	}
	if !strings.Contains(result.Content, "unique marker content") {
		t.Fatalf("read returned unexpected content: %s", result.Content)
	}
	if err := os.WriteFile(filepath.Join(dirB, "only-in-b.go"), []byte("package marker\nconst UniqueB = true\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	globResult, err := toolFnB(nil, "glob_files", map[string]any{"pattern": "*.go"})
	if err != nil || !strings.Contains(globResult.Content, "only-in-b.go") {
		t.Fatalf("default glob root = %q, err=%v", globResult.Content, err)
	}
	grepResult, err := toolFnB(nil, "grep_files", map[string]any{"pattern": "UniqueB"})
	if err != nil || !strings.Contains(grepResult.Content, "only-in-b.go") {
		t.Fatalf("default grep root = %q, err=%v", grepResult.Content, err)
	}
	_ = capture
}

// ─── small shared helpers ────────────────────────────────────────────────────

// captureToolFn returns a callable that routes a tool call through the
// session's real dispatch closure.
func captureToolFn(sess *Session) func(ctx context.Context, tool string, params map[string]any) (tools.ToolResult, error) {
	return func(ctx context.Context, tool string, params map[string]any) (tools.ToolResult, error) {
		return sess.ExecuteToolForTest(ctx, tool, params)
	}
}

// chdirTemp moves the process working directory for the duration of the test
// and restores it afterwards. Used to prove resolution does not depend on the
// process cwd.
func chdirTemp(t *testing.T, dir string) func() {
	t.Helper()
	previous, err := os.Getwd()
	if err != nil {
		t.Fatalf("getwd: %v", err)
	}
	if err := os.Chdir(dir); err != nil {
		t.Fatalf("chdir %s: %v", dir, err)
	}
	return func() {
		if err := os.Chdir(previous); err != nil {
			t.Fatalf("restore cwd: %v", err)
		}
	}
}

// OpenRouter's Chat video_url extension has no counterpart in the pinned OpenAI
// ChatCompletionRequestUserMessageContentPart union. Keep this gateway request
// capture separate from the native image, audio and file journeys above.
func newChatVideoGateway(t *testing.T, name string, requests chan<- map[string]any) string {
	t.Helper()
	sim := providersim.New(t)
	sim.Script(providersim.CallTools(simToolArgs("call_media", "read_file", map[string]any{"path": name})))
	forward := simChatForwarder(t, sim)
	var calls atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, request *http.Request) {
		raw, err := io.ReadAll(request.Body)
		if err != nil {
			t.Errorf("read gateway video request: %v", err)
			return
		}
		var body map[string]any
		if err := json.Unmarshal(raw, &body); err != nil {
			t.Errorf("decode request: %v", err)
			return
		}
		requests <- body
		if calls.Add(1) == 1 {
			forward(w, request, raw)
			return
		}
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = fmt.Fprint(w, "data: {\"choices\":[{\"delta\":{\"content\":\"media inspected\"},\"finish_reason\":\"stop\"}]}\n\ndata: [DONE]\n\n")
	}))
	t.Cleanup(server.Close)
	return server.URL
}
