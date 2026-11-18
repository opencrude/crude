package lspclient

import (
	"errors"
	"path/filepath"
	"slices"
	"strings"
	"unicode/utf8"

	"crude/types"
)

const (
	maxTransportDiagnostics     = 4096
	maxTransportDiagnosticBytes = 1 << 20
	maxBoundaryIdentityBytes    = 4096
	maxBoundaryUnknownInputs    = 64
)

// diagnosticPath checks the caller's transport input, not its authority over
// canonical source or project context. The manager adjudicates those owners.
func diagnosticPath(path string, b types.InsightBoundary) (string, error) {
	if !filepath.IsAbs(path) || strings.ContainsRune(path, 0) {
		return "", errors.New("invalid LSP document path")
	}
	fp := filepath.Clean(path)
	if b.Source.Path != fp {
		return "", errors.New("LSP boundary path does not match absolute document path")
	}
	if b.Source.Revision == 0 || b.Context.Generation == 0 ||
		(b.Source.Kind != "canonical" && b.Source.Kind != "disk") {
		return "", errors.New("invalid LSP source kind or boundary generation")
	}
	for _, s := range []string{b.Source.WorkspaceGeneration, b.Source.DocumentID, b.Source.Path,
		b.Source.Lifetime, b.Context.ID, b.Context.Root, b.Producer.InstanceID, b.Producer.Provider, b.Producer.ToolIdentity} {
		if s == "" || len(s) > maxBoundaryIdentityBytes || !utf8.ValidString(s) || strings.ContainsRune(s, 0) {
			return "", errors.New("invalid LSP boundary identity")
		}
	}
	if len(b.Context.UnknownInputs) > maxBoundaryUnknownInputs {
		return "", errors.New("LSP boundary unknown inputs exceed transport limit")
	}
	for _, s := range b.Context.UnknownInputs {
		if len(s) > maxBoundaryIdentityBytes || !utf8.ValidString(s) || strings.ContainsRune(s, 0) {
			return "", errors.New("invalid LSP boundary unknown input")
		}
	}
	return fp, nil
}

func cloneBoundary(b types.InsightBoundary) types.InsightBoundary {
	b.Context.UnknownInputs = slices.Clone(b.Context.UnknownInputs)
	return b
}

func equalBoundary(a, b types.InsightBoundary) bool {
	return a.Source == b.Source && a.Producer == b.Producer &&
		a.Context.ID == b.Context.ID && a.Context.Root == b.Context.Root &&
		a.Context.Generation == b.Context.Generation &&
		slices.Equal(a.Context.UnknownInputs, b.Context.UnknownInputs)
}

func clonePublication(p types.InsightDiagnosticPublication) types.InsightDiagnosticPublication {
	p.Boundary = cloneBoundary(p.Boundary)
	p.Diagnostics = slices.Clone(p.Diagnostics)
	if p.ReportedVersion != nil {
		v := *p.ReportedVersion
		p.ReportedVersion = &v
	}
	return p
}
