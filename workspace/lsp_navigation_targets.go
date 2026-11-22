package workspace

import (
	"errors"
	"math"
	"net/url"
	"path/filepath"
	"strings"
	"unicode"
	"unicode/utf8"

	"crude/lsp/lspedit"
	"crude/lsp/lspnav"
	"crude/types/insight"
)

const navigationSourceBytes = 2 << 20

func validateNavigationPosition(text string, line, col int) error {
	if len(text) > navigationSourceBytes || line < 0 || col < 0 || line > math.MaxInt32 || col > math.MaxInt32 {
		return errors.New("navigation source or position exceeds its consumer bound")
	}
	_, err := lspedit.Offset(text, line, col)
	return err
}

func navigationRange(in lspnav.Range) insight.LSPNavigationRange {
	return insight.LSPNavigationRange{
		Start: insight.LSPNavigationPosition{Line: in.Start.Line, Character: in.Start.Character},
		End:   insight.LSPNavigationPosition{Line: in.End.Line, Character: in.End.Character},
	}
}

func validateNavigationRange(text string, r insight.LSPNavigationRange) error {
	if err := validateNavigationPosition(text, r.Start.Line, r.Start.Character); err != nil {
		return err
	}
	if err := validateNavigationPosition(text, r.End.Line, r.End.Character); err != nil {
		return err
	}
	if navigationPositionBefore(r.End, r.Start) {
		return errors.New("navigation range is reversed")
	}
	return nil
}

func navigationPositionBefore(a, b insight.LSPNavigationPosition) bool {
	return a.Line < b.Line || a.Line == b.Line && a.Character < b.Character
}

func normalizeNavigation(root, text string, response insight.LSPNavigationResponse, line, col int, includeDeclaration bool) (insight.LSPNavigation, error) {
	out := insight.LSPNavigation{
		Boundary: response.Boundary, WireVersion: response.WireVersion, Kind: response.Kind,
		Line: line, Col: col, IncludeDeclaration: includeDeclaration,
	}
	if response.Unsupported {
		if len(response.JSON) != 0 {
			return insight.LSPNavigation{}, errors.New("unsupported navigation response also contains target data")
		}
		out.Answer = insight.LSPNavigationUnsupported
		return out, nil
	}
	parsed, err := lspnav.Parse(lspnav.Kind(response.Kind), response.JSON)
	if err != nil {
		return insight.LSPNavigation{}, err
	}
	out.Answer = string(parsed.Answer)
	for _, target := range parsed.Targets {
		row := insight.LSPNavigationTarget{URI: target.URI, Range: navigationRange(target.Range),
			Selection: navigationRange(target.Selection), Link: target.Link}
		if target.Origin != nil {
			origin := navigationRange(*target.Origin)
			if err := validateNavigationRange(text, origin); err != nil {
				return insight.LSPNavigation{}, errors.New("navigation origin does not fit the exact original source")
			}
			row.Origin = &origin
		}
		path, err := navigationURIPath(target.URI)
		if err != nil {
			row.UnavailableReason = err.Error()
		} else {
			row.Path = path
			rel, err := filepath.Rel(root, path)
			row.External = err != nil || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator))
		}
		out.Targets = append(out.Targets, row)
	}
	return out, nil
}

// navigationURIPath authorizes only syntactically unambiguous, native, local
// regular-file candidates. It performs no I/O. External candidates are marked
// for explicit selection; a server cannot make listing read home or dependencies.
func navigationURIPath(raw string) (string, error) {
	if raw == "" || len(raw) > lspnav.MaxURIBytes || !utf8.ValidString(raw) || strings.IndexFunc(raw, unicode.IsControl) >= 0 || strings.Contains(raw, "#") {
		return "", errors.New("unsupported or oversized navigation URI")
	}
	u, err := url.Parse(raw)
	if err != nil || u.Scheme != "file" || u.Opaque != "" || u.User != nil ||
		u.RawQuery != "" || u.ForceQuery || u.Fragment != "" || u.RawFragment != "" ||
		u.Host != "" && !strings.EqualFold(u.Host, "localhost") {
		return "", errors.New("navigation supports only local file URIs without authority, query or fragment")
	}
	if u.Path == "" || !utf8.ValidString(u.Path) || strings.IndexFunc(u.Path, unicode.IsControl) >= 0 || strings.Contains(u.Path, "\\") {
		return "", errors.New("invalid local navigation path")
	}
	path := filepath.FromSlash(u.Path)
	if trimmed := strings.TrimPrefix(path, string(filepath.Separator)); filepath.VolumeName(trimmed) != "" {
		path = trimmed
	}
	if !filepath.IsAbs(path) || strings.HasPrefix(path, string(filepath.Separator)+string(filepath.Separator)) {
		return "", errors.New("navigation path must be native absolute and host-local")
	}
	path = filepath.Clean(path)
	if len(path) > 4096 {
		return "", errors.New("navigation path exceeds its consumer bound")
	}
	return path, nil
}
