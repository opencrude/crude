package viewmodel

import (
	"time"

	"crude/tui/editor"
	"crude/tui/window"
	"crude/types"
)

type openFilesInspection struct {
	shellInspection
	ObservedAt time.Time            `json:"observed_at"`
	Editors    []openFileInspection `json:"editors"`
}

type openFileInspection struct {
	TabID              window.TabID         `json:"tab_id"`
	Source             *types.InsightSource `json:"canonical_source,omitempty"`
	SourceMatchesView  bool                 `json:"source_matches_view"`
	DiagnosticEvidence string               `json:"diagnostic_evidence"`
	DiagnosticID       string               `json:"diagnostic_id,omitempty"`
	Errors             int                  `json:"errors,omitempty"`
	Warnings           int                  `json:"warnings,omitempty"`
	Other              int                  `json:"other,omitempty"`
}

// This read projects the already-applied Problems facts on Update. It does not
// sample a server, export document text or make the shell its diagnostic owner.
func (m *App) inspectOpenFiles(shell shellInspection) openFilesInspection {
	result := openFilesInspection{shellInspection: shell, ObservedAt: time.Now(),
		Editors: make([]openFileInspection, 0, len(shell.Tabs))}
	for _, tab := range shell.Tabs {
		ed := m.editorForTab(tab.ID)
		if ed == nil {
			continue
		}
		result.Editors = append(result.Editors, inspectOpenFile(tab.ID, ed, m.lspFacts[tab.Path]))
	}
	return result
}

func inspectOpenFile(tab window.TabID, ed *editor.Editor, facts types.InsightDiagnostics) openFileInspection {
	source, matches := ed.CurrentInsightSource()
	file := openFileInspection{TabID: tab, SourceMatchesView: matches, DiagnosticEvidence: "not_observed"}
	if source.DocumentID != "" {
		// The tab already carries the path. Do not duplicate it for every view.
		source.Path = ""
		file.Source = &source
	}
	if facts.ID == "" {
		return file
	}
	file.DiagnosticID = facts.ID
	switch {
	case facts.State == types.InsightStale:
		file.DiagnosticEvidence = "out_of_date"
	case facts.Limited:
		file.DiagnosticEvidence = "limited"
	case ed.MatchesDiagnosticSource(facts):
		file.DiagnosticEvidence = string(facts.State)
	default:
		file.DiagnosticEvidence = "out_of_date"
	}
	// Counts describe the referenced batch only, including when it is old.
	for _, diagnostic := range facts.Diagnostics {
		switch diagnostic.Severity {
		case editor.DiagError:
			file.Errors++
		case editor.DiagWarning:
			file.Warnings++
		default:
			file.Other++
		}
	}
	return file
}
