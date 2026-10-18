package session

import (
	"bytes"
	"encoding/base64"
	"encoding/json"
	"errors"
	"image"
	"image/png"
	"strings"
	"testing"

	"crude/provider"
	"crude/tools"
	captypes "crude/types/capability"
)

type mediaTypedError struct{}

func (*mediaTypedError) Error() string { return "approval required" }

func TestCapabilityTransportFailureKeepsActionableCause(t *testing.T) {
	cause := errors.New("request timed out while waiting for provider")
	result, err := capabilityMediaResult(captypes.Result{IsError: true}, cause, nil)
	if !errors.Is(err, cause) || !strings.Contains(result.Content, cause.Error()) {
		t.Fatalf("transport diagnostic replaced by generic failure: result=%q err=%v", result.Content, err)
	}
	parts := tools.ResultContentParts(result)
	if len(parts) != 1 || !strings.Contains(parts[0].Text, cause.Error()) {
		t.Fatalf("model-facing evidence lost the actual failure: %+v", parts)
	}
}

func TestCapabilityMediaAdmissionEvidenceAndErrors(t *testing.T) {
	var imageBytes bytes.Buffer
	if err := png.Encode(&imageBytes, image.NewRGBA(image.Rect(0, 0, 1, 1))); err != nil {
		t.Fatal(err)
	}
	data := base64.StdEncoding.EncodeToString(imageBytes.Bytes())
	policy := &tools.MediaReadPolicy{Model: "fixture", Standard: "openai", Modalities: []string{"image", "pdf", "audio"}}
	for _, tc := range []struct {
		name, data, mime, typ string
		deny                  bool
	}{
		{"image", data, "image/png", "image", false},
		{"resource", data, "image/png", "resource", false},
		{"base64", "?!", "image/png", "image", true},
		{"MIME", data, "application/pdf", "image", true},
		{"empty", "", "image/png", "image", true},
		{"oversize", strings.Repeat("A", base64.StdEncoding.EncodedLen(1<<20)+4), "image/png", "image", true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			original := &mediaTypedError{}
			result, err := capabilityMediaResult(captypes.Result{Content: []captypes.Content{{Type: tc.typ, Data: tc.data, MIMEType: tc.mime, URI: "file:///never-open", Text: "retained text"}}, Structured: json.RawMessage(`{"external_prompt":true}`), IsError: true, Unknown: true}, original, policy)
			var typed *mediaTypedError
			if !errors.As(err, &typed) {
				t.Fatal("typed original error lost")
			}
			if (len(result.Attachments) > 0) == tc.deny {
				t.Fatalf("attachments=%d denial=%v", len(result.Attachments), tc.deny)
			}
			for _, want := range []string{"retained text", "file:///never-open", "external_prompt", "Result unknown:"} {
				if !strings.Contains(result.Content, want) {
					t.Fatalf("lost %q", want)
				}
			}
		})
	}
	result, err := capabilityMediaResult(captypes.Result{Content: []captypes.Content{{Type: "image"}}}, nil, policy)
	if err == nil || !strings.Contains(result.Content, "empty or malformed") {
		t.Fatal("empty image treated as success")
	}
	result, err = capabilityMediaResult(captypes.Result{Content: []captypes.Content{{Type: "resource_link", URI: "file:///never-open"}}}, nil, policy)
	if err != nil || len(result.Attachments) != 0 || !strings.Contains(result.Content, "file:///never-open") {
		t.Fatal("resource link dereferenced or dropped")
	}
}

func TestCapabilityMediaSupportedRegisteredWireProjection(t *testing.T) {
	var pngBytes bytes.Buffer
	if err := png.Encode(&pngBytes, image.NewRGBA(image.Rect(0, 0, 1, 1))); err != nil {
		t.Fatal(err)
	}
	for _, standard := range []string{"openai", "openai-responses", "codex", "anthropic", "ollama"} {
		t.Run(standard, func(t *testing.T) {
			policy := &tools.MediaReadPolicy{Model: "fixture", Standard: standard, Modalities: []string{"image", "pdf", "audio"}}
			result, err := capabilityMediaResult(captypes.Result{Content: []captypes.Content{{Type: "image", MIMEType: "image/png", Data: base64.StdEncoding.EncodeToString(pngBytes.Bytes())}}}, nil, policy)
			if err != nil || len(result.Attachments) != 1 {
				t.Fatalf("admission: %v", err)
			}
			proto, ok := provider.ProtocolByID(standard)
			if !ok {
				t.Fatal("unregistered protocol")
			}
			body, err := proto.EncodeRequest(&provider.ChatRequest{Model: "fixture", Messages: []provider.ChatMessage{provider.NewMultiPartMessage("user", result.Attachments)}})
			if err != nil || !bytes.Contains(body, []byte(base64.StdEncoding.EncodeToString(pngBytes.Bytes()))) {
				t.Fatalf("wire lost payload: %s %v", body, err)
			}
		})
	}
}

func TestCapabilityMediaInterleavingAndDetachedComposition(t *testing.T) {
	var pngBytes bytes.Buffer
	if err := png.Encode(&pngBytes, image.NewRGBA(image.Rect(0, 0, 1, 1))); err != nil {
		t.Fatal(err)
	}
	// WAV header sufficient for MIME sniffing; no transformation of evidence.
	wav := append([]byte("RIFF\x24\x00\x00\x00WAVEfmt "), make([]byte, 40)...)
	raw := captypes.Result{Content: []captypes.Content{
		{Type: "text", Text: "A"},
		{Type: "image", MIMEType: "image/png", Data: base64.StdEncoding.EncodeToString(pngBytes.Bytes())},
		{Type: "text", Text: "B"},
		{Type: "audio", MIMEType: "audio/wav", Data: base64.StdEncoding.EncodeToString(wav)},
		{Type: "text", Text: "C"},
	}, Structured: json.RawMessage(`{"tail":true}`)}
	result, err := capabilityMediaResult(raw, errors.New("LOCAL schema failure"), &tools.MediaReadPolicy{Model: "fixture", Standard: "openai", Modalities: []string{"image", "audio"}})
	if err == nil {
		t.Fatal("original error lost")
	}
	parts := tools.ResultContentParts(result)
	var sequence []string
	for _, part := range parts {
		if part.Type == "text" {
			if part.Text == "A" || part.Text == "B" || part.Text == "C" {
				sequence = append(sequence, part.Text)
			}
		} else {
			sequence = append(sequence, part.Type)
		}
	}
	if strings.Join(sequence, "/") != "A/image_url/B/audio/C" {
		t.Fatalf("order lost: %v %#v", sequence, parts)
	}
	if !strings.Contains(parts[len(parts)-1].Text, `"tail":true`) {
		t.Fatal("structured tail lost")
	}
	for _, part := range parts {
		if part.ImageURL != nil {
			part.ImageURL.URL = "mutated"
		}
		if len(part.Data) > 0 {
			part.Data[0] = 0
		}
	}
	if result.Attachments[0].ImageURL.URL == "mutated" || result.Attachments[1].Data[0] == 0 {
		t.Fatal("composition aliases original media")
	}
	if got := tools.ResultContentParts(tools.ToolResult{Content: "excluded", ContentParts: []provider.ContentPart{}}); len(got) != 0 {
		t.Fatal("explicit empty parts fell back")
	}
}
