package viewmodel

import (
	"database/sql"
	"encoding/base64"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"crude/promptref"
	"crude/sessiondb"
)

type referenceStore struct {
	refs       map[string]promptref.Reference
	counter    map[promptref.Kind]int
	sessionID  string
	sessionDir string
	db         *sql.DB
}

func newReferenceStore(sessionID, sessionDir string, db *sql.DB) *referenceStore {
	return &referenceStore{
		refs:       make(map[string]promptref.Reference),
		counter:    make(map[promptref.Kind]int),
		sessionID:  sessionID,
		sessionDir: sessionDir,
		db:         db,
	}
}

func (s *referenceStore) CreateReference(req promptref.CreateRequest) (promptref.Reference, error) {
	if req.Kind == "" {
		req.Kind = promptref.KindPastedText
	}
	s.counter[req.Kind]++
	idx := s.counter[req.Kind]
	label := req.Label
	if label == "" {
		label = defaultReferenceLabel(req.Kind, idx)
	}
	ref := promptref.Reference{
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

	if ref.Kind == promptref.KindPastedTextFile && ref.FilePath == "" {
		path, err := s.writeReferenceFile(ref.Label, req.Content, ".txt")
		if err != nil {
			return promptref.Reference{}, err
		}
		ref.FilePath = path
		ref.Content = ""
	}

	if ref.Kind == promptref.KindPastedImage && ref.FilePath == "" && ref.Content != "" {
		ext := ".png"
		if strings.Contains(ref.Mime, "jpeg") || strings.Contains(ref.Mime, "jpg") {
			ext = ".jpg"
		} else if strings.Contains(ref.Mime, "gif") {
			ext = ".gif"
		} else if strings.Contains(ref.Mime, "webp") {
			ext = ".webp"
		}
		b64 := ref.Content
		ref.Content = ""
		data, err := base64.StdEncoding.DecodeString(b64)
		if err != nil {
			return promptref.Reference{}, fmt.Errorf("decode base64 image: %w", err)
		}
		path, err := s.writeReferenceBinary(ref.Label, data, ext)
		if err != nil {
			return promptref.Reference{}, err
		}
		ref.FilePath = path
	}

	s.refs[ref.ID] = ref
	if s.db != nil {
		_ = sessiondb.InsertPromptReference(s.db, ref)
	}
	return ref, nil
}

func (s *referenceStore) UpdateReferenceContent(id, content string) error {
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

func (s *referenceStore) DeleteReference(id string) error {
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

func (s *referenceStore) GetReference(id string) (promptref.Reference, bool) {
	ref, ok := s.refs[id]
	return ref, ok
}

func (s *referenceStore) ListReferences() []promptref.Reference {
	refs := make([]promptref.Reference, 0, len(s.refs))
	for _, ref := range s.refs {
		refs = append(refs, ref)
	}
	return refs
}

func (s *referenceStore) AddExisting(ref promptref.Reference) {
	if s.refs == nil {
		s.refs = make(map[string]promptref.Reference)
	}
	s.refs[ref.ID] = ref
}

func (s *referenceStore) writeReferenceFile(label, content, ext string) (string, error) {
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
	if err := os.WriteFile(path, []byte(content), 0644); err != nil {
		return "", err
	}
	return path, nil
}

func (s *referenceStore) writeReferenceBinary(label string, data []byte, ext string) (string, error) {
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

func defaultReferenceLabel(kind promptref.Kind, idx int) string {
	switch kind {
	case promptref.KindPastedText:
		return fmt.Sprintf("[Pasted Text%d]", idx)
	case promptref.KindPastedTextFile:
		return fmt.Sprintf("[Pasted Text File%d]", idx)
	case promptref.KindPastedImage:
		return fmt.Sprintf("[Pasted IMG%d]", idx)
	case promptref.KindSelectedFile:
		return fmt.Sprintf("[File%d]", idx)
	default:
		return fmt.Sprintf("[Reference%d]", idx)
	}
}

func (m *App) persistReference(ref promptref.Reference) {
	if m.refStore != nil && m.refStore.db != nil {
		if err := sessiondb.InsertPromptReference(m.refStore.db, ref); err != nil {
			m.logger.Warn("REF", "persist_failed", "error", err.Error())
		}
	}
}
