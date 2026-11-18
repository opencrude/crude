package coreprotocol

import "time"

const (
	ClientFilesBrowse         = "client_files.browse"
	ClientFilesBrowseExternal = "client_files.browse_external"
	ClientFilesReadExternal   = "client_files.read_external"
)

type ClientDirectoryEntry struct {
	Name      string    `json:"name"`
	Path      string    `json:"path"`
	IsDir     bool      `json:"is_dir"`
	IsSymlink bool      `json:"is_symlink"`
	ModTime   time.Time `json:"mod_time"`
}
