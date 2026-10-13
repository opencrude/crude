package tools

import (
	"bytes"
	"image"
	"image/png"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"crude/provider"
)

func TestMediaOverridesWinOverCatalogIncludingExplicitNo(t *testing.T) {
	path := filepath.Join(t.TempDir(), "image.png")
	file, err := os.Create(path)
	if err != nil {
		t.Fatal(err)
	}
	if err := png.Encode(file, image.NewRGBA(image.Rect(0, 0, 1, 1))); err != nil {
		_ = file.Close()
		t.Fatal(err)
	}
	if err := file.Close(); err != nil {
		t.Fatal(err)
	}
	const model = "media-override-regression"
	for _, tc := range []struct {
		catalog  bool
		override []string
		accepted bool
	}{
		{true, nil, true}, {true, []string{}, false}, {false, []string{provider.ModalityImage}, true},
	} {
		catalog := []string{}
		if tc.catalog {
			catalog = append(catalog, provider.ModalityImage)
		}
		provider.SetModelModalities(model, catalog)
		policy := &MediaReadPolicy{Model: model, Standard: "openai", Modalities: tc.override}
		handled, text, attachments := policy.tryMediaAttachment(path, nil)
		if !handled || (len(attachments) > 0) != tc.accepted {
			t.Fatalf("catalog=%v override=%#v: %q, attachments=%d", tc.catalog, tc.override, text, len(attachments))
		}
	}
}

func TestMediaByteAdmissionPDFAndAudioProtocolContracts(t *testing.T) {
	var pngBytes bytes.Buffer
	if err := png.Encode(&pngBytes, image.NewRGBA(image.Rect(0, 0, 1, 1))); err != nil {
		t.Fatal(err)
	}
	wav := append([]byte("RIFF"), 0, 0, 0, 0)
	wav = append(wav, []byte("WAVEfmt ")...)
	for _, tc := range []struct {
		standard, mime, kind string
		data                 []byte
		deny                 bool
	}{
		{"openai", "application/pdf", "document", []byte("%PDF-1.7\nfixture\n%%EOF"), false},
		{"anthropic", "application/pdf", "document", []byte("%PDF-1.7\nfixture\n%%EOF"), false},
		{"openai", "audio/wav", "audio", wav, false},
		{"anthropic", "audio/wav", "audio", wav, true},
		{"ollama", "application/pdf", "document", []byte("%PDF-1.7\nfixture\n%%EOF"), true},
		{"unknown", "image/png", "image_url", pngBytes.Bytes(), true},
	} {
		p := &MediaReadPolicy{Model: "fixture", Standard: tc.standard, Modalities: []string{"image", "pdf", "audio"}}
		part, err := p.AdmitBytes("fixture", tc.mime, tc.data)
		if (err != nil) != tc.deny || (!tc.deny && part.Type != tc.kind) {
			t.Fatalf("%s %s: part=%+v err=%v", tc.standard, tc.mime, part, err)
		}
	}
	p := &MediaReadPolicy{Model: "fixture", Standard: "openai", Modalities: []string{}}
	if _, err := p.AdmitBytes("fixture", "image/png", pngBytes.Bytes()); err == nil || !strings.Contains(err.Error(), "disabled") {
		t.Fatal("explicit empty became inherited")
	}
	if _, err := p.AdmitBytes("fixture", "image/png", []byte("not an image")); err == nil || !strings.Contains(err.Error(), "does not match") {
		t.Fatal("spoofed MIME accepted")
	}
}
