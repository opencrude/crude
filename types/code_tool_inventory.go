package types

import "time"

// CodeTool describes a managed tool installed by Crude.
type CodeTool struct {
	ID, Package, Version, Dir, Executable, Bin, Command string
	SHA256, DigestOrigin, Source, URL, Archive          string
	Size                                                int64
	Files                                               int
	Licenses, Workspaces                                []string
	InstalledAt, LastUsed                               time.Time
	InstalledBy, Approval                               string
	FileEntries                                         []CodeToolFile
}

// CodeToolFile is a literal entry inside a managed tool folder.
type CodeToolFile struct {
	Path, Kind, LinkTarget string
	Size                   int64
}

// CodeToolHistoryEvent records an install, failure, use or removal.
type CodeToolHistoryEvent struct {
	Time                                   time.Time
	Kind, ToolID, Package, Version, Detail string
	By, Approval                           string
}

// UnmanagedCodeTool is a program resolved from outside the shared store.
// It is informational and cannot be selected for removal.
type UnmanagedCodeTool struct {
	Name, Command, Executable, Source string
}

// CodeToolInventory is a point-in-time read. History is newest first.
type CodeToolInventory struct {
	Tools        []CodeTool
	TotalBytes   int64
	Unrecognized []string
	Unmanaged    []UnmanagedCodeTool
	// Project is the same bounded assembly snapshot used to resolve Unmanaged.
	// An empty or incomplete snapshot is not evidence that a tool is absent.
	Project       AssemblyPlan
	History       []CodeToolHistoryEvent
	HistoryTotal  int
	HistoryOffset int
}

// CodeToolRemovalPreview is an immutable description of the exact removal.
type CodeToolRemovalPreview struct {
	Tools []CodeTool
	Paths []string
	Bytes int64
	Token string
}
