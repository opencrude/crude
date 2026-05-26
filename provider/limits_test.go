package provider

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func resetDynamicLimits() {
	dynamicLimitsMu.Lock()
	dynamicLimits = make(map[string]modelLimits)
	dynamicLimitsMu.Unlock()
}

func TestProviderFamilyLimits(t *testing.T) {
	resetDynamicLimits()

	tests := []struct {
		provider   string
		wantImage  int64
		wantText   int64
		wantInline int64
	}{
		{"openai/gpt-4o", 20 * 1024 * 1024, 2 * 1024 * 1024, 20 * 1024 * 1024},
		{"anthropic/claude-3-5-sonnet", 32 * 1024 * 1024, 2 * 1024 * 1024, 32 * 1024 * 1024},
		{"google/gemini-2.0-flash", 20 * 1024 * 1024, 2 * 1024 * 1024, 20 * 1024 * 1024},
		{"groq/llama-3.2-90b-vision", 20 * 1024 * 1024, 2 * 1024 * 1024, 20 * 1024 * 1024},
		{"deepseek/deepseek-chat", 20 * 1024 * 1024, 2 * 1024 * 1024, 20 * 1024 * 1024},
		{"unknown/some-model", 20 * 1024 * 1024, 2 * 1024 * 1024, 20 * 1024 * 1024},
	}

	for _, tt := range tests {
		got := MaxImageFileSizeFor(tt.provider)
		if got != tt.wantImage {
			t.Errorf("MaxImageFileSizeFor(%q): got %d, want %d", tt.provider, got, tt.wantImage)
		}
		got = MaxTextFileSizeFor(tt.provider)
		if got != tt.wantText {
			t.Errorf("MaxTextFileSizeFor(%q): got %d, want %d", tt.provider, got, tt.wantText)
		}
		got = MaxInlineContentSizeFor(tt.provider)
		if got != tt.wantInline {
			t.Errorf("MaxInlineContentSizeFor(%q): got %d, want %d", tt.provider, got, tt.wantInline)
		}
	}
}

func TestProviderFamilyLimits_CaseInsensitive(t *testing.T) {
	resetDynamicLimits()

	// Provider prefix should be case-insensitive
	if MaxImageFileSizeFor("Anthropic/Claude-3") != 32*1024*1024 {
		t.Error("provider family limits should be case-insensitive")
	}
	if MaxImageFileSizeFor("OpenAI/GPT-4o") != 20*1024*1024 {
		t.Error("provider family limits should be case-insensitive")
	}
}

func TestPerModelLimits_DynamicOverride(t *testing.T) {
	resetDynamicLimits()

	// Before setting dynamic limits, should use provider-family defaults
	if MaxImageFileSizeFor("openai/gpt-4o") != 20*1024*1024 {
		t.Error("should use provider default before dynamic override")
	}

	// Set custom limits
	SetModelLimits("gpt-4o", modelLimits{
		MaxImageFileSize:     10 * 1024 * 1024,
		MaxTextFileSize:      1 * 1024 * 1024,
		MaxInlineContentSize: 10 * 1024 * 1024,
	})

	// Should find the stripped prefix match
	if MaxImageFileSizeFor("openai/gpt-4o") != 10*1024*1024 {
		t.Errorf("MaxImageFileSizeFor with dynamic override: got %d, want %d",
			MaxImageFileSizeFor("openai/gpt-4o"), 10*1024*1024)
	}
	if MaxTextFileSizeFor("openai/gpt-4o") != 1*1024*1024 {
		t.Errorf("MaxTextFileSizeFor with dynamic override: got %d, want %d",
			MaxTextFileSizeFor("openai/gpt-4o"), 1*1024*1024)
	}
}

func TestPerModelLimits_NoProviderPrefix(t *testing.T) {
	resetDynamicLimits()

	// Model ID without provider prefix should still work with dynamicLimits
	SetModelLimits("custom-model", modelLimits{
		MaxImageFileSize:     5 * 1024 * 1024,
		MaxTextFileSize:      500 * 1024,
		MaxInlineContentSize: 5 * 1024 * 1024,
	})

	if MaxImageFileSizeFor("custom-model") != 5*1024*1024 {
		t.Errorf("MaxImageFileSizeFor custom model: got %d, want %d",
			MaxImageFileSizeFor("custom-model"), 5*1024*1024)
	}
}

func TestFileSizeExceeded_UnderLimit(t *testing.T) {
	dir := t.TempDir()
	f := filepath.Join(dir, "small.txt")
	if err := os.WriteFile(f, []byte("hello"), 0644); err != nil {
		t.Fatal(err)
	}
	got := FileSizeExceeded(f, 1024)
	if got != "" {
		t.Errorf("FileSizeExceeded(small file, 1KB limit): got %q, want empty", got)
	}
}

func TestFileSizeExceeded_OverLimit(t *testing.T) {
	dir := t.TempDir()
	f := filepath.Join(dir, "big.txt")
	data := make([]byte, 200)
	if err := os.WriteFile(f, data, 0644); err != nil {
		t.Fatal(err)
	}
	got := FileSizeExceeded(f, 100)
	if got == "" {
		t.Error("FileSizeExceeded(oversized file): expected non-empty warning, got empty")
	}
	if !strings.Contains(got, "big.txt") {
		t.Errorf("warning should mention path, got %q", got)
	}
}

func TestFileSizeExceeded_NonExistentFile(t *testing.T) {
	got := FileSizeExceeded("/nonexistent/path/file.txt", 1024)
	if got != "" {
		t.Errorf("FileSizeExceeded(nonexistent): got %q, want empty (let caller handle)", got)
	}
}

func TestFileSizeExceeded_ExactLimit(t *testing.T) {
	dir := t.TempDir()
	f := filepath.Join(dir, "exact.txt")
	data := make([]byte, 100)
	if err := os.WriteFile(f, data, 0644); err != nil {
		t.Fatal(err)
	}
	got := FileSizeExceeded(f, 100)
	if got != "" {
		t.Errorf("FileSizeExceeded(exactly at limit): got %q, want empty", got)
	}
	data = make([]byte, 101)
	if err := os.WriteFile(f, data, 0644); err != nil {
		t.Fatal(err)
	}
	got = FileSizeExceeded(f, 100)
	if got == "" {
		t.Error("FileSizeExceeded(1 byte over): expected non-empty warning")
	}
}
