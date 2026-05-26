package provider

import "strings"

// ImageMimeFromExt returns the MIME type for a known image extension
// and a boolean indicating whether the extension is a recognised image type.
// The extension should include the leading dot (e.g. ".png").
func ImageMimeFromExt(ext string) (string, bool) {
	switch strings.ToLower(ext) {
	case ".png":
		return "image/png", true
	case ".jpg", ".jpeg":
		return "image/jpeg", true
	case ".gif":
		return "image/gif", true
	case ".webp":
		return "image/webp", true
	case ".svg":
		return "image/svg+xml", true
	case ".bmp":
		return "image/bmp", true
	default:
		return "", false
	}
}

// ImageExtFromMime returns the preferred file extension for an image MIME type.
// Returns ".png" as the default for unknown image MIME types.
func ImageExtFromMime(mime string) string {
	lower := strings.ToLower(mime)
	switch {
	case strings.Contains(lower, "jpeg") || strings.Contains(lower, "jpg"):
		return ".jpg"
	case strings.Contains(lower, "png"):
		return ".png"
	case strings.Contains(lower, "gif"):
		return ".gif"
	case strings.Contains(lower, "webp"):
		return ".webp"
	case strings.Contains(lower, "svg"):
		return ".svg"
	case strings.Contains(lower, "bmp"):
		return ".bmp"
	default:
		return ".png"
	}
}
