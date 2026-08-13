package provider

import (
	"encoding/json"
	"testing"
)

// --- TextContent round-trip tests ---

func TestTextContent_IsText(t *testing.T) {
	c := TextContent("hello world")
	if !c.IsText() {
		t.Error("TextContent should be text")
	}
}

func TestTextContent_String(t *testing.T) {
	c := TextContent("hello world")
	if got := c.String(); got != "hello world" {
		t.Errorf("String(): got %q, want %q", got, "hello world")
	}
}

func TestTextContent_Parts(t *testing.T) {
	c := TextContent("hello")
	parts := c.Parts()
	if len(parts) != 1 {
		t.Fatalf("Parts(): expected 1 part, got %d", len(parts))
	}
	if parts[0].Type != "text" || parts[0].Text != "hello" {
		t.Errorf("Parts()[0]: expected {text hello}, got %+v", parts[0])
	}
}

func TestTextContent_HasImage(t *testing.T) {
	c := TextContent("just text")
	if c.HasImage() {
		t.Error("text content should not have images")
	}
}

func TestTextContent_MarshalJSON(t *testing.T) {
	c := TextContent("hello")
	b, err := json.Marshal(c)
	if err != nil {
		t.Fatalf("MarshalJSON: %v", err)
	}
	if string(b) != `"hello"` {
		t.Errorf("MarshalJSON: got %s, want %q", b, `"hello"`)
	}
}

func TestTextContent_UnmarshalJSON(t *testing.T) {
	var c MessageContent
	if err := json.Unmarshal([]byte(`"hello"`), &c); err != nil {
		t.Fatalf("UnmarshalJSON: %v", err)
	}
	if !c.IsText() {
		t.Error("unmarshaled string should be text")
	}
	if c.String() != "hello" {
		t.Errorf("String(): got %q, want %q", c.String(), "hello")
	}
}

func TestTextContent_UnmarshalJSONNull(t *testing.T) {
	var content MessageContent
	if err := json.Unmarshal([]byte(`null`), &content); err != nil {
		t.Fatalf("UnmarshalJSON null: %v", err)
	}
	if !content.IsText() || content.String() != "" {
		t.Fatalf("null content = %q, IsText=%v", content.String(), content.IsText())
	}
}

func TestEstimateMessageTokensIncludesProtocolMetadata(t *testing.T) {
	base := EstimateMessageTokens(NewTextMessage("assistant", "answer"), "test")
	enriched := EstimateMessageTokens(ChatMessage{
		Role:             "assistant",
		Content:          PartsContent([]ContentPart{{Type: "image_url", ImageURL: &ImageURL{URL: "data:image/png;base64,abc"}}}),
		ReasoningContent: "private reasoning",
		ProviderItems: []ProviderItem{{
			Standard: "anthropic", Type: "thinking",
			Data: json.RawMessage(`{"type":"thinking","thinking":"private reasoning","signature":"sig"}`),
		}},
	}, "test")
	if enriched <= base+1024 {
		t.Fatalf("metadata estimate = %d, base = %d", enriched, base)
	}
}

// --- PartsContent round-trip tests ---

func TestPartsContent_IsText(t *testing.T) {
	parts := []ContentPart{
		{Type: "text", Text: "hello"},
		{Type: "image_url", ImageURL: &ImageURL{URL: "data:image/png;base64,abc"}},
	}
	c := PartsContent(parts)
	if c.IsText() {
		t.Error("PartsContent should not be text")
	}
}

func TestPartsContent_String(t *testing.T) {
	parts := []ContentPart{
		{Type: "text", Text: "hello"},
		{Type: "image_url", ImageURL: &ImageURL{URL: "data:image/png;base64,abc"}},
	}
	c := PartsContent(parts)
	// String() extracts text parts only, skipping images
	if got := c.String(); got != "hello" {
		t.Errorf("String(): got %q, want %q", got, "hello")
	}
}

func TestPartsContent_Parts(t *testing.T) {
	origParts := []ContentPart{
		{Type: "text", Text: "hello"},
		{Type: "image_url", ImageURL: &ImageURL{URL: "data:image/png;base64,abc"}},
	}
	c := PartsContent(origParts)
	parts := c.Parts()
	if len(parts) != 2 {
		t.Fatalf("Parts(): expected 2 parts, got %d", len(parts))
	}
	if parts[0].Type != "text" || parts[0].Text != "hello" {
		t.Errorf("Parts()[0]: got %+v", parts[0])
	}
	if parts[1].Type != "image_url" || parts[1].ImageURL == nil {
		t.Errorf("Parts()[1]: got %+v", parts[1])
	}
}

func TestPartsContent_HasImage(t *testing.T) {
	parts := []ContentPart{
		{Type: "text", Text: "desc"},
		{Type: "image_url", ImageURL: &ImageURL{URL: "data:image/png;base64,abc"}},
	}
	c := PartsContent(parts)
	if !c.HasImage() {
		t.Error("multipart with image_url should HasImage=true")
	}
}

func TestPartsContent_HasImage_False(t *testing.T) {
	parts := []ContentPart{
		{Type: "text", Text: "desc"},
		{Type: "text", Text: "more text"},
	}
	c := PartsContent(parts)
	if c.HasImage() {
		t.Error("multipart without image_url should HasImage=false")
	}
}

// --- MarshalJSON multipart ---

func TestPartsContent_MarshalJSON(t *testing.T) {
	parts := []ContentPart{
		{Type: "text", Text: "hello"},
		{Type: "image_url", ImageURL: &ImageURL{URL: "data:image/png;base64,abc"}},
	}
	c := PartsContent(parts)
	b, err := json.Marshal(c)
	if err != nil {
		t.Fatalf("MarshalJSON multipart: %v", err)
	}
	// Should be a JSON array, not a string
	if len(b) == 0 || b[0] != '[' {
		t.Errorf("MarshalJSON multipart: expected JSON array, got %s", b)
	}
	// Should not contain the sentinel prefix
	if string(b) == "" || string(b)[0] == '\x00' {
		t.Error("MarshalJSON should strip sentinel prefix")
	}
}

// --- UnmarshalJSON array → multipart with sentinel ---

func TestUnmarshalJSON_Array(t *testing.T) {
	input := `[{"type":"text","text":"hello"},{"type":"image_url","image_url":{"url":"data:image/png;base64,abc"}}]`
	var c MessageContent
	if err := json.Unmarshal([]byte(input), &c); err != nil {
		t.Fatalf("UnmarshalJSON array: %v", err)
	}
	if c.IsText() {
		t.Error("unmarshaled array should be multipart, not text")
	}
	parts := c.Parts()
	if len(parts) != 2 {
		t.Fatalf("Parts(): expected 2, got %d", len(parts))
	}
	if parts[0].Type != "text" || parts[0].Text != "hello" {
		t.Errorf("Parts()[0]: got %+v", parts[0])
	}
}

// --- MultipartJSON strips sentinel ---

func TestMultipartJSON_StripsSentinel(t *testing.T) {
	parts := []ContentPart{
		{Type: "text", Text: "hi"},
	}
	c := PartsContent(parts)
	json := c.MultipartJSON()
	if len(json) == 0 {
		t.Fatal("MultipartJSON should return non-empty for multipart content")
	}
	// Should not contain the sentinel
	if json[0] == '\x00' {
		t.Error("MultipartJSON should strip sentinel prefix")
	}
	if json[0] != '[' {
		t.Errorf("MultipartJSON should start with '[', got %c", json[0])
	}
}

func TestMultipartJSON_TextContent(t *testing.T) {
	c := TextContent("hello")
	if got := c.MultipartJSON(); got != "" {
		t.Errorf("MultipartJSON on text should return empty, got %q", got)
	}
}

// --- ContentDescription tests ---

func TestContentDescription_Text(t *testing.T) {
	c := TextContent("hello world")
	if got := c.ContentDescription(); got != "hello world" {
		t.Errorf("ContentDescription text: got %q, want %q", got, "hello world")
	}
}

func TestContentDescription_MultipartWithImage(t *testing.T) {
	parts := []ContentPart{
		{Type: "text", Text: "look at this"},
		{Type: "image_url", ImageURL: &ImageURL{URL: "data:image/jpeg;base64,abc"}},
	}
	c := PartsContent(parts)
	got := c.ContentDescription()
	if got == "" {
		t.Fatal("ContentDescription should not be empty")
	}
	// Should contain the text
	if !contains(got, "look at this") {
		t.Errorf("ContentDescription should contain text, got %q", got)
	}
	// Should contain image marker with MIME type
	if !contains(got, "[Image: image/jpeg]") {
		t.Errorf("ContentDescription should contain [Image: image/jpeg], got %q", got)
	}
}

func TestContentDescription_MultipartTextOnly(t *testing.T) {
	parts := []ContentPart{
		{Type: "text", Text: "line1"},
		{Type: "text", Text: "line2"},
	}
	c := PartsContent(parts)
	got := c.ContentDescription()
	if got != "line1\nline2" {
		t.Errorf("ContentDescription text-only multipart: got %q, want %q", got, "line1\nline2")
	}
}

// --- Edge cases ---

func TestEmptyContent(t *testing.T) {
	c := MessageContent("")
	if !c.IsText() {
		t.Error("empty content should be text")
	}
	if c.String() != "" {
		t.Errorf("empty String(): got %q, want empty", c.String())
	}
	if c.Parts() != nil {
		t.Errorf("empty Parts(): expected nil, got %v", c.Parts())
	}
	if c.HasImage() {
		t.Error("empty content should not have images")
	}
	if got := c.ContentDescription(); got != "" {
		t.Errorf("empty ContentDescription: got %q", got)
	}
	if got := c.MultipartJSON(); got != "" {
		t.Errorf("empty MultipartJSON: got %q, want empty", got)
	}
}

func TestPlainTextStartingWithBracket(t *testing.T) {
	// Plain text starting with "[{" should be treated as text, not multipart
	c := TextContent("[{\"type\":\"text\",\"text\":\"hello\"}]")
	if !c.IsText() {
		t.Error("plain text starting with '[' should still be text (no sentinel)")
	}
}

func TestMultipleImagesInParts(t *testing.T) {
	parts := []ContentPart{
		{Type: "image_url", ImageURL: &ImageURL{URL: "data:image/png;base64,img1"}},
		{Type: "image_url", ImageURL: &ImageURL{URL: "data:image/gif;base64,img2"}},
		{Type: "text", Text: "between"},
		{Type: "image_url", ImageURL: &ImageURL{URL: "data:image/webp;base64,img3"}},
	}
	c := PartsContent(parts)
	if !c.HasImage() {
		t.Error("multipart with 3 images should HasImage=true")
	}
	desc := c.ContentDescription()
	if !contains(desc, "[Image: image/png]") {
		t.Errorf("ContentDescription missing png marker: %q", desc)
	}
	if !contains(desc, "[Image: image/gif]") {
		t.Errorf("ContentDescription missing gif marker: %q", desc)
	}
	if !contains(desc, "[Image: image/webp]") {
		t.Errorf("ContentDescription missing webp marker: %q", desc)
	}
	if !contains(desc, "between") {
		t.Errorf("ContentDescription missing text: %q", desc)
	}
}

// --- JSON round-trip tests ---

func TestJSONRoundTrip_Text(t *testing.T) {
	orig := TextContent("hello world")
	b, err := json.Marshal(orig)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	var loaded MessageContent
	if err := json.Unmarshal(b, &loaded); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if !loaded.IsText() {
		t.Error("round-trip text should still be text")
	}
	if loaded.String() != orig.String() {
		t.Errorf("round-trip: got %q, want %q", loaded.String(), orig.String())
	}
}

func TestJSONRoundTrip_Multipart(t *testing.T) {
	origParts := []ContentPart{
		{Type: "text", Text: "hello"},
		{Type: "image_url", ImageURL: &ImageURL{URL: "data:image/png;base64,abc"}},
	}
	orig := PartsContent(origParts)
	b, err := json.Marshal(orig)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	var loaded MessageContent
	if err := json.Unmarshal(b, &loaded); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if loaded.IsText() {
		t.Error("round-trip multipart should still be multipart")
	}
	if !loaded.HasImage() {
		t.Error("round-trip multipart should still have image")
	}
}

func TestJSONRoundTrip_ChatMessage(t *testing.T) {
	msg := ChatMessage{
		Role: "user",
		Content: PartsContent([]ContentPart{
			{Type: "text", Text: "describe this"},
			{Type: "image_url", ImageURL: &ImageURL{URL: "data:image/png;base64,abc"}},
		}),
	}
	b, err := json.Marshal(msg)
	if err != nil {
		t.Fatalf("marshal ChatMessage: %v", err)
	}
	var loaded ChatMessage
	if err := json.Unmarshal(b, &loaded); err != nil {
		t.Fatalf("unmarshal ChatMessage: %v", err)
	}
	if loaded.Content.IsText() {
		t.Error("round-trip ChatMessage multipart should be multipart")
	}
	if !loaded.Content.HasImage() {
		t.Error("round-trip ChatMessage should have image")
	}
}

// --- UserInput tests ---

func TestPlainInput(t *testing.T) {
	ui := PlainInput("hello")
	if ui.Text != "hello" {
		t.Errorf("Text: got %q, want %q", ui.Text, "hello")
	}
	if len(ui.ReferenceIDs) != 0 {
		t.Errorf("ReferenceIDs: got %v, want empty", ui.ReferenceIDs)
	}
}

func TestInputWithRefs(t *testing.T) {
	ui := InputWithRefs("hello", []string{"ref1", "ref2"})
	if ui.Text != "hello" {
		t.Errorf("Text: got %q, want %q", ui.Text, "hello")
	}
	if len(ui.ReferenceIDs) != 2 {
		t.Fatalf("ReferenceIDs: got %d, want 2", len(ui.ReferenceIDs))
	}
	if ui.ReferenceIDs[0] != "ref1" || ui.ReferenceIDs[1] != "ref2" {
		t.Errorf("ReferenceIDs: got %v", ui.ReferenceIDs)
	}
}

// --- extractMimeFromDataURL tests ---

func TestExtractMimeFromDataURL(t *testing.T) {
	tests := []struct {
		input string
		want  string
	}{
		{"data:image/png;base64,abc123", "image/png"},
		{"data:image/jpeg;base64,xyz", "image/jpeg"},
		{"data:image/gif,raw", "image/gif"},
		{"data:application/pdf;base64,abc", "application/pdf"},
		{"notaurl", "image"},
	}
	for _, tt := range tests {
		got := extractMimeFromDataURL(tt.input)
		if got != tt.want {
			t.Errorf("extractMimeFromDataURL(%q): got %q, want %q", tt.input, got, tt.want)
		}
	}
}

// --- helper ---

func contains(s, substr string) bool {
	return len(s) >= len(substr) && (s == substr || len(s) > 0 && containsSubstr(s, substr))
}

func containsSubstr(s, substr string) bool {
	for i := 0; i <= len(s)-len(substr); i++ {
		if s[i:i+len(substr)] == substr {
			return true
		}
	}
	return false
}
