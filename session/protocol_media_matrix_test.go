package session

import (
	"encoding/base64"
	"encoding/json"
	"strings"
	"testing"

	"crude/provider"
)

func TestE2EProtocolMediaEncodingMatrix(t *testing.T) {
	imageURL := "data:image/png;base64," + base64.StdEncoding.EncodeToString(mediaPNG.content)
	cases := []struct {
		name       string
		standard   string
		part       provider.ContentPart
		wireType   string
		payloadKey string
		wantPrefix string
	}{
		{"openai image", "openai", provider.ContentPart{Type: "image_url", ImageURL: &provider.ImageURL{URL: imageURL}}, "image_url", "url", "data:image/png;base64,"},
		{"openai PDF", "openai", provider.ContentPart{Type: "document", MimeType: "application/pdf", Filename: "report.pdf", Data: mediaPDF.content}, "file", "file_data", "data:application/pdf;base64,"},
		{"openai audio", "openai", provider.ContentPart{Type: "audio", MimeType: "audio/wav", Filename: "voice.wav", Data: mediaWAV.content}, "input_audio", "data", ""},
		{"openai video", "openai", provider.ContentPart{Type: "video", MimeType: "video/mp4", Filename: "clip.mp4", Data: mediaMP4.content}, "video_url", "url", "data:video/mp4;base64,"},
		{"anthropic image", "anthropic", provider.ContentPart{Type: "image_url", ImageURL: &provider.ImageURL{URL: imageURL}}, "image", "data", ""},
		{"anthropic PDF", "anthropic", provider.ContentPart{Type: "document", MimeType: "application/pdf", Filename: "report.pdf", Data: mediaPDF.content}, "document", "data", ""},
		{"responses image", "openai-responses", provider.ContentPart{Type: "image_url", ImageURL: &provider.ImageURL{URL: imageURL}}, "input_image", "image_url", "data:image/png;base64,"},
		{"responses PDF", "openai-responses", provider.ContentPart{Type: "document", MimeType: "application/pdf", Filename: "report.pdf", Data: mediaPDF.content}, "input_file", "file_data", "data:application/pdf;base64,"},
	}

	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			message := provider.NewMultiPartMessage("user", []provider.ContentPart{
				{Type: "text", Text: "inspect"}, testCase.part,
			})
			proto, ok := provider.ProtocolByID(testCase.standard)
			if !ok {
				t.Fatalf("protocol %q not registered", testCase.standard)
			}
			body, err := proto.EncodeRequest(&provider.ChatRequest{
				Model: "matrix-model", Messages: []provider.ChatMessage{message},
			})
			if err != nil {
				t.Fatalf("encode: %v", err)
			}
			var decoded any
			if err := json.Unmarshal(body, &decoded); err != nil {
				t.Fatalf("wire is not JSON: %v", err)
			}
			part := findTypedMap(decoded, testCase.wireType)
			if part == nil {
				t.Fatalf("wire has no %q part: %s", testCase.wireType, body)
			}
			value := nestedWireString(part, testCase.payloadKey)
			if testCase.wantPrefix != "" && !strings.HasPrefix(value, testCase.wantPrefix) {
				t.Fatalf("%s payload = %q", testCase.payloadKey, value)
			}
			if value != "" {
				validateWirePayload(t, value, testCase.wantPrefix)
			}
		})
	}

	t.Run("ollama image", func(t *testing.T) {
		message := provider.NewMultiPartMessage("user", []provider.ContentPart{
			{Type: "text", Text: "inspect"},
			{Type: "image_url", ImageURL: &provider.ImageURL{URL: imageURL}},
		})
		proto, _ := provider.ProtocolByID("ollama")
		body, err := proto.EncodeRequest(&provider.ChatRequest{Model: "matrix-model", Messages: []provider.ChatMessage{message}})
		if err != nil {
			t.Fatal(err)
		}
		var decoded map[string]any
		_ = json.Unmarshal(body, &decoded)
		messages := decoded["messages"].([]any)
		images := messages[0].(map[string]any)["images"].([]any)
		if len(images) != 1 {
			t.Fatalf("ollama images = %#v", images)
		}
		payload, err := base64.StdEncoding.DecodeString(images[0].(string))
		if err != nil || string(payload) != string(mediaPNG.content) {
			t.Fatalf("ollama payload mismatch: err=%v", err)
		}
	})
}

func nestedWireString(part map[string]any, key string) string {
	if value, ok := part[key].(string); ok {
		return value
	}
	for _, childKey := range []string{"image_url", "file", "input_audio", "video_url", "source"} {
		if child, ok := part[childKey].(map[string]any); ok {
			if value, ok := child[key].(string); ok {
				return value
			}
		}
	}
	return ""
}

func validateWirePayload(t *testing.T, value, prefix string) {
	t.Helper()
	if prefix == "" {
		_, err := base64.StdEncoding.DecodeString(value)
		if err != nil {
			t.Fatalf("payload is not base64: %v", err)
		}
		return
	}
	payload := strings.TrimPrefix(value, prefix)
	if _, err := base64.StdEncoding.DecodeString(payload); err != nil {
		t.Fatalf("data URL payload is not base64: %v", err)
	}
}
