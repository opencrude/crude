// Package fileuri converts native paths to local file URIs and SQLite DSNs.
package fileuri

import (
	"fmt"
	"net/url"
	"path/filepath"
	"runtime"
	"strings"
)

// FromPath returns the canonical file URI for path, matching the LSP transport.
func FromPath(path string) string {
	if path == "" {
		return ""
	}
	if abs, err := filepath.Abs(path); err == nil {
		path = abs
	}
	if runtime.GOOS == "windows" && len(path) >= 2 && path[1] == ':' {
		path = "/" + strings.ToUpper(path[:1]) + path[1:]
	}
	return (&url.URL{Scheme: "file", Path: filepath.ToSlash(path)}).String()
}

// ToPath accepts only absolute local file URIs, not remote authorities or UNC paths.
func ToPath(uri string) (string, error) {
	u, err := url.Parse(uri)
	if err != nil {
		return "", err
	}
	if u.Scheme != "file" || u.Opaque != "" || u.User != nil ||
		(u.Host != "" && !strings.EqualFold(u.Host, "localhost")) ||
		u.RawQuery != "" || u.ForceQuery || u.Fragment != "" || strings.Contains(uri, "#") ||
		!strings.HasPrefix(u.Path, "/") || strings.HasPrefix(u.Path, "//") ||
		strings.ContainsAny(u.Path, "\\\x00") {
		return "", fmt.Errorf("invalid local file URI %q", uri)
	}
	path := u.Path
	if runtime.GOOS == "windows" {
		if len(path) < 4 || path[0] != '/' || path[2] != ':' || path[3] != '/' ||
			(path[1] < 'a' || path[1] > 'z') && (path[1] < 'A' || path[1] > 'Z') {
			return "", fmt.Errorf("invalid native file URI %q", uri)
		}
		path = strings.ToUpper(path[1:2]) + path[2:]
	}
	return filepath.Clean(filepath.FromSlash(path)), nil
}

// SQLiteDSN appends the caller's unmodified SQLite query to an escaped file URI.
func SQLiteDSN(path, rawQuery string) string {
	uri := FromPath(path)
	if rawQuery != "" {
		uri += "?" + rawQuery
	}
	return uri
}
