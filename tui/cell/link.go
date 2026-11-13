package cell

import (
	"net/url"
	"strings"
)

// Link is immutable terminal hyperlink metadata, separate from visible text.
// Cells and cached rows share it; repaint/occlusion replaces it with the glyph.
type Link struct{ target, params string }

// NewLink accepts web/mail destinations only. Terminal controls, unsafe schemes
// and malformed targets never become terminal commands or executable links.
func NewLink(target, params string) *Link {
	if target == "" || len(target) > 8192 || len(params) > 256 {
		return nil
	}
	for _, r := range target + params {
		if r < 32 || r >= 127 && r < 160 {
			return nil
		}
	}
	parsed, err := url.Parse(target)
	if err != nil {
		return nil
	}
	switch strings.ToLower(parsed.Scheme) {
	case "http", "https":
		if parsed.Host == "" {
			return nil
		}
	case "mailto":
		if parsed.Opaque == "" && parsed.Path == "" {
			return nil
		}
	default:
		return nil
	}
	return &Link{target: target, params: params}
}
func (l *Link) Target() string {
	if l == nil {
		return ""
	}
	return l.target
}
func (l *Link) Params() string {
	if l == nil {
		return ""
	}
	return l.params
}
func (l *Link) Equal(other *Link) bool {
	if l == other {
		return true
	}
	return l != nil && other != nil && *l == *other
}
