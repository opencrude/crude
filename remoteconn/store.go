package remoteconn

import (
	"context"
	"encoding/hex"
	"encoding/json"
	"errors"
	"net"
	"path/filepath"
	"slices"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"

	"crude/buildinfo"
)

const Local = "local"

var ErrNotFound = errors.New("remoteconn: connection not found")

type Connection struct {
	ID           string `json:"id"`
	Name         string `json:"name"`
	Kind         string `json:"kind"` // ssh or direct
	Host         string `json:"host"` // SSH config alias or direct DNS/IP (no URI/userinfo)
	User         string `json:"user,omitempty"`
	Port         int    `json:"port,omitempty"` // zero preserves SSH config's choice
	IdentityFile string `json:"identity_file,omitempty"`
	Fingerprint  string `json:"fingerprint,omitempty"` // required for direct
	Carrier      string `json:"carrier,omitempty"`     // auto, ssh, quic, tls
}

type RecentWorkspace struct {
	Path     string    `json:"path"`
	Name     string    `json:"name"`
	OpenedAt time.Time `json:"opened_at"`
}

type Snapshot struct {
	Version     int                          `json:"version"`
	Connections []Connection                 `json:"connections"`
	Recent      map[string][]RecentWorkspace `json:"recent"`
	LastBackend string                       `json:"last_backend"`
}

type Store struct{ file *PrivateFile }

// DefaultPath is beside the clientstate database, never on the selected core.
func DefaultPath() (string, error) {
	home, err := buildinfo.Home()
	if err != nil {
		return "", err
	}
	return filepath.Join(home, buildinfo.DataDir(), "remotes", "connections.json"), nil
}

func Open(path string) (*Store, error) {
	f, err := OpenPrivateFile(path)
	if err != nil {
		return nil, err
	}
	s := &Store{file: f}
	_, err = s.Snapshot(context.Background())
	return s, err
}

func cleanText(s string, max int, required bool) bool {
	if len(s) > max || !utf8.ValidString(s) || (required && strings.TrimSpace(s) == "") {
		return false
	}
	return !strings.ContainsFunc(s, unicode.IsControl)
}

func ValidFingerprint(s string) bool {
	b, err := hex.DecodeString(s)
	return err == nil && len(b) == 32 && s == strings.ToLower(s)
}

func (c Connection) Validate() error {
	if !cleanText(c.ID, 128, true) || c.ID == Local || strings.ContainsAny(c.ID, "/\\") || !cleanText(c.Name, 256, true) || !cleanText(c.Host, 253, true) || strings.ContainsAny(c.Host, " \t/@?#") || strings.HasPrefix(c.Host, "-") || !cleanText(c.User, 256, false) || strings.HasPrefix(c.User, "-") || !cleanText(c.IdentityFile, 4096, false) || c.Port < 0 || c.Port > 65535 {
		return errors.New("remoteconn: invalid connection")
	}
	if c.Carrier != "" && c.Carrier != "auto" && c.Carrier != "ssh" && c.Carrier != "quic" && c.Carrier != "tls" {
		return errors.New("remoteconn: invalid carrier")
	}
	switch c.Kind {
	case "direct":
		if c.Port == 0 || c.User != "" || c.IdentityFile != "" || c.Carrier == "ssh" || !ValidFingerprint(c.Fingerprint) {
			return errors.New("remoteconn: invalid direct connection")
		}
		if strings.Contains(c.Host, ":") && net.ParseIP(c.Host) == nil {
			return errors.New("remoteconn: invalid direct host")
		}
	case "ssh":
		if c.Fingerprint != "" && !ValidFingerprint(c.Fingerprint) {
			return errors.New("remoteconn: invalid fingerprint")
		}
	default:
		return errors.New("remoteconn: invalid connection kind")
	}
	return nil
}

func decodeSnapshot(raw []byte) (Snapshot, error) {
	if raw == nil {
		return Snapshot{Version: 1, Recent: map[string][]RecentWorkspace{}, LastBackend: Local}, nil
	}
	var s Snapshot
	if err := DecodeDocument(raw, &s); err != nil {
		return Snapshot{}, err
	}
	if err := documentVersion(s.Version); err != nil {
		return Snapshot{}, err
	}
	seen := map[string]bool{Local: true}
	for _, c := range s.Connections {
		if c.Validate() != nil || seen[c.ID] {
			return Snapshot{}, errors.New("remoteconn: invalid saved connections")
		}
		seen[c.ID] = true
	}
	if !seen[s.LastBackend] {
		return Snapshot{}, errors.New("remoteconn: unknown saved backend")
	}
	for id, recent := range s.Recent {
		if !seen[id] || len(recent) > 20 {
			return Snapshot{}, errors.New("remoteconn: invalid workspace recency")
		}
		for _, w := range recent {
			if !cleanText(w.Path, 4096, true) || !cleanText(w.Name, 256, false) {
				return Snapshot{}, errors.New("remoteconn: invalid workspace recency")
			}
		}
	}
	if s.Recent == nil {
		s.Recent = map[string][]RecentWorkspace{}
	}
	return s, nil
}

func (s *Store) Snapshot(ctx context.Context) (Snapshot, error) {
	var out Snapshot
	err := s.file.Update(ctx, func(raw []byte) ([]byte, error) { var err error; out, err = decodeSnapshot(raw); return nil, err })
	return out, err
}

func (s *Store) update(ctx context.Context, fn func(*Snapshot) error) error {
	return s.file.Update(ctx, func(raw []byte) ([]byte, error) {
		snap, err := decodeSnapshot(raw)
		if err != nil {
			return nil, err
		}
		if err = fn(&snap); err != nil {
			return nil, err
		}
		return json.Marshal(snap)
	})
}

func (s *Store) Save(ctx context.Context, c Connection) error {
	if err := c.Validate(); err != nil {
		return err
	}
	return s.update(ctx, func(s *Snapshot) error {
		for i := range s.Connections {
			if s.Connections[i].ID == c.ID {
				s.Connections[i] = c
				return nil
			}
		}
		if len(s.Connections) >= 256 {
			return errors.New("remoteconn: connection limit reached")
		}
		s.Connections = append(s.Connections, c)
		return nil
	})
}

// Remove removes metadata only. The host must delete the credential first;
// secret deletion failure must leave the visible connection available to retry.
func (s *Store) Remove(ctx context.Context, id string) error {
	return s.update(ctx, func(s *Snapshot) error {
		i := slices.IndexFunc(s.Connections, func(c Connection) bool { return c.ID == id })
		if i < 0 {
			return ErrNotFound
		}
		s.Connections = slices.Delete(s.Connections, i, i+1)
		delete(s.Recent, id)
		if s.LastBackend == id {
			s.LastBackend = Local
		}
		return nil
	})
}

// RecordUse is called only after successful destination attachment, never on a
// connect attempt. The path is opaque and belongs to that backend's filesystem.
func (s *Store) RecordUse(ctx context.Context, id string, w RecentWorkspace) error {
	if !cleanText(w.Path, 4096, true) || !cleanText(w.Name, 256, false) || w.OpenedAt.IsZero() {
		return errors.New("remoteconn: invalid recent workspace")
	}
	return s.update(ctx, func(s *Snapshot) error {
		if id != Local && !slices.ContainsFunc(s.Connections, func(c Connection) bool { return c.ID == id }) {
			return ErrNotFound
		}
		recent := slices.DeleteFunc(s.Recent[id], func(old RecentWorkspace) bool { return old.Path == w.Path })
		recent = append(recent, w)
		slices.SortStableFunc(recent, func(a, b RecentWorkspace) int { return b.OpenedAt.Compare(a.OpenedAt) })
		if len(recent) > 20 {
			recent = recent[:20]
		}
		s.Recent[id] = recent
		s.LastBackend = id
		return nil
	})
}
