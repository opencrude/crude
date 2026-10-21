package projectpath

import (
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	"golang.org/x/sys/unix"
)

const userDirsLimit = 64 * 1024

var xdgDefaults = []struct{ key, name string }{
	{"XDG_DESKTOP_DIR", "Desktop"}, {"XDG_DOCUMENTS_DIR", "Documents"},
	{"XDG_DOWNLOAD_DIR", "Downloads"}, {"XDG_MUSIC_DIR", "Music"},
	{"XDG_PICTURES_DIR", "Pictures"}, {"XDG_VIDEOS_DIR", "Videos"},
	{"XDG_PUBLICSHARE_DIR", "Public"}, {"XDG_TEMPLATES_DIR", "Templates"},
}

func personalDirectories(home string) ([]string, error) {
	config := os.Getenv("XDG_CONFIG_HOME")
	if !filepath.IsAbs(config) {
		config = ""
		if home != "" {
			config = filepath.Join(home, ".config")
		}
	}
	paths := make(map[string]string, len(xdgDefaults))
	for _, item := range xdgDefaults {
		paths[item.key] = ""
		if home != "" {
			paths[item.key] = filepath.Join(home, item.name)
		}
	}
	if config == "" {
		// Inspect reports the unavailable home. Never guess relative defaults or
		// read a configuration file from the current project instead.
		return nil, nil
	}
	// NONBLOCK prevents a malformed FIFO configuration from hanging startup.
	file, err := os.OpenFile(filepath.Join(config, "user-dirs.dirs"), os.O_RDONLY|unix.O_NONBLOCK, 0)
	if os.IsNotExist(err) {
		return xdgPaths(paths), nil
	}
	if err != nil {
		return xdgPaths(paths), fmt.Errorf("read XDG personal directories: %w", err)
	}
	defer func() { _ = file.Close() }()
	info, err := file.Stat()
	if err != nil {
		return xdgPaths(paths), fmt.Errorf("stat XDG personal directories: %w", err)
	}
	if !info.Mode().IsRegular() {
		return xdgPaths(paths), fmt.Errorf("XDG personal directories is not a regular file")
	}
	data, err := io.ReadAll(io.LimitReader(file, userDirsLimit+1))
	if err != nil {
		return xdgPaths(paths), fmt.Errorf("read XDG personal directories: %w", err)
	}
	if len(data) > userDirsLimit {
		return xdgPaths(paths), fmt.Errorf("XDG personal directories exceeds %d bytes", userDirsLimit)
	}
	var issues []error
	for i, line := range strings.Split(string(data), "\n") {
		key, value, ok := strings.Cut(strings.TrimSpace(line), "=")
		key = strings.TrimSpace(key)
		if !ok {
			if _, known := paths[key]; known {
				issues = append(issues, fmt.Errorf("XDG personal directories line %d: expected an assignment", i+1))
			}
			continue
		}
		if _, known := paths[key]; !known {
			continue
		}
		parsed, err := xdgValue(strings.TrimSpace(value), home)
		if err != nil {
			issues = append(issues, fmt.Errorf("XDG personal directories line %d: %w", i+1, err))
			continue
		}
		paths[key] = parsed
	}
	return xdgPaths(paths), errors.Join(issues...)
}

func xdgPaths(paths map[string]string) []string {
	result := make([]string, 0, len(xdgDefaults))
	for _, item := range xdgDefaults {
		if path := paths[item.key]; path != "" {
			result = append(result, path)
		}
	}
	return result
}

// xdgValue parses the restricted shell-style format without running a shell or
// expanding arbitrary variables, commands, backticks or environment content.
func xdgValue(value, home string) (string, error) {
	if !strings.HasPrefix(value, `"`) {
		return "", fmt.Errorf("expected a double-quoted absolute path")
	}
	var out strings.Builder
	for i := 1; i < len(value); i++ {
		switch value[i] {
		case '"':
			tail := strings.TrimSpace(value[i+1:])
			if tail != "" && !strings.HasPrefix(tail, "#") {
				return "", fmt.Errorf("unexpected content after path")
			}
			if !filepath.IsAbs(out.String()) {
				return "", fmt.Errorf("personal directory is not absolute")
			}
			return filepath.Clean(out.String()), nil
		case '\\':
			if i+1 >= len(value) {
				return "", fmt.Errorf("incomplete escape")
			}
			i++
			if !strings.ContainsRune("$`\"\\", rune(value[i])) {
				out.WriteByte('\\')
			}
			out.WriteByte(value[i])
		case '$':
			rest := value[i:]
			if home == "" {
				return "", fmt.Errorf("personal home is unavailable for expansion")
			}
			if strings.HasPrefix(rest, "${HOME}") {
				out.WriteString(home)
				i += len("${HOME}") - 1
			} else if strings.HasPrefix(rest, "$HOME") && (len(rest) == 5 || rest[5] == '/' || rest[5] == '"') {
				out.WriteString(home)
				i += len("$HOME") - 1
			} else {
				return "", fmt.Errorf("unsupported expansion in personal directory")
			}
		case '`':
			return "", fmt.Errorf("command expansion is not supported")
		default:
			out.WriteByte(value[i])
		}
	}
	return "", fmt.Errorf("unterminated personal directory path")
}
