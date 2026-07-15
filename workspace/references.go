package workspace

import (
	"database/sql"
	"encoding/base64"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"crude/provider"
	"crude/sessiondb"
)

type ReferenceStore struct {
	refs       map[string]sessiondb.Reference
	counter    map[sessiondb.Kind]int
	sessionID  string
	sessionDir string
	db         *sql.DB
}

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
		// Custom label collides with an existing active label — uniquify
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
		ID:        fmt.Sprintf("ref-%d-%d", time.Now().UnixNano(), idx),
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

	if ref.Kind == sessiondb.KindPastedTextFile && ref.FilePath == "" {
		path, err := s.writeReferenceFile(ref.Label, []byte(req.Content), ".txt")
		if err != nil {
			return sessiondb.Reference{}, err
		}
		ref.FilePath = path
		ref.Content = ""
	}

	if ref.Kind == sessiondb.KindPastedImage && ref.Content != "" {
		ext := provider.ImageExtFromMime(ref.Mime)
		b64 := ref.Content
		ref.Content = ""
		data, err := base64.StdEncoding.DecodeString(b64)
		if err != nil {
			return sessiondb.Reference{}, fmt.Errorf("decode base64 image: %w", err)
		}
		path, err := s.writeReferenceFile(ref.Label, data, ext)
		if err != nil {
			return sessiondb.Reference{}, err
		}
		ref.FilePath = path
	}

	s.refs[ref.ID] = ref
	if s.db != nil {
		_ = sessiondb.InsertPromptReference(s.db, ref)
	}
	return ref, nil
}

func (s *ReferenceStore) UpdateReferenceContent(id, content string) error {
	ref, ok := s.refs[id]
	if !ok {
		return nil
	}
	ref.Content = content
	s.refs[id] = ref
	if s.db != nil {
		return sessiondb.UpdatePromptReferenceContent(s.db, id, content)
	}
	return nil
}

func (s *ReferenceStore) DeleteReference(id string) error {
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

func (s *ReferenceStore) GetReference(id string) (sessiondb.Reference, bool) {
	ref, ok := s.refs[id]
	return ref, ok
}

func (s *ReferenceStore) ListReferences() []sessiondb.Reference {
	refs := make([]sessiondb.Reference, 0, len(s.refs))
	for _, ref := range s.refs {
		if ref.Active() {
			refs = append(refs, ref)
		}
	}
	return refs
}

func (s *ReferenceStore) AddExisting(ref sessiondb.Reference) {
	if s.refs == nil {
		s.refs = make(map[string]sessiondb.Reference)
	}

	// Migrate legacy pasted images: decode base64 to disk if Content is set
	// but FilePath is empty. Also migrate if both are set (Content should be
	// cleared to avoid DB bloat).
	if ref.Kind == sessiondb.KindPastedImage && ref.Content != "" {
		b64 := ref.Content
		ref.Content = ""
		data, err := base64.StdEncoding.DecodeString(b64)
		if err == nil {
			ext := provider.ImageExtFromMime(ref.Mime)
			path, err := s.writeReferenceFile(ref.Label, data, ext)
			if err == nil {
				ref.FilePath = path
				// Persist the migration so we don't re-migrate on next load
				if s.db != nil {
					_ = sessiondb.UpdatePromptReferenceFilePath(s.db, ref.ID, path)
				}
			}
		}
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
	if err := os.MkdirAll(dir, 0755); err != nil {
		return "", err
	}
	base := strings.Trim(label, "[]")
	base = strings.ReplaceAll(base, " ", "-")
	if base == "" {
		base = "reference"
	}
	path := filepath.Join(dir, fmt.Sprintf("%s-%d%s", base, time.Now().UnixNano(), ext))
	if err := os.WriteFile(path, data, 0644); err != nil {
		return "", err
	}
	return path, nil
}

func defaultReferenceLabel(kind sessiondb.Kind, idx int) string {
	switch kind {
	case sessiondb.KindPastedText:
		return fmt.Sprintf("[Pasted Text%d]", idx)
	case sessiondb.KindPastedTextFile:
		return fmt.Sprintf("[Pasted Text File%d]", idx)
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
