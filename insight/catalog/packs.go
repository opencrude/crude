package catalog

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync/atomic"
)

// MaxPacks bounds how many pack files one directory may supply.
const MaxPacks = 64

// PackProblem names a pack file that was not used and why.
type PackProblem struct {
	Path   string
	Reason string
}

type activeCatalog struct {
	catalog  *Catalog
	dir      string
	problems []PackProblem
}

var active atomic.Pointer[activeCatalog]

// Current returns the catalog every product path reads: the embedded catalog
// plus the packs loaded by UsePacks. It never returns nil.
func Current() *Catalog {
	if state := active.Load(); state != nil {
		return state.catalog
	}
	c, err := Builtin()
	if err != nil {
		empty, _ := build(nil, Document{Format: FormatVersion})
		return empty
	}
	return c
}

// UsePacks loads every "*.json" file in dir, in name order, over the embedded
// catalog and makes the result Current. A pack that does not decode or
// validate is skipped and reported; the others still load. A missing
// directory means no packs. Packs are data only: a pack can name a command,
// and that command still runs only through trust and the normal launch path.
func UsePacks(dir string) []PackProblem {
	base, err := Builtin()
	if err != nil {
		return []PackProblem{{Path: "embedded catalog", Reason: err.Error()}}
	}
	state := &activeCatalog{catalog: base, dir: dir}
	entries, err := os.ReadDir(dir)
	if err != nil && !os.IsNotExist(err) {
		state.problems = append(state.problems, PackProblem{Path: dir, Reason: err.Error()})
	}
	var names []string
	for _, entry := range entries {
		if entry.Type().IsRegular() && strings.HasSuffix(entry.Name(), ".json") {
			names = append(names, entry.Name())
		}
	}
	sort.Strings(names)
	if len(names) > MaxPacks {
		state.problems = append(state.problems, PackProblem{Path: dir, Reason: fmt.Sprintf("only the first %d packs are used", MaxPacks)})
		names = names[:MaxPacks]
	}
	for _, name := range names {
		path := filepath.Join(dir, name)
		data, err := readBounded(path)
		if err == nil {
			var next *Catalog
			if next, err = Load(state.catalog, data); err == nil {
				state.catalog = next
				continue
			}
		}
		state.problems = append(state.problems, PackProblem{Path: path, Reason: err.Error()})
	}
	active.Store(state)
	return append([]PackProblem(nil), state.problems...)
}

// PackProblems returns the problems of the last UsePacks call.
func PackProblems() []PackProblem {
	if state := active.Load(); state != nil {
		return append([]PackProblem(nil), state.problems...)
	}
	return nil
}

func readBounded(path string) ([]byte, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	info, err := f.Stat()
	if err != nil {
		return nil, err
	}
	if info.Size() > MaxDocumentBytes {
		return nil, fmt.Errorf("pack is larger than %d bytes", MaxDocumentBytes)
	}
	data := make([]byte, info.Size())
	if _, err := f.ReadAt(data, 0); err != nil && info.Size() > 0 {
		return nil, err
	}
	return data, nil
}
