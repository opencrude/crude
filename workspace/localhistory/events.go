package localhistory

import (
	"bytes"
	"context"
	"errors"
	"sort"
	"strings"
	"sync"
	"unicode/utf8"
)

// Event is one recorded point of the workspace timeline and the files that
// changed since the snapshot before it. A full snapshot is compared with the
// previous full snapshot. A scoped safety copy observes only its own paths.
type Event struct {
	ID        string   `json:"id"`
	BaseID    string   `json:"base_id,omitempty"`
	CreatedAt int64    `json:"created_unix_ns"`
	Kind      string   `json:"kind"`
	Label     string   `json:"label,omitempty"`
	SessionID string   `json:"session_id,omitempty"`
	MessageID string   `json:"message_id,omitempty"`
	AgentIDs  []string `json:"agent_ids,omitempty"`
	Paths     []string `json:"paths,omitempty"`
}

// EventQuery selects one event's files, a page at a time. After is the last
// path of the previous page.
type EventQuery struct {
	SnapshotID string `json:"snapshot_id"`
	After      string `json:"after,omitempty"`
}

// EventFile holds both retained versions of one changed path. The bytes are
// the stored objects; Local History's own file size setting already bounds
// them. Note explains a version without text, such as a folder or a link.
type EventFile struct {
	Path          string `json:"path"`
	Before        []byte `json:"before,omitempty"`
	After         []byte `json:"after,omitempty"`
	BeforePresent bool   `json:"before_present,omitempty"`
	AfterPresent  bool   `json:"after_present,omitempty"`
	Binary        bool   `json:"binary,omitempty"`
	Note          string `json:"note,omitempty"`
}

// EventPage is one page of an event's changed files, in path order.
type EventPage struct {
	Event Event       `json:"event"`
	Files []EventFile `json:"files"`
	After string      `json:"after,omitempty"`
}

// eventPageFiles is how many files one preview page reads. Every file stays
// reachable through After; the page only bounds a single response.
const eventPageFiles = 40

// eventIndex remembers each snapshot's changed paths. Manifests are
// immutable, so an entry stays valid while its base still exists.
type eventIndex struct {
	mu      sync.Mutex
	changes map[string]eventChange
}

type eventChange struct {
	base  string
	paths []string
	full  bool // observes the whole workspace, so later points compare with it
}

// Events lists the timeline newest first with the files each point changed.
// A path keeps only the events that changed that path or anything below it.
// The first call reads every manifest once; later calls reuse the results.
func (s *Store) Events(ctx context.Context, path string) ([]Event, error) {
	if err := s.beginOperation(); err != nil {
		return nil, err
	}
	defer s.endOperation()
	records, err := s.timeline(-1)
	if err != nil {
		return nil, err
	}
	attribution, err := s.scopeAttribution(ctx)
	if err != nil {
		return nil, err
	}
	changes, err := s.eventChanges(ctx, records)
	if err != nil {
		return nil, err
	}
	path = strings.Trim(path, "/")
	out := make([]Event, 0, len(records))
	for _, record := range records {
		change := changes[record.ID]
		if path != "" && !touches(change.paths, path) {
			continue
		}
		event := Event{ID: record.ID, BaseID: change.base, CreatedAt: record.CreatedAt, Kind: record.Kind, Label: record.Label, Paths: change.paths}
		if scope, ok := attribution[record.ID]; ok {
			event.SessionID, event.MessageID, event.AgentIDs = scope.SessionID, scope.MessageID, scope.agents
		}
		out = append(out, event)
	}
	return out, nil
}

// EventChanges reads one page of an event's changed files with both versions.
func (s *Store) EventChanges(ctx context.Context, q EventQuery) (EventPage, error) {
	events, err := s.Events(ctx, "")
	if err != nil {
		return EventPage{}, err
	}
	var page EventPage
	found := false
	for _, event := range events {
		if event.ID == q.SnapshotID {
			page.Event, found = event, true
			break
		}
	}
	if !found {
		return EventPage{}, errors.New("this history point is no longer kept")
	}
	if err := s.beginOperation(); err != nil {
		return EventPage{}, err
	}
	defer s.endOperation()
	page.Files = []EventFile{}
	start := sort.SearchStrings(page.Event.Paths, q.After)
	if q.After != "" && start < len(page.Event.Paths) && page.Event.Paths[start] == q.After {
		start++
	}
	paths := page.Event.Paths[min(start, len(page.Event.Paths)):]
	if len(paths) > eventPageFiles {
		paths = paths[:eventPageFiles]
		page.After = paths[len(paths)-1]
	}
	if len(paths) == 0 {
		return page, nil
	}
	after, err := s.LoadManifest(page.Event.ID)
	if err != nil {
		return EventPage{}, err
	}
	var before Manifest
	if page.Event.BaseID != "" {
		if before, err = s.LoadManifest(page.Event.BaseID); err != nil {
			return EventPage{}, err
		}
	}
	for _, path := range paths {
		if err := ctx.Err(); err != nil {
			return EventPage{}, err
		}
		file := EventFile{Path: path}
		var beforeNote, afterNote string
		file.Before, file.BeforePresent, beforeNote = s.eventVersion(before.Entries[path], page.Event.BaseID != "" && hasEntry(before, path))
		file.After, file.AfterPresent, afterNote = s.eventVersion(after.Entries[path], hasEntry(after, path))
		file.Note = afterNote
		if file.Note == "" {
			file.Note = beforeNote
		}
		file.Binary = isBinary(file.Before) || isBinary(file.After)
		page.Files = append(page.Files, file)
	}
	return page, nil
}

func hasEntry(m Manifest, path string) bool {
	_, ok := m.Entries[path]
	return ok
}

// eventVersion returns the text of a stored file, or a note for anything else.
func (s *Store) eventVersion(entry Entry, present bool) ([]byte, bool, string) {
	if !present {
		return nil, false, ""
	}
	switch entry.Type {
	case EntryFile:
		data, err := s.readFileBytes(entry.Hash)
		if err != nil {
			return nil, true, "The stored copy of this file is unavailable."
		}
		return data, true, ""
	case EntryDirectory:
		return nil, true, "Folder"
	case EntrySymlink:
		return nil, true, "Link to " + entry.SymlinkTarget
	default:
		return nil, true, "Special file"
	}
}

func isBinary(data []byte) bool {
	return len(data) > 0 && (bytes.IndexByte(data, 0) >= 0 || !utf8.Valid(data))
}

func touches(paths []string, target string) bool {
	for _, path := range paths {
		if path == target || strings.HasPrefix(path, target+"/") {
			return true
		}
	}
	return false
}

// eventChanges fills the index for records (newest first) in time order. A
// cached entry whose base was pruned is computed again against the new base.
func (s *Store) eventChanges(ctx context.Context, records []SnapshotRecord) (map[string]eventChange, error) {
	s.events.mu.Lock()
	defer s.events.mu.Unlock()
	if s.events.changes == nil {
		s.events.changes = make(map[string]eventChange)
	}
	out := make(map[string]eventChange, len(records))
	var base Manifest // the loaded manifest of baseID, when it was needed
	baseID := ""
	for i := len(records) - 1; i >= 0; i-- {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		record := records[i]
		change, ok := s.events.changes[record.ID]
		if !ok || change.base != baseID {
			manifest, err := s.LoadManifest(record.ID)
			if err != nil {
				return nil, err
			}
			change = eventChange{base: baseID, full: manifest.ScopePaths == nil && manifest.ScopeRoots == nil}
			if baseID != "" {
				if base.ID != baseID {
					if base, err = s.LoadManifest(baseID); err != nil {
						return nil, err
					}
				}
				change.paths = changedPaths(base, manifest)
			}
			s.events.changes[record.ID] = change
			if change.full {
				base = manifest
			}
		}
		out[record.ID] = change
		if change.full {
			baseID = record.ID
		}
	}
	for id := range s.events.changes {
		if _, ok := out[id]; !ok {
			delete(s.events.changes, id)
		}
	}
	return out, nil
}

// changedPaths lists the paths both manifests observe whose entries differ.
func changedPaths(before, after Manifest) []string {
	changed := []string{}
	seen := make(map[string]bool, len(after.Entries))
	check := func(path string) {
		if seen[path] {
			return
		}
		seen[path] = true
		if !manifestObserves(before, path) || !manifestObserves(after, path) {
			return
		}
		a, aok := before.Entries[path]
		b, bok := after.Entries[path]
		if aok != bok || (aok && !entriesEqual(a, b)) {
			changed = append(changed, path)
		}
	}
	for path := range after.Entries {
		check(path)
	}
	for path := range before.Entries {
		check(path)
	}
	sort.Strings(changed)
	return changed
}

type scopeOwner struct {
	MessageScope
	agents []string
}

// scopeAttribution maps an agent turn's closing snapshot to its message and
// to the agents whose committed file edits belong to that message.
func (s *Store) scopeAttribution(ctx context.Context) (map[string]scopeOwner, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT s.after_snapshot_id,s.session_id,s.message_id,COALESCE(GROUP_CONCAT(DISTINCT o.agent_id),'')
FROM history_message_scopes s
LEFT JOIN history_agent_operations o ON o.session_id=s.session_id AND o.message_id=s.message_id AND o.status='committed'
WHERE s.after_snapshot_id<>'' GROUP BY s.session_id,s.message_id`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := make(map[string]scopeOwner)
	for rows.Next() {
		var owner scopeOwner
		var after, agents string
		if err := rows.Scan(&after, &owner.SessionID, &owner.MessageID, &agents); err != nil {
			return nil, err
		}
		if agents != "" {
			owner.agents = strings.Split(agents, ",")
			sort.Strings(owner.agents)
		}
		out[after] = owner
	}
	return out, rows.Err()
}
