package tools

import (
	"image"
	"image/png"
	"os"
	"path/filepath"
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
