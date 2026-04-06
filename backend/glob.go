package backend

import (
	"path/filepath"
	"strings"
)

// matchGlobPattern reports whether relPath (slash-separated) matches the
// given glob pattern.  It supports:
//   - Standard filepath.Match wildcards (* ? [range]) within a single segment
//   - ** to match zero or more path segments (including none)
//
// Pattern and relPath must both use forward slashes.
func matchGlobPattern(pattern, relPath string) bool {
	patParts := strings.Split(filepath.ToSlash(pattern), "/")
	relParts := strings.Split(filepath.ToSlash(relPath), "/")
	return matchSegments(patParts, relParts)
}

// matchSegments is a recursive helper for matchGlobPattern.
func matchSegments(pat, rel []string) bool {
	for len(pat) > 0 && len(rel) > 0 {
		p := pat[0]
		if p == "**" {
			// ** can consume zero or more rel segments.
			// Try matching the remainder of pat against every suffix of rel.
			for i := 0; i <= len(rel); i++ {
				if matchSegments(pat[1:], rel[i:]) {
					return true
				}
			}
			return false
		}
		// Single-segment match via filepath.Match (handles *, ?, [...]).
		ok, err := filepath.Match(p, rel[0])
		if err != nil || !ok {
			return false
		}
		pat = pat[1:]
		rel = rel[1:]
	}
	// Consume trailing ** segments (they can match zero segments).
	for len(pat) > 0 && pat[0] == "**" {
		pat = pat[1:]
	}
	return len(pat) == 0 && len(rel) == 0
}
