package memorybrowser

import "crude/memory"

const (
	ModeKnowledge   = "knowledge"
	ModeEpisodes    = "episodes"
	MutationPin     = "pin"
	MutationCorrect = "correct"
	MutationRetire  = "retire"
	pageBrowse      = "browse"
	pageDetail      = "detail"
	pageCorrect     = "correct"
	pageRetire      = "retire"
	pageMenu        = "actions"
)

type SearchMsg struct {
	Target   *Screen
	Epoch    uint64
	Sequence uint64
	Query    string
	Mode     string
	Status   string
}

type DetailMsg struct {
	Target   *Screen
	Epoch    uint64
	Sequence uint64
	ID       string
	Mode     string
	Episode  memory.Episode
}

type MutationMsg struct {
	Target           *Screen
	Epoch            uint64
	Sequence         uint64
	Kind             string
	ID               string
	ExpectedRevision int64
	Pinned           bool
	Replacement      memory.Record
}

type CancelMsg struct {
	Target *Screen
	Epoch  uint64
}

type DetailData struct {
	Record    memory.Record
	Episode   memory.Episode
	Revisions []memory.Revision
	Feedback  []memory.Feedback
	Exposures []memory.Exposure
}

func (detail DetailData) ID() string {
	if detail.Record.ID != "" {
		return detail.Record.ID
	}
	return detail.Episode.ID
}

func cloneRecord(record memory.Record) memory.Record {
	if record.ScopePaths != nil {
		record.ScopePaths = append([]string{}, record.ScopePaths...)
	}
	if record.Evidence != nil {
		record.Evidence = append([]memory.Evidence{}, record.Evidence...)
	}
	return record
}
