package coreprotocol

import "crude/fileio"

const ProjectReplacePlan = "files.replace_plan"

type ProjectReplaceRequest struct {
	Matches     []fileio.ProjectSearchMatch `json:"matches"`
	Query       string                      `json:"query"`
	Replacement string                      `json:"replacement"`
	Options     fileio.ProjectSearchOptions `json:"options"`
}
type ProjectReplaceResult struct {
	Edits   []fileio.ProjectFileReplacement `json:"edits"`
	Skipped int                             `json:"skipped"`
}
