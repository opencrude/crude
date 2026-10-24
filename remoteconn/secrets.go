package remoteconn

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"path/filepath"

	"github.com/zalando/go-keyring"
)

var ErrSecretUnavailable = errors.New("remoteconn: credential storage unavailable")

// Keyring enables isolated tests; nil selects the platform OS keychain.
// Implementations must map absent items to ErrNotFound and never log values.
type Keyring interface {
	Get(service, account string) (string, error)
	Set(service, account, value string) error
	Delete(service, account string) error
}
type systemKeyring struct{}

func (systemKeyring) Get(s, a string) (string, error) {
	v, e := keyring.Get(s, a)
	if errors.Is(e, keyring.ErrNotFound) {
		e = ErrNotFound
	}
	return v, e
}
func (systemKeyring) Set(s, a, v string) error { return keyring.Set(s, a, v) }
func (systemKeyring) Delete(s, a string) error {
	e := keyring.Delete(s, a)
	if errors.Is(e, keyring.ErrNotFound) {
		return nil
	}
	return e
}

type SecretOptions struct {
	Keyring Keyring
	// Fallback is explicit consent, not automatic policy. Notice must surface a
	// persistent user-visible notice; both are required before writing plaintext.
	AllowFileFallback bool
	Notice            func(string)
}
type Secrets struct {
	file    *PrivateFile
	keyring Keyring
	service string
	options SecretOptions
}
type secretEntry struct {
	Backend string `json:"backend"`
	Value   string `json:"value,omitempty"`
}
type secretDocument struct {
	Version int                    `json:"version"`
	Entries map[string]secretEntry `json:"entries"`
}

func (s secretEntry) String() string   { return "[credential redacted]" }
func (s secretEntry) GoString() string { return s.String() }

// OpenSecrets namespaces OS credentials by the exact client profile directory.
// Use the same private directory as connections.json; no SSH password API exists.
func OpenSecrets(dir string, opts SecretOptions) (*Secrets, error) {
	abs, err := filepath.Abs(dir)
	if err != nil {
		return nil, err
	}
	f, err := OpenPrivateFile(filepath.Join(abs, "credentials.json"))
	if err != nil {
		return nil, err
	}
	k := opts.Keyring
	if k == nil {
		k = systemKeyring{}
	}
	sum := sha256.Sum256([]byte(abs))
	return &Secrets{file: f, keyring: k, service: "crude-remote/" + hex.EncodeToString(sum[:]), options: opts}, nil
}
func decodeSecrets(raw []byte) (secretDocument, error) {
	if raw == nil {
		return secretDocument{Version: 1, Entries: map[string]secretEntry{}}, nil
	}
	var d secretDocument
	if err := DecodeDocument(raw, &d); err != nil {
		return d, err
	}
	if err := documentVersion(d.Version); err != nil {
		return d, err
	}
	if d.Entries == nil {
		return d, ErrSecretUnavailable
	}
	for id, e := range d.Entries {
		if !cleanText(id, 128, true) || (e.Backend != "keychain" && e.Backend != "file") || (e.Backend == "keychain" && e.Value != "") {
			return d, ErrSecretUnavailable
		}
	}
	return d, nil
}
func (s *Secrets) notice() error {
	if !s.options.AllowFileFallback || s.options.Notice == nil {
		return ErrSecretUnavailable
	}
	s.options.Notice("OS keychain unavailable: remote device credentials are stored unencrypted in a private 0600 file on this client.")
	return nil
}
func (s *Secrets) Set(ctx context.Context, id, value string) error {
	if !cleanText(id, 128, true) || !cleanText(value, 4096, true) {
		return ErrSecretUnavailable
	}
	return s.file.Update(ctx, func(raw []byte) ([]byte, error) {
		d, err := decodeSecrets(raw)
		if err != nil {
			return nil, err
		}
		e := d.Entries[id]
		if e.Backend == "file" {
			if err = s.notice(); err != nil {
				return nil, err
			}
			e.Value = value
		} else if err = s.keyring.Set(s.service, id, value); err == nil {
			e = secretEntry{Backend: "keychain"}
		} else {
			if e.Backend == "keychain" {
				return nil, ErrSecretUnavailable
			}
			if err = s.notice(); err != nil {
				return nil, err
			}
			e = secretEntry{Backend: "file", Value: value}
		}
		d.Entries[id] = e
		return json.Marshal(d)
	})
}
func (s *Secrets) Get(ctx context.Context, id string) (string, error) {
	var value string
	err := s.file.Update(ctx, func(raw []byte) ([]byte, error) {
		d, err := decodeSecrets(raw)
		if err != nil {
			return nil, err
		}
		e, ok := d.Entries[id]
		if !ok {
			return nil, ErrNotFound
		}
		if e.Backend == "file" {
			if err = s.notice(); err != nil {
				return nil, err
			}
			value = e.Value
			return nil, nil
		}
		value, err = s.keyring.Get(s.service, id)
		if err != nil {
			value = ""
			return nil, ErrSecretUnavailable
		}
		return nil, nil
	})
	return value, err
}
func (s *Secrets) Delete(ctx context.Context, id string) error {
	return s.file.Update(ctx, func(raw []byte) ([]byte, error) {
		d, err := decodeSecrets(raw)
		if err != nil {
			return nil, err
		}
		e, ok := d.Entries[id]
		if !ok {
			return nil, nil
		}
		if e.Backend == "keychain" {
			if err = s.keyring.Delete(s.service, id); err != nil && !errors.Is(err, ErrNotFound) {
				return nil, ErrSecretUnavailable
			}
		}
		delete(d.Entries, id)
		return json.Marshal(d)
	})
}
