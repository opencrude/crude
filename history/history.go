// Package history keeps the global cross-session prompt history
// (~/.crude/history.json) useful instead of noisy. It is deliberately
// dependency-free: an in-memory ordered store with three cheap, well-defined
// mechanisms layered on top of a normalization function.
//
//  1. Exact-repeat folding — prompts are keyed by their normalized text
//     (trim + whitespace collapse + lowercase). A repeat bumps the entry's
//     count and lastSeen and refreshes its stored text, so "hello" typed
//     thirty times becomes one entry with count 30 instead of thirty rows.
//
//  2. Retry-window collapsing — near-identical resubmits within a short
//     window (a typo fix, a re-send after an error) merge into the entry they
//     retry. Similarity is Jaccard over character 4-gram shingles of the
//     normalized text, applied only to texts long enough for the metric to be
//     meaningful (short strings like "hi" must match exactly).
//
//  3. Score-based eviction — the list is capped; when it overflows, the
//     entry with the lowest score is dropped, where score decays with age:
//     score = count * 2^(-ageDays). Frequently used prompts survive; stale
//     one-offs go first.
//
// Recall order is insertion order of distinct entries (oldest → newest), which
// is what the composer's up/down browsing expects.
package history

import (
	"encoding/json"
	"math"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"
	"unicode/utf8"

	"crude/buildinfo"
)

const (
	// DefaultMaxEntries caps the file size; matches the historical cap.
	DefaultMaxEntries = 500

	// RetryWindow is how long after an entry a near-identical prompt is
	// treated as a retry of it rather than a new entry.
	RetryWindow = 2 * time.Minute

	// minShingleLen is the minimum normalized length for fuzzy matching.
	// Below it, similarity on shingles is noise ("hi" vs "no").
	minShingleLen = 12

	// maxRecordBytes skips pathological paste-sized prompts entirely.
	maxRecordBytes = 32 << 10 // 32 KiB

	shingleN = 4
)

// Entry is one distinct prompt. Text is the most recent raw variant typed by
// the user; FirstSeen/LastSeen/Count describe how the prompt is used.
type Entry struct {
	Text      string `json:"text"`
	Count     int    `json:"count"`
	FirstSeen int64  `json:"first_seen"` // unix seconds
	LastSeen  int64  `json:"last_seen"`  // unix seconds
}

// Store is safe for concurrent use: Record runs on the UI thread while Save
// snapshots from a detached goroutine.
type Store struct {
	mu      sync.Mutex
	entries []Entry // oldest → newest (distinct entries)
	index   map[string]int
	max     int
}

// New returns an empty store with the default cap.
func New() *Store {
	return &Store{index: make(map[string]int), max: DefaultMaxEntries}
}

func (s *Store) Len() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return len(s.entries)
}

// Record folds one submitted prompt into the store and reports whether it
// created a new entry (true) or merged into an existing one (false).
func (s *Store) Record(text string, now time.Time) bool {
	raw := strings.TrimSpace(text)
	if raw == "" || len(raw) > maxRecordBytes {
		return false
	}
	key := Normalize(raw)
	if key == "" {
		return false
	}

	s.mu.Lock()
	defer s.mu.Unlock()
	ts := now.Unix()

	// 1. Exact repeat.
	if i, ok := s.index[key]; ok {
		s.mergeLocked(i, raw, ts)
		return false
	}

	// 2. Retry of a recent near-identical prompt (newest first).
	if utf8.RuneCountInString(key) >= minShingleLen {
		sh := shingles(key)
		for i := len(s.entries) - 1; i >= 0; i-- {
			e := &s.entries[i]
			if ts-e.LastSeen > int64(RetryWindow/time.Second) {
				break // MRU tail: anything older is outside the window
			}
			if utf8.RuneCountInString(Normalize(e.Text)) < minShingleLen {
				continue
			}
			if jaccard(sh, shingles(Normalize(e.Text))) >= 0.85 {
				s.mergeLocked(i, raw, ts)
				return false
			}
		}
	}

	// 3. New entry.
	s.entries = append(s.entries, Entry{Text: raw, Count: 1, FirstSeen: ts, LastSeen: ts})
	s.index[key] = len(s.entries) - 1
	s.evictLocked(now)
	return true
}

// mergeLocked folds a repeat into entries[i]: refresh text/count/lastSeen and
// move the entry to the MRU tail so Recall order stays newest-last (and the
// retry-window scan can break at the first stale entry). Must hold mu.
func (s *Store) mergeLocked(i int, raw string, ts int64) {
	e := s.entries[i]
	e.Count++
	e.LastSeen = ts
	e.Text = raw
	s.entries = append(s.entries[:i], s.entries[i+1:]...)
	s.entries = append(s.entries, e)
	s.reindexLocked()
}

// evictLocked drops the lowest-scoring entry when over cap. Score decays with
// age so frequently reused prompts survive while stale one-offs go first.
// Must hold mu.
func (s *Store) evictLocked(now time.Time) {
	for len(s.entries) > s.max {
		worst := -1
		var worstScore float64
		for i := range s.entries {
			ageDays := math.Max(0, now.Sub(time.Unix(s.entries[i].LastSeen, 0)).Hours()/24)
			score := float64(s.entries[i].Count) * math.Exp2(-ageDays)
			if worst == -1 || score < worstScore {
				worst, worstScore = i, score
			}
		}
		if worst < 0 {
			return
		}
		delete(s.index, Normalize(s.entries[worst].Text))
		s.entries = append(s.entries[:worst], s.entries[worst+1:]...)
		s.reindexLocked()
	}
}

// reindexLocked rebuilds the key index after positions shift. Must hold mu.
func (s *Store) reindexLocked() {
	s.index = make(map[string]int, len(s.entries))
	for i := range s.entries {
		s.index[Normalize(s.entries[i].Text)] = i
	}
}

// Recall returns prompt texts oldest → newest, ready for composer up/down
// browsing (which reads the slice backwards from the end).
func (s *Store) Recall() []string {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := make([]string, len(s.entries))
	for i := range s.entries {
		out[i] = s.entries[i].Text
	}
	return out
}

// Entries returns a copy of all entries, oldest → newest.
func (s *Store) Entries() []Entry {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]Entry(nil), s.entries...)
}

// ── Normalization and similarity ─────────────────────────────────────────────

// Normalize canonicalizes text for comparison: trim, collapse every run of
// whitespace to a single space, lowercase.
func Normalize(text string) string {
	return strings.Join(strings.Fields(strings.ToLower(strings.TrimSpace(text))), " ")
}

// shingles returns the set of character n-grams of text.
func shingles(text string) map[string]struct{} {
	r := []rune(text)
	out := make(map[string]struct{}, len(r))
	for i := 0; i+shingleN <= len(r); i++ {
		out[string(r[i:i+shingleN])] = struct{}{}
	}
	if len(out) == 0 { // shorter than n — fall back to the whole text
		out[text] = struct{}{}
	}
	return out
}

// jaccard is |A∩B| / |A∪B| over two shingle sets.
func jaccard(a, b map[string]struct{}) float64 {
	if len(a) == 0 || len(b) == 0 {
		return 0
	}
	small, big := a, b
	if len(small) > len(big) {
		small, big = big, small
	}
	inter := 0
	for k := range small {
		if _, ok := big[k]; ok {
			inter++
		}
	}
	union := len(a) + len(b) - inter
	if union == 0 {
		return 0
	}
	return float64(inter) / float64(union)
}

// ── Persistence ──────────────────────────────────────────────────────────────

type fileV1 struct {
	Version int     `json:"version"`
	Entries []Entry `json:"entries"`
}

// DefaultPath returns the global history file location: <home>/.crude/history.json
// (or the beta data dir). Falls back to a relative path when home is unknown.
func DefaultPath() string {
	home, err := os.UserHomeDir()
	if err != nil {
		return "crude_history.json"
	}
	return filepath.Join(home, buildinfo.DataDir(), "history.json")
}

// LoadFile reads path, transparently migrating the legacy format (a plain
// JSON array of strings written by pre-history versions). Missing file yields
// an empty store.
func LoadFile(path string) (*Store, error) {
	s := New()
	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return s, nil
		}
		return nil, err
	}
	// Try current format first.
	var v1 fileV1
	if err := json.Unmarshal(data, &v1); err == nil && v1.Version == 1 && v1.Entries != nil {
		now := time.Now()
		base := now.Add(-time.Duration(len(v1.Entries)) * time.Second)
		for i, e := range v1.Entries {
			if strings.TrimSpace(e.Text) == "" || e.Count < 1 {
				continue
			}
			seen := e.LastSeen
			if seen <= 0 {
				seen = base.Add(time.Duration(i) * time.Second).Unix()
			}
			first := e.FirstSeen
			if first <= 0 {
				first = seen
			}
			s.entries = append(s.entries, Entry{Text: e.Text, Count: e.Count, FirstSeen: first, LastSeen: seen})
		}
		s.reindexLocked()
		s.evictLocked(now)
		return s, nil
	}
	// Legacy plain array: preserve order, counts unknown → 1. Timestamps are
	// staggered backwards from the file's mtime so migrated entries age
	// realistically for eviction scoring instead of looking brand new.
	var legacy []string
	if err := json.Unmarshal(data, &legacy); err != nil {
		return nil, err
	}
	base := fileMtime(path)
	for i, text := range legacy {
		if strings.TrimSpace(text) == "" {
			continue
		}
		ts := base.Add(-time.Duration(len(legacy)-i) * time.Second).Unix()
		s.entries = append(s.entries, Entry{Text: text, Count: 1, FirstSeen: ts, LastSeen: ts})
	}
	s.reindexLocked()
	s.evictLocked(time.Now())
	return s, nil
}

// fileMtime returns the modification time of path, or now if unavailable.
func fileMtime(path string) time.Time {
	if fi, err := os.Stat(path); err == nil {
		return fi.ModTime()
	}
	return time.Now()
}

// Save writes the store atomically (temp file + rename, owner-only perms).
// Entries are sorted oldest → newest as stored; LastSeen ordering is what
// matters downstream, not array order, but stable output keeps diffs sane.
func (s *Store) Save(path string) error {
	s.mu.Lock()
	entries := append([]Entry(nil), s.entries...)
	s.mu.Unlock()

	payload := fileV1{Version: 1, Entries: entries}
	data, err := json.MarshalIndent(payload, "", "  ")
	if err != nil {
		return err
	}
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, data, 0600); err != nil {
		return err
	}
	return os.Rename(tmp, path)
}
