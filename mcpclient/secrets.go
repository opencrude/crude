package mcpclient

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"sync"

	"crude/fileio"
)

// Secret store: credentials a user (or an agent acting for them through self
// control) gives Crude for MCP connections. Values live in one owner-only file
// in the application credential vault, never in settings, never on the wire to
// a shell, and are referenced from connections as secret:NAME.

const secretsFile = "secrets.json"

var secretName = regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_.-]{0,127}$`)

// secretsMu serializes read-modify-write of the vault file across every
// manager that shares it (per-worktree execution clients included).
var secretsMu sync.Mutex

// ValidSecretName reports whether name can be stored and referenced.
func ValidSecretName(name string) bool { return secretName.MatchString(name) }

func (manager *Manager) secretsPath() string {
	if manager.credentialDir == "" {
		return ""
	}
	return filepath.Join(manager.credentialDir, secretsFile)
}

func readSecrets(path string) (map[string]string, error) {
	values := make(map[string]string)
	if path == "" {
		return values, nil
	}
	data, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return values, nil
	}
	if err != nil {
		return nil, fmt.Errorf("read secret store: %w", err)
	}
	if err := json.Unmarshal(data, &values); err != nil {
		return nil, fmt.Errorf("secret store is unreadable: %w", err)
	}
	return values, nil
}

// SetSecrets stores or (with an empty value) removes named secrets in one
// atomic replacement of the vault file.
func (manager *Manager) SetSecrets(updates map[string]string) error {
	path := manager.secretsPath()
	if path == "" {
		return errors.New("application credential vault is unavailable")
	}
	for name, value := range updates {
		if !ValidSecretName(name) {
			return fmt.Errorf("secret name %q must start with a letter or underscore and use letters, digits, '_', '.' or '-'", name)
		}
		if strings.ContainsRune(value, 0) || len(value) > 64<<10 {
			return fmt.Errorf("secret %s is oversized or contains NUL", name)
		}
	}
	secretsMu.Lock()
	defer secretsMu.Unlock()
	values, err := readSecrets(path)
	if err != nil {
		return err
	}
	for name, value := range updates {
		if value = strings.TrimSpace(value); value == "" {
			delete(values, name)
		} else {
			values[name] = value
		}
	}
	if err := os.MkdirAll(manager.credentialDir, 0o700); err != nil {
		return err
	}
	encoded, err := json.Marshal(values)
	if err != nil {
		return err
	}
	return fileio.WriteAtomicChecked(path, encoded, 0o600, nil)
}

// SecretNames lists stored secret names; values never leave the core.
func (manager *Manager) SecretNames() []string {
	secretsMu.Lock()
	values, err := readSecrets(manager.secretsPath())
	secretsMu.Unlock()
	if err != nil {
		return nil
	}
	names := make([]string, 0, len(values))
	for name := range values {
		names = append(names, name)
	}
	sort.Strings(names)
	return names
}

func (manager *Manager) storedSecret(name string) (string, bool) {
	secretsMu.Lock()
	values, err := readSecrets(manager.secretsPath())
	secretsMu.Unlock()
	if err != nil {
		return "", false
	}
	value, exists := values[name]
	return value, exists && value != ""
}

// resolveSecret resolves a credential reference: env:NAME reads the core's
// environment, secret:NAME reads Crude's secret store.
func (manager *Manager) resolveSecret(reference string) (string, error) {
	if name, found := strings.CutPrefix(reference, "secret:"); found && name != "" {
		if value, exists := manager.storedSecret(name); exists {
			return value, nil
		}
		return "", fmt.Errorf("secret %q is not set; store it in Settings → MCP or through Crude self control", name)
	}
	if name, found := strings.CutPrefix(reference, "env:"); found && name != "" {
		value, exists := os.LookupEnv(name)
		if !exists || value == "" {
			return "", fmt.Errorf("credential reference %q is unavailable", reference)
		}
		return value, nil
	}
	return "", errors.New("credential reference must be secret:NAME (Crude secret store) or env:NAME (environment variable)")
}
