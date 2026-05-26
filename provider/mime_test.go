package provider

import "testing"

func TestImageMimeFromExt_Known(t *testing.T) {
	tests := []struct {
		ext      string
		wantMime string
		wantOK   bool
	}{
		{".png", "image/png", true},
		{".jpg", "image/jpeg", true},
		{".jpeg", "image/jpeg", true},
		{".gif", "image/gif", true},
		{".webp", "image/webp", true},
		{".svg", "image/svg+xml", true},
		{".bmp", "image/bmp", true},
	}
	for _, tt := range tests {
		mime, ok := ImageMimeFromExt(tt.ext)
		if mime != tt.wantMime || ok != tt.wantOK {
			t.Errorf("ImageMimeFromExt(%q): got (%q, %v), want (%q, %v)", tt.ext, mime, ok, tt.wantMime, tt.wantOK)
		}
	}
}

func TestImageMimeFromExt_UpperCase(t *testing.T) {
	mime, ok := ImageMimeFromExt(".PNG")
	if !ok || mime != "image/png" {
		t.Errorf("ImageMimeFromExt(.PNG): got (%q, %v), want (image/png, true)", mime, ok)
	}
	mime, ok = ImageMimeFromExt(".JPG")
	if !ok || mime != "image/jpeg" {
		t.Errorf("ImageMimeFromExt(.JPG): got (%q, %v), want (image/jpeg, true)", mime, ok)
	}
}

func TestImageMimeFromExt_Unknown(t *testing.T) {
	mime, ok := ImageMimeFromExt(".txt")
	if ok || mime != "" {
		t.Errorf("ImageMimeFromExt(.txt): got (%q, %v), want (\"\", false)", mime, ok)
	}
	mime, ok = ImageMimeFromExt(".pdf")
	if ok || mime != "" {
		t.Errorf("ImageMimeFromExt(.pdf): got (%q, %v), want (\"\", false)", mime, ok)
	}
	mime, ok = ImageMimeFromExt("")
	if ok || mime != "" {
		t.Errorf("ImageMimeFromExt(\"\"): got (%q, %v), want (\"\", false)", mime, ok)
	}
	mime, ok = ImageMimeFromExt(".tiff")
	if ok || mime != "" {
		t.Errorf("ImageMimeFromExt(.tiff): got (%q, %v), want (\"\", false)", mime, ok)
	}
}

func TestImageExtFromMime_Known(t *testing.T) {
	tests := []struct {
		mime string
		want string
	}{
		{"image/png", ".png"},
		{"image/jpeg", ".jpg"},
		{"image/jpg", ".jpg"},
		{"image/gif", ".gif"},
		{"image/webp", ".webp"},
		{"image/svg+xml", ".svg"},
		{"image/bmp", ".bmp"},
	}
	for _, tt := range tests {
		got := ImageExtFromMime(tt.mime)
		if got != tt.want {
			t.Errorf("ImageExtFromMime(%q): got %q, want %q", tt.mime, got, tt.want)
		}
	}
}

func TestImageExtFromMime_UpperCase(t *testing.T) {
	got := ImageExtFromMime("IMAGE/PNG")
	if got != ".png" {
		t.Errorf("ImageExtFromMime(IMAGE/PNG): got %q, want .png", got)
	}
	got = ImageExtFromMime("Image/Jpeg")
	if got != ".jpg" {
		t.Errorf("ImageExtFromMime(Image/Jpeg): got %q, want .jpg", got)
	}
}

func TestImageExtFromMime_Unknown(t *testing.T) {
	got := ImageExtFromMime("image/tiff")
	if got != ".png" {
		t.Errorf("ImageExtFromMime(image/tiff): got %q, want .png (default)", got)
	}
	got = ImageExtFromMime("application/octet-stream")
	if got != ".png" {
		t.Errorf("ImageExtFromMime(application/octet-stream): got %q, want .png (default)", got)
	}
	got = ImageExtFromMime("")
	if got != ".png" {
		t.Errorf("ImageExtFromMime(\"\"): got %q, want .png (default)", got)
	}
}

func TestImageMimeFromExt_ExtFromMime_RoundTrip(t *testing.T) {
	// All extensions that have a unique round-trip
	exts := []string{".png", ".gif", ".webp", ".svg", ".bmp"}
	for _, ext := range exts {
		mime, ok := ImageMimeFromExt(ext)
		if !ok {
			t.Errorf("ImageMimeFromExt(%q): not ok", ext)
			continue
		}
		got := ImageExtFromMime(mime)
		if got != ext {
			t.Errorf("round-trip %q → %q → %q: got %q", ext, mime, ext, got)
		}
	}
	// .jpg/.jpeg both map to image/jpeg → .jpg
	mime, ok := ImageMimeFromExt(".jpeg")
	if !ok || mime != "image/jpeg" {
		t.Errorf("ImageMimeFromExt(.jpeg): got (%q, %v)", mime, ok)
	}
	got := ImageExtFromMime(mime)
	if got != ".jpg" {
		t.Errorf("ImageExtFromMime(image/jpeg): got %q, want .jpg", got)
	}
}
