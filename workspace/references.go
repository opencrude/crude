package workspace

import (
	"crude/history"
	"database/sql"
	"errors"
	"fmt"
	"math/rand/v2"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"

	"crude/provider"
	"crude/sessiondb"
)

type ReferenceStore struct {
	mu             sync.RWMutex
	creationMu     sync.Mutex
	reservedLabels map[string]bool
	refs           map[string]sessiondb.Reference
	counter        map[sessiondb.Kind]int
	sessionID      string
	sessionDir     string
	db             *sql.DB
}

// SessionReferences is a session's @reference store as consumed by shells and
// the core. The local implementation is ReferenceStore; a connected shell uses
// a replica whose reads are local and whose writes are core requests.
type SessionReferences interface {
	ReadReferenceContent(id string) (content string, readOnly bool, err error)
	SaveReferenceContent(expected sessiondb.Reference, original, content string) error
	CreateReference(req sessiondb.CreateRequest) (sessiondb.Reference, error)
	RestorePrompt(entry history.Entry) (string, []string, error)
	ImportReference(old sessiondb.Reference) (sessiondb.Reference, error)
	DeleteReference(id string) error
	RenameReference(id, label string) (string, bool)
	GetReference(id string) (sessiondb.Reference, bool)
	ListReferences() []sessiondb.Reference
	AddExisting(ref sessiondb.Reference)
}

var _ SessionReferences = (*ReferenceStore)(nil)

func NewReferenceStore(sessionID, sessionDir string, db *sql.DB) *ReferenceStore {
	return &ReferenceStore{
		refs:       make(map[string]sessiondb.Reference),
		counter:    make(map[sessiondb.Kind]int),
		sessionID:  sessionID,
		sessionDir: sessionDir,
		db:         db,
	}
}

func (s *ReferenceStore) CreateReference(req sessiondb.CreateRequest) (sessiondb.Reference, error) {
	// Serialize label allocation/publication without holding the projection
	// lock across disk or SQLite. Get/List must stay usable while a paste stores.
	s.creationMu.Lock()
	defer s.creationMu.Unlock()
	s.mu.Lock()
	if req.Kind == "" {
		req.Kind = sessiondb.KindPastedText
	}
	s.counter[req.Kind]++
	idx := s.counter[req.Kind]
	label := req.Label
	if label == "" {
		label = defaultReferenceLabel(req.Kind, idx)
		// Ensure label is unique among active refs
		for s.labelExists(label) {
			s.counter[req.Kind]++
			idx = s.counter[req.Kind]
			label = defaultReferenceLabel(req.Kind, idx)
		}
	} else if s.labelExists(label) {
		// Custom label collides with an existing active label; uniquify
		// by appending an incrementing numeric suffix.
		base := label[:len(label)-1] // strip trailing ]
		suffix := 1
		for {
			candidate := fmt.Sprintf("%s%d]", base, suffix)
			if !s.labelExists(candidate) {
				label = candidate
				break
			}
			suffix++
		}
	}
	ref := sessiondb.Reference{
		ID:        fmt.Sprintf("ref-%016x%016x", rand.Uint64(), rand.Uint64()),
		SessionID: s.sessionID,
		Kind:      req.Kind,
		Label:     label,
		Title:     req.Title,
		Mime:      req.Mime,
		Content:   req.Content,
		FilePath:  req.FilePath,
		Inline:    req.Inline,
		CreatedAt: time.Now().UnixNano(),
	}
	if s.reservedLabels == nil {
		s.reservedLabels = make(map[string]bool)
	}
	s.reservedLabels[label] = true
	s.mu.Unlock()
	defer func() {
		s.mu.Lock()
		delete(s.reservedLabels, label)
		s.mu.Unlock()
	}()

	if ref.Kind == sessiondb.KindPastedTextFile && ref.FilePath == "" {
		path, err := s.writeReferenceFile(ref.Label, []byte(req.Content), ".txt")
		if err != nil {
			return sessiondb.Reference{}, err
		}
		ref.FilePath = path
		ref.Content = ""
	}

	if ref.Kind == sessiondb.KindPastedImage && ref.Content != "" {
		return sessiondb.Reference{}, errors.New("image creation requires binary Data, not text Content")
	}
	if ref.Kind == sessiondb.KindPastedImage && len(req.Data) > 0 {
		ext := provider.ImageExtFromMime(ref.Mime)
		if err := provider.ValidateImageBytes(ref.Mime, req.Data); err != nil {
			return sessiondb.Reference{}, err
		}
		path, err := s.writeReferenceFile(ref.Label, req.Data, ext)
		if err != nil {
			return sessiondb.Reference{}, err
		}
		ref.FilePath = path
	}

	if s.db != nil {
		if err := sessiondb.InsertPromptReference(s.db, ref); err != nil {
			return sessiondb.Reference{}, err
		}
	}
	s.mu.Lock()
	s.refs[ref.ID] = ref
	s.mu.Unlock()
	return ref, nil
}

// RestorePrompt imports history attachments into this session's namespace.
// Call off the UI thread. Validate the entire draft before creating references;
// a missing attachment must never degrade into an apparently valid text prompt.
func (s *ReferenceStore) RestorePrompt(entry history.Entry) (string, []string, error) {
	runes := []rune(entry.Text)
	end := 0
	for _, attachment := range entry.Attachments {
		ref := attachment.Reference
		if attachment.Unavailable {
			return "", nil, fmt.Errorf("attachment %s could not be saved; reattach it before reusing this prompt", ref.DisplayName())
		}
		if attachment.Start < end || attachment.End <= attachment.Start || attachment.End > len(runes) || string(runes[attachment.Start:attachment.End]) != ref.DisplayName() {
			return "", nil, fmt.Errorf("invalid attachment span in prompt history")
		}
		if ref.FilePath != "" {
			info, err := os.Stat(ref.FilePath)
			if err != nil || !info.Mode().IsRegular() {
				return "", nil, fmt.Errorf("attachment %s is unavailable; the current draft was kept", ref.DisplayName())
			}
		}
		end = attachment.End
	}
	var text strings.Builder
	var ids []string
	imported := make(map[string]sessiondb.Reference)
	inProse := func(label string) bool {
		start := 0
		for _, attachment := range entry.Attachments {
			if strings.Contains(string(runes[start:attachment.Start]), label) {
				return true
			}
			start = attachment.End
		}
		return strings.Contains(string(runes[start:]), label)
	}
	end = 0
	for _, attachment := range entry.Attachments {
		old := attachment.Reference
		ref, ok := imported[old.ID]
		if !ok {
			var err error
			ref, err = s.ImportReference(old)
			if err == nil {
				imported[old.ID] = ref
				label := strings.TrimSuffix(ref.DisplayName(), "]")
				for suffix := 1; inProse(ref.DisplayName()); suffix++ {
					renamed, ok := s.RenameReference(ref.ID, fmt.Sprintf("%s (recalled %d)]", label, suffix))
					if !ok {
						err = fmt.Errorf("could not assign a safe attachment label")
						break
					}
					ref.Label = renamed
				}
			}
			if err != nil {
				for _, created := range imported {
					_ = s.DeleteReference(created.ID)
				}
				return "", nil, err
			}
			imported[old.ID] = ref
			ids = append(ids, ref.ID)
		}
		text.WriteString(string(runes[end:attachment.Start]))
		text.WriteString(ref.DisplayName())
		end = attachment.End
	}
	text.WriteString(string(runes[end:]))
	return text.String(), ids, nil
}

// ImportReference keeps recalled and externally viewed media session-owned.
// Neither eviction nor an external application's edits can mutate history.
func (s *ReferenceStore) ImportReference(old sessiondb.Reference) (sessiondb.Reference, error) {
	path := old.FilePath
	if path != "" {
		data, err := history.ReadAttachment(path)
		if err != nil {
			return sessiondb.Reference{}, err
		}
		path, err = s.writeReferenceFile("history", data, filepath.Ext(path))
		if err != nil {
			return sessiondb.Reference{}, err
		}
	}
	return s.CreateReference(sessiondb.CreateRequest{Kind: old.Kind, Title: old.Title, Mime: old.Mime, Content: old.Content, FilePath: path, Inline: old.Inline})
}

func (s *ReferenceStore) DeleteReference(id string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	ref, ok := s.refs[id]
	if !ok {
		return nil
	}
	ref.DeletedAt = time.Now().UnixNano()
	s.refs[id] = ref
	if s.db != nil {
		return sessiondb.DeletePromptReference(s.db, id)
	}
	return nil
}

// RenameReference relabels an active reference. The label is bracket-wrapped,
// made unique among the other active references (mirroring CreateReference's
// uniquifier), persisted when a DB backs the store, and reflected in the
// in-memory map. It returns the final label actually applied.
func (s *ReferenceStore) RenameReference(id, label string) (string, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	ref, ok := s.refs[id]
	if !ok || !ref.Active() {
		return "", false
	}
	label = normalizeReferenceLabel(label)
	if label == "" || s.reservedLabels[label] {
		return "", false
	}
	for otherID, other := range s.refs {
		if otherID == id || !other.Active() || other.Label != label {
			continue
		}
		base := strings.TrimSuffix(label, "]")
		suffix := 1
		for {
			candidate := fmt.Sprintf("%s %d]", base, suffix)
			conflict := false
			for thirdID, third := range s.refs {
				if thirdID != id && third.Active() && third.Label == candidate {
					conflict = true
					break
				}
			}
			if !conflict && !s.reservedLabels[candidate] {
				label = candidate
				break
			}
			suffix++
		}
		break
	}
	ref.Label = label
	if s.db != nil {
		if err := sessiondb.UpdatePromptReferenceLabel(s.db, id, label); err != nil {
			return "", false
		}
	}
	s.refs[id] = ref
	return label, true
}

// normalizeReferenceLabel wraps a bare name in reference brackets and strips
// surrounding whitespace. Empty inner text is rejected.
func normalizeReferenceLabel(label string) string {
	label = strings.TrimSpace(label)
	if label == "" {
		return ""
	}
	if !strings.HasPrefix(label, "[") {
		label = "[" + label
	}
	if !strings.HasSuffix(label, "]") {
		label += "]"
	}
	if strings.TrimSpace(strings.TrimSuffix(strings.TrimPrefix(label, "["), "]")) == "" {
		return ""
	}
	return label
}

func (s *ReferenceStore) GetReference(id string) (sessiondb.Reference, bool) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	ref, ok := s.refs[id]
	return ref, ok
}

func (s *ReferenceStore) ListReferences() []sessiondb.Reference {
	s.mu.RLock()
	defer s.mu.RUnlock()
	refs := make([]sessiondb.Reference, 0, len(s.refs))
	for _, ref := range s.refs {
		if ref.Active() {
			refs = append(refs, ref)
		}
	}
	return refs
}

func (s *ReferenceStore) AddExisting(ref sessiondb.Reference) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.refs == nil {
		s.refs = make(map[string]sessiondb.Reference)
	}

	s.refs[ref.ID] = ref

	// Update counter to avoid label collision on next creation
	if idx := extractLabelIndex(ref.Label); idx > s.counter[ref.Kind] {
		s.counter[ref.Kind] = idx
	}
}

func (s *ReferenceStore) writeReferenceFile(label string, data []byte, ext string) (string, error) {
	dir := filepath.Join(s.sessionDir, "references")
	if dir == "" {
		dir = filepath.Join(os.TempDir(), "crude-references")
	}
	if err := os.MkdirAll(dir, 0700); err != nil {
		return "", err
	}
	base := strings.Trim(label, "[]")
	base = strings.ReplaceAll(base, " ", "-")
	if base == "" {
		base = "reference"
	}
	file, err := os.CreateTemp(dir, base+"-*"+ext)
	if err != nil {
		return "", err
	}
	_, writeErr := file.Write(data)
	closeErr := file.Close()
	if err := errors.Join(writeErr, closeErr); err != nil {
		_ = os.Remove(file.Name())
		return "", err
	}
	return file.Name(), nil
}

func defaultReferenceLabel(kind sessiondb.Kind, idx int) string {
	switch kind {
	case sessiondb.KindMCPTool:
		return fmt.Sprintf("[Tool%d]", idx)
	case sessiondb.KindMCPContext:
		return fmt.Sprintf("[MCP Context%d]", idx)
	case sessiondb.KindPastedText, sessiondb.KindPastedTextFile:
		return fmt.Sprintf("[Pasted Text%d]", idx)
	case sessiondb.KindPastedImage:
		return fmt.Sprintf("[Pasted IMG%d]", idx)
	case sessiondb.KindSelectedFile:
		return fmt.Sprintf("[File%d]", idx)
	default:
		return fmt.Sprintf("[Reference%d]", idx)
	}
}

// labelExists checks if any active reference already has this label.
func (s *ReferenceStore) labelExists(label string) bool {
	if s.reservedLabels[label] {
		return true
	}
	for _, ref := range s.refs {
		if ref.Active() && ref.Label == label {
			return true
		}
	}
	return false
}

// extractLabelIndex extracts the numeric suffix from a reference label.
// Generated labels have the format "[Pasted Text1]", "[File2]", "[Pasted IMG1]",
// "[Pasted Text File3]", "[Reference4]" where the number is attached to the
// last word before the closing bracket. Returns 0 if no numeric suffix is found.
func extractLabelIndex(label string) int {
	// Strip surrounding brackets
	s := strings.Trim(label, "[]")
	if s == "" {
		return 0
	}
	// Find the trailing numeric portion
	i := len(s) - 1
	for i >= 0 && s[i] >= '0' && s[i] <= '9' {
		i--
	}
	if i == len(s)-1 {
		return 0 // no trailing digits
	}
	idx, err := strconv.Atoi(s[i+1:])
	if err != nil {
		return 0
	}
	return idx
}
