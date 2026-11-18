package remoteconn

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"testing"
	"time"
)

func privateTestDir(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	if err := os.Chmod(dir, 0700); err != nil {
		t.Fatal(err)
	}
	return dir
}

func TestStoreDurableConcurrentHandles(t *testing.T) {
	path := filepath.Join(privateTestDir(t), "private", "connections.json")
	a, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	b, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	var wg sync.WaitGroup
	for i := range 24 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			s := a
			if i%2 == 0 {
				s = b
			}
			if err := s.Save(t.Context(), Connection{ID: fmt.Sprint(i), Name: fmt.Sprint("Host ", i), Kind: "ssh", Host: "config-alias"}); err != nil {
				t.Error(err)
			}
		}()
	}
	wg.Wait()
	c, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	snap, err := c.Snapshot(t.Context())
	if err != nil || len(snap.Connections) != 24 {
		t.Fatalf("connections %d error %v", len(snap.Connections), err)
	}
	for i := range 25 {
		if err = c.RecordUse(t.Context(), "1", RecentWorkspace{Path: fmt.Sprint("/remote/", i), OpenedAt: time.Unix(int64(i+1), 0)}); err != nil {
			t.Fatal(err)
		}
	}
	if err = c.RecordUse(t.Context(), Local, RecentWorkspace{Path: "/local", OpenedAt: time.Now()}); err != nil {
		t.Fatal(err)
	}
	snap, err = c.Snapshot(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	if len(snap.Recent["1"]) != 20 || snap.Recent["1"][0].Path != "/remote/24" || snap.LastBackend != Local || len(snap.Recent[Local]) != 1 {
		t.Fatalf("bad recency %+v", snap)
	}
	snap.Connections[0].Name = "mutated"
	delete(snap.Recent, "1")
	fresh, _ := c.Snapshot(t.Context())
	if fresh.Connections[0].Name == "mutated" || len(fresh.Recent["1"]) != 20 {
		t.Fatal("snapshot aliases store")
	}
	if err = c.Remove(t.Context(), "1"); err != nil {
		t.Fatal(err)
	}
	fresh, _ = c.Snapshot(t.Context())
	if len(fresh.Connections) != 23 || len(fresh.Recent["1"]) != 0 {
		t.Fatal("remove left metadata")
	}
	fi, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if runtime.GOOS != "windows" && fi.Mode().Perm() != 0600 {
		t.Fatalf("mode %o", fi.Mode().Perm())
	}
}

func TestStoreRejectsUnsafeOrCorruptFile(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("POSIX permissions/symlinks")
	}
	for _, kind := range []string{"mode", "symlink", "version", "missing_version", "empty", "null", "unknown", "trailing"} {
		t.Run(kind, func(t *testing.T) {
			dir := privateTestDir(t)
			path := filepath.Join(dir, "connections.json")
			data := []byte(`{"version":1,"connections":[],"recent":{},"last_backend":"local"}`)
			switch kind {
			case "missing_version":
				data = []byte(`{}`)
			case "empty":
				data = []byte{}
			case "null":
				data = []byte(`null`)
			case "version":
				data = []byte(`{"version":2}`)
			case "unknown":
				data = []byte(`{"version":1,"secret":"DO-NOT-LEAK"}`)
			case "trailing":
				data = append(data, []byte(` {}`)...)
			}
			if err := os.WriteFile(path, data, 0600); err != nil {
				t.Fatal(err)
			}
			if kind == "mode" {
				if err := os.Chmod(path, 0644); err != nil {
					t.Fatal(err)
				}
			}
			if kind == "symlink" {
				link := filepath.Join(dir, "linked.json")
				if err := os.Symlink(path, link); err != nil {
					t.Fatal(err)
				}
				path = link
			}
			if _, err := Open(path); err == nil || strings.Contains(err.Error(), "DO-NOT-LEAK") {
				t.Fatalf("unsafe open %v", err)
			}
		})
	}
}
func TestStoreValidationAndCancelledWrite(t *testing.T) {
	s, err := Open(filepath.Join(privateTestDir(t), "connections.json"))
	if err != nil {
		t.Fatal(err)
	}
	for _, c := range []Connection{{ID: "local", Name: "bad", Kind: "ssh", Host: "host"}, {ID: "a", Name: "bad", Kind: "direct", Host: "host", Port: 443}, {ID: "a", Name: "bad", Kind: "ssh", Host: "user@host"}, {ID: "a", Name: "bad", Kind: "ssh", Host: "-option"}} {
		if s.Save(t.Context(), c) == nil {
			t.Fatalf("accepted %+v", c)
		}
	}
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	if err := s.Save(ctx, Connection{ID: "ok", Name: "ok", Kind: "ssh", Host: "host"}); !errors.Is(err, context.Canceled) {
		t.Fatal(err)
	}
	snap, _ := s.Snapshot(t.Context())
	if len(snap.Connections) != 0 {
		t.Fatal("cancelled write committed")
	}
}

type fakeKeyring struct {
	values map[string]string
	fail   bool
}

func (k *fakeKeyring) Get(s, a string) (string, error) {
	if k.fail {
		return "", errors.New("SECRET injected backend error")
	}
	v, ok := k.values[s+a]
	if !ok {
		return "", ErrNotFound
	}
	return v, nil
}
func (k *fakeKeyring) Set(s, a, v string) error {
	if k.fail {
		return errors.New("SECRET injected backend error")
	}
	if k.values == nil {
		k.values = map[string]string{}
	}
	k.values[s+a] = v
	return nil
}
func (k *fakeKeyring) Delete(s, a string) error {
	if k.fail {
		return errors.New("SECRET injected backend error")
	}
	delete(k.values, s+a)
	return nil
}

func TestSecretsKeychainAndExplicitFallback(t *testing.T) {
	for _, fallback := range []bool{false, true} {
		t.Run(fmt.Sprint(fallback), func(t *testing.T) {
			dir := privateTestDir(t)
			k := &fakeKeyring{fail: fallback}
			notices := 0
			s, err := OpenSecrets(dir, SecretOptions{Keyring: k, AllowFileFallback: fallback, Notice: func(s string) {
				notices++
				if strings.Contains(s, "device-secret") {
					t.Fatal("notice leaked credential")
				}
			}})
			if err != nil {
				t.Fatal(err)
			}
			if err = s.Set(t.Context(), "device", "device-secret"); err != nil {
				t.Fatal(err)
			}
			raw, err := os.ReadFile(filepath.Join(dir, "credentials.json"))
			if err != nil {
				t.Fatal(err)
			}
			if strings.Contains(string(raw), "device-secret") != fallback {
				t.Fatalf("wrong secret storage %s", raw)
			}
			got, err := s.Get(t.Context(), "device")
			if err != nil || got != "device-secret" {
				t.Fatalf("get %q %v", got, err)
			}
			if fallback && notices != 2 {
				t.Fatalf("fallback silently used: %d notices", notices)
			}
			if !fallback {
				k.fail = true
				if _, err = s.Get(t.Context(), "device"); err == nil || strings.Contains(err.Error(), "SECRET") {
					t.Fatal("keychain error leaked or downgrade permitted")
				}
				if err = s.Delete(t.Context(), "device"); err == nil {
					t.Fatal("hidden keychain deletion failure")
				}
				k.fail = false
			}
			if err = s.Delete(t.Context(), "device"); err != nil {
				t.Fatal(err)
			}
			if _, err = s.Get(t.Context(), "device"); !errors.Is(err, ErrNotFound) {
				t.Fatal(err)
			}
		})
	}
	for _, opts := range []SecretOptions{{}, {AllowFileFallback: true}, {Notice: func(string) {}}} {
		opts.Keyring = &fakeKeyring{fail: true}
		s, err := OpenSecrets(privateTestDir(t), opts)
		if err != nil {
			t.Fatal(err)
		}
		if err = s.Set(t.Context(), "id", "device-secret"); !errors.Is(err, ErrSecretUnavailable) {
			t.Fatalf("silent fallback %v", err)
		}
	}
}

func TestPrivateFilePreservesBytesOnMutationFailure(t *testing.T) {
	path := filepath.Join(privateTestDir(t), "private.json")
	f, err := OpenPrivateFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if err = f.Update(t.Context(), func([]byte) ([]byte, error) { return json.Marshal(map[string]int{"version": 1}) }); err != nil {
		t.Fatal(err)
	}
	before, _ := os.ReadFile(path)
	want := errors.New("save failed")
	if err = f.Update(t.Context(), func([]byte) ([]byte, error) { return []byte("replacement"), want }); !errors.Is(err, want) {
		t.Fatal(err)
	}
	after, _ := os.ReadFile(path)
	if string(after) != string(before) {
		t.Fatal("failed mutation changed bytes")
	}
}

func FuzzCurrentFormatDocuments(f *testing.F) {
	for _, seed := range []string{`{}`, `null`, `{"version":1,"connections":[],"recent":{},"last_backend":"local"}`, `{"version":1,"entries":{}}`} {
		f.Add([]byte(seed))
	}
	f.Fuzz(func(t *testing.T, raw []byte) {
		if len(raw) > maxDocumentBytes {
			t.Skip()
		}
		if snap, err := decodeSnapshot(raw); err == nil {
			encoded, err := json.Marshal(snap)
			if err != nil {
				t.Fatal(err)
			}
			if _, err = decodeSnapshot(encoded); err != nil {
				t.Fatal("accepted non-roundtrippable connection document")
			}
		}
		if secrets, err := decodeSecrets(raw); err == nil {
			encoded, err := json.Marshal(secrets)
			if err != nil {
				t.Fatal(err)
			}
			if _, err = decodeSecrets(encoded); err != nil {
				t.Fatal("accepted non-roundtrippable secret document")
			}
		}
	})
}
