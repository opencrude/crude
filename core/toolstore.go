package core

import (
	"log"
	"path/filepath"

	"crude/buildinfo"
	"crude/insight/catalog"
	"crude/toolstore"
)

// openToolStore opens the core's tool store under the Crude data folder and
// reports catalog packs that were not used. A store that cannot open leaves
// code intelligence with tools from the system path and settings only.
func openToolStore() *toolstore.Store {
	for _, problem := range catalog.PackProblems() {
		log.Printf("crude core: catalog pack %s not used: %s", problem.Path, problem.Reason)
	}
	home, err := buildinfo.Home()
	if err != nil {
		return nil
	}
	store, err := toolstore.Open(filepath.Join(home, buildinfo.DataDir(), "tools"))
	if err != nil {
		log.Printf("crude core: tool store not available: %v", err)
		return nil
	}
	return store
}
