package coreprotocol

// FaultCoreUpdating refuses a session while the core updates saved data.
// Its Welcome carries the update's progress; WatchDataUpdate follows it.
const FaultCoreUpdating = "core_updating"

// DataUpdateRequest asks for the data update's progress instead of a session.
type DataUpdateRequest struct {
	// Retry runs the update again after the user freed disk space.
	Retry bool `json:"retry,omitempty"`
}

// DataUpdatePhase is the stage of a data update.
type DataUpdatePhase string

const (
	// DataUpdateChecking reads the format of every saved database.
	DataUpdateChecking DataUpdatePhase = "checking"
	// DataUpdateUpdating backs up and updates the databases that need it.
	DataUpdateUpdating DataUpdatePhase = "updating"
	// DataUpdateFinished ends the update; the core starts normally.
	DataUpdateFinished DataUpdatePhase = "finished"
	// DataUpdateNoSpace waits for free disk space for the backups.
	DataUpdateNoSpace DataUpdatePhase = "no_space"
	// DataUpdateNewer refuses data saved by a newer Crude. Nothing changes.
	DataUpdateNewer DataUpdatePhase = "newer"
	// DataUpdateBlocked keeps the core closed until a file can be safely updated.
	DataUpdateBlocked DataUpdatePhase = "blocked"
)

// DataUpdate is the progress of the core's data update.
type DataUpdate struct {
	Phase DataUpdatePhase `json:"phase"`
	// Version is the version of the Crude that runs the update.
	Version string `json:"version"`
	// Theme is the theme preset the update screen uses.
	Theme string `json:"theme,omitempty"`
	// Done and Total measure the work in bytes of saved data.
	Done  int64 `json:"done"`
	Total int64 `json:"total"`
	// Step says what the update does now, in user words.
	Step string `json:"step,omitempty"`
	// Files counts the databases the update changes; FilesDone the finished ones.
	Files     int `json:"files"`
	FilesDone int `json:"files_done"`
	// Stores reports each kind of saved data.
	Stores []DataUpdateStore `json:"stores,omitempty"`
	// Problems lists databases the update could not update. Each stays as it was.
	Problems []DataUpdateProblem `json:"problems,omitempty"`
	// Backup is the folder that holds a copy of every database before its update.
	Backup string `json:"backup,omitempty"`
	// Needed and Free describe the disk space the backups need.
	Needed int64 `json:"needed,omitempty"`
	Free   int64 `json:"free,omitempty"`
	// Newer lists databases saved by a newer Crude.
	Newer []DataUpdateProblem `json:"newer,omitempty"`
	// Final marks the last status of this update.
	Final bool `json:"final,omitempty"`
}

// DataUpdateStore is one kind of saved data in an update.
type DataUpdateStore struct {
	Name   string `json:"name"`
	Files  int    `json:"files"`
	Done   int    `json:"done"`
	Failed int    `json:"failed"`
}

// DataUpdateProblem names one database and what happened to it.
type DataUpdateProblem struct {
	Path   string `json:"path"`
	Detail string `json:"detail"`
}

// Quiet reports a finished update that changed nothing the user must see.
func (u DataUpdate) Quiet() bool {
	return u.Phase == DataUpdateFinished && u.Files == 0 && len(u.Problems) == 0
}
