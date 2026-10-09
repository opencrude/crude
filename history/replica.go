package history

// InstallReplica replaces the read mirror with an authoritative core snapshot.
// It does not fold, evict, allocate identities, read media, or persist files.
func (s *Store) InstallReplica(entries []Entry) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.entries = make([]Entry, len(entries))
	for i, entry := range entries {
		s.entries[i] = entry.Clone()
	}
	s.reindexLocked()
}
