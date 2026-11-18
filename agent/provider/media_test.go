package provider

import (
	"os"
	"path/filepath"
	"testing"
)

func TestMediaModalityExtensionMatrix(t *testing.T) {
	cases := map[string]string{
		"png": ModalityImage, "jpg": ModalityImage, "jpeg": ModalityImage,
		"gif": ModalityImage, "webp": ModalityImage, "svg": ModalityImage, "bmp": ModalityImage,
		"pdf": ModalityPDF,
		"wav": ModalityAudio, "mp3": ModalityAudio, "m4a": ModalityAudio,
		"ogg": ModalityAudio, "flac": ModalityAudio, "aac": ModalityAudio,
		"mp4": ModalityVideo, "mov": ModalityVideo, "webm": ModalityVideo,
		"mkv": ModalityVideo, "avi": ModalityVideo, "m4v": ModalityVideo,
	}
	for extension, want := range cases {
		t.Run(extension, func(t *testing.T) {
			got, mime, ok := MediaModalityFromPath("fixture." + extension)
			if !ok || got != want || mime == "" {
				t.Fatalf("modality = %q mime=%q ok=%v, want %q", got, mime, ok, want)
			}
		})
	}
}

func TestMediaModalitySniffsRenamedImage(t *testing.T) {
	path := filepath.Join(t.TempDir(), "attachment.bin")
	data := []byte{0x89, 'P', 'N', 'G', '\r', '\n', 0x1a, '\n', 0, 0, 0, 0}
	if err := os.WriteFile(path, data, 0o600); err != nil {
		t.Fatal(err)
	}
	modality, mime, ok := MediaModalityFromPath(path)
	if !ok || modality != ModalityImage || mime != "image/png" {
		t.Fatalf("sniffed modality=%q mime=%q ok=%v", modality, mime, ok)
	}
}

func TestProtocolMediaSupportMatrix(t *testing.T) {
	cases := []struct {
		protocol, modality, mime string
		want                     bool
	}{
		{"openai", ModalityImage, "image/png", true},
		{"openai", ModalityPDF, "application/pdf", true},
		{"openai", ModalityAudio, "audio/wav", true},
		{"openai", ModalityAudio, "audio/ogg", false},
		{"openai", ModalityVideo, "video/mp4", true},
		{"anthropic", ModalityImage, "image/webp", true},
		{"anthropic", ModalityPDF, "application/pdf", true},
		{"anthropic", ModalityAudio, "audio/wav", false},
		{"openai-responses", ModalityImage, "image/png", true},
		{"openai-responses", ModalityPDF, "application/pdf", true},
		{"codex", ModalityPDF, "application/pdf", true},
		{"ollama", ModalityImage, "image/bmp", true},
		{"ollama", ModalityPDF, "application/pdf", false},
	}
	for _, testCase := range cases {
		if got := ProtocolSupportsMedia(testCase.protocol, testCase.modality, testCase.mime); got != testCase.want {
			t.Errorf("ProtocolSupportsMedia(%q,%q,%q)=%v want %v", testCase.protocol, testCase.modality, testCase.mime, got, testCase.want)
		}
	}
}
